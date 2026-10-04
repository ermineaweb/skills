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

// Expressions ajoutées pour le skill agenda : elles étaient jusqu'ici
// refusées, les expressions existantes ne changent pas (TestResolve).
func TestResolveRelativeAndWeekend(t *testing.T) {
	paris, _ := time.LoadLocation("Europe/Paris")
	// Mercredi 30 septembre 2026, 10h17 à Paris.
	now := time.Date(2026, 9, 30, 10, 17, 0, 0, paris)
	cases := []struct {
		expr       string
		start, end string
	}{
		{"dans deux heures", "2026-09-30T12:17:00+02:00", "2026-09-30T13:17:00+02:00"},
		{"dans 2h", "2026-09-30T12:17:00+02:00", "2026-09-30T13:17:00+02:00"},
		{"dans 30 minutes", "2026-09-30T10:47:00+02:00", "2026-09-30T11:47:00+02:00"},
		{"dans trois jours", "2026-10-03T00:00:00+02:00", "2026-10-04T00:00:00+02:00"},
		{"dans 3 jours à 14h", "2026-10-03T14:00:00+02:00", "2026-10-03T15:00:00+02:00"},
		{"dans une semaine", "2026-10-07T00:00:00+02:00", "2026-10-08T00:00:00+02:00"},
		{"ce week-end", "2026-10-03T00:00:00+02:00", "2026-10-05T00:00:00+02:00"},
		{"le week-end prochain", "2026-10-03T00:00:00+02:00", "2026-10-05T00:00:00+02:00"},
		{"vendredi prochain", "2026-10-02T00:00:00+02:00", "2026-10-03T00:00:00+02:00"},
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

	// Le dimanche, « ce week-end » est celui en cours, « le week-end
	// prochain » le suivant.
	sunday := time.Date(2026, 10, 4, 10, 0, 0, 0, paris)
	if p, _ := ResolveIncludingPast("ce week-end", sunday, paris); p.Start.Format(time.DateOnly) != "2026-10-03" {
		t.Errorf("ce week-end (dimanche) : %v", p.Start)
	}
	if p, _ := Resolve("le week-end prochain", sunday, paris); p.Start.Format(time.DateOnly) != "2026-10-10" {
		t.Errorf("week-end prochain (dimanche) : %v", p.Start)
	}
}

func TestResolveIncludingPast(t *testing.T) {
	paris, _ := time.LoadLocation("Europe/Paris")
	now := time.Date(2026, 9, 30, 10, 17, 0, 0, paris)
	cases := []struct{ expr, start string }{
		{"aujourd'hui", "2026-09-30T00:00:00+02:00"}, // journée entière
		{"hier", "2026-09-29T00:00:00+02:00"},
		{"avant-hier après-midi", "2026-09-28T12:00:00+02:00"},
		{"aujourd'hui à 9h", "2026-09-30T09:00:00+02:00"},
	}
	for _, tc := range cases {
		p, err := ResolveIncludingPast(tc.expr, now, paris)
		if err != nil || p.Start.Format(time.RFC3339) != tc.start {
			t.Errorf("%s : %v, %v (attendu %s)", tc.expr, p.Start, err, tc.start)
		}
	}
	// Resolve, lui, refuse toujours le passé.
	if _, err := Resolve("hier", now, paris); !errors.Is(err, ErrPast) {
		t.Errorf("Resolve(hier) : %v", err)
	}
}

func TestParseClock(t *testing.T) {
	cases := map[string][2]int{"14h": {14, 0}, "14h30": {14, 30}, "9:05": {9, 5}, "à 9 heures": {9, 0}, "midi": {12, 0}, "minuit": {0, 0}}
	for in, want := range cases {
		h, m, ok := ParseClock(in)
		if !ok || h != want[0] || m != want[1] {
			t.Errorf("ParseClock(%q) = %d:%d %v", in, h, m, ok)
		}
	}
	for _, in := range []string{"25h", "demain", "14h75", ""} {
		if _, _, ok := ParseClock(in); ok {
			t.Errorf("ParseClock(%q) accepté", in)
		}
	}
}

func TestFormatFR(t *testing.T) {
	paris, _ := time.LoadLocation("Europe/Paris")
	d := time.Date(2026, 10, 1, 14, 30, 0, 0, paris)
	if got := FormatFR(d, paris); got != "jeudi 1er octobre à 14h30" {
		t.Errorf("FormatFR = %q", got)
	}
	if got := FormatDayFR(d, nil) + " " + FormatClockFR(d.Add(30*time.Minute), nil); got != "jeudi 1er octobre 15h" {
		t.Errorf("FormatDayFR/FormatClockFR = %q", got)
	}
}
