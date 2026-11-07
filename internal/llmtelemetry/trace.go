package llmtelemetry

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

const traceHeader = "X-Lab-Trace-ID"
const traceMaxBytes int64 = 16 << 20
const traceBackups = 4

var validTraceID = regexp.MustCompile(`^[0-9a-f]{24}$`)

type traceLog struct {
	mu   sync.Mutex
	path string
	boot string
}

type traceEvent struct {
	At        time.Time `json:"at"`
	Boot      string    `json:"boot_id"`
	ID        string    `json:"trace_id"`
	Event     string    `json:"event"`
	Endpoint  string    `json:"endpoint"`
	ElapsedMS float64   `json:"elapsed_ms"`
	Outcome   string    `json:"outcome,omitempty"`
	Status    int       `json:"http_status,omitempty"`
	Bytes     int64     `json:"bytes,omitempty"`
}

func newTraceLog(dir string) *traceLog {
	if dir == "" {
		return nil
	}
	boot, _ := os.ReadFile("/proc/sys/kernel/random/boot_id")
	return &traceLog{path: filepath.Join(dir, "inference-steps.jsonl"), boot: strings.TrimSpace(string(boot))}
}

func requestTraceID(candidate string) string {
	if validTraceID.MatchString(candidate) {
		return candidate
	}
	var id [12]byte
	if _, err := rand.Read(id[:]); err != nil {
		log.Print("inference_diagnostic_id_error")
		return ""
	}
	return hex.EncodeToString(id[:])
}

func (l *traceLog) record(id, endpoint, event, outcome string, start time.Time, status int, bytes int64) {
	if l == nil || id == "" {
		return
	}
	record := traceEvent{At: time.Now().UTC(), Boot: l.boot, ID: id, Event: event, Endpoint: endpoint, ElapsedMS: float64(time.Since(start).Microseconds()) / 1000, Outcome: outcome, Status: status, Bytes: bytes}
	if err := l.append(record); err != nil {
		log.Print("inference_diagnostic_write_error")
	}
}

func (l *traceLog) append(event traceEvent) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	dir := filepath.Dir(l.path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	data, err := json.Marshal(event)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if stat, err := os.Stat(l.path); err == nil && stat.Size()+int64(len(data)) > traceMaxBytes {
		for i := traceBackups; i >= 1; i-- {
			old := l.path
			if i > 1 {
				old = fmt.Sprintf("%s.%d", l.path, i-1)
			}
			if err := os.Rename(old, fmt.Sprintf("%s.%d", l.path, i)); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
		}
	}
	f, err := os.OpenFile(l.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(data)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	err = d.Sync()
	closeErr = d.Close()
	if err != nil {
		return err
	}
	return closeErr
}
