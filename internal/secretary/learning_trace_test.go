package secretary

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func traceTestService(t *testing.T, read func(context.Context, string) (CapabilityResult, error)) (*Service, *JSONLLearningStore) {
	t.Helper()
	state := t.TempDir()
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"respuesta sintética"}}]}`))
	}))
	t.Cleanup(model.Close)
	service := New(model.URL, "test-key", "test instructions", state, map[int64]bool{7: true})
	store := NewJSONLLearningStore(state)
	service.Learning = store
	service.ModelLabel = "test-model"
	service.Capabilities = []ReadCapability{{Name: "Rovo", Description: "tareas", Match: func(string) bool { return true }, Read: read}}
	return service, store
}

func TestTraceRecordsSelectedCapabilitiesAndEvidence(t *testing.T) {
	service, store := traceTestService(t, func(context.Context, string) (CapabilityResult, error) {
		return CapabilityResult{Text: "Tarea de prueba", ResultCount: 1, Evidence: []EvidenceRef{{Provider: "jira", ID: "ISSUE-7", Title: "Plan trimestral"}}}, nil
	})
	if _, err := service.ReplyInConversation(context.Background(), 7, "telegram", "chat-1", "¿Qué tareas tengo?"); err != nil {
		t.Fatal(err)
	}
	trace, ok, err := store.LastTrace(7, "telegram", conversationDigest("telegram", "chat-1"))
	if err != nil || !ok {
		t.Fatalf("LastTrace = %+v, %t, %v", trace, ok, err)
	}
	if trace.ID == "" || trace.SchemaVersion != learningSchemaVersion || trace.Intent == "" || trace.Outcome != "success" || trace.RouterVersion != secretaryRouterVersion || trace.ModelLabel != "test-model" {
		t.Fatalf("trace missing interaction metadata: %+v", trace)
	}
	if len(trace.Capabilities) != 1 || trace.Capabilities[0].Capability != "Rovo" || trace.Capabilities[0].Outcome != "success" || trace.Capabilities[0].ResultCount != 1 || len(trace.Capabilities[0].Evidence) != 1 || trace.Capabilities[0].Evidence[0].ID != "ISSUE-7" || trace.Capabilities[0].Duration <= 0 {
		t.Fatalf("trace missing capability evidence: %+v", trace.Capabilities)
	}
}

func TestTraceDistinguishesEmptyFromConnectorFailure(t *testing.T) {
	for _, tc := range []struct {
		name, expected string
		read           func(context.Context, string) (CapabilityResult, error)
	}{
		{name: "empty", expected: "empty", read: func(context.Context, string) (CapabilityResult, error) { return CapabilityResult{}, nil }},
		{name: "failure", expected: "error", read: func(context.Context, string) (CapabilityResult, error) { return CapabilityResult{}, os.ErrNotExist }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			service, store := traceTestService(t, tc.read)
			if _, err := service.ReplyInConversation(context.Background(), 7, "telegram", "chat-1", "consulta tareas"); err != nil {
				t.Fatal(err)
			}
			trace, ok, err := store.LastTrace(7, "telegram", conversationDigest("telegram", "chat-1"))
			if err != nil || !ok {
				t.Fatalf("LastTrace = %+v, %t, %v", trace, ok, err)
			}
			if len(trace.Capabilities) != 1 || trace.Capabilities[0].Outcome != tc.expected {
				t.Fatalf("outcome = %+v, want %s", trace.Capabilities, tc.expected)
			}
			if tc.expected == "empty" && trace.Outcome != "empty" {
				t.Fatalf("trace outcome = %s, want empty", trace.Outcome)
			}
			if tc.expected == "error" && trace.Outcome != "blocked" {
				t.Fatalf("trace outcome = %s, want blocked", trace.Outcome)
			}
		})
	}
}

func TestTraceDoesNotPersistPromptOrProviderPayload(t *testing.T) {
	secretPrompt := "consulta privada palabra-azul-912"
	secretPayload := "payload privado transcripción-violeta-544"
	service, _ := traceTestService(t, func(context.Context, string) (CapabilityResult, error) {
		return CapabilityResult{Text: secretPayload, ResultCount: 1}, nil
	})
	if _, err := service.ReplyInConversation(context.Background(), 7, "telegram", "chat-1", secretPrompt); err != nil {
		t.Fatal(err)
	}
	data, err := filepath.Glob(filepath.Join(service.StateDir, "learning", "traces", "7", "*.jsonl"))
	if err != nil || len(data) != 1 {
		t.Fatalf("trace paths = %v, %v", data, err)
	}
	raw, err := os.ReadFile(data[0])
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), secretPrompt) || strings.Contains(string(raw), secretPayload) || strings.Contains(string(raw), "respuesta sintética") {
		t.Fatalf("learning trace persisted content: %s", raw)
	}
}

func TestInteractionMetricsUseBoundedLabels(t *testing.T) {
	metrics := NewVoiceMetrics()
	metrics.ObserveInteraction("task-ISSUE-192", "customer-42", "secret-user-content", time.Second)
	metrics.ObserveMemoryRetrieval("hit-private-chat-id", 1)
	w := httptest.NewRecorder()
	metrics.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := w.Body.String()
	if !strings.Contains(body, `lab_secretary_interactions_total{capability="other",intent="other",outcome="other"} 1`) {
		t.Fatalf("unbounded interaction labels not collapsed: %s", body)
	}
	if !strings.Contains(body, `lab_secretary_memory_retrieval_total{outcome="other"} 1`) || strings.Contains(body, "ISSUE-192") || strings.Contains(body, "private-chat-id") {
		t.Fatalf("memory metrics leaked or omitted bounded labels: %s", body)
	}
}
