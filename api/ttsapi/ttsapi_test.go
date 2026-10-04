package ttsapi_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"skills/agent"
	"skills/api"
	"skills/api/ttsapi"
	"skills/model/scripted"
	"skills/services/tts"
	"skills/services/tts/ttstest"
	synthesevocale "skills/skills/synthese-vocale"
	ttstools "skills/tools/tts"
)

// Parcours complet : message → effet lire_a_voix_haute → lecture de l'audio.
func TestAudioRoute(t *testing.T) {
	cases := []struct {
		name       string
		engine     *ttstest.Engine
		wantStatus int
		wantBody   string
	}{
		{"audio", &ttstest.Engine{}, 200, "AUDIO:Bonjour"},
		{"moteur indisponible", &ttstest.Engine{Err: tts.ErrEngineUnavailable}, 503, ""},
		{"erreur du moteur", &ttstest.Engine{Err: tts.ErrEngineError}, 502, ""},
		{"audio vide", &ttstest.Engine{Audio: new(string)}, 502, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv, sessionID, audioID := setup(t, c.engine)
			res, err := http.Get(srv.URL + "/api/sessions/" + sessionID + "/audio/" + audioID)
			if err != nil {
				t.Fatal(err)
			}
			defer res.Body.Close()
			body, _ := io.ReadAll(res.Body)
			if res.StatusCode != c.wantStatus {
				t.Fatalf("statut = %d (%s)", res.StatusCode, body)
			}
			if c.wantStatus == 200 && (string(body) != c.wantBody || res.Header.Get("Content-Type") != "audio/mpeg") {
				t.Fatalf("audio = %q (%s)", body, res.Header.Get("Content-Type"))
			}
			if c.wantStatus != 200 && bytes.Contains(body, []byte("tts:")) {
				t.Fatalf("détail interne exposé : %s", body)
			}
		})
	}

	srv, sessionID, _ := setup(t, &ttstest.Engine{})
	res, _ := http.Get(srv.URL + "/api/sessions/" + sessionID + "/audio/inconnu")
	res.Body.Close()
	if res.StatusCode != 404 {
		t.Fatalf("audio inconnu : statut %d", res.StatusCode)
	}
}

func setup(t *testing.T, e *ttstest.Engine) (*httptest.Server, string, string) {
	t.Helper()
	svc, err := tts.NewService(tts.Config{Engine: e, Voices: map[string][]string{"fr-FR": {"ff_siwis"}}, DefaultLanguage: "fr-FR"})
	if err != nil {
		t.Fatal(err)
	}
	skill, _ := synthesevocale.New(synthesevocale.Config{Service: svc})
	m := scripted.New(
		scripted.Call(ttstools.ToolLireAVoixHaute, map[string]any{"texte": "Bonjour"}),
		scripted.Say("Voici la lecture."),
	)
	rt, _ := agent.New(m)
	if err := rt.RegisterSkill(skill); err != nil {
		t.Fatal(err)
	}
	opts := append(ttsapi.Options(svc), api.WithActivatedSkills(synthesevocale.Name))
	srv := httptest.NewServer(api.New(rt, time.Now, opts...).Handler())
	t.Cleanup(srv.Close)

	var sess struct {
		SessionID string `json:"session_id"`
	}
	post(t, srv.URL+"/api/sessions", map[string]string{"timezone": "Europe/Paris"}, &sess)
	var reply struct {
		Effects []struct {
			Tool   string          `json:"tool"`
			Result ttstools.Output `json:"result"`
		} `json:"effects"`
	}
	post(t, srv.URL+"/api/sessions/"+sess.SessionID+"/messages", map[string]string{"message": "Lis « Bonjour »."}, &reply)
	if len(reply.Effects) != 1 || reply.Effects[0].Result.AudioID == "" {
		t.Fatalf("effets = %+v", reply.Effects)
	}
	return srv, sess.SessionID, reply.Effects[0].Result.AudioID
}

func post(t *testing.T, url string, body, out any) {
	t.Helper()
	b, _ := json.Marshal(body)
	res, err := http.Post(url, "application/json", bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode >= 300 {
		t.Fatalf("POST %s : statut %d", url, res.StatusCode)
	}
	if err := json.NewDecoder(res.Body).Decode(out); err != nil {
		t.Fatal(err)
	}
}
