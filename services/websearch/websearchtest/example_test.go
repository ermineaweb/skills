package websearchtest_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"

	"skills/services/websearch"
	"skills/services/websearch/websearchtest"
)

// collect est un composant extérieur qui ne connaît que les interfaces :
// il cherche, puis lit une fois chaque page trouvée et range le résultat
// par domaine final (après redirection). Les futurs tools web_search et
// web_fetch du skill reçoivent ces interfaces de la même façon.
func collect(ctx context.Context, se websearch.SearchEngine, wf websearch.WebFetcher, queries []string) (pages map[string][]string, failed map[string]string, err error) {
	pages, failed = map[string][]string{}, map[string]string{}
	seen := map[string]bool{}
	for _, q := range queries {
		results, err := se.Search(ctx, q)
		if err != nil {
			return nil, nil, err
		}
		for _, r := range results {
			if r.Domain == "" || seen[r.URL] {
				continue
			}
			seen[r.URL] = true
			p, err := wf.Fetch(ctx, r.URL)
			var se *websearch.StatusError
			switch {
			case errors.As(err, &se):
				failed[r.URL] = fmt.Sprint(se.StatusCode)
			case err != nil:
				failed[r.URL] = "injoignable"
			default:
				d := websearch.Domain(p.URL)
				if !slices.Contains(pages[d], p.URL) {
					pages[d] = append(pages[d], p.URL)
				}
			}
		}
	}
	return pages, failed, nil
}

func TestInjectedThroughInterfaces(t *testing.T) {
	s, err := websearchtest.LoadScenario(websearchtest.Fixtures(), "duplicates")
	if err != nil {
		t.Fatal(err)
	}
	pages, failed, err := collect(context.Background(), s.Search, s.Fetcher, []string{"SaaS France", "Altiva"})
	if err != nil {
		t.Fatal(err)
	}
	// L'alias acmesoftware.test aboutit sur acme-saas.test : un seul domaine.
	if _, ok := pages["acmesoftware.test"]; ok {
		t.Error("l'alias doit être rangé sous le domaine final")
	}
	if got := pages["acme-saas.test"]; !slices.Equal(got, []string{"https://acme-saas.test/", "https://acme-saas.test/careers"}) {
		t.Errorf("acme-saas.test : %v", got)
	}
	if len(pages["altiva.test"]) != 1 || len(pages["altiva-conseil.test"]) != 1 || len(failed) != 0 {
		t.Errorf("pages = %v, échecs = %v", pages, failed)
	}
	if got := s.Search.Calls(); !slices.Equal(got, []string{"SaaS France", "Altiva"}) {
		t.Errorf("recherches = %v", got)
	}

	s, _ = websearchtest.LoadScenario(websearchtest.Fixtures(), "inaccessible-page")
	_, failed, _ = collect(context.Background(), s.Search, s.Fetcher, []string{"SaaS France recrutement"})
	want := map[string]string{
		"https://acme-saas.test/careers":        "404",
		"https://sendara.test/careers":          "503",
		"https://zenith-analytics.test/careers": "404",
	}
	if fmt.Sprint(failed) != fmt.Sprint(want) {
		t.Errorf("échecs = %v", failed)
	}

	s, _ = websearchtest.LoadScenario(websearchtest.Fixtures(), "degraded-search")
	if _, _, err := collect(context.Background(), s.Search, s.Fetcher, []string{"SaaS France"}); !errors.Is(err, websearch.ErrSearchFailed) {
		t.Errorf("panne du moteur : %v", err)
	}
}

func Example() {
	s, err := websearchtest.LoadScenario(websearchtest.Fixtures(), "commercial-signals")
	if err != nil {
		panic(err)
	}
	// Dans l'application : injecter s.Search et s.Fetcher là où les tools
	// attendent un websearch.SearchEngine et un websearch.WebFetcher.
	var (
		engine  websearch.SearchEngine = s.Search
		fetcher websearch.WebFetcher   = s.Fetcher
	)
	ctx := context.Background()
	results, _ := engine.Search(ctx, "SaaS France recrutement commercial")
	for _, r := range results {
		fmt.Println(r.Domain, "—", r.Title)
	}
	page, err := fetcher.Fetch(ctx, results[0].URL)
	fmt.Println(page.StatusCode, page.Title, err)
	_, err = fetcher.Fetch(ctx, results[3].URL)
	fmt.Println(err)
	fmt.Println(s.Search.Calls(), s.Fetcher.Calls())
	// Output:
	// acme-saas.test — Carrières — Acme SaaS
	// nuvio.test — Carrières — Nuvio
	// sendara.test — Nous recrutons — Sendara
	// zenith-analytics.test — Carrières — Zenith Analytics
	// 200 Carrières — Acme SaaS <nil>
	// websearch: https://zenith-analytics.test/careers a répondu 404
	// [SaaS France recrutement commercial] [https://acme-saas.test/careers https://zenith-analytics.test/careers]
}
