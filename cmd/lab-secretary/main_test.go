package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/stratecode/lab/internal/secretary"
)

func TestParseTelegramMode(t *testing.T) {
	for input, want := range map[string]string{
		"":         "polling",
		"polling":  "polling",
		"webhook":  "webhook",
		"disabled": "disabled",
	} {
		got, err := parseTelegramMode(input)
		if err != nil || string(got) != want {
			t.Errorf("parseTelegramMode(%q) = %q, %v; want %q", input, got, err, want)
		}
	}
	if _, err := parseTelegramMode("both"); err == nil {
		t.Fatal("invalid Telegram mode was accepted")
	}
}

func TestParseProgressDelay(t *testing.T) {
	for input, want := range map[string]time.Duration{
		"":   30 * time.Second,
		"1":  time.Second,
		"30": 30 * time.Second,
	} {
		got, err := parseProgressDelay(input)
		if err != nil || got != want {
			t.Errorf("parseProgressDelay(%q) = %s, %v; want %s", input, got, err, want)
		}
	}
	for _, input := range []string{"0", "121", "fast"} {
		if _, err := parseProgressDelay(input); err == nil {
			t.Errorf("parseProgressDelay(%q) accepted an invalid threshold", input)
		}
	}
}

func TestParseLearningRetentionDefaultsAndBounds(t *testing.T) {
	got, err := parseLearningRetention("", "")
	if err != nil || got.TraceDays != 90 || got.CandidateDays != 30 {
		t.Fatalf("default retention = %+v, %v", got, err)
	}
	got, err = parseLearningRetention("120", "14")
	if err != nil || got.TraceDays != 120 || got.CandidateDays != 14 {
		t.Fatalf("configured retention = %+v, %v", got, err)
	}
	for _, invalid := range []string{"0", "3651", "bad"} {
		if _, err := parseLearningRetention(invalid, "30"); err == nil {
			t.Errorf("parseLearningRetention(%q) accepted an invalid trace retention", invalid)
		}
		if _, err := parseLearningRetention("90", invalid); err == nil {
			t.Errorf("parseLearningRetention(candidate=%q) accepted invalid retention", invalid)
		}
	}
}

func TestWebhookModeDoesNotStartPolling(t *testing.T) {
	telegramCalls := 0
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		telegramCalls++
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer api.Close()
	queue, err := secretary.OpenTelegramUpdateQueue(filepath.Join(t.TempDir(), "state"))
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	bot := &secretary.Bot{Base: api.URL, Token: "secret", Client: api.Client(), Service: secretary.New("", "", "", t.TempDir(), map[int64]bool{7: true})}
	run, err := configureTelegram("webhook", mux, bot, queue, "webhook-secret")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := run(ctx); err != nil {
		t.Fatal(err)
	}
	if telegramCalls != 0 {
		t.Fatalf("webhook mode made %d Telegram API calls; it must not start polling", telegramCalls)
	}
}

func TestDisabledModeStartsNoTelegramConsumer(t *testing.T) {
	telegramCalls := 0
	api := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { telegramCalls++ }))
	defer api.Close()
	mux := http.NewServeMux()
	bot := &secretary.Bot{Base: api.URL, Token: "secret", Client: api.Client(), Service: secretary.New("", "", "", t.TempDir(), map[int64]bool{7: true})}
	run, err := configureTelegram("disabled", mux, bot, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if telegramCalls != 0 {
		t.Fatalf("disabled mode made %d Telegram API calls", telegramCalls)
	}
}
