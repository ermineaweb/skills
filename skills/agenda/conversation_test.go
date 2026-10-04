package agendaskill_test

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"
	"testing"
	"time"

	"skills/agent"
	"skills/model"
	"skills/model/scripted"
	"skills/services/agenda"
	agendaskill "skills/skills/agenda"
	agendatools "skills/tools/agenda"
	"skills/types"
)

// Ces tests rejouent des conversations avec un modèle scripté : ils
// vérifient le parcours agent → skill → agenda et ses garde-fous, pas la
// compréhension d'un vrai modèle.

var paris, _ = time.LoadLocation("Europe/Paris")

// Mercredi 30 septembre 2026, 10h00 à Paris.
var now = time.Date(2026, 9, 30, 10, 0, 0, 0, paris)

func setup(t *testing.T) (*agent.Runtime, *scripted.Model, *agent.Session, *agenda.MockProvider) {
	t.Helper()
	ag := agenda.NewMockProvider()
	skill, err := agendaskill.New(agendaskill.Config{Provider: ag})
	if err != nil {
		t.Fatal(err)
	}
	m := scripted.New()
	rt, _ := agent.New(m, agent.WithClock(func() time.Time { return now }))
	if err := rt.RegisterSkill(skill); err != nil {
		t.Fatal(err)
	}
	s, err := agent.NewSession("s1", "Europe/Paris", types.UserContext{ClientID: "c-42", Name: "Camille"})
	if err != nil {
		t.Fatal(err)
	}
	return rt, m, s, ag
}

func turn(t *testing.T, rt *agent.Runtime, s *agent.Session, msg string) agent.Reply {
	t.Helper()
	r, err := rt.Handle(context.Background(), s, msg)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestCreateConversation(t *testing.T) {
	rt, m, s, ag := setup(t)
	m.Push(
		scripted.Call(agent.ActivateSkillTool, map[string]string{"nom": agendaskill.Name}),
		scripted.Call(agendatools.ToolCreateEvent, map[string]any{"titre": "Rendez-vous avec Paul", "debut": "demain à 14h", "participants": []string{"Paul"}}),
		scripted.SayFn(func(req model.Request) (string, error) {
			res, _ := scripted.LastToolResult(req, agendatools.ToolCreateEvent)
			return "C'est noté : " + res["evenement"].(map[string]any)["libelle"].(string) + ".", nil
		}),
	)
	r := turn(t, rt, s, "Ajoute un rendez-vous demain à 14h avec Paul.")
	if len(r.Effects) != 1 || r.Effects[0].Tool != agendatools.ToolCreateEvent {
		t.Fatalf("effets = %+v", r.Effects)
	}
	if r.Text != "C'est noté : jeudi 1er octobre de 14h à 15h." {
		t.Fatalf("réponse = %q", r.Text)
	}
	if ag.Calls(agenda.OpCreate) != 1 {
		t.Fatal("création non transmise à l'agenda")
	}
}

// L'utilisateur est celui de la session : un identifiant d'utilisateur
// fourni par le modèle est refusé par le schéma, avant tout appel.
func TestModelCannotChooseUser(t *testing.T) {
	rt, m, s, ag := setup(t)
	s.ActivateSkill(agendaskill.Name)
	m.Push(
		scripted.Call(agendatools.ToolListEvents, map[string]any{"periode": "demain", "user_id": "c-autre"}),
		scripted.Say("Je ne peux pas."),
	)
	r := turn(t, rt, s, "Montre l'agenda de c-autre demain.")
	if r.Steps[0].Success || r.Steps[0].ErrorCode != types.ErrInvalidRequest || ag.Calls(agenda.OpList) != 0 {
		t.Fatalf("étape = %+v", r.Steps[0])
	}
}

// « Décale ma réunion de 10h » avec deux réunions à 10h : le skill ne
// choisit pas, le modèle demande, puis agit sur l'événement précisé.
func TestAmbiguousUpdateConversation(t *testing.T) {
	rt, m, s, ag := setup(t)
	s.ActivateSkill(agendaskill.Name)
	m.Push(
		scripted.Call(agendatools.ToolCreateEvent, map[string]any{"titre": "Réunion équipe", "debut": "vendredi 10h"}),
		scripted.Call(agendatools.ToolCreateEvent, map[string]any{"titre": "Réunion client", "debut": "vendredi 10h", "ignorer_conflits": true}),
		scripted.Say("C'est noté."),
	)
	turn(t, rt, s, "Ajoute une réunion équipe et une réunion client vendredi à 10h.")

	m.Push(
		scripted.Call(agendatools.ToolUpdateEvent, map[string]any{"cible": map[string]any{"quand": "vendredi à 10h", "texte": "réunion"}, "debut": "15h"}),
		scripted.Say("Tu parles de la réunion équipe ou de la réunion client ?"),
	)
	r := turn(t, rt, s, "Décale ma réunion de 10h à 15h.")
	if r.Steps[0].ErrorCode != types.ErrEventAmbiguous || len(r.Effects) != 0 || ag.Calls(agenda.OpUpdate) != 0 {
		t.Fatalf("ambiguïté : %+v", r.Steps)
	}

	// Le modèle reprend l'identifiant de la réunion client dans l'erreur.
	m.Push(
		scripted.CallFn(agendatools.ToolUpdateEvent, func(req model.Request) (any, error) {
			id := regexp.MustCompile(`\[([^\]]+)\] Réunion client`).FindStringSubmatch(lastToolContent(req))
			return map[string]any{"event_id": id[1], "debut": "15h"}, nil
		}),
		scripted.Say("La réunion client est déplacée à 15h."),
	)
	r = turn(t, rt, s, "La réunion client.")
	if len(r.Effects) != 1 {
		t.Fatalf("effets = %+v, étapes = %+v", r.Effects, r.Steps)
	}
	var out agendatools.EventOutput
	_ = json.Unmarshal(r.Effects[0].Result, &out)
	if out.Evenement.Titre != "Réunion client" || out.Evenement.Libelle != "vendredi 2 octobre de 15h à 16h" {
		t.Fatalf("modifié = %+v", out.Evenement)
	}
}

// lastToolContent renvoie le contenu du dernier résultat d'outil de
// l'historique.
func lastToolContent(req model.Request) string {
	for i := len(req.Messages) - 1; i >= 0; i-- {
		if req.Messages[i].Role == model.RoleTool {
			return req.Messages[i].Content
		}
	}
	return ""
}

func TestSkillMetadata(t *testing.T) {
	skill, err := agendaskill.New(agendaskill.Config{Provider: agenda.NewMockProvider()})
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tool := range skill.Tools() {
		names = append(names, tool.Definition.Name)
	}
	if got := strings.Join(names, ","); got != "list_events,create_event,update_event,delete_event" {
		t.Fatalf("tools = %s", got)
	}
	if !strings.Contains(skill.Description(), "prise-de-rendez-vous") || !strings.Contains(skill.Description(), "conseil") {
		t.Fatalf("la description doit délimiter le skill : %s", skill.Description())
	}
	if _, err := agendaskill.New(agendaskill.Config{}); err == nil {
		t.Fatal("Provider absent accepté")
	}
}
