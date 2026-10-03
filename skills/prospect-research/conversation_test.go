package prospectresearch_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"skills/agent"
	"skills/model/scripted"
	"skills/services/websearch/websearchtest"
	prospectresearch "skills/skills/prospect-research"
	prospecttools "skills/tools/prospects"
	"skills/types"
)

// Ces tests rejouent des conversations avec un modèle scripté : ils
// vérifient les tools et leurs garde-fous sur le moteur simulé, pas la
// qualité de recherche d'un vrai modèle.

type env struct {
	rt      *agent.Runtime
	model   *scripted.Model
	session *agent.Session
	sc      *websearchtest.Scenario
}

func setup(t *testing.T, scenario string) env {
	t.Helper()
	sc, err := websearchtest.LoadScenario(websearchtest.Fixtures(), scenario)
	if err != nil {
		t.Fatal(err)
	}
	skill, err := prospectresearch.New(prospectresearch.Config{Search: sc.Search, Fetcher: sc.Fetcher})
	if err != nil {
		t.Fatal(err)
	}
	m := scripted.New()
	rt, _ := agent.New(m, agent.WithMaxSteps(20))
	if err := rt.RegisterSkill(skill); err != nil {
		t.Fatal(err) // le schéma de sortie doit compiler dans le runtime
	}
	s, err := agent.NewSession("test", "Europe/Paris", types.UserContext{})
	if err != nil {
		t.Fatal(err)
	}
	return env{rt: rt, model: m, session: s, sc: sc}
}

func (e env) turn(t *testing.T, steps ...scripted.Step) agent.Reply {
	t.Helper()
	e.model.Push(steps...)
	r, err := e.rt.Handle(context.Background(), e.session, "Trouve des entreprises SaaS B2B françaises de 20 à 200 salariés.")
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func step(t *testing.T, r agent.Reply, tool string) agent.StepTrace {
	t.Helper()
	for i := len(r.Steps) - 1; i >= 0; i-- {
		if r.Steps[i].Tool == tool {
			return r.Steps[i]
		}
	}
	t.Fatalf("aucun appel à %s", tool)
	return agent.StepTrace{}
}

// acmeResult renvoie un résultat valide pour Acme ; edit permet de le
// dégrader pour tester les refus.
func acmeResult(edit func(p map[string]any)) map[string]any {
	fact := func(v any, ids ...string) map[string]any {
		if ids == nil {
			ids = []string{}
		}
		status := "verified"
		if v == nil {
			status = "unknown"
		}
		return map[string]any{"value": v, "status": status, "source_ids": ids}
	}
	prospect := map[string]any{
		"id": "acme-saas.test",
		"company": map[string]any{
			"name": "Acme SaaS", "domain": "acme-saas.test", "website": "https://acme-saas.test/",
			"legal_name":     fact("ACME SAS", "s2"),
			"registry_ids":   []any{map[string]any{"scheme": "SIREN", "value": "900100200", "source_ids": []string{"s2"}}},
			"country":        fact("FR", "s2"),
			"headquarters":   fact("Lyon", "s1", "s2"),
			"industry":       fact("Logiciel d'automatisation de workflows", "s1"),
			"business_model": fact("SaaS B2B", "s1"),
			"description":    fact("Plateforme SaaS d'automatisation des workflows finance et opérations.", "s1"),
			"employees":      map[string]any{"min": 85, "max": 85, "status": "verified", "source_ids": []string{"s1"}},
			"revenue":        map[string]any{"min": nil, "max": nil, "unit": "EUR", "status": "unknown", "source_ids": []string{}},
			"technologies":   []any{},
			"parent_company": fact(nil),
			"former_names":   []string{"ACME Software"},
		},
		"icp_match": map[string]any{"status": "match", "matched_criteria": []string{"c1", "c2", "c3"}, "unverified_criteria": []string{}, "failed_criteria": []string{}},
		"signals":   []any{},
		"sources": []any{
			map[string]any{"id": "s1", "url": "https://acme-saas.test/", "title": "Acme SaaS", "type": "primary", "kind": "official_site", "accessed_at": "2026-10-03"},
			map[string]any{"id": "s2", "url": "https://registre-entreprises.test/societe/900100200", "title": "ACME SAS", "type": "primary", "kind": "registry", "accessed_at": "2026-10-03"},
		},
		"confidence": "high", "confidence_reason": "Identité et effectif établis par le site et le registre.", "to_verify": []string{},
	}
	if edit != nil {
		edit(prospect)
	}
	return map[string]any{"resultat": map[string]any{
		"schema_version": "1.0", "status": "partial",
		"request": map[string]any{
			"summary": "SaaS B2B français, 20 à 200 salariés.", "requested_count": 10,
			"criteria": []any{
				map[string]any{"id": "c1", "kind": "must_have", "dimension": "industry", "description": "Éditeur SaaS", "origin": "user"},
				map[string]any{"id": "c2", "kind": "must_have", "dimension": "geography", "description": "France", "origin": "user"},
				map[string]any{"id": "c3", "kind": "must_have", "dimension": "employees", "description": "20 à 200 salariés", "origin": "user"},
			},
			"signals_sought": []string{}, "signal_window_months": 12, "assumptions": []string{"10 prospects par défaut."},
		},
		"missing_inputs": []string{},
		"prospects":      []any{prospect},
		"excluded":       []any{map[string]any{"name": "Ferrolux Industrie", "domain": "ferrolux.test", "reason": "Industrie", "criterion_ids": []string{"c1"}}},
		"search_log":     map[string]any{"strategies": []string{"secteur × pays"}, "queries": []string{"SaaS France"}, "unreachable_urls": []string{}},
		"warnings":       []string{},
	}}
}

// research : activation, recherche, lecture de deux pages, puis remise.
func research(result map[string]any) []scripted.Step {
	return []scripted.Step{
		scripted.Call(agent.ActivateSkillTool, map[string]any{"nom": prospectresearch.Name}),
		scripted.Call("web_search", map[string]any{"query": "Acme"}),
		scripted.Call("web_fetch", map[string]any{"url": "https://acme-saas.test/"}),
		scripted.Call("web_fetch", map[string]any{"url": "https://registre-entreprises.test/societe/900100200"}),
		scripted.Call("enregistrer_prospects", result),
		scripted.Say("J'ai trouvé 1 prospect : Acme SaaS."),
	}
}

func TestSkillDefinition(t *testing.T) {
	sc, _ := websearchtest.LoadScenario(websearchtest.Fixtures(), "basic")
	s, err := prospectresearch.New(prospectresearch.Config{Search: sc.Search, Fetcher: sc.Fetcher})
	if err != nil {
		t.Fatal(err)
	}
	if strings.HasPrefix(s.Instructions(), "---") || !strings.Contains(s.Instructions(), "## Dans cette application") {
		t.Fatal("instructions : corps de SKILL.md sans en-tête, suivi de hote.md")
	}
	if !strings.Contains(s.Description(), "ICP") {
		t.Fatalf("description = %q", s.Description())
	}
	var names []string
	for _, tool := range s.Tools() {
		names = append(names, tool.Definition.Name)
	}
	if strings.Join(names, ",") != "web_search,web_fetch,enregistrer_prospects" {
		t.Fatalf("tools = %v", names)
	}
	if _, err := prospectresearch.New(prospectresearch.Config{}); err == nil {
		t.Fatal("Search et Fetcher sont obligatoires")
	}
}

func TestResearchIsRecorded(t *testing.T) {
	e := setup(t, "basic")
	r := e.turn(t, research(acmeResult(nil))...)
	if st := step(t, r, "enregistrer_prospects"); !st.Success {
		t.Fatalf("remise refusée : %s", st.Result)
	}
	if len(r.Effects) != 1 || r.Effects[0].Tool != "enregistrer_prospects" {
		t.Fatalf("effets = %v", r.Effects)
	}
	raw, ok := prospecttools.LastResult(e.session.State())
	var got struct {
		Prospects []struct {
			ID string `json:"id"`
		} `json:"prospects"`
	}
	if !ok || json.Unmarshal(raw, &got) != nil || len(got.Prospects) != 1 || got.Prospects[0].ID != "acme-saas.test" {
		t.Fatalf("résultat conservé : %s", raw)
	}
	if calls := e.sc.Fetcher.Calls(); len(calls) != 2 {
		t.Fatalf("pages lues : %v", calls)
	}
}

func TestResultRefused(t *testing.T) {
	cases := map[string]struct {
		edit func(p map[string]any)
		code types.ErrorCode
		want string
	}{
		"URL inventée": {func(p map[string]any) {
			p["sources"] = append(p["sources"].([]any), map[string]any{"id": "s3", "url": "https://acme-saas.test/a-propos", "type": "primary", "kind": "official_site"})
		}, types.ErrInvalidRequest, "ni renvoyée par web_search ni lue"},
		"site inventé": {func(p map[string]any) {
			p["company"].(map[string]any)["website"] = "https://acme.example/"
		}, types.ErrInvalidRequest, "website"},
		"source_id inconnu": {func(p map[string]any) {
			p["company"].(map[string]any)["industry"].(map[string]any)["source_ids"] = []string{"s9"}
		}, types.ErrInvalidRequest, "absent de ses sources"},
		"hors schéma": {func(p map[string]any) {
			p["confidence"] = "certaine"
		}, types.ErrInvalidRequest, "confidence"},
		"inconnu remplacé par 0": {func(p map[string]any) {
			p["company"].(map[string]any)["employees"] = map[string]any{"min": 0, "max": 0, "status": "inconnu", "source_ids": []string{}}
		}, types.ErrInvalidRequest, "status"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			e := setup(t, "basic")
			steps := research(acmeResult(c.edit))
			r := e.turn(t, steps...)
			st := step(t, r, "enregistrer_prospects")
			if st.Success || st.ErrorCode != c.code || !strings.Contains(string(st.Result), c.want) {
				t.Fatalf("résultat : %s", st.Result)
			}
			if _, ok := prospecttools.LastResult(e.session.State()); ok || len(r.Effects) != 0 {
				t.Fatal("un résultat refusé ne doit pas être conservé")
			}
		})
	}
}

func TestDuplicateProspectRefused(t *testing.T) {
	e := setup(t, "basic")
	res := acmeResult(nil)
	inner := res["resultat"].(map[string]any)
	inner["prospects"] = append(inner["prospects"].([]any), inner["prospects"].([]any)[0])
	r := e.turn(t, research(res)...)
	if st := step(t, r, "enregistrer_prospects"); st.Success || !strings.Contains(string(st.Result), "deux fois") {
		t.Fatalf("doublon accepté : %s", st.Result)
	}
}

func TestWebToolErrors(t *testing.T) {
	e := setup(t, "degraded-search")
	r := e.turn(t,
		scripted.Call(agent.ActivateSkillTool, map[string]any{"nom": prospectresearch.Name}),
		scripted.Call("web_search", map[string]any{"query": "SaaS France"}),
		scripted.Call("web_search", map[string]any{"query": "SaaS Lyon"}),
		scripted.Call("web_fetch", map[string]any{"url": "https://zenith-analytics.test/"}),
		scripted.Call("web_fetch", map[string]any{"url": "https://acme-saas.test/"}),
		scripted.Say("Le moteur de recherche est en partie indisponible."),
	)
	if r.Steps[1].ErrorCode != types.ErrSearchUnavailable {
		t.Errorf("panne du moteur : %s", r.Steps[1].Result)
	}
	var lyon struct {
		Results []struct {
			URL string `json:"url"`
		} `json:"results"`
	}
	_ = json.Unmarshal(r.Steps[2].Result, &lyon)
	if len(lyon.Results) != 1 || lyon.Results[0].URL != "https://acme-saas.test/" {
		t.Errorf("les résultats malformés ne doivent pas être transmis : %s", r.Steps[2].Result)
	}
	if r.Steps[3].ErrorCode != types.ErrPageUnavailable || !strings.Contains(string(r.Steps[3].Result), "500") {
		t.Errorf("page en erreur : %s", r.Steps[3].Result)
	}
	var page struct {
		Title string `json:"title"`
		Text  string `json:"text"`
	}
	_ = json.Unmarshal(r.Steps[4].Result, &page)
	if page.Title == "" || strings.Contains(page.Text, "<") || !strings.Contains(page.Text, "85 collaborateurs") {
		t.Errorf("texte de la page : %+v", page)
	}
}
