package synthesevocale_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"skills/agent"
	"skills/model/scripted"
	"skills/services/tts"
	"skills/services/tts/ttstest"
	synthesevocale "skills/skills/synthese-vocale"
	ttstools "skills/tools/tts"
	"skills/types"
)

// Agent → skill → tts.Service, sur un moteur factice : le skill ne dépend
// d'aucun moteur concret.

func setup(t *testing.T) (*agent.Runtime, *scripted.Model, *agent.Session, *ttstest.Engine) {
	t.Helper()
	e := &ttstest.Engine{}
	svc, err := tts.NewService(tts.Config{
		Engine:          e,
		Voices:          map[string][]string{"fr-FR": {"ff_siwis"}, "en-US": {"af_heart"}},
		DefaultLanguage: "fr-FR",
		MaxTextLength:   100,
	})
	if err != nil {
		t.Fatal(err)
	}
	skill, err := synthesevocale.New(synthesevocale.Config{Service: svc})
	if err != nil {
		t.Fatal(err)
	}
	m := scripted.New()
	rt, _ := agent.New(m)
	if err := rt.RegisterSkill(skill); err != nil {
		t.Fatal(err)
	}
	s, err := agent.NewSession("test", "Europe/Paris", types.UserContext{})
	if err != nil {
		t.Fatal(err)
	}
	return rt, m, s, e
}

func TestReadAloud(t *testing.T) {
	rt, m, s, e := setup(t)
	m.Push(
		scripted.Call(agent.ActivateSkillTool, map[string]string{"nom": synthesevocale.Name}),
		scripted.Call(ttstools.ToolLireAVoixHaute, map[string]any{"texte": "Bonjour, comment puis-je vous aider ?"}),
		scripted.Say("Voici la lecture."),
	)
	r, err := rt.Handle(context.Background(), s, "Lis-moi cette réponse à voix haute.")
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Effects) != 1 || r.Effects[0].Tool != ttstools.ToolLireAVoixHaute {
		t.Fatalf("effets = %+v", r.Effects)
	}
	var out ttstools.Output
	if err := json.Unmarshal(r.Effects[0].Result, &out); err != nil || out.AudioID == "" {
		t.Fatalf("résultat = %s", r.Effects[0].Result)
	}
	if out.Langue != "fr-FR" || out.Voix != "ff_siwis" || out.Format != tts.FormatMP3 || out.Vitesse != 1 {
		t.Fatalf("valeurs par défaut = %+v", out)
	}
	// Le tool ne génère pas l'audio : le moteur n'est appelé qu'à la lecture.
	if n := len(e.Requests()); n != 0 {
		t.Fatalf("moteur appelé %d fois par le tool", n)
	}
	req, ok := ttstools.Lookup(s.State(), out.AudioID)
	if !ok || req.Text != "Bonjour, comment puis-je vous aider ?" {
		t.Fatalf("demande conservée = %+v, %v", req, ok)
	}
	if _, ok := ttstools.Lookup(s.State(), "inconnu"); ok {
		t.Fatal("identifiant inventé accepté")
	}
}

func TestInvalidVoiceIsExplainedToModel(t *testing.T) {
	rt, m, s, _ := setup(t)
	s.ActivateSkill(synthesevocale.Name)
	m.Push(
		scripted.Call(ttstools.ToolLireAVoixHaute, map[string]any{"texte": "Bonjour", "voix": "ff_inventee"}),
		scripted.Say("Cette voix n'existe pas."),
	)
	r, err := rt.Handle(context.Background(), s, "Lis « Bonjour » avec une autre voix.")
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Effects) != 0 || r.Steps[0].ErrorCode != types.ErrInvalidRequest {
		t.Fatalf("étapes = %+v", r.Steps)
	}
	if !strings.Contains(string(r.Steps[0].Result), "ff_siwis") {
		t.Fatalf("voix acceptées absentes du message : %s", r.Steps[0].Result)
	}
}

func TestInstructionsListConfiguration(t *testing.T) {
	svc, _ := tts.NewService(tts.Config{Engine: &ttstest.Engine{}, Voices: map[string][]string{"fr-FR": {"ff_siwis"}}, DefaultLanguage: "fr-FR"})
	skill, _ := synthesevocale.New(synthesevocale.Config{Service: svc})
	for _, want := range []string{"Voix en fr-FR : ff_siwis", "2000 caractères", "mp3"} {
		if !strings.Contains(skill.Instructions(), want) {
			t.Errorf("instructions sans %q", want)
		}
	}
}
