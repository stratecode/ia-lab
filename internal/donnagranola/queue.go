package donnagranola

import (
	"context"
	"encoding/base64"
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

const maxPendingEvents = 256

type FileEventQueue struct {
	root      string
	pending   string
	completed string
	failed    string
	mu        sync.Mutex
}

type queuedEvent struct {
	Event     GranolaEvent `json:"event"`
	NotBefore time.Time    `json:"not_before,omitempty"`
	Failure   string       `json:"failure,omitempty"`
}

func OpenEventQueue(stateDir string) (*FileEventQueue, error) {
	if strings.TrimSpace(stateDir) == "" {
		return nil, errors.New("Granola queue state directory is required")
	}
	root := filepath.Join(stateDir, "granola-events")
	q := &FileEventQueue{root: root, pending: filepath.Join(root, "pending"), completed: filepath.Join(root, "completed"), failed: filepath.Join(root, "failed")}
	for _, path := range []string{root, q.pending, q.completed, q.failed} {
		if err := os.MkdirAll(path, 0700); err != nil {
			return nil, errors.New("cannot create Granola queue state")
		}
		if err := os.Chmod(path, 0700); err != nil {
			return nil, errors.New("cannot secure Granola queue state")
		}
	}
	return q, nil
}

func (q *FileEventQueue) Enqueue(ctx context.Context, event GranolaEvent) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if !eventIDPattern.MatchString(event.EventID) || !noteIDPattern.MatchString(event.NoteID) || event.EventType != EventNoteGenerated || event.Scope != ScopePersonal {
		return false, errors.New("invalid Granola event")
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	pendingPath, completePath := q.pendingPath(event.EventID), q.completedPath(event.EventID)
	for _, path := range []string{pendingPath, completePath, q.failedPath(event.EventID)} {
		if _, err := os.Stat(path); err == nil {
			return false, nil
		} else if !os.IsNotExist(err) {
			return false, errors.New("cannot inspect Granola event state")
		}
	}
	entries, err := os.ReadDir(q.pending)
	if err != nil {
		return false, errors.New("cannot inspect Granola queue")
	}
	if len(entries) >= maxPendingEvents {
		return false, errors.New("Granola queue is full")
	}
	if event.ReceivedAt.IsZero() {
		event.ReceivedAt = time.Now().UTC()
	}
	if err := writeEventJSON(pendingPath, queuedEvent{Event: event}); err != nil {
		return false, errors.New("cannot persist Granola event")
	}
	return true, nil
}

func (q *FileEventQueue) Next(ctx context.Context) (GranolaEvent, error) {
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		q.mu.Lock()
		entries, err := os.ReadDir(q.pending)
		if err != nil {
			q.mu.Unlock()
			return GranolaEvent{}, errors.New("cannot read Granola queue")
		}
		sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasPrefix(entry.Name(), "pending-") || !strings.HasSuffix(entry.Name(), ".json") {
				continue
			}
			path := filepath.Join(q.pending, entry.Name())
			data, err := os.ReadFile(path)
			if err != nil {
				q.mu.Unlock()
				return GranolaEvent{}, errors.New("cannot read queued Granola event")
			}
			var item queuedEvent
			if err := json.Unmarshal(data, &item); err != nil || !eventIDPattern.MatchString(item.Event.EventID) || !noteIDPattern.MatchString(item.Event.NoteID) || item.Event.EventType != EventNoteGenerated || item.Event.Scope != ScopePersonal || q.pendingPath(item.Event.EventID) != path {
				q.mu.Unlock()
				return GranolaEvent{}, errors.New("invalid queued Granola event")
			}
			if !item.NotBefore.IsZero() && time.Now().Before(item.NotBefore) {
				continue
			}
			q.mu.Unlock()
			return item.Event, nil
		}
		q.mu.Unlock()
		select {
		case <-ctx.Done():
			return GranolaEvent{}, ctx.Err()
		case <-ticker.C:
		}
	}
}

func (q *FileEventQueue) Retry(eventID string, delay time.Duration) error {
	if !eventIDPattern.MatchString(eventID) || delay < 0 {
		return errors.New("invalid Granola retry")
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	path := q.pendingPath(eventID)
	data, err := os.ReadFile(path)
	if err != nil {
		return errors.New("Granola event is not pending")
	}
	var item queuedEvent
	if json.Unmarshal(data, &item) != nil || item.Event.EventID != eventID {
		return errors.New("invalid queued Granola event")
	}
	item.Event.Attempts++
	item.NotBefore = time.Now().UTC().Add(delay)
	if err := writeEventJSON(path, item); err != nil {
		return errors.New("cannot persist Granola retry")
	}
	return nil
}

func (q *FileEventQueue) Fail(eventID string, cause error) error {
	if !eventIDPattern.MatchString(eventID) {
		return errors.New("invalid Granola event ID")
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	pendingPath := q.pendingPath(eventID)
	data, err := os.ReadFile(pendingPath)
	if err != nil {
		return errors.New("Granola event is not pending")
	}
	var item queuedEvent
	if json.Unmarshal(data, &item) != nil || item.Event.EventID != eventID {
		return errors.New("invalid queued Granola event")
	}
	item.Failure = failureCategory(cause)
	if err := writeEventJSON(q.failedPath(eventID), item); err != nil {
		return errors.New("cannot persist failed Granola event")
	}
	if err := os.Remove(pendingPath); err != nil && !os.IsNotExist(err) {
		return errors.New("cannot remove failed Granola event")
	}
	return syncEventDir(q.pending)
}

func (q *FileEventQueue) Complete(eventID string) error {
	if !eventIDPattern.MatchString(eventID) {
		return errors.New("invalid Granola event ID")
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	pendingPath, completePath := q.pendingPath(eventID), q.completedPath(eventID)
	if _, err := os.Stat(completePath); err == nil {
		return nil
	}
	if _, err := os.Stat(pendingPath); err != nil {
		return errors.New("Granola event is not pending")
	}
	if err := os.Rename(pendingPath, completePath); err != nil {
		return errors.New("cannot persist Granola completion")
	}
	if err := syncEventDir(q.pending); err != nil {
		return err
	}
	return syncEventDir(q.completed)
}

func (q *FileEventQueue) pendingPath(eventID string) string {
	return filepath.Join(q.pending, "pending-"+base64.RawURLEncoding.EncodeToString([]byte(eventID))+".json")
}

func (q *FileEventQueue) completedPath(eventID string) string {
	return filepath.Join(q.completed, "completed-"+base64.RawURLEncoding.EncodeToString([]byte(eventID))+".json")
}

func (q *FileEventQueue) failedPath(eventID string) string {
	return filepath.Join(q.failed, "failed-"+base64.RawURLEncoding.EncodeToString([]byte(eventID))+".json")
}

func writeEventJSON(path string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".granola-queue-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return err
	}
	return syncEventDir(filepath.Dir(path))
}

func syncEventDir(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	defer dir.Close()
	if err := dir.Sync(); err != nil && !errors.Is(err, os.ErrInvalid) {
		return fmt.Errorf("cannot sync Granola queue directory")
	}
	return nil
}
