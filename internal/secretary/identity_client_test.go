package secretary

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stratecode/lab/internal/donnaidentity"
)

type guidedMappingSources struct{ options []MappingOption }

func (guidedMappingSources) Jira(context.Context) (string, error)    { return "", nil }
func (guidedMappingSources) Granola(context.Context) (string, error) { return "", nil }
func (guidedMappingSources) Status() map[string]bool                 { return map[string]bool{"Jira": true} }
func (s guidedMappingSources) FindMappingOptions(_ context.Context, kind donnaidentity.EntityKind, _ string) ([]MappingOption, error) {
	out := []MappingOption{}
	for _, option := range s.options {
		if option.Kind == kind {
			out = append(out, option)
		}
	}
	return out, nil
}

func newTestIdentityService(t *testing.T) (*httptest.Server, *IdentityClient) {
	t.Helper()
	store, err := donnaidentity.NewJSONStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(donnaidentity.NewHandler(store, donnaidentity.HeaderAuthenticator{Token: "test-token-identity-service-32-bytes"}))
	t.Cleanup(server.Close)
	return server, &IdentityClient{BaseURL: server.URL, Token: "test-token-identity-service-32-bytes"}
}

func TestGuidedMappingRanksExactAndKeepsSameNameAmbiguous(t *testing.T) {
	options := []MappingOption{
		{Kind: donnaidentity.EntityPerson, CanonicalID: "account-z", CanonicalLabel: "Ana García", Provider: "Jira", ExternalID: "account-z", Evidence: []string{"Jira user search"}},
		{Kind: donnaidentity.EntityPerson, CanonicalID: "account-a", CanonicalLabel: "Ana García", Provider: "Jira", ExternalID: "account-a", Evidence: []string{"Jira user search"}},
		{Kind: donnaidentity.EntityPerson, CanonicalID: "account-3", CanonicalLabel: "Ana García López", Provider: "Jira", ExternalID: "account-3"},
		{Kind: donnaidentity.EntityPerson, CanonicalID: "missing-email", CanonicalLabel: "Ana García", Provider: "Jira"},
	}
	ranked := rankMappingOptions(options, "Ana García")
	if len(ranked) != 3 {
		t.Fatalf("ranked candidates=%d, want 3 (drop incomplete identifiers)", len(ranked))
	}
	if ranked[0].ExternalID != "account-a" || ranked[1].ExternalID != "account-z" {
		t.Fatalf("identical names should remain as stable, separate options: %#v", ranked)
	}
	for _, option := range ranked {
		if strings.Contains(option.CanonicalLabel, "@") {
			t.Fatalf("email was fabricated in candidate: %#v", option)
		}
	}
}

func TestGuidedMappingOnlyConfirmsSelectedCandidateAndBindsDigest(t *testing.T) {
	_, client := newTestIdentityService(t)
	s := New("http://model.invalid", "key", "", "", nil)
	s.Identity = client
	s.Sources = guidedMappingSources{options: []MappingOption{
		{Kind: donnaidentity.EntityPerson, CanonicalID: "acc-1", CanonicalLabel: "Ana García", Provider: "Jira", ExternalID: "acc-1", Evidence: []string{"Jira user search"}},
		{Kind: donnaidentity.EntityPerson, CanonicalID: "acc-2", CanonicalLabel: "Ana García", Provider: "Jira", ExternalID: "acc-2", Evidence: []string{"Jira user search"}},
	}}
	var progress []string
	answer, err := s.guidedMapping(context.Background(), 19, "telegram", "chat-19", "/mapeo persona Ana García", func(s string) { progress = append(progress, s) }, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(answer, "Jira no ha proporcionado emails") || strings.Count(answer, "/mapeo confirmar ") != 2 {
		t.Fatalf("unexpected candidate prompt: %s", answer)
	}
	if len(progress) != 1 || progress[0] != "Voy a buscar candidatos en Jira." {
		t.Fatalf("expected Jira progress update, got %#v", progress)
	}
	principal, _ := NewOperatorContext(19, "telegram", "chat-19")
	pending, err := client.ListMappings(context.Background(), principal, "", donnaidentity.MappingPending)
	if err != nil || len(pending) != 2 {
		t.Fatalf("pending candidates=%d, err=%v", len(pending), err)
	}
	selected := pending[0]
	wrong := selected.PayloadDigest
	wrong = wrong[:len(wrong)-1] + "0"
	if wrong == selected.PayloadDigest {
		wrong = wrong[:len(wrong)-1] + "1"
	}
	answer, err = s.guidedMapping(context.Background(), 19, "telegram", "chat-19", "/mapeo confirmar "+selected.ID+" "+wrong, nil, nil)
	if err != nil || !strings.Contains(answer, "huella no coincide") {
		t.Fatalf("changed candidate digest should fail closed, answer=%q err=%v", answer, err)
	}
	answer, err = s.guidedMapping(context.Background(), 19, "telegram", "chat-19", "/mapeo confirmar "+selected.ID+" "+selected.PayloadDigest, nil, nil)
	if err != nil || !strings.Contains(answer, "Mapeo confirmado") {
		t.Fatalf("exact candidate confirmation failed: %q, %v", answer, err)
	}
	approved, err := client.ListMappings(context.Background(), principal, "", donnaidentity.MappingApproved)
	if err != nil || len(approved) != 1 || approved[0].ID != selected.ID {
		t.Fatalf("approved candidates=%#v, err=%v", approved, err)
	}
}

func TestGuidedMappingMissingOpenWebUIOperatorFailsClosed(t *testing.T) {
	_, client := newTestIdentityService(t)
	s := New("http://model.invalid", "key", "", "", nil)
	s.Identity = client
	answer, err := s.guidedMapping(context.Background(), 0, "openwebui", "webui-chat", "/mapeo persona Ana", nil, nil)
	if err != nil || !strings.Contains(answer, "identidad autenticada") {
		t.Fatalf("missing identity must fail closed, got %q err=%v", answer, err)
	}
	if _, err := NewOperatorContext(19, "openwebui", ""); err == nil {
		t.Fatal("missing Open WebUI conversation must be rejected")
	}
}

func TestGuidedMappingAddsOperatorSuppliedEmailAndProjectAlias(t *testing.T) {
	_, client := newTestIdentityService(t)
	principal, _ := NewOperatorContext(31, "openwebui", "owui-chat-31")
	for _, root := range []donnaidentity.MappingCandidate{
		{Kind: donnaidentity.EntityPerson, CanonicalID: "jira-person-31", CanonicalLabel: "Valentina Lagos", Provider: "Jira", ExternalID: "jira-person-31"},
		{Kind: donnaidentity.EntityProject, CanonicalID: "PMI", CanonicalLabel: "PMI", Provider: "Jira", ExternalID: "PMI"},
	} {
		candidate, err := client.CreateCandidate(context.Background(), principal, root)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = client.ConfirmMapping(context.Background(), principal, candidate.ID, candidate.PayloadDigest, true); err != nil {
			t.Fatal(err)
		}
	}
	s := New("http://model.invalid", "key", "", "", nil)
	s.Identity = client
	for _, command := range []string{"/mapeo email valentina@example.test para jira-person-31", "/mapeo alias responsable de PMI para PMI"} {
		answer, err := s.guidedMapping(context.Background(), 31, "openwebui", "owui-chat-31", command, nil, nil)
		if err != nil || !strings.Contains(answer, "Candidato") || !strings.Contains(answer, "/mapeo confirmar") {
			t.Fatalf("command %q response=%q err=%v", command, answer, err)
		}
	}
	pending, err := client.ListMappings(context.Background(), principal, "", donnaidentity.MappingPending)
	if err != nil || len(pending) != 2 {
		t.Fatalf("pending operator candidates=%#v err=%v", pending, err)
	}
	for _, item := range pending {
		answer, confirmErr := s.guidedMapping(context.Background(), 31, "openwebui", "owui-chat-31", "/mapeo confirmar "+item.ID+" "+item.PayloadDigest, nil, nil)
		if confirmErr != nil || !strings.Contains(answer, "Mapeo confirmado") {
			t.Fatalf("confirmation response=%q err=%v", answer, confirmErr)
		}
	}
	items, err := client.ApprovedKnowledge(context.Background(), 31)
	if err != nil {
		t.Fatal(err)
	}
	if got := ResolveEntity("valentina@example.test", EntityPerson, items); got.Status != EntityResolved {
		t.Fatalf("confirmed email did not resolve: %#v", got)
	}
	if got := ResolveEntity("responsable de PMI", EntityProject, items); got.Status != EntityResolved || got.Item == nil || got.Item.Value != "PMI" {
		t.Fatalf("confirmed project alias did not resolve: %#v", got)
	}
}

func TestApprovedIdentityMappingsEnterDeterministicEntityResolution(t *testing.T) {
	_, client := newTestIdentityService(t)
	principal, _ := NewOperatorContext(23, "telegram", "source-chat")
	candidate, err := client.CreateCandidate(context.Background(), principal, donnaidentity.MappingCandidate{Kind: donnaidentity.EntityPerson, CanonicalID: "jira-acc-23", CanonicalLabel: "Valentina Lagos", Provider: "Jira", ExternalID: "jira-acc-23", Aliases: []string{"Vale"}, Evidence: []string{"Jira user search"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = client.ConfirmMapping(context.Background(), principal, candidate.ID, candidate.PayloadDigest, true); err != nil {
		t.Fatal(err)
	}
	items, err := client.ApprovedKnowledge(context.Background(), 23)
	if err != nil {
		t.Fatal(err)
	}
	got := ResolveEntity("Vale", EntityPerson, items)
	if got.Status != EntityResolved || got.Item == nil || got.Item.Value != "jira-acc-23" {
		t.Fatalf("approved identity did not resolve by exact approved alias: %#v", got)
	}
	got = ResolveEntity("jIra-aCc-23", EntityPerson, items)
	if got.Status != EntityResolved || got.Item == nil {
		t.Fatalf("approved external ID did not resolve case-insensitively: %#v", got)
	}
	for _, addition := range []struct {
		kind  donnaidentity.EntityKind
		value string
	}{{donnaidentity.EntityEmail, "valentina@example.test"}, {donnaidentity.EntityAlias, "La Vale"}} {
		candidate, createErr := client.CreateCandidate(context.Background(), principal, donnaidentity.MappingCandidate{Kind: addition.kind, CanonicalID: "jira-acc-23", CanonicalLabel: "Valentina Lagos", Provider: "Jira", ExternalID: "jira-acc-23", Aliases: []string{addition.value}})
		if createErr != nil {
			t.Fatal(createErr)
		}
		if _, confirmErr := client.ConfirmMapping(context.Background(), principal, candidate.ID, candidate.PayloadDigest, true); confirmErr != nil {
			t.Fatal(confirmErr)
		}
	}
	items, err = client.ApprovedKnowledge(context.Background(), 23)
	if err != nil || len(items) != 1 {
		t.Fatalf("aliases should enrich one canonical identity, items=%#v err=%v", items, err)
	}
	for _, alias := range []string{"valentina@example.test", "La Vale"} {
		if got := ResolveEntity(alias, EntityPerson, items); got.Status != EntityResolved {
			t.Fatalf("approved alias %q did not resolve: %#v", alias, got)
		}
	}
	project, err := client.CreateCandidate(context.Background(), principal, donnaidentity.MappingCandidate{Kind: donnaidentity.EntityProject, CanonicalID: "PMI", CanonicalLabel: "Project PMI", Provider: "Jira", ExternalID: "PMI"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = client.ConfirmMapping(context.Background(), principal, project.ID, project.PayloadDigest, true); err != nil {
		t.Fatal(err)
	}
	projectAlias, err := client.CreateCandidate(context.Background(), principal, donnaidentity.MappingCandidate{Kind: donnaidentity.EntityAlias, CanonicalID: "PMI", CanonicalLabel: "Project PMI", Provider: "Jira", ExternalID: "PMI", Aliases: []string{"Proyecto de Valentina"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = client.ConfirmMapping(context.Background(), principal, projectAlias.ID, projectAlias.PayloadDigest, true); err != nil {
		t.Fatal(err)
	}
	items, err = client.ApprovedKnowledge(context.Background(), 23)
	if err != nil {
		t.Fatal(err)
	}
	if got := ResolveEntity("Proyecto de Valentina", EntityProject, items); got.Status != EntityResolved || got.Item == nil || got.Item.Value != "PMI" {
		t.Fatalf("project alias failed to resolve: %#v", got)
	}
}

func TestIdentityClientUsesTrustedPrincipalHeaders(t *testing.T) {
	seen := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = true
		if r.Header.Get("X-Operator-ID") != "19" || r.Header.Get("X-Channel") != "telegram" || r.Header.Get("X-Conversation-ID") != "chat-19" || r.Header.Get("Authorization") != "Bearer secret" {
			t.Errorf("principal headers missing")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("[]"))
	}))
	defer server.Close()
	client := IdentityClient{BaseURL: server.URL, Token: "secret"}
	principal, _ := NewOperatorContext(19, "telegram", "chat-19")
	if _, err := client.ListMappings(context.Background(), principal, "", ""); err != nil {
		t.Fatal(err)
	}
	if !seen {
		t.Fatal("identity service was not called")
	}
}
