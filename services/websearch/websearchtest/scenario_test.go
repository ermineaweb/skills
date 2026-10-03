package websearchtest

import (
	"context"
	"errors"
	"io/fs"
	"reflect"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"skills/services/websearch"
)

func load(t *testing.T, name string) *Scenario {
	t.Helper()
	s, err := LoadScenario(Fixtures(), name)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestAllScenariosLoad(t *testing.T) {
	names, err := ScenarioNames(Fixtures())
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"basic", "commercial-signals", "conflicting-data", "degraded-search", "duplicates", "inaccessible-page", "missing-data"}
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("scénarios = %v", names)
	}
	for _, name := range names {
		s := load(t, name)
		if s.Name != name || s.ICP == "" || s.Description == "" {
			t.Errorf("%s : métadonnées incomplètes", name)
		}
	}
}

// Chaque résultat de recherche bien formé pointe vers une page déclarée
// (contenu, erreur ou redirection) : une faute de frappe dans une URL de
// fixture ferait sinon passer une page pour injoignable.
func TestSearchResultsPointToDeclaredPages(t *testing.T) {
	names, _ := ScenarioNames(Fixtures())
	for _, name := range names {
		s := load(t, name)
		for _, e := range s.Search.entries {
			for _, r := range e.Results {
				if !r.Malformed && !s.Fetcher.Has(r.URL) {
					t.Errorf("%s : %v → %s n'est pas déclarée dans les pages", name, e.Keywords, r.URL)
				}
			}
		}
	}
}

// Chaque fichier HTML de pages/ est utilisé par l'index ou un scénario.
func TestNoOrphanPage(t *testing.T) {
	fsys := Fixtures()
	used := map[string]bool{}
	var index struct {
		Pages []PageFixture `json:"pages"`
	}
	if err := decodeFile(fsys, "pages/index.json", &index); err != nil {
		t.Fatal(err)
	}
	pages := index.Pages
	names, _ := ScenarioNames(fsys)
	for _, name := range names {
		var sc scenarioFile
		if err := decodeFile(fsys, "scenarios/"+name+".json", &sc); err != nil {
			t.Fatal(err)
		}
		pages = append(pages, sc.Pages...)
	}
	for _, p := range pages {
		used[p.File] = true
	}
	files, _ := fs.Glob(fsys, "pages/*.html")
	for _, f := range files {
		if !used[strings.TrimPrefix(f, "pages/")] {
			t.Errorf("%s n'est référencé nulle part", f)
		}
	}
}

func TestUniverse(t *testing.T) {
	s := load(t, "basic")
	companies := 0
	for host := range s.Fetcher.hosts {
		switch host {
		case "annuaire-pro.test", "registre-entreprises.test", "tech-news.test", "acmesoftware.test", "brightpay.test":
		default:
			companies++
		}
		if !strings.HasSuffix(host, ".test") {
			t.Errorf("domaine %q hors du TLD réservé .test", host)
		}
	}
	if companies < 15 || companies > 20 {
		t.Fatalf("%d entreprises dans l'univers", companies)
	}
}

func TestBasicScenario(t *testing.T) {
	s := load(t, "basic")
	ctx := context.Background()

	got, err := s.Search.Search(ctx, "SaaS B2B France 20 200 employés")
	if err != nil || len(got) < 10 {
		t.Fatalf("recherche : %d résultats, %v", len(got), err)
	}
	// Snippet trompeur : le moteur annonce un éditeur SaaS, la page dit le contraire.
	i := slices.IndexFunc(got, func(r websearch.SearchResult) bool { return r.Domain == "cloudnova.test" })
	if i < 0 || !strings.Contains(got[i].Snippet, "SaaS B2B") {
		t.Fatalf("snippet Cloudnova : %+v", got)
	}
	page, err := s.Fetcher.Fetch(ctx, got[i].URL)
	if err != nil || !strings.Contains(page.Content, "ESN") || !strings.Contains(page.Content, "pas de logiciel propre") {
		t.Fatalf("page Cloudnova : %v", err)
	}
	var se *websearch.StatusError
	if _, err := s.Fetcher.Fetch(ctx, "https://zenith-analytics.test/"); !errors.As(err, &se) || se.StatusCode != 500 {
		t.Fatalf("Zenith : %v", err)
	}
	// Filiale : effectif de l'entité et du groupe sur deux domaines distincts.
	fr, _ := s.Fetcher.Fetch(ctx, "https://brightpay-fr.test/")
	group, _ := s.Fetcher.Fetch(ctx, "https://brightpay.test/")
	if !strings.Contains(fr.Content, "110 salariés") || !strings.Contains(group.Content, "3,000 employees") {
		t.Fatal("pages Brightpay")
	}
}

func TestMissingDataScenario(t *testing.T) {
	s := load(t, "missing-data")
	ctx := context.Background()
	got, _ := s.Search.Search(ctx, "Calmeo effectif")
	if !reflect.DeepEqual(urls(got), []string{"https://calmeo.test/"}) {
		t.Fatalf("Calmeo : la fiche de registre ne doit pas être trouvable : %v", urls(got))
	}
	page, _ := s.Fetcher.Fetch(ctx, "https://calmeo.test/")
	if strings.Contains(page.Content, "collaborateurs") {
		t.Fatal("la page Calmeo de ce scénario ne doit pas donner d'effectif")
	}
	var se *websearch.StatusError
	if _, err := s.Fetcher.Fetch(ctx, "https://registre-entreprises.test/societe/900500600"); !errors.As(err, &se) {
		t.Fatalf("registre Calmeo : %v", err)
	}
	// Le scénario basic n'est pas affecté par ces surcharges.
	basic, _ := load(t, "basic").Fetcher.Fetch(ctx, "https://calmeo.test/")
	if !strings.Contains(basic.Content, "60 collaborateurs") {
		t.Fatal("surcharge de page propagée hors de son scénario")
	}
}

func TestDuplicatesScenario(t *testing.T) {
	s := load(t, "duplicates")
	ctx := context.Background()
	got, _ := s.Search.Search(ctx, "éditeur SaaS français Lyon")
	domains := map[string]int{}
	for _, r := range got {
		domains[r.Domain]++
	}
	if domains["acme-saas.test"] != 2 || domains["acmesoftware.test"] != 1 || domains["annuaire-pro.test"] != 1 || domains["registre-entreprises.test"] != 1 {
		t.Fatalf("références à Acme : %v", domains)
	}
	p, err := s.Fetcher.Fetch(ctx, "https://acmesoftware.test/")
	if err != nil || p.URL != "https://acme-saas.test/" {
		t.Fatalf("alias Acme : %+v %v", p, err)
	}
	if !strings.Contains(p.Content, "anciennement ACME Software") {
		t.Fatal("l'ancien nom doit être mentionné")
	}
}

func TestConflictingDataScenario(t *testing.T) {
	s := load(t, "conflicting-data")
	ctx := context.Background()
	site, _ := s.Fetcher.Fetch(ctx, "https://kalista.test/")
	dir, _ := s.Fetcher.Fetch(ctx, "https://annuaire-pro.test/entreprises/kalista")
	if !strings.Contains(site.Content, "120 personnes") || !strings.Contains(dir.Content, "250 salariés") {
		t.Fatal("les deux sources Kalista doivent se contredire")
	}
	about, _ := s.Fetcher.Fetch(ctx, "https://logitrame.test/about")
	if !strings.Contains(about.Content, "En 2018") {
		t.Fatal("Logitrame : donnée datée attendue")
	}
}

func TestCommercialSignalsScenario(t *testing.T) {
	s := load(t, "commercial-signals")
	ctx := context.Background()
	got, _ := s.Search.Search(ctx, "SaaS France recrutement commercial")
	want := []string{"acme-saas.test", "nuvio.test", "sendara.test", "zenith-analytics.test"}
	var domains []string
	for _, r := range got {
		domains = append(domains, r.Domain)
	}
	if !reflect.DeepEqual(domains, want) {
		t.Fatalf("recrutement : %v", domains)
	}
	careers, _ := s.Fetcher.Fetch(ctx, "https://acme-saas.test/careers")
	for _, job := range []string{"Account Executive", "Sales Manager", "Customer Success Manager"} {
		if !strings.Contains(careers.Content, job) {
			t.Errorf("poste %q absent", job)
		}
	}
	news, _ := s.Fetcher.Fetch(ctx, "https://nuvio.test/news")
	for _, signal := range []string{"Lancement de Nuvio Cards", "bureau de Madrid", "Partenariat avec Banque Horizon"} {
		if !strings.Contains(news.Content, signal) {
			t.Errorf("signal %q absent", signal)
		}
	}
	calmeo, _ := s.Search.Search(ctx, "Calmeo")
	for _, r := range calmeo {
		if strings.Contains(r.URL, "careers") || strings.Contains(r.URL, "news") {
			t.Errorf("Calmeo ne doit avoir aucune page de signal : %s", r.URL)
		}
	}
}

func TestInaccessiblePageScenario(t *testing.T) {
	s := load(t, "inaccessible-page")
	ctx := context.Background()
	var se *websearch.StatusError
	for url, code := range map[string]int{
		"https://acme-saas.test/careers":        404,
		"https://sendara.test/careers":          503,
		"https://zenith-analytics.test/":        500,
		"https://zenith-analytics.test/careers": 404,
	} {
		if _, err := s.Fetcher.Fetch(ctx, url); !errors.As(err, &se) || se.StatusCode != code {
			t.Errorf("%s : %v, attendu %d", url, err, code)
		}
	}
	if _, err := s.Fetcher.Fetch(ctx, "https://nuvio.test/"); !errors.Is(err, websearch.ErrUnreachable) {
		t.Errorf("Nuvio : %v", err)
	}
	if _, err := s.Fetcher.Fetch(ctx, "https://nuvio.test/news"); err != nil {
		t.Errorf("les autres pages de Nuvio restent accessibles : %v", err)
	}
}

func TestDegradedSearchScenario(t *testing.T) {
	s := load(t, "degraded-search")
	ctx := context.Background()
	for _, q := range []string{"SaaS France", "SaaS France recrutement"} {
		if _, err := s.Search.Search(ctx, q); !errors.Is(err, websearch.ErrSearchFailed) {
			t.Errorf("%q : %v", q, err)
		}
	}
	if got, err := s.Search.Search(ctx, "SaaS B2B France"); err != nil || len(got) != 0 {
		t.Errorf("résultats vides attendus : %v %v", got, err)
	}
	got, _ := s.Search.Search(ctx, "SaaS Lyon")
	if len(got) != 4 || got[0].Domain != "" || got[1].Domain != "" || got[2].Domain != "" || got[3].Domain != "acme-saas.test" {
		t.Errorf("résultats malformés : %+v", got)
	}
	got, _ = s.Search.Search(ctx, "SaaS Lille")
	if len(got) != 4 || got[0].URL != got[1].URL {
		t.Errorf("résultat dupliqué : %+v", got)
	}
	// Recherches non surchargées : fixtures communes.
	if got, _ := s.Search.Search(ctx, "Kalista"); len(got) != 2 {
		t.Errorf("Kalista : %v", urls(got))
	}
}

func TestLoadIsDeterministicAndIsolated(t *testing.T) {
	a, b := load(t, "basic"), load(t, "basic")
	ctx := context.Background()
	for _, q := range []string{"SaaS France", "SaaS B2B France", "Acme", "Kalista effectif", "SaaS levée de fonds"} {
		ra, errA := a.Search.Search(ctx, q)
		rb, errB := b.Search.Search(ctx, q)
		if !reflect.DeepEqual(ra, rb) || (errA == nil) != (errB == nil) {
			t.Errorf("%q : résultats différents entre deux chargements", q)
		}
	}
	pa, _ := a.Fetcher.Fetch(ctx, "https://nuvio.test/")
	pb, _ := b.Fetcher.Fetch(ctx, "https://nuvio.test/")
	if !reflect.DeepEqual(pa, pb) {
		t.Error("même page, contenus différents")
	}
	if len(a.Search.Calls()) != 5 || len(load(t, "basic").Search.Calls()) != 0 {
		t.Error("chaque chargement doit avoir son propre historique d'appels")
	}
}

func TestLoadScenarioErrors(t *testing.T) {
	index := `{"pages": [{"url": "https://a.test/", "file": "a.html"}]}`
	base := fstest.MapFS{
		"pages/index.json":  {Data: []byte(index)},
		"pages/a.html":      {Data: []byte("<title>A</title>")},
		"search/s.json":     {Data: []byte(`{"entries": [{"keywords": ["a"], "results": [{"title": "A", "url": "https://a.test/", "snippet": ""}]}]}`)},
		"search/typo.json":  {Data: []byte(`{"entries": [{"keyword": ["a"], "results": []}]}`)},
		"scenarios/ok.json": {Data: []byte(`{"name": "ok", "icp": "x", "search_files": ["s.json"], "expect": {"prospects": ["a.test"]}}`)},
	}
	if _, err := LoadScenario(base, "ok"); err != nil {
		t.Fatalf("scénario minimal : %v", err)
	}
	cases := map[string]string{
		"nom incohérent":     `{"name": "autre", "icp": "x"}`,
		"icp absent":         `{"name": "bad"}`,
		"champ inconnu":      `{"name": "bad", "icp": "x", "pagez": []}`,
		"fichier absent":     `{"name": "bad", "icp": "x", "search_files": ["absent.json"]}`,
		"faute de frappe":    `{"name": "bad", "icp": "x", "search_files": ["typo.json"]}`,
		"page sans fichier":  `{"name": "bad", "icp": "x", "pages": [{"url": "https://b.test/", "file": "absent.html"}]}`,
		"domaine inconnu":    `{"name": "bad", "icp": "x", "expect": {"prospects": ["z.test"]}}`,
		"attendu et exclu":   `{"name": "bad", "icp": "x", "expect": {"prospects": ["a.test"], "excluded": ["a.test"]}}`,
		"JSON en trop":       `{"name": "bad", "icp": "x"} {}`,
		"surcharge invalide": `{"name": "bad", "icp": "x", "pages": [{"url": "b.test", "status": 404}]}`,
	}
	for name, raw := range cases {
		fsys := fstest.MapFS{"scenarios/bad.json": {Data: []byte(raw)}}
		for k, v := range base {
			fsys[k] = v
		}
		if _, err := LoadScenario(fsys, "bad"); err == nil {
			t.Errorf("%s : scénario accepté", name)
		}
	}
}
