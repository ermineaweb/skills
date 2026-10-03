package app

import (
	"fmt"
	"strconv"
	"time"

	"skills/api/calendarapi"
	"skills/services/calendar"
	priserdv "skills/skills/prise-de-rendez-vous"
)

const rendezVousName = priserdv.Name

// rendezVous construit le skill prise-de-rendez-vous sur le calendrier de
// démonstration, avec les routes d'agenda de l'interface.
//
// Configuration :
//
//	CALENDAR_TZ   fuseau des horaires d'ouverture du calendrier (obligatoire), ex: Europe/Paris
//	AUTO_BOOKING  politique produit : réservation directe d'un créneau unique (true/false, défaut false)
func rendezVous(env Env) (module, error) {
	tz := env.Getenv("CALENDAR_TZ")
	if tz == "" {
		return module{}, fmt.Errorf("CALENDAR_TZ : %w (fuseau des horaires d'ouverture, ex: Europe/Paris)", errMissing)
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return module{}, fmt.Errorf("CALENDAR_TZ : %w", err)
	}
	autoBooking := false
	if raw := env.Getenv("AUTO_BOOKING"); raw != "" {
		if autoBooking, err = strconv.ParseBool(raw); err != nil {
			return module{}, fmt.Errorf("AUTO_BOOKING : valeur %q invalide (true ou false)", raw)
		}
	}
	cal, err := calendar.NewDemoProvider(loc, env.Now)
	if err != nil {
		return module{}, err
	}
	skill, err := priserdv.New(priserdv.Config{Provider: cal, AutoBooking: autoBooking})
	if err != nil {
		return module{}, err
	}
	return module{skill: skill, api: calendarapi.Options(cal, env.Now)}, nil
}
