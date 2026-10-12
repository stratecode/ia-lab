package secretary

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type Turn struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}
type SourceReader interface {
	Jira(context.Context) (string, error)
	Granola(context.Context) (string, error)
	Status() map[string]bool
}
type Service struct {
	Upstream, Key, Instructions, StateDir string
	Allowed                               map[int64]bool
	Sources                               SourceReader
	Client                                *http.Client
	mu                                    sync.Mutex
	history                               map[int64][]Turn
}

func New(upstream, key, instructions, stateDir string, allowed map[int64]bool) *Service {
	return &Service{Upstream: strings.TrimRight(upstream, "/"), Key: key, Instructions: instructions, StateDir: stateDir, Allowed: allowed, Client: &http.Client{Timeout: 120 * time.Second}, history: map[int64][]Turn{}}
}
func bounded(messages []Turn) []Turn {
	n := 0
	start := len(messages)
	for start >= 2 && len(messages)-start < 8 {
		size := len([]rune(messages[start-2].Content)) + len([]rune(messages[start-1].Content))
		if n+size > 4000 {
			break
		}
		n += size
		start -= 2
	}
	return append([]Turn{}, messages[start:]...)
}
func atomicJSON(path string, value any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	b, err := json.Marshal(value)
	if err != nil {
		return err
	}
	temp := path + ".tmp"
	if err = os.WriteFile(temp, b, 0600); err != nil {
		return err
	}
	return os.Rename(temp, path)
}
func (s *Service) historyPath(user int64) string {
	return filepath.Join(s.StateDir, fmt.Sprintf("telegram-%d.json", user))
}
func (s *Service) Reply(ctx context.Context, user int64, text string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	text = strings.TrimSpace(text)
	if text == "" {
		return "Escribe una consulta de texto.", nil
	}
	switch strings.Fields(text)[0] {
	case "/start", "/help":
		return "Soy tu secretaria del lab. Puedes escribirme en español.\n/new: conversación nueva\n/status: conexiones\n/jira: pendientes\n/reuniones: reuniones recientes\n/resumen: tareas y reuniones\nTexto primero; voz todavía no disponible.", nil
	case "/new":
		if err := atomicJSON(s.historyPath(user), []Turn{}); err != nil {
			return "", err
		}
		delete(s.history, user)
		return "Conversación nueva. Tus chats de Open WebUI siguen disponibles allí.", nil
	case "/status":
		return s.sourceStatus() + "\nModelo: LFM principal. Acciones externas: solo lectura.", nil
	case "/jira", "/reuniones", "/resumen":
		if err := s.loadHistory(user); err != nil {
			return "", err
		}
		answer := s.readSources(ctx, strings.Fields(text)[0])
		return answer, s.remember(user, text, answer)
	}
	if strings.HasPrefix(text, "/") {
		return "Comando no disponible. Consulta /help.", nil
	}
	if len([]rune(text)) > 4000 {
		return "La consulta es demasiado larga; envía un fragmento de hasta 4.000 caracteres.", nil
	}
	if err := s.loadHistory(user); err != nil {
		return "", err
	}
	turns := append([]Turn{{Role: "system", Content: s.Instructions}}, s.history[user]...)
	turns = append(turns, Turn{Role: "user", Content: text})
	payload := map[string]any{"model": "main", "messages": turns, "temperature": 0, "max_tokens": 512, "stream": false}
	data, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, "POST", s.Upstream+"/v1/chat/completions", bytes.NewReader(data))
	if err != nil {
		return "", errors.New("invalid model endpoint")
	}
	req.Header.Set("Authorization", "Bearer "+s.Key)
	req.Header.Set("Content-Type", "application/json")
	res, err := s.Client.Do(req)
	if err != nil {
		return "", errors.New("model unavailable")
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return "", fmt.Errorf("model HTTP %d", res.StatusCode)
	}
	var response struct {
		Choices []struct {
			Message Turn `json:"message"`
		} `json:"choices"`
	}
	if err = json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&response); err != nil {
		return "", errors.New("invalid model response")
	}
	if len(response.Choices) == 0 || strings.TrimSpace(response.Choices[0].Message.Content) == "" {
		return "", errors.New("empty model response")
	}
	answer := response.Choices[0].Message.Content
	return answer, s.remember(user, text, answer)
}
func (s *Service) loadHistory(user int64) error {
	if _, ok := s.history[user]; !ok {
		var turns []Turn
		b, err := os.ReadFile(s.historyPath(user))
		if err == nil {
			if err = json.Unmarshal(b, &turns); err != nil {
				return errors.New("invalid history state")
			}
		}
		if err != nil && !os.IsNotExist(err) {
			return errors.New("cannot read history")
		}
		s.history[user] = bounded(turns)
	}
	return nil
}
func (s *Service) remember(user int64, text, answer string) error {
	next := bounded(append(append([]Turn{}, s.history[user]...), Turn{Role: "user", Content: text}, Turn{Role: "assistant", Content: answer}))
	if err := atomicJSON(s.historyPath(user), next); err != nil {
		return errors.New("cannot persist history")
	}
	s.history[user] = next
	return nil
}

func (s *Service) sourceStatus() string {
	status := map[string]bool{}
	if s.Sources != nil {
		status = s.Sources.Status()
	}
	lines := []string{}
	for _, name := range []string{"Jira", "Granola"} {
		state := "no conectado"
		if status[name] {
			state = "configurado (validar consulta)"
		}
		lines = append(lines, name+": "+state)
	}
	return strings.Join(lines, "\n")
}
func (s *Service) readSources(ctx context.Context, command string) string {
	if s.Sources == nil {
		return s.sourceStatus()
	}
	lines := []string{}
	if command != "/reuniones" {
		text, err := s.Sources.Jira(ctx)
		if err != nil {
			text = "Jira: no disponible; no se han verificado tareas."
		}
		lines = append(lines, text)
	}
	if command != "/jira" {
		text, err := s.Sources.Granola(ctx)
		if err != nil {
			text = "Granola: no disponible; no se han verificado reuniones."
		}
		lines = append(lines, text)
	}
	return strings.Join(lines, "\n\n")
}
func (s *Service) Handler() http.Handler {
	upstream, _ := url.Parse(s.Upstream)
	proxy := httputil.NewSingleHostReverseProxy(upstream)
	proxy.FlushInterval = -1
	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) { http.Error(w, "model unavailable", 502) }
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, `{"status":true}`)
			return
		}
		if subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+s.Key)) != 1 || s.Key == "" {
			http.Error(w, "unauthorised", 401)
			return
		}
		if r.URL.Path != "/v1/chat/completions" && r.URL.Path != "/v1/models" {
			http.NotFound(w, r)
			return
		}
		if r.URL.Path == "/v1/chat/completions" {
			if r.Method != "POST" {
				http.Error(w, "POST required", 405)
				return
			}
			data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
			if err != nil {
				http.Error(w, "request too large", 413)
				return
			}
			var payload map[string]json.RawMessage
			if json.Unmarshal(data, &payload) != nil {
				http.Error(w, "invalid JSON", 400)
				return
			}
			var messages []map[string]json.RawMessage
			if json.Unmarshal(payload["messages"], &messages) != nil || len(messages) == 0 {
				http.Error(w, "messages required", 400)
				return
			}
			var last string
			json.Unmarshal(messages[len(messages)-1]["content"], &last)
			command := strings.TrimSpace(last)
			if command == "/jira" || command == "/reuniones" || command == "/resumen" || command == "/status" {
				answer := s.sourceStatus()
				if command != "/status" {
					answer = s.readSources(r.Context(), command)
				}
				var streaming bool
				json.Unmarshal(payload["stream"], &streaming)
				if streaming {
					w.Header().Set("Content-Type", "text/event-stream")
					event, _ := json.Marshal(map[string]any{"id": "secretary-read", "object": "chat.completion.chunk", "model": "main", "choices": []any{map[string]any{"index": 0, "delta": map[string]string{"content": answer}, "finish_reason": nil}}})
					stop, _ := json.Marshal(map[string]any{"id": "secretary-read", "object": "chat.completion.chunk", "model": "main", "choices": []any{map[string]any{"index": 0, "delta": map[string]string{}, "finish_reason": "stop"}}})
					fmt.Fprintf(w, "data: %s\n\ndata: %s\n\ndata: [DONE]\n\n", event, stop)
				} else {
					w.Header().Set("Content-Type", "application/json")
					json.NewEncoder(w).Encode(map[string]any{"id": "secretary-read", "object": "chat.completion", "model": "main", "choices": []any{map[string]any{"index": 0, "message": Turn{Role: "assistant", Content: answer}, "finish_reason": "stop"}}})
				}
				return
			}
			// Preserve multimodal/message fields; only prepend the shared instruction.
			system, _ := json.Marshal(map[string]string{"role": "system", "content": s.Instructions})
			var instruction map[string]json.RawMessage
			json.Unmarshal(system, &instruction)
			messages = append([]map[string]json.RawMessage{instruction}, messages...)
			payload["messages"], _ = json.Marshal(messages)
			data, _ = json.Marshal(payload)
			r.Body = io.NopCloser(bytes.NewReader(data))
			r.ContentLength = int64(len(data))
		}
		proxy.ServeHTTP(w, r)
	})
}
