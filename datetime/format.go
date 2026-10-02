package datetime

import (
	"fmt"
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
	day := fmt.Sprint(t.Day())
	if t.Day() == 1 {
		day = "1er"
	}
	hour := fmt.Sprintf("%dh", t.Hour())
	if t.Minute() != 0 {
		hour = fmt.Sprintf("%dh%02d", t.Hour(), t.Minute())
	}
	return fmt.Sprintf("%s %s %s à %s", weekdayNames[t.Weekday()], day, monthNames[t.Month()], hour)
}
