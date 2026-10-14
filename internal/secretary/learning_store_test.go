package secretary

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func learningTestTrace(operatorID int64, channel, conversation string, at time.Time) InteractionTrace {
	return InteractionTrace{
		OperatorID: operatorID, At: at, Channel: channel, ConversationRef: conversation,
		Intent: "tool", Meaning: "lookup", Outcome: "success",
		Capabilities: []CapabilityOutcome{{Capability: "Granola", Outcome: "success", ResultCount: 1}},
	}
}

func TestJSONLLearningStoreRoundTripsVersionedRecords(t *testing.T) {
	store := NewJSONLLearningStore(t.TempDir())
	now := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	trace := learningTestTrace(7, "telegram", "chat-a", now)
	if err := store.AppendTrace(trace); err != nil {
		t.Fatal(err)
	}
	got, ok, err := store.LastTrace(7, "telegram", trace.ConversationRef)
	if err != nil || !ok {
		t.Fatalf("LastTrace() = %+v, %t, %v", got, ok, err)
	}
	if got.SchemaVersion != learningSchemaVersion || got.ID == "" || got.OperatorID != 7 || got.Capabilities[0].Capability != "Granola" {
		t.Fatalf("trace did not round-trip with schema and identity: %+v", got)
	}
	if err := store.AppendFeedback(FeedbackEvent{InteractionID: got.ID, OperatorID: 7, At: now, Label: FeedbackCorrect}); err != nil {
		t.Fatal(err)
	}
	item := KnowledgeItem{OperatorID: 7, Type: KnowledgeTypeFact, Subject: "release", Predicate: "date", Value: "viernes", Status: KnowledgeApproved, CreatedAt: now}
	if err := store.SaveKnowledge(item); err != nil {
		t.Fatal(err)
	}
	items, err := store.ListKnowledge(7, KnowledgeApproved)
	if err != nil || len(items) != 1 || items[0].SchemaVersion != learningSchemaVersion || items[0].ID == "" || items[0].Value != "viernes" {
		t.Fatalf("knowledge round trip = %+v, %v", items, err)
	}
}

func TestJSONLLearningStoreSetsPrivatePermissions(t *testing.T) {
	root := t.TempDir()
	store := NewJSONLLearningStore(root)
	if err := store.AppendTrace(learningTestTrace(7, "telegram", "chat-a", time.Now())); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{filepath.Join(root, "learning"), filepath.Join(root, "learning", "traces"), filepath.Join(root, "learning", "traces", "7")} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat directory %s: %v", path, err)
		}
		if got := info.Mode().Perm(); got != 0700 {
			t.Fatalf("directory %s mode = %04o, want 0700", path, got)
		}
	}
	files, err := filepath.Glob(filepath.Join(root, "learning", "traces", "7", "*.jsonl"))
	if err != nil || len(files) != 1 {
		t.Fatalf("trace files = %v, %v", files, err)
	}
	info, err := os.Stat(files[0])
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("trace file mode = %v, %v; want 0600", info, err)
	}
}

func TestLearningStoreForgetAndRetention(t *testing.T) {
	store := NewJSONLLearningStore(t.TempDir())
	now := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	old := now.AddDate(0, 0, -100)
	oldTrace := learningTestTrace(7, "telegram", "old-chat", old)
	oldTrace.ID = "old-trace"
	if err := store.AppendTrace(oldTrace); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendFeedback(FeedbackEvent{InteractionID: oldTrace.ID, OperatorID: 7, At: old, Label: FeedbackIncorrect}); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendTrace(learningTestTrace(7, "telegram", "recent-chat", now)); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendTrace(learningTestTrace(8, "telegram", "other-operator", now)); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveKnowledge(KnowledgeItem{OperatorID: 7, Type: KnowledgeTypeFact, Status: KnowledgeCandidate, CreatedAt: now.AddDate(0, 0, -31), Value: "candidate"}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveKnowledge(KnowledgeItem{OperatorID: 7, Type: KnowledgeTypeFact, Status: KnowledgeApproved, CreatedAt: old, Value: "approved without expiry"}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveKnowledge(KnowledgeItem{OperatorID: 7, Type: KnowledgeTypeFact, Status: KnowledgeApproved, CreatedAt: old, ValidUntil: old, Value: "approved but expired"}); err != nil {
		t.Fatal(err)
	}
	if err := store.Prune(now, 90, 30); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := store.LastTrace(7, "telegram", oldTrace.ConversationRef); err != nil || ok {
		t.Fatalf("expired trace survived pruning: found=%t err=%v", ok, err)
	}
	if _, ok, err := store.LastTrace(8, "telegram", "other-operator"); err != nil || !ok {
		t.Fatalf("other operator trace was pruned: found=%t err=%v", ok, err)
	}
	items, err := store.ListKnowledge(7, KnowledgeApproved)
	if err != nil || len(items) != 1 || items[0].Value != "approved without expiry" {
		t.Fatalf("approved knowledge retention = %+v, %v", items, err)
	}
	if err := store.Forget(7); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := store.LastTrace(7, "telegram", "recent-chat"); err != nil || ok {
		t.Fatalf("operator trace survived forget: found=%t err=%v", ok, err)
	}
	if _, ok, err := store.LastTrace(8, "telegram", "other-operator"); err != nil || !ok {
		t.Fatalf("forget removed another operator's trace: found=%t err=%v", ok, err)
	}
}

func TestLearningStoreLastTraceUsesConversationScope(t *testing.T) {
	store := NewJSONLLearningStore(t.TempDir())
	now := time.Now().UTC()
	first := learningTestTrace(7, "telegram", "conversation-a", now)
	second := learningTestTrace(7, "telegram", "conversation-b", now.Add(time.Second))
	first.Intent, second.Intent = "first", "second"
	if err := store.AppendTrace(first); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendTrace(second); err != nil {
		t.Fatal(err)
	}
	got, ok, err := store.LastTrace(7, "telegram", "conversation-a")
	if err != nil || !ok || got.Intent != "first" {
		t.Fatalf("conversation scope returned %+v, %t, %v", got, ok, err)
	}
}
