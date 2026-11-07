package donnagranola

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSecretarySinkPostsAuthenticatedNotification(t *testing.T) {
	var got secretaryNotificationRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/internal/granola-notifications" || r.Method != http.MethodPost || r.Header.Get("Authorization") != "Bearer 12345678901234567890123456789012" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.String())
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Error(err)
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()
	// httptest binds to loopback, so it satisfies the sink's private-service rule.
	sink, err := NewSecretaryNotificationSink(server.URL, "12345678901234567890123456789012")
	if err != nil {
		t.Fatal(err)
	}
	err = sink.Deliver(context.Background(), OperatorContext{OperatorID: 42, TelegramChatID: 42}, Notification{ID: "evt_1", Message: "Granola · PMI"})
	if err != nil {
		t.Fatal(err)
	}
	if got.OperatorID != 42 || got.TelegramChatID != 42 || got.Notification.ID != "evt_1" || got.Notification.Message != "Granola · PMI" {
		t.Fatalf("request = %#v", got)
	}
}

func TestSecretarySinkMarksServerFailuresRetryable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "temporarily unavailable", http.StatusServiceUnavailable)
	}))
	defer server.Close()
	sink, err := NewSecretaryNotificationSink(server.URL, "12345678901234567890123456789012")
	if err != nil {
		t.Fatal(err)
	}
	err = sink.Deliver(context.Background(), OperatorContext{OperatorID: 42}, Notification{ID: "evt_1", Message: "follow-up"})
	if !errors.Is(err, ErrTemporary) {
		t.Fatalf("expected retryable error, got %v", err)
	}
}

func TestSecretarySinkRejectsNonLoopbackEndpoint(t *testing.T) {
	if _, err := NewSecretaryNotificationSink("https://example.org", "12345678901234567890123456789012"); err == nil {
		t.Fatal("expected remote endpoint rejection")
	}
}
