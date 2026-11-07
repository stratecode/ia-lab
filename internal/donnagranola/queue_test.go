package donnagranola

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestFileEventQueuePersistsAndDeduplicatesAcrossRestart(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	queue, err := OpenEventQueue(dir)
	if err != nil {
		t.Fatal(err)
	}
	event := GranolaEvent{EventID: "evt_abc", NoteID: "not_1d3tmYTlCICgjy", EventType: EventNoteGenerated, Scope: ScopePersonal, ReceivedAt: time.Now().UTC()}
	first, err := queue.Enqueue(ctx, event)
	if err != nil || !first {
		t.Fatalf("first enqueue = %v, %v", first, err)
	}
	queue, err = OpenEventQueue(dir)
	if err != nil {
		t.Fatal(err)
	}
	second, err := queue.Enqueue(ctx, event)
	if err != nil || second {
		t.Fatalf("duplicate enqueue = %v, %v", second, err)
	}
	got, err := queue.Next(ctx)
	if err != nil || got.EventID != event.EventID || got.NoteID != event.NoteID {
		t.Fatalf("Next() = %#v, %v", got, err)
	}
	if err := queue.Complete(event.EventID); err != nil {
		t.Fatal(err)
	}
	queue, err = OpenEventQueue(dir)
	if err != nil {
		t.Fatal(err)
	}
	third, err := queue.Enqueue(ctx, event)
	if err != nil || third {
		t.Fatalf("completed event enqueue = %v, %v", third, err)
	}
}

func TestEventRetryAndFailureStateSurviveRestart(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	queue, err := OpenEventQueue(dir)
	if err != nil {
		t.Fatal(err)
	}
	event := GranolaEvent{EventID: "evt_retry_state", NoteID: "not_1d3tmYTlCICgjy", EventType: EventNoteGenerated, Scope: ScopePersonal, ReceivedAt: time.Now().UTC()}
	if _, err := queue.Enqueue(ctx, event); err != nil {
		t.Fatal(err)
	}
	if err := queue.Retry(event.EventID, 0); err != nil {
		t.Fatal(err)
	}
	queue, err = OpenEventQueue(dir)
	if err != nil {
		t.Fatal(err)
	}
	queued, err := queue.Next(ctx)
	if err != nil || queued.Attempts != 1 {
		t.Fatalf("retry metadata = %#v, %v", queued, err)
	}
	if err := queue.Fail(event.EventID, errors.New("test failure")); err != nil {
		t.Fatal(err)
	}
	queue, err = OpenEventQueue(dir)
	if err != nil {
		t.Fatal(err)
	}
	if first, err := queue.Enqueue(ctx, event); err != nil || first {
		t.Fatalf("failed event was not deduplicated: %v, %v", first, err)
	}
}
