package calendar

import (
	"context"
	"errors"
	"testing"
	"time"

	"skills/types"
)

func demo(t *testing.T) *MockProvider {
	t.Helper()
	paris, _ := time.LoadLocation("Europe/Paris")
	now := time.Date(2026, 9, 30, 10, 0, 0, 0, paris)
	m, err := NewDemoProvider(paris, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func code(err error) types.ErrorCode {
	var te *types.Error
	if errors.As(err, &te) {
		return te.Code
	}
	return ""
}

func TestMockRequiresLocation(t *testing.T) {
	if _, err := NewMockProvider(nil, nil); err == nil {
		t.Fatal("un fuseau doit être exigé")
	}
}

func TestMockRejectsSlotNeverIssued(t *testing.T) {
	m := demo(t)
	_, err := m.BookAppointment(context.Background(), BookingRequest{SlotID: "slot_x", TypeRendezVous: "consultation", ClientName: "A"})
	if code(err) != types.ErrSlotNoLongerAvailable {
		t.Fatalf("err = %v", err)
	}
}

func TestMockOwnership(t *testing.T) {
	m := demo(t)
	ctx := context.Background()
	paris := m.loc
	res, _ := m.SearchAvailability(ctx, AvailabilityRequest{
		ProfessionnelID: "paul",
		Start:           time.Date(2026, 10, 1, 12, 0, 0, 0, paris),
		End:             time.Date(2026, 10, 1, 18, 0, 0, 0, paris),
	})
	b, err := m.BookAppointment(ctx, BookingRequest{SlotID: res.Slots[0].ID, TypeRendezVous: "consultation", ClientID: "c-1", ClientName: "A"})
	if err != nil || !b.Confirmed {
		t.Fatalf("réservation: %v", err)
	}
	// Un autre client ne peut ni annuler ni déplacer, et n'apprend pas que le rendez-vous existe.
	if _, err := m.CancelAppointment(ctx, CancelAppointmentRequest{AppointmentID: b.Appointment.ID, ClientID: "c-2"}); code(err) != types.ErrAppointmentNotFound {
		t.Fatalf("annulation par un tiers: %v", err)
	}
	if _, err := m.UpdateAppointment(ctx, UpdateAppointmentRequest{AppointmentID: b.Appointment.ID, NewSlotID: res.Slots[1].ID, ClientID: "c-2"}); code(err) != types.ErrAppointmentNotFound {
		t.Fatalf("modification par un tiers: %v", err)
	}
	// Déplacement vers un créneau qui chevauche le rendez-vous lui-même : autorisé.
	u, err := m.UpdateAppointment(ctx, UpdateAppointmentRequest{AppointmentID: b.Appointment.ID, NewSlotID: res.Slots[0].ID, ClientID: "c-1"})
	if err != nil || !u.Updated {
		t.Fatalf("modification sur place: %v", err)
	}
	// Double annulation.
	if _, err := m.CancelAppointment(ctx, CancelAppointmentRequest{AppointmentID: b.Appointment.ID, ClientID: "c-1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.CancelAppointment(ctx, CancelAppointmentRequest{AppointmentID: b.Appointment.ID, ClientID: "c-1"}); code(err) != types.ErrCancelFailed {
		t.Fatalf("double annulation: %v", err)
	}
}

func TestMockRejectsTypeDurationMismatch(t *testing.T) {
	m := demo(t)
	ctx := context.Background()
	res, _ := m.SearchAvailability(ctx, AvailabilityRequest{
		ProfessionnelID: "paul",
		Start:           time.Date(2026, 10, 1, 8, 0, 0, 0, m.loc),
		End:             time.Date(2026, 10, 1, 12, 0, 0, 0, m.loc),
	})
	// Créneau de 30 min recherché sans type : impossible d'y placer un bilan (60 min).
	_, err := m.BookAppointment(ctx, BookingRequest{SlotID: res.Slots[0].ID, TypeRendezVous: "bilan", ClientName: "A"})
	if code(err) != types.ErrInvalidRequest {
		t.Fatalf("err = %v", err)
	}
}

func TestMockSearchValidation(t *testing.T) {
	m := demo(t)
	ctx := context.Background()
	start := time.Date(2026, 10, 1, 8, 0, 0, 0, m.loc)
	cases := []AvailabilityRequest{
		{Start: start, End: start},
		{Start: start, End: start.AddDate(0, 2, 0)},
		{Start: start, End: start.Add(time.Hour), TypeRendezVous: "massage"},
	}
	for _, req := range cases {
		if _, err := m.SearchAvailability(ctx, req); code(err) != types.ErrInvalidRequest {
			t.Errorf("%+v : err = %v", req, err)
		}
	}
}
