// Package datetime convertit des expressions temporelles françaises
// ("jeudi après-midi", "demain matin", "la semaine prochaine"…) en intervalles
// ISO 8601 explicites, dans un fuseau horaire fourni.
//
// Le calcul est déterministe : il ne dépend ni du modèle d'IA ni de l'heure
// système, mais de l'instant `now` et du fuseau passés en paramètre.
package datetime

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	// Embarque la base des fuseaux horaires : le résultat ne dépend pas du
	// système hôte (images Docker minimales, etc.).
	_ "time/tzdata"
)

// Granularity indique la précision de l'intervalle résolu.
type Granularity string

const (
	GranularityWeek    Granularity = "semaine"
	GranularityWeekend Granularity = "week_end"
	GranularityDay     Granularity = "jour"
	GranularityDayPart Granularity = "partie_de_journee"
	GranularityHour    Granularity = "heure"
)

// Period est un intervalle [Start, End[ exprimé dans le fuseau demandé.
type Period struct {
	Start       time.Time
	End         time.Time
	Granularity Granularity
	// Note signale une partie de l'expression qui n'a pas pu être prise en
	// compte (l'agent doit alors filtrer ou demander une précision).
	Note string
}

var (
	// ErrNoLocation : le fuseau horaire est obligatoire, jamais supposé.
	ErrNoLocation = errors.New("fuseau horaire non défini")
	// ErrUnrecognized : l'expression ne contient aucun repère temporel connu.
	ErrUnrecognized = errors.New("expression temporelle non reconnue")
	// ErrMissingDay : un horaire est donné sans jour ("16h", "le matin").
	ErrMissingDay = errors.New("jour non précisé")
	// ErrPast : l'intervalle est entièrement dans le passé.
	ErrPast = errors.New("période passée")
	// ErrInvalidDate : date inexistante (ex: 31/02).
	ErrInvalidDate = errors.New("date invalide")
)

type dayPart struct {
	key        string
	start, end int // heures
}

// Ordre important : "apres midi" doit être testé avant "midi".
var dayParts = []dayPart{
	{"apres midi", 12, 18},
	{"aprem", 12, 18},
	{"matinee", 8, 12},
	{"matin", 8, 12},
	{"midi", 12, 14},
	{"soiree", 18, 21},
	{"soir", 18, 21},
}

var weekdays = map[string]time.Weekday{
	"lundi": time.Monday, "mardi": time.Tuesday, "mercredi": time.Wednesday,
	"jeudi": time.Thursday, "vendredi": time.Friday, "samedi": time.Saturday,
	"dimanche": time.Sunday,
}

var months = map[string]time.Month{
	"janvier": time.January, "fevrier": time.February, "mars": time.March,
	"avril": time.April, "mai": time.May, "juin": time.June, "juillet": time.July,
	"aout": time.August, "septembre": time.September, "octobre": time.October,
	"novembre": time.November, "decembre": time.December,
}

var (
	reTextDate  = regexp.MustCompile(`\b(\d{1,2})(?:er)?\s+(janvier|fevrier|mars|avril|mai|juin|juillet|aout|septembre|octobre|novembre|decembre)(?:\s+(\d{4}))?\b`)
	reSlashDate = regexp.MustCompile(`\b(\d{1,2})/(\d{1,2})(?:/(\d{4}))?\b`)
	reHour      = regexp.MustCompile(`\b(\d{1,2})\s*h\s*(\d{2})?\b`)
	reWeekday   = regexp.MustCompile(`\b(lundi|mardi|mercredi|jeudi|vendredi|samedi|dimanche)\b`)
	reIn        = regexp.MustCompile(`\bdans (\d{1,3}|une?|deux|trois|quatre|cinq|six|sept|huit|neuf|dix|quinze|vingt|trente) ?(minutes?|min|heures?|h|jours?|semaines?)\b`)
)

var numberWords = map[string]int{
	"un": 1, "une": 1, "deux": 2, "trois": 3, "quatre": 4, "cinq": 5, "six": 6, "sept": 7,
	"huit": 8, "neuf": 9, "dix": 10, "quinze": 15, "vingt": 20, "trente": 30,
}

// Resolve convertit une expression en intervalle, relativement à now, dans loc.
// Une période passée est refusée (ErrPast) et une période en cours commence
// à now : Resolve sert à chercher un moment à venir.
func Resolve(expr string, now time.Time, loc *time.Location) (Period, error) {
	return resolve(expr, now, loc, false)
}

// ResolveIncludingPast est Resolve sans le traitement du passé : la période
// est renvoyée entière, même commencée ou passée (« aujourd'hui » couvre
// toute la journée, « hier » est accepté). Elle sert à consulter ou à
// enregistrer un agenda.
func ResolveIncludingPast(expr string, now time.Time, loc *time.Location) (Period, error) {
	return resolve(expr, now, loc, true)
}

func resolve(expr string, now time.Time, loc *time.Location, keepPast bool) (Period, error) {
	if loc == nil {
		return Period{}, ErrNoLocation
	}
	now = now.In(loc)
	s := " " + normalize(expr) + " "
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)

	var (
		day     *time.Time
		p       Period
		haveDay bool
	)
	setDay := func(d time.Time) { day, haveDay = &d, true }

	switch {
	case strings.Contains(s, " semaine prochaine "):
		// Du lundi suivant au lundi d'après.
		offset := (int(time.Monday) - int(now.Weekday()) + 7) % 7
		if offset == 0 {
			offset = 7
		}
		start := today.AddDate(0, 0, offset)
		p = Period{Start: start, End: start.AddDate(0, 0, 7), Granularity: GranularityWeek}
	case strings.Contains(s, " cette semaine "):
		offset := (int(time.Monday) - int(now.Weekday()) + 7) % 7
		if offset == 0 {
			offset = 7
		}
		p = Period{Start: today, End: today.AddDate(0, 0, offset), Granularity: GranularityWeek}
	case strings.Contains(s, " week end prochain ") || strings.Contains(s, " weekend prochain "):
		// Samedi suivant, ou celui d'après si l'on est déjà le week-end.
		start := today.AddDate(0, 0, (int(time.Saturday)-int(now.Weekday())+7)%7)
		if now.Weekday() == time.Saturday || now.Weekday() == time.Sunday {
			start = today.AddDate(0, 0, 7-(int(now.Weekday())+1)%7)
		}
		p = Period{Start: start, End: start.AddDate(0, 0, 2), Granularity: GranularityWeekend}
	case strings.Contains(s, " week end ") || strings.Contains(s, " weekend "):
		// Ce week-end : samedi et dimanche de la semaine en cours.
		start := today.AddDate(0, 0, (int(time.Saturday)-int(now.Weekday())+7)%7)
		if now.Weekday() == time.Sunday {
			start = today.AddDate(0, 0, -1)
		}
		p = Period{Start: start, End: start.AddDate(0, 0, 2), Granularity: GranularityWeekend}
	case reIn.MatchString(s):
		m := reIn.FindStringSubmatch(s)
		n, ok := numberWords[m[1]]
		if !ok {
			n, _ = strconv.Atoi(m[1])
		}
		switch unit := m[2]; {
		case strings.HasPrefix(unit, "min"):
			start := now.Add(time.Duration(n) * time.Minute).Truncate(time.Minute)
			p = Period{Start: start, End: start.Add(time.Hour), Granularity: GranularityHour}
		case unit == "h" || strings.HasPrefix(unit, "heure"):
			start := now.Add(time.Duration(n) * time.Hour).Truncate(time.Minute)
			p = Period{Start: start, End: start.Add(time.Hour), Granularity: GranularityHour}
		case strings.HasPrefix(unit, "jour"):
			setDay(today.AddDate(0, 0, n))
		default:
			setDay(today.AddDate(0, 0, 7*n))
		}
		// L'heure éventuelle (« dans 3 jours à 14h ») est lue après : le
		// nombre de « dans 2 h » n'est pas une heure.
		s = strings.Replace(s, m[0], " ", 1)
	case strings.Contains(s, " apres demain "):
		setDay(today.AddDate(0, 0, 2))
	case strings.Contains(s, " demain "):
		setDay(today.AddDate(0, 0, 1))
	case strings.Contains(s, " avant hier "):
		setDay(today.AddDate(0, 0, -2))
	case strings.Contains(s, " hier "):
		setDay(today.AddDate(0, 0, -1))
	case strings.Contains(s, " aujourd hui ") || strings.Contains(s, " ce matin ") ||
		strings.Contains(s, " cet apres midi ") || strings.Contains(s, " ce soir ") ||
		strings.Contains(s, " ce midi "):
		setDay(today)
	default:
		if m := reTextDate.FindStringSubmatch(s); m != nil {
			d, err := explicitDate(m[1], months[m[2]], m[3], today)
			if err != nil {
				return Period{}, err
			}
			setDay(d)
		} else if m := reSlashDate.FindStringSubmatch(s); m != nil {
			mo, _ := strconv.Atoi(m[2])
			if mo < 1 || mo > 12 {
				return Period{}, ErrInvalidDate
			}
			d, err := explicitDate(m[1], time.Month(mo), m[3], today)
			if err != nil {
				return Period{}, err
			}
			setDay(d)
		} else if m := reWeekday.FindStringSubmatch(s); m != nil {
			// Prochaine occurrence strictement future : "jeudi" dit un jeudi
			// désigne le jeudi suivant.
			offset := (int(weekdays[m[1]]) - int(now.Weekday()) + 7) % 7
			if offset == 0 {
				offset = 7
			}
			setDay(today.AddDate(0, 0, offset))
		}
	}

	hour := reHour.FindStringSubmatch(s)
	part, hasPart := findDayPart(s)

	switch {
	case p.Granularity == GranularityWeek:
		if hour != nil || hasPart {
			p.Note = "Le moment de la journée n'est pas appliqué à une période d'une semaine : filtre les créneaux retournés."
		}
	case p.Granularity == GranularityWeekend:
		if hour != nil || hasPart {
			p.Note = "Le moment de la journée n'est pas appliqué au week-end entier : filtre les résultats."
		}
	case p.Granularity == GranularityHour:
		// Instant relatif (« dans deux heures ») : déjà résolu.
	case haveDay && hour != nil:
		h, _ := strconv.Atoi(hour[1])
		mi := 0
		if hour[2] != "" {
			mi, _ = strconv.Atoi(hour[2])
		}
		if h > 23 || mi > 59 {
			return Period{}, ErrInvalidDate
		}
		start := time.Date(day.Year(), day.Month(), day.Day(), h, mi, 0, 0, loc)
		p = Period{Start: start, End: start.Add(time.Hour), Granularity: GranularityHour}
	case haveDay && hasPart:
		p = Period{
			Start:       time.Date(day.Year(), day.Month(), day.Day(), part.start, 0, 0, 0, loc),
			End:         time.Date(day.Year(), day.Month(), day.Day(), part.end, 0, 0, 0, loc),
			Granularity: GranularityDayPart,
		}
	case haveDay:
		p = Period{Start: *day, End: day.AddDate(0, 0, 1), Granularity: GranularityDay}
	case hour != nil || hasPart:
		return Period{}, ErrMissingDay
	default:
		return Period{}, ErrUnrecognized
	}

	if keepPast {
		return p, nil
	}
	if !p.End.After(now) {
		return Period{}, ErrPast
	}
	if p.Start.Before(now) {
		p.Start = now.Truncate(time.Minute)
	}
	return p, nil
}

func explicitDate(dayStr string, month time.Month, yearStr string, today time.Time) (time.Time, error) {
	d, _ := strconv.Atoi(dayStr)
	year := today.Year()
	explicitYear := yearStr != ""
	if explicitYear {
		year, _ = strconv.Atoi(yearStr)
	}
	t := time.Date(year, month, d, 0, 0, 0, 0, today.Location())
	if t.Day() != d || t.Month() != month {
		return time.Time{}, ErrInvalidDate
	}
	// Sans année explicite, une date déjà passée désigne l'année suivante.
	if !explicitYear && t.Before(today) {
		t = t.AddDate(1, 0, 0)
	}
	return t, nil
}

func findDayPart(s string) (dayPart, bool) {
	for _, dp := range dayParts {
		if strings.Contains(s, " "+dp.key+" ") {
			return dp, true
		}
	}
	return dayPart{}, false
}

var accentReplacer = strings.NewReplacer(
	"é", "e", "è", "e", "ê", "e", "ë", "e", "à", "a", "â", "a", "ù", "u", "û", "u",
	"ô", "o", "î", "i", "ï", "i", "ç", "c",
	"'", " ", "’", " ", "-", " ", ",", " ", ".", " ", "?", " ", "!", " ",
)

func normalize(s string) string {
	s = accentReplacer.Replace(strings.ToLower(s))
	return strings.Join(strings.Fields(s), " ")
}

// Describe renvoie une description lisible d'une erreur de résolution,
// destinée à l'agent.
func Describe(err error) string {
	switch {
	case errors.Is(err, ErrMissingDay):
		return "Le jour n'est pas précisé : demande-le à l'utilisateur ou déduis-le du contexte de la conversation."
	case errors.Is(err, ErrPast):
		return "Cette période est déjà passée : demande une autre date à l'utilisateur."
	case errors.Is(err, ErrInvalidDate):
		return "Cette date n'existe pas : demande une précision à l'utilisateur."
	case errors.Is(err, ErrNoLocation):
		return "Fuseau horaire inconnu : impossible de calculer la date."
	default:
		return fmt.Sprintf("Expression non reconnue (%v) : reformule avec un jour précis ou demande une précision.", err)
	}
}
