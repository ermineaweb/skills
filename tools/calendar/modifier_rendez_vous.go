package calendartools

import (
	"context"
	"encoding/json"

	"skills/services/calendar"
	"skills/types"
)

type modifierArgs struct {
	AppointmentID string `json:"appointment_id"`
	NewSlotID     string `json:"new_slot_id"`
}

// NewModifierRendezVous renvoie le handler de modifier_rendez_vous.
func NewModifierRendezVous(p calendar.Provider) types.ToolHandler {
	return types.ToolHandlerFunc(func(ctx context.Context, tc types.ToolContext, raw json.RawMessage) (any, *types.Error) {
		if err := checkContext(tc); err != nil {
			return nil, err
		}
		var a modifierArgs
		if err := decode(raw, &a); err != nil {
			return nil, err
		}
		l := getLedger(tc.State)
		if !l.appointments[a.AppointmentID] {
			return nil, unknownID("appointment_id", ToolListerRendezVous+" ou "+ToolReserverCreneau)
		}
		if !l.slots[a.NewSlotID] {
			return nil, unknownID("new_slot_id", ToolRechercherDisponibilites)
		}

		res, err := p.UpdateAppointment(ctx, calendar.UpdateAppointmentRequest{
			AppointmentID: a.AppointmentID,
			NewSlotID:     a.NewSlotID,
			ClientID:      tc.User.ClientID,
		})
		if err != nil {
			return nil, providerError(ctx, "update", err)
		}
		if !res.Updated {
			return nil, types.NewError(types.ErrUpdateFailed)
		}
		l.appointments[res.Appointment.ID] = true
		return AppointmentOutput{Appointment: appointmentView(res.Appointment, tc.Location)}, nil
	})
}
