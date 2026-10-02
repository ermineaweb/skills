package agent_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"skills/agent"
	"skills/model"
	"skills/model/scripted"
	"skills/types"
)

// testSkill est un skill minimal, indépendant du calendrier.
type testSkill struct {
	name  string
	tools []types.Tool
	calls *int
}

func (s testSkill) Name() string         { return s.name }
func (s testSkill) Description() string  { return "skill de test " + s.name }
func (s testSkill) Instructions() string { return "INSTRUCTIONS-" + s.name }
func (s testSkill) Tools() []types.Tool  { return s.tools }

func newTestSkill(calls *int) testSkill {
	schema := json.RawMessage(`{"type":"object","additionalProperties":false,"required":["x"],"properties":{"x":{"type":"integer","minimum":1}}}`)
	return testSkill{
		name:  "demo",
		calls: calls,
		tools: []types.Tool{
			{
				Definition: types.ToolDefinition{Name: "ecrire", Description: "écrit", Parameters: schema},
				Mutating:   true,
				Handler: types.ToolHandlerFunc(func(_ context.Context, tc types.ToolContext, args json.RawMessage) (any, *types.Error) {
					*calls++
					if tc.Location == nil || tc.User.ClientID != "c-1" {
						return nil, types.NewError(types.ErrInternal)
					}
					return map[string]any{"ok": true}, nil
				}),
			},
			{
				Definition: types.ToolDefinition{Name: "panique", Description: "panique", Parameters: json.RawMessage(`{"type":"object"}`)},
				Handler: types.ToolHandlerFunc(func(context.Context, types.ToolContext, json.RawMessage) (any, *types.Error) {
					panic("boom")
				}),
			},
		},
	}
}

var fixedNow = time.Date(2026, 9, 30, 10, 0, 0, 0, time.UTC)

func setup(t *testing.T, steps ...scripted.Step) (*agent.Runtime, *agent.Session, *scripted.Model, *int) {
	t.Helper()
	calls := 0
	m := scripted.New(steps...)
	rt, err := agent.New(m, agent.WithClock(func() time.Time { return fixedNow }), agent.WithMaxSteps(5))
	if err != nil {
		t.Fatal(err)
	}
	if err := rt.RegisterSkill(newTestSkill(&calls)); err != nil {
		t.Fatal(err)
	}
	s, err := agent.NewSession("s1", "Europe/Paris", types.UserContext{ClientID: "c-1", Name: "Camille"})
	if err != nil {
		t.Fatal(err)
	}
	return rt, s, m, &calls
}

func activate() scripted.Step {
	return scripted.Call(agent.ActivateSkillTool, map[string]any{"nom": "demo"})
}

func TestSessionRequiresTimezone(t *testing.T) {
	if _, err := agent.NewSession("s", "", types.UserContext{}); !errors.Is(err, agent.ErrTimezoneRequired) {
		t.Fatalf("err = %v", err)
	}
	if _, err := agent.NewSession("s", "Mars/Olympus", types.UserContext{}); err == nil {
		t.Fatal("fuseau invalide accepté")
	}
}

func TestToolUnavailableBeforeActivation(t *testing.T) {
	rt, s, _, calls := setup(t, scripted.Call("ecrire", map[string]any{"x": 1}), scripted.Say("ok"))
	reply, err := rt.Handle(context.Background(), s, "go")
	if err != nil {
		t.Fatal(err)
	}
	if *calls != 0 || reply.Steps[0].ErrorCode != types.ErrInvalidRequest {
		t.Fatalf("le tool ne doit pas être exécuté avant activation : %+v", reply.Steps)
	}
}

func TestActivationExposesInstructionsAndTools(t *testing.T) {
	rt, s, m, _ := setup(t, activate(), scripted.Say("ok"))
	if _, err := rt.Handle(context.Background(), s, "go"); err != nil {
		t.Fatal(err)
	}
	reqs := m.Requests()
	first, second := reqs[0], reqs[1]
	if strings.Contains(first.System, "INSTRUCTIONS-demo") || len(first.Tools) != 1 {
		t.Fatal("avant activation : ni instructions ni tools du skill")
	}
	if !strings.Contains(second.System, "INSTRUCTIONS-demo") || len(second.Tools) != 3 {
		t.Fatalf("après activation : instructions et tools attendus (%d tools)", len(second.Tools))
	}
	if !strings.Contains(first.System, "skill de test demo") {
		t.Error("le prompt système doit lister les skills")
	}
	// Contexte variable dans le message utilisateur, jamais dans le prompt
	// système (qui doit rester identique entre sessions pour le cache).
	userMsg := first.Messages[0].Content
	for _, want := range []string{"mercredi 30 septembre à 12h", "2026-09-30T12:00:00+02:00", "Europe/Paris", "Camille", "\ngo"} {
		if !strings.Contains(userMsg, want) {
			t.Errorf("message utilisateur sans %q : %q", want, userMsg)
		}
		if want != "\ngo" && strings.Contains(first.System, want) {
			t.Errorf("prompt système avec du contexte variable %q", want)
		}
	}
	if got := s.ActiveSkills(); len(got) != 1 || got[0] != "demo" {
		t.Fatalf("skills actifs = %v", got)
	}
}

func TestSchemaValidationBeforeExecution(t *testing.T) {
	rt, s, _, calls := setup(t,
		activate(),
		scripted.Call("ecrire", map[string]any{}),                  // champ requis absent
		scripted.Call("ecrire", map[string]any{"x": 0}),            // hors bornes
		scripted.Call("ecrire", map[string]any{"x": 1, "y": true}), // propriété inconnue
		scripted.Say("fin"),
	)
	reply, err := rt.Handle(context.Background(), s, "go")
	if err != nil {
		t.Fatal(err)
	}
	codes := []types.ErrorCode{reply.Steps[1].ErrorCode, reply.Steps[2].ErrorCode, reply.Steps[3].ErrorCode}
	want := []types.ErrorCode{types.ErrMissingInformation, types.ErrInvalidRequest, types.ErrInvalidRequest}
	for i := range want {
		if codes[i] != want[i] {
			t.Fatalf("codes = %v, attendu %v", codes, want)
		}
	}
	if *calls != 0 {
		t.Fatal("aucun handler ne doit être appelé avec des arguments invalides")
	}
	var res map[string]any
	_ = json.Unmarshal(reply.Steps[1].Result, &res)
	if res["success"] != false || res["error"].(map[string]any)["missing"].([]any)[0] != "x" {
		t.Fatalf("résultat = %s", reply.Steps[1].Result)
	}
}

func TestEffectsOnlyForConfirmedMutations(t *testing.T) {
	rt, s, _, calls := setup(t, activate(), scripted.Call("ecrire", map[string]any{"x": 2}), scripted.Say("fait"))
	reply, err := rt.Handle(context.Background(), s, "go")
	if err != nil {
		t.Fatal(err)
	}
	if *calls != 1 || len(reply.Effects) != 1 || reply.Effects[0].Tool != "ecrire" {
		t.Fatalf("effets = %+v", reply.Effects)
	}
	if string(reply.Effects[0].Result) != `{"success":true,"ok":true}` {
		t.Fatalf("résultat = %s", reply.Effects[0].Result)
	}
	if reply.Text != "fait" {
		t.Fatalf("texte = %q", reply.Text)
	}
}

func TestPanicInToolIsContained(t *testing.T) {
	rt, s, _, _ := setup(t, activate(), scripted.Call("panique", map[string]any{}), scripted.Say("désolé"))
	reply, err := rt.Handle(context.Background(), s, "go")
	if err != nil {
		t.Fatal(err)
	}
	if reply.Steps[1].ErrorCode != types.ErrInternal {
		t.Fatalf("steps = %+v", reply.Steps)
	}
}

func TestMaxSteps(t *testing.T) {
	steps := []scripted.Step{activate()}
	for i := 0; i < 10; i++ {
		steps = append(steps, scripted.Call("ecrire", map[string]any{"x": 1}))
	}
	rt, s, _, _ := setup(t, steps...)
	if _, err := rt.Handle(context.Background(), s, "go"); !errors.Is(err, agent.ErrMaxSteps) {
		t.Fatalf("err = %v", err)
	}
}

func TestModelErrorRollsBackHistoryWhenNothingHappened(t *testing.T) {
	failing := func(model.Request) (model.Message, error) { return model.Message{}, errors.New("réseau") }
	rt, s, _, _ := setup(t, failing)
	if _, err := rt.Handle(context.Background(), s, "bonjour"); err == nil {
		t.Fatal("erreur attendue")
	}
	if len(s.Messages()) != 0 {
		t.Fatalf("historique non restauré : %+v", s.Messages())
	}
}

func TestModelErrorKeepsHistoryAfterEffects(t *testing.T) {
	failing := func(model.Request) (model.Message, error) { return model.Message{}, errors.New("réseau") }
	rt, s, _, _ := setup(t, activate(), scripted.Call("ecrire", map[string]any{"x": 1}), failing)
	reply, err := rt.Handle(context.Background(), s, "go")
	if err == nil {
		t.Fatal("erreur attendue")
	}
	if len(reply.Effects) != 1 || len(s.Messages()) == 0 {
		t.Fatal("les effets réels doivent être conservés et remontés")
	}
}

func TestRegisterSkillRejectsDuplicates(t *testing.T) {
	calls := 0
	rt, _ := agent.New(scripted.New())
	if err := rt.RegisterSkill(newTestSkill(&calls)); err != nil {
		t.Fatal(err)
	}
	if err := rt.RegisterSkill(newTestSkill(&calls)); err == nil {
		t.Fatal("skill dupliqué accepté")
	}
	other := newTestSkill(&calls)
	other.name = "autre"
	if err := rt.RegisterSkill(other); err == nil {
		t.Fatal("tool dupliqué entre skills accepté")
	}
	bad := testSkill{name: "bad", tools: []types.Tool{{
		Definition: types.ToolDefinition{Name: "t", Parameters: json.RawMessage(`{"type":"object","pattern":"("}`)},
		Handler:    types.ToolHandlerFunc(func(context.Context, types.ToolContext, json.RawMessage) (any, *types.Error) { return nil, nil }),
	}}}
	if err := rt.RegisterSkill(bad); err == nil {
		t.Fatal("schéma invalide accepté")
	}
}

func TestSystemPromptIsStableAcrossSessions(t *testing.T) {
	rt, s1, m, _ := setup(t, activate(), scripted.Say("a"), activate(), scripted.Say("b"))
	s2, _ := agent.NewSession("s2", "America/New_York", types.UserContext{Name: "Autre"})
	if _, err := rt.Handle(context.Background(), s1, "un"); err != nil {
		t.Fatal(err)
	}
	if _, err := rt.Handle(context.Background(), s2, "deux"); err != nil {
		t.Fatal(err)
	}
	reqs := m.Requests()
	if reqs[1].System != reqs[3].System {
		t.Fatal("le prompt système doit être identique d'une session à l'autre")
	}
}

func TestWarmup(t *testing.T) {
	rt, _, m, calls := setup(t, scripted.Say("bonjour"))
	if err := rt.Warmup(context.Background(), "demo"); err != nil {
		t.Fatal(err)
	}
	req := m.Requests()[0]
	if !strings.Contains(req.System, "INSTRUCTIONS-demo") || len(req.Tools) != 3 || *calls != 0 {
		t.Fatalf("préchauffage incorrect : %d tools", len(req.Tools))
	}
	if err := rt.Warmup(context.Background(), "inconnu"); err == nil {
		t.Fatal("skill inconnu accepté")
	}
}
