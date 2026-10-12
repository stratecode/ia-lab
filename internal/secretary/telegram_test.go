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

func TestPollingPersistsOffsetRejectsOtherUsersAndDeliversReply(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	modelCalls := 0
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		modelCalls++
		io.WriteString(w, `{"choices":[{"message":{"content":"Respuesta real"}}]}`)
	}))
	defer model.Close()
	polls, sent, rejected := 0, 0, 0
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch strings.TrimPrefix(r.URL.Path, "/botsecret/") {
		case "getMe":
			io.WriteString(w, `{"ok":true,"result":{"id":1}}`)
		case "getWebhookInfo":
			io.WriteString(w, `{"ok":true,"result":{"url":""}}`)
		case "getUpdates":
			polls++
			if polls == 1 {
				io.WriteString(w, `{"ok":true,"result":[{"update_id":10,"message":{"from":{"id":8},"chat":{"id":8,"type":"private"},"text":"No"}},{"update_id":11,"message":{"from":{"id":7},"chat":{"id":99,"type":"group"},"text":"No"}},{"update_id":12,"message":{"from":{"id":7},"chat":{"id":7,"type":"private"},"text":"Hola"}}]}`)
			} else {
				var body struct {
					Offset int64 `json:"offset"`
				}
				json.NewDecoder(r.Body).Decode(&body)
				if body.Offset != 13 {
					t.Errorf("checkpoint not used: %d", body.Offset)
				}
				io.WriteString(w, `{"ok":true,"result":[]}`)
				cancel()
			}
		case "sendMessage":
			var body struct {
				ChatID int64  `json:"chat_id"`
				Text   string `json:"text"`
			}
			json.NewDecoder(r.Body).Decode(&body)
			if body.ChatID != 7 || body.Text != "Respuesta real" {
				t.Errorf("unexpected delivery: %+v", body)
			}
			sent++
			io.WriteString(w, `{"ok":true,"result":{}}`)
		case "sendChatAction":
			io.WriteString(w, `{"ok":true,"result":true}`)
		default:
			t.Errorf("unexpected operation %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer api.Close()
	dir := t.TempDir()
	s := New(model.URL, "key", "instructions", dir, map[int64]bool{7: true})
	b := Bot{Base: api.URL, Token: "secret", Client: api.Client(), Service: s, OnEvent: func(event string) {
		if event == "telegram_rejected" {
			rejected++
		}
	}}
	if err := b.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if sent != 1 || modelCalls != 1 || rejected != 2 {
		t.Fatalf("sent %d model %d rejected %d", sent, modelCalls, rejected)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "offset.json"))
	if err != nil || string(raw) != "13" {
		t.Fatalf("offset %s %v", raw, err)
	}
	stat, _ := os.Stat(filepath.Join(dir, "telegram-7.json"))
	if stat.Mode().Perm() != 0600 {
		t.Fatal("private history permissions")
	}
}

func TestTelegramTransportErrorNeverContainsBotToken(t *testing.T) {
	b := Bot{Base: "http://127.0.0.1:1", Token: "private-token", Client: &http.Client{Timeout: time.Second}}
	err := b.call(context.Background(), "getMe", nil, nil)
	if err == nil || strings.Contains(err.Error(), "private-token") {
		t.Fatalf("unsafe error %v", err)
	}
}
