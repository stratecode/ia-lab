package secretary

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestResolveEntityUsesExactExternalIDAndApprovedAlias(t *testing.T) {
	items := []KnowledgeItem{
		{ID: "p-1", OperatorID: 7, Type: KnowledgeTypeEntityAlias, EntityType: EntityProject, Status: KnowledgeApproved, Aliases: []string{"Northstar"}, ExternalRefs: map[string]string{"jira": "PROJ-17"}},
		{ID: "p-2", OperatorID: 7, Type: KnowledgeTypeEntityAlias, EntityType: EntityProject, Status: KnowledgeApproved, Aliases: []string{"Southwind"}, ExternalRefs: map[string]string{"jira": "PROJ-18"}},
	}
	for _, query := range []string{"PROJ-17", "northstar"} {
		got := ResolveEntity(query, EntityProject, items)
		if got.Status != EntityResolved || got.Item == nil || got.Item.ID != "p-1" {
			t.Errorf("ResolveEntity(%q) = %+v", query, got)
		}
	}
	if got := ResolveEntity("PROJ-18", EntityProject, items); got.Status != EntityResolved || got.Item.ID != "p-2" {
		t.Fatalf("exact external ID = %+v", got)
	}
}

func TestResolveEntityLeavesAmbiguousAliasUnresolved(t *testing.T) {
	items := []KnowledgeItem{
		{ID: "person-1", Type: KnowledgeTypeEntityAlias, EntityType: EntityPerson, Status: KnowledgeApproved, Aliases: []string{"Alex García"}},
		{ID: "person-2", Type: KnowledgeTypeEntityAlias, EntityType: EntityPerson, Status: KnowledgeApproved, Aliases: []string{"Alex Garcia"}},
	}
	got := ResolveEntity("Alex García", EntityPerson, items)
	if got.Status != EntityAmbiguous || got.Item != nil || len(got.CandidateIDs) != 2 {
		t.Fatalf("same display name was resolved: %+v", got)
	}
	pending := KnowledgeItem{ID: "candidate-1", Type: KnowledgeTypeEntityAlias, EntityType: EntityPerson, Status: KnowledgeCandidate, Aliases: []string{"Alex García"}}
	if got := ResolveEntity("Alex García", EntityPerson, []KnowledgeItem{pending}); got.Status != EntityAmbiguous || got.Item != nil {
		t.Fatalf("pending alias should remain unresolved: %+v", got)
	}
}

func TestPendingKnowledgeIsNeverInjected(t *testing.T) {
	var requestBody string
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var data map[string]any
		_ = json.NewDecoder(r.Body).Decode(&data)
		encoded, _ := json.Marshal(data)
		requestBody = string(encoded)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"Respuesta."}}]}`))
	}))
	defer model.Close()
	state := t.TempDir()
	s := New(model.URL, "key", "instructions", state, map[int64]bool{7: true})
	s.Learning = NewJSONLLearningStore(state)
	if err := s.Learning.SaveKnowledge(KnowledgeItem{OperatorID: 7, Type: KnowledgeTypeFact, Status: KnowledgeCandidate, Subject: "proyecto Atlas", Value: "NO_INYECTAR_CANDIDATO_3bdc"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReplyInConversation(context.Background(), 7, "telegram", "chat-a", "Explícame el proyecto Atlas"); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(requestBody, "NO_INYECTAR_CANDIDATO_3bdc") {
		t.Fatalf("pending knowledge reached model: %s", requestBody)
	}
}

func TestApprovedKnowledgeIncludesSourceAndValidity(t *testing.T) {
	state := t.TempDir()
	s := New("", "", "", state, nil)
	s.Learning = NewJSONLLearningStore(state)
	start := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	item := KnowledgeItem{OperatorID: 7, Type: KnowledgeTypeRelationship, Status: KnowledgeApproved, Subject: "cliente Atlas", Predicate: "responsable", Value: "Marta Ruiz", CreatedAt: start, UpdatedAt: start, ValidFrom: start, ValidUntil: end, Sources: []EvidenceRef{{Provider: "jira", ID: "ATLAS-12", Title: "Cuenta Atlas", RetrievedAt: start}}}
	if err := s.Learning.SaveKnowledge(item); err != nil {
		t.Fatal(err)
	}
	got, err := s.relevantKnowledge(7, "quién es el responsable del cliente Atlas", time.Date(2026, 10, 15, 0, 0, 0, 0, time.UTC))
	if err != nil || len(got) != 1 {
		t.Fatalf("knowledge retrieval = %+v, %v", got, err)
	}
	context := formatMemoryContext(knowledgeAsMemories(got))
	for _, want := range []string{"Hecho aprobado", "Marta Ruiz", "ATLAS-12", "Validez: 2026-10-01 a 2026-11-01", "consultado 2026-10-01"} {
		if !strings.Contains(context, want) {
			t.Errorf("knowledge context missing %q: %s", want, context)
		}
	}
	if got, err := s.relevantKnowledge(7, "responsable cliente Atlas", end); err != nil || len(got) != 0 {
		t.Fatalf("expired knowledge remained valid: %+v, %v", got, err)
	}
}

func TestHistoricalKnowledgeDoesNotOverrideLiveSourceState(t *testing.T) {
	var prompt string
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var data struct {
			Messages []Turn `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&data)
		for _, turn := range data.Messages {
			prompt += turn.Content + "\n"
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"Jira indica que está cerrado."}}]}`))
	}))
	defer model.Close()
	state := t.TempDir()
	s := New(model.URL, "key", "instructions", state, map[int64]bool{7: true})
	s.Learning = NewJSONLLearningStore(state)
	if err := s.Learning.SaveKnowledge(KnowledgeItem{OperatorID: 7, Type: KnowledgeTypeFact, Status: KnowledgeApproved, Subject: "proyecto Atlas", Value: "Históricamente estaba abierto", CreatedAt: time.Now(), UpdatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	s.Capabilities = []ReadCapability{{Name: "Rovo", Description: "Jira", Match: func(string) bool { return true }, Read: func(context.Context, string) (CapabilityResult, error) {
		return CapabilityResult{Text: "Estado actual en Jira: cerrado", ResultCount: 1}, nil
	}}}
	if _, err := s.ReplyInConversation(context.Background(), 7, "telegram", "chat-a", "¿Qué estado actual tiene la tarea del proyecto Atlas?"); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Históricamente estaba abierto", "Estado actual en Jira: cerrado", "sus datos actuales prevalecen sobre la memoria"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("synthesis omitted source hierarchy %q: %s", want, prompt)
		}
	}
}
