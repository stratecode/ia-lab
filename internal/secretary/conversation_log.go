package secretary

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// These diagnostic checkpoints deliberately contain no message, prompt, URL,
// headers or error strings. Each append is synced before execution continues.
const conversationLogMaxBytes int64 = 16 << 20
const conversationLogBackups = 4

type conversationLog struct {
	mu         sync.Mutex
	path, boot string
	sequence   uint64
	maxBytes   int64
}
type conversationTrace struct {
	logger      *conversationLog
	id, attempt string
	started     time.Time
}
type conversationContextKey struct{}
type conversationEvent struct {
	At         time.Time `json:"at"`
	Boot       string    `json:"boot_id"`
	TraceID    string    `json:"trace_id"`
	Attempt    string    `json:"attempt_id"`
	Sequence   uint64    `json:"sequence"`
	ElapsedMS  float64   `json:"elapsed_ms"`
	Event      string    `json:"event"`
	Outcome    string    `json:"outcome,omitempty"`
	DurationMS float64   `json:"duration_ms,omitempty"`
	Status     int       `json:"http_status,omitempty"`
	Bytes      int64     `json:"bytes,omitempty"`
}

var conversationEventName = regexp.MustCompile(`^[a-zA-Z0-9_.-]{1,96}$`)

func newConversationLog(stateDir string) *conversationLog {
	if stateDir == "" {
		return nil
	}
	boot, _ := os.ReadFile("/proc/sys/kernel/random/boot_id")
	bootID := strings.TrimSpace(string(boot))
	if bootID == "" {
		bootID = fmt.Sprintf("process-%d", os.Getpid())
	}
	return &conversationLog{path: filepath.Join(stateDir, "diagnostics", "conversation-steps.jsonl"), boot: bootID, maxBytes: conversationLogMaxBytes}
}
func withConversationTrace(ctx context.Context, logger *conversationLog, id string) context.Context {
	if logger == nil {
		return ctx
	}
	if _, ok := ctx.Value(conversationContextKey{}).(*conversationTrace); ok {
		return ctx
	}
	var nonce [12]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		log.Print("conversation_diagnostic_id_error")
		return ctx
	}
	return context.WithValue(ctx, conversationContextKey{}, &conversationTrace{logger: logger, id: id, attempt: hex.EncodeToString(nonce[:]), started: time.Now()})
}
func conversationTraceID(ctx context.Context) string {
	trace, _ := ctx.Value(conversationContextKey{}).(*conversationTrace)
	if trace == nil {
		return ""
	}
	return trace.attempt
}
func conversationRecord(ctx context.Context, event, outcome string, duration time.Duration, status int, bytes int64) {
	trace, _ := ctx.Value(conversationContextKey{}).(*conversationTrace)
	if trace == nil {
		return
	}
	if !conversationEventName.MatchString(event) {
		log.Print("conversation_diagnostic_event_error")
		return
	}
	record := conversationEvent{At: time.Now().UTC(), TraceID: trace.id, Attempt: trace.attempt, ElapsedMS: float64(time.Since(trace.started).Microseconds()) / 1000, Event: event, Outcome: outcome, DurationMS: float64(duration.Microseconds()) / 1000, Status: status, Bytes: bytes}
	if err := trace.logger.append(record); err != nil {
		log.Print("conversation_diagnostic_write_error")
	}
}
func conversationStep(ctx context.Context, event string) func(error) {
	start := time.Now()
	conversationRecord(ctx, event+".start", "", 0, 0, 0)
	return func(err error) {
		outcome := "ok"
		if err != nil {
			outcome = "error"
		}
		conversationRecord(ctx, event+".end", outcome, time.Since(start), 0, 0)
	}
}
func (logger *conversationLog) append(event conversationEvent) error {
	logger.mu.Lock()
	defer logger.mu.Unlock()
	dir := filepath.Dir(logger.path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	if err := os.Chmod(dir, 0700); err != nil {
		return err
	}
	// Sync the parent as well so a newly-created diagnostic directory survives.
	if err := syncTelegramQueueDir(filepath.Dir(dir)); err != nil {
		return err
	}
	logger.sequence++
	event.Sequence = logger.sequence
	event.Boot = logger.boot
	data, err := json.Marshal(event)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if stat, err := os.Stat(logger.path); err == nil && stat.Size()+int64(len(data)) > logger.maxBytes {
		for i := conversationLogBackups; i >= 1; i-- {
			old := logger.path
			if i > 1 {
				old = fmt.Sprintf("%s.%d", logger.path, i-1)
			}
			target := fmt.Sprintf("%s.%d", logger.path, i)
			if err := os.Rename(old, target); err != nil && !os.IsNotExist(err) {
				return err
			}
		}
	}
	file, err := os.OpenFile(logger.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	if err = file.Chmod(0600); err == nil {
		_, err = file.Write(data)
	}
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return syncTelegramQueueDir(dir)
}

// HTTP headers and body completion are separate checkpoints: a model can stall
// before headers, during streaming, or while its JSON response is being decoded.
func conversationHTTP(ctx context.Context, client *http.Client, req *http.Request, kind string) (*http.Response, error) {
	done := conversationStep(ctx, kind+".http")
	response, err := client.Do(req)
	done(err)
	if err != nil {
		conversationRecord(ctx, kind+".transport_error", "error", 0, 0, 0)
		return nil, err
	}
	outcome := "ok"
	if response.StatusCode >= 400 {
		outcome = "error"
	}
	conversationRecord(ctx, kind+".headers", outcome, 0, response.StatusCode, 0)
	response.Body = &conversationBody{ReadCloser: response.Body, ctx: ctx, kind: kind, status: response.StatusCode}
	return response, nil
}

type conversationBody struct {
	io.ReadCloser
	ctx    context.Context
	kind   string
	status int
	bytes  int64
	failed bool
	once   sync.Once
}

func (body *conversationBody) Read(p []byte) (int, error) {
	n, err := body.ReadCloser.Read(p)
	body.bytes += int64(n)
	if err != nil && err != io.EOF {
		body.failed = true
	}
	if err == io.EOF {
		body.finish()
	}
	return n, err
}
func (body *conversationBody) Close() error {
	err := body.ReadCloser.Close()
	if err != nil {
		body.failed = true
	}
	body.finish()
	return err
}
func (body *conversationBody) finish() {
	body.once.Do(func() {
		outcome := "ok"
		if body.failed || body.status >= 400 {
			outcome = "error"
		}
		conversationRecord(body.ctx, body.kind+".body.end", outcome, 0, body.status, body.bytes)
	})
}

func (s *Service) loadHistoryWithDiagnostics(ctx context.Context, user int64, channel, conversation string) (err error) {
	done := conversationStep(ctx, "history.read")
	defer func() { done(err) }()
	return s.loadHistory(user, channel, conversation)
}
func (s *Service) rememberWithDiagnostics(ctx context.Context, user int64, key, channel, conversation, text, answer string) (err error) {
	done := conversationStep(ctx, "conversation.save")
	defer func() { done(err) }()
	return s.remember(user, key, channel, conversation, text, answer)
}
