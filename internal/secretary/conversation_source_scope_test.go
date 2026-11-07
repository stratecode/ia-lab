package secretary

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestSyntheticSourceExclusionsPersistAcrossAnaphoricFollowUp(t *testing.T) {
	var intentCalls, jiraReads, granolaReads, modelCalls int
	var followUpKeptSyntheticFacts bool

	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		modelCalls++
		var request struct {
			Messages []Turn `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		var joined strings.Builder
		for _, message := range request.Messages {
			joined.WriteString(message.Content)
			joined.WriteByte('\n')
		}
		if strings.Contains(joined.String(), "¿Y ella cuándo?") {
			followUpKeptSyntheticFacts = strings.Contains(joined.String(), "viernes 6 de noviembre")
		}
		answer := "Nuria enviará la versión final el viernes 6 de noviembre."
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"`+answer+`"}}]}`)
	}))
	defer model.Close()

	intent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		intentCalls++
		var request intentWireRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		payload, _ := json.Marshal(intentWireResult{
			SchemaVersion: "1", ProfileVersion: "donna-v1", Status: "resolved",
			Goal: stringPointer("lookup"), SuggestedCapabilities: []string{"jira.read"}, Basis: "deterministic",
		})
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(payload)
	}))
	defer intent.Close()

	service := New(model.URL, "key", "instructions", t.TempDir(), map[int64]bool{7: true})
	service.Intent = &IntentHTTPClient{URL: intent.URL, Token: "test-secret", Client: intent.Client()}
	service.IntentMode = "enabled"
	service.Capabilities = []ReadCapability{
		{Name: "Granola", Description: "notas", Match: func(string) bool { return true }, Read: func(context.Context, string) (CapabilityResult, error) {
			granolaReads++
			return CapabilityResult{Text: "dato externo de Granola", ResultCount: 1}, nil
		}},
		{Name: "Jira", Description: "incidencias", Match: jiraTaskIntent, Read: func(context.Context, string) (CapabilityResult, error) {
			jiraReads++
			return CapabilityResult{Text: "dato externo de Jira", ResultCount: 1}, nil
		}},
	}

	const channel = "telegram"
	const conversation = "synthetic-source-scope"
	first := syntheticDateScenario
	if _, err := service.ReplyInConversation(context.Background(), 7, channel, conversation, first); err != nil {
		t.Fatal(err)
	}
	if jiraReads != 0 || granolaReads != 0 {
		t.Fatalf("initial excluded turn read sources: Jira=%d Granola=%d", jiraReads, granolaReads)
	}

	answer, err := service.ReplyInConversation(context.Background(), 7, channel, conversation, "¿Y ella cuándo?")
	if err != nil {
		t.Fatal(err)
	}
	if jiraReads != 0 || granolaReads != 0 || intentCalls != 0 {
		t.Fatalf("anaphoric follow-up widened source scope: answer=%q intent=%d Jira=%d Granola=%d", answer, intentCalls, jiraReads, granolaReads)
	}
	if !followUpKeptSyntheticFacts || !strings.Contains(answer, "viernes 6 de noviembre") {
		t.Fatalf("follow-up lost synthetic context: answer=%q modelKeptFacts=%t", answer, followUpKeptSyntheticFacts)
	}
	if modelCalls != 2 {
		t.Fatalf("expected both turns to use the model with conversation context, got %d calls", modelCalls)
	}
}

func TestExplicitReadOnlyJiraRequestOverridesPriorConversationExclusion(t *testing.T) {
	var intentCalls, jiraReads, granolaReads int
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"Nuria enviará la versión final el viernes 6 de noviembre."}}]}`)
	}))
	defer model.Close()

	intent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		intentCalls++
		payload, _ := json.Marshal(intentWireResult{
			SchemaVersion: "1", ProfileVersion: "donna-v1", Status: "resolved",
			Goal: stringPointer("lookup"), SuggestedCapabilities: []string{"jira.read"}, Basis: "deterministic",
		})
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(payload)
	}))
	defer intent.Close()

	service := New(model.URL, "key", "instructions", t.TempDir(), map[int64]bool{7: true})
	service.Intent = &IntentHTTPClient{URL: intent.URL, Token: "test-secret", Client: intent.Client()}
	service.IntentMode = "enabled"
	service.Capabilities = []ReadCapability{
		{Name: "Granola", Description: "notas", Match: func(string) bool { return true }, Read: func(context.Context, string) (CapabilityResult, error) {
			granolaReads++
			return CapabilityResult{Text: "dato externo de Granola", ResultCount: 1}, nil
		}},
		{Name: "Jira", Description: "incidencias", Match: jiraTaskIntent, Read: func(context.Context, string) (CapabilityResult, error) {
			jiraReads++
			return CapabilityResult{Text: "DEMO-42: estado abierto", ResultCount: 1, DirectAnswer: true}, nil
		}},
	}

	const channel = "telegram"
	const conversation = "explicit-read-after-scope"
	first := syntheticDateScenario
	if _, err := service.ReplyInConversation(context.Background(), 7, channel, conversation, first); err != nil {
		t.Fatal(err)
	}
	answer, err := service.ReplyInConversation(context.Background(), 7, channel, conversation, "Entonces, consulta Jira en modo de solo lectura para verificar el estado de DEMO-42.")
	if err != nil {
		t.Fatal(err)
	}
	if intentCalls != 1 || jiraReads != 1 || granolaReads != 0 || !strings.Contains(answer, "DEMO-42") {
		t.Fatalf("explicit read-only request did not route to Jira alone: answer=%q intent=%d Jira=%d Granola=%d", answer, intentCalls, jiraReads, granolaReads)
	}
}

func TestSyntheticSingleDatedActionSummaryUsesTheSuppliedFact(t *testing.T) {
	state := &sourceScopeTestState{}
	service := newSourceScopeTestService(t, "Te recomiendo revisar los documentos disponibles para confirmar la fecha.", state)

	answer, err := service.ReplyInConversation(context.Background(), 7, "telegram", "single-dated-summary", syntheticDateScenario)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(answer, "viernes 6 de noviembre") || strings.Contains(strings.ToLower(answer), "recomiendo revisar") {
		t.Fatalf("synthetic summary was not grounded in its only dated fact: %q", answer)
	}
	if state.modelCalls != 1 || state.jiraReads != 0 || state.granolaReads != 0 {
		t.Fatalf("expected one model call and no source reads, got model=%d Jira=%d Granola=%d", state.modelCalls, state.jiraReads, state.granolaReads)
	}
}

func TestSyntheticAnaphoricDateFollowUpUsesPriorSingleDatedFact(t *testing.T) {
	state := &sourceScopeTestState{}
	service := newSourceScopeTestService(t, "No tengo información específica; revisa tus documentos para confirmarlo.", state)

	const conversation = "anaphoric-dated-summary"
	if _, err := service.ReplyInConversation(context.Background(), 7, "telegram", conversation, syntheticDateScenario); err != nil {
		t.Fatal(err)
	}
	answer, err := service.ReplyInConversation(context.Background(), 7, "telegram", conversation, "¿Y ella cuándo?")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(answer, "viernes 6 de noviembre") || strings.Contains(strings.ToLower(answer), "revisa tus documentos") {
		t.Fatalf("anaphoric follow-up did not recover the single dated fact: %q", answer)
	}
	if state.modelCalls != 2 || state.jiraReads != 0 || state.granolaReads != 0 || state.intentCalls != 0 {
		t.Fatalf("expected two model calls and no classifier or source reads, got model=%d intent=%d Jira=%d Granola=%d", state.modelCalls, state.intentCalls, state.jiraReads, state.granolaReads)
	}
	if !containsSyntheticFollowUpInstruction(state.modelMessages) {
		t.Fatal("LFM system prompt did not identify the anaphoric turn as part of a closed synthetic case")
	}
}

func TestConversationSourceExclusionsResetAtNewTopicAndNewCommand(t *testing.T) {
	tests := []struct {
		name     string
		reset    string
		followUp string
	}{
		{name: "new topic", followUp: "Cambiemos de tema: ¿Qué estado tiene DEMO-42?"},
		{name: "new command", reset: "/new", followUp: "¿Y ella cuándo?"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state := &sourceScopeTestState{}
			service := newSourceScopeTestService(t, "DEMO-42: estado abierto.", state)
			const conversation = "scope-reset-" + "case"
			if _, err := service.ReplyInConversation(context.Background(), 7, "telegram", conversation, syntheticDateScenario); err != nil {
				t.Fatal(err)
			}
			if tt.reset != "" {
				if _, err := service.ReplyInConversation(context.Background(), 7, "telegram", conversation, tt.reset); err != nil {
					t.Fatal(err)
				}
			}
			answer, err := service.ReplyInConversation(context.Background(), 7, "telegram", conversation, tt.followUp)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(answer, "DEMO-42") || state.intentCalls != 1 || state.jiraReads != 1 || state.granolaReads != 0 {
				t.Fatalf("conversation reset retained the previous source exclusion: answer=%q intent=%d Jira=%d Granola=%d", answer, state.intentCalls, state.jiraReads, state.granolaReads)
			}
		})
	}
}

func TestLiveLFMReturnsSyntheticDateWithoutReadingSources(t *testing.T) {
	upstream := strings.TrimRight(os.Getenv("LAB_SECRETARY_LFM_UPSTREAM"), "/")
	if upstream == "" {
		t.Skip("set LAB_SECRETARY_LFM_UPSTREAM to run the live LFM acceptance test")
	}

	state := &sourceScopeTestState{}
	service := newSourceScopeTestService(t, "", state)
	modelKey := os.Getenv("LAB_SECRETARY_LFM_KEY")
	service.Upstream = upstream
	service.Key = modelKey
	service.Models["main"] = ModelTarget{Name: "main", Upstream: upstream, Key: modelKey}
	service.Intent = nil
	service.IntentMode = ""

	const conversation = "live-lfm-synthetic-date"
	first, err := service.ReplyInConversation(context.Background(), 7, "telegram", conversation, syntheticDateScenario)
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.ReplyInConversation(context.Background(), 7, "telegram", conversation, "¿Y ella cuándo?")
	if err != nil {
		t.Fatal(err)
	}
	for turn, answer := range map[string]string{"initial": first, "follow-up": second} {
		if !strings.Contains(strings.ToLower(answer), "viernes 6 de noviembre") {
			t.Errorf("live LFM %s answer lost the supplied date: %q", turn, answer)
		}
		if strings.Contains(strings.ToLower(answer), "recomiendo revisar") || strings.Contains(strings.ToLower(answer), "documentos disponibles") {
			t.Errorf("live LFM %s answer added an unsupported recommendation: %q", turn, answer)
		}
	}
	if state.jiraReads != 0 || state.granolaReads != 0 || state.intentCalls != 0 {
		t.Fatalf("live LFM synthetic test called a source or classifier: intent=%d Jira=%d Granola=%d", state.intentCalls, state.jiraReads, state.granolaReads)
	}
}

func TestSyntheticDatedActionFallbackRejectsAmbiguousFacts(t *testing.T) {
	query := "Caso ficticio. Nuria enviará la versión final el viernes 6 de noviembre. Paula confirmará el aforo el jueves 5 de noviembre. Resume el dato."
	if answer, ok := syntheticClosedWorldAnswer(query); ok {
		t.Fatalf("summary fallback guessed between two dated facts: %q", answer)
	}
	history := []Turn{{Role: "user", Content: query}}
	if answer, ok := syntheticAnaphoricDateAnswer("¿Y ella cuándo?", history); ok {
		t.Fatalf("anaphoric fallback guessed between two dated facts: %q", answer)
	}
}

const syntheticDateScenario = "Caso ficticio. No consultes Jira ni Granola ni uses datos externos. Nuria enviará la versión final el viernes 6 de noviembre. Resume el dato."

type sourceScopeTestState struct {
	intentCalls   int
	jiraReads     int
	granolaReads  int
	modelCalls    int
	modelMessages [][]Turn
}

func newSourceScopeTestService(t *testing.T, modelAnswer string, state *sourceScopeTestState) *Service {
	t.Helper()
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		state.modelCalls++
		var request struct {
			Messages []Turn `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		state.modelMessages = append(state.modelMessages, append([]Turn(nil), request.Messages...))
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"content": modelAnswer}}}})
	}))
	t.Cleanup(model.Close)

	intent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		state.intentCalls++
		payload, _ := json.Marshal(intentWireResult{
			SchemaVersion: "1", ProfileVersion: "donna-v1", Status: "resolved",
			Goal: stringPointer("lookup"), SuggestedCapabilities: []string{"jira.read"}, Basis: "deterministic",
		})
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(payload)
	}))
	t.Cleanup(intent.Close)

	service := New(model.URL, "key", "instructions", t.TempDir(), map[int64]bool{7: true})
	service.Intent = &IntentHTTPClient{URL: intent.URL, Token: "test-secret", Client: intent.Client()}
	service.IntentMode = "enabled"
	service.Capabilities = []ReadCapability{
		{Name: "Granola", Description: "notas", Match: func(string) bool { return true }, Read: func(context.Context, string) (CapabilityResult, error) {
			state.granolaReads++
			return CapabilityResult{Text: "dato externo de Granola", ResultCount: 1}, nil
		}},
		{Name: "Jira", Description: "incidencias", Match: jiraTaskIntent, Read: func(context.Context, string) (CapabilityResult, error) {
			state.jiraReads++
			return CapabilityResult{Text: "DEMO-42: estado abierto", ResultCount: 1, DirectAnswer: true}, nil
		}},
	}
	return service
}

func containsSyntheticFollowUpInstruction(messages [][]Turn) bool {
	if len(messages) < 2 || len(messages[1]) == 0 {
		return false
	}
	return strings.Contains(strings.ToLower(messages[1][0].Content), "seguimiento de un caso ficticio anterior")
}
