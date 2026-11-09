package secretary

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// The work journal is the raw material for Donna learning how the operator works.
// It is an append-only record of what the operator answered to hourly check-ins:
// verbatim text, a timestamp and nothing inferred by a model. Learning from it is
// a separate, later step the operator asks for.

const (
	journalWindow       = 14 * 24 * time.Hour // how long check-ins keep asking
	journalFirstHour    = 9                   // first local hour with a check-in (inclusive)
	journalLastHour     = 18                  // last local hour with a check-in (inclusive)
	journalSlotGrace    = 20 * time.Minute    // a slot is skipped, not asked late, after this
	journalAnswerWindow = 55 * time.Minute    // an unanswered check-in expires after this
	journalMaxTextRunes = 1000
	journalMaxEntries   = 5000
	journalMaxPause     = 24 * time.Hour
)

type JournalKind string

const (
	JournalAnswer  JournalKind = "answer"
	JournalNote    JournalKind = "note"
	JournalSkipped JournalKind = "skipped"
)

type JournalEntry struct {
	At   time.Time   `json:"at"`
	Slot string      `json:"slot,omitempty"`
	Kind JournalKind `json:"kind"`
	Text string      `json:"text,omitempty"`
}

type openCheckin struct {
	MessageID int64     `json:"message_id"`
	AskedAt   time.Time `json:"asked_at"`
	Slot      string    `json:"slot"`
}

type journalState struct {
	StartedAt    time.Time      `json:"started_at,omitempty"`
	Stopped      bool           `json:"stopped,omitempty"`
	PausedUntil  time.Time      `json:"paused_until,omitempty"`
	DoneNotified bool           `json:"done_notified,omitempty"`
	LastSlot     string         `json:"last_slot,omitempty"`
	Open         *openCheckin   `json:"open,omitempty"`
	Entries      []JournalEntry `json:"entries"`
}

// WorkJournal persists check-in state under <state>/work-journal.
type WorkJournal struct {
	mu    sync.Mutex
	path  string
	state journalState
	Now   func() time.Time
}

func OpenWorkJournal(stateDir string) (*WorkJournal, error) {
	if strings.TrimSpace(stateDir) == "" {
		return nil, errors.New("state directory is required")
	}
	j := &WorkJournal{path: filepath.Join(stateDir, "work-journal", "journal.json")}
	data, err := os.ReadFile(j.path)
	switch {
	case err == nil:
		if json.Unmarshal(data, &j.state) != nil {
			return nil, errors.New("work journal is unreadable")
		}
	case !errors.Is(err, os.ErrNotExist):
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(j.path), 0700); err != nil {
		return nil, err
	}
	return j, nil
}

func (j *WorkJournal) now() time.Time {
	if j.Now != nil {
		return j.Now()
	}
	return time.Now()
}

func (j *WorkJournal) saveLocked() error {
	if len(j.state.Entries) > journalMaxEntries {
		j.state.Entries = j.state.Entries[len(j.state.Entries)-journalMaxEntries:]
	}
	return atomicJSON(j.path, j.state)
}

func (j *WorkJournal) activeLocked(now time.Time) bool {
	if j.state.Stopped {
		return false
	}
	if !j.state.StartedAt.IsZero() && now.After(j.state.StartedAt.Add(journalWindow)) {
		return false
	}
	return true
}

// SlotDue reports the check-in slot to ask now, if any: weekdays, 09:00 to 18:59
// local time, in the first minutes of each hour, once per slot, never while
// paused, stopped or after the two-week window.
func (j *WorkJournal) SlotDue(now time.Time, loc *time.Location) (string, bool) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if !j.activeLocked(now) || now.Before(j.state.PausedUntil) {
		return "", false
	}
	local := now.In(loc)
	if wd := local.Weekday(); wd == time.Saturday || wd == time.Sunday {
		return "", false
	}
	if local.Hour() < journalFirstHour || local.Hour() > journalLastHour {
		return "", false
	}
	if time.Duration(local.Minute())*time.Minute >= journalSlotGrace {
		return "", false
	}
	slot := local.Format("2006-01-02T15")
	if slot == j.state.LastSlot {
		return "", false
	}
	return slot, true
}

// Asked records that a check-in for the slot went out. messageID 0 means the
// send outcome is uncertain: the slot is not repeated, but a reply cannot be tied
// to it.
func (j *WorkJournal) Asked(slot string, messageID int64, now time.Time) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.expireLocked(now, true)
	if j.state.StartedAt.IsZero() {
		j.state.StartedAt = now
	}
	j.state.LastSlot = slot
	if messageID != 0 {
		j.state.Open = &openCheckin{MessageID: messageID, AskedAt: now, Slot: slot}
	}
	return j.saveLocked()
}

func (j *WorkJournal) expireLocked(now time.Time, force bool) bool {
	open := j.state.Open
	if open == nil || (!force && now.Sub(open.AskedAt) < journalAnswerWindow) {
		return false
	}
	j.state.Entries = append(j.state.Entries, JournalEntry{At: open.AskedAt, Slot: open.Slot, Kind: JournalSkipped})
	j.state.Open = nil
	return true
}

// Expire turns an unanswered check-in into a "skipped" entry once its window ends.
func (j *WorkJournal) Expire(now time.Time) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.expireLocked(now, false) {
		_ = j.saveLocked()
	}
}

func cleanJournalText(text string) string {
	text = strings.Join(strings.Fields(text), " ")
	if runes := []rune(text); len(runes) > journalMaxTextRunes {
		text = strings.TrimSpace(string(runes[:journalMaxTextRunes]))
	}
	return text
}

// IsOpen reports whether messageID is the check-in currently waiting for an answer.
func (j *WorkJournal) IsOpen(messageID int64, now time.Time) bool {
	j.mu.Lock()
	defer j.mu.Unlock()
	open := j.state.Open
	return open != nil && open.MessageID == messageID && now.Sub(open.AskedAt) < journalAnswerWindow
}

// Answer records the reply to the open check-in. It returns false when messageID
// is not the open one, so an ordinary reply is never captured by accident.
func (j *WorkJournal) Answer(messageID int64, text string, now time.Time) (bool, error) {
	text = cleanJournalText(text)
	j.mu.Lock()
	defer j.mu.Unlock()
	open := j.state.Open
	if text == "" || open == nil || open.MessageID != messageID || now.Sub(open.AskedAt) >= journalAnswerWindow {
		return false, nil
	}
	j.state.Entries = append(j.state.Entries, JournalEntry{At: now, Slot: open.Slot, Kind: JournalAnswer, Text: text})
	j.state.Open = nil
	return true, j.saveLocked()
}

// Note records a free note typed with /diario. It answers the open check-in when
// there is one.
func (j *WorkJournal) Note(text string, now time.Time) error {
	text = cleanJournalText(text)
	if text == "" {
		return errors.New("empty note")
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	entry := JournalEntry{At: now, Kind: JournalNote, Text: text}
	if open := j.state.Open; open != nil && now.Sub(open.AskedAt) < journalAnswerWindow {
		entry.Kind, entry.Slot = JournalAnswer, open.Slot
		j.state.Open = nil
	}
	j.state.Entries = append(j.state.Entries, entry)
	return j.saveLocked()
}

func (j *WorkJournal) Pause(d time.Duration, now time.Time) (time.Time, error) {
	if d <= 0 || d > journalMaxPause {
		return time.Time{}, errors.New("pause must be between one minute and 24 hours")
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	j.state.PausedUntil = now.Add(d)
	return j.state.PausedUntil, j.saveLocked()
}

func (j *WorkJournal) Stop() error {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.state.Stopped, j.state.Open = true, nil
	return j.saveLocked()
}

// Resume lifts a stop or pause. It does not extend a finished window.
func (j *WorkJournal) Resume() error {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.state.Stopped, j.state.PausedUntil = false, time.Time{}
	return j.saveLocked()
}

// Forget deletes every entry and the window, keeping only the stopped flag so a
// wipe never silently restarts the questions.
func (j *WorkJournal) Forget() error {
	j.mu.Lock()
	defer j.mu.Unlock()
	stopped := j.state.Stopped
	j.state = journalState{Stopped: stopped}
	return j.saveLocked()
}

// DoneNotice reports, once, that the two-week window has ended.
func (j *WorkJournal) DoneNotice(now time.Time) (answered int, due bool) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.state.DoneNotified || j.state.StartedAt.IsZero() || !now.After(j.state.StartedAt.Add(journalWindow)) {
		return 0, false
	}
	for _, e := range j.state.Entries {
		if e.Kind != JournalSkipped {
			answered++
		}
	}
	return answered, true
}

func (j *WorkJournal) MarkDoneNotified() error {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.state.DoneNotified = true
	return j.saveLocked()
}

type JournalStatus struct {
	Started       bool
	Stopped       bool
	PausedUntil   time.Time
	DaysLeft      int
	Answered      int
	Skipped       int
	Finished      bool
	WindowEndsAt  time.Time
	LatestAnswers []JournalEntry
}

func (j *WorkJournal) Status(now time.Time, recent int) JournalStatus {
	j.mu.Lock()
	defer j.mu.Unlock()
	st := JournalStatus{Started: !j.state.StartedAt.IsZero(), Stopped: j.state.Stopped}
	if now.Before(j.state.PausedUntil) {
		st.PausedUntil = j.state.PausedUntil
	}
	if st.Started {
		st.WindowEndsAt = j.state.StartedAt.Add(journalWindow)
		if left := st.WindowEndsAt.Sub(now); left > 0 {
			st.DaysLeft = int((left + 24*time.Hour - 1) / (24 * time.Hour))
		} else {
			st.Finished = true
		}
	}
	for _, e := range j.state.Entries {
		switch e.Kind {
		case JournalSkipped:
			st.Skipped++
		default:
			st.Answered++
		}
	}
	for i := len(j.state.Entries) - 1; i >= 0 && len(st.LatestAnswers) < recent; i-- {
		if j.state.Entries[i].Kind != JournalSkipped {
			st.LatestAnswers = append([]JournalEntry{j.state.Entries[i]}, st.LatestAnswers...)
		}
	}
	return st
}
