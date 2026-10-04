package searxng

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"skills/services/websearch"
)

func TestSearch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/search" || r.URL.Query().Get("format") != "json" {
			t.Errorf("requête = %s", r.URL)
		}
		switch r.URL.Query().Get("q") {
		case "saas lyon":
			w.Write([]byte(`{"results":[
				{"url":"https://www.acme.fr/","title":" Acme ","content":"Éditeur SaaS","publishedDate":null},
				{"url":"https://www.acme.fr/","title":"Acme (doublon)","content":""},
				{"url":"https://news.test/a","title":"Levée","content":"…","publishedDate":"2026-09-12T00:00:00"}
			],"unresponsive_engines":[["google","CAPTCHA"]]}`))
		case "rien":
			w.Write([]byte(`{"results":[],"unresponsive_engines":[]}`))
		case "bloqué":
			w.Write([]byte(`{"results":[],"unresponsive_engines":[["google","CAPTCHA"],["bing","timeout"]]}`))
		default:
			w.WriteHeader(http.StatusForbidden)
		}
	}))
	defer srv.Close()
	e, err := New(srv.URL+"/", nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	got, err := e.Search(ctx, "saas lyon")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("résultats = %+v", got)
	}
	if got[0] != (websearch.SearchResult{Title: "Acme", URL: "https://www.acme.fr/", Snippet: "Éditeur SaaS", Domain: "acme.fr"}) {
		t.Errorf("résultat 0 = %+v", got[0])
	}
	if got[1].PublishedAt != "2026-09-12" {
		t.Errorf("date = %q", got[1].PublishedAt)
	}

	if got, err := e.Search(ctx, "rien"); err != nil || len(got) != 0 {
		t.Errorf("aucun résultat : %v, %v", got, err)
	}
	for _, q := range []string{"bloqué", "json désactivé"} {
		if _, err := e.Search(ctx, q); !errors.Is(err, websearch.ErrSearchFailed) {
			t.Errorf("%s : erreur = %v", q, err)
		}
	}
}

func TestNewInvalidURL(t *testing.T) {
	for _, u := range []string{"", "searxng:8080", "ftp://searxng"} {
		if _, err := New(u, nil); err == nil {
			t.Errorf("New(%q) accepté", u)
		}
	}
}
