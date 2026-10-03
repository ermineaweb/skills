// Package calendarapi ajoute à l'API les routes de lecture de l'agenda,
// utilisées par l'interface quand le skill prise-de-rendez-vous est activé :
//
//	GET /api/sessions/{id}/rendez-vous   → rendez-vous à venir du client de la session
//	GET /api/sessions/{id}/calendrier    → vue semaine de l'agenda (démo, si le calendrier est un calendar.Viewer)
//
// Ces routes lisent le calendrier directement, sans passer par le runtime ni
// par le modèle : l'interface affiche ainsi l'état réel de l'agenda,
// indépendamment du texte généré.
package calendarapi

import (
	"net/http"
	"time"

	"skills/agent"
	"skills/api"
	"skills/datetime"
	"skills/services/calendar"
)

type routes struct {
	cal  calendar.Provider
	view calendar.Viewer
	now  func() time.Time
}

// Options renvoie les routes à passer à api.New. cal est le même Provider
// que celui du skill. La vue semaine n'est ajoutée que si cal implémente
// calendar.Viewer (calendrier de démonstration).
func Options(cal calendar.Provider, now func() time.Time) []api.Option {
	if now == nil {
		now = time.Now
	}
	rt := &routes{cal: cal, now: now}
	opts := []api.Option{api.WithSessionRoute("GET", "rendez-vous", rt.listAppointments)}
	if v, ok := cal.(calendar.Viewer); ok {
		rt.view = v
		opts = append(opts, api.WithSessionRoute("GET", "calendrier", rt.calendarWeek))
	}
	return opts
}

type appointmentView struct {
	ID               string `json:"id"`
	Start            string `json:"start"`
	Libelle          string `json:"libelle"`
	ProfessionnelNom string `json:"professionnel_nom"`
	TypeRendezVous   string `json:"type_rendez_vous"`
}

func (rt *routes) listAppointments(w http.ResponseWriter, r *http.Request, sess *agent.Session) {
	res, err := rt.cal.ListAppointments(r.Context(), calendar.ListAppointmentsRequest{
		ClientID: sess.User.ClientID, From: rt.now(),
	})
	if err != nil {
		api.WriteError(w, http.StatusBadGateway, "agenda indisponible")
		return
	}
	out := make([]appointmentView, 0, len(res.Appointments))
	for _, a := range res.Appointments {
		out = append(out, appointmentView{
			ID:               a.ID,
			Start:            a.Start.In(sess.Location).Format(time.RFC3339),
			Libelle:          datetime.FormatFR(a.Start, sess.Location),
			ProfessionnelNom: a.ProfessionnelNom,
			TypeRendezVous:   a.TypeRendezVous,
		})
	}
	api.WriteJSON(w, http.StatusOK, map[string]any{"rendez_vous": out})
}
