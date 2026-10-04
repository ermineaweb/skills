package agenda

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"skills/types"
)

// Operation identifie une opération du Provider (pour l'injection de pannes).
type Operation string

const (
	OpList   Operation = "list"
	OpCreate Operation = "create"
	OpUpdate Operation = "update"
	OpDelete Operation = "delete"
)

// MaxListRange limite l'amplitude d'une consultation.
const MaxListRange = 93 * 24 * time.Hour

// MockProvider est un agenda en mémoire, déterministe, avec injection de
// pannes. Chaque événement appartient à un propriétaire (OwnerID). Il ne
// déclenche aucun rappel : il les conserve, comme le ferait un fournisseur
// réel qui, lui, les notifie.
type MockProvider struct {
	mu     sync.Mutex
	events map[string]*Event
	seq    int

	unavailable bool
	failNext    map[Operation]types.ErrorCode
	unconfirmed map[Operation]bool
	calls       map[Operation]int
}

var _ Provider = (*MockProvider)(nil)

// NewMockProvider crée un agenda vide.
func NewMockProvider() *MockProvider {
	return &MockProvider{
		events:      map[string]*Event{},
		failNext:    map[Operation]types.ErrorCode{},
		unconfirmed: map[Operation]bool{},
		calls:       map[Operation]int{},
	}
}

// SetUnavailable simule une panne technique de l'agenda.
func (m *MockProvider) SetUnavailable(v bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.unavailable = v
}

// FailNext fait échouer la prochaine opération op avec le code donné.
func (m *MockProvider) FailNext(op Operation, code types.ErrorCode) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.failNext[op] = code
}

// UnconfirmedNext fait renvoyer à la prochaine écriture op un résultat sans
// confirmation (Created/Updated/Deleted = false), sans erreur.
func (m *MockProvider) UnconfirmedNext(op Operation) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.unconfirmed[op] = true
}

// Calls renvoie le nombre d'appels à op.
func (m *MockProvider) Calls(op Operation) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.calls[op]
}

// Event renvoie une copie de l'événement id, quel que soit son propriétaire
// (inspection en test).
func (m *MockProvider) Event(id string) (Event, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.events[id]
	if !ok {
		return Event{}, false
	}
	return clone(*e), true
}

func (m *MockProvider) begin(ctx context.Context, op Operation, owner string) error {
	m.calls[op]++
	if err := ctx.Err(); err != nil {
		return err
	}
	if m.unavailable {
		return errors.New("agenda: service indisponible (simulation)")
	}
	if code, ok := m.failNext[op]; ok {
		delete(m.failNext, op)
		return types.NewError(code)
	}
	if owner == "" {
		return types.NewError(types.ErrNotAuthenticated)
	}
	return nil
}

func (m *MockProvider) takeUnconfirmed(op Operation) bool {
	if m.unconfirmed[op] {
		delete(m.unconfirmed, op)
		return true
	}
	return false
}

func (m *MockProvider) ListEvents(ctx context.Context, req ListRequest) (ListResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.begin(ctx, OpList, req.OwnerID); err != nil {
		return ListResult{}, err
	}
	if !req.To.After(req.From) || req.To.Sub(req.From) > MaxListRange {
		return ListResult{}, types.Errorf(types.ErrInvalidRequest, "Période invalide : la fin doit suivre le début, sur %d jours au plus.", int(MaxListRange.Hours()/24))
	}
	var out []Occurrence
	for _, e := range m.events {
		if e.OwnerID != req.OwnerID || !Matches(*e, req.Query) {
			continue
		}
		out = append(out, Occurrences(clone(*e), req.From, req.To, 0)...)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if !out[i].Start.Equal(out[j].Start) {
			return out[i].Start.Before(out[j].Start)
		}
		return out[i].Event.ID < out[j].Event.ID
	})
	return ListResult{Occurrences: out}, nil
}

func (m *MockProvider) CreateEvent(ctx context.Context, req CreateRequest) (CreateResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.begin(ctx, OpCreate, req.OwnerID); err != nil {
		return CreateResult{}, err
	}
	e := clone(req.Event)
	if err := validate(e); err != nil {
		return CreateResult{}, err
	}
	if m.takeUnconfirmed(OpCreate) {
		return CreateResult{Created: false}, nil
	}
	m.seq++
	e.ID, e.OwnerID = fmt.Sprintf("evt-%d", m.seq), req.OwnerID
	m.events[e.ID] = &e
	return CreateResult{Created: true, Event: clone(e)}, nil
}

func (m *MockProvider) UpdateEvent(ctx context.Context, req UpdateRequest) (UpdateResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.begin(ctx, OpUpdate, req.OwnerID); err != nil {
		return UpdateResult{}, err
	}
	e, err := m.owned(req.EventID, req.OwnerID)
	if err != nil {
		return UpdateResult{}, err
	}
	target := clone(*e)
	detach := req.Occurrence != nil && e.Recurrence != nil
	if detach {
		if !IsOccurrence(*e, *req.Occurrence) {
			return UpdateResult{}, types.NewError(types.ErrEventNotFound)
		}
		// L'occurrence devient un événement distinct.
		target.Recurrence, target.Exceptions = nil, nil
		target.Start, target.End = *req.Occurrence, req.Occurrence.Add(e.End.Sub(e.Start))
	}
	apply(&target, req.Changes)
	if err := validate(target); err != nil {
		return UpdateResult{}, err
	}
	if m.takeUnconfirmed(OpUpdate) {
		return UpdateResult{Updated: false, Event: clone(*e)}, nil
	}
	if detach {
		e.Exceptions = append(e.Exceptions, *req.Occurrence)
		m.seq++
		target.ID = fmt.Sprintf("evt-%d", m.seq)
		m.events[target.ID] = &target
		return UpdateResult{Updated: true, Event: clone(target)}, nil
	}
	*e = target
	return UpdateResult{Updated: true, Event: clone(target)}, nil
}

func (m *MockProvider) DeleteEvent(ctx context.Context, req DeleteRequest) (DeleteResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.begin(ctx, OpDelete, req.OwnerID); err != nil {
		return DeleteResult{}, err
	}
	e, err := m.owned(req.EventID, req.OwnerID)
	if err != nil {
		return DeleteResult{}, err
	}
	if req.Occurrence != nil && e.Recurrence != nil && !IsOccurrence(*e, *req.Occurrence) {
		return DeleteResult{}, types.NewError(types.ErrEventNotFound)
	}
	if m.takeUnconfirmed(OpDelete) {
		return DeleteResult{Deleted: false, Event: clone(*e)}, nil
	}
	if req.Occurrence != nil && e.Recurrence != nil {
		e.Exceptions = append(e.Exceptions, *req.Occurrence)
		return DeleteResult{Deleted: true, Event: clone(*e)}, nil
	}
	delete(m.events, e.ID)
	return DeleteResult{Deleted: true, Event: clone(*e)}, nil
}

// owned ne révèle pas l'existence d'un événement appartenant à un autre
// utilisateur : il est alors introuvable.
func (m *MockProvider) owned(id, owner string) (*Event, error) {
	e, ok := m.events[id]
	if !ok || e.OwnerID != owner {
		return nil, types.NewError(types.ErrEventNotFound)
	}
	return e, nil
}

func validate(e Event) error {
	if strings.TrimSpace(e.Title) == "" {
		return &types.Error{Code: types.ErrMissingInformation, Message: types.DefaultHint(types.ErrMissingInformation), Missing: []string{"titre"}}
	}
	if e.Start.IsZero() || !e.End.After(e.Start) {
		return types.Errorf(types.ErrInvalidRequest, "La fin de l'événement doit suivre son début.")
	}
	if e.End.Sub(e.Start) > 14*24*time.Hour {
		return types.Errorf(types.ErrInvalidRequest, "Un événement dure au plus 14 jours ; utilise une récurrence pour un événement répété.")
	}
	if e.Recurrence != nil {
		return e.Recurrence.Validate(e.Start)
	}
	return nil
}

func apply(e *Event, c Changes) {
	if c.Title != nil {
		e.Title = *c.Title
	}
	if c.Start != nil {
		e.Start = *c.Start
	}
	if c.End != nil {
		e.End = *c.End
	}
	if c.AllDay != nil {
		e.AllDay = *c.AllDay
	}
	if c.Description != nil {
		e.Description = *c.Description
	}
	if c.Location != nil {
		e.Location = *c.Location
	}
	if c.Attendees != nil {
		e.Attendees = slices.Clone(*c.Attendees)
	}
	if c.Reminders != nil {
		e.Reminders = slices.Clone(*c.Reminders)
	}
}

func clone(e Event) Event {
	e.Attendees = slices.Clone(e.Attendees)
	e.Reminders = slices.Clone(e.Reminders)
	e.Exceptions = slices.Clone(e.Exceptions)
	if e.Recurrence != nil {
		r := *e.Recurrence
		r.Weekdays = slices.Clone(r.Weekdays)
		e.Recurrence = &r
	}
	return e
}
