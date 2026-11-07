package secretary

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

type failingNotificationWriter struct{ header http.Header }

func (w *failingNotificationWriter) Header() http.Header {
	if w.header == nil {
		w.header = make(http.Header)
	}
	return w.header
}
func (*failingNotificationWriter) WriteHeader(int) {}
func (*failingNotificationWriter) Write([]byte) (int, error) {
	return 0, errors.New("client disconnected")
}

func TestGranolaNotificationStoreScopesDeduplicatesAndSurvivesRestart(t *testing.T) {
	root := t.TempDir()
	store, err := OpenGranolaNotificationStore(root)
	if err != nil {
		t.Fatal(err)
	}
	notification := GranolaNotification{ID: "evt_1", Message: "Granola · PMI"}
	if created, err := store.Put(7, notification); err != nil || !created {
		t.Fatalf("Put = %v, %v", created, err)
	}
	store, err = OpenGranolaNotificationStore(root)
	if err != nil {
		t.Fatal(err)
	}
	if created, err := store.Put(7, notification); err != nil || created {
		t.Fatalf("duplicate Put = %v, %v", created, err)
	}
	seven, err := store.Pending(7)
	if err != nil || len(seven) != 1 {
		t.Fatalf("operator 7 pending = %#v, %v", seven, err)
	}
	eight, err := store.Pending(8)
	if err != nil || len(eight) != 0 {
		t.Fatalf("operator 8 pending = %#v, %v", eight, err)
	}
	if err := store.Acknowledge(7, []string{"evt_1"}); err != nil {
		t.Fatal(err)
	}
	if pending, err := store.Pending(7); err != nil || len(pending) != 0 {
		t.Fatalf("pending after acknowledgement = %#v, %v", pending, err)
	}
	if created, err := store.Put(7, notification); err != nil || created {
		t.Fatalf("acknowledged event was recreated: %v, %v", created, err)
	}
}

func TestGranolaTaskCandidateSurvivesDeliveryAndOpenWebUIReceivesActionMarker(t *testing.T) {
	store, err := OpenGranolaNotificationStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	notification := GranolaNotification{ID: "evt_task", Message: "Granola · PMI", TaskCandidates: []GranolaTaskCandidate{{NoteID: "note-1", MeetingTitle: "Revisión", SourceSpan: "Preparar informe", SourceSummary: "Preparar informe antes del viernes"}}}
	if _, err := store.Put(7, notification); err != nil {
		t.Fatal(err)
	}
	if err := store.Acknowledge(7, []string{notification.ID}); err != nil {
		t.Fatal(err)
	}
	loaded, found, err := store.Get(7, notification.ID)
	if err != nil || !found || len(loaded.TaskCandidates) != 1 || loaded.TaskCandidates[0].SourceSpan != "Preparar informe" {
		t.Fatalf("delivered candidate unavailable: %#v found=%v err=%v", loaded, found, err)
	}
	// Use a fresh store to verify the action marker before acknowledgement.
	pending, err := OpenGranolaNotificationStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pending.Put(7, notification); err != nil {
		t.Fatal(err)
	}
	_, rendered, err := (&Service{GranolaNotifications: pending}).granolaPendingMessage(7)
	if err != nil || !strings.Contains(rendered, "<!-- donna-jira-candidate:evt_task:0 -->") {
		t.Fatalf("candidate action marker missing: %q err=%v", rendered, err)
	}
}

func TestGranolaTaskCandidateExpiresAfterThirtyDays(t *testing.T) {
	root := t.TempDir()
	store, err := OpenGranolaNotificationStore(root)
	if err != nil {
		t.Fatal(err)
	}
	notification := GranolaNotification{ID: "evt_expired", Message: "Granola · PMI", TaskCandidates: []GranolaTaskCandidate{{NoteID: "note-1", MeetingTitle: "Revisión", SourceSpan: "Preparar informe", SourceSummary: "Preparar informe antes del viernes"}}}
	if _, err := store.Put(7, notification); err != nil {
		t.Fatal(err)
	}
	if err := store.Acknowledge(7, []string{notification.ID}); err != nil {
		t.Fatal(err)
	}
	notification.DeliveredAt = time.Now().UTC().Add(-granolaCandidateRetention - time.Minute)
	if err := atomicGranolaNotificationJSON(store.candidatePath(7, notification.ID), notification); err != nil {
		t.Fatal(err)
	}
	store, err = OpenGranolaNotificationStore(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, found, err := store.Get(7, notification.ID); err != nil || found {
		t.Fatalf("expired candidate remained available: found=%v err=%v", found, err)
	}
	if _, err := os.Stat(store.candidatePath(7, notification.ID)); !os.IsNotExist(err) {
		t.Fatalf("expired candidate record was not pruned: err=%v", err)
	}
}

func TestOpenWebUIGranolaActionStartsReviewableJiraProposal(t *testing.T) {
	modelCalls := 0
	modelServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		modelCalls++
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"model reply"}}]}`)
	}))
	defer modelServer.Close()
	actionServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/v1/actions/draft" {
			w.WriteHeader(http.StatusCreated)
		}
		switch r.URL.Path {
		case "/v1/config/options":
			_, _ = io.WriteString(w, `{"project_keys":["LAB"],"issue_types":["Task"]}`)
		case "/v1/tasks/matches":
			_, _ = io.WriteString(w, `{"matches":[]}`)
		case "/v1/actions/draft":
			_, _ = io.WriteString(w, `{"action_id":"draft-1","kind":"create","project_key":"LAB","issue_type":"Task","summary":"Preparar informe","description":"Fuente Granola: Preparar informe","source_span":"Preparar informe","note_url":"https://granola.ai/notes/note-1","payload_digest":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","expires_at":"2026-11-01T12:10:00Z"}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer actionServer.Close()

	s := New(modelServer.URL, "key", "", t.TempDir(), map[int64]bool{17: true})
	s.JiraActions = &JiraActionsClient{BaseURL: actionServer.URL, Token: "separate-service-token-at-least-32-bytes"}
	s.GranolaNotifications, _ = OpenGranolaNotificationStore(s.StateDir)
	notification := GranolaNotification{ID: "evt_task", Message: "Granola · PMI", TaskCandidates: []GranolaTaskCandidate{{NoteID: "note-1", MeetingTitle: "Revisión", SourceSpan: "Preparar informe", SourceSummary: "Preparar informe antes del viernes"}}}
	if _, err := s.GranolaNotifications.Put(17, notification); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/internal/jira-proposals/start", strings.NewReader(`{"notification_id":"evt_task","candidate_index":0}`))
	request.Header.Set("Authorization", "Bearer separate-service-token-at-least-32-bytes")
	request.Header.Set("X-Donna-Operator-ID", strconv.FormatInt(17, 10))
	request.Header.Set("X-Donna-Channel", "openwebui")
	request.Header.Set("X-Donna-Conversation-ID", "chat-1")
	request.Header.Set("X-OpenWebUI-User-Id", "admin")
	request.Header.Set("X-OpenWebUI-User-Role", "admin")
	result := httptest.NewRecorder()
	s.Handler().ServeHTTP(result, request)
	var response struct {
		Message string `json:"message"`
	}
	if json.Unmarshal(result.Body.Bytes(), &response) != nil || result.Code != http.StatusOK || !strings.Contains(response.Message, "falta el enlace") {
		t.Fatalf("start proposal status=%d body=%s", result.Code, result.Body.String())
	}

	chatPayload, err := json.Marshal(map[string]any{"model": "main", "stream": false, "messages": []map[string]string{{"role": "user", "content": "https://granola.ai/notes/note-1"}}})
	if err != nil {
		t.Fatal(err)
	}
	chat := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(string(chatPayload)))
	chat.Header.Set("Authorization", "Bearer key")
	chat.Header.Set("X-OpenWebUI-User-Id", "admin")
	chat.Header.Set("X-OpenWebUI-User-Role", "admin")
	chat.Header.Set("X-OpenWebUI-Chat-Id", "chat-1")
	chatResult := httptest.NewRecorder()
	s.Handler().ServeHTTP(chatResult, chat)
	var completion struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if json.Unmarshal(chatResult.Body.Bytes(), &completion) != nil || chatResult.Code != http.StatusOK || len(completion.Choices) != 1 || !strings.Contains(completion.Choices[0].Message.Content, "<!-- donna-jira-action:draft-1:") || modelCalls != 0 {
		t.Fatalf("clarification did not return a reviewable preview: status=%d body=%s", chatResult.Code, chatResult.Body.String())
	}
}

func TestGranolaNotificationRetainsTelegramRetryUntilBothChannelsComplete(t *testing.T) {
	store, err := OpenGranolaNotificationStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	n := GranolaNotification{ID: "evt_dual", Message: "Granola · PMI", TelegramRequired: true}
	if _, err := store.Put(7, n); err != nil {
		t.Fatal(err)
	}
	if err := store.Acknowledge(7, []string{n.ID}); err != nil {
		t.Fatal(err)
	}
	if pending, err := store.Pending(7); err != nil || len(pending) != 0 {
		t.Fatalf("Open WebUI ack should hide item from next chat: %#v %v", pending, err)
	}
	telegramPending, ok, err := store.PendingTelegram(7, n.ID)
	if err != nil || !ok || telegramPending.TelegramDelivered {
		t.Fatalf("Telegram retry was lost: %#v ok=%v err=%v", telegramPending, ok, err)
	}
	if err := store.MarkTelegramDelivered(7, n.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := store.PendingTelegram(7, n.ID); err != nil || ok {
		t.Fatalf("completed notification is still pending for Telegram: ok=%v err=%v", ok, err)
	}
	if created, err := store.Put(7, n); err != nil || created {
		t.Fatalf("fully delivered notification was recreated: created=%v err=%v", created, err)
	}
}

func TestPendingTelegramHonorsDurableCompletionMarkerAfterInterruptedCleanup(t *testing.T) {
	store, err := OpenGranolaNotificationStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	n := GranolaNotification{ID: "evt_cleanup", Message: "Granola · PMI", TelegramRequired: true}
	if _, err := store.Put(7, n); err != nil {
		t.Fatal(err)
	}
	if err := atomicGranolaNotificationJSON(store.deliveredPath(7, n.ID), map[string]string{"id": n.ID}); err != nil {
		t.Fatal(err)
	}
	pending, found, err := store.PendingTelegram(7, n.ID)
	if err != nil || found || pending.ID != "" {
		t.Fatalf("stale pending record overrode completion marker: %#v found=%v err=%v", pending, found, err)
	}
}

func TestGranolaNotificationEndpointRequiresPrivateTokenAndConfiguredOperator(t *testing.T) {
	s := New("http://127.0.0.1:9", "key", "instructions", t.TempDir(), map[int64]bool{7: true})
	s.GranolaNotificationToken = "01234567890123456789012345678901"
	s.GranolaOperatorID = 7
	s.GranolaNotifications, _ = OpenGranolaNotificationStore(s.StateDir)
	body, _ := json.Marshal(GranolaNotificationRequest{OperatorID: 8, TelegramChatID: 8, Notification: GranolaNotification{ID: "evt_1", Message: "private"}})
	request := httptest.NewRequest(http.MethodPost, "/internal/granola-notifications", strings.NewReader(string(body)))
	request.Header.Set("Authorization", "Bearer "+s.GranolaNotificationToken)
	result := httptest.NewRecorder()
	s.Handler().ServeHTTP(result, request)
	if result.Code != http.StatusForbidden {
		t.Fatalf("mismatched operator status = %d", result.Code)
	}
	body, _ = json.Marshal(GranolaNotificationRequest{OperatorID: 7, Notification: GranolaNotification{ID: "evt_1", Message: "private"}})
	request = httptest.NewRequest(http.MethodPost, "/internal/granola-notifications", strings.NewReader(string(body)))
	result = httptest.NewRecorder()
	s.Handler().ServeHTTP(result, request)
	if result.Code != http.StatusUnauthorized {
		t.Fatalf("missing token status = %d", result.Code)
	}
	request = httptest.NewRequest(http.MethodPost, "/internal/granola-notifications", strings.NewReader(string(body)))
	request.Header.Set("Authorization", "Bearer "+s.GranolaNotificationToken)
	result = httptest.NewRecorder()
	s.Handler().ServeHTTP(result, request)
	if result.Code != http.StatusAccepted {
		t.Fatalf("valid notification status = %d body=%s", result.Code, result.Body.String())
	}
}

func TestGranolaNotificationPersistenceFailureIsRetryable(t *testing.T) {
	root := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(root, []byte("block parent directory"), 0600); err != nil {
		t.Fatal(err)
	}
	s := New("http://127.0.0.1:9", "key", "instructions", t.TempDir(), map[int64]bool{7: true})
	s.GranolaNotificationToken = "01234567890123456789012345678901"
	s.GranolaOperatorID = 7
	s.GranolaNotifications = &GranolaNotificationStore{root: root}
	body, _ := json.Marshal(GranolaNotificationRequest{OperatorID: 7, Notification: GranolaNotification{ID: "evt_disk", Message: "Granola · PMI"}})
	request := httptest.NewRequest(http.MethodPost, "/internal/granola-notifications", strings.NewReader(string(body)))
	request.Header.Set("Authorization", "Bearer "+s.GranolaNotificationToken)
	result := httptest.NewRecorder()
	s.Handler().ServeHTTP(result, request)
	if result.Code != http.StatusServiceUnavailable {
		t.Fatalf("persistence failure status = %d, want retryable 503", result.Code)
	}
}

func TestGranolaNotificationPushesOnceToAuthorizedPrivateTelegramChat(t *testing.T) {
	sends := 0
	telegram := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/bottoken/sendMessage" {
			t.Errorf("Telegram path = %q", r.URL.Path)
		}
		var payload struct {
			ChatID int64  `json:"chat_id"`
			Text   string `json:"text"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
		}
		if payload.ChatID != 7 || !strings.Contains(payload.Text, "Granola") {
			t.Errorf("Telegram payload = %#v", payload)
		}
		sends++
		_, _ = w.Write([]byte(`{"ok":true,"result":{}}`))
	}))
	defer telegram.Close()
	root := t.TempDir()
	s := New("http://127.0.0.1:9", "key", "instructions", root, map[int64]bool{7: true})
	s.GranolaNotificationToken = "01234567890123456789012345678901"
	s.GranolaOperatorID = 7
	s.GranolaNotifications, _ = OpenGranolaNotificationStore(root)
	s.GranolaBot = &Bot{Base: telegram.URL, Token: "token", Client: telegram.Client()}
	payload, _ := json.Marshal(GranolaNotificationRequest{OperatorID: 7, TelegramChatID: 7, Notification: GranolaNotification{ID: "evt_telegram", Message: "Granola · PMI"}})
	for i := 0; i < 2; i++ {
		request := httptest.NewRequest(http.MethodPost, "/internal/granola-notifications", strings.NewReader(string(payload)))
		request.Header.Set("Authorization", "Bearer "+s.GranolaNotificationToken)
		result := httptest.NewRecorder()
		s.Handler().ServeHTTP(result, request)
		if result.Code != http.StatusAccepted {
			t.Fatalf("delivery %d status=%d body=%s", i, result.Code, result.Body.String())
		}
	}
	if sends != 1 {
		t.Fatalf("Telegram sends = %d", sends)
	}
}

func TestGranolaNotificationTelegramRetrySurvivesOpenWebUIAcknowledgement(t *testing.T) {
	sends := 0
	shouldFail := true
	telegram := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		sends++
		if shouldFail {
			http.Error(w, "temporary Telegram failure", http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte(`{"ok":true,"result":{}}`))
	}))
	defer telegram.Close()
	root := t.TempDir()
	s := New("http://127.0.0.1:9", "key", "instructions", root, map[int64]bool{7: true})
	s.GranolaNotificationToken = "01234567890123456789012345678901"
	s.GranolaOperatorID = 7
	s.GranolaNotifications, _ = OpenGranolaNotificationStore(root)
	s.GranolaBot = &Bot{Base: telegram.URL, Token: "token", Client: telegram.Client()}
	payload, _ := json.Marshal(GranolaNotificationRequest{OperatorID: 7, TelegramChatID: 7, Notification: GranolaNotification{ID: "evt_retry_after_webui", Message: "Granola · PMI"}})
	post := func() *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodPost, "/internal/granola-notifications", strings.NewReader(string(payload)))
		request.Header.Set("Authorization", "Bearer "+s.GranolaNotificationToken)
		result := httptest.NewRecorder()
		s.Handler().ServeHTTP(result, request)
		return result
	}
	if result := post(); result.Code != http.StatusServiceUnavailable {
		t.Fatalf("first delivery status = %d", result.Code)
	}
	if err := s.GranolaNotifications.Acknowledge(7, []string{"evt_retry_after_webui"}); err != nil {
		t.Fatal(err)
	}
	shouldFail = false
	if result := post(); result.Code != http.StatusAccepted {
		t.Fatalf("retry delivery status = %d body=%s", result.Code, result.Body.String())
	}
	if sends != 2 {
		t.Fatalf("Telegram sends = %d, want failed attempt plus retry", sends)
	}
	if pending, err := s.GranolaNotifications.Pending(7); err != nil || len(pending) != 0 {
		t.Fatalf("fully delivered notification remains pending: %#v err=%v", pending, err)
	}
}

func TestOpenWebUIReceivesOnlyItsPendingGranolaNotification(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"respuesta breve"}}]}`))
	}))
	defer upstream.Close()
	s := New(upstream.URL, "key", "instructions", t.TempDir(), map[int64]bool{7: true})
	s.GranolaNotifications, _ = OpenGranolaNotificationStore(s.StateDir)
	if _, err := s.GranolaNotifications.Put(7, GranolaNotification{ID: "evt_7", Message: "Granola · PMI: prepara el informe."}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GranolaNotifications.Put(8, GranolaNotification{ID: "evt_8", Message: "NO DEBE VERSE"}); err != nil {
		t.Fatal(err)
	}
	result := performOpenWebUIChat(t, s, "hola", false)
	if !strings.Contains(result.Body.String(), "Granola · PMI: prepara el informe.") || strings.Contains(result.Body.String(), "NO DEBE VERSE") {
		t.Fatalf("unexpected Open WebUI response: %s", result.Body.String())
	}
	if pending, err := s.GranolaNotifications.Pending(7); err != nil || len(pending) != 0 {
		t.Fatalf("pending after conversation = %#v, %v", pending, err)
	}
	if pending, err := s.GranolaNotifications.Pending(8); err != nil || len(pending) != 1 {
		t.Fatalf("other operator's pending was altered = %#v, %v", pending, err)
	}
}

func TestOpenWebUIDoesNotAcknowledgeGranolaNotificationWhenResponseWriteFails(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"respuesta breve"}}]}`))
	}))
	defer upstream.Close()
	s := New(upstream.URL, "key", "instructions", t.TempDir(), map[int64]bool{7: true})
	s.GranolaNotifications, _ = OpenGranolaNotificationStore(s.StateDir)
	if _, err := s.GranolaNotifications.Put(7, GranolaNotification{ID: "evt_disconnect", Message: "Granola · PMI"}); err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(map[string]any{"model": "main", "stream": false, "messages": []map[string]string{{"role": "user", "content": "hola"}}})
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(string(payload)))
	request.Header.Set("Authorization", "Bearer key")
	request.Header.Set("X-OpenWebUI-User-Id", "owner")
	request.Header.Set("X-OpenWebUI-User-Role", "admin")
	request.Header.Set("X-OpenWebUI-Chat-Id", "disconnect-case")
	s.Handler().ServeHTTP(&failingNotificationWriter{}, request)
	pending, err := s.GranolaNotifications.Pending(7)
	if err != nil || len(pending) != 1 {
		t.Fatalf("notification lost after client disconnect: pending=%#v err=%v", pending, err)
	}
}
