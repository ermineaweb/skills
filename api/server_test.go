package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"skills/agent"
	"skills/api"
	"skills/api/calendarapi"
	"skills/api/prospectsapi"
	"skills/model"
	"skills/model/scripted"
	"skills/services/calendar"
	priserdv "skills/skills/prise-de-rendez-vous"
)

func setup(t *testing.T) (*httptest.Server, *scripted.Model, *calendar.MockProvider) {
	t.Helper()
	paris, _ := time.LoadLocation("Europe/Paris")
	now := func() time.Time { return time.Date(2026, 9, 30, 10, 0, 0, 0, paris) }
	cal, _ := calendar.NewDemoProvider(paris, now)
	skill, err := priserdv.New(priserdv.Config{Provider: cal})
	if err != nil {
		t.Fatal(err)
	}
	m := scripted.New()
	rt, _ := agent.New(m, agent.WithClock(now))
	if err := rt.RegisterSkill(skill); err != nil {
		t.Fatal(err)
	}
	opts := append(calendarapi.Options(cal, now), api.WithActivatedSkills(priserdv.Name))
	srv := httptest.NewServer(api.New(rt, now, opts...).Handler())
	t.Cleanup(srv.Close)
	return srv, m, cal
}

func call(t *testing.T, method, url string, body any) (int, map[string]any) {
	t.Helper()
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, url, r)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func TestSessionRequiresTimezone(t *testing.T) {
	srv, _, _ := setup(t)
	if code, _ := call(t, "POST", srv.URL+"/api/sessions", map[string]any{"nom": "A"}); code != http.StatusBadRequest {
		t.Fatalf("code = %d", code)
	}
	if code, _ := call(t, "POST", srv.URL+"/api/sessions/inconnue/messages", map[string]any{"message": "x"}); code != http.StatusNotFound {
		t.Fatalf("code = %d", code)
	}
}

func TestBookingThroughHTTP(t *testing.T) {
	srv, m, cal := setup(t)
	code, created := call(t, "POST", srv.URL+"/api/sessions", map[string]any{"timezone": "Europe/Paris", "nom": "Camille"})
	if code != http.StatusCreated {
		t.Fatalf("création : %d %v", code, created)
	}
	base := srv.URL + "/api/sessions/" + created["session_id"].(string)

	// Skill déjà actif (WithActivatedSkills) : pas d'appel à activer_skill.
	m.Push(
		scripted.Call("interpreter_date", map[string]any{"expression": "jeudi après-midi"}),
		scripted.CallFn("rechercher_disponibilites", func(req model.Request) (any, error) {
			p, _ := scripted.LastToolResult(req, "interpreter_date")
			return map[string]any{"date_debut": p["date_debut"], "date_fin": p["date_fin"], "professionnel_id": "paul"}, nil
		}),
		scripted.CallFn("reserver_creneau", func(req model.Request) (any, error) {
			res, _ := scripted.LastToolResult(req, "rechercher_disponibilites")
			slot := res["slots"].([]any)[0].(map[string]any)
			return map[string]any{"slot_id": slot["id"], "type_rendez_vous": "consultation"}, nil
		}),
		scripted.Say("Votre rendez-vous est confirmé."),
	)
	code, reply := call(t, "POST", base+"/messages", map[string]any{"message": "Jeudi après-midi avec Paul, le premier créneau."})
	if code != http.StatusOK || reply["text"] != "Votre rendez-vous est confirmé." {
		t.Fatalf("réponse : %d %v", code, reply)
	}
	if n := len(reply["effects"].([]any)); n != 1 {
		t.Fatalf("effets = %d", n)
	}

	code, agenda := call(t, "GET", base+"/rendez-vous", nil)
	list := agenda["rendez_vous"].([]any)
	if code != http.StatusOK || len(list) != 1 {
		t.Fatalf("agenda : %d %v", code, agenda)
	}
	if lib := list[0].(map[string]any)["libelle"].(string); !strings.Contains(lib, "jeudi 1er octobre à 14h") {
		t.Fatalf("libellé = %q", lib)
	}

	// Rendez-vous d'un autre client, pris directement dans le calendrier.
	paris, _ := time.LoadLocation("Europe/Paris")
	res, _ := cal.SearchAvailability(context.Background(), calendar.AvailabilityRequest{
		ProfessionnelID: "marie", TypeRendezVous: "consultation",
		Start: time.Date(2026, 10, 2, 0, 0, 0, 0, paris), End: time.Date(2026, 10, 3, 0, 0, 0, 0, paris),
	})
	if _, err := cal.BookAppointment(context.Background(), calendar.BookingRequest{
		SlotID: res.Slots[0].ID, TypeRendezVous: "consultation", ClientID: "autre", ClientName: "Dominique",
	}); err != nil {
		t.Fatal(err)
	}

	// Vue semaine (démo) : semaine du lundi 28 septembre, sans le week-end.
	code, week := call(t, "GET", base+"/calendrier?date=2026-10-01", nil)
	if code != http.StatusOK || week["debut"] != "2026-09-28" || len(week["jours"].([]any)) != 5 {
		t.Fatalf("semaine : %d %v", code, week)
	}
	if week["heure_min"] != 9.0 || week["heure_max"] != 18.0 {
		t.Fatalf("plage horaire : %v-%v", week["heure_min"], week["heure_max"])
	}
	rdv := week["rendez_vous"].([]any)
	if len(rdv) != 2 {
		t.Fatalf("rendez-vous : %v", rdv)
	}
	mine, other := rdv[0].(map[string]any), rdv[1].(map[string]any)
	if mine["jour"] != "2026-10-01" || mine["debut_min"] != 840.0 || mine["moi"] != true || mine["client"] != "Camille" {
		t.Fatalf("mon rendez-vous : %v", mine)
	}
	if other["moi"] != false || other["client"] != nil {
		t.Fatalf("le rendez-vous d'un autre client doit être anonyme : %v", other)
	}
	if code, _ := call(t, "GET", base+"/calendrier?date=demain", nil); code != http.StatusBadRequest {
		t.Fatalf("date invalide : %d", code)
	}
}

func TestModelErrorReturnsBadGateway(t *testing.T) {
	srv, _, _ := setup(t) // script vide : le modèle échoue
	_, created := call(t, "POST", srv.URL+"/api/sessions", map[string]any{"timezone": "Europe/Paris"})
	code, reply := call(t, "POST", srv.URL+"/api/sessions/"+created["session_id"].(string)+"/messages", map[string]any{"message": "Bonjour"})
	if code != http.StatusBadGateway || strings.Contains(reply["error"].(string), "scripted") {
		t.Fatalf("réponse : %d %v", code, reply)
	}
}

// Sans routes de domaine, l'API ne sert que les sessions et les messages.
func TestServerWithoutDomainRoutes(t *testing.T) {
	rt, _ := agent.New(scripted.New())
	srv := httptest.NewServer(api.New(rt, nil).Handler())
	t.Cleanup(srv.Close)
	code, created := call(t, "POST", srv.URL+"/api/sessions", map[string]any{"timezone": "Europe/Paris"})
	if code != http.StatusCreated {
		t.Fatalf("création : %d %v", code, created)
	}
	if code, _ := call(t, "GET", srv.URL+"/api/sessions/"+created["session_id"].(string)+"/rendez-vous", nil); code != http.StatusNotFound {
		t.Fatalf("route d'agenda sans calendrier : %d", code)
	}
}

func TestSkillsAndProspectsRoutes(t *testing.T) {
	srv, _, _ := setup(t)
	code, out := call(t, "GET", srv.URL+"/api/skills", nil)
	if code != http.StatusOK || len(out["skills"].([]any)) != 1 || out["skills"].([]any)[0] != priserdv.Name {
		t.Fatalf("skills : %d %v", code, out)
	}

	rt, _ := agent.New(scripted.New())
	srv2 := httptest.NewServer(api.New(rt, nil, prospectsapi.Options()...).Handler())
	t.Cleanup(srv2.Close)
	_, created := call(t, "POST", srv2.URL+"/api/sessions", map[string]any{"timezone": "Europe/Paris"})
	code, out = call(t, "GET", srv2.URL+"/api/sessions/"+created["session_id"].(string)+"/prospects", nil)
	if v, ok := out["resultat"]; code != http.StatusOK || !ok || v != nil {
		t.Fatalf("prospects sans résultat : %d %v", code, out)
	}
}
