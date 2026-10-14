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
		{name: "planning", text: "Ayúdame a revisar el plan de la semana.", want: true},
		{name: "meeting summary", text: "Resúmeme las reuniones de esta mañana.", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := rovoTaskIntent(tt.text); got != tt.want {
				t.Fatalf("rovoTaskIntent(%q) = %t, want %t", tt.text, got, tt.want)
			}
		})
	}
}
