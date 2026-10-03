package calendarapi

import (
	"net/http"
	"time"

	"skills/agent"
	"skills/api"
)

// Vue semaine de l'agenda pour l'interface de démonstration. Elle lit le
// calendrier directement (calendar.Viewer), sans passer par le runtime ni
// par les skills :
//
//	GET /api/sessions/{id}/calendrier?date=2026-10-05
//
// date (facultative, défaut : aujourd'hui) désigne un jour de la semaine
// affichée, dans le fuseau de la session. Les horaires sont renvoyés en
// minutes depuis minuit dans ce fuseau, prêts à placer dans une grille.

type calendarDay struct {
	Date  string `json:"date"`
	Today bool   `json:"aujourdhui"`
}

type calendarBlock struct {
	Jour            string `json:"jour"`
	DebutMin        int    `json:"debut_min"`
	FinMin          int    `json:"fin_min"`
	ProfessionnelID string `json:"professionnel_id"`
}

type calendarAppointment struct {
	calendarBlock
	ID             string `json:"id"`
	TypeRendezVous string `json:"type_rendez_vous"`
	// Mine : rendez-vous du client de la session. Ceux des autres clients
	// sont anonymes (Client vide).
	Mine   bool   `json:"moi"`
	Client string `json:"client,omitempty"`
}

type calendarPro struct {
	ID  string `json:"id"`
	Nom string `json:"nom"`
}

type calendarWeek struct {
	Debut          string                `json:"debut"`
	Jours          []calendarDay         `json:"jours"`
	HeureMin       int                   `json:"heure_min"`
	HeureMax       int                   `json:"heure_max"`
	Maintenant     calendarBlock         `json:"maintenant"`
	Professionnels []calendarPro         `json:"professionnels"`
	Ouvertures     []calendarBlock       `json:"ouvertures"`
	RendezVous     []calendarAppointment `json:"rendez_vous"`
}

func (rt *routes) calendarWeek(w http.ResponseWriter, r *http.Request, sess *agent.Session) {
	loc := sess.Location
	now := rt.now().In(loc)
	day := now
	if raw := r.URL.Query().Get("date"); raw != "" {
		d, err := time.ParseInLocation(time.DateOnly, raw, loc)
		if err != nil {
			api.WriteError(w, http.StatusBadRequest, "date invalide (format AAAA-MM-JJ)")
			return
		}
		day = d
	}
	offset := (int(day.Weekday()) + 6) % 7 // lundi = 0
	monday := time.Date(day.Year(), day.Month(), day.Day()-offset, 0, 0, 0, 0, loc)
	end := monday.AddDate(0, 0, 7)

	ov, err := rt.view.Overview(r.Context(), monday, end)
	if err != nil {
		api.WriteError(w, http.StatusBadGateway, "agenda indisponible")
		return
	}

	out := calendarWeek{
		Debut:          monday.Format(time.DateOnly),
		Maintenant:     block(now, now, loc),
		Professionnels: []calendarPro{},
		Ouvertures:     []calendarBlock{},
		RendezVous:     []calendarAppointment{},
	}
	for _, p := range ov.Professionals {
		out.Professionnels = append(out.Professionnels, calendarPro{ID: p.ID, Nom: p.Nom})
	}
	minMin, maxMin := 24*60, 0
	used := map[string]bool{}
	track := func(b calendarBlock) {
		used[b.Jour] = true
		minMin, maxMin = min(minMin, b.DebutMin), max(maxMin, b.FinMin)
	}
	for _, o := range ov.Openings {
		b := block(o.Start, o.End, loc)
		b.ProfessionnelID = o.ProfessionnelID
		track(b)
		out.Ouvertures = append(out.Ouvertures, b)
	}
	for _, a := range ov.Appointments {
		b := block(a.Start, a.End, loc)
		b.ProfessionnelID = a.ProfessionnelID
		track(b)
		v := calendarAppointment{calendarBlock: b, ID: a.ID, TypeRendezVous: a.TypeRendezVous}
		if a.ClientID != "" && a.ClientID == sess.User.ClientID {
			v.Mine, v.Client = true, a.ClientName
		}
		out.RendezVous = append(out.RendezVous, v)
	}

	// Jours ouvrés, plus le week-end s'il contient quelque chose.
	for d := monday; d.Before(end); d = d.AddDate(0, 0, 1) {
		date := d.Format(time.DateOnly)
		if wd := d.Weekday(); (wd == time.Saturday || wd == time.Sunday) && !used[date] {
			continue
		}
		out.Jours = append(out.Jours, calendarDay{Date: date, Today: date == now.Format(time.DateOnly)})
	}

	// Plage horaire affichée : heures pleines englobant l'agenda.
	out.HeureMin, out.HeureMax = 9, 18
	if minMin < maxMin {
		out.HeureMin, out.HeureMax = minMin/60, (maxMin+59)/60
	}
	api.WriteJSON(w, http.StatusOK, out)
}

// block place [start, end] dans le jour de start, en minutes depuis minuit
// (fuseau loc). Un bloc qui passe minuit est coupé à la fin du jour.
func block(start, end time.Time, loc *time.Location) calendarBlock {
	start, end = start.In(loc), end.In(loc)
	b := calendarBlock{
		Jour:     start.Format(time.DateOnly),
		DebutMin: start.Hour()*60 + start.Minute(),
		FinMin:   end.Hour()*60 + end.Minute(),
	}
	if end.Format(time.DateOnly) != b.Jour {
		b.FinMin = 24 * 60
	}
	return b
}
