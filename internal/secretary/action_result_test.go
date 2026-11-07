package secretary

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestUnverifiedWriteClaimIsNotReturnedOrRemembered(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"He creado la tarea en Jira."}}]}`)
	}))
	defer upstream.Close()
	s := New(upstream.URL, "key", "instructions", t.TempDir(), map[int64]bool{7: true})

	var events []InteractionEvent
	answer, err := s.ReplyWithInteraction(context.Background(), 7, "openwebui", "fresh-chat", "Sí, hazlo", func(event InteractionEvent) {
		events = append(events, event)
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.ToLower(answer), "he creado") || !strings.Contains(strings.ToLower(answer), "no he") {
		t.Fatalf("unverified action claim reached the user: %q", answer)
	}
	if len(events) == 0 || events[len(events)-1].Kind != EventBlocked {
		t.Fatalf("unverified action was not reported as blocked: %+v", events)
	}
	for _, turn := range s.history[s.historyKey(7, "openwebui", "fresh-chat")] {
		if turn.Role == "assistant" && strings.Contains(strings.ToLower(turn.Content), "he creado") {
			t.Fatalf("unverified action claim entered conversation memory: %q", turn.Content)
		}
	}
}

func TestReadOnlySourceAnswerCannotClaimAnExternalWrite(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"He actualizado la tarea SW-87 en Jira."}}]}`)
	}))
	defer upstream.Close()
	s := New(upstream.URL, "key", "instructions", t.TempDir(), map[int64]bool{7: true})
	s.Capabilities = []ReadCapability{{
		Name: "Jira", Description: "tareas en Jira",
		Match: func(query string) bool { return strings.Contains(strings.ToLower(query), "jira") },
		Read: func(context.Context, string) (CapabilityResult, error) {
			return CapabilityResult{Text: "SW-87: En curso.", ResultCount: 1}, nil
		},
	}}

	answer, err := s.ReplyWithInteraction(context.Background(), 7, "openwebui", "fresh-chat", "¿Cómo está SW-87 en Jira?", nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.ToLower(answer), "he actualizado") || !strings.Contains(strings.ToLower(answer), "no he") {
		t.Fatalf("source answer falsely claimed a write: %q", answer)
	}
}

func TestUnverifiedWriteClaimIsNotStreamed(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"He crea\"}}]}\n\ndata: {\"choices\":[{\"delta\":{\"content\":\"do la tarea en Jira.\"}}]}\n\ndata: [DONE]\n\n")
	}))
	defer upstream.Close()
	s := New(upstream.URL, "key", "instructions", t.TempDir(), map[int64]bool{7: true})
	result := performOpenWebUIChat(t, s, "Sí, hazlo", true)
	body := result.Body.String()
	if strings.Contains(body, "He crea") || strings.Contains(body, "do la tarea en Jira") || !strings.Contains(body, "No he aplicado") {
		t.Fatalf("unverified model chunks escaped before verification: %q", body)
	}
}

func TestSourcedAnswerRejectsIssueKeyAbsentFromProviderResult(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"SW-93 está finalizada y asignada a Fran."}}]}`)
	}))
	defer upstream.Close()
	s := New(upstream.URL, "key", "instructions", t.TempDir(), map[int64]bool{7: true})
	s.Capabilities = []ReadCapability{{
		Name: "Jira", Description: "tareas en Jira",
		Match: func(query string) bool { return strings.Contains(strings.ToLower(query), "jira") },
		Read: func(context.Context, string) (CapabilityResult, error) {
			return CapabilityResult{Text: "SW-87: En curso; asignada a Gonzalo.", ResultCount: 1}, nil
		},
	}}
	answer, err := s.ReplyWithInteraction(context.Background(), 7, "openwebui", "fresh-chat", "¿Cómo está SW-87 en Jira?", nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(answer, "SW-93") || !strings.Contains(strings.ToLower(answer), "no puedo verificar") {
		t.Fatalf("unsupported issue key survived source verification: %q", answer)
	}
}

func TestConfirmationOfModelOfferCannotBecomeWriteSuccess(t *testing.T) {
	modelCalls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		modelCalls++
		if modelCalls == 1 {
			_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"Sí, puedo crear la tarea en Jira si me lo confirmas."}}]}`)
			return
		}
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"Listo, ya está."}}]}`)
	}))
	defer upstream.Close()
	s := New(upstream.URL, "key", "instructions", t.TempDir(), map[int64]bool{7: true})
	offerAnswer, err := s.ReplyWithInteraction(context.Background(), 7, "openwebui", "offer-chat", "¿Puedes encargarte de esto?", nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.ToLower(offerAnswer), "puedo crear") || !strings.Contains(strings.ToLower(offerAnswer), "solo tiene acceso de lectura") {
		t.Fatalf("model offered an unavailable external write: %q", offerAnswer)
	}
	answer, err := s.ReplyWithInteraction(context.Background(), 7, "openwebui", "offer-chat", "Sí, hazlo", nil)
	if err != nil {
		t.Fatal(err)
	}
	if modelCalls != 1 || !strings.Contains(strings.ToLower(answer), "no he aplicado") {
		t.Fatalf("confirmation escaped read-only gate: calls=%d answer=%q", modelCalls, answer)
	}
}

func TestIndirectJiraCreateRequestStopsBeforeModel(t *testing.T) {
	modelCalls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		modelCalls++
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"Tarea asignada en SW."}}]}`)
	}))
	defer upstream.Close()
	s := New(upstream.URL, "key", "instructions", t.TempDir(), map[int64]bool{7: true})
	answer, err := s.ReplyWithInteraction(context.Background(), 7, "openwebui", "write-chat", "¿Puedes encargarte de crear una tarea para revisar el catálogo en SW?", nil)
	if err != nil {
		t.Fatal(err)
	}
	if modelCalls != 0 || !strings.Contains(strings.ToLower(answer), "solo tiene acceso de lectura") {
		t.Fatalf("indirect write reached model: calls=%d answer=%q", modelCalls, answer)
	}
	answer, err = s.ReplyWithInteraction(context.Background(), 7, "openwebui", "write-chat", "Sí, hazlo", nil)
	if err != nil {
		t.Fatal(err)
	}
	if modelCalls != 0 || !strings.Contains(strings.ToLower(answer), "no he aplicado") {
		t.Fatalf("confirmation after blocked write lost context: calls=%d answer=%q", modelCalls, answer)
	}
}

func TestExternalWriteGateDoesNotClassifySummaryRequest(t *testing.T) {
	if requestsExternalWrite("¿Puedes crear un resumen de la tarea SW-87?") {
		t.Fatal("an in-chat summary was classified as an external write")
	}
}

func TestUnverifiedPassiveAssignmentClaimIsBlocked(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"Tarea asignada: revisar el catálogo de SW."}}]}`)
	}))
	defer upstream.Close()
	s := New(upstream.URL, "key", "instructions", t.TempDir(), map[int64]bool{7: true})
	answer, err := s.ReplyWithInteraction(context.Background(), 7, "openwebui", "passive-chat", "¿Puedes encargarte de esto?", nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.ToLower(answer), "tarea asignada") || !strings.Contains(strings.ToLower(answer), "no he aplicado") {
		t.Fatalf("unverified passive assignment reached user: %q", answer)
	}
}
