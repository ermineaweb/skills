package websearchtest

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"unicode"

	"skills/services/websearch"
)

// SearchEntry associe des mots-clés à une page de résultats (fichiers
// search/*.json, ou champ "search" d'un scénario).
//
// Sélection (déterministe) : une entrée correspond à une requête si chacun
// de ses mots-clés apparaît parmi les mots de la requête. Un mot-clé peut
// proposer des variantes séparées par « | » (« recrutement|hiring ») : une
// seule suffit. Mots-clés et requête sont comparés en minuscules, sans
// accents, mot par mot (pas de sous-chaîne : « saas » ne correspond pas à
// « saasify »). Parmi les entrées qui correspondent, la retenue est celle de
// plus forte Priority, puis celle qui a le plus de mots-clés (la plus
// spécifique), puis la première dans l'ordre de chargement. Aucune entrée
// ne correspond : la recherche renvoie une liste vide, sans erreur.
type SearchEntry struct {
	Keywords []string        `json:"keywords"`
	Priority int             `json:"priority,omitempty"`
	Results  []ResultFixture `json:"results"`
	// Error simule une panne du moteur : Search renvoie une erreur qui
	// enveloppe websearch.ErrSearchFailed. Exclusif avec Results.
	Error string `json:"error,omitempty"`

	alternatives [][]string // Keywords normalisés, découpés sur « | »
}

// ResultFixture est un résultat de recherche tel qu'écrit dans les fixtures.
type ResultFixture struct {
	Title       string `json:"title"`
	URL         string `json:"url"`
	Snippet     string `json:"snippet"`
	PublishedAt string `json:"published_at,omitempty"`
	// Malformed déclare un résultat volontairement invalide (URL ou titre
	// absents) : il est renvoyé tel quel au lieu d'être refusé au chargement.
	Malformed bool `json:"malformed,omitempty"`
}

// MockSearchEngine implémente websearch.SearchEngine à partir de fixtures.
// Il ne fait aucun appel réseau. Sûr pour un usage concurrent.
type MockSearchEngine struct {
	entries []SearchEntry

	mu    sync.Mutex
	calls []string
}

var _ websearch.SearchEngine = (*MockSearchEngine)(nil)

// NewMockSearchEngine valide les entrées et construit le mock. L'ordre des
// entrées sert à départager les égalités (voir SearchEntry).
func NewMockSearchEngine(entries []SearchEntry) (*MockSearchEngine, error) {
	m := &MockSearchEngine{entries: make([]SearchEntry, len(entries))}
	for i, e := range entries {
		if err := e.prepare(); err != nil {
			return nil, fmt.Errorf("entrée de recherche %d %v : %w", i, e.Keywords, err)
		}
		m.entries[i] = e
	}
	return m, nil
}

// Search renvoie les résultats de l'entrée retenue pour query.
func (m *MockSearchEngine) Search(ctx context.Context, query string) ([]websearch.SearchResult, error) {
	m.mu.Lock()
	m.calls = append(m.calls, query)
	m.mu.Unlock()

	if err := ctx.Err(); err != nil {
		return nil, err
	}
	words := tokenize(query)
	if len(words) == 0 {
		return nil, fmt.Errorf("%w : requête vide", websearch.ErrSearchFailed)
	}
	best := -1
	for i := range m.entries {
		if m.entries[i].matches(words) && (best < 0 || m.entries[i].beats(&m.entries[best])) {
			best = i
		}
	}
	if best < 0 {
		return []websearch.SearchResult{}, nil
	}
	e := m.entries[best]
	if e.Error != "" {
		return nil, fmt.Errorf("%w : %s", websearch.ErrSearchFailed, e.Error)
	}
	out := make([]websearch.SearchResult, len(e.Results))
	for i, r := range e.Results {
		out[i] = websearch.SearchResult{
			Title:       r.Title,
			URL:         r.URL,
			Snippet:     r.Snippet,
			Domain:      websearch.Domain(r.URL),
			PublishedAt: r.PublishedAt,
		}
	}
	return out, nil
}

// Calls renvoie les requêtes reçues, dans l'ordre (y compris celles qui ont
// échoué).
func (m *MockSearchEngine) Calls() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.calls...)
}

// Reset efface l'historique des appels.
func (m *MockSearchEngine) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = nil
}

func (e *SearchEntry) prepare() error {
	if len(e.Keywords) == 0 {
		return errors.New("keywords vide")
	}
	switch {
	case e.Error != "" && len(e.Results) > 0:
		return errors.New("error et results sont exclusifs")
	case e.Error == "" && e.Results == nil:
		return errors.New(`results absent (utiliser "results": [] pour simuler une recherche sans résultat)`)
	}
	e.alternatives = nil
	for _, kw := range e.Keywords {
		var alts []string
		for _, alt := range strings.Split(kw, "|") {
			words := tokenize(alt)
			if len(words) != 1 {
				return fmt.Errorf("mot-clé %q : chaque variante doit être un seul mot", kw)
			}
			alts = append(alts, words[0])
		}
		e.alternatives = append(e.alternatives, alts)
	}
	for i, r := range e.Results {
		if r.Malformed {
			continue
		}
		if r.Title == "" {
			return fmt.Errorf("résultat %d : title vide (marquer \"malformed\": true si c'est voulu)", i)
		}
		if u, err := url.Parse(r.URL); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return fmt.Errorf("résultat %d : URL %q invalide (marquer \"malformed\": true si c'est voulu)", i, r.URL)
		}
	}
	return nil
}

func (e *SearchEntry) matches(words []string) bool {
	for _, alts := range e.alternatives {
		found := false
		for _, a := range alts {
			for _, w := range words {
				if w == a {
					found = true
					break
				}
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// beats : e est préférée à o (o la précède dans l'ordre de chargement).
func (e *SearchEntry) beats(o *SearchEntry) bool {
	if e.Priority != o.Priority {
		return e.Priority > o.Priority
	}
	return len(e.alternatives) > len(o.alternatives)
}

var accents = strings.NewReplacer(
	"à", "a", "â", "a", "ä", "a", "á", "a", "ã", "a",
	"é", "e", "è", "e", "ê", "e", "ë", "e",
	"î", "i", "ï", "i", "í", "i",
	"ô", "o", "ö", "o", "ó", "o",
	"ù", "u", "û", "u", "ü", "u", "ú", "u",
	"ç", "c", "ñ", "n", "œ", "oe", "æ", "ae", "ß", "ss",
)

// tokenize découpe s en mots minuscules sans accents.
func tokenize(s string) []string {
	s = accents.Replace(strings.ToLower(s))
	return strings.FieldsFunc(s, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
}
