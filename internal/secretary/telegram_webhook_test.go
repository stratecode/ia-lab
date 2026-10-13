package secretary

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestTelegramWebhookRejectsInvalidRequests(t *testing.T) {
	queue, err := OpenTelegramUpdateQueue(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	handler := TelegramWebhook{Queue: queue, Secret: "correct-secret"}

	cases := []struct {
		name, method, token, body string
		want                      int
	}{
		{name: "wrong method", method: http.MethodGet, token: "correct-secret", body: `{}`, want: http.StatusMethodNotAllowed},
		{name: "missing secret", method: http.MethodPost, body: `{}`, want: http.StatusUnauthorized},
		{name: "wrong secret", method: http.MethodPost, token: "incorrect", body: `{}`, want: http.StatusUnauthorized},
		{name: "malformed json", method: http.MethodPost, token: "correct-secret", body: `{`, want: http.StatusBadRequest},
		{name: "oversized body", method: http.MethodPost, token: "correct-secret", body: `{"padding":"` + strings.Repeat("x", (1<<20)+1) + `"}`, want: http.StatusRequestEntityTooLarge},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, "/telegram/webhook", strings.NewReader(tc.body))
			if tc.token != "" {
				req.Header.Set("X-Telegram-Bot-Api-Secret-Token", tc.token)
			}
			res := httptest.NewRecorder()
			handler.ServeHTTP(res, req)
			if res.Code != tc.want {
				t.Fatalf("status %d, want %d", res.Code, tc.want)
			}
		})
	}
	entries, err := os.ReadDir(filepath.Join(queue.dir, "pending"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("invalid request persisted %d files", len(entries))
	}
}

func TestFileUpdateQueuePersistsAndDeduplicatesAcrossRestart(t *testing.T) {
	dir := t.TempDir()
	update := Update{ID: 41, Message: &Message{ID: 9, From: User{ID: 7}, Chat: Chat{ID: 7, Type: "private"}, Text: "hola"}}

	queue, err := OpenTelegramUpdateQueue(dir)
	if err != nil {
		t.Fatal(err)
	}
	accepted, err := queue.Enqueue(update)
	if err != nil || !accepted {
		t.Fatalf("first enqueue accepted=%v err=%v", accepted, err)
	}
	accepted, err = queue.Enqueue(update)
	if err != nil || accepted {
		t.Fatalf("duplicate enqueue accepted=%v err=%v", accepted, err)
	}

	queue, err = OpenTelegramUpdateQueue(dir)
	if err != nil {
		t.Fatal(err)
	}
	got, err := queue.Next(context.Background())
	if err != nil || got.ID != update.ID || got.Message.Text != update.Message.Text {
		t.Fatalf("recovered update=%+v err=%v", got, err)
	}
	if err := queue.Complete(update.ID); err != nil {
		t.Fatal(err)
	}
	queue, err = OpenTelegramUpdateQueue(dir)
	if err != nil {
		t.Fatal(err)
	}
	accepted, err = queue.Enqueue(update)
	if err != nil || accepted {
		t.Fatalf("completed update replay accepted=%v err=%v", accepted, err)
	}
}

func TestTelegramWebhookWorkerProcessesAllowedUpdate(t *testing.T) {
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, `{"choices":[{"message":{"content":"respuesta"}}]}`)
	}))
	defer model.Close()

	var cancel context.CancelFunc
	var sent []string
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch strings.TrimPrefix(r.URL.Path, "/botsecret/") {
		case "sendChatAction":
			io.WriteString(w, `{"ok":true,"result":true}`)
		case "sendMessage":
			var body struct {
				ChatID int64  `json:"chat_id"`
				Text   string `json:"text"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.ChatID != 7 {
				t.Errorf("unexpected Telegram reply: %+v err=%v", body, err)
			}
			sent = append(sent, body.Text)
			io.WriteString(w, `{"ok":true,"result":{}}`)
			if body.Text == "respuesta" && cancel != nil {
				cancel()
			}
		default:
			t.Errorf("unexpected Telegram method: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer api.Close()

	queue, err := OpenTelegramUpdateQueue(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	service := New(model.URL, "key", "instructions", t.TempDir(), map[int64]bool{7: true})
	bot := &Bot{Base: api.URL, Token: "secret", Client: api.Client(), Service: service}
	ctx, stop := context.WithCancel(context.Background())
	cancel = stop
	defer stop()

	workerDone := make(chan error, 1)
	go func() { workerDone <- bot.RunWebhookWorker(ctx, queue) }()
	handler := TelegramWebhook{Queue: queue, Secret: "secret"}
	req := httptest.NewRequest(http.MethodPost, "/telegram/webhook", strings.NewReader(`{"update_id":1,"message":{"message_id":2,"from":{"id":7},"chat":{"id":7,"type":"private"},"text":"hola"}}`))
	req.Header.Set("X-Telegram-Bot-Api-Secret-Token", "secret")
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("webhook status %d: %s", res.Code, res.Body.String())
	}
	select {
	case err := <-workerDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("webhook worker did not finish")
	}
	if len(sent) != 1 || sent[0] != "respuesta" {
		t.Fatalf("sent replies=%v, want one final answer", sent)
	}
	if _, err := os.Stat(filepath.Join(queue.dir, "pending-00000000000000000001.json")); !os.IsNotExist(err) {
		t.Fatalf("update still pending: %v", err)
	}
}

func TestTelegramWebhookWorkerRetriesAfterRestart(t *testing.T) {
	var sendAttempts int
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/sendMessage") {
			sendAttempts++
			if sendAttempts == 1 {
				w.WriteHeader(http.StatusServiceUnavailable)
				io.WriteString(w, `{"ok":false}`)
				return
			}
		}
		io.WriteString(w, `{"ok":true,"result":{}}`)
	}))
	defer api.Close()

	dir := t.TempDir()
	queue, err := OpenTelegramUpdateQueue(dir)
	if err != nil {
		t.Fatal(err)
	}
	update := Update{ID: 8, Message: &Message{ID: 2, From: User{ID: 7}, Chat: Chat{ID: 7, Type: "private"}, Text: "/help"}}
	if _, err := queue.Enqueue(update); err != nil {
		t.Fatal(err)
	}
	bot := &Bot{Base: api.URL, Token: "secret", Client: api.Client(), Service: New("", "", "", t.TempDir(), map[int64]bool{7: true})}
	ctx1, stop1 := context.WithCancel(context.Background())
	bot.OnEvent = func(event string) {
		if event == "telegram_send_error" {
			stop1()
		}
	}
	if err := bot.RunWebhookWorker(ctx1, queue); err != nil {
		t.Fatal(err)
	}
	stop1()

	queue, err = OpenTelegramUpdateQueue(dir)
	if err != nil {
		t.Fatal(err)
	}
	ctx2, stop2 := context.WithCancel(context.Background())
	bot.OnEvent = func(event string) {
		if event == "telegram_reply_sent" {
			stop2()
		}
	}
	if err := bot.RunWebhookWorker(ctx2, queue); err != nil {
		t.Fatal(err)
	}
	stop2()
	if sendAttempts != 2 {
		t.Fatalf("send attempts=%d, want failed delivery and one answer after restart", sendAttempts)
	}
	queue, err = OpenTelegramUpdateQueue(dir)
	if err != nil {
		t.Fatal(err)
	}
	accepted, err := queue.Enqueue(update)
	if err != nil || accepted {
		t.Fatalf("completed retry replay accepted=%v err=%v", accepted, err)
	}
}
