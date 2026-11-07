package donnagranola

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type memoryQueue struct {
	events []GranolaEvent
	ids    map[string]bool
}

func (q *memoryQueue) Enqueue(_ context.Context, event GranolaEvent) (bool, error) {
	if q.ids == nil {
		q.ids = map[string]bool{}
	}
	if q.ids[event.EventID] {
		return false, nil
	}
	q.ids[event.EventID] = true
	q.events = append(q.events, event)
	return true, nil
}

func TestWebhookAcceptsSignedPersonalNoteGenerated(t *testing.T) {
	now := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	queue := &memoryQueue{}
	handler := NewWebhookHandler(queue, StaticSecret("whsec_"+base64.StdEncoding.EncodeToString([]byte("signing secret long"))), func() time.Time { return now })
	body := []byte(`{"event_id":"evt_123","event_type":"note.generated","note_id":"not_1d3tmYTlCICgjy","occurred_at":"2026-10-28T11:59:00Z"}`)
	req := signedRequest(t, body, "evt_123", now, "signing secret long")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("status = %d, body = %q", recorder.Code, recorder.Body.String())
	}
	if len(queue.events) != 1 || queue.events[0].Scope != ScopePersonal || queue.events[0].NoteID != "not_1d3tmYTlCICgjy" {
		t.Fatalf("queued events = %#v", queue.events)
	}
}

func TestWebhookRejectsInvalidSignatureWithoutQueueing(t *testing.T) {
	now := time.Now().UTC()
	queue := &memoryQueue{}
	handler := NewWebhookHandler(queue, StaticSecret("whsec_"+base64.StdEncoding.EncodeToString([]byte("signing secret long"))), func() time.Time { return now })
	req := signedRequest(t, []byte(`{"event_id":"evt_1","event_type":"note.generated","note_id":"not_1d3tmYTlCICgjy"}`), "evt_1", now, "wrong")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusUnauthorized || len(queue.events) != 0 {
		t.Fatalf("status=%d queued=%d", recorder.Code, len(queue.events))
	}
}

func TestWebhookRejectsStaleTimestampWithoutQueueing(t *testing.T) {
	now := time.Now().UTC()
	queue := &memoryQueue{}
	handler := NewWebhookHandler(queue, StaticSecret("whsec_"+base64.StdEncoding.EncodeToString([]byte("signing secret long"))), func() time.Time { return now })
	req := signedRequest(t, []byte(`{"event_id":"evt_1","event_type":"note.generated","note_id":"not_1d3tmYTlCICgjy"}`), "evt_1", now.Add(-6*time.Minute), "signing secret long")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusUnauthorized || len(queue.events) != 0 {
		t.Fatalf("status=%d queued=%d", recorder.Code, len(queue.events))
	}
}

func TestWebhookRejectsNonGeneratedAndPayloadScopeWithoutQueueing(t *testing.T) {
	now := time.Now().UTC()
	queue := &memoryQueue{}
	handler := NewWebhookHandler(queue, StaticSecret("whsec_"+base64.StdEncoding.EncodeToString([]byte("signing secret long"))), func() time.Time { return now })
	for _, body := range []string{
		`{"event_id":"evt_1","event_type":"note.edited","note_id":"not_1d3tmYTlCICgjy"}`,
		`{"event_id":"evt_1","event_type":"note.generated","note_id":"not_1d3tmYTlCICgjy","scope":"workspace"}`,
	} {
		req := signedRequest(t, []byte(body), "evt_1", now, "signing secret long")
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, req)
		if recorder.Code < 400 || len(queue.events) != 0 {
			t.Fatalf("body=%s status=%d queued=%d", body, recorder.Code, len(queue.events))
		}
	}
}

func TestWebhookDeduplicatesEventID(t *testing.T) {
	now := time.Now().UTC()
	queue := &memoryQueue{}
	handler := NewWebhookHandler(queue, StaticSecret("whsec_"+base64.StdEncoding.EncodeToString([]byte("signing secret long"))), func() time.Time { return now })
	body := []byte(`{"event_id":"evt_1","event_type":"note.generated","note_id":"not_1d3tmYTlCICgjy"}`)
	for i := 0; i < 2; i++ {
		req := signedRequest(t, body, "evt_1", now, "signing secret long")
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, req)
		if recorder.Code != http.StatusAccepted {
			t.Fatalf("duplicate %d status=%d body=%q", i, recorder.Code, recorder.Body.String())
		}
	}
	if len(queue.events) != 1 {
		t.Fatalf("queued %d events", len(queue.events))
	}
}

func TestWebhookRejectsMalformedBodyAndTooLargeBody(t *testing.T) {
	now := time.Now().UTC()
	queue := &memoryQueue{}
	handler := NewWebhookHandler(queue, StaticSecret("whsec_"+base64.StdEncoding.EncodeToString([]byte("signing secret long"))), func() time.Time { return now })
	for _, body := range [][]byte{[]byte("{"), make([]byte, maxWebhookBody+1)} {
		req := signedRequest(t, body, "evt_1", now, "signing secret long")
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, req)
		if recorder.Code < 400 || len(queue.events) != 0 {
			t.Fatalf("size=%d status=%d queued=%d", len(body), recorder.Code, len(queue.events))
		}
	}
}

func signedRequest(t *testing.T, body []byte, id string, at time.Time, key string) *http.Request {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/webhooks/granola", bytes.NewReader(body))
	timestamp := fmt.Sprint(at.Unix())
	mac := hmac.New(sha256.New, []byte(key))
	_, _ = mac.Write([]byte(id + "." + timestamp + "."))
	_, _ = mac.Write(body)
	request.Header.Set("webhook-id", id)
	request.Header.Set("webhook-timestamp", timestamp)
	request.Header.Set("webhook-signature", "v1,"+base64.StdEncoding.EncodeToString(mac.Sum(nil)))
	return request
}
