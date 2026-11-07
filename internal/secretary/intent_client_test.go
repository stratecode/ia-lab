package secretary

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestIntentFallbackCategoriesNeverExposeErrorText(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want string
	}{
		{"unavailable", errors.New("intent service unavailable: secret-token-value"), "service_unavailable"},
		{"http status", errors.New("intent service status 503: private host"), "service_http_error"},
		{"missing source", errors.New("intent source missing: account name"), "source_missing"},
		{"ambiguous", errors.New("intent capabilities ambiguous: project foo"), "ambiguous_route"},
		{"unresolved", errors.New("intent unresolved: user said a private phrase"), "unresolved_intent"},
		{"invalid", errors.New("intent response invalid: private response body"), "invalid_intent_response"},
		{"negated", errIntentNegatedAction, "action_negated"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := intentFallbackCategory(tc.err); got != tc.want {
				t.Fatalf("category=%q want=%q", got, tc.want)
			}
		})
	}
}

func TestExplicitReadOnlyComparisonOverridesSingleActionRoute(t *testing.T) {
	intent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"schema_version":"1","profile_version":"donna-v1","status":"resolved","goal":"change_request","requested_operation":"link","suggested_capabilities":["jira.read"],"basis":"deterministic"}`)
	}))
	defer intent.Close()
	service := New("", "", "", t.TempDir(), nil)
	service.Intent = &IntentHTTPClient{URL: intent.URL, Client: intent.Client()}
	service.Capabilities = []ReadCapability{
		{Name: "Granola", Match: func(query string) bool {
			return containsAny(normalizedIntentText(query), "granola", "daily", "reunion")
		}, Read: func(context.Context, string) (CapabilityResult, error) { return CapabilityResult{}, nil }},
		{Name: "Jira", Match: jiraTaskIntent, Read: func(context.Context, string) (CapabilityResult, error) { return CapabilityResult{}, nil }},
	}
	query := "¿Puedes relacionar la acción de explicar a Gonzalo las dos tablas, mencionada en la daily del 26 de octubre, con PMI-46? Consulta ambas fuentes."
	history := []Turn{{Role: "user", Content: "Solo Granola: en la daily del 26 de octubre, ¿qué modelo de datos debía explicar Fran a Gonzalo?"}}
	caps, matched, readQuery, meaning, err := service.interpretRoute(context.Background(), query, history)
	if err != nil || !matched || !reflect.DeepEqual(capabilityNames(caps), []string{"Granola", "Jira"}) || !strings.Contains(readQuery, "daily del 26 de octubre") || meaning != InteractionAction {
		t.Fatalf("route=%v matched=%t query=%q meaning=%s err=%v", capabilityNames(caps), matched, readQuery, meaning, err)
	}
}

func TestEnabledIntentControlsDonnaReply(t *testing.T) {
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"PMI-46 sigue en espera."}}]}`)
	}))
	defer model.Close()
	intent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload intentWireRequest
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
		}
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(payload.Utterance, "vacaciones") {
			_, _ = io.WriteString(w, `{"schema_version":"1","profile_version":"donna-v1","status":"resolved","goal":"general","target_mentions":[],"suggested_capabilities":[],"basis":"deterministic","ambiguities":[]}`)
			return
		}
		_, _ = io.WriteString(w, `{"schema_version":"1","profile_version":"donna-v1","status":"resolved","goal":"change_request","requested_operation":"issue.close","target_mentions":["PMI-46"],"suggested_capabilities":["jira.read"],"basis":"deterministic","ambiguities":[]}`)
	}))
	defer intent.Close()
	s := New(model.URL, "key", "instructions", t.TempDir(), map[int64]bool{7: true})
	s.Intent = &IntentHTTPClient{URL: intent.URL, Token: "test-secret", Client: intent.Client()}
	s.IntentMode = "enabled"
	reads := 0
	s.Capabilities = []ReadCapability{{Name: "Jira", Description: "tareas", Match: func(string) bool { return true }, Read: func(context.Context, string) (CapabilityResult, error) {
		reads++
		return CapabilityResult{Text: "PMI-46: Waiting", ResultCount: 1}, nil
	}}}
	answer, err := s.Reply(context.Background(), 7, "Quiero que cierres PMI-46")
	if err != nil || reads != 0 || !strings.Contains(answer, "solo tiene acceso de lectura") || strings.Contains(answer, "Waiting") {
		t.Fatalf("answer=%q reads=%d err=%v", answer, reads, err)
	}
	_, err = s.Reply(context.Background(), 7, "Prepara un plan para mis vacaciones")
	if err != nil || reads != 0 {
		t.Fatalf("generic request called Jira: reads=%d err=%v", reads, err)
	}
}

func TestEnabledIntentKeepsExplicitGranolaAndJiraComparisonMultiSource(t *testing.T) {
	var granolaReads, jiraReads int
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			Messages []Turn `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if len(payload.Messages) < 2 || !strings.Contains(payload.Messages[0].Content, "Notas de Granola") || !strings.Contains(payload.Messages[0].Content, "PMI-46: Waiting") {
			t.Errorf("expected both source results in synthesis, got %+v", payload.Messages)
		}
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"{\"facts\":[],\"links\":[]}"}}]}`)
	}))
	defer model.Close()
	intent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// Simulate a plausible but incomplete microservice suggestion.
		_, _ = io.WriteString(w, `{"schema_version":"1","profile_version":"donna-v1","status":"resolved","goal":"lookup","target_mentions":["PMI-46"],"suggested_capabilities":["granola.read"],"basis":"deterministic","ambiguities":[]}`)
	}))
	defer intent.Close()

	s := New(model.URL, "key", "instructions", t.TempDir(), map[int64]bool{7: true})
	s.Intent = &IntentHTTPClient{URL: intent.URL, Token: "test-secret", Client: intent.Client()}
	s.IntentMode = "enabled"
	s.Capabilities = []ReadCapability{
		{Name: "Granola", Description: "notas de reuniones", Match: func(query string) bool {
			return strings.Contains(strings.ToLower(query), "granola")
		}, Read: func(context.Context, string) (CapabilityResult, error) {
			granolaReads++
			return CapabilityResult{Text: "Notas de Granola: dos tablas", ResultCount: 1}, nil
		}},
		{Name: "Jira", Description: "tareas", Match: jiraTaskIntent, Read: func(context.Context, string) (CapabilityResult, error) {
			jiraReads++
			return CapabilityResult{Text: "PMI-46: Waiting", ResultCount: 1}, nil
		}},
	}

	answer, err := s.Reply(context.Background(), 7, "Contrasta Granola y Jira: ¿hay evidencia de que las dos tablas de la daily correspondan a PMI-46?")
	if err != nil || !strings.Contains(answer, "No he encontrado una coincidencia verificable") || granolaReads != 1 || jiraReads != 1 {
		t.Fatalf("answer=%q granola=%d jira=%d err=%v", answer, granolaReads, jiraReads, err)
	}
}

func TestEnabledIntentHonorsExplicitExclusionOfEveryMatchedSource(t *testing.T) {
	var intentCalls, granolaReads, jiraReads int
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			Messages []Turn `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(payload.Messages[0].Content, "Clasifica la intención") {
			t.Fatal("excluded-source request should bypass model intent classification")
		}
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"La publicación de Catálogo Norte depende de recibir las tres fotos el jueves; si llegan, podrá salir el viernes."}}]}`)
	}))
	defer model.Close()
	intent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		intentCalls++
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"schema_version":"1","profile_version":"donna-v1","status":"resolved","goal":"lookup","target_mentions":[],"suggested_capabilities":["granola.read"],"basis":"deterministic","ambiguities":[]}`)
	}))
	defer intent.Close()

	s := New(model.URL, "key", "instructions", t.TempDir(), map[int64]bool{7: true})
	s.Intent = &IntentHTTPClient{URL: intent.URL, Client: intent.Client()}
	s.IntentMode = "enabled"
	s.Capabilities = []ReadCapability{
		{Name: "Granola", Match: func(query string) bool {
			return localIntent(query) == intentCapability && containsAny(normalizedIntentText(query), "granola", "reunion", "reuniones")
		}, Read: func(context.Context, string) (CapabilityResult, error) {
			granolaReads++
			return CapabilityResult{Text: "dato externo no solicitado", ResultCount: 1}, nil
		}},
		{Name: "Jira", Match: jiraTaskIntent, Read: func(context.Context, string) (CapabilityResult, error) {
			jiraReads++
			return CapabilityResult{Text: "dato externo no solicitado", ResultCount: 1}, nil
		}},
	}

	query := "Prueba sintética; no consultes Jira ni Granola. Estas notas son inventadas: la revisión de Catálogo Norte se retrasa porque faltan fotos de 3 productos; Elena enviará las fotos el jueves; si llegan, la publicación puede salir el viernes. Resume en dos frases qué pasa y qué queda pendiente."
	answer, err := s.Reply(context.Background(), 7, query)
	if err != nil || !strings.Contains(answer, "tres fotos el jueves") || strings.Contains(answer, "dato externo") {
		t.Fatalf("answer=%q err=%v", answer, err)
	}
	if intentCalls != 0 || granolaReads != 0 || jiraReads != 0 {
		t.Fatalf("excluded sources were consulted: intent=%d Granola=%d Jira=%d", intentCalls, granolaReads, jiraReads)
	}
}

func TestEnabledIntentAcknowledgesNegatedActionWithoutReadingSource(t *testing.T) {
	intent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"schema_version":"1","profile_version":"donna-v1","status":"resolved","goal":"general","requested_operation":"issue.close","target_mentions":["PMI-46"],"suggested_capabilities":[],"basis":"deterministic","ambiguities":[],"reason_code":"action_negated"}`)
	}))
	defer intent.Close()
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { t.Error("negated action should not call the model") }))
	defer model.Close()
	s := New(model.URL, "key", "instructions", t.TempDir(), map[int64]bool{7: true})
	s.Intent = &IntentHTTPClient{URL: intent.URL, Token: "test-secret", Client: intent.Client()}
	s.IntentMode = "enabled"
	reads := 0
	s.Capabilities = []ReadCapability{{Name: "Jira", Read: func(context.Context, string) (CapabilityResult, error) {
		reads++
		return CapabilityResult{Text: "sensitive", ResultCount: 1}, nil
	}}}
	answer, err := s.Reply(context.Background(), 7, "No cierres PMI-46")
	if err != nil || answer != "Entendido; no aplicaré ningún cambio." || reads != 0 {
		t.Fatalf("answer=%q reads=%d err=%v", answer, reads, err)
	}
}

func TestEnabledIntentFallsBackToClassifierWhenIntentServiceIsAmbiguous(t *testing.T) {
	var classifierCalls, synthesisCalls, sourceReads int
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			Messages []Turn `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if len(payload.Messages) == 0 {
			t.Fatal("model call has no messages")
		}
		if strings.Contains(payload.Messages[0].Content, "Clasifica la intención") {
			classifierCalls++
			_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"{\"intent\":\"tool\"}"}}]}`)
			return
		}
		if !strings.Contains(payload.Messages[0].Content, "síntesis ejecutiva") || !strings.Contains(payload.Messages[0].Content, "no reproduzcas literalmente") {
			t.Fatalf("model call was not a Granola executive synthesis: %+v", payload.Messages)
		}
		synthesisCalls++
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"Se acordó mantener el plan; Donna lo resume sin copiar la nota."}}]}`)
	}))
	defer model.Close()
	intent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"schema_version":"1","profile_version":"donna-v1","status":"ambiguous","goal":null,"target_mentions":[],"suggested_capabilities":[],"basis":"deterministic","ambiguities":["meeting"]}`)
	}))
	defer intent.Close()

	s := New(model.URL, "key", "instructions", t.TempDir(), map[int64]bool{7: true})
	s.Intent = &IntentHTTPClient{URL: intent.URL, Token: "test-secret", Client: intent.Client()}
	s.IntentMode = "enabled"
	s.Capabilities = []ReadCapability{{Name: "Granola", Description: "notas de reuniones", Match: func(string) bool { return false }, Read: func(context.Context, string) (CapabilityResult, error) {
		sourceReads++
		return CapabilityResult{Text: "La reunión terminó con la decisión de mantener el plan.", ResultCount: 1, DirectAnswer: true}, nil
	}}}

	answer, err := s.Reply(context.Background(), 7, "¿Qué pasó en la reunión?")
	if err != nil || answer != "Se acordó mantener el plan; Donna lo resume sin copiar la nota." || classifierCalls != 1 || synthesisCalls != 1 || sourceReads != 1 {
		t.Fatalf("answer=%q classifier=%d syntheses=%d reads=%d err=%v", answer, classifierCalls, synthesisCalls, sourceReads, err)
	}
}

func TestEnabledIntentServiceFailureDoesNotClassifyClearGeneralQuestion(t *testing.T) {
	var classifierCalls, answerCalls int
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			Messages []Turn `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if len(payload.Messages) > 0 && strings.Contains(payload.Messages[0].Content, "Clasifica la intención") {
			classifierCalls++
			_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"{\"intent\":\"clarify\"}"}}]}`)
			return
		}
		answerCalls++
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"¡Hola, Fran!"}}]}`)
	}))
	defer model.Close()
	intent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "temporarily unavailable", http.StatusServiceUnavailable)
	}))
	defer intent.Close()

	s := New(model.URL, "key", "instructions", t.TempDir(), map[int64]bool{7: true})
	s.Intent = &IntentHTTPClient{URL: intent.URL, Token: "test-secret", Client: intent.Client()}
	s.IntentMode = "enabled"
	answer, err := s.Reply(context.Background(), 7, "Hola")
	if err != nil || answer != "¡Hola, Fran!" || classifierCalls != 0 || answerCalls != 1 {
		t.Fatalf("answer=%q classifier=%d answerCalls=%d err=%v", answer, classifierCalls, answerCalls, err)
	}
}

func TestEnabledIntentServiceFailureUsesDeterministicLocalRouteFirst(t *testing.T) {
	var reads, modelCalls int
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		modelCalls++
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"unexpected model call"}}]}`)
	}))
	defer model.Close()
	intent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "temporarily unavailable", http.StatusServiceUnavailable)
	}))
	defer intent.Close()

	s := New(model.URL, "key", "instructions", t.TempDir(), map[int64]bool{7: true})
	s.Intent = &IntentHTTPClient{URL: intent.URL, Token: "test-secret", Client: intent.Client()}
	s.IntentMode = "enabled"
	s.Capabilities = []ReadCapability{{Name: "Granola", Description: "notas", Match: func(query string) bool {
		return strings.Contains(strings.ToLower(query), "granola")
	}, Read: func(context.Context, string) (CapabilityResult, error) {
		reads++
		return CapabilityResult{Text: "Resumen disponible.", ResultCount: 1, DirectAnswer: true}, nil
	}}}
	answer, err := s.Reply(context.Background(), 7, "Consulta Granola")
	if err != nil || answer != "Resumen disponible." || reads != 1 || modelCalls != 0 {
		t.Fatalf("answer=%q reads=%d model=%d err=%v", answer, reads, modelCalls, err)
	}
}

func TestIntentRouteRejectsInventedCapability(t *testing.T) {
	s := routingServiceForTest()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-secret" {
			t.Error("missing auth")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"schema_version":"1","profile_version":"donna-v1","status":"resolved","goal":"lookup","target_mentions":[],"suggested_capabilities":["jira.write"],"basis":"deterministic","ambiguities":[]}`))
	}))
	defer server.Close()
	s.Intent = &IntentHTTPClient{URL: server.URL, Token: "test-secret", Client: server.Client()}
	_, matched, _, _, err := s.interpretRoute(context.Background(), "Busca PMI-46", nil)
	if err == nil || matched {
		t.Fatalf("unregistered capability accepted: matched=%v err=%v", matched, err)
	}
}

func TestIntentRoutePreservesChangeAndRecall(t *testing.T) {
	cases := []struct {
		phrase, body string
		want         secretaryIntent
		meaning      InteractionMeaning
		source       string
	}{
		{"Quiero que cierres PMI-46", `{"schema_version":"1","profile_version":"donna-v1","status":"resolved","goal":"change_request","requested_operation":"issue.close","target_mentions":["PMI-46"],"suggested_capabilities":["jira.read"],"basis":"deterministic","ambiguities":[]}`, intentCapability, InteractionAction, "Jira"},
		{"Recuérdame qué hablamos sobre el presupuesto", `{"schema_version":"1","profile_version":"donna-v1","status":"resolved","goal":"recall","target_mentions":[],"suggested_capabilities":["memory.read"],"basis":"deterministic","ambiguities":[]}`, intentMemory, InteractionLookup, ""},
	}
	for _, tc := range cases {
		t.Run(tc.phrase, func(t *testing.T) {
			s := routingServiceForTest()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()
			s.Intent = &IntentHTTPClient{URL: server.URL, Token: "test-secret", Client: server.Client()}
			caps, matched, _, meaning, err := s.interpretRoute(context.Background(), tc.phrase, nil)
			if err != nil || meaning != tc.meaning || ((tc.source != "") != matched) {
				t.Fatalf("route=%v %v %v err=%v", capabilityNames(caps), matched, meaning, err)
			}
			if tc.source != "" && (len(caps) != 1 || caps[0].Name != tc.source) {
				t.Fatalf("source=%v", capabilityNames(caps))
			}
		})
	}
}

func TestIntentRouteKeepsNamedPersonWorkOnJiraAndAppliesSavedScope(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
	}{
		{
			name: "intent model says general",
			body: `{"schema_version":"1","profile_version":"donna-v1","status":"resolved","goal":"general","target_mentions":[],"suggested_capabilities":[],"basis":"deterministic","ambiguities":[]}`,
		},
		{
			name: "intent model selects jira",
			body: `{"schema_version":"1","profile_version":"donna-v1","status":"resolved","goal":"lookup","target_mentions":["Valentina"],"suggested_capabilities":["jira.read"],"basis":"deterministic","ambiguities":[]}`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := routingServiceForTest()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()
			s.Intent = &IntentHTTPClient{URL: server.URL, Token: "test-secret", Client: server.Client()}

			query := "en qué está trabajando hoy Valentina?"
			caps, matched, readQuery, meaning, err := s.interpretRoute(context.Background(), query, nil)
			if err != nil || !matched || meaning != InteractionLookup || !reflect.DeepEqual(capabilityNames(caps), []string{"Jira"}) {
				t.Fatalf("route=%v matched=%v meaning=%v err=%v", capabilityNames(caps), matched, meaning, err)
			}
			if !strings.Contains(readQuery, "Valentina") || !strings.Contains(readQuery, "Proyecto PMI") || !strings.Contains(readQuery, "en curso") {
				t.Fatalf("read query lost saved Jira scope: %q", readQuery)
			}
		})
	}
}

func TestIntentRouteFailsClosed(t *testing.T) {
	s := routingServiceForTest()
	s.Intent = &IntentHTTPClient{URL: "http://127.0.0.1:1", Token: "test-secret", Client: &http.Client{}}
	_, matched, _, _, err := s.interpretRoute(context.Background(), "Cierra PMI-46", nil)
	if err == nil || matched || !strings.Contains(err.Error(), "intent") {
		t.Fatalf("route should fail closed: %v %v", matched, err)
	}
}
