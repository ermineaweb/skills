package agendatools

import (
	"context"
	"encoding/json"
	"strings"

	"skills/services/agenda"
	"skills/types"
)

type updateArgs struct {
	Target
	Portee          string    `json:"portee"`
	Titre           *string   `json:"titre"`
	Debut           string    `json:"debut"`
	Fin             string    `json:"fin"`
	DureeMinutes    int       `json:"duree_minutes"`
	Description     *string   `json:"description"`
	Lieu            *string   `json:"lieu"`
	Participants    *[]string `json:"participants"`
	RappelsMinutes  *[]int    `json:"rappels_minutes"`
	IgnorerConflits bool      `json:"ignorer_conflits"`
}

// NewUpdateEvent renvoie le handler de update_event.
func NewUpdateEvent(p agenda.Provider) types.ToolHandler {
	return types.ToolHandlerFunc(func(ctx context.Context, tc types.ToolContext, raw json.RawMessage) (any, *types.Error) {
		if err := checkContext(tc); err != nil {
			return nil, err
		}
		var a updateArgs
		if err := decode(raw, &a); err != nil {
			return nil, err
		}
		occ, err := resolveTarget(ctx, p, tc, a.Target)
		if err != nil {
			return nil, err
		}
		portee, err := scope(occ, a.Portee)
		if err != nil {
			return nil, err
		}
		series := occ.Recurring() && portee == ScopeSeries

		var c agenda.Changes
		if a.Titre != nil {
			t := strings.TrimSpace(*a.Titre)
			if t == "" {
				return nil, types.Errorf(types.ErrInvalidRequest, "Le titre ne peut pas être vide.")
			}
			c.Title = &t
		}
		for _, f := range []struct {
			src *string
			dst **string
		}{{a.Description, &c.Description}, {a.Lieu, &c.Location}} {
			if f.src != nil {
				v := strings.TrimSpace(*f.src) // "" efface le champ
				*f.dst = &v
			}
		}
		if a.Participants != nil {
			v := cleanList(*a.Participants)
			c.Attendees = &v
		}
		if a.RappelsMinutes != nil {
			c.Reminders = a.RappelsMinutes
		}

		// Horaire : relatif à l'occurrence visée, ou à la série entière.
		ref := occ
		if series {
			ref = agenda.Occurrence{Event: occ.Event, Start: occ.Event.Start, End: occ.Event.End}
		}
		timeChanged := a.Debut != "" || a.Fin != "" || a.DureeMinutes > 0
		if timeChanged {
			if occ.Event.AllDay {
				return nil, types.Errorf(types.ErrInvalidRequest, "Événement sur une journée entière : supprime-le et recrée-le avec un horaire.")
			}
			start := ref.Start
			if a.Debut != "" {
				m, err := parseMoment(a.Debut, tc, true)
				if err != nil {
					return nil, err
				}
				switch {
				case m.isClock:
					start = at(ref.Start, m.hour, m.minute)
				case series:
					// Dans une série, seul l'horaire change : le jour reste
					// celui défini par la récurrence.
					if !sameDay(m.period.Start, occ.Start) {
						return nil, types.Errorf(types.ErrInvalidRequest,
							"Pour toute la série, seule l'heure peut changer (ex : debut « 16h »). Pour un autre jour, propose de recréer la série.")
					}
					if start, err = startOf(m, m.period.Start); err != nil {
						return nil, err
					}
					start = at(ref.Start, start.Hour(), start.Minute())
				default:
					if start, err = startOf(m, m.period.Start); err != nil {
						return nil, err
					}
				}
			}
			end, err := endOf(start, a.Fin, a.DureeMinutes, ref.End.Sub(ref.Start), tc)
			if err != nil {
				return nil, err
			}
			if !series && end.Before(tc.Now) {
				return nil, types.Errorf(types.ErrInvalidRequest, "Ce nouveau moment est déjà passé (%s) : vérifie avec l'utilisateur.", label(start, end, false, tc.Location))
			}
			c.Start, c.End = &start, &end
		}
		if c == (agenda.Changes{}) {
			return nil, types.Errorf(types.ErrInvalidRequest, "Aucune modification demandée : précise ce qui change (debut, fin, lieu…).")
		}

		if timeChanged && !a.IgnorerConflits {
			candidate := occ.Event
			if !series {
				candidate.Recurrence, candidate.Exceptions = nil, nil
			}
			candidate.Start, candidate.End = *c.Start, *c.End
			found, err := conflicts(ctx, p, tc, candidate, occ.Event.ID)
			if err != nil {
				return nil, err
			}
			if len(found) > 0 {
				return nil, conflictError(tc, found)
			}
		}

		req := agenda.UpdateRequest{OwnerID: tc.User.ClientID, EventID: occ.Event.ID, Changes: c}
		if occ.Recurring() && !series {
			start := occ.Start
			req.Occurrence = &start
		}
		res, perr := p.UpdateEvent(ctx, req)
		if perr != nil {
			return nil, providerError(ctx, "update", perr)
		}
		if !res.Updated {
			return nil, types.Errorf(types.ErrEventNotSaved, "La modification n'a pas été enregistrée ; l'événement est inchangé.")
		}
		out := eventOutput(tc, res.Event, portee)
		out.Avant = label(occ.Start, occ.End, occ.Event.AllDay, tc.Location)
		if !occ.Recurring() {
			out.Portee = ""
		}
		return out, nil
	})
}
