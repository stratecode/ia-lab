package secretary

import (
	"testing"
	"time"
)

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
