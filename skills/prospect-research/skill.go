// Package prospectresearch assemble le skill "prospect-research" : ses
// instructions (SKILL.md, complété par hote.md pour cette application), les
// schémas de ses tools (schemas/*.json, et references/output-schema.json
// pour le résultat) et leurs implémentations (tools/web, tools/prospects).
//
// Le skill ne connaît ni le modèle d'IA ni le moteur de recherche concret :
// ils sont injectés via websearch.SearchEngine et websearch.WebFetcher.
package prospectresearch

import (
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"skills/services/websearch"
	prospecttools "skills/tools/prospects"
	webtools "skills/tools/web"
	"skills/types"
)

// Name est l'identifiant du skill.
const Name = "prospect-research"

//go:embed SKILL.md hote.md schemas/*.json references/output-schema.json
var files embed.FS

// Config du skill.
type Config struct {
	// Search et Fetcher donnent accès au web. Obligatoires.
	Search  websearch.SearchEngine
	Fetcher websearch.WebFetcher
}

// Skill implémente types.Skill.
type Skill struct {
	description  string
	instructions string
	tools        []types.Tool
}

var _ types.Skill = (*Skill)(nil)

// New construit le skill.
func New(cfg Config) (*Skill, error) {
	if cfg.Search == nil || cfg.Fetcher == nil {
		return nil, errors.New("prospect-research: Search et Fetcher obligatoires")
	}
	meta, body, err := readSkillFile("SKILL.md")
	if err != nil {
		return nil, err
	}
	if meta["name"] != Name {
		return nil, fmt.Errorf("prospect-research: SKILL.md déclare le nom %q", meta["name"])
	}
	if meta["description"] == "" {
		return nil, errors.New("prospect-research: description absente de SKILL.md")
	}
	host, err := files.ReadFile("hote.md")
	if err != nil {
		return nil, err
	}
	s := &Skill{description: meta["description"], instructions: body + string(host)}

	bindings := []struct {
		file     string
		name     string
		handler  types.ToolHandler
		mutating bool
	}{
		{"web-search.json", webtools.ToolWebSearch, webtools.NewWebSearch(cfg.Search), false},
		{"web-fetch.json", webtools.ToolWebFetch, webtools.NewWebFetch(cfg.Fetcher), false},
		{"enregistrer-prospects.json", prospecttools.ToolEnregistrerProspects, prospecttools.NewEnregistrerProspects(), true},
	}
	for _, b := range bindings {
		def, err := readDefinition("schemas/" + b.file)
		if err != nil {
			return nil, err
		}
		if def.Name != b.name {
			return nil, fmt.Errorf("prospect-research: %s déclare le tool %q, attendu %q", b.file, def.Name, b.name)
		}
		if b.name == prospecttools.ToolEnregistrerProspects {
			if def.Parameters, err = withOutputSchema(def.Parameters); err != nil {
				return nil, err
			}
		}
		s.tools = append(s.tools, types.Tool{Definition: def, Handler: b.handler, Mutating: b.mutating})
	}
	return s, nil
}

func (s *Skill) Name() string         { return Name }
func (s *Skill) Description() string  { return s.description }
func (s *Skill) Instructions() string { return s.instructions }
func (s *Skill) Tools() []types.Tool  { return s.tools }

// withOutputSchema place le schéma de sortie du skill dans le paramètre
// "resultat" : le runtime valide ainsi le résultat avant d'appeler le tool.
func withOutputSchema(params json.RawMessage) (json.RawMessage, error) {
	raw, err := files.ReadFile("references/output-schema.json")
	if err != nil {
		return nil, err
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("prospect-research: output-schema.json: %w", err)
	}
	delete(out, "$schema")
	delete(out, "title")
	var p struct {
		Type                 string                    `json:"type"`
		AdditionalProperties bool                      `json:"additionalProperties"`
		Required             []string                  `json:"required"`
		Properties           map[string]map[string]any `json:"properties"`
	}
	if err := json.Unmarshal(params, &p); err != nil || p.Properties["resultat"] == nil {
		return nil, errors.New("prospect-research: enregistrer-prospects.json doit déclarer la propriété resultat")
	}
	out["description"] = p.Properties["resultat"]["description"]
	p.Properties["resultat"] = out
	return json.Marshal(p)
}

func readDefinition(path string) (types.ToolDefinition, error) {
	raw, err := files.ReadFile(path)
	if err != nil {
		return types.ToolDefinition{}, err
	}
	var def types.ToolDefinition
	if err := json.Unmarshal(raw, &def); err != nil {
		return types.ToolDefinition{}, fmt.Errorf("prospect-research: %s: %w", path, err)
	}
	if def.Name == "" || def.Description == "" || len(def.Parameters) == 0 {
		return types.ToolDefinition{}, fmt.Errorf("prospect-research: %s: name, description et parameters sont obligatoires", path)
	}
	return def, nil
}

// readSkillFile lit l'en-tête "---\nclé: valeur\n---" d'un fichier Markdown
// et renvoie le corps qui le suit.
func readSkillFile(path string) (map[string]string, string, error) {
	raw, err := files.ReadFile(path)
	if err != nil {
		return nil, "", err
	}
	lines := strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return nil, "", fmt.Errorf("prospect-research: %s sans en-tête", path)
	}
	meta := map[string]string{}
	for i, line := range lines[1:] {
		if strings.TrimSpace(line) == "---" {
			return meta, strings.TrimSpace(strings.Join(lines[i+2:], "\n")) + "\n", nil
		}
		if k, v, ok := strings.Cut(line, ":"); ok {
			meta[strings.TrimSpace(k)] = strings.TrimSpace(v)
		}
	}
	return nil, "", fmt.Errorf("prospect-research: en-tête de %s non terminé", path)
}
