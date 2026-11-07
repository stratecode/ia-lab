package donnaidentity

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type Store interface {
	CreateCandidate(context.Context, MappingCandidate) (MappingCandidate, error)
	Get(context.Context, int64, string) (MappingCandidate, error)
	List(context.Context, int64, EntityKind, MappingStatus) ([]MappingCandidate, error)
	SetStatus(context.Context, int64, string, string, string, string, MappingStatus, int64, time.Time) (MappingCandidate, error)
}

type JSONStore struct {
	root string
	path string
	mu   sync.Mutex
	data map[string]MappingCandidate
}

func NewJSONStore(root string) (*JSONStore, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return nil, ErrInvalidMapping
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, fmt.Errorf("create identity state directory: %w", err)
	}
	if err := os.Chmod(root, 0o700); err != nil {
		return nil, fmt.Errorf("secure identity state directory: %w", err)
	}

	store := &JSONStore{root: root, path: filepath.Join(root, "mappings.json"), data: map[string]MappingCandidate{}}
	if err := store.load(); err != nil {
		return nil, err
	}
	return store, nil
}

func (s *JSONStore) load() error {
	data, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read identity mappings: %w", err)
	}
	var records []MappingCandidate
	if err := json.Unmarshal(data, &records); err != nil {
		return fmt.Errorf("decode identity mappings: %w", err)
	}
	for _, record := range records {
		if err := validateStoredCandidate(record); err != nil {
			return fmt.Errorf("invalid identity mapping store: %w", err)
		}
		s.data[record.ID] = record
	}
	return nil
}

func (s *JSONStore) CreateCandidate(ctx context.Context, candidate MappingCandidate) (MappingCandidate, error) {
	if err := ctx.Err(); err != nil {
		return MappingCandidate{}, err
	}
	candidate.Channel = strings.TrimSpace(candidate.Channel)
	candidate.ConversationID = strings.TrimSpace(candidate.ConversationID)
	candidate.Kind = EntityKind(strings.TrimSpace(string(candidate.Kind)))
	candidate.CanonicalID = strings.TrimSpace(candidate.CanonicalID)
	candidate.CanonicalLabel = strings.TrimSpace(candidate.CanonicalLabel)
	candidate.Provider = strings.ToLower(strings.TrimSpace(candidate.Provider))
	candidate.ExternalID = strings.TrimSpace(candidate.ExternalID)
	candidate.Aliases = cleanStrings(candidate.Aliases)
	candidate.Evidence = cleanStrings(candidate.Evidence)
	if candidate.Status == "" {
		candidate.Status = MappingPending
	}
	if candidate.Status != MappingPending || candidate.ID != "" || candidate.ConfirmedBy != 0 || !candidate.ConfirmedAt.IsZero() {
		return MappingCandidate{}, ErrInvalidMapping
	}
	if err := validateCandidate(candidate); err != nil {
		return MappingCandidate{}, err
	}
	id, err := newMappingID()
	if err != nil {
		return MappingCandidate{}, err
	}
	now := time.Now().UTC()
	candidate.ID = id
	candidate.CreatedAt = now
	candidate.PayloadDigest = payloadDigest(candidate)

	s.mu.Lock()
	defer s.mu.Unlock()
	s.data[id] = candidate
	if err := s.persistLocked(); err != nil {
		delete(s.data, id)
		return MappingCandidate{}, err
	}
	return cloneCandidate(candidate), nil
}

func (s *JSONStore) Get(ctx context.Context, operatorID int64, id string) (MappingCandidate, error) {
	if err := ctx.Err(); err != nil {
		return MappingCandidate{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	item, ok := s.data[id]
	if !ok || item.OperatorID != operatorID {
		return MappingCandidate{}, ErrMappingNotFound
	}
	return cloneCandidate(item), nil
}

func (s *JSONStore) List(ctx context.Context, operatorID int64, kind EntityKind, status MappingStatus) ([]MappingCandidate, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	items := make([]MappingCandidate, 0)
	for _, item := range s.data {
		if item.OperatorID != operatorID || (kind != "" && item.Kind != kind) || (status != "" && item.Status != status) {
			continue
		}
		items = append(items, cloneCandidate(item))
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].CreatedAt.Equal(items[j].CreatedAt) {
			return items[i].ID < items[j].ID
		}
		return items[i].CreatedAt.Before(items[j].CreatedAt)
	})
	return items, nil
}

func (s *JSONStore) SetStatus(ctx context.Context, operatorID int64, id, channel, conversationID, digest string, status MappingStatus, confirmedBy int64, at time.Time) (MappingCandidate, error) {
	if err := ctx.Err(); err != nil {
		return MappingCandidate{}, err
	}
	if status != MappingApproved && status != MappingRejected || operatorID <= 0 || confirmedBy != operatorID || at.IsZero() {
		return MappingCandidate{}, ErrInvalidMapping
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	item, ok := s.data[id]
	if !ok || item.OperatorID != operatorID {
		return MappingCandidate{}, ErrMappingNotFound
	}
	if item.Channel != channel || item.ConversationID != conversationID || item.PayloadDigest != digest || payloadDigest(item) != item.PayloadDigest {
		return MappingCandidate{}, ErrMappingConflict
	}
	if item.Status == status {
		return cloneCandidate(item), nil
	}
	if item.Status != MappingPending {
		return MappingCandidate{}, ErrMappingConflict
	}
	previous := item
	item.Status = status
	if status == MappingApproved {
		item.ConfirmedAt = at.UTC()
		item.ConfirmedBy = confirmedBy
	}
	s.data[id] = item
	if err := s.persistLocked(); err != nil {
		s.data[id] = previous
		return MappingCandidate{}, err
	}
	return cloneCandidate(item), nil
}

func (s *JSONStore) persistLocked() error {
	items := make([]MappingCandidate, 0, len(s.data))
	for _, item := range s.data {
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	data, err := json.Marshal(items)
	if err != nil {
		return fmt.Errorf("encode identity mappings: %w", err)
	}
	temp, err := os.CreateTemp(s.root, ".mappings-*.tmp")
	if err != nil {
		return fmt.Errorf("create identity state temp file: %w", err)
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if err := temp.Chmod(0o600); err != nil {
		temp.Close()
		return fmt.Errorf("secure identity state temp file: %w", err)
	}
	if _, err := temp.Write(data); err != nil {
		temp.Close()
		return fmt.Errorf("write identity state: %w", err)
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return fmt.Errorf("sync identity state: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("close identity state: %w", err)
	}
	if err := os.Rename(tempPath, s.path); err != nil {
		return fmt.Errorf("replace identity state: %w", err)
	}
	dir, err := os.Open(s.root)
	if err == nil {
		_ = dir.Sync()
		_ = dir.Close()
	}
	return nil
}

func validateCandidate(candidate MappingCandidate) error {
	if candidate.OperatorID <= 0 || candidate.Channel == "" || candidate.ConversationID == "" || candidate.CanonicalID == "" || candidate.CanonicalLabel == "" || candidate.Provider == "" || candidate.ExternalID == "" {
		return ErrInvalidMapping
	}
	if candidate.Channel != "telegram" && candidate.Channel != "openwebui" {
		return ErrInvalidMapping
	}
	switch candidate.Kind {
	case EntityPerson, EntityEmail, EntityProject, EntityAlias:
	default:
		return ErrInvalidMapping
	}
	if candidate.Provider != "jira" && candidate.Provider != "granola" {
		return ErrInvalidMapping
	}
	if len(candidate.CanonicalLabel) > 240 || len(candidate.Aliases) > 32 || len(candidate.Evidence) > 8 {
		return ErrInvalidMapping
	}
	for _, value := range append(append([]string{}, candidate.Aliases...), candidate.Evidence...) {
		if len(value) > 300 {
			return ErrInvalidMapping
		}
	}
	return nil
}

func validateStoredCandidate(candidate MappingCandidate) error {
	if err := validateCandidate(candidate); err != nil {
		return err
	}
	if candidate.ID == "" || candidate.PayloadDigest == "" || candidate.CreatedAt.IsZero() {
		return ErrInvalidMapping
	}
	switch candidate.Status {
	case MappingPending:
		if candidate.ConfirmedBy != 0 || !candidate.ConfirmedAt.IsZero() {
			return ErrInvalidMapping
		}
	case MappingApproved:
		if candidate.ConfirmedBy <= 0 || candidate.ConfirmedAt.IsZero() {
			return ErrInvalidMapping
		}
	case MappingRejected:
	default:
		return ErrInvalidMapping
	}
	if payloadDigest(candidate) != candidate.PayloadDigest {
		return ErrInvalidMapping
	}
	return nil
}

func payloadDigest(candidate MappingCandidate) string {
	canonical := struct {
		Kind           EntityKind `json:"kind"`
		CanonicalID    string     `json:"canonical_id"`
		CanonicalLabel string     `json:"canonical_label"`
		Provider       string     `json:"provider"`
		ExternalID     string     `json:"external_id"`
		Aliases        []string   `json:"aliases,omitempty"`
		Evidence       []string   `json:"evidence,omitempty"`
	}{candidate.Kind, candidate.CanonicalID, candidate.CanonicalLabel, candidate.Provider, candidate.ExternalID, candidate.Aliases, candidate.Evidence}
	data, _ := json.Marshal(canonical)
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

func newMappingID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("create mapping ID: %w", err)
	}
	return hex.EncodeToString(raw[:]), nil
}

func cleanStrings(values []string) []string {
	cleaned := make([]string, 0, len(values))
	seen := map[string]struct{}{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		key := strings.ToLower(value)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		cleaned = append(cleaned, value)
	}
	return cleaned
}

func cloneCandidate(candidate MappingCandidate) MappingCandidate {
	candidate.Aliases = append([]string(nil), candidate.Aliases...)
	candidate.Evidence = append([]string(nil), candidate.Evidence...)
	return candidate
}
