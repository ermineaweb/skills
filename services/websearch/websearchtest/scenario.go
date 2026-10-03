// Package websearchtest fournit un moteur de recherche et un lecteur de
// pages déterministes (MockSearchEngine, MockWebFetcher) qui répondent à
// partir de fixtures locales, pour tester le skill prospect-research sans
// Internet.
//
// Les fixtures (testdata/prospect-research, embarquées : voir Fixtures)
// décrivent un univers d'entreprises fictives (domaines en .test) :
//
//	search/*.json      pages de résultats, sélectionnées par mots-clés (voir SearchEntry)
//	pages/index.json   URL → fichier HTML, redirection ou panne (voir PageFixture)
//	pages/*.html       contenu des pages
//	scenarios/*.json   scénarios : fichiers de recherche utilisés, surcharges
//	                   (pannes, résultats vides…) et attendus pour le skill
//
// Un scénario active les pannes par ses fixtures, jamais par du code de
// test. Les mocks ne simulent que la recherche et la lecture de pages : le
// modèle, le skill et ses tools restent réels.
package websearchtest

import (
	"bytes"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"slices"
	"strings"
)

//go:embed testdata/prospect-research
var embedded embed.FS

// Fixtures renvoie les fixtures embarquées du skill prospect-research
// (racine contenant search/, pages/ et scenarios/).
func Fixtures() fs.FS {
	sub, err := fs.Sub(embedded, "testdata/prospect-research")
	if err != nil {
		panic(err) // chemin constant : impossible
	}
	return sub
}

// Scenario est un scénario chargé, avec des mocks neufs (historiques
// d'appels vides, propres à ce chargement).
type Scenario struct {
	Name        string
	Description string
	// ICP est la demande à envoyer à l'agent pour ce scénario.
	ICP     string
	Expect  Expectations
	Search  *MockSearchEngine
	Fetcher *MockWebFetcher
}

// Expectations décrit ce que le skill doit produire sur ce scénario. Le mock
// ne s'en sert pas : elles servent aux tests du skill.
type Expectations struct {
	// Prospects : domaines qui doivent figurer dans prospects (match ou probable).
	Prospects []string `json:"prospects"`
	// Excluded : domaines qui ne doivent jamais figurer dans prospects.
	Excluded []string `json:"excluded"`
	// Checks : vérifications qualitatives (doublons, incertitude, signaux…).
	Checks []string `json:"checks"`
}

type scenarioFile struct {
	Name        string        `json:"name"`
	Description string        `json:"description"`
	ICP         string        `json:"icp"`
	SearchFiles []string      `json:"search_files"`
	Search      []SearchEntry `json:"search"`
	Pages       []PageFixture `json:"pages"`
	Expect      Expectations  `json:"expect"`
}

// ScenarioNames renvoie les noms des scénarios de fsys, triés.
func ScenarioNames(fsys fs.FS) ([]string, error) {
	files, err := fs.Glob(fsys, "scenarios/*.json")
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(files))
	for _, f := range files {
		names = append(names, strings.TrimSuffix(path.Base(f), ".json"))
	}
	slices.Sort(names)
	return names, nil
}

// LoadScenario charge scenarios/<name>.json depuis fsys (voir Fixtures).
//
// Recherche : les entrées du champ "search" du scénario, puis celles des
// search_files dans l'ordre donné. Pages : pages/index.json, dont les
// entrées de même URL sont remplacées par celles du champ "pages" du
// scénario. Toute incohérence (champ inconnu, fichier absent, URL
// invalide, attendu sur un domaine inexistant) est une erreur.
func LoadScenario(fsys fs.FS, name string) (*Scenario, error) {
	var sc scenarioFile
	if err := decodeFile(fsys, "scenarios/"+name+".json", &sc); err != nil {
		return nil, err
	}
	if sc.Name != name {
		return nil, fmt.Errorf("scénario %s : name vaut %q", name, sc.Name)
	}
	if sc.ICP == "" {
		return nil, fmt.Errorf("scénario %s : icp vide", name)
	}

	entries := slices.Clone(sc.Search)
	for _, f := range sc.SearchFiles {
		var file struct {
			Entries []SearchEntry `json:"entries"`
		}
		if err := decodeFile(fsys, "search/"+f, &file); err != nil {
			return nil, fmt.Errorf("scénario %s : %w", name, err)
		}
		entries = append(entries, file.Entries...)
	}
	search, err := NewMockSearchEngine(entries)
	if err != nil {
		return nil, fmt.Errorf("scénario %s : %w", name, err)
	}

	var index struct {
		Pages []PageFixture `json:"pages"`
	}
	if err := decodeFile(fsys, "pages/index.json", &index); err != nil {
		return nil, err
	}
	pages, err := mergePages(index.Pages, sc.Pages)
	if err != nil {
		return nil, fmt.Errorf("scénario %s : %w", name, err)
	}
	for i := range pages {
		if pages[i].File == "" {
			continue
		}
		body, err := fs.ReadFile(fsys, "pages/"+pages[i].File)
		if err != nil {
			return nil, fmt.Errorf("scénario %s : page %s : %w", name, pages[i].URL, err)
		}
		pages[i].Body = string(body)
	}
	fetcher, err := NewMockWebFetcher(pages)
	if err != nil {
		return nil, fmt.Errorf("scénario %s : %w", name, err)
	}

	if err := checkExpectations(sc.Expect, fetcher); err != nil {
		return nil, fmt.Errorf("scénario %s : %w", name, err)
	}
	return &Scenario{
		Name:        sc.Name,
		Description: sc.Description,
		ICP:         sc.ICP,
		Expect:      sc.Expect,
		Search:      search,
		Fetcher:     fetcher,
	}, nil
}

// mergePages remplace les pages de base par les surcharges de même URL et
// ajoute les autres, en conservant l'ordre.
func mergePages(base, overrides []PageFixture) ([]PageFixture, error) {
	out := slices.Clone(base)
	pos := map[string]int{}
	for i, p := range out {
		if key, ok := pageKey(p.URL); ok {
			pos[key] = i
		}
	}
	for _, o := range overrides {
		key, ok := pageKey(o.URL)
		if !ok {
			return nil, fmt.Errorf("surcharge de page : URL %q invalide", o.URL)
		}
		if i, found := pos[key]; found {
			out[i] = o
		} else {
			pos[key] = len(out)
			out = append(out, o)
		}
	}
	return out, nil
}

func checkExpectations(e Expectations, f *MockWebFetcher) error {
	for _, d := range append(slices.Clone(e.Prospects), e.Excluded...) {
		if !f.hosts[d] {
			return fmt.Errorf("attendu sur le domaine %q, absent des pages", d)
		}
	}
	for _, d := range e.Prospects {
		if slices.Contains(e.Excluded, d) {
			return fmt.Errorf("domaine %q à la fois attendu et exclu", d)
		}
	}
	return nil
}

// decodeFile lit un fichier JSON en refusant les champs inconnus (une faute
// de frappe dans une fixture doit échouer, pas être ignorée).
func decodeFile(fsys fs.FS, name string, dst any) error {
	raw, err := fs.ReadFile(fsys, name)
	if err != nil {
		return err
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return fmt.Errorf("%s : %w", name, err)
	}
	if dec.More() {
		return fmt.Errorf("%s : %w", name, errors.New("contenu après l'objet JSON"))
	}
	return nil
}
