package secretary

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestParseTimerRequestUnderstandsSimpleSpanishDuration(t *testing.T) {
	tests := []struct {
		input string
		want  time.Duration
	}{
		{input: "ponme un temporizador de 10 minutos", want: 10 * time.Minute},
		{input: "Avísame en 30 segundos", want: 30 * time.Second},
		{input: "temporizador 10'", want: 10 * time.Minute},
		{input: "/timer 2m", want: 2 * time.Minute},
	}
	for _, tt := range tests {
		got, ok := parseTimerRequest(tt.input)
		if !ok || got != tt.want {
			t.Errorf("parseTimerRequest(%q) = %v, %v; want %v, true", tt.input, got, ok, tt.want)
		}
	}
	for _, input := range []string{"temporizador 0 segundos", "temporizador 25 horas", "texto normal"} {
		if _, ok := parseTimerRequest(input); ok {
			t.Errorf("parseTimerRequest(%q) unexpectedly accepted", input)
		}
	}
}

func TestDueTimerIsDeliveredOnceAndPersisted(t *testing.T) {
	s := New("http://127.0.0.1:1", "key", "instructions", t.TempDir(), map[int64]bool{7: true})
	if err := s.writeTimers([]Timer{{ID: "abc123", UserID: 7, ChatID: 7, DueAt: time.Now().Add(-time.Second)}}); err != nil {
		t.Fatal(err)
	}
	sends := 0
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/sendMessage") {
			t.Errorf("unexpected Telegram operation: %s", r.URL.Path)
		}
		var body struct {
			ChatID int64  `json:"chat_id"`
			Text   string `json:"text"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body.ChatID != 7 || !strings.Contains(body.Text, "Temporizador terminado") {
			t.Errorf("unexpected timer message: %+v", body)
		}
		sends++
		w.Write([]byte(`{"ok":true,"result":{}}`))
	}))
	defer api.Close()
	b := &Bot{Base: api.URL, Token: "secret", Client: api.Client(), Service: s}
	b.deliverDueTimers(context.Background())
	b.deliverDueTimers(context.Background())
	if sends != 1 {
		t.Fatalf("timer sent %d times, want once", sends)
	}
	timers, err := s.readTimers()
	if err != nil || len(timers) != 1 || !timers[0].Delivered {
		t.Fatalf("delivered timer state = %#v, %v", timers, err)
	}
}

func TestReplySchedulesTimerAndPersistsListAndCancellation(t *testing.T) {
	dir := t.TempDir()
	s := New("http://127.0.0.1:1", "key", "instructions", dir, map[int64]bool{7: true})
	answer, err := s.Reply(context.Background(), 7, "Ponme un temporizador de 10 minutos")
	if err != nil || !strings.Contains(answer, "10 minutos") {
		t.Fatalf("timer acknowledgement = %q, %v", answer, err)
	}
	timers, err := s.readTimers()
	if err != nil || len(timers) != 1 || timers[0].UserID != 7 || timers[0].Delivered || timers[0].Canceled {
		t.Fatalf("stored timer = %#v, %v", timers, err)
	}
	if time.Until(timers[0].DueAt) < 9*time.Minute || time.Until(timers[0].DueAt) > 10*time.Minute {
		t.Fatalf("unexpected due time: %s", timers[0].DueAt)
	}

	// A new service instance represents a lab-secretary restart.
	s = New("http://127.0.0.1:1", "key", "instructions", dir, map[int64]bool{7: true})
	answer, err = s.Reply(context.Background(), 7, "/timers")
	if err != nil || !strings.Contains(answer, timers[0].ID) {
		t.Fatalf("timer list after restart = %q, %v", answer, err)
	}
	answer, err = s.Reply(context.Background(), 7, "/cancel "+timers[0].ID)
	if err != nil || !strings.Contains(answer, "cancelado") {
		t.Fatalf("timer cancellation = %q, %v", answer, err)
	}
	timers, err = s.readTimers()
	if err != nil || !timers[0].Canceled {
		t.Fatalf("canceled timer state = %#v, %v", timers, err)
	}
}

func TestInvalidTimerRequestDoesNotFallThroughToModel(t *testing.T) {
	modelCalls := 0
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		modelCalls++
		w.Write([]byte(`{"choices":[{"message":{"content":"lo he programado"}}]}`))
	}))
	defer model.Close()
	s := New(model.URL, "key", "instructions", t.TempDir(), map[int64]bool{7: true})
	answer, err := s.Reply(context.Background(), 7, "Ponme un temporizador de 25 horas")
	if err != nil || !strings.Contains(answer, "24 horas") {
		t.Fatalf("invalid timer response = %q, %v", answer, err)
	}
	if modelCalls != 0 {
		t.Fatalf("invalid timer request reached the model %d times", modelCalls)
	}
}
