package secretary

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stratecode/lab/internal/donnaidentity"
)

func TestJiraMappingCandidatesPreserveIdenticalNamesAndSourceIDs(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("query") != "Ana" || r.URL.Query().Get("maxResults") != "50" {
			t.Errorf("unexpected bounded Jira query: %s", r.URL.RawQuery)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `[{"accountId":"account-a","displayName":"Ana García","active":true},{"accountId":"account-b","displayName":"Ana García","active":true},{"accountId":"inactive","displayName":"Ana García","active":false}]`)
	}))
	defer server.Close()
	connectors := &Connectors{Config: ConnectorConfig{Jira: JiraRESTConfig{AccessToken: "jira-test", CloudID: "cloud-1"}}, Client: server.Client(), allowTestHTTP: true, jiraAPIBase: server.URL}
	options, err := connectors.FindMappingOptions(context.Background(), donnaidentity.EntityPerson, "Ana")
	if err != nil {
		t.Fatal(err)
	}
	if len(options) != 2 {
		t.Fatalf("got %d active candidates, want 2", len(options))
	}
	if options[0].CanonicalLabel != options[1].CanonicalLabel || options[0].ExternalID == options[1].ExternalID {
		t.Fatalf("same names were merged or IDs lost: %#v", options)
	}
	for _, option := range options {
		if option.CanonicalID == "" || option.ExternalID == "" {
			t.Fatalf("candidate lacks source ID: %#v", option)
		}
	}
}

func TestJiraProjectMappingCandidatesUseProjectKeys(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"values":[{"key":"PMI","name":"Project PMI"},{"key":"bad key","name":"Malformed"}]}`)
	}))
	defer server.Close()
	connectors := &Connectors{Config: ConnectorConfig{Jira: JiraRESTConfig{AccessToken: "jira-test", CloudID: "cloud-1"}}, Client: server.Client(), allowTestHTTP: true, jiraAPIBase: server.URL}
	options, err := connectors.FindMappingOptions(context.Background(), donnaidentity.EntityProject, "PMI")
	if err != nil || len(options) != 1 || options[0].CanonicalID != "PMI" || options[0].ExternalID != "PMI" {
		t.Fatalf("project key candidate=%#v err=%v", options, err)
	}
}
