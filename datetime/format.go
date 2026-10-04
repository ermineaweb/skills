package datetime

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var weekdayNames = [...]string{"dimanche", "lundi", "mardi", "mercredi", "jeudi", "vendredi", "samedi"}

var monthNames = [...]string{"", "janvier", "février", "mars", "avril", "mai", "juin",
	"juillet", "août", "septembre", "octobre", "novembre", "décembre"}

// FormatFR formate un instant en français dans le fuseau loc,
// ex: "jeudi 1er octobre à 14h30". Le calcul du jour de la semaine est fait
// ici, et non par le modèle, pour éviter toute erreur de calendrier.
func FormatFR(t time.Time, loc *time.Location) string {
	if loc != nil {
		t = t.In(loc)
	}
	return FormatDayFR(t, nil) + " à " + FormatClockFR(t, nil)
}

// FormatDayFR formate le jour d'un instant, ex: "jeudi 1er octobre".
func FormatDayFR(t time.Time, loc *time.Location) string {
	if loc != nil {
		t = t.In(loc)
	}
	day := fmt.Sprint(t.Day())
	if t.Day() == 1 {
		day = "1er"
	}
	return fmt.Sprintf("%s %s %s", weekdayNames[t.Weekday()], day, monthNames[t.Month()])
}

// FormatClockFR formate l'heure d'un instant, ex: "14h", "14h30".
func FormatClockFR(t time.Time, loc *time.Location) string {
	if loc != nil {
		t = t.In(loc)
	}
	if t.Minute() != 0 {
		return fmt.Sprintf("%dh%02d", t.Hour(), t.Minute())
	}
	return fmt.Sprintf("%dh", t.Hour())
}

var reClock = regexp.MustCompile(`^(\d{1,2})\s*(?:h|:|heures?)\s*(\d{2})?$`)

// ParseClock lit une heure seule : "14h", "14h30", "14:30", "9 heures",
// "midi", "minuit".
func ParseClock(s string) (hour, minute int, ok bool) {
	s = normalize(s)
	s = strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(s, "a "), "vers "))
	switch s {
	case "midi":
		return 12, 0, true
	case "minuit":
		return 0, 0, true
	}
	m := reClock.FindStringSubmatch(s)
	if m == nil {
		return 0, 0, false
	}
	hour, _ = strconv.Atoi(m[1])
	if m[2] != "" {
		minute, _ = strconv.Atoi(m[2])
	}
	if hour > 23 || minute > 59 {
		return 0, 0, false
	}
	return hour, minute, true
}
