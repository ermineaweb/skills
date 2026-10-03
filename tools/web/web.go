// Package webtools implémente les tools de recherche web (web_search) et de
// lecture de pages (web_fetch) au-dessus de websearch.SearchEngine et
// websearch.WebFetcher.
//
// Ils tiennent un registre des URL renvoyées par un résultat de recherche
// ou lues dans la session : un autre tool peut ainsi refuser une URL que le
// modèle aurait inventée (voir Seen).
//
// Ce package ne connaît aucun modèle d'IA ni aucun moteur concret.
package webtools

import (
	"context"
	"encoding/json"
	"errors"
	"html"
	"log/slog"
	"net/url"
	"regexp"
	"strings"

	"skills/services/websearch"
	"skills/types"
)

// Noms des tools exposés au modèle.
const (
	ToolWebSearch = "web_search"
	ToolWebFetch  = "web_fetch"
)

const (
	// MaxResults limite le nombre de résultats renvoyés au modèle.
	MaxResults = 10
	// MaxPageChars limite le texte d'une page renvoyé au modèle : chaque
	// page lue reste dans l'historique et est renvoyée à chaque appel.
	MaxPageChars = 4000
)

// ---- Registre des URL vues (anti-hallucination) ----

const registryKey = "webtools.urls"

func registry(st types.StateStore) map[string]bool {
	if v, ok := st.Get(registryKey); ok {
		return v.(map[string]bool)
	}
	m := map[string]bool{}
	st.Set(registryKey, m)
	return m
}

func remember(st types.StateStore, rawURL string) {
	if k := urlKey(rawURL); k != "" {
		registry(st)[k] = true
	}
}

// Seen indique si rawURL a été renvoyée par web_search ou lue par web_fetch
// dans la session. Schéma, « www. », fragment et « / » final sont ignorés.
func Seen(st types.StateStore, rawURL string) bool {
	k := urlKey(rawURL)
	return k != "" && registry(st)[k]
}

func urlKey(rawURL string) string {
	d := websearch.Domain(rawURL)
	if d == "" {
		return ""
	}
	u, _ := url.Parse(rawURL)
	key := d + strings.TrimSuffix(u.EscapedPath(), "/")
	if u.RawQuery != "" {
		key += "?" + u.RawQuery
	}
	return key
}

// ---- web_search ----

type searchArgs struct {
	Query string `json:"query"`
}

// SearchOutput est le résultat de web_search.
type SearchOutput struct {
	Query   string                   `json:"query"`
	Results []websearch.SearchResult `json:"results"`
	Note    string                   `json:"note"`
}

// NewWebSearch renvoie le handler de web_search.
func NewWebSearch(engine websearch.SearchEngine) types.ToolHandler {
	return types.ToolHandlerFunc(func(ctx context.Context, tc types.ToolContext, raw json.RawMessage) (any, *types.Error) {
		var a searchArgs
		if err := json.Unmarshal(raw, &a); err != nil || strings.TrimSpace(a.Query) == "" {
			return nil, types.Errorf(types.ErrInvalidRequest, "query doit être une requête non vide.")
		}
		results, err := engine.Search(ctx, a.Query)
		if err != nil {
			slog.WarnContext(ctx, "web_search", "query", a.Query, "error", err)
			return nil, types.NewError(types.ErrSearchUnavailable)
		}
		out := SearchOutput{Query: a.Query, Results: []websearch.SearchResult{}}
		for _, r := range results {
			if r.Domain == "" || r.Title == "" {
				continue // résultat malformé : jamais transmis au modèle
			}
			if len(out.Results) == MaxResults {
				break
			}
			out.Results = append(out.Results, r)
			remember(tc.State, r.URL)
		}
		if len(out.Results) == 0 {
			out.Note = "Aucun résultat : essaie une autre formulation. N'invente aucune entreprise."
		} else {
			out.Note = "Les snippets ne sont pas vérifiés : lis la page avec web_fetch avant d'affirmer un fait."
		}
		return out, nil
	})
}

// ---- web_fetch ----

type fetchArgs struct {
	URL string `json:"url"`
}

// FetchOutput est le résultat de web_fetch.
type FetchOutput struct {
	URL          string `json:"url"`
	RequestedURL string `json:"requested_url,omitempty"`
	Title        string `json:"title"`
	Text         string `json:"text"`
	Truncated    bool   `json:"truncated"`
}

// NewWebFetch renvoie le handler de web_fetch.
func NewWebFetch(fetcher websearch.WebFetcher) types.ToolHandler {
	return types.ToolHandlerFunc(func(ctx context.Context, tc types.ToolContext, raw json.RawMessage) (any, *types.Error) {
		var a fetchArgs
		if err := json.Unmarshal(raw, &a); err != nil || websearch.Domain(a.URL) == "" {
			return nil, types.Errorf(types.ErrInvalidRequest, "url doit être une URL http(s) absolue, recopiée depuis un résultat de web_search ou une page lue.")
		}
		page, err := fetcher.Fetch(ctx, a.URL)
		var se *websearch.StatusError
		switch {
		case errors.As(err, &se):
			return nil, types.Errorf(types.ErrPageUnavailable, "La page a répondu %d. %s", se.StatusCode, types.DefaultHint(types.ErrPageUnavailable))
		case errors.Is(err, websearch.ErrInvalidURL):
			return nil, types.Errorf(types.ErrInvalidRequest, "URL invalide.")
		case err != nil:
			slog.WarnContext(ctx, "web_fetch", "url", a.URL, "error", err)
			return nil, types.NewError(types.ErrPageUnavailable)
		}
		remember(tc.State, a.URL)
		remember(tc.State, page.URL)
		text := pageText(page.Content)
		out := FetchOutput{URL: page.URL, Title: page.Title, Text: text}
		if page.URL != a.URL {
			out.RequestedURL = a.URL
		}
		if r := []rune(text); len(r) > MaxPageChars {
			out.Text, out.Truncated = string(r[:MaxPageChars]), true
		}
		return out, nil
	})
}

var (
	reHidden = regexp.MustCompile(`(?is)<(script|style|noscript|head)\b.*?</(script|style|noscript|head)>`)
	reBlock  = regexp.MustCompile(`(?i)</?(p|div|h[1-6]|li|ul|ol|tr|table|dt|dd|dl|br|header|footer|article|section|nav)\b[^>]*>`)
	reTag    = regexp.MustCompile(`<[^>]*>`)
	reSpaces = regexp.MustCompile(`[ \t\r\f\v]+`)
	reLines  = regexp.MustCompile(`\n\s*\n+`)
)

// pageText extrait le texte lisible d'une page HTML : sans balises, scripts
// ni styles, un paragraphe par ligne. Le texte brut est renvoyé tel quel.
func pageText(content string) string {
	if !strings.Contains(content, "<") {
		return strings.TrimSpace(content)
	}
	s := reHidden.ReplaceAllString(content, " ")
	s = reBlock.ReplaceAllString(s, "\n")
	s = reTag.ReplaceAllString(s, " ")
	s = html.UnescapeString(s)
	s = reSpaces.ReplaceAllString(s, " ")
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimSpace(l)
	}
	return strings.TrimSpace(reLines.ReplaceAllString(strings.Join(lines, "\n"), "\n"))
}
