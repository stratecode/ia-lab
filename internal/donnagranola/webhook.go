package donnagranola

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	EventNoteGenerated = "note.generated"
	ScopePersonal      = "personal"
	maxWebhookBody     = 32 << 10
	webhookClockSkew   = 5 * time.Minute
)

var eventIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,200}$`)
var noteIDPattern = regexp.MustCompile(`^[Nn][Oo][Tt]_[A-Za-z0-9]{14}$`)

type GranolaEvent struct {
	EventID    string    `json:"event_id"`
	NoteID     string    `json:"note_id"`
	EventType  string    `json:"event_type"`
	Scope      string    `json:"scope"`
	ReceivedAt time.Time `json:"received_at"`
	Attempts   int       `json:"attempts,omitempty"`
}

type EventQueue interface {
	Enqueue(context.Context, GranolaEvent) (bool, error)
}

type SecretProvider interface {
	WebhookSecret() string
}

type staticSecret string

func StaticSecret(value string) SecretProvider { return staticSecret(value) }
func (s staticSecret) WebhookSecret() string   { return string(s) }

type Clock func() time.Time

type webhookPayload struct {
	EventID    string          `json:"event_id"`
	EventType  string          `json:"event_type"`
	NoteID     string          `json:"note_id"`
	OccurredAt string          `json:"occurred_at"`
	Scope      json.RawMessage `json:"scope"`
}

func VerifySignature(rawBody []byte, messageID, signature, timestamp, secret string, now time.Time) error {
	if len(rawBody) > maxWebhookBody || strings.TrimSpace(secret) == "" {
		return errors.New("invalid webhook signature configuration")
	}
	seconds, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil {
		return errors.New("invalid webhook timestamp")
	}
	signedAt := time.Unix(seconds, 0)
	if signedAt.Before(now.Add(-webhookClockSkew)) || signedAt.After(now.Add(webhookClockSkew)) {
		return errors.New("stale webhook timestamp")
	}
	key, err := decodeSigningSecret(secret)
	if err != nil {
		return errors.New("invalid webhook signing secret")
	}
	return verifySignatureWithID(rawBody, messageID, signature, timestamp, key)
}

func verifySignatureWithID(rawBody []byte, messageID, signature, timestamp string, key []byte) error {
	if !eventIDPattern.MatchString(messageID) {
		return errors.New("invalid webhook message ID")
	}
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(messageID + "." + timestamp + "."))
	_, _ = mac.Write(rawBody)
	want := mac.Sum(nil)
	for _, part := range strings.Fields(signature) {
		algorithm, candidate, ok := strings.Cut(part, ",")
		if !ok || algorithm != "v1" {
			continue
		}
		got, err := base64.StdEncoding.DecodeString(candidate)
		if err == nil && hmac.Equal(got, want) {
			return nil
		}
	}
	return errors.New("webhook signature mismatch")
}

func decodeSigningSecret(secret string) ([]byte, error) {
	encoded := strings.TrimSpace(secret)
	if strings.HasPrefix(encoded, "whsec_") {
		encoded = strings.TrimPrefix(encoded, "whsec_")
	}
	key, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || len(key) < 16 {
		return nil, errors.New("invalid signing secret")
	}
	return key, nil
}

func ValidateSigningSecret(secret string) error {
	_, err := decodeSigningSecret(secret)
	return err
}

func NewWebhookHandler(queue EventQueue, secrets SecretProvider, clock Clock) http.Handler {
	if clock == nil {
		clock = time.Now
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			if r.Method != http.MethodGet {
				http.Error(w, "GET required", http.StatusMethodNotAllowed)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"status":"ok"}`)
			return
		}
		if r.URL.Path != "/webhooks/granola" {
			http.NotFound(w, r)
			return
		}
		if r.Method != http.MethodPost {
			http.Error(w, "POST required", http.StatusMethodNotAllowed)
			return
		}
		if queue == nil || secrets == nil || strings.TrimSpace(secrets.WebhookSecret()) == "" {
			http.Error(w, "webhook unavailable", http.StatusServiceUnavailable)
			return
		}
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxWebhookBody))
		if err != nil {
			http.Error(w, "request too large", http.StatusRequestEntityTooLarge)
			return
		}
		messageID := strings.TrimSpace(r.Header.Get("webhook-id"))
		timestamp := strings.TrimSpace(r.Header.Get("webhook-timestamp"))
		signature := r.Header.Get("webhook-signature")
		if !eventIDPattern.MatchString(messageID) {
			http.Error(w, "invalid webhook ID", http.StatusBadRequest)
			return
		}
		if err := VerifySignature(body, messageID, signature, timestamp, secrets.WebhookSecret(), clock()); err != nil {
			http.Error(w, "invalid webhook signature", http.StatusUnauthorized)
			return
		}
		var payload webhookPayload
		if err := json.Unmarshal(body, &payload); err != nil {
			http.Error(w, "invalid webhook payload", http.StatusBadRequest)
			return
		}
		if len(payload.Scope) != 0 {
			http.Error(w, "webhook scope is configured at enrollment", http.StatusBadRequest)
			return
		}
		if payload.EventID != messageID || payload.EventType != EventNoteGenerated || !eventIDPattern.MatchString(payload.EventID) || !noteIDPattern.MatchString(payload.NoteID) {
			http.Error(w, "unsupported webhook event", http.StatusBadRequest)
			return
		}
		if payload.OccurredAt != "" {
			if _, err := time.Parse(time.RFC3339, payload.OccurredAt); err != nil {
				http.Error(w, "invalid event time", http.StatusBadRequest)
				return
			}
		}
		_, err = queue.Enqueue(r.Context(), GranolaEvent{
			EventID: payload.EventID, NoteID: payload.NoteID, EventType: payload.EventType,
			Scope: ScopePersonal, ReceivedAt: clock().UTC(),
		})
		if err != nil {
			http.Error(w, "event not persisted", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusAccepted)
	})
}
