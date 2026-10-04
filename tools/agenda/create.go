package agendatools

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"skills/datetime"
	"skills/services/agenda"
	"skills/types"
)

// RecurrenceArgs est la récurrence telle que le modèle la décrit.
type RecurrenceArgs struct {
	Frequence  string   `json:"frequence"`
	Intervalle int      `json:"intervalle"`
	Jours      []string `json:"jours"`
	Position   int      `json:"position"`
	JusquAu    string   `json:"jusqu_au"`
	Nombre     int      `json:"nombre"`
}

type createArgs struct {
	Titre           string          `json:"titre"`
	Debut           string          `json:"debut"`
	Fin             string          `json:"fin"`
	DureeMinutes    int             `json:"duree_minutes"`
	JourneeEntiere  bool            `json:"journee_entiere"`
	Description     string          `json:"description"`
	Lieu            string          `json:"lieu"`
	Participants    []string        `json:"participants"`
	RappelsMinutes  []int           `json:"rappels_minutes"`
	Recurrence      *RecurrenceArgs `json:"recurrence"`
	IgnorerConflits bool            `json:"ignorer_conflits"`
}

// EventOutput est le résultat d'une création, modification ou suppression.
type EventOutput struct {
	Evenement EventView `json:"evenement"`
	// ProchainesOccurrences : libellés des occurrences suivantes d'une série.
	ProchainesOccurrences []string `json:"prochaines_occurrences,omitempty"`
	Portee                string   `json:"portee,omitempty"`
	Avant                 string   `json:"avant,omitempty"`
}

// NewCreateEvent renvoie le handler de create_event.
func NewCreateEvent(p agenda.Provider) types.ToolHandler {
	return types.ToolHandlerFunc(func(ctx context.Context, tc types.ToolContext, raw json.RawMessage) (any, *types.Error) {
		if err := checkContext(tc); err != nil {
			return nil, err
		}
		var a createArgs
		if err := decode(raw, &a); err != nil {
			return nil, err
		}
		if strings.TrimSpace(a.Titre) == "" {
			return nil, &types.Error{Code: types.ErrMissingInformation, Message: "Titre manquant : demande l'objet de l'événement.", Missing: []string{"titre"}}
		}
		ev := agenda.Event{
			Title: strings.TrimSpace(a.Titre), TimeZone: tc.Location.String(), AllDay: a.JourneeEntiere,
			Description: strings.TrimSpace(a.Description), Location: strings.TrimSpace(a.Lieu),
			Attendees: cleanList(a.Participants), Reminders: a.RappelsMinutes,
		}
		m, err := parseMoment(a.Debut, tc, true)
		if err != nil {
			return nil, err
		}
		if a.JourneeEntiere {
			if ev.Start, ev.End, err = allDayRange(m, a.Fin, tc); err != nil {
				return nil, err
			}
		} else {
			if m.isClock {
				return nil, &types.Error{Code: types.ErrMissingInformation,
					Message: "Le jour n'est pas précisé : demande-le à l'utilisateur.", Missing: []string{"jour"}}
			}
			if ev.Start, err = startOf(m, m.period.Start); err != nil {
				return nil, err
			}
			if ev.End, err = endOf(ev.Start, a.Fin, a.DureeMinutes, DefaultDurationMinutes*time.Minute, tc); err != nil {
				return nil, err
			}
		}
		if ev.End.Before(tc.Now) {
			return nil, types.Errorf(types.ErrInvalidRequest,
				"Ce moment est déjà passé (%s) : vérifie la date avec l'utilisateur.", label(ev.Start, ev.End, ev.AllDay, tc.Location))
		}
		if a.Recurrence != nil {
			r, err := recurrence(*a.Recurrence, tc)
			if err != nil {
				return nil, err
			}
			ev.Recurrence = &r
			if verr := r.Validate(ev.Start); verr != nil {
				return nil, providerError(ctx, "validate", verr)
			}
			// Le premier événement d'une série est sa première occurrence
			// (« tous les lundis » demandé un mercredi : lundi prochain).
			first := agenda.Occurrences(ev, ev.Start, ev.Start.AddDate(2, 0, 0), 1)
			if len(first) == 0 {
				return nil, types.Errorf(types.ErrInvalidRequest, "Cette récurrence ne produit aucune occurrence : vérifie-la avec l'utilisateur.")
			}
			ev.Start, ev.End = first[0].Start, first[0].End
		}
		if !a.IgnorerConflits {
			found, err := conflicts(ctx, p, tc, ev, "")
			if err != nil {
				return nil, err
			}
			if len(found) > 0 {
				return nil, conflictError(tc, found)
			}
		}

		res, perr := p.CreateEvent(ctx, agenda.CreateRequest{OwnerID: tc.User.ClientID, Event: ev})
		if perr != nil {
			return nil, providerError(ctx, "create", perr)
		}
		if !res.Created {
			return nil, types.Errorf(types.ErrEventNotSaved, "L'événement n'a pas été créé : n'annonce aucun ajout et propose de réessayer.")
		}
		return eventOutput(tc, res.Event, ""), nil
	})
}

// eventOutput présente un événement (sa première occurrence et, pour une
// série, les suivantes).
func eventOutput(tc types.ToolContext, e agenda.Event, portee string) EventOutput {
	occ := agenda.Occurrences(e, e.Start, e.Start.AddDate(2, 0, 0), 4)
	if len(occ) == 0 {
		occ = []agenda.Occurrence{{Event: e, Start: e.Start, End: e.End}}
	}
	out := EventOutput{Evenement: remember(tc, occ[:1])[0], Portee: portee}
	for _, o := range occ[1:] {
		out.ProchainesOccurrences = append(out.ProchainesOccurrences, label(o.Start, o.End, e.AllDay, tc.Location))
	}
	return out
}

// allDayRange : journée(s) entière(s), de debut à fin (jour inclus).
func allDayRange(m moment, fin string, tc types.ToolContext) (time.Time, time.Time, *types.Error) {
	if m.isClock {
		return time.Time{}, time.Time{}, &types.Error{Code: types.ErrMissingInformation, Message: "Le jour n'est pas précisé.", Missing: []string{"jour"}}
	}
	day := m.period.Start
	start := at(day, 0, 0)
	last := start
	if fin != "" {
		fm, err := parseMoment(fin, tc, false)
		if err != nil {
			return time.Time{}, time.Time{}, err
		}
		if fm.isClock {
			return time.Time{}, time.Time{}, types.Errorf(types.ErrInvalidRequest, "Pour une journée entière, fin est un jour, pas une heure.")
		}
		last = at(fm.period.Start, 0, 0)
		if last.Before(start) {
			return time.Time{}, time.Time{}, types.Errorf(types.ErrInvalidRequest, "Le dernier jour précède le premier.")
		}
	}
	return start, last.AddDate(0, 0, 1), nil
}

var frequencies = map[string]agenda.Frequency{
	"quotidienne": agenda.Daily, "hebdomadaire": agenda.Weekly, "mensuelle": agenda.Monthly, "annuelle": agenda.Yearly,
}

var weekdays = map[string]time.Weekday{
	"lundi": time.Monday, "mardi": time.Tuesday, "mercredi": time.Wednesday, "jeudi": time.Thursday,
	"vendredi": time.Friday, "samedi": time.Saturday, "dimanche": time.Sunday,
}

// recurrence convertit la récurrence décrite par le modèle.
func recurrence(a RecurrenceArgs, tc types.ToolContext) (agenda.Recurrence, *types.Error) {
	f, ok := frequencies[a.Frequence]
	if !ok {
		return agenda.Recurrence{}, types.Errorf(types.ErrInvalidRequest, "frequence : quotidienne, hebdomadaire, mensuelle ou annuelle.")
	}
	r := agenda.Recurrence{Frequency: f, Interval: a.Intervalle, Position: a.Position, Count: a.Nombre}
	for _, j := range a.Jours {
		wd, ok := weekdays[strings.ToLower(strings.TrimSpace(j))]
		if !ok {
			return agenda.Recurrence{}, types.Errorf(types.ErrInvalidRequest, "Jour de la semaine inconnu : %q.", j)
		}
		r.Weekdays = append(r.Weekdays, wd)
	}
	if a.JusquAu != "" {
		m, err := parseMoment(a.JusquAu, tc, false)
		if err != nil {
			return agenda.Recurrence{}, err
		}
		if m.isClock {
			return agenda.Recurrence{}, types.Errorf(types.ErrInvalidRequest, "jusqu_au est un jour (ex : 2026-12-31), pas une heure.")
		}
		// Fin de période : « jusqu'en décembre » converti en 2026-12-31 par
		// le modèle, ou une expression (« le 31 décembre »).
		last := m.period.End.Add(-time.Nanosecond)
		if m.period.Granularity == datetime.GranularityHour {
			last = m.period.Start
		}
		until := at(last, 0, 0)
		r.Until = &until
	}
	return r, nil
}
