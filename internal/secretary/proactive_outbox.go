package secretary

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

type ProactiveStatus string

const (
	ProactivePending   ProactiveStatus = "pending"
	ProactiveSending   ProactiveStatus = "sending"
	ProactiveDelivered ProactiveStatus = "delivered"
	ProactiveFailed    ProactiveStatus = "failed"
	ProactiveUncertain ProactiveStatus = "uncertain"
	ProactiveExpired   ProactiveStatus = "expired"
)

const (
	proactiveMaxAttempts    = 3
	proactiveMaxAge         = 24 * time.Hour
	proactiveRetention      = 30 * 24 * time.Hour
	proactiveMaxTextRunes   = 700
	proactiveOpenWebUILimit = 3
)

// ProactiveNotification is a generic, source-grounded message waiting for
// delivery. It is persisted before any send is attempted. Granola follow-ups keep
// their own store; nothing here reads or changes them.
type ProactiveNotification struct {
	ID             string          `json:"id"`
	OperatorID     int64           `json:"operator_id"`
	Channel        string          `json:"channel"`
	ConversationID string          `json:"conversation_id"`
	TaskID         string          `json:"task_id"`
	RunID          string          `json:"run_id"`
	ScheduledFor   time.Time       `json:"scheduled_for"`
	Text           string          `json:"text"`
	Evidence       []EvidenceRef   `json:"evidence,omitempty"`
	Status         ProactiveStatus `json:"status"`
	Attempts       int             `json:"attempts"`
	SendGroup      string          `json:"send_group,omitempty"`
	CreatedAt      time.Time       `json:"created_at"`
	UpdatedAt      time.Time       `json:"updated_at"`
	SentAt         time.Time       `json:"sent_at,omitempty"`
}

// ProactiveOutbox is the durable queue between skill runs and channels.
type ProactiveOutbox struct {
	path string
	mu   sync.Mutex
	Now  func() time.Time
}

type proactiveOutboxFile struct {
	Items []ProactiveNotification `json:"items"`
}

// OpenProactiveOutbox opens the outbox. A notification left in "sending" by a
// process that died mid-send is turned into "uncertain": it may or may not have
// reached the channel, so it is never retried automatically.
func OpenProactiveOutbox(stateDir string) (*ProactiveOutbox, error) {
	if stateDir == "" {
		return nil, errors.New("state directory required")
	}
	dir := filepath.Join(stateDir, "proactive-outbox")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, errors.New("cannot create proactive outbox")
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return nil, errors.New("cannot protect proactive outbox")
	}
	o := &ProactiveOutbox{path: filepath.Join(dir, "outbox.json"), Now: time.Now}
	o.mu.Lock()
	defer o.mu.Unlock()
	items, err := o.load()
	if err != nil {
		return nil, err
	}
	changed := false
	for i := range items {
		if items[i].Status == ProactiveSending {
			items[i].Status, items[i].UpdatedAt = ProactiveUncertain, o.now().UTC()
			changed = true
		}
	}
	if changed {
		if err := o.save(items); err != nil {
			return nil, err
		}
	}
	return o, nil
}

func (o *ProactiveOutbox) now() time.Time {
	if o.Now == nil {
		return time.Now()
	}
	return o.Now()
}

func (o *ProactiveOutbox) load() ([]ProactiveNotification, error) {
	b, err := os.ReadFile(o.path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, errors.New("cannot read proactive outbox")
	}
	var file proactiveOutboxFile
	if err := json.Unmarshal(b, &file); err != nil {
		return nil, errors.New("invalid proactive outbox")
	}
	return file.Items, nil
}

func (o *ProactiveOutbox) save(items []ProactiveNotification) error {
	kept := items[:0:0]
	now := o.now()
	for _, item := range items {
		final := item.Status == ProactiveDelivered || item.Status == ProactiveExpired
		if final && now.Sub(item.UpdatedAt) > proactiveRetention {
			continue
		}
		kept = append(kept, item)
	}
	if err := atomicJSON(o.path, proactiveOutboxFile{Items: kept}); err != nil {
		return errors.New("cannot persist proactive outbox")
	}
	return nil
}

func proactiveID(taskID string, scheduledFor time.Time) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s@%d", taskID, scheduledFor.UTC().Unix())))
	return "n" + hex.EncodeToString(sum[:6])
}

// Enqueue persists a notification. The ID derives from (task, scheduled
// instant), so enqueueing the same occurrence twice returns the stored one and
// created=false: a restart or duplicate tick cannot produce a second message.
func (o *ProactiveOutbox) Enqueue(n ProactiveNotification) (ProactiveNotification, bool, error) {
	if n.OperatorID == 0 || n.Channel == "" || n.TaskID == "" || n.Text == "" {
		return ProactiveNotification{}, false, errors.New("incomplete notification")
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	items, err := o.load()
	if err != nil {
		return ProactiveNotification{}, false, err
	}
	n.ID = proactiveID(n.TaskID, n.ScheduledFor)
	for _, existing := range items {
		if existing.ID == n.ID {
			return existing, false, nil
		}
	}
	now := o.now().UTC()
	n.Text = truncateTextRunes(n.Text, proactiveMaxTextRunes)
	n.Status, n.Attempts, n.CreatedAt, n.UpdatedAt = ProactivePending, 0, now, now
	if err := o.save(append(items, n)); err != nil {
		return ProactiveNotification{}, false, err
	}
	return n, true, nil
}

// ForOccurrence finds the notification already queued for a task occurrence.
func (o *ProactiveOutbox) ForOccurrence(taskID string, scheduledFor time.Time) (ProactiveNotification, bool, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	items, err := o.load()
	if err != nil {
		return ProactiveNotification{}, false, err
	}
	id := proactiveID(taskID, scheduledFor)
	for _, item := range items {
		if item.ID == id {
			return item, true, nil
		}
	}
	return ProactiveNotification{}, false, nil
}

// Deliverable returns Telegram notifications ready to send: pending, or failed
// with a failure known to have happened before the send was accepted.
func (o *ProactiveOutbox) Deliverable(channel string) ([]ProactiveNotification, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	items, err := o.load()
	if err != nil {
		return nil, err
	}
	var out []ProactiveNotification
	for _, item := range items {
		if item.Channel != channel {
			continue
		}
		if item.Status == ProactivePending || (item.Status == ProactiveFailed && item.Attempts < proactiveMaxAttempts) {
			out = append(out, item)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].ScheduledFor.Before(out[j].ScheduledFor) })
	return out, nil
}

// BeginSend persists the "sending" state before the channel is contacted.
func (o *ProactiveOutbox) BeginSend(ids []string, group string) error {
	return o.update(ids, func(n *ProactiveNotification, now time.Time) {
		n.Status, n.SendGroup, n.UpdatedAt = ProactiveSending, group, now
		n.Attempts++
	})
}

// Complete records the outcome of a send. Failed means the channel rejected the
// message before accepting it; uncertain means the outcome is unknown.
func (o *ProactiveOutbox) Complete(ids []string, status ProactiveStatus) error {
	switch status {
	case ProactiveDelivered, ProactiveFailed, ProactiveUncertain, ProactiveExpired:
	default:
		return errors.New("invalid final status")
	}
	return o.update(ids, func(n *ProactiveNotification, now time.Time) {
		n.Status, n.UpdatedAt = status, now
		if status == ProactiveDelivered || status == ProactiveUncertain {
			n.SentAt = now
		}
	})
}

func (o *ProactiveOutbox) update(ids []string, apply func(*ProactiveNotification, time.Time)) error {
	if len(ids) == 0 {
		return nil
	}
	want := map[string]bool{}
	for _, id := range ids {
		want[id] = true
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	items, err := o.load()
	if err != nil {
		return err
	}
	now := o.now().UTC()
	found := 0
	for i := range items {
		if want[items[i].ID] {
			apply(&items[i], now)
			found++
		}
	}
	if found != len(want) {
		return errors.New("notification not found")
	}
	return o.save(items)
}

// SentMessagesOn counts distinct Telegram messages sent (or possibly sent) to the
// operator on a local day. Coalesced notifications share one send group.
func (o *ProactiveOutbox) SentMessagesOn(operatorID int64, now time.Time, loc *time.Location) (int, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	items, err := o.load()
	if err != nil {
		return 0, err
	}
	day := now.In(loc).Format("2006-01-02")
	groups := map[string]bool{}
	for _, item := range items {
		if item.OperatorID != operatorID || item.Channel != "telegram" || item.SentAt.IsZero() {
			continue
		}
		if item.Status != ProactiveDelivered && item.Status != ProactiveUncertain {
			continue
		}
		if item.SentAt.In(loc).Format("2006-01-02") == day {
			key := item.SendGroup
			if key == "" {
				key = item.ID
			}
			groups[key] = true
		}
	}
	return len(groups), nil
}

// Expire marks notifications that have waited too long (outside the send window,
// or failing repeatedly) as expired, and returns them so their runs can close.
func (o *ProactiveOutbox) Expire() ([]ProactiveNotification, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	items, err := o.load()
	if err != nil {
		return nil, err
	}
	now := o.now().UTC()
	var expired []ProactiveNotification
	for i := range items {
		waiting := items[i].Status == ProactivePending || items[i].Status == ProactiveFailed
		if waiting && now.Sub(items[i].CreatedAt) > proactiveMaxAge {
			items[i].Status, items[i].UpdatedAt = ProactiveExpired, now
			expired = append(expired, items[i])
		}
	}
	if len(expired) > 0 {
		if err := o.save(items); err != nil {
			return nil, err
		}
	}
	return expired, nil
}

// PendingOpenWebUI returns up to limit queued messages for the operator and how
// many more remain. Delivery happens on the next authenticated conversation.
func (o *ProactiveOutbox) PendingOpenWebUI(operatorID int64, limit int) ([]ProactiveNotification, int, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	items, err := o.load()
	if err != nil {
		return nil, 0, err
	}
	var out []ProactiveNotification
	for _, item := range items {
		if item.OperatorID == operatorID && item.Channel == "openwebui" && item.Status == ProactivePending {
			out = append(out, item)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].ScheduledFor.Before(out[j].ScheduledFor) })
	remaining := 0
	if len(out) > limit {
		remaining = len(out) - limit
		out = out[:limit]
	}
	return out, remaining, nil
}

// Uncertain lists the operator's notifications whose delivery is unknown.
func (o *ProactiveOutbox) Uncertain(operatorID int64) ([]ProactiveNotification, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	items, err := o.load()
	if err != nil {
		return nil, err
	}
	var out []ProactiveNotification
	for _, item := range items {
		if item.OperatorID == operatorID && item.Status == ProactiveUncertain {
			out = append(out, item)
		}
	}
	return out, nil
}
