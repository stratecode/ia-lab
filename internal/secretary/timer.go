package secretary

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type Timer struct {
	ID        string    `json:"id"`
	UserID    int64     `json:"user_id"`
	ChatID    int64     `json:"chat_id"`
	CreatedAt time.Time `json:"created_at"`
	DueAt     time.Time `json:"due_at"`
	Delivered bool      `json:"delivered"`
	Canceled  bool      `json:"canceled"`
	// Reason is what the alert says ("Revisa la comida"). Empty means the generic text.
	Reason string `json:"reason,omitempty"`
	// Draft is a reminder Donna understood from natural language and is waiting
	// for the operator's confirmation. A draft never fires and expires unconfirmed;
	// Duration is counted from the confirmation, not from the request.
	Draft    bool          `json:"draft,omitempty"`
	Duration time.Duration `json:"duration,omitempty"`
}

// timerDraftTTL is how long a draft waits for its confirmation button.
const timerDraftTTL = 10 * time.Minute

func (t Timer) staleDraft(now time.Time) bool { return t.Draft && now.Sub(t.CreatedAt) > timerDraftTTL }

// alertText is the message sent when the timer ends.
func (t Timer) alertText() string {
	if t.Reason == "" {
		return "⏰ Temporizador terminado."
	}
	return "⏰ " + strings.TrimRight(t.Reason, ".") + "."
}

var timerRequest = regexp.MustCompile(`(?i)^(?:/timer\s+|(?:(?:pon(?:me)?\s+)?(?:un\s+)?temporizador(?:\s+de)?\s+)|(?:av[ií]same\s+en\s+))(\d+)\s*(s|seg(?:undo)?s?|m|min(?:uto)?s?|h|hora?s?|['’′])(?:\s*[.!])?$`)
var timerIntent = regexp.MustCompile(`(?i)^(?:/timer(?:\s|$)|.*temporizador\b|av[ií]same\s+en\b)`)

func parseTimerRequest(text string) (time.Duration, bool) {
	matches := timerRequest.FindStringSubmatch(strings.TrimSpace(text))
	if len(matches) != 3 {
		return 0, false
	}
	amount, err := strconv.Atoi(matches[1])
	if err != nil || amount < 1 {
		return 0, false
	}
	unit := strings.ToLower(matches[2])
	var unitDuration time.Duration
	switch unit {
	case "s", "seg", "segundo", "segundos":
		unitDuration = time.Second
	case "m", "min", "minuto", "minutos", "'", "’", "′":
		unitDuration = time.Minute
	case "h", "hora", "horas":
		unitDuration = time.Hour
	default:
		return 0, false
	}
	if amount > int((24*time.Hour)/unitDuration) {
		return 0, false
	}
	return time.Duration(amount) * unitDuration, true
}

func isTimerIntent(text string) bool { return timerIntent.MatchString(strings.TrimSpace(text)) }

func (s *Service) timerPath() string { return filepath.Join(s.StateDir, "timers.json") }

func (s *Service) readTimers() ([]Timer, error) {
	s.timerMu.Lock()
	defer s.timerMu.Unlock()
	return s.readTimersLocked()
}

func (s *Service) readTimersLocked() ([]Timer, error) {
	var timers []Timer
	b, err := os.ReadFile(s.timerPath())
	if os.IsNotExist(err) {
		return timers, nil
	}
	if err != nil {
		return nil, errors.New("cannot read timer state")
	}
	if err := json.Unmarshal(b, &timers); err != nil {
		return nil, errors.New("invalid timer state")
	}
	return timers, nil
}

func (s *Service) writeTimers(timers []Timer) error {
	s.timerMu.Lock()
	defer s.timerMu.Unlock()
	return s.writeTimersLocked(timers)
}

func (s *Service) writeTimersLocked(timers []Timer) error {
	if err := atomicJSON(s.timerPath(), timers); err != nil {
		return errors.New("cannot persist timer state")
	}
	return nil
}

func newTimerID() (string, error) {
	var raw [8]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", errors.New("cannot create timer id")
	}
	return hex.EncodeToString(raw[:]), nil
}

func (s *Service) createTimer(user int64, duration time.Duration) (Timer, error) {
	return s.createTimerWithReason(user, duration, "")
}

func (s *Service) createTimerWithReason(user int64, duration time.Duration, reason string) (Timer, error) {
	return s.createTimerOpts(user, duration, reason, false)
}

// createTimerDraft stores a reminder that does nothing until confirmTimer.
func (s *Service) createTimerDraft(user int64, duration time.Duration, reason string) (Timer, error) {
	return s.createTimerOpts(user, duration, reason, true)
}

func (s *Service) createTimerOpts(user int64, duration time.Duration, reason string, draft bool) (Timer, error) {
	s.timerMu.Lock()
	defer s.timerMu.Unlock()
	timers, err := s.readTimersLocked()
	if err != nil {
		return Timer{}, err
	}
	active := 0
	for _, timer := range timers {
		if timer.UserID == user && !timer.Canceled && !timer.Delivered && !timer.staleDraft(time.Now()) {
			active++
		}
	}
	if active >= 10 {
		return Timer{}, errors.New("timer limit reached")
	}
	id, err := newTimerID()
	if err != nil {
		return Timer{}, err
	}
	now := time.Now().UTC()
	timer := Timer{ID: id, UserID: user, ChatID: user, CreatedAt: now, DueAt: now.Add(duration), Reason: reason, Draft: draft, Duration: duration}
	timers = append(timers, timer)
	if err := s.writeTimersLocked(timers); err != nil {
		return Timer{}, err
	}
	return timer, nil
}

func (s *Service) timersForUser(user int64) ([]Timer, error) {
	timers, err := s.readTimers()
	if err != nil {
		return nil, err
	}
	active := make([]Timer, 0, len(timers))
	for _, timer := range timers {
		if timer.UserID == user && !timer.Canceled && !timer.Delivered && !timer.Draft {
			active = append(active, timer)
		}
	}
	return active, nil
}

func (s *Service) cancelTimer(user int64, id string) (bool, error) {
	s.timerMu.Lock()
	defer s.timerMu.Unlock()
	timers, err := s.readTimersLocked()
	if err != nil {
		return false, err
	}
	for i := range timers {
		if timers[i].ID == id && timers[i].UserID == user && !timers[i].Canceled && !timers[i].Delivered {
			timers[i].Canceled = true
			return true, s.writeTimersLocked(timers)
		}
	}
	return false, nil
}

func (s *Service) dueTimers(now time.Time) ([]Timer, error) {
	timers, err := s.readTimers()
	if err != nil {
		return nil, err
	}
	due := make([]Timer, 0)
	for _, timer := range timers {
		if !timer.Canceled && !timer.Delivered && !timer.Draft && !timer.DueAt.After(now) {
			due = append(due, timer)
		}
	}
	return due, nil
}

func (s *Service) markTimerDelivered(id string) error {
	s.timerMu.Lock()
	defer s.timerMu.Unlock()
	timers, err := s.readTimersLocked()
	if err != nil {
		return err
	}
	for i := range timers {
		if timers[i].ID == id && !timers[i].Canceled {
			timers[i].Delivered = true
			return s.writeTimersLocked(timers)
		}
	}
	return fmt.Errorf("timer %s no longer exists", id)
}

func formatTimerDuration(d time.Duration) string {
	switch {
	case d%time.Hour == 0:
		hours := int(d / time.Hour)
		unit := "horas"
		if hours == 1 {
			unit = "hora"
		}
		return fmt.Sprintf("%d %s", hours, unit)
	case d%time.Minute == 0:
		minutes := int(d / time.Minute)
		unit := "minutos"
		if minutes == 1 {
			unit = "minuto"
		}
		return fmt.Sprintf("%d %s", minutes, unit)
	default:
		seconds := int(d / time.Second)
		unit := "segundos"
		if seconds == 1 {
			unit = "segundo"
		}
		return fmt.Sprintf("%d %s", seconds, unit)
	}
}

type timerConfirmation int

const (
	timerConfirmed timerConfirmation = iota
	timerAlreadyActive
	timerDraftExpired
	timerDraftMissing
)

// confirmTimer activates the operator's own draft. The countdown starts now.
func (s *Service) confirmTimer(user int64, id string) (Timer, timerConfirmation, error) {
	s.timerMu.Lock()
	defer s.timerMu.Unlock()
	timers, err := s.readTimersLocked()
	if err != nil {
		return Timer{}, timerDraftMissing, err
	}
	now := time.Now().UTC()
	for i := range timers {
		if timers[i].ID != id || timers[i].UserID != user || timers[i].Canceled || timers[i].Delivered {
			continue
		}
		if !timers[i].Draft {
			return timers[i], timerAlreadyActive, nil
		}
		if timers[i].staleDraft(now) {
			timers[i].Canceled = true
			return Timer{}, timerDraftExpired, s.writeTimersLocked(timers)
		}
		timers[i].Draft = false
		timers[i].DueAt = now.Add(timers[i].Duration)
		return timers[i], timerConfirmed, s.writeTimersLocked(timers)
	}
	return Timer{}, timerDraftMissing, nil
}

// confirmTimerReply is the shared answer for a button press or /confirmar-aviso.
func (s *Service) confirmTimerReply(user int64, id string) string {
	timer, status, err := s.confirmTimer(user, id)
	switch {
	case err != nil:
		return "No he podido confirmar el aviso; no he programado nada."
	case status == timerConfirmed:
		return reminderSetText(timer.ID, timer.Duration, timer.Reason)
	case status == timerAlreadyActive:
		return "Ese aviso ya estaba programado."
	case status == timerDraftExpired:
		return "Ese aviso caducó sin confirmar y no se programó. Pídemelo otra vez."
	}
	return "No encuentro un aviso tuyo pendiente de confirmar con ese ID."
}
