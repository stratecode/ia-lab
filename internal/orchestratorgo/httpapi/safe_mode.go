package httpapi

import "sync"

type SafeModeState struct {
	mu      sync.RWMutex
	enabled bool
}

func NewSafeModeState(enabled bool) *SafeModeState {
	return &SafeModeState{enabled: enabled}
}

func (s *SafeModeState) Enabled() bool {
	if s == nil {
		return true
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.enabled
}

func (s *SafeModeState) SetEnabled(enabled bool) (previous bool) {
	if s == nil {
		return enabled
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	previous = s.enabled
	s.enabled = enabled
	return previous
}
