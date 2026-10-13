package secretary

import (
	"sync"
	"testing"
	"time"
)

func TestInteractionProgressReportsLatestStageOnceAfterDelay(t *testing.T) {
	delayed := make(chan InteractionEvent, 2)
	tracker := newInteractionProgressTracker(120*time.Millisecond, func(event InteractionEvent) {
		if event.Kind == EventWorkDelayed {
			delayed <- event
		}
	})
	tracker.Observe(InteractionEvent{Kind: EventIntentRecognized, Meaning: InteractionLookup})
	tracker.Observe(InteractionEvent{Kind: EventLookupStarted, Meaning: InteractionLookup, Source: "Granola"})
	time.Sleep(30 * time.Millisecond)
	tracker.Observe(InteractionEvent{Kind: EventStageChanged, Stage: "sintetizando la respuesta"})
	defer tracker.Close()

	select {
	case event := <-delayed:
		if event.Kind != EventWorkDelayed || event.Stage != "sintetizando la respuesta" {
			t.Fatalf("delayed interaction = %+v", event)
		}
	case <-time.After(time.Second):
		t.Fatal("expected a delayed interaction event")
	}

	select {
	case event := <-delayed:
		t.Fatalf("unexpected repeated delayed interaction: %+v", event)
	case <-time.After(160 * time.Millisecond):
	}
}

func TestInteractionProgressSuppressesUpdateWhenWorkFinishesBeforeDelay(t *testing.T) {
	var mu sync.Mutex
	var events []InteractionEvent
	tracker := newInteractionProgressTracker(80*time.Millisecond, func(event InteractionEvent) {
		mu.Lock()
		defer mu.Unlock()
		events = append(events, event)
	})
	tracker.Observe(InteractionEvent{Kind: EventIntentRecognized, Meaning: InteractionNeutral})
	tracker.Observe(InteractionEvent{Kind: EventCompleted})
	time.Sleep(120 * time.Millisecond)
	tracker.Close()

	mu.Lock()
	defer mu.Unlock()
	for _, event := range events {
		if event.Kind == EventWorkDelayed {
			t.Fatalf("fast completion emitted delayed progress: %+v", events)
		}
	}
}

func TestInteractionProgressDefaultsToThirtySeconds(t *testing.T) {
	tracker := newInteractionProgressTracker(0, nil)
	defer tracker.Close()
	if tracker.delay != 30*time.Second {
		t.Fatalf("default interaction delay = %s, want 30s", tracker.delay)
	}
}

func TestProgressTrackerReportsLatestStageOnceAfterDelay(t *testing.T) {
	updates := make(chan string, 2)
	tracker := newProgressTracker(150*time.Millisecond, func(stage string) {
		updates <- stage
	})
	tracker.SetStage("interpretando la consulta")
	time.Sleep(30 * time.Millisecond)
	tracker.SetStage("consultando Granola")
	defer tracker.Close()

	select {
	case got := <-updates:
		if got != "Sigo con ello: consultando Granola." {
			t.Fatalf("delayed progress = %q", got)
		}
	case <-time.After(time.Second):
		t.Fatal("expected a delayed progress update")
	}

	select {
	case got := <-updates:
		t.Fatalf("unexpected second progress update: %q", got)
	case <-time.After(200 * time.Millisecond):
	}
}

func TestProgressTrackerSuppressesUpdateWhenWorkFinishesBeforeDelay(t *testing.T) {
	updates := make(chan string, 1)
	tracker := newProgressTracker(50*time.Millisecond, func(stage string) {
		updates <- stage
	})
	tracker.SetStage("preparando la respuesta")
	tracker.Close()
	time.Sleep(80 * time.Millisecond)
	select {
	case got := <-updates:
		t.Fatalf("unexpected progress update after completion: %q", got)
	default:
	}
}

func TestTelegramAcknowledgementVariesBetweenRequests(t *testing.T) {
	bot := &Bot{}
	first := bot.nextAcknowledgement()
	second := bot.nextAcknowledgement()
	if first == second {
		t.Fatalf("consecutive acknowledgements should vary, got %q twice", first)
	}
}
