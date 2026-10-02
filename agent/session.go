package agent

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"skills/model"
	"skills/types"
)

// Session est l'état d'une conversation : historique, skills actifs, état
// des tools, identité de l'utilisateur et fuseau horaire.
//
// Le fuseau est obligatoire : il n'est jamais supposé silencieusement.
type Session struct {
	ID       string
	User     types.UserContext
	Location *time.Location

	mu       sync.Mutex
	messages []model.Message
	active   []string
	state    *types.MemoryState
}

// ErrTimezoneRequired est renvoyée si aucun fuseau n'est fourni.
var ErrTimezoneRequired = errors.New("agent: fuseau horaire obligatoire (ex: Europe/Paris)")

// NewSession crée une session. timezone est un nom IANA (ex: "Europe/Paris").
func NewSession(id, timezone string, user types.UserContext) (*Session, error) {
	if strings.TrimSpace(timezone) == "" {
		return nil, ErrTimezoneRequired
	}
	loc, err := time.LoadLocation(timezone)
	if err != nil {
		return nil, fmt.Errorf("agent: fuseau horaire invalide %q: %w", timezone, err)
	}
	return &Session{ID: id, User: user, Location: loc, state: types.NewMemoryState()}, nil
}

// ActivateSkill active un skill sans passer par le modèle (ex: widget dédié).
func (s *Session) ActivateSkill(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.activate(name)
}

func (s *Session) activate(name string) {
	if !slices.Contains(s.active, name) {
		s.active = append(s.active, name)
	}
}

// ActiveSkills renvoie les skills actifs, dans l'ordre d'activation.
func (s *Session) ActiveSkills() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.active...)
}

// Messages renvoie une copie de l'historique.
func (s *Session) Messages() []model.Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]model.Message(nil), s.messages...)
}

// State renvoie l'état clé/valeur partagé par les tools de la session.
func (s *Session) State() types.StateStore { return s.state }
