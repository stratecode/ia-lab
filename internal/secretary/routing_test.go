package secretary

import (
	"context"
	"reflect"
	"strings"
	"testing"
)

func routingServiceForTest() *Service {
	s := &Service{}
	s.Capabilities = []ReadCapability{
		{
			Name: "Granola", Description: "reuniones",
			Match: func(query string) bool {
				return localIntent(query) == intentCapability && containsAny(normalizedIntentText(query), "reunion", "reuniones", "granola")
			},
			Read: func(_ context.Context, _ string) (CapabilityResult, error) {
				return CapabilityResult{Text: "notas", ResultCount: 1}, nil
			},
		},
		{
			Name: "Jira", Description: "tareas",
			Match: jiraTaskIntent,
			Read: func(_ context.Context, _ string) (CapabilityResult, error) {
				return CapabilityResult{Text: "tareas", ResultCount: 1}, nil
			},
		},
	}
	return s
}

func TestCapabilitiesForIntentSelectsExpectedSources(t *testing.T) {
	tests := []struct {
		name  string
		query string
		want  []string
	}{
		{name: "meeting summary", query: "Dame el resumen de las reuniones de hoy", want: []string{"Granola"}},
		{name: "meeting owner question", query: "¿Quién revisa los textos en español e inglés de Stonecare según la reunión del 14 de octubre?", want: []string{"Granola"}},
		{name: "meeting assigned work phrased as obligation", query: "Según la reunión de Stonecare del 14 de octubre, ¿qué debía completar Fran y para cuándo?", want: []string{"Granola"}},
		{name: "work task", query: "¿Qué tareas tengo pendientes?", want: []string{"Jira"}},
		{name: "specific Jira issue fields", query: "Dame el estado, la persona asignada y el vencimiento de SW-76, actualizar importador de locations.", want: []string{"Jira"}},
		{name: "meeting follow-up task", query: "¿Qué tareas debería hacer según las reuniones de esta mañana?", want: []string{"Granola", "Jira"}},
		{name: "only Granola explicitly excludes Jira", query: "Solo Granola: dime una reunión reciente. No consultes Jira.", want: []string{"Granola"}},
		{name: "meeting question excludes Jira without only wording", query: "¿Qué se dijo en una reunión reciente? No consultes Jira.", want: []string{"Granola"}},
		{name: "only Jira explicitly excludes Granola", query: "Solo Jira: ¿qué estado tiene PMI-46? No consultes Granola.", want: []string{"Jira"}},
		{name: "both sources excluded in one coordinated phrase", query: "Prueba sintética; no consultes Jira ni Granola. Resume estas notas inventadas.", want: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			capabilities, ok, _ := routingServiceForTest().capabilitiesFor(tt.query, nil)
			if tt.want == nil {
				if ok || len(capabilities) != 0 {
					t.Fatalf("capabilitiesFor(%q) = %v, %t; want no source route", tt.query, capabilityNames(capabilities), ok)
				}
				return
			}
			if !ok {
				t.Fatalf("capabilitiesFor(%q) reported no route", tt.query)
			}
			got := make([]string, 0, len(capabilities))
			for _, capability := range capabilities {
				got = append(got, capability.Name)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("capabilitiesFor(%q) = %v, want %v", tt.query, got, tt.want)
			}
		})
	}
}

func TestJiraPersonQuestionAndVerificationKeepUserProjectContext(t *testing.T) {
	history := []Turn{
		{Role: "user", Content: "Solo Granola: resume la daily."},
		{Role: "assistant", Content: "Nota de Granola."},
		{Role: "user", Content: "Solo Jira: consulta PMI-46."},
		{Role: "assistant", Content: "PMI-46 verificado."},
		{Role: "user", Content: "¿En qué está trabajando hoy Valentina?"},
		{Role: "assistant", Content: "Valentina estabiliza el despliegue Oracle."},
	}
	for _, query := range []string{
		"¿En qué está trabajando hoy Valentina?",
		"Creo que te lo has inventado. Verifícalo directamente en Jira.",
	} {
		capabilities, matched, readQuery := routingServiceForTest().capabilitiesFor(query, history)
		if !matched || !reflect.DeepEqual(capabilityNames(capabilities), []string{"Jira"}) {
			t.Fatalf("capabilitiesFor(%q) = %v, %t; want Jira only", query, capabilityNames(capabilities), matched)
		}
		if !strings.Contains(readQuery, "Valentina") || !strings.Contains(readQuery, "PMI") || !strings.Contains(readQuery, "en curso") {
			t.Errorf("read query lost user, project, or current-work scope: %q", readQuery)
		}
		if strings.Contains(strings.ToLower(readQuery), "oracle") {
			t.Errorf("assistant-generated claim leaked into Jira query: %q", readQuery)
		}
	}
}

func TestValentinaCurrentWorkDefaultsToPMIWithoutChatHistory(t *testing.T) {
	query := "Solo Jira: ¿qué tareas tiene Valentina en curso?"
	capabilities, matched, readQuery := routingServiceForTest().capabilitiesFor(query, nil)
	if !matched || !reflect.DeepEqual(capabilityNames(capabilities), []string{"Jira"}) {
		t.Fatalf("capabilitiesFor(%q) = %v, %t; want Jira only", query, capabilityNames(capabilities), matched)
	}
	if !strings.Contains(readQuery, "Valentina") || !strings.Contains(readQuery, "Proyecto PMI") || !strings.Contains(readQuery, "en curso") {
		t.Fatalf("read query lost Valentina's saved PMI scope: %q", readQuery)
	}
}

func TestMeetingTaskQuestionUsesGranolaAndJira(t *testing.T) {
	query := "¿Tengo algo importante que ejecutar según las reuniones de esta mañana?"
	capabilities, ok, _ := routingServiceForTest().capabilitiesFor(query, nil)
	if !ok || len(capabilities) != 2 || capabilities[0].Name != "Granola" || capabilities[1].Name != "Jira" {
		t.Fatalf("meeting task route = %v, %t; want Granola then Jira", capabilityNames(capabilities), ok)
	}
}

func TestExplicitJiraIssueDoesNotInheritPriorMeetingContext(t *testing.T) {
	query := "Dame el estado actual y la persona asignada de SW-76."
	history := []Turn{{Role: "user", Content: "Resume la reunión de Stonecare del 14 de octubre."}}
	capabilities, ok, readQuery := routingServiceForTest().capabilitiesFor(query, history)
	if !ok || !reflect.DeepEqual(capabilityNames(capabilities), []string{"Jira"}) {
		t.Fatalf("explicit Jira issue route = %v, %t; want only Jira", capabilityNames(capabilities), ok)
	}
	if readQuery != query {
		t.Fatalf("explicit Jira query inherited meeting context: %q", readQuery)
	}
}

func TestMeetingFollowUpResolvesAnaphoraWithinRecentHistory(t *testing.T) {
	anchor := "Hazme un resumen ejecutivo de la reunión de Stonecare del 14 de octubre."
	query := "¿Qué tareas quedaron asignadas a cada persona en esa misma reunión?"
	history := []Turn{{Role: "user", Content: anchor}}
	for i := 0; i < 5; i++ {
		history = append(history, Turn{Role: "user", Content: "consulta intermedia sin referencia a una reunión"})
	}
	_, ok, readQuery := routingServiceForTest().capabilitiesFor(query, history)
	if !ok {
		t.Fatal("meeting follow-up did not route")
	}
	if !strings.Contains(readQuery, "Stonecare del 14 de octubre") || !strings.Contains(readQuery, query) {
		t.Fatalf("follow-up query lost its meeting anchor: %q", readQuery)
	}
}

func TestCrossSourceAnaphoricFollowUpKeepsMeetingContextBesideExplicitJiraKey(t *testing.T) {
	anchor := "Solo Granola: en la daily del 26 de octubre, ¿qué modelo de datos tenía que explicar Fran a Gonzalo y cuáles eran las dos tablas?"
	query := "¿Eso está vinculado con PMI-46? Contrasta ambas fuentes."
	history := []Turn{{Role: "user", Content: anchor}}
	for i := 0; i < 2; i++ {
		history = append(history, Turn{Role: "user", Content: "consulta intermedia sin referencia a reuniones"})
	}
	capabilities, ok, readQuery := routingServiceForTest().capabilitiesFor(query, history)
	if !ok || !reflect.DeepEqual(capabilityNames(capabilities), []string{"Granola", "Jira"}) {
		t.Fatalf("cross-source follow-up route = %v, %t; want Granola and Jira", capabilityNames(capabilities), ok)
	}
	if !strings.Contains(readQuery, "daily del 26 de octubre") || !strings.Contains(readQuery, "PMI-46") || !strings.Contains(readQuery, "dos tablas") {
		t.Fatalf("follow-up lost meeting details or issue anchor: %q", readQuery)
	}
}

func TestExplicitDailyFollowUpKeepsMeetingContextBesideExplicitJiraKey(t *testing.T) {
	anchor := "Solo Granola: en la daily del 26 de octubre, ¿qué modelo de datos tenía que explicar Fran a Gonzalo y cuáles eran las dos tablas?"
	query := "¿Puedes relacionar la acción de explicar a Gonzalo las dos tablas, mencionada en la daily del 26 de octubre, con PMI-46? Consulta ambas fuentes."
	history := []Turn{
		{Role: "user", Content: anchor},
		{Role: "assistant", Content: "Fran debía explicar el modelo de datos."},
		{Role: "user", Content: "¿Puedes relacionar la acción de explicar a Gonzalo las dos tablas, mencionada en la daily del 26 de octubre, con PMI-46?"},
	}
	capabilities, ok, readQuery := routingServiceForTest().capabilitiesFor(query, history)
	if !ok || !reflect.DeepEqual(capabilityNames(capabilities), []string{"Granola", "Jira"}) {
		t.Fatalf("explicit daily cross-source route = %v, %t; want Granola and Jira", capabilityNames(capabilities), ok)
	}
	if !strings.Contains(readQuery, "daily del 26 de octubre") || !strings.Contains(readQuery, "PMI-46") {
		t.Fatalf("explicit daily comparison lost an anchor: %q", readQuery)
	}
}

func TestMeetingSummaryUsesGranolaOnly(t *testing.T) {
	capabilities, ok, _ := routingServiceForTest().capabilitiesFor("Resume las reuniones de esta mañana", nil)
	if !ok || len(capabilities) != 1 || capabilities[0].Name != "Granola" {
		t.Fatalf("meeting summary route = %v, %t; want Granola only", capabilityNames(capabilities), ok)
	}
}

func TestAmbiguousCapabilityMatchFailsClosed(t *testing.T) {
	s := &Service{Capabilities: []ReadCapability{
		{Name: "Uno", Match: func(string) bool { return true }, Read: func(context.Context, string) (CapabilityResult, error) { return CapabilityResult{}, nil }},
		{Name: "Dos", Match: func(string) bool { return true }, Read: func(context.Context, string) (CapabilityResult, error) { return CapabilityResult{}, nil }},
	}}
	if capabilities, ok, _ := s.capabilitiesFor("consulta ambigua", nil); ok || len(capabilities) != 0 {
		t.Fatalf("ambiguous route = %v, %t; want no capabilities and false", capabilityNames(capabilities), ok)
	}
}

func capabilityNames(capabilities []ReadCapability) []string {
	names := make([]string, 0, len(capabilities))
	for _, capability := range capabilities {
		names = append(names, capability.Name)
	}
	return names
}
