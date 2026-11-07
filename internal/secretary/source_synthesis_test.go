package secretary

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMultiSourceSynthesisKeepsDecisionStatusOwnerAndDates(t *testing.T) {
	const question = "Resume Jira y Granola: decisión, cambio de estado, responsable y fechas del proyecto Nube."
	const jiraText = "NUBE-104 — Estado: bloqueada; sigue esperando la validación legal. Responsable: Paula. Fecha objetivo: 14 de noviembre."
	const granolaText = "Decisión: mantener el despliegue de Nube para el 14 de noviembre. Cambio de estado: la validación legal llegó el 29 de octubre y el bloqueo queda resuelto. Paula revisará el expediente antes del 12 de noviembre."
	var modelCalls int
	var receivedPrompt string
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		modelCalls++
		var request struct {
			Messages []Turn `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		if len(request.Messages) > 0 {
			receivedPrompt = request.Messages[0].Content
		}
		if strings.Contains(receivedPrompt, "citas exactas") {
			_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"{\"facts\":[{\"source\":\"Jira\",\"evidence_id\":\"NUBE-104\",\"category\":\"status\",\"quote\":\"Estado: bloqueada; sigue esperando la validación legal\"},{\"source\":\"Jira\",\"evidence_id\":\"NUBE-104\",\"category\":\"owner\",\"quote\":\"Responsable: Paula\"},{\"source\":\"Jira\",\"evidence_id\":\"NUBE-104\",\"category\":\"date\",\"quote\":\"Fecha objetivo: 14 de noviembre\"},{\"source\":\"Granola\",\"evidence_id\":\"meeting-104\",\"category\":\"decision\",\"quote\":\"Decisión: mantener el despliegue de Nube para el 14 de noviembre\"},{\"source\":\"Granola\",\"evidence_id\":\"meeting-104\",\"category\":\"status\",\"quote\":\"Cambio de estado: la validación legal llegó el 29 de octubre y el bloqueo queda resuelto\"},{\"source\":\"Granola\",\"evidence_id\":\"meeting-104\",\"category\":\"owner\",\"quote\":\"Paula revisará el expediente antes del 12 de noviembre\"}] }"}}]}`)
			return
		}
		// Reproduce el fallo anterior: la fuente está en el contexto, pero la
		// síntesis invierte el estado y omite responsable y fecha.
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"El bloqueo sigue pendiente de validación legal. El próximo paso es revisar la nota."}}]}`)
	}))
	defer model.Close()

	service := New(model.URL, "key", "instructions", t.TempDir(), nil)
	capabilities := []ReadCapability{
		{Name: "Jira", Description: "incidencias", Read: func(context.Context, string) (CapabilityResult, error) {
			return CapabilityResult{Text: jiraText, ResultCount: 1, Evidence: []EvidenceRef{{Provider: "Jira", ID: "NUBE-104", URL: "https://jira.example/browse/NUBE-104"}}}, nil
		}},
		{Name: "Granola", Description: "reuniones", Read: func(context.Context, string) (CapabilityResult, error) {
			return CapabilityResult{Text: granolaText, ResultCount: 1, Evidence: []EvidenceRef{{Provider: "Granola", ID: "meeting-104", URL: "https://notes.example/meeting-104"}}}, nil
		}},
	}

	answer, _, reason, err := service.answerFromCapabilities(context.Background(), question, question, capabilities, nil, nil, nil, nil)
	if err != nil || reason != "" {
		t.Fatalf("answer=%q reason=%q err=%v", answer, reason, err)
	}
	for _, expected := range []string{
		"mantener el despliegue de Nube",
		"bloqueada",
		"el bloqueo queda resuelto",
		"Paula",
		"29 de octubre",
		"12 de noviembre",
		"14 de noviembre",
	} {
		if !strings.Contains(answer, expected) {
			t.Errorf("synthesis omitted %q: %s", expected, answer)
		}
	}
	if !strings.Contains(receivedPrompt, "citas exactas") || modelCalls != 1 {
		t.Fatalf("expected one structured extraction call, calls=%d prompt=%q", modelCalls, receivedPrompt)
	}
}

func TestSourceSynthesisFallsBackToCompleteSourceTextWhenExtractionDropsADate(t *testing.T) {
	question := "Resume Jira y Granola: decisión, cambio de estado, responsable y fechas."
	names := []string{"Jira", "Granola"}
	results := []CapabilityResult{
		{Text: "Estado: bloqueada. Responsable: Paula. Fecha objetivo: 14 de noviembre.", Evidence: []EvidenceRef{{Provider: "Jira", ID: "NUBE-104", URL: "https://jira.example/browse/NUBE-104"}}},
		{Text: "Decisión: mantener el despliegue el 14 de noviembre. Cambio de estado: la validación llegó el 29 de octubre. Paula revisará el expediente antes del 12 de noviembre.", Evidence: []EvidenceRef{{Provider: "Granola", ID: "meeting-104", URL: "https://notes.example/meeting-104"}}},
	}
	partial := sourceSynthesisExtraction{Facts: []sourceSynthesisFact{
		{Source: "Jira", EvidenceID: "NUBE-104", Category: "status", Quote: "Estado: bloqueada"},
		{Source: "Jira", EvidenceID: "NUBE-104", Category: "owner", Quote: "Responsable: Paula"},
		{Source: "Jira", EvidenceID: "NUBE-104", Category: "date", Quote: "Fecha objetivo: 14 de noviembre"},
		{Source: "Granola", EvidenceID: "meeting-104", Category: "decision", Quote: "Decisión: mantener el despliegue el 14 de noviembre"},
		{Source: "Granola", EvidenceID: "meeting-104", Category: "status", Quote: "Cambio de estado: la validación llegó el 29 de octubre"},
		{Source: "Granola", EvidenceID: "meeting-104", Category: "owner", Quote: "Paula revisará el expediente antes del 12 de noviembre"},
	}}
	answer := renderVerifiedSourceSynthesis(question, partial, names, results)
	for _, expected := range []string{"14 de noviembre", "29 de octubre", "12 de noviembre", "https://notes.example/meeting-104"} {
		if !strings.Contains(answer, expected) {
			t.Errorf("incomplete extraction did not fall back to full cited source text; missing %q in %s", expected, answer)
		}
	}
}

func TestSourceSynthesisClarifiesRequestedDateMissingFromEverySource(t *testing.T) {
	question := "Resume Jira y Granola: responsable y fecha acordada."
	names := []string{"Jira", "Granola"}
	results := []CapabilityResult{
		{Text: "Responsable: Paula.", Evidence: []EvidenceRef{{Provider: "Jira", ID: "NUBE-104", URL: "https://jira.example/browse/NUBE-104"}}},
		{Text: "Fran revisará el cambio.", Evidence: []EvidenceRef{{Provider: "Granola", ID: "meeting-104", URL: "https://notes.example/meeting-104"}}},
	}
	extraction := sourceSynthesisExtraction{Facts: []sourceSynthesisFact{
		{Source: "Jira", EvidenceID: "NUBE-104", Category: "owner", Quote: "Responsable: Paula"},
		{Source: "Granola", EvidenceID: "meeting-104", Category: "owner", Quote: "Fran revisará el cambio"},
	}}
	answer := renderVerifiedSourceSynthesis(question, extraction, names, results)
	if !strings.Contains(answer, "No consta la fecha") || !strings.Contains(answer, "¿Qué fecha acordó el equipo?") {
		t.Fatalf("missing date should be identified and clarified: %s", answer)
	}
}

func TestSyntheticCrossSourceSummaryKeepsBothStatusesOwnerAndEveryDate(t *testing.T) {
	query := "CASO FICTICIO. No consultes Jira ni Granola; no uses datos externos ni escribas en ellos. Fuentes simuladas: Jira (NUBE-104): estado actual de Jira: bloqueada por validación legal pendiente. Responsable: Paula. Fecha objetivo: 14 de noviembre. Granola (reunión de Nube): decisión: mantener el despliegue para el 14 de noviembre. Actualización: la validación legal llegó el 29 de octubre y el bloqueo quedó resuelto. Paula revisará el expediente antes del 12 de noviembre. Resume en dos frases la decisión, el cambio de estado, quién hace qué y todas las fechas. Si Jira y Granola reflejan estados distintos, distingue lo que dice cada fuente sin asumir que Jira ya se actualizó."

	answer, ok := syntheticClosedWorldAnswer(query)
	if !ok {
		t.Fatal("synthetic source summary did not use the closed-world renderer")
	}
	for _, expected := range []string{
		"mantener el despliegue para el 14 de noviembre",
		"bloqueada por validación legal pendiente",
		"bloqueo quedó resuelto",
		"Paula",
		"29 de octubre",
		"12 de noviembre",
		"14 de noviembre",
		"no confirma que Jira haya actualizado",
	} {
		if !strings.Contains(answer, expected) {
			t.Errorf("synthetic source summary omitted %q: %s", expected, answer)
		}
	}
	if strings.Contains(answer, "mantener el bloqueo") || strings.Contains(answer, "para el 29 de octubre") {
		t.Fatalf("synthetic source summary changed the decision or dates: %s", answer)
	}
	if got := strings.Count(answer, "."); got != 2 {
		t.Fatalf("synthetic source summary should use two sentences, got %d: %s", got, answer)
	}
}

func TestSyntheticCrossSourceSummaryAcceptsFictionalSourcesLabel(t *testing.T) {
	query := "CASO FICTICIO. No consultes Jira ni Granola; no uses datos externos. Fuentes ficticias: Jira (PRISMA-42): estado actual de Jira: bloqueada por revisión de seguridad pendiente. Responsable: Mei. Fecha objetivo: 9 de noviembre. Granola (reunión de Prisma): decisión: mantener el lanzamiento para el 9 de noviembre. Actualización: la revisión de seguridad se aprobó el 3 de noviembre y el bloqueo quedó resuelto. Mei comprobará el informe antes del 7 de noviembre. Resume en dos frases la decisión, el cambio de estado, quién hace qué y todas las fechas."

	answer, ok := syntheticClosedWorldAnswer(query)
	if !ok {
		t.Fatal("fictional source label did not use the closed-world renderer")
	}
	for _, expected := range []string{"PRISMA-42", "Mei", "9 de noviembre", "3 de noviembre", "7 de noviembre", "bloqueada por revisión de seguridad pendiente", "mantener el lanzamiento"} {
		if !strings.Contains(answer, expected) {
			t.Errorf("synthetic source summary omitted %q: %s", expected, answer)
		}
	}
}
