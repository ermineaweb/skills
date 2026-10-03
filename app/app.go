// Package app assemble l'application à partir de la configuration : il
// enregistre dans le runtime les skills demandés, construit leurs
// dépendances (services métier) et les routes d'API qui leur sont propres.
//
// C'est le seul package qui connaît la liste des skills concrets : les
// points d'entrée (cmd/…) ne choisissent que le modèle et leur interface
// (HTTP, terminal). Ajouter un skill = ajouter une entrée dans available.
package app

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"skills/agent"
	"skills/api"
	"skills/model"
	"skills/types"
)

// Env donne à un skill accès à sa configuration.
type Env struct {
	// Getenv lit une variable de configuration propre au skill.
	Getenv func(string) string
	// Now est l'horloge de l'application.
	Now func() time.Time
}

// module est un skill prêt à enregistrer, avec ses éventuelles routes d'API.
type module struct {
	skill types.Skill
	api   []api.Option
}

type factory func(Env) (module, error)

// available associe chaque skill disponible à sa construction.
var available = map[string]factory{
	rendezVousName:       rendezVous,
	prospectResearchName: prospectResearch,
}

// Available renvoie les noms des skills disponibles, triés.
func Available() []string {
	names := make([]string, 0, len(available))
	for name := range available {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

// Config choisit les skills de l'application.
type Config struct {
	// Skills : skills à enregistrer. Vide = tous les skills disponibles.
	Skills []string
	// Activated : skills actifs dès l'ouverture d'une session, ce qui évite
	// un appel au modèle (activer_skill) par conversation. Ils doivent faire
	// partie de Skills.
	Activated []string
	// Getenv lit la configuration propre aux skills. Défaut : os.Getenv.
	Getenv func(string) string
	// Now est l'horloge. Défaut : time.Now.
	Now func() time.Time
	// MaxSteps borne le nombre d'appels au modèle par message (0 : valeur
	// par défaut du runtime). Une recherche de prospects en demande
	// davantage qu'une prise de rendez-vous.
	MaxSteps int
}

// ConfigFromEnv lit SKILLS et ACTIVE_SKILLS (listes de noms séparés par des
// virgules) et AGENT_MAX_STEPS. La configuration propre à chaque skill est
// lue à la construction, avec Getenv.
func ConfigFromEnv() (Config, error) {
	cfg := Config{
		Skills:    splitList(os.Getenv("SKILLS")),
		Activated: splitList(os.Getenv("ACTIVE_SKILLS")),
	}
	if raw := os.Getenv("AGENT_MAX_STEPS"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 {
			return Config{}, fmt.Errorf("AGENT_MAX_STEPS : valeur %q invalide (entier positif)", raw)
		}
		cfg.MaxSteps = n
	}
	return cfg, nil
}

// App est l'application assemblée.
type App struct {
	Runtime *agent.Runtime
	// Skills : skills enregistrés, dans l'ordre d'enregistrement.
	Skills []string
	// Activated : skills à activer à l'ouverture de chaque session.
	Activated []string
	// APIOptions : options à passer à api.New (skills activés et routes
	// propres aux skills).
	APIOptions []api.Option
}

// New construit le runtime sur le modèle m et y enregistre les skills de cfg.
func New(m model.Adapter, cfg Config, opts ...agent.Option) (*App, error) {
	env := Env{Getenv: cfg.Getenv, Now: cfg.Now}
	if env.Getenv == nil {
		env.Getenv = os.Getenv
	}
	if env.Now == nil {
		env.Now = time.Now
	}
	names := cfg.Skills
	if len(names) == 0 {
		names = Available()
	}
	for _, name := range cfg.Activated {
		if !slices.Contains(names, name) {
			return nil, fmt.Errorf("app: le skill actif %q ne fait pas partie des skills enregistrés (%s)", name, strings.Join(names, ", "))
		}
	}

	if cfg.MaxSteps > 0 {
		opts = append([]agent.Option{agent.WithMaxSteps(cfg.MaxSteps)}, opts...)
	}
	rt, err := agent.New(m, opts...)
	if err != nil {
		return nil, err
	}
	a := &App{Runtime: rt, Activated: cfg.Activated}
	for _, name := range names {
		build, ok := available[name]
		if !ok {
			return nil, fmt.Errorf("app: skill %q inconnu (disponibles : %s)", name, strings.Join(Available(), ", "))
		}
		if slices.Contains(a.Skills, name) {
			continue
		}
		mod, err := build(env)
		if err != nil {
			return nil, fmt.Errorf("app: %s: %w", name, err)
		}
		if err := rt.RegisterSkill(mod.skill); err != nil {
			return nil, err
		}
		a.Skills = append(a.Skills, name)
		a.APIOptions = append(a.APIOptions, mod.api...)
	}
	if len(a.Activated) > 0 {
		a.APIOptions = append(a.APIOptions, api.WithActivatedSkills(a.Activated...))
	}
	return a, nil
}

// NewSession ouvre une session dans laquelle les skills Activated sont actifs.
func (a *App) NewSession(id, timezone string, user types.UserContext) (*agent.Session, error) {
	s, err := agent.NewSession(id, timezone, user)
	if err != nil {
		return nil, err
	}
	for _, name := range a.Activated {
		s.ActivateSkill(name)
	}
	return s, nil
}

func splitList(raw string) []string {
	var out []string
	for _, v := range strings.Split(raw, ",") {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	return out
}

var errMissing = errors.New("variable obligatoire absente")
