package secretary

import (
	"reflect"
	"testing"
	"time"
)

func TestInteractionMeaningForIntent(t *testing.T) {
	tests := []struct {
		name   string
		intent secretaryIntent
		text   string
		want   InteractionMeaning
	}{
		{name: "capability lookup", intent: intentCapability, text: "¿Qué se decidió en la última reunión?", want: InteractionLookup},
		{name: "explicit agreement", intent: intentGeneral, text: "Estoy de acuerdo contigo.", want: InteractionAgreement},
		{name: "explicit disagreement", intent: intentGeneral, text: "No estoy de acuerdo contigo.", want: InteractionNeutral},
		{name: "requested action", intent: intentAmbiguous, text: "Cierra la tarea STRAT-42, por favor.", want: InteractionAction},
		{name: "ordinary conversation", intent: intentGeneral, text: "Buenos días.", want: InteractionNeutral},
		{name: "ambiguous request", intent: intentAmbiguous, text: "¿Qué me toca ahora?", want: InteractionNeutral},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := interactionMeaning(tt.intent, tt.text); got != tt.want {
				t.Errorf("interactionMeaning(%q, %q) = %q, want %q", tt.intent, tt.text, got, tt.want)
			}
		})
	}
}

func TestDelayedProgressFinishesBeforeTerminalEventReturns(t *testing.T) {
	delayedStarted := make(chan struct{})
	releaseDelayed := make(chan struct{})
	delayedFinished := make(chan struct{})
	tracker := newInteractionProgressTracker(time.Millisecond, func(event InteractionEvent) {
		if event.Kind == EventWorkDelayed {
			close(delayedStarted)
			<-releaseDelayed
			close(delayedFinished)
		}
	})
	defer tracker.Close()

	select {
	case <-delayedStarted:
	case <-time.After(time.Second):
		t.Fatal("delayed event was not emitted")
	}

	terminalReturned := make(chan struct{})
	go func() {
		tracker.Observe(InteractionEvent{Kind: EventCompleted})
		close(terminalReturned)
	}()
	select {
	case <-terminalReturned:
		t.Fatal("terminal event returned while delayed feedback was still rendering")
	case <-time.After(20 * time.Millisecond):
	}

	close(releaseDelayed)
	select {
	case <-terminalReturned:
	case <-time.After(time.Second):
		t.Fatal("terminal event did not return after delayed feedback finished")
	}
	select {
	case <-delayedFinished:
	default:
		t.Fatal("terminal event returned before delayed feedback finished")
	}
}

func TestInteractionEventContainsOnlyChannelNeutralFields(t *testing.T) {
	want := []string{"Kind", "Meaning", "Source", "Action", "Stage", "Reason"}
	typeOfEvent := reflect.TypeOf(InteractionEvent{})
	got := make([]string, typeOfEvent.NumField())
	for i := range typeOfEvent.NumField() {
		got[i] = typeOfEvent.Field(i).Name
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("InteractionEvent fields = %v, want %v", got, want)
	}
}
