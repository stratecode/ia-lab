package secretary

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	telegramWebhookMaxBody     = 1 << 20
	telegramWebhookMaxPending  = 128
	telegramCompletedRetention = 48 * time.Hour
)

var errTelegramQueueFull = errors.New("Telegram update queue is full")

type TelegramUpdateQueue interface {
	Enqueue(Update) (bool, error)
	Next(context.Context) (Update, error)
	Complete(int64) error
}

type FileTelegramUpdateQueue struct {
	dir       string
	pending   string
	completed string
	mu        sync.Mutex
}

type completedTelegramUpdate struct {
	CompletedAt time.Time `json:"completed_at"`
}

func OpenTelegramUpdateQueue(stateDir string) (*FileTelegramUpdateQueue, error) {
	if strings.TrimSpace(stateDir) == "" {
		return nil, errors.New("Telegram queue state directory is required")
	}
	dir := filepath.Join(stateDir, "telegram-updates")
	q := &FileTelegramUpdateQueue{dir: dir, pending: filepath.Join(dir, "pending"), completed: filepath.Join(dir, "completed")}
	for _, path := range []string{dir, q.pending, q.completed} {
		if err := os.MkdirAll(path, 0700); err != nil {
			return nil, errors.New("cannot create Telegram queue state")
		}
		if err := os.Chmod(path, 0700); err != nil {
			return nil, errors.New("cannot secure Telegram queue state")
		}
	}
	return q, nil
}

func (q *FileTelegramUpdateQueue) Enqueue(update Update) (bool, error) {
	if update.ID <= 0 {
		return false, errors.New("invalid Telegram update ID")
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if err := q.pruneCompletedLocked(time.Now()); err != nil {
		return false, err
	}
	if _, err := os.Stat(q.pendingPath(update.ID)); err == nil {
		return false, nil
	} else if !os.IsNotExist(err) {
		return false, errors.New("cannot inspect pending Telegram update")
	}
	if _, err := os.Stat(q.completedPath(update.ID)); err == nil {
		return false, nil
	} else if !os.IsNotExist(err) {
		return false, errors.New("cannot inspect completed Telegram update")
	}
	entries, err := os.ReadDir(q.pending)
	if err != nil {
		return false, errors.New("cannot inspect Telegram queue")
	}
	if len(entries) >= telegramWebhookMaxPending {
		return false, errTelegramQueueFull
	}
	if err := writeTelegramQueueJSON(q.pendingPath(update.ID), update); err != nil {
		return false, errors.New("cannot persist Telegram update")
	}
	return true, nil
}

func (q *FileTelegramUpdateQueue) Next(ctx context.Context) (Update, error) {
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		q.mu.Lock()
		entries, err := os.ReadDir(q.pending)
		if err != nil {
			q.mu.Unlock()
			return Update{}, errors.New("cannot read Telegram queue")
		}
		sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasPrefix(entry.Name(), "pending-") || !strings.HasSuffix(entry.Name(), ".json") {
				continue
			}
			id, err := telegramUpdateID(entry.Name(), "pending-")
			if err != nil {
				q.mu.Unlock()
				return Update{}, errors.New("invalid queued Telegram update filename")
			}
			if _, err := os.Stat(q.completedPath(id)); err == nil {
				if err := os.Remove(filepath.Join(q.pending, entry.Name())); err != nil && !os.IsNotExist(err) {
					q.mu.Unlock()
					return Update{}, errors.New("cannot clear completed Telegram update")
				}
				continue
			} else if !os.IsNotExist(err) {
				q.mu.Unlock()
				return Update{}, errors.New("cannot inspect completed Telegram update")
			}
			data, err := os.ReadFile(filepath.Join(q.pending, entry.Name()))
			if err != nil {
				q.mu.Unlock()
				return Update{}, errors.New("cannot read queued Telegram update")
			}
			var update Update
			if err := json.Unmarshal(data, &update); err != nil || update.ID != id {
				q.mu.Unlock()
				return Update{}, errors.New("invalid queued Telegram update")
			}
			q.mu.Unlock()
			return update, nil
		}
		q.mu.Unlock()
		select {
		case <-ctx.Done():
			return Update{}, ctx.Err()
		case <-ticker.C:
		}
	}
}

func (q *FileTelegramUpdateQueue) Complete(id int64) error {
	if id <= 0 {
		return errors.New("invalid Telegram update ID")
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	pendingPath := q.pendingPath(id)
	if _, err := os.Stat(pendingPath); os.IsNotExist(err) {
		if _, doneErr := os.Stat(q.completedPath(id)); doneErr == nil {
			return nil
		}
		return errors.New("Telegram update is not pending")
	} else if err != nil {
		return errors.New("cannot inspect pending Telegram update")
	}
	if err := writeTelegramQueueJSON(q.completedPath(id), completedTelegramUpdate{CompletedAt: time.Now().UTC()}); err != nil {
		return errors.New("cannot persist Telegram completion")
	}
	if err := os.Remove(pendingPath); err != nil && !os.IsNotExist(err) {
		return errors.New("cannot remove completed Telegram update")
	}
	return syncTelegramQueueDir(q.pending)
}

func (q *FileTelegramUpdateQueue) pendingPath(id int64) string {
	return filepath.Join(q.pending, fmt.Sprintf("pending-%020d.json", id))
}

func (q *FileTelegramUpdateQueue) completedPath(id int64) string {
	return filepath.Join(q.completed, fmt.Sprintf("completed-%020d.json", id))
}

func (q *FileTelegramUpdateQueue) pruneCompletedLocked(now time.Time) error {
	entries, err := os.ReadDir(q.completed)
	if err != nil {
		return errors.New("cannot inspect completed Telegram updates")
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasPrefix(entry.Name(), "completed-") || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(q.completed, entry.Name()))
		if err != nil {
			return errors.New("cannot read completed Telegram update")
		}
		var completed completedTelegramUpdate
		if err := json.Unmarshal(data, &completed); err != nil || completed.CompletedAt.IsZero() {
			return errors.New("invalid completed Telegram update")
		}
		if now.Sub(completed.CompletedAt) > telegramCompletedRetention {
			if err := os.Remove(filepath.Join(q.completed, entry.Name())); err != nil && !os.IsNotExist(err) {
				return errors.New("cannot prune completed Telegram update")
			}
		}
	}
	return nil
}

func telegramUpdateID(name, prefix string) (int64, error) {
	value := strings.TrimSuffix(strings.TrimPrefix(name, prefix), ".json")
	return strconv.ParseInt(value, 10, 64)
}

func writeTelegramQueueJSON(path string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".telegram-queue-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return err
	}
	return syncTelegramQueueDir(filepath.Dir(path))
}

func syncTelegramQueueDir(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

type TelegramWebhook struct {
	Queue  TelegramUpdateQueue
	Secret string
}

func (h TelegramWebhook) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	token := r.Header.Get("X-Telegram-Bot-Api-Secret-Token")
	if h.Secret == "" || len(token) != len(h.Secret) || subtle.ConstantTimeCompare([]byte(token), []byte(h.Secret)) != 1 {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	if h.Queue == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, telegramWebhookMaxBody)
	data, err := io.ReadAll(r.Body)
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			w.WriteHeader(http.StatusRequestEntityTooLarge)
			return
		}
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	var update Update
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	if err := decoder.Decode(&update); err != nil || update.ID <= 0 {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	if _, err := h.Queue.Enqueue(update); err != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	w.WriteHeader(http.StatusOK)
}
