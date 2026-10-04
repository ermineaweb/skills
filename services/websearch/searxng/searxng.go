// Package searxng implémente websearch.SearchEngine au-dessus de l'API JSON
// d'une instance SearXNG (métamoteur auto-hébergé, voir compose.yaml).
//
// L'instance doit autoriser le format JSON (search.formats dans
// settings.yml), sinon elle répond 403.
package searxng

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"skills/services/websearch"
)

// DefaultTimeout borne une recherche : SearXNG attend lui-même ses moteurs
// (quelques secondes), puis agrège.
const DefaultTimeout = 20 * time.Second

// Engine interroge une instance SearXNG. Sûr pour un usage concurrent.
type Engine struct {
	endpoint string
	client   *http.Client
}

var _ websearch.SearchEngine = (*Engine)(nil)

// New renvoie un moteur sur l'instance baseURL (ex : http://searxng:8080).
// client nil : client HTTP avec DefaultTimeout.
func New(baseURL string, client *http.Client) (*Engine, error) {
	u, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("searxng: URL %q invalide (attendu http(s)://hôte[:port])", baseURL)
	}
	if client == nil {
		client = &http.Client{Timeout: DefaultTimeout}
	}
	return &Engine{endpoint: strings.TrimSuffix(u.String(), "/") + "/search", client: client}, nil
}

type response struct {
	Results []struct {
		URL           string  `json:"url"`
		Title         string  `json:"title"`
		Content       string  `json:"content"`
		PublishedDate *string `json:"publishedDate"`
	} `json:"results"`
	// UnresponsiveEngines : [nom, raison] des moteurs en échec (délai,
	// blocage, captcha…).
	UnresponsiveEngines [][]string `json:"unresponsive_engines"`
}

// Search renvoie les résultats de la première page de SearXNG.
//
// Aucun résultat alors qu'au moins un moteur a échoué est une panne
// (ErrSearchFailed) : la recherche n'a pas vraiment eu lieu.
func (e *Engine) Search(ctx context.Context, query string) ([]websearch.SearchResult, error) {
	q := url.Values{"q": {query}, "format": {"json"}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, e.endpoint+"?"+q.Encode(), nil)
	if err != nil {
		return nil, fmt.Errorf("%w : %v", websearch.ErrSearchFailed, err)
	}
	req.Header.Set("Accept", "application/json")
	resp, err := e.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w : %v", websearch.ErrSearchFailed, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
		hint := ""
		if resp.StatusCode == http.StatusForbidden {
			hint = " (format json absent de search.formats ?)"
		}
		return nil, fmt.Errorf("%w : SearXNG a répondu %d%s", websearch.ErrSearchFailed, resp.StatusCode, hint)
	}
	var body response
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&body); err != nil {
		return nil, fmt.Errorf("%w : réponse illisible : %v", websearch.ErrSearchFailed, err)
	}

	out := make([]websearch.SearchResult, 0, len(body.Results))
	seen := map[string]bool{}
	for _, r := range body.Results {
		if seen[r.URL] {
			continue
		}
		seen[r.URL] = true
		res := websearch.SearchResult{
			Title:   strings.TrimSpace(r.Title),
			URL:     r.URL,
			Snippet: strings.TrimSpace(r.Content),
			Domain:  websearch.Domain(r.URL),
		}
		if r.PublishedDate != nil {
			res.PublishedAt = day(*r.PublishedDate)
		}
		out = append(out, res)
	}
	if len(out) == 0 && len(body.UnresponsiveEngines) > 0 {
		return nil, fmt.Errorf("%w : moteurs sans réponse %v", websearch.ErrSearchFailed, body.UnresponsiveEngines)
	}
	return out, nil
}

var reDay = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}`)

// day extrait AAAA-MM-JJ d'une date ISO 8601 ("2026-09-12T00:00:00"), ""
// sinon.
func day(s string) string {
	return reDay.FindString(s)
}
