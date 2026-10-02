package priserdv_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"skills/agent"
	"skills/services/calendar"
)

func jsonUnmarshal(raw []byte, v any) error { return json.Unmarshal(raw, v) }

// appointmentIDOf renvoie l'identifiant du rendez-vous du dernier effet confirmé.
func appointmentIDOf(t *testing.T, r agent.Reply) string {
	t.Helper()
	if len(r.Effects) == 0 {
		t.Fatal("aucun effet confirmé")
	}
	var res struct {
		Appointment struct {
			ID string `json:"id"`
		} `json:"appointment"`
	}
	if err := json.Unmarshal(r.Effects[len(r.Effects)-1].Result, &res); err != nil || res.Appointment.ID == "" {
		t.Fatalf("résultat sans rendez-vous : %s", r.Effects[len(r.Effects)-1].Result)
	}
	return res.Appointment.ID
}

// preBook crée directement dans le calendrier un rendez-vous jeudi à hour:00
// avec Paul (simule une conversation antérieure).
func preBook(t *testing.T, cal *calendar.MockProvider, clientID string, hour int) {
	t.Helper()
	ctx := context.Background()
	start := time.Date(2026, 10, 1, hour, 0, 0, 0, paris)
	res, err := cal.SearchAvailability(ctx, calendar.AvailabilityRequest{
		ProfessionnelID: "paul", TypeRendezVous: "consultation", Start: start, End: start.Add(30 * time.Minute),
	})
	if err != nil || len(res.Slots) != 1 {
		t.Fatalf("pré-réservation impossible : %v %v", err, res.Slots)
	}
	b, err := cal.BookAppointment(ctx, calendar.BookingRequest{
		SlotID: res.Slots[0].ID, TypeRendezVous: "consultation", ClientID: clientID, ClientName: "Camille Durand",
	})
	if err != nil || !b.Confirmed {
		t.Fatalf("pré-réservation : %v", err)
	}
}

// extractSlot renvoie l'identifiant du créneau à hhmm dans un résultat de recherche.
func extractSlot(content, hhmm string) string {
	var res struct {
		Slots []struct {
			ID    string    `json:"id"`
			Start time.Time `json:"start"`
		} `json:"slots"`
	}
	_ = json.Unmarshal([]byte(content), &res)
	for _, s := range res.Slots {
		if s.Start.Format("15:04") == hhmm {
			return s.ID
		}
	}
	return ""
}
