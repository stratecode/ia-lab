package secretary

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestConversationStepsPersistBeforeModelCompletes(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-release
		io.WriteString(w, `{"choices":[{"message":{"content":"private-output-sentinel"}}]}`)
	}))
	defer model.Close()
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, `{"ok":true,"result":{}}`) }))
	defer api.Close()
	root := t.TempDir()
	service := New(model.URL, "private-key-sentinel", "instructions", root, map[int64]bool{7: true})
	service.SetDiagnosticsEnabled(true)
	bot := &Bot{Base: api.URL, Token: "private-bot-sentinel", Client: api.Client(), Service: service}
	done := make(chan error, 1)
	go func() {
		done <- bot.processUpdate(context.Background(), Update{ID: 42, Message: &Message{ID: 1, From: User{ID: 7}, Chat: Chat{ID: 7, Type: "private"}, Text: "Hola private-input-sentinel"}})
	}()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		close(release)
		t.Fatal("model was not called")
	}
	path := filepath.Join(root, "diagnostics", "conversation-steps.jsonl")
	raw, err := os.ReadFile(path)
	if err != nil {
		close(release)
		<-done
		t.Fatalf("in-flight diagnostic log missing: %v", err)
	}
	if !strings.Contains(string(raw), `"event":"model.request.start"`) || strings.Contains(string(raw), `"event":"model.request.end"`) {
		close(release)
		<-done
		t.Fatalf("incorrect in-flight boundaries: %s", raw)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	raw, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range []string{"telegram.process.start", "authorization.end", "history.read.start", "history.read.end", "routing.start", "routing.end", "model.request.start", "model.headers", "model.body.end", "model.request.end", "conversation.save.start", "conversation.save.end", "telegram.sendMessage.start", "telegram.sendMessage.end", "telegram.process.end"} {
		if !strings.Contains(string(raw), `"event":"`+event+`"`) {
			t.Errorf("missing %s", event)
		}
	}
	for _, secret := range []string{"private-input-sentinel", "private-output-sentinel", "private-key-sentinel", "private-bot-sentinel", model.URL} {
		if strings.Contains(string(raw), secret) {
			t.Errorf("private data leaked: %s", secret)
		}
	}
	var attempt string
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		var event struct {
			TraceID string `json:"trace_id"`
			Attempt string `json:"attempt_id"`
			Boot    string `json:"boot_id"`
			Event   string `json:"event"`
		}
		if json.Unmarshal([]byte(line), &event) != nil || event.TraceID != "telegram-42" || event.Boot == "" || event.Attempt == "" {
			t.Fatalf("invalid correlation: %s", line)
		}
		if attempt == "" {
			attempt = event.Attempt
		}
		if attempt != event.Attempt {
			t.Fatal("attempt changed within update")
		}
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0600 {
		t.Fatal("log must be private")
	}
}

func TestConversationLogRotationBoundsFiles(t *testing.T) {
	root := t.TempDir()
	logger := newConversationLog(root)
	logger.maxBytes = 600
	ctx := withConversationTrace(context.Background(), logger, "telegram-44")
	for i := 0; i < 40; i++ {
		conversationRecord(ctx, "test.start", "ok", 0, 0, 0)
	}
	files, err := filepath.Glob(filepath.Join(root, "diagnostics", "conversation-steps.jsonl*"))
	if err != nil || len(files) != conversationLogBackups+1 {
		t.Fatalf("rotation files: %v %v", files, err)
	}
	for _, path := range files {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if len(raw) > 600 {
			t.Fatalf("oversized file %d", len(raw))
		}
		for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
			if !json.Valid([]byte(line)) {
				t.Fatalf("torn record: %q", line)
			}
		}
	}
}

func TestConversationCheckpointSurvivesAbruptExit(t *testing.T) {
	if os.Getenv("LAB_CONVERSATION_CRASH_HELPER") == "1" {
		ctx := withConversationTrace(context.Background(), newConversationLog(os.Getenv("LAB_CONVERSATION_CRASH_DIR")), "telegram-45")
		finish := conversationStep(ctx, "model.request")
		defer finish(nil)
		os.Exit(23)
	}
	root := t.TempDir()
	command := exec.Command(os.Args[0], "-test.run=^TestConversationCheckpointSurvivesAbruptExit$")
	command.Env = append(os.Environ(), "LAB_CONVERSATION_CRASH_HELPER=1", "LAB_CONVERSATION_CRASH_DIR="+root)
	err := command.Run()
	if status, ok := err.(*exec.ExitError); !ok || status.ExitCode() != 23 {
		t.Fatalf("helper did not exit abruptly: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(root, "diagnostics", "conversation-steps.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"event":"model.request.start"`) || strings.Contains(string(raw), `"event":"model.request.end"`) {
		t.Fatalf("checkpoint did not survive without defer: %s", raw)
	}
	ctx := withConversationTrace(context.Background(), newConversationLog(root), "telegram-45")
	conversationRecord(ctx, "recovery.start", "ok", 0, 0, 0)
	data, err := os.ReadFile(filepath.Join(root, "diagnostics", "conversation-steps.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	attempts := map[string]bool{}
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var event map[string]any
		if json.Unmarshal([]byte(line), &event) != nil {
			t.Fatalf("invalid record: %s", line)
		}
		attempts[event["attempt_id"].(string)] = true
	}
	if len(attempts) != 2 {
		t.Fatalf("restart/retry attempt reused: %v", attempts)
	}
}
