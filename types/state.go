package types

import "sync"

// MemoryState est un StateStore en mémoire, sûr en concurrence.
type MemoryState struct {
	mu     sync.Mutex
	values map[string]any
}

func NewMemoryState() *MemoryState {
	return &MemoryState{values: map[string]any{}}
}

func (s *MemoryState) Get(key string) (any, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.values[key]
	return v, ok
}

func (s *MemoryState) Set(key string, value any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.values[key] = value
}
