package secretary

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const granolaNotificationMaxBody = 12 << 10

var granolaNotificationID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,200}$`)

const granolaCandidateRetention = 30 * 24 * time.Hour

type GranolaNotification struct {
	ID                string                 `json:"id"`
	Message           string                 `json:"message"`
	TaskCandidates    []GranolaTaskCandidate `json:"task_candidates,omitempty"`
	CreatedAt         time.Time              `json:"created_at"`
	TelegramRequired  bool                   `json:"telegram_required"`
	TelegramDelivered bool                   `json:"telegram_delivered"`
	WebUIDelivered    bool                   `json:"webui_delivered"`
	DeliveredAt       time.Time              `json:"delivered_at,omitempty"`
}

type GranolaTaskCandidate struct {
	NoteID        string `json:"note_id"`
	NoteURL       string `json:"note_url"`
	MeetingTitle  string `json:"meeting_title"`
	SourceSpan    string `json:"source_span"`
	SourceSummary string `json:"source_summary"`
}

type GranolaNotificationStore struct {
	root string
	mu   sync.Mutex
}

type GranolaNotificationRequest struct {
	OperatorID     int64               `json:"operator_id"`
	TelegramChatID int64               `json:"telegram_chat_id,omitempty"`
	Notification   GranolaNotification `json:"notification"`
}

func OpenGranolaNotificationStore(stateDir string) (*GranolaNotificationStore, error) {
	if strings.TrimSpace(stateDir) == "" {
		return nil, errors.New("notification state directory required")
	}
	root := filepath.Join(stateDir, "granola-notifications")
	if err := os.MkdirAll(root, 0700); err != nil {
		return nil, errors.New("cannot create notification state")
	}
	if err := os.Chmod(root, 0700); err != nil {
		return nil, errors.New("cannot secure notification state")
	}
	store := &GranolaNotificationStore{root: root}
	operators, err := os.ReadDir(root)
	if err != nil {
		return nil, errors.New("cannot inspect notification state")
	}
	now := time.Now().UTC()
	for _, operator := range operators {
		if !operator.IsDir() || !strings.HasPrefix(operator.Name(), "operator-") {
			continue
		}
		id, err := strconv.ParseInt(strings.TrimPrefix(operator.Name(), "operator-"), 10, 64)
		if err != nil || id <= 0 {
			continue
		}
		if err := store.pruneExpiredCandidatesLocked(id, now); err != nil {
			return nil, errors.New("cannot prune expired task candidates")
		}
	}
	return store, nil
}

func (s *GranolaNotificationStore) Put(operatorID int64, notification GranolaNotification) (bool, error) {
	if s == nil || operatorID <= 0 || !granolaNotificationID.MatchString(notification.ID) || strings.TrimSpace(notification.Message) == "" || len([]rune(notification.Message)) > 6000 {
		return false, errors.New("invalid Granola notification")
	}
	if len(notification.TaskCandidates) > 5 {
		return false, errors.New("too many Granola task candidates")
	}
	for _, candidate := range notification.TaskCandidates {
		if strings.TrimSpace(candidate.NoteID) == "" || strings.TrimSpace(candidate.MeetingTitle) == "" || strings.TrimSpace(candidate.SourceSpan) == "" || !strings.Contains(candidate.SourceSummary, candidate.SourceSpan) || len([]rune(candidate.SourceSpan)) > 500 || (candidate.NoteURL != "" && !validGranolaSourceURL(candidate.NoteURL)) {
			return false, errors.New("invalid Granola task candidate")
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.pruneExpiredCandidatesLocked(operatorID, time.Now().UTC()); err != nil {
		return false, errors.New("cannot prune expired task candidates")
	}
	if notification.CreatedAt.IsZero() {
		notification.CreatedAt = time.Now().UTC()
	}
	notification.TelegramDelivered = !notification.TelegramRequired
	path := s.pendingPath(operatorID, notification.ID)
	if _, err := os.Stat(path); err == nil {
		return false, nil
	} else if !os.IsNotExist(err) {
		return false, errors.New("cannot inspect pending notification")
	}
	if _, err := os.Stat(s.deliveredPath(operatorID, notification.ID)); err == nil {
		return false, nil
	} else if !os.IsNotExist(err) {
		return false, errors.New("cannot inspect delivered notification")
	}
	if err := atomicGranolaNotificationJSON(path, notification); err != nil {
		return false, errors.New("cannot persist notification")
	}
	return true, nil
}

func (s *GranolaNotificationStore) Get(operatorID int64, id string) (GranolaNotification, bool, error) {
	if s == nil || operatorID <= 0 || !granolaNotificationID.MatchString(id) {
		return GranolaNotification{}, false, errors.New("invalid notification lookup")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, path := range []string{s.pendingPath(operatorID, id), s.candidatePath(operatorID, id)} {
		data, err := os.ReadFile(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return GranolaNotification{}, false, errors.New("cannot read stored notification")
		}
		var notification GranolaNotification
		if json.Unmarshal(data, &notification) != nil || notification.ID != id {
			return GranolaNotification{}, false, errors.New("invalid stored notification")
		}
		if path == s.candidatePath(operatorID, id) && (!notification.DeliveredAt.IsZero() && time.Since(notification.DeliveredAt) > granolaCandidateRetention) {
			_ = os.Remove(path)
			return GranolaNotification{}, false, nil
		}
		return notification, true, nil
	}
	return GranolaNotification{}, false, nil
}

func (s *GranolaNotificationStore) Pending(operatorID int64) ([]GranolaNotification, error) {
	if s == nil || operatorID <= 0 {
		return nil, errors.New("invalid notification operator")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	dir := s.operatorDir(operatorID, "pending")
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, errors.New("cannot read pending notifications")
	}
	var result []GranolaNotification
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			return nil, errors.New("cannot read notification")
		}
		var notification GranolaNotification
		if json.Unmarshal(data, &notification) != nil || !granolaNotificationID.MatchString(notification.ID) {
			return nil, errors.New("invalid pending notification")
		}
		if notification.WebUIDelivered {
			continue
		}
		if _, err := os.Stat(s.deliveredPath(operatorID, notification.ID)); err == nil {
			continue
		} else if !os.IsNotExist(err) {
			return nil, errors.New("cannot inspect notification acknowledgement")
		}
		result = append(result, notification)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].CreatedAt.Equal(result[j].CreatedAt) {
			return result[i].ID < result[j].ID
		}
		return result[i].CreatedAt.Before(result[j].CreatedAt)
	})
	return result, nil
}

func (s *GranolaNotificationStore) PendingTelegram(operatorID int64, id string) (GranolaNotification, bool, error) {
	if s == nil || operatorID <= 0 || !granolaNotificationID.MatchString(id) {
		return GranolaNotification{}, false, errors.New("invalid Telegram notification lookup")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := os.Stat(s.deliveredPath(operatorID, id)); err == nil {
		return GranolaNotification{}, false, nil
	} else if !os.IsNotExist(err) {
		return GranolaNotification{}, false, errors.New("cannot inspect Telegram notification completion")
	}
	data, err := os.ReadFile(s.pendingPath(operatorID, id))
	if os.IsNotExist(err) {
		return GranolaNotification{}, false, nil
	}
	if err != nil {
		return GranolaNotification{}, false, errors.New("cannot read pending Telegram notification")
	}
	var notification GranolaNotification
	if json.Unmarshal(data, &notification) != nil || notification.ID != id {
		return GranolaNotification{}, false, errors.New("invalid pending Telegram notification")
	}
	return notification, !notification.TelegramDelivered, nil
}

func (s *GranolaNotificationStore) MarkTelegramDelivered(operatorID int64, id string) error {
	if s == nil || operatorID <= 0 || !granolaNotificationID.MatchString(id) {
		return errors.New("invalid notification delivery")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	path := s.pendingPath(operatorID, id)
	data, err := os.ReadFile(path)
	if err != nil {
		return errors.New("notification is not pending")
	}
	var notification GranolaNotification
	if json.Unmarshal(data, &notification) != nil || notification.ID != id {
		return errors.New("invalid pending notification")
	}
	notification.TelegramDelivered = true
	if notification.WebUIDelivered {
		return s.completeLocked(operatorID, notification)
	}
	return atomicGranolaNotificationJSON(path, notification)
}

func (s *GranolaNotificationStore) Acknowledge(operatorID int64, ids []string) error {
	if s == nil || operatorID <= 0 {
		return errors.New("invalid notification operator")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, id := range ids {
		if !granolaNotificationID.MatchString(id) {
			return errors.New("invalid notification ID")
		}
		pendingPath := s.pendingPath(operatorID, id)
		if _, err := os.Stat(pendingPath); os.IsNotExist(err) {
			continue
		} else if err != nil {
			return errors.New("cannot inspect notification")
		}
		data, err := os.ReadFile(pendingPath)
		if err != nil {
			return errors.New("cannot read acknowledged notification")
		}
		var notification GranolaNotification
		if json.Unmarshal(data, &notification) != nil || notification.ID != id {
			return errors.New("invalid acknowledged notification")
		}
		notification.WebUIDelivered = true
		if notification.TelegramDelivered {
			if err := s.completeLocked(operatorID, notification); err != nil {
				return errors.New("cannot persist notification acknowledgement")
			}
		} else if err := atomicGranolaNotificationJSON(pendingPath, notification); err != nil {
			return errors.New("cannot persist notification acknowledgement")
		}
	}
	return nil
}

func (s *GranolaNotificationStore) completeLocked(operatorID int64, notification GranolaNotification) error {
	pendingPath := s.pendingPath(operatorID, notification.ID)
	notification.DeliveredAt = time.Now().UTC()
	if len(notification.TaskCandidates) > 0 {
		if err := atomicGranolaNotificationJSON(s.candidatePath(operatorID, notification.ID), notification); err != nil {
			return err
		}
	}
	if err := atomicGranolaNotificationJSON(s.deliveredPath(operatorID, notification.ID), map[string]any{"id": notification.ID, "delivered_at": notification.DeliveredAt}); err != nil {
		return err
	}
	if err := os.Remove(pendingPath); err != nil && !os.IsNotExist(err) {
		return err
	}
	return syncGranolaNotificationDir(filepath.Dir(pendingPath))
}

func atomicGranolaNotificationJSON(path string, value any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".granola-notification-*")
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
	return syncGranolaNotificationDir(filepath.Dir(path))
}

func syncGranolaNotificationDir(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	defer dir.Close()
	if err := dir.Sync(); err != nil && !errors.Is(err, os.ErrInvalid) {
		return fmt.Errorf("sync notification directory: %w", err)
	}
	return nil
}

func (s *GranolaNotificationStore) pendingPath(operatorID int64, id string) string {
	return filepath.Join(s.operatorDir(operatorID, "pending"), id+".json")
}

func (s *GranolaNotificationStore) deliveredPath(operatorID int64, id string) string {
	return filepath.Join(s.operatorDir(operatorID, "delivered"), id+".json")
}

func (s *GranolaNotificationStore) candidatePath(operatorID int64, id string) string {
	return filepath.Join(s.operatorDir(operatorID, "candidates"), id+".json")
}

func (s *GranolaNotificationStore) pruneExpiredCandidatesLocked(operatorID int64, now time.Time) error {
	dir := s.operatorDir(operatorID, "candidates")
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		var notification GranolaNotification
		if json.Unmarshal(data, &notification) != nil || notification.DeliveredAt.IsZero() || now.Sub(notification.DeliveredAt) <= granolaCandidateRetention {
			continue
		}
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

func (s *GranolaNotificationStore) operatorDir(operatorID int64, kind string) string {
	return filepath.Join(s.root, "operator-"+strconv.FormatInt(operatorID, 10), kind)
}

func (s *Service) handleGranolaNotification(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST required", http.StatusMethodNotAllowed)
		return
	}
	if s.GranolaNotificationToken == "" || s.GranolaNotifications == nil || s.GranolaOperatorID <= 0 || subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+s.GranolaNotificationToken)) != 1 {
		http.Error(w, "unauthorised", http.StatusUnauthorized)
		return
	}
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, granolaNotificationMaxBody))
	if err != nil {
		http.Error(w, "request too large", http.StatusRequestEntityTooLarge)
		return
	}
	var request GranolaNotificationRequest
	if json.Unmarshal(data, &request) != nil || request.OperatorID != s.GranolaOperatorID || (request.TelegramChatID != 0 && request.TelegramChatID != request.OperatorID) || !s.Allowed[request.OperatorID] {
		http.Error(w, "invalid operator notification", http.StatusForbidden)
		return
	}
	if !granolaNotificationID.MatchString(request.Notification.ID) || strings.TrimSpace(request.Notification.Message) == "" || len([]rune(request.Notification.Message)) > 6000 {
		http.Error(w, "invalid Granola notification", http.StatusBadRequest)
		return
	}
	if request.TelegramChatID > 0 && s.GranolaBot == nil {
		http.Error(w, "Telegram notification delivery unavailable", http.StatusServiceUnavailable)
		return
	}
	_, err = s.GranolaNotifications.Put(request.OperatorID, GranolaNotification{ID: request.Notification.ID, Message: request.Notification.Message, TaskCandidates: request.Notification.TaskCandidates, TelegramRequired: request.TelegramChatID > 0})
	if err != nil {
		http.Error(w, "notification could not be persisted", http.StatusServiceUnavailable)
		return
	}
	if request.TelegramChatID > 0 && s.GranolaBot != nil {
		pending, found, err := s.GranolaNotifications.PendingTelegram(request.OperatorID, request.Notification.ID)
		if err != nil {
			http.Error(w, "notification delivery unavailable", http.StatusServiceUnavailable)
			return
		}
		if found {
			ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
			err := s.GranolaBot.sendGranolaNotification(ctx, request.TelegramChatID, pending)
			cancel()
			if err != nil {
				http.Error(w, "Telegram notification delivery failed", http.StatusServiceUnavailable)
				return
			}
			if err := s.GranolaNotifications.MarkTelegramDelivered(request.OperatorID, pending.ID); err != nil {
				http.Error(w, "notification delivery state unavailable", http.StatusServiceUnavailable)
				return
			}
		}
	}
	w.WriteHeader(http.StatusAccepted)
}

func validGranolaSourceURL(value string) bool {
	parsed, err := url.Parse(strings.TrimSpace(value))
	return err == nil && parsed.Scheme == "https" && parsed.Host != "" && parsed.User == nil && parsed.RawQuery == "" && parsed.Fragment == ""
}

func (s *Service) granolaPendingMessage(operatorID int64) ([]GranolaNotification, string, error) {
	if s.GranolaNotifications == nil {
		return nil, "", nil
	}
	notifications, err := s.GranolaNotifications.Pending(operatorID)
	if err != nil {
		return nil, "", err
	}
	if len(notifications) == 0 {
		return nil, "", nil
	}
	remaining := len(notifications) - 3
	if len(notifications) > 3 {
		notifications = notifications[:3]
	}
	var lines []string
	for _, notification := range notifications {
		lines = append(lines, notification.Message)
		for index := range notification.TaskCandidates {
			lines = append(lines, fmt.Sprintf("<!-- donna-jira-candidate:%s:%d -->", notification.ID, index))
		}
	}
	if remaining > 0 {
		lines = append(lines, "Quedan "+strconv.Itoa(remaining)+" seguimientos por mostrar.")
	}
	return notifications, strings.Join(lines, "\n\n---\n\n"), nil
}

func (s *Service) acknowledgeGranola(operatorID int64, notifications []GranolaNotification) error {
	if s.GranolaNotifications == nil || len(notifications) == 0 {
		return nil
	}
	ids := make([]string, 0, len(notifications))
	for _, notification := range notifications {
		ids = append(ids, notification.ID)
	}
	return s.GranolaNotifications.Acknowledge(operatorID, ids)
}
