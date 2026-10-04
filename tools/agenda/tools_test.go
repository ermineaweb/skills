package agendatools_test

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"
	"testing"
	"time"

	"skills/services/agenda"
	agendatools "skills/tools/agenda"
	"skills/types"
)

// Mercredi 30 septembre 2026, 10h00 à Paris. Jeudi = 1er octobre.
var paris = mustLoc("Europe/Paris")
var now = time.Date(2026, 9, 30, 10, 0, 0, 0, paris)

func mustLoc(name string) *time.Location {
	l, err := time.LoadLocation(name)
	if err != nil {
		panic(err)
	}
	return l
}

var alice = types.UserContext{ClientID: "c-alice", Name: "Alice"}

type fixture struct {
	t     *testing.T
	ag    *agenda.MockProvider
	tc    types.ToolContext
	tools map[string]types.ToolHandler
}

func newFixture(t *testing.T, user types.UserContext) *fixture {
	t.Helper()
	return newFixtureOn(t, agenda.NewMockProvider(), user, paris)
}

// newFixtureOn : une session de user, dans le fuseau loc, sur l'agenda ag.
func newFixtureOn(t *testing.T, ag *agenda.MockProvider, user types.UserContext, loc *time.Location) *fixture {
	t.Helper()
	return &fixture{
		t:  t,
		ag: ag,
		tc: types.ToolContext{SessionID: "s-" + user.ClientID, User: user, Location: loc, Now: now.In(loc), State: types.NewMemoryState()},
		tools: map[string]types.ToolHandler{
			"create": agendatools.NewCreateEvent(ag),
			"list":   agendatools.NewListEvents(ag),
			"update": agendatools.NewUpdateEvent(ag),
			"delete": agendatools.NewDeleteEvent(ag),
		},
	}
}

func (f *fixture) call(tool string, args any) (any, *types.Error) {
	f.t.Helper()
	raw, _ := json.Marshal(args)
	return f.tools[tool].Execute(context.Background(), f.tc, raw)
}

func (f *fixture) create(args map[string]any) agendatools.EventOutput {
	f.t.Helper()
	out, err := f.call("create", args)
	if err != nil {
		f.t.Fatalf("create_event %v : %v", args, err)
	}
	return out.(agendatools.EventOutput)
}

func (f *fixture) list(args map[string]any) agendatools.ListOutput {
	f.t.Helper()
	out, err := f.call("list", args)
	if err != nil {
		f.t.Fatalf("list_events %v : %v", args, err)
	}
	return out.(agendatools.ListOutput)
}

func wantCode(t *testing.T, err *types.Error, code types.ErrorCode) {
	t.Helper()
	if err == nil || err.Code != code {
		t.Fatalf("erreur = %v, attendu %s", err, code)
	}
}

func titles(l agendatools.ListOutput) string {
	var out []string
	for _, e := range l.Evenements {
		out = append(out, e.Titre+" "+e.Libelle)
	}
	return strings.Join(out, " | ")
}

// ---- Création ----

func TestCreateSimple(t *testing.T) {
	f := newFixture(t, alice)
	out := f.create(map[string]any{"titre": "Rendez-vous avec Paul", "debut": "demain à 14h", "participants": []string{"Paul"}})
	e := out.Evenement
	if e.Debut != "2026-10-01T14:00:00+02:00" || e.Fin != "2026-10-01T15:00:00+02:00" {
		t.Fatalf("horaire = %s → %s (durée par défaut : 1 h)", e.Debut, e.Fin)
	}
	if e.Libelle != "jeudi 1er octobre de 14h à 15h" || len(e.Participants) != 1 {
		t.Fatalf("vue = %+v", e)
	}
	stored, ok := f.ag.Event(e.ID)
	if !ok || stored.OwnerID != "c-alice" || stored.TimeZone != "Europe/Paris" {
		t.Fatalf("enregistré = %+v", stored)
	}
}

func TestCreateWithEndDurationLocationReminder(t *testing.T) {
	f := newFixture(t, alice)
	e := f.create(map[string]any{"titre": "Réunion équipe", "debut": "vendredi 10h", "fin": "11h30", "lieu": "Salle Hélios", "rappels_minutes": []int{15}}).Evenement
	if e.Libelle != "vendredi 2 octobre de 10h à 11h30" || e.Lieu != "Salle Hélios" || len(e.RappelsMinutes) != 1 || e.RappelsMinutes[0] != 15 {
		t.Fatalf("vue = %+v", e)
	}
	e = f.create(map[string]any{"titre": "Appel", "debut": "le 12 octobre à 9h30", "duree_minutes": 30}).Evenement
	if e.Libelle != "lundi 12 octobre de 9h30 à 10h" {
		t.Fatalf("durée : %s", e.Libelle)
	}
	// « à midi » est une heure, pas la plage du déjeuner.
	e = f.create(map[string]any{"titre": "Déjeuner avec Marie", "debut": "mercredi à midi"}).Evenement
	if e.Libelle != "mercredi 7 octobre de 12h à 13h" {
		t.Fatalf("midi : %s", e.Libelle)
	}
	e = f.create(map[string]any{"titre": "Pause", "debut": "dans deux heures", "duree_minutes": 15}).Evenement
	if e.Debut != "2026-09-30T12:00:00+02:00" {
		t.Fatalf("dans deux heures : %s", e.Debut)
	}
	e = f.create(map[string]any{"titre": "Congés", "debut": "le 19 octobre", "fin": "le 23 octobre", "journee_entiere": true}).Evenement
	if e.Libelle != "du lundi 19 octobre au vendredi 23 octobre (journées entières)" {
		t.Fatalf("journées entières : %s", e.Libelle)
	}
}

func TestCreateRecurring(t *testing.T) {
	f := newFixture(t, alice)
	// Demandé un mercredi : la série commence lundi prochain.
	out := f.create(map[string]any{"titre": "Point hebdo", "debut": "lundi 9h", "recurrence": map[string]any{
		"frequence": "hebdomadaire", "jours": []string{"lundi"}, "jusqu_au": "2026-12-31",
	}})
	if out.Evenement.Libelle != "lundi 5 octobre de 9h à 10h" || out.Evenement.Recurrence != "toutes les semaines le lundi, jusqu'au jeudi 31 décembre" {
		t.Fatalf("série = %+v", out.Evenement)
	}
	// Changement d'heure le 25 octobre : 9h reste 9h.
	if len(out.ProchainesOccurrences) != 3 || out.ProchainesOccurrences[2] != "lundi 26 octobre de 9h à 10h" {
		t.Fatalf("prochaines = %v", out.ProchainesOccurrences)
	}
	stored, _ := f.ag.Event(strings.Split(out.Evenement.ID, "@")[0])
	if got := stored.Recurrence.RRULE(); got != "FREQ=WEEKLY;BYDAY=MO;UNTIL=20261231T225959Z" {
		t.Fatalf("RRULE = %s", got)
	}

	out = f.create(map[string]any{"titre": "Comité", "debut": "lundi 18h", "recurrence": map[string]any{
		"frequence": "mensuelle", "jours": []string{"lundi"}, "position": 1, "nombre": 3,
	}})
	if out.Evenement.Recurrence != "tous les mois, le premier lundi, 3 fois" ||
		strings.Join(out.ProchainesOccurrences, " ; ") != "lundi 2 novembre de 18h à 19h ; lundi 7 décembre de 18h à 19h" {
		t.Fatalf("premier lundi : %+v %v", out.Evenement, out.ProchainesOccurrences)
	}

	_, err := f.call("create", map[string]any{"titre": "X", "debut": "lundi 9h", "recurrence": map[string]any{
		"frequence": "quotidienne", "jours": []string{"lundi"},
	}})
	wantCode(t, err, types.ErrInvalidRequest)
}

func TestCreateMissingOrInvalid(t *testing.T) {
	f := newFixture(t, alice)
	_, err := f.call("create", map[string]any{"titre": "Réunion", "debut": "demain"})
	wantCode(t, err, types.ErrMissingInformation)
	if err.Missing[0] != "heure" {
		t.Fatalf("missing = %v", err.Missing)
	}
	_, err = f.call("create", map[string]any{"titre": "Réunion", "debut": "14h"})
	wantCode(t, err, types.ErrMissingInformation)
	_, err = f.call("create", map[string]any{"titre": "Réunion", "debut": "31/02 à 10h"})
	wantCode(t, err, types.ErrInvalidRequest)
	_, err = f.call("create", map[string]any{"titre": "Réunion", "debut": "demain à 25h"})
	wantCode(t, err, types.ErrInvalidRequest) // heure inexistante
	_, err = f.call("create", map[string]any{"titre": "Réunion", "debut": "aujourd'hui à 8h"})
	wantCode(t, err, types.ErrInvalidRequest) // déjà passé
	_, err = f.call("create", map[string]any{"titre": "Réunion", "debut": "demain 10h", "fin": "9h", "duree_minutes": 30})
	wantCode(t, err, types.ErrInvalidRequest)
	if f.ag.Calls(agenda.OpCreate) != 0 {
		t.Fatal("aucune création ne doit atteindre l'agenda")
	}
}

func TestCreateConflict(t *testing.T) {
	f := newFixture(t, alice)
	f.create(map[string]any{"titre": "Réunion client", "debut": "demain 10h"})
	_, err := f.call("create", map[string]any{"titre": "Dentiste", "debut": "demain 10h30"})
	wantCode(t, err, types.ErrEventConflict)
	if !strings.Contains(err.Message, "Réunion client") || f.ag.Calls(agenda.OpCreate) != 1 {
		t.Fatalf("conflit : %v", err)
	}
	// Confirmé par l'utilisateur.
	f.create(map[string]any{"titre": "Dentiste", "debut": "demain 10h30", "ignorer_conflits": true})
	// Une journée entière ne bloque pas un horaire.
	f.create(map[string]any{"titre": "Anniversaire de Léa", "debut": "vendredi", "journee_entiere": true})
	f.create(map[string]any{"titre": "Café", "debut": "vendredi 9h"})
}

func TestCreateProviderFailures(t *testing.T) {
	f := newFixture(t, alice)
	f.ag.UnconfirmedNext(agenda.OpCreate)
	_, err := f.call("create", map[string]any{"titre": "Réunion", "debut": "demain 10h"})
	wantCode(t, err, types.ErrEventNotSaved)
	f.ag.FailNext(agenda.OpCreate, types.ErrPermissionDenied)
	_, err = f.call("create", map[string]any{"titre": "Réunion", "debut": "demain 10h"})
	wantCode(t, err, types.ErrPermissionDenied)
	f.ag.SetUnavailable(true)
	_, err = f.call("create", map[string]any{"titre": "Réunion", "debut": "demain 10h"})
	wantCode(t, err, types.ErrCalendarUnavailable)
	if strings.Contains(err.Message, "simulation") {
		t.Fatalf("détail technique transmis : %v", err)
	}
}

// ---- Consultation ----

func TestListDayPeriodAndEmpty(t *testing.T) {
	f := newFixture(t, alice)
	// Événement passé de la matinée : « aujourd'hui » couvre toute la journée.
	if _, err := f.ag.CreateEvent(context.Background(), agenda.CreateRequest{OwnerID: "c-alice", Event: agenda.Event{
		Title: "Footing", Start: time.Date(2026, 9, 30, 7, 0, 0, 0, paris), End: time.Date(2026, 9, 30, 8, 0, 0, 0, paris),
	}}); err != nil {
		t.Fatal(err)
	}
	f.create(map[string]any{"titre": "Réunion équipe", "debut": "demain 10h"})
	f.create(map[string]any{"titre": "Réunion client", "debut": "demain 14h"})
	f.create(map[string]any{"titre": "Point hebdo", "debut": "lundi 9h", "recurrence": map[string]any{"frequence": "hebdomadaire"}})

	if got := titles(f.list(map[string]any{"periode": "aujourd'hui"})); got != "Footing mercredi 30 septembre de 7h à 8h" {
		t.Fatalf("aujourd'hui : %s", got)
	}
	day := f.list(map[string]any{"periode": "demain"})
	if day.Nombre != 2 || day.Periode.Libelle != "jeudi 1er octobre" {
		t.Fatalf("demain : %+v", day)
	}
	if got := titles(f.list(map[string]any{"periode": "demain après-midi"})); got != "Réunion client jeudi 1er octobre de 14h à 15h" {
		t.Fatalf("demain après-midi : %s", got)
	}
	if got := titles(f.list(map[string]any{"periode": "la semaine prochaine"})); got != "Point hebdo lundi 5 octobre de 9h à 10h" {
		t.Fatalf("semaine prochaine : %s", got)
	}
	// Période explicite : les occurrences de la série sont développées.
	if got := f.list(map[string]any{"du": "2026-10-01", "au": "2026-10-31"}).Nombre; got != 6 {
		t.Fatalf("octobre : %d occurrences", got)
	}
	if got := titles(f.list(map[string]any{"periode": "demain", "texte": "client"})); got != "Réunion client jeudi 1er octobre de 14h à 15h" {
		t.Fatalf("filtre : %s", got)
	}
	empty := f.list(map[string]any{"periode": "ce week-end"})
	if empty.Nombre != 0 || len(empty.Evenements) != 0 || !strings.Contains(empty.Note, "libre") {
		t.Fatalf("agenda vide : %+v", empty)
	}
	_, err := f.call("list", map[string]any{})
	wantCode(t, err, types.ErrMissingInformation)
}

// ---- Modification ----

func TestUpdate(t *testing.T) {
	f := newFixture(t, alice)
	id := f.create(map[string]any{"titre": "Rendez-vous avec Paul", "debut": "demain 14h", "duree_minutes": 45}).Evenement.ID

	// « Décale le rendez-vous de demain à 16h » : même jour, même durée.
	out, err := f.call("update", map[string]any{"event_id": id, "debut": "16h"})
	if err != nil {
		t.Fatal(err)
	}
	u := out.(agendatools.EventOutput)
	if u.Evenement.Libelle != "jeudi 1er octobre de 16h à 16h45" || u.Avant != "jeudi 1er octobre de 14h à 14h45" {
		t.Fatalf("déplacement : %+v", u)
	}
	// Par cible, sans identifiant : un seul candidat.
	out, err = f.call("update", map[string]any{"cible": map[string]any{"quand": "demain", "texte": "Paul"}, "lieu": "Café de la gare"})
	if err != nil {
		t.Fatal(err)
	}
	if e, _ := f.ag.Event(id); e.Location != "Café de la gare" || out.(agendatools.EventOutput).Evenement.Lieu != "Café de la gare" {
		t.Fatalf("lieu : %+v", e)
	}

	_, err = f.call("update", map[string]any{"event_id": id})
	wantCode(t, err, types.ErrInvalidRequest) // rien à modifier
}

func TestUpdateNotFoundAndAmbiguous(t *testing.T) {
	f := newFixture(t, alice)
	f.create(map[string]any{"titre": "Réunion équipe", "debut": "vendredi 10h"})
	f.create(map[string]any{"titre": "Réunion client", "debut": "vendredi 10h", "ignorer_conflits": true})

	_, err := f.call("update", map[string]any{"cible": map[string]any{"quand": "jeudi à 10h"}, "debut": "11h"})
	wantCode(t, err, types.ErrEventNotFound)

	// « Décale ma réunion de 10h » : deux candidats, aucun n'est choisi.
	_, err = f.call("update", map[string]any{"cible": map[string]any{"quand": "vendredi à 10h", "texte": "réunion"}, "debut": "15h"})
	wantCode(t, err, types.ErrEventAmbiguous)
	if !strings.Contains(err.Message, "Réunion équipe") || !strings.Contains(err.Message, "Réunion client") {
		t.Fatalf("candidats absents : %s", err.Message)
	}
	if f.ag.Calls(agenda.OpUpdate) != 0 {
		t.Fatal("rien ne doit être modifié en cas d'ambiguïté")
	}
	// L'utilisateur précise « la réunion client » : son identifiant a été
	// renvoyé dans l'erreur.
	m := regexp.MustCompile(`\[([^\]]+)\] Réunion client`).FindStringSubmatch(err.Message)
	if m == nil {
		t.Fatalf("identifiant absent : %s", err.Message)
	}
	clientID := m[1]
	out, uerr := f.call("update", map[string]any{"event_id": clientID, "debut": "15h"})
	if uerr != nil || out.(agendatools.EventOutput).Evenement.Titre != "Réunion client" {
		t.Fatalf("après précision : %v %v", out, uerr)
	}

	// Identifiant inventé.
	_, err = f.call("update", map[string]any{"event_id": "evt-999", "debut": "15h"})
	wantCode(t, err, types.ErrInvalidRequest)
}

func TestUpdateRecurringScope(t *testing.T) {
	f := newFixture(t, alice)
	f.create(map[string]any{"titre": "Point hebdo", "debut": "lundi 9h", "recurrence": map[string]any{"frequence": "hebdomadaire"}})
	target := map[string]any{"quand": "lundi 12 octobre", "texte": "hebdo"}

	_, err := f.call("update", map[string]any{"cible": target, "debut": "11h"})
	wantCode(t, err, types.ErrMissingInformation)
	if err.Missing[0] != "portee" {
		t.Fatalf("missing = %v", err.Missing)
	}
	// Une seule occurrence : détachée de la série.
	if _, err := f.call("update", map[string]any{"cible": target, "portee": "occurrence", "debut": "11h"}); err != nil {
		t.Fatal(err)
	}
	if got := titles(f.list(map[string]any{"du": "2026-10-05", "au": "2026-10-19"})); got !=
		"Point hebdo lundi 5 octobre de 9h à 10h | Point hebdo lundi 12 octobre de 11h à 12h | Point hebdo lundi 19 octobre de 9h à 10h" {
		t.Fatalf("occurrence : %s", got)
	}
	// Toute la série : seul l'horaire change.
	if _, err := f.call("update", map[string]any{"cible": map[string]any{"quand": "lundi 19 octobre"}, "portee": "serie", "debut": "8h30"}); err != nil {
		t.Fatal(err)
	}
	if got := titles(f.list(map[string]any{"periode": "lundi 26 octobre"})); got != "Point hebdo lundi 26 octobre de 8h30 à 9h30" {
		t.Fatalf("série : %s", got)
	}
	_, err = f.call("update", map[string]any{"cible": map[string]any{"quand": "lundi 26 octobre"}, "portee": "serie", "debut": "mardi 27 octobre 9h"})
	wantCode(t, err, types.ErrInvalidRequest)
}

// ---- Suppression ----

func TestDelete(t *testing.T) {
	f := newFixture(t, alice)
	id := f.create(map[string]any{"titre": "Rendez-vous avec Paul", "debut": "demain 14h"}).Evenement.ID
	f.create(map[string]any{"titre": "Réunion avec Paul", "debut": "vendredi 10h"})
	f.create(map[string]any{"titre": "Réunion équipe", "debut": "vendredi 15h"})

	_, err := f.call("delete", map[string]any{"cible": map[string]any{"quand": "samedi"}})
	wantCode(t, err, types.ErrEventNotFound)

	// « Annule la réunion de vendredi » : deux réunions.
	_, err = f.call("delete", map[string]any{"cible": map[string]any{"quand": "vendredi", "texte": "réunion"}})
	wantCode(t, err, types.ErrEventAmbiguous)
	if f.ag.Calls(agenda.OpDelete) != 0 {
		t.Fatal("rien ne doit être supprimé en cas d'ambiguïté")
	}
	// « Annule la réunion avec Paul vendredi » : un seul candidat.
	if _, err := f.call("delete", map[string]any{"cible": map[string]any{"quand": "vendredi", "texte": "réunion Paul"}}); err != nil {
		t.Fatal(err)
	}
	if out, err := f.call("delete", map[string]any{"event_id": id}); err != nil || out.(agendatools.EventOutput).Evenement.Titre != "Rendez-vous avec Paul" {
		t.Fatalf("suppression par identifiant : %v %v", out, err)
	}
	if _, ok := f.ag.Event(id); ok {
		t.Fatal("événement toujours présent")
	}
	// Déjà supprimé : son identifiant n'est plus valable.
	_, err = f.call("delete", map[string]any{"event_id": id})
	wantCode(t, err, types.ErrInvalidRequest)
	if got := titles(f.list(map[string]any{"periode": "cette semaine"})); got != "Réunion équipe vendredi 2 octobre de 15h à 16h" {
		t.Fatalf("reste : %s", got)
	}
}

func TestDeleteRecurring(t *testing.T) {
	f := newFixture(t, alice)
	f.create(map[string]any{"titre": "Yoga", "debut": "mardi 19h", "recurrence": map[string]any{"frequence": "hebdomadaire", "nombre": 3}})
	_, err := f.call("delete", map[string]any{"cible": map[string]any{"quand": "mardi 13 octobre"}})
	wantCode(t, err, types.ErrMissingInformation) // occurrence ou série ?
	if _, err := f.call("delete", map[string]any{"cible": map[string]any{"quand": "mardi 13 octobre"}, "portee": "occurrence"}); err != nil {
		t.Fatal(err)
	}
	if got := f.list(map[string]any{"du": "2026-10-01", "au": "2026-10-31"}).Nombre; got != 2 {
		t.Fatalf("après suppression d'une occurrence : %d", got)
	}
	if _, err := f.call("delete", map[string]any{"cible": map[string]any{"quand": "mardi 6 octobre"}, "portee": "serie"}); err != nil {
		t.Fatal(err)
	}
	if got := f.list(map[string]any{"du": "2026-10-01", "au": "2026-10-31"}).Nombre; got != 0 {
		t.Fatalf("après suppression de la série : %d", got)
	}
}

// ---- Dates et fuseau ----

func TestDatesAndTimezone(t *testing.T) {
	f := newFixture(t, alice)
	cases := map[string]string{
		"aujourd'hui à 18h":         "2026-09-30T18:00:00+02:00",
		"demain à 9h":               "2026-10-01T09:00:00+02:00",
		"après-demain 8h15":         "2026-10-02T08:15:00+02:00",
		"le 15 octobre à 11 heures": "2026-10-15T11:00:00+02:00",
		"2026-11-03T16:00:00+01:00": "2026-11-03T16:00:00+01:00",
		"2026-11-04T10:00":          "2026-11-04T10:00:00+01:00", // sans fuseau : celui de la session
	}
	for expr, want := range cases {
		e := f.create(map[string]any{"titre": expr, "debut": expr, "ignorer_conflits": true}).Evenement
		if e.Debut != want {
			t.Errorf("%q : début %s, attendu %s", expr, e.Debut, want)
		}
	}

	// Même agenda, utilisateur à New York : « demain à 9h » est 9h à New
	// York, et l'agenda est présenté dans son fuseau.
	ny := newFixtureOn(t, agenda.NewMockProvider(), types.UserContext{ClientID: "c-bob"}, mustLoc("America/New_York"))
	e := ny.create(map[string]any{"titre": "Call", "debut": "demain à 9h"}).Evenement
	if e.Debut != "2026-10-01T09:00:00-04:00" {
		t.Fatalf("New York : %s", e.Debut)
	}
	stored, _ := ny.ag.Event(e.ID)
	if stored.TimeZone != "America/New_York" {
		t.Fatalf("fuseau enregistré : %s", stored.TimeZone)
	}
}

// ---- Sécurité ----

func TestSecurity(t *testing.T) {
	ag := agenda.NewMockProvider()
	a := newFixtureOn(t, ag, alice, paris)
	id := a.create(map[string]any{"titre": "Rendez-vous médical", "debut": "demain 14h"}).Evenement.ID

	// Un autre utilisateur, sur le même agenda : rien n'est visible.
	b := newFixtureOn(t, ag, types.UserContext{ClientID: "c-bob"}, paris)
	if n := b.list(map[string]any{"periode": "demain"}).Nombre; n != 0 {
		t.Fatalf("bob voit %d événements d'alice", n)
	}
	_, err := b.call("delete", map[string]any{"cible": map[string]any{"quand": "demain à 14h"}})
	wantCode(t, err, types.ErrEventNotFound)
	// L'identifiant d'alice n'a jamais été renvoyé à bob : refusé.
	_, err = b.call("update", map[string]any{"event_id": id, "titre": "piraté"})
	wantCode(t, err, types.ErrInvalidRequest)
	if e, _ := ag.Event(id); e.Title != "Rendez-vous médical" {
		t.Fatalf("événement modifié : %+v", e)
	}

	// Sans utilisateur authentifié : aucun accès.
	anon := newFixtureOn(t, ag, types.UserContext{}, paris)
	for _, tool := range []string{"list", "create", "update", "delete"} {
		_, err := anon.call(tool, map[string]any{"periode": "demain", "titre": "x", "debut": "demain 9h", "event_id": id})
		wantCode(t, err, types.ErrNotAuthenticated)
	}
}
