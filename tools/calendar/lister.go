package calendartools

import (
	"context"
	"encoding/json"

	"skills/services/calendar"
	"skills/types"
)

// ListerRendezVousOutput liste les rendez-vous actifs du client.
type ListerRendezVousOutput struct {
	RendezVous []AppointmentView `json:"rendez_vous"`
}

// NewListerRendezVous renvoie le handler de lister_rendez_vous. Le client est
// toujours celui de la session : le modèle ne peut pas consulter les
// rendez-vous d'un autre client.
func NewListerRendezVous(p calendar.Provider) types.ToolHandler {
	return types.ToolHandlerFunc(func(ctx context.Context, tc types.ToolContext, _ json.RawMessage) (any, *types.Error) {
		if err := checkContext(tc); err != nil {
			return nil, err
		}
		if tc.User.ClientID == "" {
			return nil, types.Errorf(types.ErrMissingInformation,
				"Client non identifié : seuls les rendez-vous pris dans cette conversation sont accessibles. Pour les autres, l'utilisateur doit se connecter.")
		}
		res, err := p.ListAppointments(ctx, calendar.ListAppointmentsRequest{ClientID: tc.User.ClientID, From: tc.Now})
		if err != nil {
			return nil, providerError(ctx, "list", err)
		}
		l := getLedger(tc.State)
		out := ListerRendezVousOutput{RendezVous: []AppointmentView{}}
		for _, a := range res.Appointments {
			l.appointments[a.ID] = true
			out.RendezVous = append(out.RendezVous, appointmentView(a, tc.Location))
		}
		return out, nil
	})
}

// ListerProfessionnelsOutput liste les professionnels et leurs types de
// rendez-vous.
type ListerProfessionnelsOutput struct {
	Professionnels []types.Professional `json:"professionnels"`
}

// NewListerProfessionnels renvoie le handler de lister_professionnels.
func NewListerProfessionnels(p calendar.Provider) types.ToolHandler {
	return types.ToolHandlerFunc(func(ctx context.Context, tc types.ToolContext, _ json.RawMessage) (any, *types.Error) {
		res, err := p.ListProfessionals(ctx)
		if err != nil {
			return nil, providerError(ctx, "list_professionals", err)
		}
		return ListerProfessionnelsOutput{Professionnels: res.Professionals}, nil
	})
}
