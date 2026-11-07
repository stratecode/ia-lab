package secretary

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestBoundedHistoryKeepsTenExchangesAndExpiresAfterTwoHours(t *testing.T) {
	now := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
	turns := make([]Turn, 0, 24)
	for i := 0; i < 12; i++ {
		at := now.Add(-time.Duration(12-i) * time.Minute)
		turns = append(turns, Turn{Role: "user", Content: strings.Repeat("p", 500), At: at}, Turn{Role: "assistant", Content: strings.Repeat("r", 500), At: at})
	}
	got := boundedAt(turns, now)
	if len(got) != 20 || got[0].At != turns[4].At {
		t.Fatalf("history kept %d turns, first timestamp %v; want latest 10 exchanges", len(got), got[0].At)
	}
	for i := range got {
		got[i].At = now.Add(-3 * time.Hour)
	}
	if expired := boundedAt(got, now); len(expired) != 0 {
		t.Fatalf("history retained %d expired turns", len(expired))
	}
}

func TestConversationHistoryIsScopedByChannelAndConversation(t *testing.T) {
	var modelMessages [][]Turn
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Messages []Turn `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		modelMessages = append(modelMessages, request.Messages)
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}]}`))
	}))
	defer upstream.Close()
	s := New(upstream.URL, "key", "instructions", t.TempDir(), map[int64]bool{7: true})
	for _, turn := range []struct{ channel, conversation, text string }{
		{"telegram", "chat-a", "secreto del chat A"},
		{"telegram", "chat-b", "mensaje del chat B"},
		{"telegram", "chat-a", "¿Qué te he dicho?"},
		{"openwebui", "chat-c", "contenido del chat C"},
	} {
		if _, err := s.ReplyInConversation(context.Background(), 7, turn.channel, turn.conversation, turn.text); err != nil {
			t.Fatal(err)
		}
	}
	if !strings.Contains(joinTurnContent(modelMessages[2]), "secreto del chat A") {
		t.Fatal("same conversation did not retain its context")
	}
	for _, index := range []int{1, 3} {
		if strings.Contains(joinNonSystemTurnContent(modelMessages[index]), "secreto del chat A") {
			t.Fatalf("conversation %d received another chat's short-term history: %s", index, joinNonSystemTurnContent(modelMessages[index]))
		}
	}
}

func TestStandaloneScenarioDoesNotReceiveUnrelatedConversationHistory(t *testing.T) {
	var modelMessages [][]Turn
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Messages []Turn `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		modelMessages = append(modelMessages, request.Messages)
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"El módulo espera dos formularios."}}]}`))
	}))
	defer upstream.Close()
	s := New(upstream.URL, "key", "instructions", t.TempDir(), map[int64]bool{7: true})
	key := s.historyKey(7, "telegram", "7")
	s.history[key] = []Turn{
		{Role: "user", Content: "Caso ficticio: Catálogo Norte espera tres fotografías."},
		{Role: "assistant", Content: "Elena enviará las fotos el jueves."},
	}

	query := "Caso ficticio nuevo: el módulo Bruma espera dos formularios; Laura los enviará el miércoles. Resume el bloqueo."
	answer, err := s.ReplyInConversation(context.Background(), 7, "telegram", "7", query)
	if err != nil {
		t.Fatal(err)
	}
	if len(modelMessages) == 0 {
		t.Fatalf("synthetic scenario bypassed answer generation: %s", answer)
	}
	content := joinTurnContent(modelMessages[0])
	if !strings.Contains(content, "formularios") || !strings.Contains(content, "Bruma") {
		t.Fatalf("current scenario missing from model input: %s", content)
	}
	if strings.Contains(content, "fotografías") || strings.Contains(content, "fotos") || strings.Contains(content, "Catálogo Norte") {
		t.Fatalf("unrelated scenario leaked into model input: %s", content)
	}
}

func TestExplicitShortFollowUpKeepsRecentConversationContext(t *testing.T) {
	var modelMessages [][]Turn
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Messages []Turn `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		modelMessages = append(modelMessages, request.Messages)
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"El jueves."}}]}`))
	}))
	defer upstream.Close()
	s := New(upstream.URL, "key", "instructions", t.TempDir(), map[int64]bool{7: true})
	key := s.historyKey(7, "telegram", "7")
	s.history[key] = []Turn{
		{Role: "user", Content: "Caso ficticio: Laura enviará los formularios del módulo Bruma."},
		{Role: "assistant", Content: "El siguiente hito es recibirlos el jueves."},
	}

	if _, err := s.ReplyInConversation(context.Background(), 7, "telegram", "7", "¿Y cuándo los enviará?"); err != nil {
		t.Fatal(err)
	}
	content := joinTurnContent(modelMessages[0])
	if !strings.Contains(content, "módulo Bruma") || !strings.Contains(content, "formularios") {
		t.Fatalf("follow-up lost its conversation anchor: %s", content)
	}
}

func TestAnaphoricFollowUpKeepsRecentSyntheticFacts(t *testing.T) {
	history := []Turn{
		{Role: "user", Content: "Caso ficticio: Nuria enviará la versión final de Bruma el viernes 6 de noviembre."},
		{Role: "assistant", Content: "Nuria enviará la versión final de Bruma el 6 de noviembre."},
	}
	query := "¿Y ella cuándo la enviará?"
	if !isConversationalFollowUp(query) {
		t.Fatal("anaphoric follow-up was not recognized")
	}
	got := conversationHistoryForTurn(query, history)
	if len(got) != len(history) || !strings.Contains(joinTurnContent(got), "viernes 6 de noviembre") {
		t.Fatalf("anaphoric follow-up lost its source facts: %#v", got)
	}
}

func TestExplicitTopicReturnRecoversMatchingTurnOutsideRecentWindow(t *testing.T) {
	history := []Turn{
		{Role: "user", Content: "Caso ficticio de Bruma: Ana terminó el inventario; Leo corregirá el borrador; Nuria enviará la versión final el viernes 6 de noviembre."},
		{Role: "assistant", Content: "Nuria enviará la versión final de Bruma el 6 de noviembre."},
		{Role: "user", Content: "Caso ficticio de Delta: presupuesto aprobado, fecha de inicio no consta."},
		{Role: "assistant", Content: "¿Qué día acordaron para iniciar Delta?"},
		{Role: "user", Content: "Cambio de tema: ¿cuánto es 23 + 19?"},
		{Role: "assistant", Content: "42."},
		{Role: "user", Content: "Caso ficticio de Lirio: Roberta confirmará el aforo el miércoles."},
		{Role: "assistant", Content: "Roberta confirmará el aforo el miércoles."},
		{Role: "user", Content: "Fuentes ficticias de Prisma: Mei revisará el informe."},
		{Role: "assistant", Content: "Mei comprobará el informe."},
	}
	query := "Volviendo a Bruma, ¿qué hará Leo y qué terminó Ana?"
	got := conversationHistoryForTurn(query, history)
	joined := joinTurnContent(got)
	if len(got) != 2 || !strings.Contains(joined, "Leo corregirá el borrador") || !strings.Contains(joined, "Ana terminó el inventario") {
		t.Fatalf("topic return did not recover the matching source turn: %#v", got)
	}
	if strings.Contains(joined, "Prisma") || strings.Contains(joined, "Lirio") {
		t.Fatalf("topic return mixed unrelated turns: %#v", got)
	}
}

func TestStandaloneSyntheticScenarioDoesNotReceivePersistentMemory(t *testing.T) {
	var modelMessages [][]Turn
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Messages []Turn `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		modelMessages = append(modelMessages, request.Messages)
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"No consta la fecha. ¿Qué día acordó el equipo?"}}]}`))
	}))
	defer upstream.Close()
	state := t.TempDir()
	s := New(upstream.URL, "key", "instructions", state, map[int64]bool{7: true})
	s.Learning = NewJSONLLearningStore(state)
	now := time.Now()
	if err := s.appendMemoryAt(7, "telegram", "7", "Proyecto Cobalto: el plazo original era el 15 de mayo.", "La fecha registrada es el 15 de mayo.", now); err != nil {
		t.Fatal(err)
	}
	if err := s.Learning.SaveKnowledge(KnowledgeItem{
		OperatorID: 7, Type: KnowledgeTypeRelationship, Status: KnowledgeApproved,
		Subject: "Proyecto Cobalto", Predicate: "plazo de entrega", Value: "15 de mayo",
	}); err != nil {
		t.Fatal(err)
	}

	query := "Caso ficticio nuevo: el proyecto Orquídea cambió el plazo y la fecha nueva no consta. ¿Qué día acordó el equipo?"
	answer, err := s.ReplyInConversation(context.Background(), 7, "telegram", "7", query)
	if err != nil {
		t.Fatal(err)
	}
	if len(modelMessages) == 0 {
		t.Fatalf("synthetic scenario bypassed answer generation: %s", answer)
	}
	content := joinTurnContent(modelMessages[0])
	if !strings.Contains(content, "proyecto Orquídea") || !strings.Contains(content, "no consta") {
		t.Fatalf("current synthetic scenario missing from model input: %s", content)
	}
	if !strings.Contains(content, "usa como hechos los datos") || !strings.Contains(content, "pregunta concreta") {
		t.Fatalf("closed-world response guidance missing from model input: %s", content)
	}
	if strings.Contains(content, "15 de mayo") || strings.Contains(content, "Proyecto Cobalto") {
		t.Fatalf("unrelated persistent memory leaked into synthetic scenario: %s", content)
	}
}

func TestSyntheticFutureActionIsNotTreatedAsExternalWrite(t *testing.T) {
	modelCalls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		modelCalls++
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"Iván enviará el informe el jueves."}}]}`))
	}))
	defer upstream.Close()
	s := New(upstream.URL, "key", "instructions", t.TempDir(), map[int64]bool{7: true})

	answer, err := s.ReplyInConversation(context.Background(), 7, "telegram", "7", "Caso ficticio: Iván enviará el informe el jueves. ¿Quién lo envía y cuándo?")
	if err != nil {
		t.Fatal(err)
	}
	if modelCalls != 1 || answer != "Iván enviará el informe el jueves." {
		t.Fatalf("synthetic description was blocked as an external write: calls=%d answer=%q", modelCalls, answer)
	}
}

func TestSyntheticClosedWorldAnswersStayWithinSuppliedFacts(t *testing.T) {
	if got, ok := syntheticClosedWorldAnswer("Caso ficticio: no consultes Jira, Granola ni otras fuentes. Para el proyecto Lirio, Roberta confirmará el aforo el miércoles; la apertura está prevista para el jueves. ¿Qué queda pendiente y para cuándo?"); !ok || got != "Queda pendiente: Roberta confirmará el aforo el miércoles; la apertura está prevista para el jueves." {
		t.Fatalf("pending-action answer = %q, %v", got, ok)
	}
	if _, ok := syntheticClosedWorldAnswer("Caso ficticio: Ana enviará las fotos el jueves; Leo aprobará el texto el viernes. ¿Qué queda pendiente y para cuándo?"); ok {
		t.Fatal("multiple pending actions should remain with the model")
	}
	if got, ok := syntheticClosedWorldAnswer("Caso ficticio: Iván enviará el informe del proyecto Nube el jueves. ¿Quién lo envía y cuándo?"); !ok || got != "Iván enviará el informe del proyecto Nube el jueves." {
		t.Fatalf("direct fact answer = %q, %v", got, ok)
	}
	if got, ok := syntheticClosedWorldAnswer("Caso ficticio nuevo: el equipo cambió el plazo y la fecha nueva no consta. ¿Qué día acordó?"); !ok || got != "No consta la nueva fecha en los datos que compartes. ¿Qué día acordó el equipo?" {
		t.Fatalf("missing-date clarification = %q, %v", got, ok)
	}
	if got, ok := syntheticClosedWorldAnswer("CASO FICTICIO: El equipo dijo ‘cambiamos la fecha; la confirmaré por correo’, pero no indicó el día. ¿Cuál es la nueva fecha?"); !ok || got != "No consta la nueva fecha en los datos que compartes. ¿Qué día acordó el equipo?" {
		t.Fatalf("unconfirmed-date clarification = %q, %v", got, ok)
	}
	if _, ok := syntheticClosedWorldAnswer("CASO FICTICIO. No consultes fuentes reales; usa solo estos extractos inventados. Jira J-104: la exportación está bloqueada a la espera de validación legal; la fecha límite actual es 14 de noviembre. Nota de reunión: la validación llegó el lunes y Paula debe revisarla; no se acordó una fecha posterior. Resume bloqueo, siguiente paso y fecha conocida en dos frases."); ok {
		t.Fatal("source synthesis with a known deadline should remain with the model")
	}
	if _, ok := syntheticClosedWorldAnswer("Caso ficticio: la entrega se retrasa por dos aprobaciones; si llegan el jueves, se despliega el viernes. Resume el bloqueo."); ok {
		t.Fatal("summary request should remain with the model")
	}
	if _, ok := syntheticClosedWorldAnswer("Otra cosa: Iván enviará el informe el jueves. ¿Quién lo envía y cuándo?"); ok {
		t.Fatal("ordinary topic change should not use the synthetic-answer shortcut")
	}
}

func TestOpenWebUIUsesChatIDHeaderForHistoryScope(t *testing.T) {
	var modelMessages [][]Turn
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Messages []Turn `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&request)
		modelMessages = append(modelMessages, request.Messages)
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}]}`))
	}))
	defer upstream.Close()
	s := New(upstream.URL, "key", "instructions", t.TempDir(), map[int64]bool{7: true})
	for _, item := range []struct{ chat, text string }{{"one", "solo chat uno"}, {"two", "solo chat dos"}, {"one", "seguimiento"}} {
		request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"messages":[{"role":"user","content":"`+item.text+`"}]}`))
		request.Header.Set("Authorization", "Bearer key")
		request.Header.Set("X-OpenWebUI-User-Id", "owner")
		request.Header.Set("X-OpenWebUI-User-Role", "admin")
		request.Header.Set("X-OpenWebUI-Chat-Id", item.chat)
		result := httptest.NewRecorder()
		s.Handler().ServeHTTP(result, request)
		if result.Code != http.StatusOK {
			t.Fatalf("chat %q returned %d: %s", item.chat, result.Code, result.Body.String())
		}
	}
	if !strings.Contains(joinTurnContent(modelMessages[2]), "solo chat uno") || strings.Contains(joinTurnContent(modelMessages[2]), "solo chat dos") {
		t.Fatalf("Open WebUI chat history was not isolated: %s", joinTurnContent(modelMessages[2]))
	}
}

func TestOpenWebUIRejectsMissingConversationID(t *testing.T) {
	s := New("http://127.0.0.1:1", "key", "instructions", t.TempDir(), map[int64]bool{7: true})
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"messages":[{"role":"user","content":"Hola"}]}`))
	request.Header.Set("Authorization", "Bearer key")
	request.Header.Set("X-OpenWebUI-User-Id", "owner")
	request.Header.Set("X-OpenWebUI-User-Role", "admin")
	result := httptest.NewRecorder()
	s.Handler().ServeHTTP(result, request)
	if result.Code != http.StatusBadRequest || !strings.Contains(result.Body.String(), "chat ID required") {
		t.Fatalf("request without stable conversation ID returned %d: %s", result.Code, result.Body.String())
	}
}

func TestDailyMemoryStoresMetadataAndRecallsPreviousWeek(t *testing.T) {
	now := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
	s := New("", "", "", t.TempDir(), map[int64]bool{7: true})
	if err := s.appendMemoryAt(7, "telegram", "chat-a", "Acordamos revisar presupuesto Atlas", "Fran enviará la propuesta Atlas", now.AddDate(0, 0, -8)); err != nil {
		t.Fatal(err)
	}
	entries, err := s.relevantMemoriesAt(7, "telegram", "chat-a", "¿Qué acordamos la semana pasada sobre Atlas?", false, now)
	if err != nil || len(entries) != 1 || !strings.Contains(entries[0].Assistant, "propuesta Atlas") {
		t.Fatalf("last week memory=%#v err=%v", entries, err)
	}
	if entries[0].Channel != "telegram" || entries[0].Conversation == "" {
		t.Fatalf("memory metadata missing: %#v", entries[0])
	}
	path := s.dailyMemoryPath(7, now.AddDate(0, 0, -8))
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("daily partition missing at %s: %v", path, err)
	}
}

func TestDurableMemoryIsScopedByChannelAndConversation(t *testing.T) {
	now := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	s := New("", "", "", t.TempDir(), map[int64]bool{7: true})
	if err := s.appendMemoryAt(7, "telegram", "chat-a", "Acordamos revisar el proyecto Atlas", "Fran revisará el proyecto Atlas mañana", now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}

	for _, scope := range []struct{ channel, conversation string }{
		{"telegram", "chat-b"},
		{"openwebui", "chat-a"},
	} {
		entries, err := s.relevantMemoriesAt(7, scope.channel, scope.conversation, "¿Qué acordamos sobre el proyecto Atlas?", false, now)
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) != 0 {
			t.Fatalf("memory from telegram/chat-a leaked into %s/%s: %#v", scope.channel, scope.conversation, entries)
		}
	}

	entries, err := s.relevantMemoriesAt(7, "telegram", "chat-a", "¿Qué acordamos sobre el proyecto Atlas?", false, now)
	if err != nil || len(entries) != 1 || !strings.Contains(entries[0].Assistant, "revisará el proyecto Atlas") {
		t.Fatalf("same conversation memory=%#v err=%v", entries, err)
	}
}

func TestDurableConversationMemoryRequiresExplicitRecall(t *testing.T) {
	var requests [][]Turn
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []Turn `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		requests = append(requests, body.Messages)
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}]}`))
	}))
	defer upstream.Close()
	now := time.Now()
	s := New(upstream.URL, "key", "instructions", t.TempDir(), map[int64]bool{7: true})
	if err := s.appendMemoryAt(7, "openwebui", "chat-a", "Acordamos revisar presupuesto Atlas", "Fran enviará la propuesta Atlas el lunes", now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}

	if _, err := s.ReplyInConversation(context.Background(), 7, "openwebui", "chat-a", "Explícame el presupuesto Atlas"); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(joinTurnContent(requests[0]), "Fran enviará la propuesta Atlas") {
		t.Fatal("an ordinary request received a prior conversation exchange")
	}

	if _, err := s.ReplyInConversation(context.Background(), 7, "openwebui", "chat-a", "¿Qué acordamos sobre presupuesto Atlas?"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(joinTurnContent(requests[1]), "Fran enviará la propuesta Atlas") {
		t.Fatal("explicit conversation recall did not receive the matching prior exchange")
	}
}

func TestNaturalLanguageConversationRecallRoutesToMemory(t *testing.T) {
	for _, query := range []string{
		"¿Qué hablamos la semana pasada?",
		"¿Qué hablamos?",
		"¿Qué acordamos ayer sobre Atlas?",
		"¿Qué comentamos esta semana del lanzamiento?",
	} {
		if got := localIntent(query); got != intentMemory {
			t.Errorf("localIntent(%q) = %q, want %q", query, got, intentMemory)
		}
	}
}

func TestForgetDeletesDailyAndLegacyMemoryAndAllConversationHistory(t *testing.T) {
	s := New("", "", "", t.TempDir(), map[int64]bool{7: true})
	if err := os.MkdirAll(filepath.Dir(s.dailyMemoryPath(7, time.Now())), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(s.dailyMemoryPath(7, time.Now()), []byte("{}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(s.legacyMemoryPath(7), []byte("{}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	history := filepath.Join(s.StateDir, "history-7-some-chat.json")
	if err := os.WriteFile(history, []byte("[]"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := s.forget(7); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{s.dailyMemoryDir(7), s.legacyMemoryPath(7), history, s.historyPath(7)} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("forget left data at %s (err=%v)", path, err)
		}
	}
}

func joinTurnContent(turns []Turn) string {
	parts := make([]string, 0, len(turns))
	for _, turn := range turns {
		parts = append(parts, turn.Content)
	}
	return strings.Join(parts, "\n")
}

func joinNonSystemTurnContent(turns []Turn) string {
	parts := make([]string, 0, len(turns))
	for _, turn := range turns {
		if turn.Role != "system" {
			parts = append(parts, turn.Content)
		}
	}
	return strings.Join(parts, "\n")
}
