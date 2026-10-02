package datetime

import (
	"errors"
	"testing"
	"time"
)

func TestResolve(t *testing.T) {
	paris, err := time.LoadLocation("Europe/Paris")
	if err != nil {
		t.Fatal(err)
	}
	// Mercredi 30 septembre 2026, 10h00 à Paris.
	now := time.Date(2026, 9, 30, 10, 0, 0, 0, paris)

	cases := []struct {
		expr       string
		start, end string
	}{
		{"Je voudrais un rendez-vous jeudi après-midi", "2026-10-01T12:00:00+02:00", "2026-10-01T18:00:00+02:00"},
		{"demain matin", "2026-10-01T08:00:00+02:00", "2026-10-01T12:00:00+02:00"},
		{"jeudi", "2026-10-01T00:00:00+02:00", "2026-10-02T00:00:00+02:00"},
		{"Plutôt vendredi matin", "2026-10-02T08:00:00+02:00", "2026-10-02T12:00:00+02:00"},
		{"décale-le à lundi", "2026-10-05T00:00:00+02:00", "2026-10-06T00:00:00+02:00"},
		{"mercredi", "2026-10-07T00:00:00+02:00", "2026-10-08T00:00:00+02:00"},
		{"la semaine prochaine", "2026-10-05T00:00:00+02:00", "2026-10-12T00:00:00+02:00"},
		{"aujourd'hui", "2026-09-30T10:00:00+02:00", "2026-10-01T00:00:00+02:00"},
		{"après-demain", "2026-10-02T00:00:00+02:00", "2026-10-03T00:00:00+02:00"},
		{"le 3 octobre à 16h30", "2026-10-03T16:30:00+02:00", "2026-10-03T17:30:00+02:00"},
		{"le 1er octobre vers 9h", "2026-10-01T09:00:00+02:00", "2026-10-01T10:00:00+02:00"},
		// Passage à l'heure d'hiver le 25/10/2026 : le décalage change.
		{"26/10 matin", "2026-10-26T08:00:00+01:00", "2026-10-26T12:00:00+01:00"},
		// Date passée sans année : année suivante.
		{"le 2 janvier", "2027-01-02T00:00:00+01:00", "2027-01-03T00:00:00+01:00"},
	}
	for _, tc := range cases {
		t.Run(tc.expr, func(t *testing.T) {
			p, err := Resolve(tc.expr, now, paris)
			if err != nil {
				t.Fatalf("erreur: %v", err)
			}
			if got := p.Start.Format(time.RFC3339); got != tc.start {
				t.Errorf("start = %s, attendu %s", got, tc.start)
			}
			if got := p.End.Format(time.RFC3339); got != tc.end {
				t.Errorf("end = %s, attendu %s", got, tc.end)
			}
		})
	}
}

func TestResolveErrors(t *testing.T) {
	paris, _ := time.LoadLocation("Europe/Paris")
	now := time.Date(2026, 9, 30, 10, 0, 0, 0, paris)

	cases := []struct {
		expr string
		want error
	}{
		{"16h", ErrMissingDay},
		{"le matin", ErrMissingDay},
		{"quand vous voulez", ErrUnrecognized},
		{"31/02", ErrInvalidDate},
		{"le 12 septembre 2026", ErrPast},
	}
	for _, tc := range cases {
		t.Run(tc.expr, func(t *testing.T) {
			if _, err := Resolve(tc.expr, now, paris); !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, attendu %v", err, tc.want)
			}
		})
	}

	if _, err := Resolve("demain", now, nil); !errors.Is(err, ErrNoLocation) {
		t.Fatalf("fuseau absent : err = %v, attendu ErrNoLocation", err)
	}
}

func TestResolveNoteForWeekWithDayPart(t *testing.T) {
	paris, _ := time.LoadLocation("Europe/Paris")
	now := time.Date(2026, 9, 30, 10, 0, 0, 0, paris)
	p, err := Resolve("la semaine prochaine le matin", now, paris)
	if err != nil {
		t.Fatal(err)
	}
	if p.Granularity != GranularityWeek || p.Note == "" {
		t.Fatalf("attendu une période d'une semaine avec note, obtenu %+v", p)
	}
}
