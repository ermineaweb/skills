package calendartools

import (
	"context"
	"encoding/json"
	"strings"

	"skills/services/calendar"
	"skills/types"
)

type reserverArgs struct {
	SlotID         string  `json:"slot_id"`
	ClientID       *string `json:"client_id"`
	ClientName     *string `json:"client_name"`
	ClientEmail    *string `json:"client_email"`
	TypeRendezVous string  `json:"type_rendez_vous"`
}

// AppointmentOutput est renvoyé par les tools qui créent ou modifient un
// rendez-vous.
type AppointmentOutput struct {
	Appointment AppointmentView `json:"appointment"`
}

// NewReserverCreneau renvoie le handler de reserver_creneau.
//
// L'identité du client provient en priorité de la session (authentification
// de l'application hôte) ; le modèle ne peut pas réserver au nom d'un autre
// client_id.
func NewReserverCreneau(p calendar.Provider) types.ToolHandler {
	return types.ToolHandlerFunc(func(ctx context.Context, tc types.ToolContext, raw json.RawMessage) (any, *types.Error) {
		if err := checkContext(tc); err != nil {
			return nil, err
		}
		var a reserverArgs
		if err := decode(raw, &a); err != nil {
			return nil, err
		}

		l := getLedger(tc.State)
		if !l.slots[a.SlotID] {
			return nil, unknownID("slot_id", ToolRechercherDisponibilites)
		}

		if id := deref(a.ClientID); id != "" && id != tc.User.ClientID {
			return nil, types.Errorf(types.ErrInvalidRequest,
				"client_id ne correspond pas au client de la session : laisse ce champ à null.")
		}
		name := firstNonEmpty(tc.User.Name, deref(a.ClientName))
		email := firstNonEmpty(tc.User.Email, deref(a.ClientEmail))

		var missing []string
		if name == "" {
			missing = append(missing, "client_name")
		}
		if strings.TrimSpace(a.TypeRendezVous) == "" {
			missing = append(missing, "type_rendez_vous")
		}
		if len(missing) > 0 {
			return nil, &types.Error{Code: types.ErrMissingInformation,
				Message: types.DefaultHint(types.ErrMissingInformation), Missing: missing}
		}

		res, err := p.BookAppointment(ctx, calendar.BookingRequest{
			SlotID:         a.SlotID,
			TypeRendezVous: a.TypeRendezVous,
			ClientID:       tc.User.ClientID,
			ClientName:     name,
			ClientEmail:    email,
		})
		if err != nil {
			return nil, providerError(ctx, "book", err)
		}
		// Règle métier : sans confirmation explicite, la réservation n'existe pas.
		if !res.Confirmed || res.Appointment.ID == "" {
			return nil, types.NewError(types.ErrBookingFailed)
		}
		l.appointments[res.Appointment.ID] = true
		return AppointmentOutput{Appointment: appointmentView(res.Appointment, tc.Location)}, nil
	})
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}
