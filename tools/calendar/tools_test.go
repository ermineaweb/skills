package calendartools_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"skills/services/calendar"
	calendartools "skills/tools/calendar"
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

type fixture struct {
	t     *testing.T
	cal   *calendar.MockProvider
	tc    types.ToolContext
	tools map[string]types.ToolHandler
}

func newFixture(t *testing.T, user types.UserContext) *fixture {
	t.Helper()
	cal, err := calendar.NewDemoProvider(paris, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	return &fixture{
		t:   t,
		cal: cal,
		tc:  types.ToolContext{SessionID: "s1", User: user, Location: paris, Now: now, State: types.NewMemoryState()},
		tools: map[string]types.ToolHandler{
			"rechercher": calendartools.NewRechercherDisponibilites(cal),
			"reserver":   calendartools.NewReserverCreneau(cal),
			"modifier":   calendartools.NewModifierRendezVous(cal),
			"annuler":    calendartools.NewAnnulerRendezVous(cal),
			"lister":     calendartools.NewListerRendezVous(cal),
			"pros":       calendartools.NewListerProfessionnels(cal),
		},
	}
}

var client = types.UserContext{ClientID: "c-42", Name: "Camille Durand", Email: "camille@example.com"}

func (f *fixture) call(tool string, args any) (any, *types.Error) {
	f.t.Helper()
	raw, _ := json.Marshal(args)
	return f.tools[tool].Execute(context.Background(), f.tc, raw)
}

func (f *fixture) search(args map[string]any) (calendartools.RechercherOutput, *types.Error) {
	f.t.Helper()
	out, err := f.call("rechercher", args)
	if err != nil {
		return calendartools.RechercherOutput{}, err
	}
	return out.(calendartools.RechercherOutput), nil
}

func (f *fixture) mustSearch(args map[string]any) []calendartools.SlotView {
	f.t.Helper()
	out, err := f.search(args)
	if err != nil {
		f.t.Fatalf("recherche: %v", err)
	}
	return out.Slots
}

func (f *fixture) mustBook(slotID string) calendartools.AppointmentView {
	f.t.Helper()
	out, err := f.call("reserver", map[string]any{"slot_id": slotID, "type_rendez_vous": "consultation"})
	if err != nil {
		f.t.Fatalf("réservation: %v", err)
	}
	return out.(calendartools.AppointmentOutput).Appointment
}

func period(start, end string) map[string]any {
	return map[string]any{"date_debut": start, "date_fin": end}
}

func with(m map[string]any, k string, v any) map[string]any {
	m[k] = v
	return m
}

func hours(slots []calendartools.SlotView) []string {
	var out []string
	for _, s := range slots {
		out = append(out, s.Start.Format("15:04")+"/"+s.ProfessionnelID)
	}
	return out
}

func wantCode(t *testing.T, err *types.Error, code types.ErrorCode) {
	t.Helper()
	if err == nil || err.Code != code {
		t.Fatalf("erreur = %v, attendu %s", err, code)
	}
}

// ---------- Recherche ----------

func TestRechercheAvecUneDate(t *testing.T) {
	f := newFixture(t, client)
	slots := f.mustSearch(period("2026-10-01T00:00:00+02:00", "2026-10-02T00:00:00+02:00"))
	if len(slots) != 10 {
		t.Fatalf("attendu 10 créneaux jeudi, obtenu %v", hours(slots))
	}
	for _, s := range slots {
		if s.Start.Format("2006-01-02") != "2026-10-01" {
			t.Errorf("créneau hors de la journée : %s", s.Start)
		}
		if !strings.HasSuffix(s.Start.Format(time.RFC3339), "+02:00") {
			t.Errorf("fuseau perdu : %s", s.Start.Format(time.RFC3339))
		}
		if s.ID == "" || s.Libelle == "" {
			t.Errorf("créneau incomplet : %+v", s)
		}
	}
	if slots[0].Libelle != "jeudi 1er octobre à 9h" {
		t.Errorf("libellé = %q", slots[0].Libelle)
	}
}

func TestRechercheAvecUnePlageHoraire(t *testing.T) {
	f := newFixture(t, client)
	slots := f.mustSearch(period("2026-10-01T12:00:00+02:00", "2026-10-01T18:00:00+02:00"))
	got := strings.Join(hours(slots), " ")
	if got != "14:00/paul 15:00/marie 15:30/paul 17:00/paul" {
		t.Fatalf("créneaux = %s", got)
	}
}

func TestRechercheSansDisponibilite(t *testing.T) {
	f := newFixture(t, client)
	// Samedi : aucune ouverture.
	_, err := f.search(period("2026-10-03T00:00:00+02:00", "2026-10-04T00:00:00+02:00"))
	wantCode(t, err, types.ErrNoAvailability)
}

func TestRechercheAvecUnProfessionnel(t *testing.T) {
	f := newFixture(t, client)
	slots := f.mustSearch(with(period("2026-10-01T12:00:00+02:00", "2026-10-01T18:00:00+02:00"), "professionnel_id", "paul"))
	if got := strings.Join(hours(slots), " "); got != "14:00/paul 15:30/paul 17:00/paul" {
		t.Fatalf("créneaux = %s", got)
	}
}

func TestRechercheAvecUnTypeDeRendezVous(t *testing.T) {
	f := newFixture(t, client)
	slots := f.mustSearch(with(period("2026-10-01T00:00:00+02:00", "2026-10-02T00:00:00+02:00"), "type_rendez_vous", "bilan"))
	// Seul Paul propose des bilans (60 min) : seule sa plage 10h30-12h le permet.
	if len(slots) != 1 || slots[0].ProfessionnelID != "paul" || slots[0].End.Sub(slots[0].Start) != time.Hour {
		t.Fatalf("créneaux = %v", hours(slots))
	}
}

func TestRechercheTronqueLesResultats(t *testing.T) {
	f := newFixture(t, client)
	out, err := f.search(period("2026-10-05T00:00:00+02:00", "2026-10-12T00:00:00+02:00"))
	if err != nil {
		t.Fatal(err)
	}
	if !out.Tronque || len(out.Slots) != calendartools.MaxSlotsReturned || out.Total <= len(out.Slots) {
		t.Fatalf("troncature attendue : %d créneaux, total %d", len(out.Slots), out.Total)
	}
}

func TestRechercheRefuseLesDatesNonStructurees(t *testing.T) {
	f := newFixture(t, client)
	for _, debut := range []string{"jeudi après-midi", "2026-10-01T12:00:00", "demain"} {
		_, err := f.search(period(debut, "2026-10-01T18:00:00+02:00"))
		wantCode(t, err, types.ErrInvalidRequest)
	}
	if f.cal.Calls(calendar.OpSearch) != 0 {
		t.Fatal("le calendrier ne doit pas recevoir de date non structurée")
	}
}

func TestRechercheProfessionnelInconnu(t *testing.T) {
	f := newFixture(t, client)
	_, err := f.search(with(period("2026-10-01T12:00:00+02:00", "2026-10-01T18:00:00+02:00"), "professionnel_id", "jacques"))
	wantCode(t, err, types.ErrInvalidRequest)
}

func TestRechercheCalendrierIndisponible(t *testing.T) {
	f := newFixture(t, client)
	f.cal.SetUnavailable(true)
	_, err := f.search(period("2026-10-01T12:00:00+02:00", "2026-10-01T18:00:00+02:00"))
	wantCode(t, err, types.ErrCalendarUnavailable)
	if strings.Contains(err.Message, "dial") || strings.Contains(err.Message, "10.0.0.1") {
		t.Fatalf("détail technique exposé : %q", err.Message)
	}
}

// ---------- Réservation ----------

func thursdayPaulAfternoon(f *fixture) []calendartools.SlotView {
	return f.mustSearch(with(period("2026-10-01T12:00:00+02:00", "2026-10-01T18:00:00+02:00"), "professionnel_id", "paul"))
}

func TestReservationReussie(t *testing.T) {
	f := newFixture(t, client)
	slots := thursdayPaulAfternoon(f)
	appt := f.mustBook(slots[1].ID) // 15h30

	if appt.ID == "" || appt.Start.Format(time.RFC3339) != "2026-10-01T15:30:00+02:00" {
		t.Fatalf("rendez-vous = %+v", appt)
	}
	real, ok := f.cal.Appointment(appt.ID)
	if !ok || real.Status != types.StatusConfirmed || real.ClientID != "c-42" || real.ClientName != "Camille Durand" {
		t.Fatalf("état réel du calendrier = %+v", real)
	}
	// Le créneau n'est plus proposé.
	if got := strings.Join(hours(thursdayPaulAfternoon(f)), " "); got != "14:00/paul 17:00/paul" {
		t.Fatalf("créneaux après réservation = %s", got)
	}
}

func TestReservationCreneauDevenuIndisponible(t *testing.T) {
	f := newFixture(t, client)
	slots := thursdayPaulAfternoon(f)
	if err := f.cal.OccupySlot(slots[1].ID); err != nil { // réservé par un tiers entre-temps
		t.Fatal(err)
	}
	_, err := f.call("reserver", map[string]any{"slot_id": slots[1].ID, "type_rendez_vous": "consultation"})
	wantCode(t, err, types.ErrSlotNoLongerAvailable)
}

func TestReservationErreurDuCalendrier(t *testing.T) {
	cases := []struct {
		name  string
		setup func(*calendar.MockProvider)
		want  types.ErrorCode
	}{
		{"panne technique", func(c *calendar.MockProvider) { c.SetUnavailable(true) }, types.ErrCalendarUnavailable},
		{"échec métier", func(c *calendar.MockProvider) { c.FailNext(calendar.OpBook, types.ErrBookingFailed) }, types.ErrBookingFailed},
		{"réponse non confirmée", func(c *calendar.MockProvider) { c.UnconfirmedNext(calendar.OpBook) }, types.ErrBookingFailed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t, client)
			slots := thursdayPaulAfternoon(f)
			tc.setup(f.cal)
			out, err := f.call("reserver", map[string]any{"slot_id": slots[0].ID, "type_rendez_vous": "consultation"})
			wantCode(t, err, tc.want)
			if out != nil {
				t.Fatalf("aucun résultat ne doit être renvoyé en cas d'échec : %+v", out)
			}
			f.cal.SetUnavailable(false)
			if got := thursdayPaulAfternoon(f); len(got) != 3 {
				t.Fatalf("aucun rendez-vous ne doit avoir été créé : %v", hours(got))
			}
		})
	}
}

func TestReservationDonneesObligatoiresManquantes(t *testing.T) {
	f := newFixture(t, types.UserContext{}) // utilisateur anonyme
	slots := thursdayPaulAfternoon(f)
	_, err := f.call("reserver", map[string]any{"slot_id": slots[0].ID, "type_rendez_vous": ""})
	wantCode(t, err, types.ErrMissingInformation)
	if strings.Join(err.Missing, ",") != "client_name,type_rendez_vous" {
		t.Fatalf("missing = %v", err.Missing)
	}
	if f.cal.Calls(calendar.OpBook) != 0 {
		t.Fatal("le calendrier ne doit pas être appelé")
	}

	// Avec le nom fourni par l'utilisateur, la réservation passe.
	out, err := f.call("reserver", map[string]any{"slot_id": slots[0].ID, "type_rendez_vous": "consultation", "client_name": "Léa"})
	if err != nil {
		t.Fatal(err)
	}
	if real, _ := f.cal.Appointment(out.(calendartools.AppointmentOutput).Appointment.ID); real.ClientName != "Léa" {
		t.Fatalf("nom = %q", real.ClientName)
	}
}

func TestReservationRefuseUnSlotInvente(t *testing.T) {
	f := newFixture(t, client)
	_, err := f.call("reserver", map[string]any{"slot_id": "slot_paul_20261001T1400Z_30", "type_rendez_vous": "consultation"})
	wantCode(t, err, types.ErrInvalidRequest)
	if f.cal.Calls(calendar.OpBook) != 0 {
		t.Fatal("un slot_id inventé ne doit jamais atteindre le calendrier")
	}
}

func TestReservationRefuseUnAutreClient(t *testing.T) {
	f := newFixture(t, client)
	slots := thursdayPaulAfternoon(f)
	_, err := f.call("reserver", map[string]any{"slot_id": slots[0].ID, "type_rendez_vous": "consultation", "client_id": "c-99"})
	wantCode(t, err, types.ErrInvalidRequest)
}

// ---------- Modification ----------

func TestModificationReussie(t *testing.T) {
	f := newFixture(t, client)
	appt := f.mustBook(thursdayPaulAfternoon(f)[0].ID) // jeudi 14h
	friday := f.mustSearch(with(period("2026-10-02T08:00:00+02:00", "2026-10-02T12:00:00+02:00"), "professionnel_id", "paul"))

	out, err := f.call("modifier", map[string]any{"appointment_id": appt.ID, "new_slot_id": friday[0].ID})
	if err != nil {
		t.Fatal(err)
	}
	moved := out.(calendartools.AppointmentOutput).Appointment
	if moved.ID != appt.ID || moved.Start.Format(time.RFC3339) != "2026-10-02T09:00:00+02:00" {
		t.Fatalf("rendez-vous déplacé = %+v", moved)
	}
	if real, _ := f.cal.Appointment(appt.ID); !real.Start.Equal(moved.Start) {
		t.Fatalf("le calendrier n'a pas été mis à jour : %+v", real)
	}
}

func TestModificationNouveauCreneauIndisponible(t *testing.T) {
	f := newFixture(t, client)
	appt := f.mustBook(thursdayPaulAfternoon(f)[0].ID)
	friday := f.mustSearch(with(period("2026-10-02T08:00:00+02:00", "2026-10-02T12:00:00+02:00"), "professionnel_id", "paul"))
	_ = f.cal.OccupySlot(friday[0].ID)

	_, err := f.call("modifier", map[string]any{"appointment_id": appt.ID, "new_slot_id": friday[0].ID})
	wantCode(t, err, types.ErrSlotNoLongerAvailable)
	if real, _ := f.cal.Appointment(appt.ID); !real.Start.Equal(appt.Start) {
		t.Fatal("le rendez-vous initial doit être inchangé")
	}
}

func TestModificationRendezVousInexistant(t *testing.T) {
	f := newFixture(t, client)
	appt := f.mustBook(thursdayPaulAfternoon(f)[0].ID)
	friday := f.mustSearch(with(period("2026-10-02T08:00:00+02:00", "2026-10-02T12:00:00+02:00"), "professionnel_id", "paul"))

	// Identifiant jamais renvoyé par un tool : refusé avant le calendrier.
	_, err := f.call("modifier", map[string]any{"appointment_id": "rdv_999", "new_slot_id": friday[0].ID})
	wantCode(t, err, types.ErrInvalidRequest)

	// Rendez-vous supprimé côté back-office : le calendrier fait foi.
	f.cal.DeleteAppointment(appt.ID)
	_, err = f.call("modifier", map[string]any{"appointment_id": appt.ID, "new_slot_id": friday[0].ID})
	wantCode(t, err, types.ErrAppointmentNotFound)
}

func TestModificationNonConfirmee(t *testing.T) {
	f := newFixture(t, client)
	appt := f.mustBook(thursdayPaulAfternoon(f)[0].ID)
	friday := f.mustSearch(with(period("2026-10-02T08:00:00+02:00", "2026-10-02T12:00:00+02:00"), "professionnel_id", "paul"))
	f.cal.UnconfirmedNext(calendar.OpUpdate)
	_, err := f.call("modifier", map[string]any{"appointment_id": appt.ID, "new_slot_id": friday[0].ID})
	wantCode(t, err, types.ErrUpdateFailed)
}

// ---------- Annulation ----------

func TestAnnulationReussie(t *testing.T) {
	f := newFixture(t, client)
	appt := f.mustBook(thursdayPaulAfternoon(f)[0].ID)
	out, err := f.call("annuler", map[string]any{"appointment_id": appt.ID})
	if err != nil {
		t.Fatal(err)
	}
	if out.(calendartools.AppointmentOutput).Appointment.Status != string(types.StatusCancelled) {
		t.Fatalf("statut = %+v", out)
	}
	if real, _ := f.cal.Appointment(appt.ID); real.Status != types.StatusCancelled {
		t.Fatal("le calendrier doit refléter l'annulation")
	}
	// Le créneau est de nouveau disponible.
	if len(thursdayPaulAfternoon(f)) != 3 {
		t.Fatal("le créneau libéré doit être à nouveau proposé")
	}
}

func TestAnnulationRendezVousInexistant(t *testing.T) {
	f := newFixture(t, client)
	_, err := f.call("annuler", map[string]any{"appointment_id": "rdv_001"})
	wantCode(t, err, types.ErrInvalidRequest)

	appt := f.mustBook(thursdayPaulAfternoon(f)[0].ID)
	f.cal.DeleteAppointment(appt.ID)
	_, err = f.call("annuler", map[string]any{"appointment_id": appt.ID})
	wantCode(t, err, types.ErrAppointmentNotFound)
}

func TestAnnulationErreurDuCalendrier(t *testing.T) {
	cases := []struct {
		name  string
		setup func(*calendar.MockProvider)
		want  types.ErrorCode
	}{
		{"panne technique", func(c *calendar.MockProvider) { c.SetUnavailable(true) }, types.ErrCalendarUnavailable},
		{"échec métier", func(c *calendar.MockProvider) { c.FailNext(calendar.OpCancel, types.ErrCancelFailed) }, types.ErrCancelFailed},
		{"réponse non confirmée", func(c *calendar.MockProvider) { c.UnconfirmedNext(calendar.OpCancel) }, types.ErrCancelFailed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t, client)
			appt := f.mustBook(thursdayPaulAfternoon(f)[0].ID)
			tc.setup(f.cal)
			_, err := f.call("annuler", map[string]any{"appointment_id": appt.ID})
			wantCode(t, err, tc.want)
			if real, _ := f.cal.Appointment(appt.ID); real.Status != types.StatusConfirmed {
				t.Fatal("le rendez-vous doit rester actif")
			}
		})
	}
}

// ---------- Listes ----------

func TestListerRendezVous(t *testing.T) {
	f := newFixture(t, client)
	appt := f.mustBook(thursdayPaulAfternoon(f)[0].ID)

	// Une nouvelle conversation (nouvel état) retrouve le rendez-vous et peut l'annuler.
	f.tc.State = types.NewMemoryState()
	out, err := f.call("lister", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	list := out.(calendartools.ListerRendezVousOutput).RendezVous
	if len(list) != 1 || list[0].ID != appt.ID {
		t.Fatalf("liste = %+v", list)
	}
	if _, err := f.call("annuler", map[string]any{"appointment_id": appt.ID}); err != nil {
		t.Fatal(err)
	}

	// Un autre client ne voit rien.
	f.tc.User = types.UserContext{ClientID: "c-99", Name: "Autre"}
	out, _ = f.call("lister", map[string]any{})
	if len(out.(calendartools.ListerRendezVousOutput).RendezVous) != 0 {
		t.Fatal("fuite de rendez-vous d'un autre client")
	}

	// Utilisateur anonyme : information manquante.
	f.tc.User = types.UserContext{}
	_, err = f.call("lister", map[string]any{})
	wantCode(t, err, types.ErrMissingInformation)
}

func TestListerProfessionnels(t *testing.T) {
	f := newFixture(t, client)
	out, err := f.call("pros", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	pros := out.(calendartools.ListerProfessionnelsOutput).Professionnels
	if len(pros) != 2 || pros[1].ID != "paul" || pros[1].Nom != "Paul Martin" {
		t.Fatalf("professionnels = %+v", pros)
	}
}
