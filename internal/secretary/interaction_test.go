package secretary

import (
	"reflect"
	"testing"
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
