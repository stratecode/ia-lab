package codexlocalgateway

import (
	"sync"
	"time"
)

type responseState struct {
	messages  []chatMessage
	expiresAt time.Time
}

type responseStore struct {
	mu         sync.Mutex
	ttl        time.Duration
	maxEntries int
	items      map[string]responseState
}

func newResponseStore(ttl time.Duration, maxEntries int) *responseStore {
	if ttl <= 0 {
		ttl = 30 * time.Minute
	}
	if maxEntries <= 0 {
		maxEntries = 256
	}
	return &responseStore{
		ttl:        ttl,
		maxEntries: maxEntries,
		items:      make(map[string]responseState),
	}
}

func (s *responseStore) get(id string) ([]chatMessage, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	state, ok := s.items[id]
	if !ok {
		return nil, false
	}
	if time.Now().After(state.expiresAt) {
		delete(s.items, id)
		return nil, false
	}
	return cloneMessages(state.messages), true
}

func (s *responseStore) set(id string, messages []chatMessage) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.items) >= s.maxEntries {
		s.evictExpiredLocked()
	}
	if len(s.items) >= s.maxEntries {
		var oldestID string
		var oldest time.Time
		for id, state := range s.items {
			if oldestID == "" || state.expiresAt.Before(oldest) {
				oldestID = id
				oldest = state.expiresAt
			}
		}
		delete(s.items, oldestID)
	}
	s.items[id] = responseState{
		messages:  cloneMessages(messages),
		expiresAt: time.Now().Add(s.ttl),
	}
}

func (s *responseStore) evictExpiredLocked() {
	now := time.Now()
	for id, state := range s.items {
		if now.After(state.expiresAt) {
			delete(s.items, id)
		}
	}
}

func cloneMessages(messages []chatMessage) []chatMessage {
	cloned := make([]chatMessage, len(messages))
	copy(cloned, messages)
	for i := range cloned {
		if len(cloned[i].ToolCalls) > 0 {
			cloned[i].ToolCalls = append([]chatToolCall(nil), cloned[i].ToolCalls...)
		}
	}
	return cloned
}
