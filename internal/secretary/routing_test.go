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
			Name: "Rovo", Description: "tareas",
			Match: rovoTaskIntent,
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
		{name: "work task", query: "¿Qué tareas tengo pendientes?", want: []string{"Rovo"}},
		{name: "specific Jira issue fields", query: "Dame el estado, la persona asignada y el vencimiento de SW-76, actualizar importador de locations.", want: []string{"Rovo"}},
		{name: "meeting follow-up task", query: "¿Qué tareas debería hacer según las reuniones de esta mañana?", want: []string{"Granola", "Rovo"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			capabilities, ok, _ := routingServiceForTest().capabilitiesFor(tt.query, nil)
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

func TestMeetingTaskQuestionUsesGranolaAndRovo(t *testing.T) {
	query := "¿Tengo algo importante que ejecutar según las reuniones de esta mañana?"
	capabilities, ok, _ := routingServiceForTest().capabilitiesFor(query, nil)
	if !ok || len(capabilities) != 2 || capabilities[0].Name != "Granola" || capabilities[1].Name != "Rovo" {
		t.Fatalf("meeting task route = %v, %t; want Granola then Rovo", capabilityNames(capabilities), ok)
	}
}

func TestExplicitJiraIssueDoesNotInheritPriorMeetingContext(t *testing.T) {
	query := "Dame el estado actual y la persona asignada de SW-76."
	history := []Turn{{Role: "user", Content: "Resume la reunión de Stonecare del 14 de octubre."}}
	capabilities, ok, readQuery := routingServiceForTest().capabilitiesFor(query, history)
	if !ok || !reflect.DeepEqual(capabilityNames(capabilities), []string{"Rovo"}) {
		t.Fatalf("explicit Jira issue route = %v, %t; want only Rovo", capabilityNames(capabilities), ok)
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
