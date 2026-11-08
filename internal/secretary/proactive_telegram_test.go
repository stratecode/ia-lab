package secretary

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func telegramSendBot(t *testing.T, handler http.HandlerFunc, clientTimeout time.Duration) *Bot {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return &Bot{Base: server.URL, Token: "token", Client: &http.Client{Timeout: clientTimeout}}
}

func TestProactiveTelegramSendClassifiesOutcomes(t *testing.T) {
	cases := []struct {
		name    string
		handler http.HandlerFunc
		timeout time.Duration
		want    ProactiveSendOutcome
	}{
		{"accepted", func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, `{"ok":true,"result":{}}`) }, time.Second, SendDelivered},
		{"rejected by telegram", func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, `{"ok":false}`) }, time.Second, SendFailed},
		{"client error", func(w http.ResponseWriter, r *http.Request) { http.Error(w, "bad", http.StatusBadRequest) }, time.Second, SendFailed},
		{"rate limited", func(w http.ResponseWriter, r *http.Request) { http.Error(w, "slow", http.StatusTooManyRequests) }, time.Second, SendFailed},
		{"server error may have been accepted", func(w http.ResponseWriter, r *http.Request) { http.Error(w, "boom", http.StatusBadGateway) }, time.Second, SendUncertain},
		{"timeout after the request left", func(w http.ResponseWriter, r *http.Request) {
			time.Sleep(300 * time.Millisecond)
			io.WriteString(w, `{"ok":true}`)
		}, 50 * time.Millisecond, SendUncertain},
	}
	for _, tc := range cases {
		bot := telegramSendBot(t, tc.handler, tc.timeout)
		if got := bot.Send(context.Background(), 7, "hola"); got != tc.want {
			t.Errorf("%s: got %v want %v", tc.name, got, tc.want)
		}
	}
	if (&Bot{}).Send(context.Background(), 7, "   ") != SendFailed {
		t.Error("an empty message is never sent")
	}
}

func TestProactiveTelegramSendUsesOnePrivateMessageWithBoundedText(t *testing.T) {
	var body string
	bot := telegramSendBot(t, func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		body = string(b)
		io.WriteString(w, `{"ok":true,"result":{}}`)
	}, time.Second)
	if bot.Send(context.Background(), 42, strings.Repeat("a", 9000)) != SendDelivered {
		t.Fatal("send failed")
	}
	if !strings.Contains(body, `"chat_id":42`) || len(body) > 4500 {
		t.Fatalf("body len=%d: %.120s", len(body), body)
	}
}
