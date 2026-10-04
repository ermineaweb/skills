// Package agendaskill assemble le skill "agenda" : ses métadonnées
// (SKILL.md), ses consignes (instructions.md), les schémas de ses tools
// (schemas/*.json) et leurs implémentations (tools/agenda).
//
// Le skill ne connaît ni le modèle d'IA ni l'agenda concret : l'agenda est
// injecté via agenda.Provider.
package agendaskill

import (
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"skills/services/agenda"
	agendatools "skills/tools/agenda"
	"skills/types"
)

// Name est l'identifiant du skill.
const Name = "agenda"

//go:embed SKILL.md instructions.md schemas/*.json
var files embed.FS

// Config du skill.
type Config struct {
	// Provider est l'agenda (source de vérité). Obligatoire.
	Provider agenda.Provider
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
	if cfg.Provider == nil {
		return nil, errors.New("agenda: Provider obligatoire")
	}
	meta, err := readFrontMatter("SKILL.md")
	if err != nil {
		return nil, err
	}
	if meta["name"] != Name {
		return nil, fmt.Errorf("agenda: SKILL.md déclare le nom %q", meta["name"])
	}
	if meta["description"] == "" {
		return nil, errors.New("agenda: description absente de SKILL.md")
	}
	instr, err := files.ReadFile("instructions.md")
	if err != nil {
		return nil, err
	}

	p := cfg.Provider
	bindings := []struct {
		file     string
		name     string
		handler  types.ToolHandler
		mutating bool
	}{
		{"list-events.json", agendatools.ToolListEvents, agendatools.NewListEvents(p), false},
		{"create-event.json", agendatools.ToolCreateEvent, agendatools.NewCreateEvent(p), true},
		{"update-event.json", agendatools.ToolUpdateEvent, agendatools.NewUpdateEvent(p), true},
		{"delete-event.json", agendatools.ToolDeleteEvent, agendatools.NewDeleteEvent(p), true},
	}
	s := &Skill{description: meta["description"], instructions: string(instr)}
	for _, b := range bindings {
		def, err := readDefinition("schemas/" + b.file)
		if err != nil {
			return nil, err
		}
		if def.Name != b.name {
			return nil, fmt.Errorf("agenda: %s déclare le tool %q, attendu %q", b.file, def.Name, b.name)
		}
		s.tools = append(s.tools, types.Tool{Definition: def, Handler: b.handler, Mutating: b.mutating})
	}
	return s, nil
}

func (s *Skill) Name() string         { return Name }
func (s *Skill) Description() string  { return s.description }
func (s *Skill) Instructions() string { return s.instructions }
func (s *Skill) Tools() []types.Tool  { return s.tools }

func readDefinition(path string) (types.ToolDefinition, error) {
	raw, err := files.ReadFile(path)
	if err != nil {
		return types.ToolDefinition{}, err
	}
	var def types.ToolDefinition
	if err := json.Unmarshal(raw, &def); err != nil {
		return types.ToolDefinition{}, fmt.Errorf("agenda: %s: %w", path, err)
	}
	if def.Name == "" || def.Description == "" || len(def.Parameters) == 0 {
		return types.ToolDefinition{}, fmt.Errorf("agenda: %s: name, description et parameters sont obligatoires", path)
	}
	return def, nil
}

// readFrontMatter lit l'en-tête "---\nclé: valeur\n---" d'un fichier Markdown.
func readFrontMatter(path string) (map[string]string, error) {
	raw, err := files.ReadFile(path)
	if err != nil {
		return nil, err
	}
	lines := strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return nil, fmt.Errorf("agenda: %s sans en-tête", path)
	}
	meta := map[string]string{}
	for _, line := range lines[1:] {
		if strings.TrimSpace(line) == "---" {
			return meta, nil
		}
		if k, v, ok := strings.Cut(line, ":"); ok {
			meta[strings.TrimSpace(k)] = strings.TrimSpace(v)
		}
	}
	return nil, fmt.Errorf("agenda: en-tête de %s non terminé", path)
}
