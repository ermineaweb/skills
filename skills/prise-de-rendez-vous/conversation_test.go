package priserdv_test

// Scénarios de conversation de bout en bout : runtime + skill + tools +
// calendrier mock, avec un modèle scripté (déterministe) à la place du LLM.
//
// Ce que ces tests vérifient : l'orchestration, le fait que chaque étape a
// accès au contexte nécessaire (résultats de tools précédents), le respect des
// règles métier par le code (identifiants, confirmations, fuseaux) et l'état
// réel du calendrier.
// Ce qu'ils ne vérifient pas : la qualité de compréhension d'un LLM réel.
// Chaque étape scriptée joue le rôle du modèle ; ses réponses textuelles sont
// dérivées des résultats de tools, comme l'exigent les instructions.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"skills/agent"
	"skills/model"
	"skills/model/scripted"
	"skills/services/calendar"
	priserdv "skills/skills/prise-de-rendez-vous"
	"skills/types"
)

var (
	paris = func() *time.Location { l, _ := time.LoadLocation("Europe/Paris"); return l }()
	// Mercredi 30 septembre 2026, 10h00.
	now    = time.Date(2026, 9, 30, 10, 0, 0, 0, paris)
	client = types.UserContext{ClientID: "c-42", Name: "Camille Durand", Email: "camille@example.com"}
)

type harness struct {
	t   *testing.T
	rt  *agent.Runtime
	s   *agent.Session
	m   *scripted.Model
	cal *calendar.MockProvider
}

func newHarness(t *testing.T, cal *calendar.MockProvider, user types.UserContext) *harness {
	t.Helper()
	if cal == nil {
		var err error
		if cal, err = calendar.NewDemoProvider(paris, func() time.Time { return now }); err != nil {
			t.Fatal(err)
		}
	}
	skill, err := priserdv.New(priserdv.Config{Provider: cal})
	if err != nil {
		t.Fatal(err)
	}
	m := scripted.New()
	rt, err := agent.New(m, agent.WithClock(func() time.Time { return now }))
	if err != nil {
		t.Fatal(err)
	}
	if err := rt.RegisterSkill(skill); err != nil {
		t.Fatal(err)
	}
	s, err := agent.NewSession("conv-1", "Europe/Paris", user)
	if err != nil {
		t.Fatal(err)
	}
	return &harness{t: t, rt: rt, s: s, m: m, cal: cal}
}

// turn joue un message utilisateur ; steps décrit ce que « le modèle » fait.
func (h *harness) turn(user string, steps ...scripted.Step) agent.Reply {
	h.t.Helper()
	h.m.Push(steps...)
	reply, err := h.rt.Handle(context.Background(), h.s, user)
	if err != nil {
		h.t.Fatalf("%q : %v", user, err)
	}
	if n := h.m.Remaining(); n != 0 {
		h.t.Fatalf("%q : %d étape(s) non consommée(s)", user, n)
	}
	h.t.Logf("USER  : %s", user)
	for _, st := range reply.Steps {
		h.t.Logf("  TOOL %s %s -> %s", st.Tool, st.Arguments, truncate(string(st.Result), 160))
	}
	h.t.Logf("AGENT : %s", reply.Text)
	return reply
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// ---------- Comportements du « modèle » scripté ----------

func activate() scripted.Step {
	return scripted.Call(agent.ActivateSkillTool, map[string]any{"nom": priserdv.Name})
}

func interpret(expr string) scripted.Step {
	return scripted.Call("interpreter_date", map[string]any{"expression": expr})
}

func lastResult(req model.Request, tool string) (map[string]any, error) {
	res, ok := scripted.LastToolResult(req, tool)
	if !ok {
		return nil, fmt.Errorf("aucun résultat de %s dans l'historique", tool)
	}
	return res, nil
}

// search appelle rechercher_disponibilites sur la dernière période interprétée,
// avec les critères déjà connus (extra).
func search(extra func(req model.Request) (map[string]any, error)) scripted.Step {
	return scripted.CallFn("rechercher_disponibilites", func(req model.Request) (any, error) {
		p, err := lastResult(req, "interpreter_date")
		if err != nil {
			return nil, err
		}
		args := map[string]any{"date_debut": p["date_debut"], "date_fin": p["date_fin"]}
		if extra != nil {
			more, err := extra(req)
			if err != nil {
				return nil, err
			}
			for k, v := range more {
				args[k] = v
			}
		}
		return args, nil
	})
}

// professional retrouve l'identifiant d'un professionnel dans le résultat de
// lister_professionnels (jamais inventé).
func professional(name string) func(req model.Request) (map[string]any, error) {
	return func(req model.Request) (map[string]any, error) {
		res, err := lastResult(req, "lister_professionnels")
		if err != nil {
			return nil, err
		}
		for _, p := range res["professionnels"].([]any) {
			pm := p.(map[string]any)
			if strings.Contains(pm["nom"].(string), name) {
				return map[string]any{"professionnel_id": pm["id"]}, nil
			}
		}
		return nil, fmt.Errorf("professionnel %s introuvable", name)
	}
}

func lastSlots(req model.Request) ([]map[string]any, error) {
	res, err := lastResult(req, "rechercher_disponibilites")
	if err != nil {
		return nil, err
	}
	raw, _ := res["slots"].([]any)
	out := make([]map[string]any, 0, len(raw))
	for _, s := range raw {
		out = append(out, s.(map[string]any))
	}
	return out, nil
}

func slotByHour(req model.Request, hhmm string) (string, error) {
	slots, err := lastSlots(req)
	if err != nil {
		return "", err
	}
	for _, s := range slots {
		start, _ := time.Parse(time.RFC3339, s["start"].(string))
		if start.Format("15:04") == hhmm {
			return s["id"].(string), nil
		}
	}
	return "", fmt.Errorf("aucun créneau à %s dans la dernière liste", hhmm)
}

func slotByRank(req model.Request, i int) (string, error) {
	slots, err := lastSlots(req)
	if err != nil {
		return "", err
	}
	if i >= len(slots) {
		return "", errors.New("rang hors liste")
	}
	return slots[i]["id"].(string), nil
}

func book(pick func(req model.Request) (string, error), apptType string) scripted.Step {
	return scripted.CallFn("reserver_creneau", func(req model.Request) (any, error) {
		id, err := pick(req)
		return map[string]any{"slot_id": id, "type_rendez_vous": apptType}, err
	})
}

func appointmentIDFrom(tool string) func(req model.Request) (string, error) {
	return func(req model.Request) (string, error) {
		res, err := lastResult(req, tool)
		if err != nil {
			return "", err
		}
		appt, ok := res["appointment"].(map[string]any)
		if !ok {
			return "", fmt.Errorf("pas de rendez-vous dans le résultat de %s", tool)
		}
		return appt["id"].(string), nil
	}
}

// userMessage : formulation utilisateur des erreurs, conforme à SKILL.md.
func userMessage(res map[string]any) string {
	e, _ := res["error"].(map[string]any)
	switch types.ErrorCode(fmt.Sprint(e["code"])) {
	case types.ErrNoAvailability:
		return "Je n'ai rien trouvé sur cette période. Voulez-vous que je regarde un autre jour ?"
	case types.ErrSlotNoLongerAvailable:
		return "Désolé, ce créneau vient d'être pris. Je peux rechercher une autre disponibilité."
	case types.ErrCalendarUnavailable:
		return "L'agenda est momentanément indisponible, pouvez-vous réessayer dans quelques minutes ?"
	case types.ErrAppointmentNotFound:
		return "Je ne retrouve pas ce rendez-vous."
	default:
		return "L'opération n'a pas pu être réalisée."
	}
}

// present annonce les créneaux du dernier résultat de recherche.
func present() scripted.Step {
	return scripted.SayFn(func(req model.Request) (string, error) {
		res, err := lastResult(req, "rechercher_disponibilites")
		if err != nil {
			return "", err
		}
		if res["success"] != true {
			return userMessage(res), nil
		}
		slots, _ := lastSlots(req)
		if len(slots) > 5 { // instructions : 5 créneaux présentés au maximum
			slots = slots[:5]
		}
		var b strings.Builder
		fmt.Fprintf(&b, "J'ai trouvé ces disponibilités :\n")
		for _, s := range slots {
			fmt.Fprintf(&b, "- %s avec %s\n", s["libelle"], s["professionnel_nom"])
		}
		b.WriteString("Lequel préférez-vous ?")
		return b.String(), nil
	})
}

// report confirme (ou non) une opération, uniquement d'après le résultat du tool.
func report(tool string) scripted.Step {
	return scripted.SayFn(func(req model.Request) (string, error) {
		res, err := lastResult(req, tool)
		if err != nil {
			return "", err
		}
		if res["success"] != true {
			return userMessage(res), nil
		}
		libelle := res["appointment"].(map[string]any)["libelle"]
		switch tool {
		case "reserver_creneau":
			return fmt.Sprintf("Votre rendez-vous est confirmé : %s.", libelle), nil
		case "modifier_rendez_vous":
			return fmt.Sprintf("C'est fait, votre rendez-vous est déplacé au %s.", libelle), nil
		default:
			return fmt.Sprintf("Votre rendez-vous du %s est annulé.", libelle), nil
		}
	})
}

// ---------- Assertions ----------

func toolsCalled(r agent.Reply) string {
	var names []string
	for _, s := range r.Steps {
		names = append(names, s.Tool)
	}
	return strings.Join(names, ",")
}

func argOf(t *testing.T, r agent.Reply, tool, key string) string {
	t.Helper()
	for i := len(r.Steps) - 1; i >= 0; i-- {
		if r.Steps[i].Tool == tool {
			var m map[string]any
			_ = jsonUnmarshal(r.Steps[i].Arguments, &m)
			return fmt.Sprint(m[key])
		}
	}
	t.Fatalf("%s non appelé", tool)
	return ""
}

func mustContain(t *testing.T, text string, parts ...string) {
	t.Helper()
	for _, p := range parts {
		if !strings.Contains(text, p) {
			t.Fatalf("%q ne contient pas %q", text, p)
		}
	}
}

// ---------- Scénarios ----------

// Flux de référence : « jeudi après-midi » → 3 créneaux → « 15h30 » → confirmé.
func TestConversationFluxDeReference(t *testing.T) {
	h := newHarness(t, nil, client)

	r := h.turn("Je voudrais un rendez-vous jeudi après-midi.",
		activate(), interpret("jeudi après-midi"), search(nil), present())
	if got := toolsCalled(r); got != "activer_skill,interpreter_date,rechercher_disponibilites" {
		t.Fatalf("séquence = %s", got)
	}
	// Le backend reçoit des dates ISO avec fuseau, jamais « jeudi après-midi ».
	if d := argOf(t, r, "rechercher_disponibilites", "date_debut"); d != "2026-10-01T12:00:00+02:00" {
		t.Fatalf("date_debut = %s", d)
	}
	mustContain(t, r.Text, "jeudi 1er octobre à 14h", "à 15h30", "à 17h")
	if len(r.Effects) != 0 || h.cal.Calls(calendar.OpBook) != 0 {
		t.Fatal("aucune réservation sans choix de l'utilisateur")
	}

	r = h.turn("Je prends 15h30.",
		book(func(req model.Request) (string, error) { return slotByHour(req, "15:30") }, "consultation"),
		report("reserver_creneau"))
	mustContain(t, r.Text, "confirmé", "15h30")
	if len(r.Effects) != 1 {
		t.Fatalf("effets = %+v", r.Effects)
	}
	appt, ok := h.cal.Appointment(appointmentIDOf(t, r))
	if !ok || appt.Status != types.StatusConfirmed || appt.Start.Format(time.RFC3339) != "2026-10-01T15:30:00+02:00" {
		t.Fatalf("calendrier = %+v", appt)
	}
}

// « Je veux un rendez-vous jeudi. » : trop de créneaux → question ciblée,
// puis la réponse est combinée au contexte (jeudi).
func TestConversationJeudiPuisPrecision(t *testing.T) {
	h := newHarness(t, nil, client)

	r := h.turn("Je veux un rendez-vous jeudi.",
		activate(), interpret("jeudi"), search(nil),
		scripted.Say("J'ai de nombreuses disponibilités jeudi. Vous préférez le matin ou l'après-midi ?"))
	if argOf(t, r, "rechercher_disponibilites", "date_fin") != "2026-10-02T00:00:00+02:00" {
		t.Fatal("la recherche doit couvrir toute la journée de jeudi")
	}

	r = h.turn("L'après-midi.", interpret("jeudi après-midi"), search(nil), present())
	if got := toolsCalled(r); got != "interpreter_date,rechercher_disponibilites" {
		t.Fatalf("la collecte ne doit pas recommencer : %s", got)
	}
	if argOf(t, r, "rechercher_disponibilites", "date_debut") != "2026-10-01T12:00:00+02:00" {
		t.Fatal("la précision doit être combinée au jour déjà donné")
	}
	if h.cal.Calls(calendar.OpBook) != 0 {
		t.Fatal("aucune réservation automatique")
	}
}

// Parcours complet : Paul la semaine prochaine → vendredi matin → le deuxième
// → type → décale à lundi → le premier → annulation.
func TestConversationParcoursComplet(t *testing.T) {
	h := newHarness(t, nil, client)
	paul := professional("Paul")

	r := h.turn("Je veux voir Paul la semaine prochaine.",
		activate(),
		scripted.Call("lister_professionnels", map[string]any{}),
		interpret("la semaine prochaine"),
		search(paul),
		scripted.Say("Paul a de nombreuses disponibilités la semaine prochaine. Quel jour vous arrangerait ?"))
	if argOf(t, r, "rechercher_disponibilites", "professionnel_id") != "paul" {
		t.Fatal("l'identifiant de Paul doit venir de lister_professionnels")
	}
	if argOf(t, r, "rechercher_disponibilites", "date_debut") != "2026-10-05T00:00:00+02:00" {
		t.Fatal("la semaine prochaine commence le lundi 5 octobre")
	}

	// « Plutôt vendredi matin » : le modèle combine avec « la semaine prochaine »
	// (dernière période interprétée) et conserve Paul.
	fridayOfThatWeek := scripted.CallFn("interpreter_date", func(req model.Request) (any, error) {
		p, err := lastResult(req, "interpreter_date")
		if err != nil {
			return nil, err
		}
		monday, _ := time.Parse(time.RFC3339, p["date_debut"].(string))
		friday := monday.AddDate(0, 0, 4)
		return map[string]any{"expression": fmt.Sprintf("%d/%d matin", friday.Day(), friday.Month())}, nil
	})
	r = h.turn("Plutôt vendredi matin.", fridayOfThatWeek, search(paul), present())
	if strings.Contains(toolsCalled(r), "lister_professionnels") {
		t.Fatal("les informations déjà connues ne doivent pas être recollectées")
	}
	mustContain(t, r.Text, "vendredi 9 octobre à 9h", "vendredi 9 octobre à 10h30", "Paul Martin")

	r = h.turn("Je prends le deuxième.",
		scripted.Say("Très bien. S'agit-il d'une consultation ou d'un bilan ?"))
	if h.cal.Calls(calendar.OpBook) != 0 {
		t.Fatal("pas de réservation tant que le type n'est pas connu")
	}

	r = h.turn("Une consultation.",
		book(func(req model.Request) (string, error) { return slotByRank(req, 1) }, "consultation"),
		report("reserver_creneau"))
	mustContain(t, r.Text, "confirmé", "vendredi 9 octobre à 10h30")
	apptID := appointmentIDOf(t, r)

	r = h.turn("Finalement décale-le à lundi.",
		interpret("lundi"),
		search(func(req model.Request) (map[string]any, error) {
			return map[string]any{"professionnel_id": "paul", "type_rendez_vous": "consultation"}, nil
		}),
		present())
	mustContain(t, r.Text, "lundi 5 octobre à 9h")
	if h.cal.Calls(calendar.OpUpdate) != 0 {
		t.Fatal("le déplacement attend le choix du nouveau créneau")
	}

	r = h.turn("Le premier.",
		scripted.CallFn("modifier_rendez_vous", func(req model.Request) (any, error) {
			id, err := appointmentIDFrom("reserver_creneau")(req)
			if err != nil {
				return nil, err
			}
			slot, err := slotByRank(req, 0)
			return map[string]any{"appointment_id": id, "new_slot_id": slot}, err
		}),
		report("modifier_rendez_vous"))
	mustContain(t, r.Text, "déplacé", "lundi 5 octobre à 9h")
	if a, _ := h.cal.Appointment(apptID); a.Start.Format(time.RFC3339) != "2026-10-05T09:00:00+02:00" {
		t.Fatalf("calendrier = %+v", a)
	}

	r = h.turn("Annule mon rendez-vous.",
		scripted.CallFn("annuler_rendez_vous", func(req model.Request) (any, error) {
			id, err := appointmentIDFrom("modifier_rendez_vous")(req)
			return map[string]any{"appointment_id": id}, err
		}),
		report("annuler_rendez_vous"))
	mustContain(t, r.Text, "annulé")
	if a, _ := h.cal.Appointment(apptID); a.Status != types.StatusCancelled {
		t.Fatal("le calendrier doit refléter l'annulation")
	}
	if len(r.Effects) != 1 || r.Effects[0].Tool != "annuler_rendez_vous" {
		t.Fatalf("effets = %+v", r.Effects)
	}
}

// Exemple de la section « contexte conversationnel » : « Plutôt 16h » se
// rapporte au rendez-vous avec Paul du jeudi, sans recollecte.
func TestConversationContexte16h(t *testing.T) {
	cal, _ := calendar.NewMockProvider(paris, func() time.Time { return now })
	cal.AddAppointmentType("consultation", 30)
	cal.AddProfessional("paul", "Paul Martin", "consultation")
	thu := func(h, m int) time.Time { return time.Date(2026, 10, 1, h, m, 0, 0, paris) }
	cal.AddOpening("paul", thu(14, 0), thu(14, 30))
	cal.AddOpening("paul", thu(16, 0), thu(16, 30))
	h := newHarness(t, cal, client)

	h.turn("Je voudrais un rendez-vous avec Paul.",
		activate(), scripted.Call("lister_professionnels", map[string]any{}),
		scripted.Say("Quel jour vous conviendrait ?"))

	r := h.turn("Jeudi.", interpret("jeudi"), search(professional("Paul")),
		scripted.Say("Paul est disponible à 14h et 16h."))
	if argOf(t, r, "rechercher_disponibilites", "professionnel_id") != "paul" {
		t.Fatal("Paul doit être conservé du premier message")
	}

	// Seul type proposé par Paul : pas de question inutile.
	r = h.turn("Plutôt 16h.",
		book(func(req model.Request) (string, error) { return slotByHour(req, "16:00") }, "consultation"),
		report("reserver_creneau"))
	if got := toolsCalled(r); got != "reserver_creneau" {
		t.Fatalf("séquence = %s", got)
	}
	a, _ := h.cal.Appointment(appointmentIDOf(t, r))
	if a.ProfessionnelID != "paul" || a.Start.Format(time.RFC3339) != "2026-10-01T16:00:00+02:00" {
		t.Fatalf("calendrier = %+v", a)
	}
}

func TestConversationDemainMatin(t *testing.T) {
	h := newHarness(t, nil, client)
	r := h.turn("Est-ce que vous avez quelque chose demain matin ?",
		activate(), interpret("demain matin"), search(nil), present())
	if argOf(t, r, "rechercher_disponibilites", "date_fin") != "2026-10-01T12:00:00+02:00" {
		t.Fatal("demain matin = jeudi 8h-12h")
	}
	mustContain(t, r.Text, "à 9h avec Paul Martin", "à 9h30 avec Marie Dubois")
	if len(r.Effects) != 0 {
		t.Fatal("une consultation de disponibilités n'a aucun effet")
	}
}

func TestConversationAnnulationDansUneNouvelleConversation(t *testing.T) {
	h := newHarness(t, nil, client)
	preBook(t, h.cal, "c-42", 14)

	r := h.turn("Annule mon rendez-vous.",
		activate(),
		scripted.Call("lister_rendez_vous", map[string]any{}),
		scripted.CallFn("annuler_rendez_vous", func(req model.Request) (any, error) {
			res, err := lastResult(req, "lister_rendez_vous")
			if err != nil {
				return nil, err
			}
			list := res["rendez_vous"].([]any)
			if len(list) != 1 {
				return nil, errors.New("un seul rendez-vous attendu")
			}
			return map[string]any{"appointment_id": list[0].(map[string]any)["id"]}, nil
		}),
		report("annuler_rendez_vous"))
	mustContain(t, r.Text, "jeudi 1er octobre à 14h", "annulé")
}

func TestConversationAnnulationAmbigue(t *testing.T) {
	h := newHarness(t, nil, client)
	preBook(t, h.cal, "c-42", 14)
	preBook(t, h.cal, "c-42", 17)

	h.turn("Annule mon rendez-vous.",
		activate(),
		scripted.Call("lister_rendez_vous", map[string]any{}),
		scripted.Say("Vous avez deux rendez-vous : jeudi 1er octobre à 14h et à 17h. Lequel souhaitez-vous annuler ?"))
	if h.cal.Calls(calendar.OpCancel) != 0 {
		t.Fatal("aucune annulation tant que le rendez-vous visé est ambigu")
	}
}

func TestConversationCreneauPrisEntreTemps(t *testing.T) {
	h := newHarness(t, nil, client)
	h.turn("Je voudrais un rendez-vous jeudi après-midi.",
		activate(), interpret("jeudi après-midi"), search(nil), present())

	// Un autre client réserve 15h30 pendant que l'utilisateur réfléchit.
	var taken string
	for _, st := range h.s.Messages() {
		if st.Role == model.RoleTool && st.ToolName == "rechercher_disponibilites" {
			taken = extractSlot(st.Content, "15:30")
		}
	}
	if err := h.cal.OccupySlot(taken); err != nil {
		t.Fatal(err)
	}

	r := h.turn("Je prends 15h30.",
		book(func(req model.Request) (string, error) { return slotByHour(req, "15:30") }, "consultation"),
		report("reserver_creneau"))
	if r.Steps[0].ErrorCode != types.ErrSlotNoLongerAvailable || len(r.Effects) != 0 {
		t.Fatalf("steps = %+v", r.Steps)
	}
	mustContain(t, r.Text, "vient d'être pris", "rechercher une autre disponibilité")
	if strings.Contains(r.Text, "confirmé") {
		t.Fatal("aucune confirmation sans succès du calendrier")
	}
}

func TestConversationSlotInventeRefuse(t *testing.T) {
	h := newHarness(t, nil, client)
	r := h.turn("Réservez-moi jeudi à 16h.",
		activate(),
		// Le modèle « devine » un identifiant sans avoir cherché.
		scripted.Call("reserver_creneau", map[string]any{"slot_id": "slot_paul_20261001T1400Z_30", "type_rendez_vous": "consultation"}),
		scripted.Say("Je dois d'abord vérifier les disponibilités de jeudi."))
	if r.Steps[1].ErrorCode != types.ErrInvalidRequest || h.cal.Calls(calendar.OpBook) != 0 {
		t.Fatalf("un slot_id inventé ne doit jamais atteindre le calendrier : %+v", r.Steps[1])
	}
}

func TestConversationAucuneDisponibilite(t *testing.T) {
	h := newHarness(t, nil, client)
	r := h.turn("Vous avez quelque chose samedi ?",
		activate(), interpret("samedi"), search(nil), present())
	if r.Steps[2].ErrorCode != types.ErrNoAvailability {
		t.Fatalf("steps = %+v", r.Steps)
	}
	mustContain(t, r.Text, "rien trouvé")
}

func TestConversationCalendrierIndisponible(t *testing.T) {
	h := newHarness(t, nil, client)
	h.cal.SetUnavailable(true)
	r := h.turn("Un rendez-vous demain ?",
		activate(), interpret("demain"), search(nil), present())
	mustContain(t, r.Text, "momentanément indisponible")
	if strings.Contains(string(r.Steps[2].Result), "dial") {
		t.Fatal("détail technique transmis au modèle")
	}
}

func TestConversationAnonymeDoitDonnerSonNom(t *testing.T) {
	h := newHarness(t, nil, types.UserContext{})
	h.turn("Un rendez-vous jeudi après-midi ?", activate(), interpret("jeudi après-midi"), search(nil), present())

	r := h.turn("14h.",
		book(func(req model.Request) (string, error) { return slotByHour(req, "14:00") }, "consultation"),
		scripted.Say("À quel nom dois-je réserver ?"))
	if r.Steps[0].ErrorCode != types.ErrMissingInformation {
		t.Fatalf("steps = %+v", r.Steps)
	}

	r = h.turn("Léa Martin.",
		scripted.CallFn("reserver_creneau", func(req model.Request) (any, error) {
			id, err := slotByHour(req, "14:00")
			return map[string]any{"slot_id": id, "type_rendez_vous": "consultation", "client_name": "Léa Martin"}, err
		}),
		report("reserver_creneau"))
	mustContain(t, r.Text, "confirmé")
}

func TestSkillExposeSesToolsEtSaDescription(t *testing.T) {
	cal, _ := calendar.NewDemoProvider(paris, func() time.Time { return now })
	s, err := priserdv.New(priserdv.Config{Provider: cal})
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tool := range s.Tools() {
		names = append(names, tool.Definition.Name)
	}
	want := "interpreter_date,lister_professionnels,rechercher_disponibilites,reserver_creneau,lister_rendez_vous,modifier_rendez_vous,annuler_rendez_vous"
	if strings.Join(names, ",") != want {
		t.Fatalf("tools = %v", names)
	}
	if !strings.Contains(s.Description(), "rendez-vous") || strings.Contains(s.Instructions(), "{{") {
		t.Fatal("description ou instructions incorrectes")
	}
	if _, err := priserdv.New(priserdv.Config{}); err == nil {
		t.Fatal("Provider obligatoire")
	}
	auto, _ := priserdv.New(priserdv.Config{Provider: cal, AutoBooking: true})
	if !strings.Contains(auto.Instructions(), "Politique produit") {
		t.Fatal("la politique AutoBooking doit apparaître dans les instructions")
	}
}
