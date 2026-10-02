package calendartools

import (
	"context"
	"encoding/json"
	"time"

	"skills/services/calendar"
	"skills/types"
)

type rechercherArgs struct {
	ProfessionnelID *string `json:"professionnel_id"`
	TypeRendezVous  *string `json:"type_rendez_vous"`
	DateDebut       string  `json:"date_debut"`
	DateFin         string  `json:"date_fin"`
	DureeMinutes    *int    `json:"duree_minutes"`
}

// RechercherOutput est le résultat renvoyé au modèle.
type RechercherOutput struct {
	Slots   []SlotView `json:"slots"`
	Total   int        `json:"total"`
	Tronque bool       `json:"tronque,omitempty"`
}

// NewRechercherDisponibilites renvoie le handler de rechercher_disponibilites.
// Les créneaux renvoyés proviennent exclusivement du Provider.
func NewRechercherDisponibilites(p calendar.Provider) types.ToolHandler {
	return types.ToolHandlerFunc(func(ctx context.Context, tc types.ToolContext, raw json.RawMessage) (any, *types.Error) {
		if err := checkContext(tc); err != nil {
			return nil, err
		}
		var a rechercherArgs
		if err := decode(raw, &a); err != nil {
			return nil, err
		}
		start, err1 := time.Parse(time.RFC3339, a.DateDebut)
		end, err2 := time.Parse(time.RFC3339, a.DateFin)
		if err1 != nil || err2 != nil {
			return nil, types.Errorf(types.ErrInvalidRequest,
				"date_debut et date_fin doivent être des dates ISO 8601 avec fuseau. Utilise interpreter_date pour convertir une expression comme « jeudi après-midi ».")
		}
		if !end.After(tc.Now) {
			return nil, types.Errorf(types.ErrInvalidRequest, "La période demandée est passée.")
		}
		if start.Before(tc.Now) {
			start = tc.Now
		}
		req := calendar.AvailabilityRequest{
			ProfessionnelID: deref(a.ProfessionnelID),
			TypeRendezVous:  deref(a.TypeRendezVous),
			Start:           start,
			End:             end,
		}
		if a.DureeMinutes != nil {
			req.DurationMinutes = *a.DureeMinutes
		}

		res, err := p.SearchAvailability(ctx, req)
		if err != nil {
			return nil, providerError(ctx, "search", err)
		}
		if len(res.Slots) == 0 {
			return nil, types.NewError(types.ErrNoAvailability)
		}

		l := getLedger(tc.State)
		out := RechercherOutput{Total: len(res.Slots)}
		for i, s := range res.Slots {
			if i == MaxSlotsReturned {
				out.Tronque = true
				break
			}
			l.slots[s.ID] = true
			out.Slots = append(out.Slots, slotView(s, tc.Location))
		}
		return out, nil
	})
}
