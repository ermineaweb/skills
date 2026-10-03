package websearchtest

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"skills/services/websearch"
)

const homeHTML = `<!doctype html><html><head><TITLE> Acme &amp; Cie </TITLE></head><body>85 collaborateurs</body></html>`

func newFetcher(t *testing.T) *MockWebFetcher {
	t.Helper()
	m, err := NewMockWebFetcher([]PageFixture{
		{URL: "https://acme.test/", File: "acme.html", Body: homeHTML},
		{URL: "https://acme.test/careers", File: "careers.html", Body: "<title>Carrières</title>"},
		{URL: "https://acme.test/doc.pdf", File: "doc.pdf", ContentType: "application/pdf", Body: "%PDF"},
		{URL: "https://acme.test/search?q=x", File: "q.html", Body: "q"},
		{URL: "https://alias.test/", RedirectTo: "https://acme.test/"},
		{URL: "https://boucle.test/", RedirectTo: "https://boucle.test/"},
		{URL: "https://panne.test/", Status: 500},
		{URL: "https://panne.test/maintenance", File: "m.html", Status: 503, Body: "<title>Maintenance</title>"},
		{URL: "https://lent.test/", Error: "connection timed out"},
	})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestFetchPage(t *testing.T) {
	m := newFetcher(t)
	got, err := m.Fetch(context.Background(), "https://acme.test/")
	if err != nil {
		t.Fatal(err)
	}
	want := websearch.Page{
		RequestedURL: "https://acme.test/", URL: "https://acme.test/", StatusCode: 200,
		ContentType: "text/html; charset=utf-8", Title: "Acme & Cie", Content: homeHTML,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("page = %+v", got)
	}
	if p, _ := m.Fetch(context.Background(), "https://acme.test/doc.pdf"); p.ContentType != "application/pdf" || p.Title != "" {
		t.Fatalf("pdf = %+v", p)
	}
}

func TestFetchURLNormalization(t *testing.T) {
	m := newFetcher(t)
	for _, u := range []string{
		"https://acme.test",
		"http://acme.test/",
		"https://WWW.ACME.test/",
		"https://acme.test/#contact",
		"https://acme.test/careers/",
		"https://acme.test/search?q=x",
	} {
		if _, err := m.Fetch(context.Background(), u); err != nil {
			t.Errorf("%s : %v", u, err)
		}
	}
	// La requête compte : autre requête, autre page (404 sur un hôte connu).
	if _, err := m.Fetch(context.Background(), "https://acme.test/search?q=y"); err == nil {
		t.Error("?q=y ne doit pas correspondre à ?q=x")
	}
}

func TestFetchRedirect(t *testing.T) {
	m := newFetcher(t)
	p, err := m.Fetch(context.Background(), "https://alias.test/")
	if err != nil || p.RequestedURL != "https://alias.test/" || p.URL != "https://acme.test/" || p.Title != "Acme & Cie" {
		t.Fatalf("redirection : %+v %v", p, err)
	}
	if _, err := m.Fetch(context.Background(), "https://boucle.test/"); !errors.Is(err, websearch.ErrUnreachable) {
		t.Fatalf("boucle : %v", err)
	}
}

func TestFetchErrors(t *testing.T) {
	m := newFetcher(t)
	ctx := context.Background()

	var se *websearch.StatusError
	p, err := m.Fetch(ctx, "https://panne.test/")
	if !errors.As(err, &se) || se.StatusCode != 500 || p.StatusCode != 500 {
		t.Fatalf("500 : %+v %v", p, err)
	}
	p, err = m.Fetch(ctx, "https://panne.test/maintenance")
	if !errors.As(err, &se) || se.StatusCode != 503 || p.Title != "Maintenance" {
		t.Fatalf("503 avec corps : %+v %v", p, err)
	}
	p, err = m.Fetch(ctx, "https://acme.test/inexistante")
	if !errors.As(err, &se) || se.StatusCode != 404 || p.StatusCode != 404 {
		t.Fatalf("404 : %+v %v", p, err)
	}
	if _, err := m.Fetch(ctx, "https://inconnu.test/"); !errors.Is(err, websearch.ErrUnreachable) {
		t.Fatalf("hôte inconnu : %v", err)
	}
	if _, err := m.Fetch(ctx, "https://lent.test/"); !errors.Is(err, websearch.ErrUnreachable) || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("panne réseau : %v", err)
	}
	for _, u := range []string{"", "acme.test", "javascript:void(0)", "ftp://acme.test/"} {
		if _, err := m.Fetch(ctx, u); !errors.Is(err, websearch.ErrInvalidURL) {
			t.Errorf("%q : %v", u, err)
		}
	}
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := m.Fetch(cctx, "https://acme.test/"); !errors.Is(err, context.Canceled) {
		t.Fatalf("contexte annulé : %v", err)
	}
}

func TestFetchCalls(t *testing.T) {
	m := newFetcher(t)
	for _, u := range []string{"https://acme.test/", "https://panne.test/", "https://acme.test/"} {
		_, _ = m.Fetch(context.Background(), u)
	}
	if got := m.Calls(); !reflect.DeepEqual(got, []string{"https://acme.test/", "https://panne.test/", "https://acme.test/"}) {
		t.Fatalf("appels = %v", got)
	}
	m.Reset()
	if len(m.Calls()) != 0 {
		t.Fatal("Reset n'a pas vidé les appels")
	}
}

func TestPageValidation(t *testing.T) {
	cases := map[string][]PageFixture{
		"URL invalide":     {{URL: "acme.test", File: "a.html"}},
		"vide":             {{URL: "https://a.test/"}},
		"file et redirect": {{URL: "https://a.test/", File: "a.html", RedirectTo: "https://b.test/"}},
		"status invalide":  {{URL: "https://a.test/", File: "a.html", Status: 42}},
		"redirect invalid": {{URL: "https://a.test/", RedirectTo: "b.test"}},
		"doublon":          {{URL: "https://a.test/", File: "a.html"}, {URL: "http://www.a.test", File: "b.html"}},
	}
	for name, pages := range cases {
		if _, err := NewMockWebFetcher(pages); err == nil {
			t.Errorf("%s : pages acceptées", name)
		}
	}
}
