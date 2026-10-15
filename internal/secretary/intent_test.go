package secretary

import "testing"

func TestRovoTaskIntentCoversWorkQuestionsButNotMeetingSummaries(t *testing.T) {
	tests := []struct {
		name string
		text string
		want bool
	}{
		{name: "pending tasks with meeting context", text: "¿Qué tareas tengo pendientes según las reuniones de esta mañana?", want: true},
		{name: "action items with meeting context", text: "¿Qué acciones se acordaron hoy?", want: true},
		{name: "execution request with meeting context", text: "¿Qué tengo que ejecutar según las reuniones de esta mañana?", want: true},
		{name: "planning", text: "Ayúdame a revisar el plan de la semana.", want: true},
		{name: "meeting summary", text: "Resúmeme las reuniones de esta mañana.", want: false},
		{name: "follow-up wording", text: "¿Qué cosas tenemos que hacer para hoy?", want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := rovoTaskIntent(tt.text); got != tt.want {
				t.Fatalf("rovoTaskIntent(%q) = %t, want %t", tt.text, got, tt.want)
			}
		})
	}
}

func TestMeetingImperativeSummaryIsRecognizedLocally(t *testing.T) {
	if got := localIntent("Resume las reuniones que he tenido."); got != intentCapability {
		t.Fatalf("localIntent(summary imperative) = %q, want %q", got, intentCapability)
	}
}

func TestNamedMeetingFollowUpIsRecognizedAsGranolaIntent(t *testing.T) {
	question := "¿Quién revisa los textos en español e inglés de Stonecare según la reunión del 14 de octubre?"
	if got := localIntent(question); got != intentCapability {
		t.Fatalf("localIntent(%q) = %q, want %q", question, got, intentCapability)
	}
}

func TestMeetingActionQuestionWithoutSourceNameRoutesToCapability(t *testing.T) {
	question := "En la reunión de Stonecare del 14 de octubre, ¿qué tenía que enviar Paula y Alba y para qué?"
	if got := localIntent(question); got != intentCapability {
		t.Fatalf("localIntent(%q) = %q, want %q", question, got, intentCapability)
	}
}
