package agenda

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"skills/types"
)

// Frequency est la fréquence d'une récurrence (FREQ d'une RRULE).
type Frequency string

const (
	Daily   Frequency = "DAILY"
	Weekly  Frequency = "WEEKLY"
	Monthly Frequency = "MONTHLY"
	Yearly  Frequency = "YEARLY"
)

// Recurrence est une règle de récurrence : sous-ensemble de la RRULE de la
// RFC 5545, que les fournisseurs d'agenda gèrent nativement (voir RRULE).
type Recurrence struct {
	Frequency Frequency `json:"frequency"`
	// Interval : toutes les N périodes (0 ou 1 : chaque période).
	Interval int `json:"interval,omitempty"`
	// Weekdays (BYDAY) : jours de la semaine (Weekly, ou Monthly).
	Weekdays []time.Weekday `json:"weekdays,omitempty"`
	// Position (Monthly, un seul jour) : 1 = premier … 5, -1 = dernier
	// (« le premier lundi du mois »).
	Position int `json:"position,omitempty"`
	// Until : dernier jour possible (inclus), à minuit dans le fuseau de
	// l'événement. Exclusif avec Count.
	Until *time.Time `json:"until,omitempty"`
	// Count : nombre d'occurrences. Exclusif avec Until.
	Count int `json:"count,omitempty"`
}

// maxPeriods borne le développement d'une série sans fin.
const maxPeriods = 50000

// Validate vérifie la cohérence de la règle pour un événement commençant à
// start. L'erreur est un *types.Error (INVALID_REQUEST) explicable à
// l'utilisateur.
func (r Recurrence) Validate(start time.Time) error {
	invalid := func(format string, args ...any) error { return types.Errorf(types.ErrInvalidRequest, format, args...) }
	switch r.Frequency {
	case Daily, Weekly, Monthly, Yearly:
	default:
		return invalid("Fréquence de récurrence inconnue.")
	}
	if r.Interval < 0 || r.Interval > 99 {
		return invalid("Intervalle de récurrence invalide (1 à 99).")
	}
	if len(r.Weekdays) > 0 && r.Frequency != Weekly && r.Frequency != Monthly {
		return invalid("Les jours de la semaine ne s'appliquent qu'à une récurrence hebdomadaire ou mensuelle.")
	}
	if r.Position != 0 {
		if r.Frequency != Monthly || len(r.Weekdays) != 1 {
			return invalid("La position (« le premier lundi ») s'applique à une récurrence mensuelle avec un seul jour de la semaine.")
		}
		if r.Position < -1 || r.Position > 5 {
			return invalid("Position invalide : 1 à 5, ou -1 pour le dernier.")
		}
	}
	if r.Until != nil && r.Count > 0 {
		return invalid("Une récurrence a soit une date de fin, soit un nombre d'occurrences, pas les deux.")
	}
	if r.Count < 0 || r.Count > 1000 {
		return invalid("Nombre d'occurrences invalide (1 à 1000).")
	}
	if r.Until != nil && r.Until.Before(dayStart(start)) {
		return invalid("La date de fin de la récurrence précède le premier événement.")
	}
	return nil
}

// RRULE renvoie la règle au format de la RFC 5545 (sans le préfixe
// « RRULE: »), pour un fournisseur qui la gère nativement. UNTIL est la fin
// du dernier jour, en UTC.
func (r Recurrence) RRULE() string {
	parts := []string{"FREQ=" + string(r.Frequency)}
	if r.Interval > 1 {
		parts = append(parts, fmt.Sprintf("INTERVAL=%d", r.Interval))
	}
	if len(r.Weekdays) > 0 {
		days := make([]string, len(r.Weekdays))
		for i, d := range r.Weekdays {
			days[i] = rruleDays[d]
			if r.Position != 0 {
				days[i] = fmt.Sprint(r.Position) + days[i]
			}
		}
		parts = append(parts, "BYDAY="+strings.Join(days, ","))
	}
	if r.Count > 0 {
		parts = append(parts, fmt.Sprintf("COUNT=%d", r.Count))
	}
	if r.Until != nil {
		end := r.Until.AddDate(0, 0, 1).Add(-time.Second)
		parts = append(parts, "UNTIL="+end.UTC().Format("20060102T150405Z"))
	}
	return strings.Join(parts, ";")
}

var rruleDays = [...]string{"SU", "MO", "TU", "WE", "TH", "FR", "SA"}

// Occurrences renvoie les occurrences de e qui chevauchent [from, to[, au
// plus max (0 : sans limite), dans l'ordre. Une série est développée dans le
// fuseau de son premier début : l'heure locale est conservée lors des
// changements d'heure.
func Occurrences(e Event, from, to time.Time, max int) []Occurrence {
	dur := e.End.Sub(e.Start)
	if e.Recurrence == nil {
		if e.Start.Before(to) && e.End.After(from) {
			return []Occurrence{{Event: e, Start: e.Start, End: e.End}}
		}
		return nil
	}
	r := *e.Recurrence
	var out []Occurrence
	n := 0
	r.starts(e.Start, func(start time.Time) bool {
		n++
		if (r.Count > 0 && n > r.Count) || (r.Until != nil && !start.Before(r.Until.AddDate(0, 0, 1))) || !start.Before(to) {
			return false
		}
		if start.Add(dur).After(from) && !slices.ContainsFunc(e.Exceptions, start.Equal) {
			out = append(out, Occurrence{Event: e, Start: start, End: start.Add(dur)})
		}
		return max == 0 || len(out) < max
	})
	return out
}

// IsOccurrence indique si start est le début d'une occurrence (non
// retirée) de la série e.
func IsOccurrence(e Event, start time.Time) bool {
	for _, o := range Occurrences(e, start, start.Add(time.Nanosecond), 0) {
		if o.Start.Equal(start) {
			return true
		}
	}
	return false
}

// starts appelle yield pour chaque début de la série, dans l'ordre, à
// partir de dtstart, tant que yield renvoie true.
func (r Recurrence) starts(dtstart time.Time, yield func(time.Time) bool) {
	loc := dtstart.Location()
	iv := max(r.Interval, 1)
	at := func(y int, m time.Month, d int) (time.Time, bool) {
		t := time.Date(y, m, d, dtstart.Hour(), dtstart.Minute(), dtstart.Second(), 0, loc)
		return t, t.Month() == m && t.Day() == d
	}
	emit := func(t time.Time, ok bool) bool {
		if !ok || t.Before(dtstart) {
			return true
		}
		return yield(t)
	}
	days := r.Weekdays
	if len(days) == 0 && r.Frequency == Weekly {
		days = []time.Weekday{dtstart.Weekday()}
	}
	days = slices.Clone(days)
	slices.SortFunc(days, func(a, b time.Weekday) int { return mondayIndex(a) - mondayIndex(b) })

	for k := 0; k < maxPeriods; k++ {
		switch r.Frequency {
		case Daily:
			d := dtstart.AddDate(0, 0, k*iv)
			if !emit(at(d.Year(), d.Month(), d.Day())) {
				return
			}
		case Weekly:
			monday := dayStart(dtstart).AddDate(0, 0, -mondayIndex(dtstart.Weekday())+7*k*iv)
			for _, wd := range days {
				d := monday.AddDate(0, 0, mondayIndex(wd))
				if !emit(at(d.Year(), d.Month(), d.Day())) {
					return
				}
			}
		case Monthly:
			first := time.Date(dtstart.Year(), dtstart.Month()+time.Month(k*iv), 1, 0, 0, 0, 0, loc)
			y, m := first.Year(), first.Month()
			switch {
			case r.Position != 0:
				if !emit(nthWeekday(y, m, days[0], r.Position, at)) {
					return
				}
			case len(days) > 0:
				for d := 1; d <= 31; d++ {
					if t, ok := at(y, m, d); ok && slices.Contains(days, t.Weekday()) && !emit(t, true) {
						return
					}
				}
			default:
				if !emit(at(y, m, dtstart.Day())) {
					return
				}
			}
		case Yearly:
			if !emit(at(dtstart.Year()+k*iv, dtstart.Month(), dtstart.Day())) {
				return
			}
		default:
			return
		}
	}
}

// nthWeekday renvoie le n-ième jour wd du mois (n = -1 : le dernier).
func nthWeekday(y int, m time.Month, wd time.Weekday, n int, at func(int, time.Month, int) (time.Time, bool)) (time.Time, bool) {
	if n == -1 {
		last := time.Date(y, m+1, 0, 0, 0, 0, 0, time.UTC).Day()
		for d := last; d > last-7; d-- {
			if t, ok := at(y, m, d); ok && t.Weekday() == wd {
				return t, true
			}
		}
		return time.Time{}, false
	}
	for d := 1; d <= 7; d++ {
		if t, ok := at(y, m, d); ok && t.Weekday() == wd {
			return at(y, m, d+7*(n-1))
		}
	}
	return time.Time{}, false
}

func mondayIndex(d time.Weekday) int { return (int(d) + 6) % 7 }

func dayStart(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
}

// Matches indique si e correspond à la recherche q : chaque mot significatif
// de q (accents et casse ignorés) apparaît dans le titre, la description,
// le lieu ou les participants. Une recherche vide correspond à tout.
func Matches(e Event, q string) bool {
	hay := normalize(strings.Join(append([]string{e.Title, e.Description, e.Location}, e.Attendees...), " "))
	for _, w := range strings.Fields(normalize(q)) {
		if len(w) < 2 || stopWords[w] {
			continue
		}
		if !strings.Contains(hay, w) {
			return false
		}
	}
	return true
}

var stopWords = map[string]bool{
	"avec": true, "pour": true, "les": true, "des": true, "une": true, "un": true, "le": true, "la": true,
	"de": true, "du": true, "mon": true, "ma": true, "mes": true, "chez": true, "et": true, "au": true, "aux": true,
}

var accents = strings.NewReplacer(
	"é", "e", "è", "e", "ê", "e", "ë", "e", "à", "a", "â", "a", "ä", "a", "ù", "u", "û", "u", "ü", "u",
	"ô", "o", "ö", "o", "î", "i", "ï", "i", "ç", "c",
	"'", " ", "’", " ", "-", " ", ",", " ", ".", " ", "?", " ", "!", " ", "«", " ", "»", " ",
)

func normalize(s string) string {
	return strings.Join(strings.Fields(accents.Replace(strings.ToLower(s))), " ")
}
