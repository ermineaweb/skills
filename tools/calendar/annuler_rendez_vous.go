package calendartools

import (
	"context"
	"encoding/json"

	"skills/services/calendar"
	"skills/types"
)

type annulerArgs struct {
	AppointmentID string `json:"appointment_id"`
}

// NewAnnulerRendezVous renvoie le handler de annuler_rendez_vous.
func NewAnnulerRendezVous(p calendar.Provider) types.ToolHandler {
	return types.ToolHandlerFunc(func(ctx context.Context, tc types.ToolContext, raw json.RawMessage) (any, *types.Error) {
		if err := checkContext(tc); err != nil {
			return nil, err
		}
		var a annulerArgs
		if err := decode(raw, &a); err != nil {
			return nil, err
		}
		if !getLedger(tc.State).appointments[a.AppointmentID] {
			return nil, unknownID("appointment_id", ToolListerRendezVous+" ou "+ToolReserverCreneau)
		}

		res, err := p.CancelAppointment(ctx, calendar.CancelAppointmentRequest{
			AppointmentID: a.AppointmentID,
			ClientID:      tc.User.ClientID,
		})
		if err != nil {
			return nil, providerError(ctx, "cancel", err)
		}
		if !res.Cancelled {
			return nil, types.NewError(types.ErrCancelFailed)
		}
		return AppointmentOutput{Appointment: appointmentView(res.Appointment, tc.Location)}, nil
	})
}
