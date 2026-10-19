package secretary

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func feedbackTestService(t *testing.T) (*Service, *JSONLLearningStore) {
	t.Helper()
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"La respuesta contrastada."}}]}`))
	}))
	t.Cleanup(model.Close)
	state := t.TempDir()
	service := New(model.URL, "test-key", "instructions", state, map[int64]bool{7: true, 8: true})
	store := NewJSONLLearningStore(state)
	service.Learning = store
	service.Capabilities = []ReadCapability{{Name: "Jira", Description: "tareas", Match: func(string) bool { return true }, Read: func(context.Context, string) (CapabilityResult, error) {
		return CapabilityResult{Text: "ISSUE-17 pendiente", ResultCount: 1, Evidence: []EvidenceRef{{Provider: "jira", ID: "ISSUE-17"}}}, nil
	}}}
	return service, store
}

func createFeedbackTestTrace(t *testing.T, s *Service, channel, conversation string) InteractionTrace {
	t.Helper()
	if _, err := s.ReplyInConversation(context.Background(), 7, channel, conversation, "consulta tarea abierta"); err != nil {
		t.Fatal(err)
	}
	trace, ok, err := s.Learning.LastTrace(7, channel, conversationDigest(channel, conversation))
	if err != nil || !ok {
		t.Fatalf("trace = %+v, %t, %v", trace, ok, err)
	}
	return trace
}

func TestFeedbackIsScopedToOperatorChannelAndConversation(t *testing.T) {
	s, store := feedbackTestService(t)
	trace := createFeedbackTestTrace(t, s, "telegram", "chat-a")
	for _, tc := range []struct {
		operator              int64
		channel, conversation string
	}{
		{7, "telegram", "chat-b"}, {7, "openwebui", "chat-a"}, {8, "telegram", "chat-a"},
	} {
		if err := s.SubmitFeedback(tc.operator, tc.channel, tc.conversation, FeedbackInput{Label: FeedbackCorrect}); err == nil {
			t.Errorf("feedback crossed operator/channel/conversation boundary: %+v", tc)
		}
	}
	if err := s.SubmitFeedback(7, "telegram", "chat-a", FeedbackInput{Label: FeedbackCorrect}); err != nil {
		t.Fatal(err)
	}
	items, err := store.ListKnowledge(7, KnowledgeCandidate)
	if err != nil || len(items) != 0 {
		t.Fatalf("positive feedback created candidate: %+v, %v (trace %s)", items, err, trace.ID)
	}
}

func TestFeedbackCommandDoesNotCreateTrace(t *testing.T) {
	s, _ := feedbackTestService(t)
	before := createFeedbackTestTrace(t, s, "telegram", "chat-a")
	answer, err := s.ReplyInConversation(context.Background(), 7, "telegram", "chat-a", "/feedback bien")
	if err != nil || !strings.Contains(answer, "marcada como correcta") {
		t.Fatalf("feedback command = %q, %v", answer, err)
	}
	after, ok, err := s.Learning.LastTrace(7, "telegram", conversationDigest("telegram", "chat-a"))
	if err != nil || !ok || after.ID != before.ID {
		t.Fatalf("feedback generated a trace: before=%+v after=%+v %t %v", before, after, ok, err)
	}
	paths, err := filepath.Glob(filepath.Join(s.StateDir, "learning", "feedback", "7", "*.jsonl"))
	if err != nil || len(paths) != 1 {
		t.Fatalf("feedback records = %v, %v", paths, err)
	}
	raw, err := os.ReadFile(paths[0])
	if err != nil {
		t.Fatal(err)
	}
	var event FeedbackEvent
	if err := json.Unmarshal(raw[:len(raw)-1], &event); err != nil {
		t.Fatal(err)
	}
	if event.InteractionID != before.ID || event.Label != FeedbackCorrect {
		t.Fatalf("feedback event = %+v", event)
	}
}

func TestFeedbackCreatesUnapprovedCandidate(t *testing.T) {
	s, store := feedbackTestService(t)
	_ = createFeedbackTestTrace(t, s, "telegram", "chat-a")
	answer, err := s.ReplyInConversation(context.Background(), 7, "telegram", "chat-a", "/feedback corrige: La tarea ISSUE-17 ya está cerrada")
	if err != nil || !strings.Contains(answer, "aún no la usaré") {
		t.Fatalf("correction response = %q, %v", answer, err)
	}
	items, err := store.ListKnowledge(7, KnowledgeCandidate)
	if err != nil || len(items) != 1 || items[0].Status != KnowledgeCandidate || items[0].Value != "La tarea ISSUE-17 ya está cerrada" || len(items[0].Sources) != 1 {
		t.Fatalf("candidate = %+v, %v", items, err)
	}
}

func TestKnowledgeReviewRequiresValidCandidate(t *testing.T) {
	s, store := feedbackTestService(t)
	_ = createFeedbackTestTrace(t, s, "openwebui", "chat-a")
	if _, err := s.ReplyInConversation(context.Background(), 7, "openwebui", "chat-a", "/feedback mal: estado de Jira desactualizado"); err != nil {
		t.Fatal(err)
	}
	pending, err := store.ListKnowledge(7, KnowledgeCandidate)
	if err != nil || len(pending) != 1 {
		t.Fatalf("pending = %+v, %v", pending, err)
	}
	listed, err := s.ReplyInConversation(context.Background(), 7, "openwebui", "chat-a", "/aprendizaje")
	if err != nil || !strings.Contains(listed, pending[0].ID) {
		t.Fatalf("/aprendizaje = %q, %v", listed, err)
	}
	if err := s.ReviewKnowledge(7, "/aprueba", "not-owned-or-existing"); err == nil {
		t.Fatal("unknown candidate was approved")
	}
	if err := s.ReviewKnowledge(8, "/aprueba", pending[0].ID); err == nil {
		t.Fatal("candidate was approved by another operator")
	}
	approvedMessage, err := s.ReplyInConversation(context.Background(), 7, "openwebui", "chat-a", "/aprueba "+pending[0].ID)
	if err != nil || !strings.Contains(approvedMessage, "Corrección aprobada") {
		t.Fatalf("/aprueba = %q, %v", approvedMessage, err)
	}
	approved, err := store.ListKnowledge(7, KnowledgeApproved)
	if err != nil || len(approved) != 1 || approved[0].ID != pending[0].ID {
		t.Fatalf("approved = %+v, %v", approved, err)
	}
	if _, err := s.ReplyInConversation(context.Background(), 7, "openwebui", "chat-a", "/feedback corrige: el cliente no es el proyecto"); err != nil {
		t.Fatal(err)
	}
	pending, err = store.ListKnowledge(7, KnowledgeCandidate)
	if err != nil || len(pending) != 1 {
		t.Fatalf("second pending = %+v, %v", pending, err)
	}
	discarded, err := s.ReplyInConversation(context.Background(), 7, "openwebui", "chat-a", "/descarta "+pending[0].ID)
	if err != nil || !strings.Contains(discarded, "descartada") {
		t.Fatalf("/descarta = %q, %v", discarded, err)
	}
	rejected, err := store.ListKnowledge(7, KnowledgeRejected)
	if err != nil || len(rejected) != 1 || rejected[0].ID != pending[0].ID {
		t.Fatalf("rejected = %+v, %v", rejected, err)
	}
}
