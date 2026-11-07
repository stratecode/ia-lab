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
)

func TestThreeTurnConversationKeepsAnaphoricMeetingContextForJiraComparison(t *testing.T) {
	var granolaReads, jiraReads, comparisonCalls int
	var granolaFollowUpQuery string
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		comparisonCalls++
		var request struct {
			Messages []Turn `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		if len(request.Messages) < 2 || !strings.Contains(request.Messages[0].Content, "PMI-46: Waiting") || !strings.Contains(request.Messages[0].Content, "Fran explica a Gonzalo") {
			t.Errorf("comparison omitted source evidence: %+v", request.Messages)
		}
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"{\"facts\":[{\"source\":\"Granola\",\"evidence_id\":\"meeting-1\",\"quote\":\"Fran explica a Gonzalo el modelo de datos con la pizarra\"},{\"source\":\"Jira\",\"evidence_id\":\"PMI-46\",\"quote\":\"PMI-46: Waiting\"}],\"links\":[{\"granola_fact\":0,\"jira_fact\":1,\"shared_phrase\":\"Fran explica a Gonzalo el modelo de datos con la pizarra\"}] }"}}]}`)
	}))
	defer model.Close()
	intent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request intentWireRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		capability := "granola.read"
		if strings.Contains(request.Utterance, "PMI-46") {
			capability = "jira.read"
		}
		payload, _ := json.Marshal(intentWireResult{SchemaVersion: "1", ProfileVersion: "donna-v1", Status: "resolved", Goal: stringPointer("lookup"), SuggestedCapabilities: []string{capability}, Basis: "deterministic"})
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(payload)
	}))
	defer intent.Close()

	service := New(model.URL, "key", "instructions", t.TempDir(), map[int64]bool{7: true})
	service.Learning = NewJSONLLearningStore(t.TempDir())
	service.SetDiagnosticsEnabled(true)
	service.Intent = &IntentHTTPClient{URL: intent.URL, Token: "test-secret", Client: intent.Client()}
	service.IntentMode = "enabled"
	service.Capabilities = []ReadCapability{
		{Name: "Granola", Description: "notas", Match: func(query string) bool {
			return containsAny(normalizedIntentText(query), "granola", "reunion", "reuniones", "daily")
		}, Read: func(_ context.Context, query string) (CapabilityResult, error) {
			granolaReads++
			granolaFollowUpQuery = query
			return CapabilityResult{Text: "Fran explica a Gonzalo el modelo de datos con la pizarra.", ResultCount: 1, Evidence: []EvidenceRef{{Provider: "granola", ID: "meeting-1", Title: "Daily del 26 de octubre", URL: "https://notes.example/meeting-1"}}, DirectAnswer: granolaReads == 1}, nil
		}},
		{Name: "Jira", Description: "incidencias", Match: jiraTaskIntent, Read: func(context.Context, string) (CapabilityResult, error) {
			jiraReads++
			return CapabilityResult{Text: "PMI-46: Waiting", ResultCount: 1, Evidence: []EvidenceRef{{Provider: "jira", ID: "PMI-46", Title: "Migración del Core", URL: "https://jira.example/browse/PMI-46"}}, DirectAnswer: jiraReads == 1}, nil
		}},
	}

	conversation := "chat-three-turn-eval"
	first, err := service.ReplyInConversation(context.Background(), 7, "openwebui", conversation, "Solo Granola: en la daily del 26 de octubre, ¿qué tenía que explicar Fran a Gonzalo?")
	if err != nil || !strings.Contains(first, "modelo de datos") {
		t.Fatalf("first answer=%q err=%v", first, err)
	}
	second, err := service.ReplyInConversation(context.Background(), 7, "openwebui", conversation, "Solo Jira: ¿cuál es el estado de PMI-46?")
	if err != nil || !strings.Contains(second, "PMI-46: Waiting") {
		t.Fatalf("second answer=%q err=%v", second, err)
	}
	third, err := service.ReplyInConversation(context.Background(), 7, "openwebui", conversation, "¿Eso está vinculado con PMI-46? Contrasta ambas fuentes.")
	if err != nil || !strings.Contains(third, "No he encontrado una coincidencia verificable") || strings.Contains(third, "Coincidencia verificable") {
		t.Fatalf("third answer=%q err=%v", third, err)
	}
	if granolaReads != 2 || jiraReads != 2 || comparisonCalls != 1 {
		t.Fatalf("reads granola=%d jira=%d comparisons=%d", granolaReads, jiraReads, comparisonCalls)
	}
	if !strings.Contains(granolaFollowUpQuery, "daily del 26 de octubre") || !strings.Contains(granolaFollowUpQuery, "PMI-46") {
		t.Fatalf("third turn lost conversation anchor in Granola query: %q", granolaFollowUpQuery)
	}
	trace, ok, err := service.Learning.LastTrace(7, "openwebui", conversationDigest("openwebui", conversation))
	if err != nil || !ok || trace.RouteDecision != "intent_service_multi" || trace.FallbackReason != "" {
		t.Fatalf("last route trace=%+v ok=%t err=%v", trace, ok, err)
	}
	logData, err := os.ReadFile(filepath.Join(service.StateDir, "diagnostics", "conversation-steps.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(logData), "PMI-46") || strings.Contains(string(logData), "Fran explica") || !strings.Contains(string(logData), `"event":"router.decision"`) || !strings.Contains(string(logData), `"outcome":"intent_service_multi"`) {
		t.Fatalf("router diagnostic is missing or contains private content: %s", logData)
	}
}

func stringPointer(value string) *string { return &value }
