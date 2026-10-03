package websearchtest

import (
	"context"
	"errors"
	"fmt"
	"html"
	"net/url"
	"strings"
	"sync"

	"skills/services/websearch"
)

// maxRedirects borne les redirections suivies par MockWebFetcher.
const maxRedirects = 5

// PageFixture décrit la réponse à une URL (fichier pages/index.json, ou
// champ "pages" d'un scénario).
//
// Une page est soit un contenu (File, avec Status 200 par défaut ou un code
// d'erreur), soit une redirection (RedirectTo), soit une panne réseau
// (Error). Une page d'erreur HTTP peut n'avoir aucun File.
type PageFixture struct {
	URL         string `json:"url"`
	File        string `json:"file,omitempty"`
	Status      int    `json:"status,omitempty"`
	ContentType string `json:"content_type,omitempty"`
	RedirectTo  string `json:"redirect_to,omitempty"`
	// Error simule une panne réseau : Fetch renvoie une erreur qui
	// enveloppe websearch.ErrUnreachable.
	Error string `json:"error,omitempty"`

	// Body est le contenu de File, chargé par LoadScenario.
	Body string `json:"-"`
}

// MockWebFetcher implémente websearch.WebFetcher à partir de fixtures. Il
// ne fait aucune requête HTTP. Sûr pour un usage concurrent.
//
// Correspondance des URL, volontairement tolérante comme un vrai site :
// schéma (http/https), casse de l'hôte, préfixe « www. », fragment (#…) et
// « / » final sont ignorés ; la requête (?…) compte. Une URL absente des
// fixtures répond 404 si son hôte existe dans les fixtures, sinon elle est
// injoignable (hôte inconnu).
type MockWebFetcher struct {
	pages map[string]PageFixture // clé : pageKey(URL)
	hosts map[string]bool

	mu    sync.Mutex
	calls []string
}

var _ websearch.WebFetcher = (*MockWebFetcher)(nil)

// NewMockWebFetcher valide les pages et construit le mock. Deux pages de
// même URL (après normalisation) sont refusées.
func NewMockWebFetcher(pages []PageFixture) (*MockWebFetcher, error) {
	m := &MockWebFetcher{pages: map[string]PageFixture{}, hosts: map[string]bool{}}
	for _, p := range pages {
		if err := p.validate(); err != nil {
			return nil, fmt.Errorf("page %q : %w", p.URL, err)
		}
		key, _ := pageKey(p.URL)
		if _, dup := m.pages[key]; dup {
			return nil, fmt.Errorf("page %q déclarée deux fois", p.URL)
		}
		if p.Status == 0 {
			p.Status = 200
		}
		if p.ContentType == "" {
			p.ContentType = "text/html; charset=utf-8"
		}
		m.pages[key] = p
		m.hosts[websearch.Domain(p.URL)] = true
	}
	return m, nil
}

// Fetch renvoie la page fixture de rawURL, en suivant les redirections.
func (m *MockWebFetcher) Fetch(ctx context.Context, rawURL string) (websearch.Page, error) {
	m.mu.Lock()
	m.calls = append(m.calls, rawURL)
	m.mu.Unlock()

	if err := ctx.Err(); err != nil {
		return websearch.Page{}, err
	}
	current := rawURL
	for hop := 0; ; hop++ {
		key, ok := pageKey(current)
		if !ok {
			return websearch.Page{}, fmt.Errorf("%w : %q", websearch.ErrInvalidURL, current)
		}
		p, found := m.pages[key]
		if !found {
			if !m.hosts[websearch.Domain(current)] {
				return websearch.Page{}, fmt.Errorf("%w : hôte inconnu (%s)", websearch.ErrUnreachable, current)
			}
			page := websearch.Page{RequestedURL: rawURL, URL: current, StatusCode: 404, ContentType: "text/html; charset=utf-8"}
			return page, &websearch.StatusError{URL: current, StatusCode: 404}
		}
		switch {
		case p.Error != "":
			return websearch.Page{}, fmt.Errorf("%w : %s (%s)", websearch.ErrUnreachable, p.Error, current)
		case p.RedirectTo != "":
			if hop >= maxRedirects {
				return websearch.Page{}, fmt.Errorf("%w : trop de redirections (%s)", websearch.ErrUnreachable, rawURL)
			}
			current = p.RedirectTo
			continue
		}
		page := websearch.Page{
			RequestedURL: rawURL,
			URL:          p.URL,
			StatusCode:   p.Status,
			ContentType:  p.ContentType,
			Title:        extractTitle(p.Body),
			Content:      p.Body,
		}
		if p.Status >= 400 {
			return page, &websearch.StatusError{URL: p.URL, StatusCode: p.Status}
		}
		return page, nil
	}
}

// Calls renvoie les URL demandées, dans l'ordre (y compris celles qui ont
// échoué).
func (m *MockWebFetcher) Calls() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.calls...)
}

// Reset efface l'historique des appels.
func (m *MockWebFetcher) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = nil
}

// Has indique si une page (contenu, erreur ou redirection) est déclarée
// pour rawURL.
func (m *MockWebFetcher) Has(rawURL string) bool {
	key, ok := pageKey(rawURL)
	if !ok {
		return false
	}
	_, found := m.pages[key]
	return found
}

func (p PageFixture) validate() error {
	if _, ok := pageKey(p.URL); !ok {
		return errors.New("URL invalide")
	}
	kinds := 0
	for _, set := range []bool{p.File != "", p.RedirectTo != "", p.Error != ""} {
		if set {
			kinds++
		}
	}
	if kinds > 1 {
		return errors.New("file, redirect_to et error sont exclusifs")
	}
	if p.Status != 0 && (p.Status < 200 || p.Status > 599) {
		return fmt.Errorf("status %d invalide", p.Status)
	}
	if kinds == 0 && p.Status < 400 {
		return errors.New("ni file, ni redirect_to, ni error, ni status d'erreur")
	}
	if p.RedirectTo != "" {
		if _, ok := pageKey(p.RedirectTo); !ok {
			return fmt.Errorf("redirect_to %q invalide", p.RedirectTo)
		}
	}
	return nil
}

// pageKey normalise une URL pour la recherche dans les fixtures (voir
// MockWebFetcher).
func pageKey(rawURL string) (string, bool) {
	u, err := url.Parse(rawURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
		return "", false
	}
	path := strings.TrimSuffix(u.EscapedPath(), "/")
	key := websearch.Domain(rawURL) + path
	if u.RawQuery != "" {
		key += "?" + u.RawQuery
	}
	return key, true
}

// extractTitle renvoie le texte de la première balise <title>.
func extractTitle(body string) string {
	// Minuscules ASCII seulement : les indices restent ceux de body.
	lower := strings.Map(func(r rune) rune {
		if 'A' <= r && r <= 'Z' {
			return r + 'a' - 'A'
		}
		return r
	}, body)
	start := strings.Index(lower, "<title>")
	if start < 0 {
		return ""
	}
	start += len("<title>")
	end := strings.Index(lower[start:], "</title>")
	if end < 0 {
		return ""
	}
	return strings.TrimSpace(html.UnescapeString(body[start : start+end]))
}
