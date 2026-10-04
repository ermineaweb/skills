package agenda

import (
	"context"
	"errors"
	"testing"
	"time"

	"skills/types"
)

var paris, _ = time.LoadLocation("Europe/Paris")

func at(y int, m time.Month, d, h, mi int) time.Time { return time.Date(y, m, d, h, mi, 0, 0, paris) }

func starts(occ []Occurrence) []string {
	out := make([]string, len(occ))
	for i, o := range occ {
		out[i] = o.Start.Format("2006-01-02 15:04 -07")
	}
	return out
}

func eq(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("occurrences = %v, attendu %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("occurrences = %v, attendu %v", got, want)
		}
	}
}

func TestOccurrences(t *testing.T) {
	until := at(2026, 10, 19, 0, 0)
	cases := []struct {
		name string
		e    Event
		from time.Time
		to   time.Time
		want []string
	}{
		{
			"tous les lundis à 9h, jusqu'au 19 octobre",
			Event{Start: at(2026, 10, 5, 9, 0), End: at(2026, 10, 5, 10, 0), Recurrence: &Recurrence{Frequency: Weekly, Until: &until}},
			at(2026, 10, 1, 0, 0), at(2026, 12, 1, 0, 0),
			[]string{"2026-10-05 09:00 +02", "2026-10-12 09:00 +02", "2026-10-19 09:00 +02"},
		},
		{
			// Passage à l'heure d'hiver le 25 octobre : 9h reste 9h.
			"lundi et mercredi, 4 fois, à travers le changement d'heure",
			Event{Start: at(2026, 10, 21, 9, 0), End: at(2026, 10, 21, 10, 0), Recurrence: &Recurrence{Frequency: Weekly, Weekdays: []time.Weekday{time.Wednesday, time.Monday}, Count: 4}},
			at(2026, 10, 1, 0, 0), at(2026, 12, 1, 0, 0),
			[]string{"2026-10-21 09:00 +02", "2026-10-26 09:00 +01", "2026-10-28 09:00 +01", "2026-11-02 09:00 +01"},
		},
		{
			"premier lundi du mois",
			Event{Start: at(2026, 10, 5, 9, 0), End: at(2026, 10, 5, 10, 0), Recurrence: &Recurrence{Frequency: Monthly, Weekdays: []time.Weekday{time.Monday}, Position: 1}},
			at(2026, 10, 1, 0, 0), at(2027, 1, 1, 0, 0),
			[]string{"2026-10-05 09:00 +02", "2026-11-02 09:00 +01", "2026-12-07 09:00 +01"},
		},
		{
			"dernier vendredi du mois",
			Event{Start: at(2026, 10, 30, 17, 0), End: at(2026, 10, 30, 18, 0), Recurrence: &Recurrence{Frequency: Monthly, Weekdays: []time.Weekday{time.Friday}, Position: -1}},
			at(2026, 10, 1, 0, 0), at(2027, 1, 1, 0, 0),
			[]string{"2026-10-30 17:00 +01", "2026-11-27 17:00 +01", "2026-12-25 17:00 +01"},
		},
		{
			"le 31 de chaque mois : les mois sans 31 sont sautés",
			Event{Start: at(2026, 10, 31, 8, 0), End: at(2026, 10, 31, 9, 0), Recurrence: &Recurrence{Frequency: Monthly}},
			at(2026, 10, 1, 0, 0), at(2027, 2, 1, 0, 0),
			[]string{"2026-10-31 08:00 +01", "2026-12-31 08:00 +01", "2027-01-31 08:00 +01"},
		},
		{
			"un jour sur deux, avec une occurrence retirée",
			Event{Start: at(2026, 10, 1, 7, 0), End: at(2026, 10, 1, 8, 0), Recurrence: &Recurrence{Frequency: Daily, Interval: 2}, Exceptions: []time.Time{at(2026, 10, 3, 7, 0)}},
			at(2026, 10, 1, 0, 0), at(2026, 10, 8, 0, 0),
			[]string{"2026-10-01 07:00 +02", "2026-10-05 07:00 +02", "2026-10-07 07:00 +02"},
		},
		{
			"période qui commence en cours d'occurrence",
			Event{Start: at(2026, 10, 1, 9, 0), End: at(2026, 10, 1, 11, 0)},
			at(2026, 10, 1, 10, 0), at(2026, 10, 1, 12, 0),
			[]string{"2026-10-01 09:00 +02"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			eq(t, starts(Occurrences(c.e, c.from, c.to, 0)), c.want)
		})
	}
}

func TestRecurrenceValidateAndRRULE(t *testing.T) {
	start := at(2026, 10, 5, 9, 0)
	until := at(2026, 12, 31, 0, 0)
	r := Recurrence{Frequency: Weekly, Interval: 2, Weekdays: []time.Weekday{time.Monday, time.Wednesday}, Until: &until}
	if err := r.Validate(start); err != nil {
		t.Fatal(err)
	}
	if got := r.RRULE(); got != "FREQ=WEEKLY;INTERVAL=2;BYDAY=MO,WE;UNTIL=20261231T225959Z" {
		t.Errorf("RRULE = %s", got)
	}
	first := Recurrence{Frequency: Monthly, Weekdays: []time.Weekday{time.Monday}, Position: 1, Count: 6}
	if got := first.RRULE(); got != "FREQ=MONTHLY;BYDAY=1MO;COUNT=6" {
		t.Errorf("RRULE = %s", got)
	}
	bad := []Recurrence{
		{Frequency: "HOURLY"},
		{Frequency: Daily, Weekdays: []time.Weekday{time.Monday}},
		{Frequency: Weekly, Position: 1, Weekdays: []time.Weekday{time.Monday}},
		{Frequency: Weekly, Count: 3, Until: &until},
		{Frequency: Weekly, Until: ptr(at(2026, 9, 1, 0, 0))},
	}
	for _, r := range bad {
		var te *types.Error
		if err := r.Validate(start); !errors.As(err, &te) || te.Code != types.ErrInvalidRequest {
			t.Errorf("%+v : erreur = %v", r, err)
		}
	}
}

func ptr[T any](v T) *T { return &v }

func TestMatches(t *testing.T) {
	e := Event{Title: "Réunion équipe", Location: "Salle Hélios", Attendees: []string{"Paul Martin"}}
	for q, want := range map[string]bool{"": true, "réunion": true, "REUNION avec Paul": true, "helios": true, "réunion client": false, "Marie": false} {
		if got := Matches(e, q); got != want {
			t.Errorf("Matches(%q) = %v", q, got)
		}
	}
}

func TestMockOwnershipAndOccurrences(t *testing.T) {
	ctx := context.Background()
	m := NewMockProvider()
	res, err := m.CreateEvent(ctx, CreateRequest{OwnerID: "alice", Event: Event{
		Title: "Point hebdo", Start: at(2026, 10, 5, 9, 0), End: at(2026, 10, 5, 10, 0), TimeZone: "Europe/Paris",
		Recurrence: &Recurrence{Frequency: Weekly},
	}})
	if err != nil || !res.Created {
		t.Fatalf("création : %+v, %v", res, err)
	}
	id := res.Event.ID

	// Un autre utilisateur ne voit, ne modifie ni ne supprime rien.
	l, _ := m.ListEvents(ctx, ListRequest{OwnerID: "bob", From: at(2026, 10, 1, 0, 0), To: at(2026, 11, 1, 0, 0)})
	if len(l.Occurrences) != 0 {
		t.Fatalf("bob voit %d occurrences", len(l.Occurrences))
	}
	title := "piraté"
	_, err = m.UpdateEvent(ctx, UpdateRequest{OwnerID: "bob", EventID: id, Changes: Changes{Title: &title}})
	if !isCode(err, types.ErrEventNotFound) {
		t.Fatalf("modification par bob : %v", err)
	}
	if _, err = m.DeleteEvent(ctx, DeleteRequest{OwnerID: "bob", EventID: id}); !isCode(err, types.ErrEventNotFound) {
		t.Fatalf("suppression par bob : %v", err)
	}
	if _, err = m.ListEvents(ctx, ListRequest{From: at(2026, 10, 1, 0, 0), To: at(2026, 11, 1, 0, 0)}); !isCode(err, types.ErrNotAuthenticated) {
		t.Fatalf("sans propriétaire : %v", err)
	}

	// Déplacer une seule occurrence la détache de la série.
	occ, newStart := at(2026, 10, 12, 9, 0), at(2026, 10, 12, 16, 0)
	newEnd := newStart.Add(time.Hour)
	u, err := m.UpdateEvent(ctx, UpdateRequest{OwnerID: "alice", EventID: id, Occurrence: &occ, Changes: Changes{Start: &newStart, End: &newEnd}})
	if err != nil || !u.Updated || u.Event.ID == id || u.Event.Recurrence != nil {
		t.Fatalf("occurrence détachée : %+v, %v", u, err)
	}
	// Supprimer une autre occurrence la retire de la série.
	occ2 := at(2026, 10, 19, 9, 0)
	if d, err := m.DeleteEvent(ctx, DeleteRequest{OwnerID: "alice", EventID: id, Occurrence: &occ2}); err != nil || !d.Deleted {
		t.Fatalf("suppression d'occurrence : %+v, %v", d, err)
	}
	l, _ = m.ListEvents(ctx, ListRequest{OwnerID: "alice", From: at(2026, 10, 1, 0, 0), To: at(2026, 10, 27, 0, 0)})
	eq(t, starts(l.Occurrences), []string{"2026-10-05 09:00 +02", "2026-10-12 16:00 +02", "2026-10-26 09:00 +01"})

	// Une occurrence inexistante est introuvable.
	bogus := at(2026, 10, 13, 9, 0)
	if _, err := m.DeleteEvent(ctx, DeleteRequest{OwnerID: "alice", EventID: id, Occurrence: &bogus}); !isCode(err, types.ErrEventNotFound) {
		t.Fatalf("occurrence inexistante : %v", err)
	}
}

func TestMockFailures(t *testing.T) {
	ctx := context.Background()
	m := NewMockProvider()
	ev := Event{Title: "Test", Start: at(2026, 10, 5, 9, 0), End: at(2026, 10, 5, 10, 0)}
	m.UnconfirmedNext(OpCreate)
	if r, err := m.CreateEvent(ctx, CreateRequest{OwnerID: "a", Event: ev}); err != nil || r.Created {
		t.Fatalf("non confirmé : %+v, %v", r, err)
	}
	m.FailNext(OpCreate, types.ErrPermissionDenied)
	if _, err := m.CreateEvent(ctx, CreateRequest{OwnerID: "a", Event: ev}); !isCode(err, types.ErrPermissionDenied) {
		t.Fatalf("permission : %v", err)
	}
	m.SetUnavailable(true)
	var te *types.Error
	if _, err := m.CreateEvent(ctx, CreateRequest{OwnerID: "a", Event: ev}); err == nil || errors.As(err, &te) {
		t.Fatalf("panne technique : %v", err)
	}
}

func isCode(err error, code types.ErrorCode) bool {
	var te *types.Error
	return errors.As(err, &te) && te.Code == code
}
