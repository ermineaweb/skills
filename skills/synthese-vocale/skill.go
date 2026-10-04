// Package synthesevocale assemble le skill "synthese-vocale" : ses
// métadonnées (SKILL.md), ses consignes (instructions.md, complétées par la
// configuration du service), le schéma de son tool (schemas/) et son
// implémentation (tools/tts).
//
// Le skill ne connaît ni le modèle d'IA ni le moteur de synthèse : il est
// construit sur un tts.Service, lui-même construit sur un tts.Engine.
package synthesevocale

import (
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"skills/services/tts"
	ttstools "skills/tools/tts"
	"skills/types"
)

// Name est l'identifiant du skill.
const Name = "synthese-vocale"

//go:embed SKILL.md instructions.md schemas/*.json
var files embed.FS

// Config du skill.
type Config struct {
	// Service de synthèse. Obligatoire.
	Service *tts.Service
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
	if cfg.Service == nil {
		return nil, errors.New("synthese-vocale: Service obligatoire")
	}
	meta, err := readFrontMatter("SKILL.md")
	if err != nil {
		return nil, err
	}
	if meta["name"] != Name {
		return nil, fmt.Errorf("synthese-vocale: SKILL.md déclare le nom %q", meta["name"])
	}
	if meta["description"] == "" {
		return nil, errors.New("synthese-vocale: description absente de SKILL.md")
	}
	instr, err := files.ReadFile("instructions.md")
	if err != nil {
		return nil, err
	}
	def, err := readDefinition("schemas/lire-a-voix-haute.json")
	if err != nil {
		return nil, err
	}
	if def.Name != ttstools.ToolLireAVoixHaute {
		return nil, fmt.Errorf("synthese-vocale: le schéma déclare le tool %q, attendu %q", def.Name, ttstools.ToolLireAVoixHaute)
	}
	return &Skill{
		description:  meta["description"],
		instructions: string(instr) + "\n" + catalog(cfg.Service.Catalog()),
		tools: []types.Tool{{
			Definition: def,
			Handler:    ttstools.NewLireAVoixHaute(cfg.Service),
			// Effet transmis à l'interface : l'audio_id à lire.
			Mutating: true,
		}},
	}, nil
}

func (s *Skill) Name() string         { return Name }
func (s *Skill) Description() string  { return s.description }
func (s *Skill) Instructions() string { return s.instructions }
func (s *Skill) Tools() []types.Tool  { return s.tools }

// catalog décrit au modèle les valeurs acceptées par le service.
func catalog(c tts.Catalog) string {
	var b strings.Builder
	fmt.Fprintf(&b, "- Langue par défaut : %s.\n", c.DefaultLanguage)
	langs := make([]string, 0, len(c.Voices))
	for l := range c.Voices {
		langs = append(langs, l)
	}
	slices.Sort(langs)
	for _, l := range langs {
		fmt.Fprintf(&b, "- Voix en %s : %s (par défaut : %s).\n", l, strings.Join(c.Voices[l], ", "), c.Voices[l][0])
	}
	formats := make([]string, len(c.Formats))
	for i, f := range c.Formats {
		formats[i] = string(f)
	}
	fmt.Fprintf(&b, "- Formats : %s (par défaut : %s).\n", strings.Join(formats, ", "), c.DefaultFormat)
	fmt.Fprintf(&b, "- Vitesse : de %v à %v.\n", c.MinSpeed, c.MaxSpeed)
	fmt.Fprintf(&b, "- Longueur maximale d'un texte : %d caractères.\n", c.MaxTextLength)
	return b.String()
}

func readDefinition(path string) (types.ToolDefinition, error) {
	raw, err := files.ReadFile(path)
	if err != nil {
		return types.ToolDefinition{}, err
	}
	var def types.ToolDefinition
	if err := json.Unmarshal(raw, &def); err != nil {
		return types.ToolDefinition{}, fmt.Errorf("synthese-vocale: %s: %w", path, err)
	}
	if def.Name == "" || def.Description == "" || len(def.Parameters) == 0 {
		return types.ToolDefinition{}, fmt.Errorf("synthese-vocale: %s: name, description et parameters sont obligatoires", path)
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
		return nil, fmt.Errorf("synthese-vocale: %s sans en-tête", path)
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
	return nil, fmt.Errorf("synthese-vocale: en-tête de %s non terminé", path)
}
