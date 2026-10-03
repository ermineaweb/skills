package websearchtest

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"

	"skills/services/websearch"
)

func result(url string) ResultFixture {
	return ResultFixture{Title: url, URL: url, Snippet: "…"}
}

func newSearch(t *testing.T, entries ...SearchEntry) *MockSearchEngine {
	t.Helper()
	m, err := NewMockSearchEngine(entries)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func urls(rs []websearch.SearchResult) []string {
	out := []string{}
	for _, r := range rs {
		out = append(out, r.URL)
	}
	return out
}

func TestSearchSelection(t *testing.T) {
	m := newSearch(t,
		SearchEntry{Keywords: []string{"saas", "france|french"}, Results: []ResultFixture{result("https://a.test/")}},
		SearchEntry{Keywords: []string{"saas", "france", "recrutement|hiring"}, Results: []ResultFixture{result("https://b.test/")}},
		SearchEntry{Keywords: []string{"saas", "france"}, Results: []ResultFixture{result("https://doublon.test/")}},
		SearchEntry{Keywords: []string{"acme"}, Priority: 1, Results: []ResultFixture{result("https://acme.test/")}},
		SearchEntry{Keywords: []string{"employés"}, Results: []ResultFixture{result("https://e.test/")}},
	)
	cases := []struct {
		query string
		want  []string
	}{
		{"SaaS France", []string{"https://a.test/"}},
		{"french saas companies", []string{"https://a.test/"}},
		{"SaaS France recrutement commercial", []string{"https://b.test/"}}, // plus spécifique
		{"saas FRANCE hiring", []string{"https://b.test/"}},
		{"Acme SaaS France", []string{"https://acme.test/"}}, // priorité
		{"EMPLOYES", []string{"https://e.test/"}},            // accents et casse ignorés
		{"nombre d'employés", []string{"https://e.test/"}},
		{"saasify france", []string{}}, // mot entier, pas sous-chaîne
		{"logiciel", []string{}},       // aucune entrée : liste vide, pas d'erreur
	}
	for _, c := range cases {
		got, err := m.Search(context.Background(), c.query)
		if err != nil {
			t.Fatalf("%q : %v", c.query, err)
		}
		if !reflect.DeepEqual(urls(got), c.want) {
			t.Errorf("%q → %v, attendu %v", c.query, urls(got), c.want)
		}
	}
}

func TestSearchResultFields(t *testing.T) {
	m := newSearch(t, SearchEntry{Keywords: []string{"x"}, Results: []ResultFixture{
		{Title: "T", URL: "https://WWW.Acme.test/careers", Snippet: "S", PublishedAt: "2026-09-15"},
		{Title: "", URL: "acme.test/sans-schema", Malformed: true},
	}})
	got, _ := m.Search(context.Background(), "x")
	want := []websearch.SearchResult{
		{Title: "T", URL: "https://WWW.Acme.test/careers", Snippet: "S", Domain: "acme.test", PublishedAt: "2026-09-15"},
		{URL: "acme.test/sans-schema"}, // malformé : renvoyé tel quel, Domain vide
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("résultats = %+v", got)
	}
	got[0].Title = "modifié"
	again, _ := m.Search(context.Background(), "x")
	if again[0].Title != "T" {
		t.Fatal("modifier un résultat renvoyé ne doit pas modifier les fixtures")
	}
}

func TestSearchErrors(t *testing.T) {
	m := newSearch(t,
		SearchEntry{Keywords: []string{"panne"}, Error: "backend indisponible"},
		SearchEntry{Keywords: []string{"vide"}, Results: []ResultFixture{}},
	)
	if _, err := m.Search(context.Background(), "panne"); !errors.Is(err, websearch.ErrSearchFailed) || !strings.Contains(err.Error(), "backend indisponible") {
		t.Fatalf("panne : %v", err)
	}
	if got, err := m.Search(context.Background(), "vide"); err != nil || got == nil || len(got) != 0 {
		t.Fatalf("vide : %v %v", got, err)
	}
	if _, err := m.Search(context.Background(), "  ?! "); !errors.Is(err, websearch.ErrSearchFailed) {
		t.Fatalf("requête vide : %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := m.Search(ctx, "vide"); !errors.Is(err, context.Canceled) {
		t.Fatalf("contexte annulé : %v", err)
	}
}

func TestSearchEntryValidation(t *testing.T) {
	cases := map[string]SearchEntry{
		"sans mot-clé":        {Results: []ResultFixture{}},
		"sans results":        {Keywords: []string{"x"}},
		"error et results":    {Keywords: []string{"x"}, Error: "e", Results: []ResultFixture{result("https://a.test/")}},
		"variante multi-mots": {Keywords: []string{"saas b2b"}, Results: []ResultFixture{}},
		"variante vide":       {Keywords: []string{"a|"}, Results: []ResultFixture{}},
		"URL invalide":        {Keywords: []string{"x"}, Results: []ResultFixture{{Title: "t", URL: "a.test"}}},
		"titre vide":          {Keywords: []string{"x"}, Results: []ResultFixture{{URL: "https://a.test/"}}},
	}
	for name, e := range cases {
		if _, err := NewMockSearchEngine([]SearchEntry{e}); err == nil {
			t.Errorf("%s : entrée acceptée", name)
		}
	}
}

func TestSearchCallsAndDeterminism(t *testing.T) {
	m := newSearch(t,
		SearchEntry{Keywords: []string{"a"}, Results: []ResultFixture{result("https://a.test/"), result("https://b.test/")}},
		SearchEntry{Keywords: []string{"panne"}, Error: "x"},
	)
	first, _ := m.Search(context.Background(), "a")
	_, _ = m.Search(context.Background(), "panne")
	second, _ := m.Search(context.Background(), "a")
	if !reflect.DeepEqual(first, second) {
		t.Fatal("même requête, résultats différents")
	}
	if got := m.Calls(); !reflect.DeepEqual(got, []string{"a", "panne", "a"}) {
		t.Fatalf("appels = %v", got)
	}
	m.Reset()
	if len(m.Calls()) != 0 {
		t.Fatal("Reset n'a pas vidé les appels")
	}
}

func TestSearchConcurrent(t *testing.T) {
	m := newSearch(t, SearchEntry{Keywords: []string{"a"}, Results: []ResultFixture{result("https://a.test/")}})
	var wg sync.WaitGroup
	for range 50 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if got, err := m.Search(context.Background(), "a"); err != nil || len(got) != 1 {
				t.Error("résultat inattendu en concurrence")
			}
		}()
	}
	wg.Wait()
	if n := len(m.Calls()); n != 50 {
		t.Fatalf("appels = %d", n)
	}
}
