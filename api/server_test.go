package api_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"skills/agent"
	"skills/api"
	"skills/model"
	"skills/model/scripted"
	"skills/services/calendar"
	priserdv "skills/skills/prise-de-rendez-vous"
)

func setup(t *testing.T) (*httptest.Server, *scripted.Model) {
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
	srv := httptest.NewServer(api.New(rt, cal, now, api.WithActivatedSkills(priserdv.Name)).Handler())
	t.Cleanup(srv.Close)
	return srv, m
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
	srv, _ := setup(t)
	if code, _ := call(t, "POST", srv.URL+"/api/sessions", map[string]any{"nom": "A"}); code != http.StatusBadRequest {
		t.Fatalf("code = %d", code)
	}
	if code, _ := call(t, "POST", srv.URL+"/api/sessions/inconnue/messages", map[string]any{"message": "x"}); code != http.StatusNotFound {
		t.Fatalf("code = %d", code)
	}
}

func TestBookingThroughHTTP(t *testing.T) {
	srv, m := setup(t)
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
}

func TestModelErrorReturnsBadGateway(t *testing.T) {
	srv, _ := setup(t) // script vide : le modèle échoue
	_, created := call(t, "POST", srv.URL+"/api/sessions", map[string]any{"timezone": "Europe/Paris"})
	code, reply := call(t, "POST", srv.URL+"/api/sessions/"+created["session_id"].(string)+"/messages", map[string]any{"message": "Bonjour"})
	if code != http.StatusBadGateway || strings.Contains(reply["error"].(string), "scripted") {
		t.Fatalf("réponse : %d %v", code, reply)
	}
}
