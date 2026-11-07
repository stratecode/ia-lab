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

func TestCrossSourceComparisonRejectsUnverifiedFactsAndLinks(t *testing.T) {
	granola := CapabilityResult{
		Text:     "Fran explica a Gonzalo el modelo de datos con la pizarra. También Gonzalo prepara un script para analizar literales.",
		Evidence: []EvidenceRef{{Provider: "granola", ID: "meeting-1", Title: "Daily", URL: "https://notes.example/meeting-1"}},
	}
	jira := CapabilityResult{
		Text:     "PMI-46: Waiting. Valentina Lagos figura como responsable.",
		Evidence: []EvidenceRef{{Provider: "jira", ID: "PMI-46", Title: "Migración del Core", URL: "https://jira.example/browse/PMI-46"}},
	}
	extraction := sourceComparisonExtraction{
		Facts: []sourceComparisonFact{
			{Source: "Granola", EvidenceID: "meeting-1", Quote: "Gonzalo prepara un script para analizar literales"},
			{Source: "Jira", EvidenceID: "PMI-46", Quote: "PMI-46: Waiting"},
			{Source: "Jira", EvidenceID: "PMI-46", Quote: "El proyecto confirma que comparten un mismo script"},
		},
		Links: []sourceComparisonLink{{GranolaFact: 0, JiraFact: 1, SharedPhrase: "Gonzalo prepara un script para analizar literales"}},
	}

	answer := renderVerifiedSourceComparison(extraction, []string{"Granola", "Jira"}, []CapabilityResult{granola, jira})
	if strings.Contains(answer, "Coincidencia verificable") || strings.Contains(answer, "comparten un mismo script") {
		t.Fatalf("unsupported relation or quote survived: %q", answer)
	}
	if !strings.Contains(answer, "No he encontrado una coincidencia verificable") || !strings.Contains(answer, "https://notes.example/meeting-1") || !strings.Contains(answer, "https://jira.example/browse/PMI-46") {
		t.Fatalf("expected separated facts and both source citations: %q", answer)
	}
}

func TestCrossSourceComparisonAcceptsExactSharedActionAndOwner(t *testing.T) {
	phrase := "Fran explica a Gonzalo el modelo de datos con la pizarra"
	granola := CapabilityResult{
		Text:     "Acción: " + phrase + ".",
		Evidence: []EvidenceRef{{Provider: "granola", ID: "meeting-1", Title: "Daily", URL: "https://notes.example/meeting-1"}},
	}
	jira := CapabilityResult{
		Text:     "Descripción: " + phrase + ". PMI-46 permanece en Waiting.",
		Evidence: []EvidenceRef{{Provider: "jira", ID: "PMI-46", Title: "Modelo de datos", URL: "https://jira.example/browse/PMI-46"}},
	}
	extraction := sourceComparisonExtraction{
		Facts: []sourceComparisonFact{
			{Source: "Granola", EvidenceID: "meeting-1", Quote: phrase},
			{Source: "Jira", EvidenceID: "PMI-46", Quote: phrase},
		},
		Links: []sourceComparisonLink{{GranolaFact: 0, JiraFact: 1, SharedPhrase: phrase}},
	}

	answer := renderVerifiedSourceComparison(extraction, []string{"Granola", "Jira"}, []CapabilityResult{granola, jira})
	if !strings.Contains(answer, "Coincidencia verificable") || !strings.Contains(answer, phrase) || !strings.Contains(answer, "https://notes.example/meeting-1") || !strings.Contains(answer, "https://jira.example/browse/PMI-46") {
		t.Fatalf("expected grounded link with both references, got %q", answer)
	}
}

func TestCrossSourceComparisonFallbackSeparatesCitedFactsWithoutInventingLink(t *testing.T) {
	answer := renderSourceComparisonFallback("Fran daily Gonzalo modelo tablas PMI-46",
		[]string{"Granola", "Jira"},
		[]CapabilityResult{
			{Text: "Fran explica a Gonzalo el modelo de datos con la pizarra.", Evidence: []EvidenceRef{{Provider: "granola", ID: "meeting-1", Title: "Daily del 26 de octubre", URL: "https://notes.example/meeting-1"}}},
			{Text: "PMI-46: Waiting", Evidence: []EvidenceRef{{Provider: "jira", ID: "PMI-46", Title: "Migración del Core", URL: "https://jira.example/browse/PMI-46"}}},
		},
	)
	if !strings.Contains(answer, "Daily del 26 de octubre") || !strings.Contains(answer, "Fran explica a Gonzalo el modelo de datos") || !strings.Contains(answer, "PMI-46") || !strings.Contains(answer, "no aparece en el texto recuperado de Granola") || !strings.Contains(answer, "No puedo confirmar una relación") {
		t.Fatalf("fallback did not retain separate cited facts: %s", answer)
	}
}

func TestSourceComparisonFallbackSelectsMeetingPassageFromConversationContext(t *testing.T) {
	text := "# Nóminas y Pagos\n- Transferencias migradas a emisión de fichero NOMINA\n# Updates del Equipo\n- Gonzalo: trabajando en script para analizar literales\n- Fran le explicará el modelo de datos con detalle en la daily: dos tablas (maestra de claves + traducciones por cliente/defecto)\n# Próximos Pasos\n- Explicar modelo de datos a Gonzalo en la daily (Fran)"
	query := "¿Puedes relacionar la acción de explicar a Gonzalo las dos tablas, mencionada en la daily del 26 de octubre, con PMI-46? Consulta ambas fuentes. Reunión de referencia: daily del 26 de octubre"
	got := sourceExcerptForQuery(text, query, 180)
	if !strings.Contains(got, "Fran le explicará el modelo de datos") || !strings.Contains(got, "dos tablas") {
		t.Fatalf("selected irrelevant meeting excerpt: %q", got)
	}
}

func TestAnswerFromCapabilitiesUsesVerifiedComparisonForExplicitCrossSourceQuestion(t *testing.T) {
	var modelCalls int
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		modelCalls++
		var request struct {
			Messages []Turn `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		if len(request.Messages) < 2 || !strings.Contains(request.Messages[0].Content, "cita debe copiarse literalmente") {
			t.Errorf("missing structured evidence extraction instructions: %+v", request.Messages)
		}
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"{\"facts\":[{\"source\":\"Granola\",\"evidence_id\":\"meeting-1\",\"quote\":\"Fran explica a Gonzalo el modelo de datos con la pizarra\"},{\"source\":\"Jira\",\"evidence_id\":\"PMI-46\",\"quote\":\"PMI-46: Waiting\"}],\"links\":[{\"granola_fact\":0,\"jira_fact\":1,\"shared_phrase\":\"Fran explica a Gonzalo el modelo de datos con la pizarra\"}] }"}}]}`)
	}))
	defer model.Close()
	service := New(model.URL, "key", "instructions", t.TempDir(), nil)
	capabilities := []ReadCapability{
		{Name: "Granola", Read: func(context.Context, string) (CapabilityResult, error) {
			return CapabilityResult{Text: "Fran explica a Gonzalo el modelo de datos con la pizarra.", ResultCount: 1, Evidence: []EvidenceRef{{Provider: "granola", ID: "meeting-1", Title: "Daily", URL: "https://notes.example/meeting-1"}}}, nil
		}},
		{Name: "Jira", Read: func(context.Context, string) (CapabilityResult, error) {
			return CapabilityResult{Text: "PMI-46: Waiting", ResultCount: 1, Evidence: []EvidenceRef{{Provider: "jira", ID: "PMI-46", Title: "Migración del Core", URL: "https://jira.example/browse/PMI-46"}}}, nil
		}},
	}

	answer, _, _, err := service.answerFromCapabilities(context.Background(), "¿Hay evidencia de que la acción de Granola esté vinculada a PMI-46?", "", capabilities, nil, nil, nil, nil)
	if err != nil || modelCalls != 1 || strings.Contains(answer, "Coincidencia verificable") || !strings.Contains(answer, "No he encontrado una coincidencia verificable") {
		t.Fatalf("answer=%q model=%d err=%v", answer, modelCalls, err)
	}
	if !strings.Contains(answer, "https://notes.example/meeting-1") || !strings.Contains(answer, "https://jira.example/browse/PMI-46") {
		t.Fatalf("both source citations should be preserved: %q", answer)
	}
}

func TestAnswerFromCapabilitiesFallsBackToSeparateCitedFactsWhenModelFails(t *testing.T) {
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer model.Close()
	service := New(model.URL, "key", "instructions", t.TempDir(), nil)
	capabilities := []ReadCapability{
		{Name: "Granola", Read: func(context.Context, string) (CapabilityResult, error) {
			return CapabilityResult{Text: "Fran explica el modelo de datos.", ResultCount: 1, Evidence: []EvidenceRef{{Provider: "granola", ID: "meeting-1", Title: "Daily del 26 de octubre", URL: "https://notes.example/meeting-1"}}}, nil
		}},
		{Name: "Jira", Read: func(context.Context, string) (CapabilityResult, error) {
			return CapabilityResult{Text: "PMI-46: Waiting", ResultCount: 1, Evidence: []EvidenceRef{{Provider: "jira", ID: "PMI-46", Title: "Migración del Core", URL: "https://jira.example/browse/PMI-46"}}}, nil
		}},
	}
	answer, _, block, err := service.answerFromCapabilities(context.Background(), "¿Eso está vinculado con PMI-46? Contrasta Granola y Jira.", "", capabilities, nil, nil, nil, nil)
	if err != nil || block != "" || !strings.Contains(answer, "no aparece en el texto recuperado de Granola") || !strings.Contains(answer, "https://notes.example/meeting-1") {
		t.Fatalf("answer=%q block=%q err=%v", answer, block, err)
	}
}
