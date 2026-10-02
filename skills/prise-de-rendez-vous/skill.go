// Package priserdv assemble le skill "prise-de-rendez-vous" : ses
// instructions (instructions.md), ses métadonnées (SKILL.md), les schémas de
// ses tools (schemas/*.json) et leurs implémentations (tools/…).
//
// Le skill ne connaît ni le modèle d'IA ni l'agenda concret : le calendrier
// est injecté via calendar.Provider.
package priserdv

import (
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"skills/services/calendar"
	calendartools "skills/tools/calendar"
	datetimetools "skills/tools/datetime"
	"skills/types"
)

// Name est l'identifiant du skill.
const Name = "prise-de-rendez-vous"

//go:embed SKILL.md instructions.md schemas/*.json
var files embed.FS

// Config du skill.
type Config struct {
	// Provider est le calendrier (source de vérité). Obligatoire.
	Provider calendar.Provider
	// AutoBooking : politique produit autorisant la réservation sans choix
	// explicite quand la recherche renvoie un seul créneau correspondant
	// exactement à la demande. Désactivée par défaut.
	AutoBooking bool
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
		return nil, errors.New("prise-de-rendez-vous: Provider obligatoire")
	}

	meta, err := readFrontMatter("SKILL.md")
	if err != nil {
		return nil, err
	}
	if meta["name"] != Name {
		return nil, fmt.Errorf("prise-de-rendez-vous: SKILL.md déclare le nom %q", meta["name"])
	}
	if meta["description"] == "" {
		return nil, errors.New("prise-de-rendez-vous: description absente de SKILL.md")
	}

	instr, err := files.ReadFile("instructions.md")
	if err != nil {
		return nil, err
	}
	policy := "Attends toujours le choix explicite de l'utilisateur avant de réserver, même si un seul créneau correspond."
	if cfg.AutoBooking {
		policy = "Politique produit : si la recherche renvoie un seul créneau et qu'il correspond exactement à la demande (jour, moment, professionnel), tu peux le réserver directement. Sinon, attends le choix explicite de l'utilisateur."
	}

	p := cfg.Provider
	bindings := []struct {
		file     string
		name     string
		handler  types.ToolHandler
		mutating bool
	}{
		{"interpreter-date.json", datetimetools.ToolInterpreterDate, datetimetools.NewInterpreterDate(), false},
		{"lister-professionnels.json", calendartools.ToolListerProfessionnels, calendartools.NewListerProfessionnels(p), false},
		{"rechercher-disponibilites.json", calendartools.ToolRechercherDisponibilites, calendartools.NewRechercherDisponibilites(p), false},
		{"reserver-creneau.json", calendartools.ToolReserverCreneau, calendartools.NewReserverCreneau(p), true},
		{"lister-rendez-vous.json", calendartools.ToolListerRendezVous, calendartools.NewListerRendezVous(p), false},
		{"modifier-rendez-vous.json", calendartools.ToolModifierRendezVous, calendartools.NewModifierRendezVous(p), true},
		{"annuler-rendez-vous.json", calendartools.ToolAnnulerRendezVous, calendartools.NewAnnulerRendezVous(p), true},
	}

	s := &Skill{
		description:  meta["description"],
		instructions: strings.ReplaceAll(string(instr), "{{POLITIQUE_RESERVATION}}", policy),
	}
	for _, b := range bindings {
		def, err := readDefinition("schemas/" + b.file)
		if err != nil {
			return nil, err
		}
		if def.Name != b.name {
			return nil, fmt.Errorf("prise-de-rendez-vous: %s déclare le tool %q, attendu %q", b.file, def.Name, b.name)
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
		return types.ToolDefinition{}, fmt.Errorf("prise-de-rendez-vous: %s: %w", path, err)
	}
	if def.Name == "" || def.Description == "" || len(def.Parameters) == 0 {
		return types.ToolDefinition{}, fmt.Errorf("prise-de-rendez-vous: %s: name, description et parameters sont obligatoires", path)
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
		return nil, fmt.Errorf("prise-de-rendez-vous: %s sans en-tête", path)
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
	return nil, fmt.Errorf("prise-de-rendez-vous: en-tête de %s non terminé", path)
}
