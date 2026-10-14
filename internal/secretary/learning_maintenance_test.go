package secretary

import (
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestForgetRemovesLearningDataAndConversationMemory(t *testing.T) {
	state := t.TempDir()
	s := New("", "", "", state, nil)
	now := time.Now().UTC()
	trace := learningTestTrace(7, "telegram", conversationDigest("telegram", "chat-a"), now)
	if err := s.Learning.AppendTrace(trace); err != nil {
		t.Fatal(err)
	}
	stored, ok, err := s.Learning.LastTrace(7, "telegram", trace.ConversationRef)
	if err != nil || !ok {
		t.Fatalf("trace = %+v, %t, %v", stored, ok, err)
	}
	if err := s.Learning.AppendFeedback(FeedbackEvent{InteractionID: stored.ID, OperatorID: 7, At: now, Label: FeedbackCorrect}); err != nil {
		t.Fatal(err)
	}
	if err := s.Learning.SaveKnowledge(KnowledgeItem{OperatorID: 7, Type: KnowledgeTypeFact, Value: "privado", Status: KnowledgeApproved, CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := s.Learning.AppendTrace(learningTestTrace(8, "telegram", "other-chat", now)); err != nil {
		t.Fatal(err)
	}
	if err := s.appendMemoryAt(7, "telegram", "chat-a", "pregunta privada", "respuesta privada", now); err != nil {
		t.Fatal(err)
	}
	if err := atomicJSON(s.historyPathFor(7, "telegram", "chat-a"), []Turn{{Role: "user", Content: "historial"}}); err != nil {
		t.Fatal(err)
	}
	s.history = map[string][]Turn{"7:telegram:chat-a": {{Role: "user", Content: "in-memory"}}}
	if err := s.forget(7); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := s.Learning.LastTrace(7, "telegram", trace.ConversationRef); err != nil || ok {
		t.Fatalf("trace survived forget: %t %v", ok, err)
	}
	items, err := s.Learning.ListKnowledge(7, "")
	if err != nil || len(items) != 0 {
		t.Fatalf("knowledge survived forget: %+v %v", items, err)
	}
	if _, ok, err := s.Learning.LastTrace(8, "telegram", "other-chat"); err != nil || !ok {
		t.Fatalf("forget removed another operator's data: %t %v", ok, err)
	}
	if _, ok := s.history["7:telegram:chat-a"]; ok {
		t.Fatal("in-memory history survived forget")
	}
	for _, path := range []string{s.dailyMemoryDir(7), s.legacyMemoryPath(7), s.historyPathFor(7, "telegram", "chat-a"), filepath.Join(state, "learning", "traces", "7"), filepath.Join(state, "learning", "feedback", "7"), filepath.Join(state, "learning", "knowledge", "7")} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("path remains after forget: %s (%v)", path, err)
		}
	}
}

func TestLearningMaintenancePrunesOnStartAndDaily(t *testing.T) {
	state := t.TempDir()
	s := New("", "", "", state, nil)
	oldAt := time.Now().UTC().AddDate(0, 0, -100)
	if err := s.Learning.AppendTrace(learningTestTrace(7, "telegram", "old-at-start", oldAt)); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		s.runLearningMaintenance(ctx, LearningRetention{TraceDays: 90, CandidateDays: 30}, 20*time.Millisecond)
	}()
	deadline := time.After(2 * time.Second)
	for {
		if _, ok, err := s.Learning.LastTrace(7, "telegram", "old-at-start"); err != nil {
			t.Fatal(err)
		} else if !ok {
			break
		}
		select {
		case <-deadline:
			t.Fatal("startup maintenance did not prune expired trace")
		case <-time.After(10 * time.Millisecond):
		}
	}
	if err := s.Learning.AppendTrace(learningTestTrace(7, "telegram", "old-next-day", oldAt)); err != nil {
		t.Fatal(err)
	}
	for {
		if _, ok, err := s.Learning.LastTrace(7, "telegram", "old-next-day"); err != nil {
			t.Fatal(err)
		} else if !ok {
			break
		}
		select {
		case <-deadline:
			t.Fatal("scheduled maintenance did not prune expired trace")
		case <-time.After(10 * time.Millisecond):
		}
	}
	cancel()
	<-done
}

type failingPruneStore struct{ LearningStore }

func (f failingPruneStore) Prune(time.Time, int, int) error { return os.ErrPermission }

func TestLearningMaintenanceFailureIsMetricOnly(t *testing.T) {
	s := New("", "", "", t.TempDir(), nil)
	s.Learning = failingPruneStore{LearningStore: s.Learning}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		s.runLearningMaintenance(ctx, LearningRetention{TraceDays: 90, CandidateDays: 30}, time.Hour)
	}()
	select {
	case <-done:
		t.Fatal("maintenance should continue until context cancellation")
	case <-time.After(20 * time.Millisecond):
	}
	w := httptest.NewRecorder()
	s.VoiceMetrics().Handler().ServeHTTP(w, httptest.NewRequest("GET", "/metrics", nil))
	if !strings.Contains(w.Body.String(), "lab_secretary_learning_maintenance_errors_total 1") {
		t.Fatalf("maintenance error metric missing: %s", w.Body.String())
	}
	cancel()
	<-done
}
