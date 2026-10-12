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
	s.timerMu.Lock()
	defer s.timerMu.Unlock()
	timers, err := s.readTimersLocked()
	if err != nil {
		return Timer{}, err
	}
	active := 0
	for _, timer := range timers {
		if timer.UserID == user && !timer.Canceled && !timer.Delivered {
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
	timer := Timer{ID: id, UserID: user, ChatID: user, CreatedAt: now, DueAt: now.Add(duration)}
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
		if timer.UserID == user && !timer.Canceled && !timer.Delivered {
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
		if !timer.Canceled && !timer.Delivered && !timer.DueAt.After(now) {
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
