// Commande demo : rejoue une conversation complète avec un modèle scripté
// (aucun LLM, aucune clé d'API) et le calendrier mock, en affichant chaque
// appel de tool. Utile pour visualiser la séparation des responsabilités.
//
//	go run ./cmd/demo -tz Europe/Paris [-now 2026-09-30T10:00:00+02:00]
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"skills/agent"
	"skills/model"
	"skills/model/scripted"
	"skills/services/calendar"
	priserdv "skills/skills/prise-de-rendez-vous"
	"skills/types"
)

func main() {
	tz := flag.String("tz", "", "fuseau horaire IANA de l'utilisateur (obligatoire), ex: Europe/Paris")
	nowFlag := flag.String("now", "", "instant simulé au format RFC 3339 (défaut : maintenant)")
	flag.Parse()
	if err := run(*tz, *nowFlag); err != nil {
		fmt.Fprintln(os.Stderr, "erreur:", err)
		os.Exit(1)
	}
}

func run(tz, nowFlag string) error {
	if tz == "" {
		return errors.New("-tz est obligatoire : le fuseau n'est jamais supposé")
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return err
	}
	now := time.Now().In(loc)
	if nowFlag != "" {
		if now, err = time.Parse(time.RFC3339, nowFlag); err != nil {
			return fmt.Errorf("-now: %w", err)
		}
	}
	clock := func() time.Time { return now }

	// 1. Service métier (source de vérité).
	cal, err := calendar.NewDemoProvider(loc, clock)
	if err != nil {
		return err
	}
	// 2. Skill : instructions + tools, branché sur le calendrier.
	skill, err := priserdv.New(priserdv.Config{Provider: cal})
	if err != nil {
		return err
	}
	// 3. Modèle : ici scripté ; remplaçable par n'importe quel model.Adapter.
	m := scripted.New()
	// 4. Runtime.
	rt, err := agent.New(m, agent.WithClock(clock))
	if err != nil {
		return err
	}
	if err := rt.RegisterSkill(skill); err != nil {
		return err
	}
	session, err := agent.NewSession("demo", tz, types.UserContext{ClientID: "c-42", Name: "Camille Durand"})
	if err != nil {
		return err
	}

	turns := []struct {
		user  string
		steps []scripted.Step
	}{
		{"Je voudrais un rendez-vous jeudi après-midi.", []scripted.Step{
			scripted.Call(agent.ActivateSkillTool, map[string]any{"nom": priserdv.Name}),
			scripted.Call("interpreter_date", map[string]any{"expression": "jeudi après-midi"}),
			searchLastPeriod(nil),
			presentSlots(),
		}},
		{"Je prends 15h30.", []scripted.Step{
			scripted.CallFn("reserver_creneau", func(req model.Request) (any, error) {
				id, err := slotAt(req, "15:30")
				return map[string]any{"slot_id": id, "type_rendez_vous": "consultation"}, err
			}),
			confirm("reserver_creneau", "Votre rendez-vous est confirmé : %s."),
		}},
		{"Finalement, décalez mon rendez-vous à vendredi.", []scripted.Step{
			scripted.Call("interpreter_date", map[string]any{"expression": "vendredi"}),
			searchLastPeriod(map[string]any{"professionnel_id": "paul", "type_rendez_vous": "consultation"}),
			presentSlots(),
		}},
		{"Le premier.", []scripted.Step{
			scripted.CallFn("modifier_rendez_vous", func(req model.Request) (any, error) {
				appt, err := last(req, "reserver_creneau")
				if err != nil {
					return nil, err
				}
				slot, err := slotRank(req, 0)
				return map[string]any{"appointment_id": appt["appointment"].(map[string]any)["id"], "new_slot_id": slot}, err
			}),
			confirm("modifier_rendez_vous", "C'est fait, votre rendez-vous est déplacé au %s."),
		}},
		{"Annule mon rendez-vous.", []scripted.Step{
			scripted.CallFn("annuler_rendez_vous", func(req model.Request) (any, error) {
				appt, err := last(req, "modifier_rendez_vous")
				if err != nil {
					return nil, err
				}
				return map[string]any{"appointment_id": appt["appointment"].(map[string]any)["id"]}, nil
			}),
			confirm("annuler_rendez_vous", "Votre rendez-vous du %s est annulé."),
		}},
	}

	fmt.Printf("Maintenant : %s (%s)\n\n", now.Format(time.RFC3339), tz)
	for _, t := range turns {
		m.Push(t.steps...)
		reply, err := rt.Handle(context.Background(), session, t.user)
		if err != nil {
			return err
		}
		fmt.Printf("UTILISATEUR > %s\n", t.user)
		for _, st := range reply.Steps {
			status := "ok"
			if !st.Success {
				status = string(st.ErrorCode)
			}
			fmt.Printf("    [tool] %s %s → %s\n", st.Tool, compact(st.Arguments), status)
		}
		for _, e := range reply.Effects {
			fmt.Printf("    [calendrier] effet confirmé par %s\n", e.Tool)
		}
		fmt.Printf("AGENT       > %s\n\n", strings.ReplaceAll(reply.Text, "\n", "\n              "))
	}
	return nil
}

func last(req model.Request, tool string) (map[string]any, error) {
	res, ok := scripted.LastToolResult(req, tool)
	if !ok {
		return nil, fmt.Errorf("aucun résultat de %s", tool)
	}
	if res["success"] != true {
		return nil, fmt.Errorf("%s a échoué : %v", tool, res["error"])
	}
	return res, nil
}

func searchLastPeriod(extra map[string]any) scripted.Step {
	return scripted.CallFn("rechercher_disponibilites", func(req model.Request) (any, error) {
		p, err := last(req, "interpreter_date")
		if err != nil {
			return nil, err
		}
		args := map[string]any{"date_debut": p["date_debut"], "date_fin": p["date_fin"]}
		for k, v := range extra {
			args[k] = v
		}
		return args, nil
	})
}

func slots(req model.Request) ([]map[string]any, error) {
	res, err := last(req, "rechercher_disponibilites")
	if err != nil {
		return nil, err
	}
	var out []map[string]any
	for _, s := range res["slots"].([]any) {
		out = append(out, s.(map[string]any))
	}
	return out, nil
}

func slotAt(req model.Request, hhmm string) (string, error) {
	list, err := slots(req)
	if err != nil {
		return "", err
	}
	for _, s := range list {
		if t, _ := time.Parse(time.RFC3339, s["start"].(string)); t.Format("15:04") == hhmm {
			return s["id"].(string), nil
		}
	}
	return "", fmt.Errorf("pas de créneau à %s", hhmm)
}

func slotRank(req model.Request, i int) (string, error) {
	list, err := slots(req)
	if err != nil || i >= len(list) {
		return "", fmt.Errorf("créneau n°%d indisponible", i+1)
	}
	return list[i]["id"].(string), nil
}

func presentSlots() scripted.Step {
	return scripted.SayFn(func(req model.Request) (string, error) {
		res, ok := scripted.LastToolResult(req, "rechercher_disponibilites")
		if !ok || res["success"] != true {
			return "Je n'ai rien trouvé sur cette période. Voulez-vous que je regarde un autre jour ?", nil
		}
		list, _ := slots(req)
		if len(list) > 5 {
			list = list[:5]
		}
		var b strings.Builder
		b.WriteString("J'ai trouvé ces disponibilités :")
		for _, s := range list {
			fmt.Fprintf(&b, "\n- %s avec %s", s["libelle"], s["professionnel_nom"])
		}
		b.WriteString("\nLequel préférez-vous ?")
		return b.String(), nil
	})
}

func confirm(tool, format string) scripted.Step {
	return scripted.SayFn(func(req model.Request) (string, error) {
		res, ok := scripted.LastToolResult(req, tool)
		if !ok || res["success"] != true {
			return "Désolé, l'opération n'a pas pu être enregistrée.", nil
		}
		return fmt.Sprintf(format, res["appointment"].(map[string]any)["libelle"]), nil
	})
}

func compact(raw json.RawMessage) string {
	s := string(raw)
	if len(s) > 110 {
		return s[:110] + "…"
	}
	return s
}
