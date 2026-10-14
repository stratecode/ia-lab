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

func TestReadOnlyMCPKeepsSourcesAndRejectsWrites(t *testing.T) {
	calls := []string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer secret" {
			t.Error("missing OAuth auth")
		}
		var req struct {
			ID     int    `json:"id"`
			Method string `json:"method"`
			Params struct {
				Name string `json:"name"`
			} `json:"params"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		if req.Method == "notifications/initialized" {
			w.WriteHeader(202)
			return
		}
		if req.Method == "initialize" {
			io.WriteString(w, `{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2025-03-26","capabilities":{}}}`)
			return
		}
		if req.Method == "tools/list" {
			io.WriteString(w, `{"jsonrpc":"2.0","id":2,"result":{"tools":[{"name":"list_meetings","inputSchema":{"type":"object"}},{"name":"get_meetings","inputSchema":{"type":"object"}}]}}`)
			return
		}
		calls = append(calls, req.Params.Name)
		result := `[{"id":"cloud-1","url":"https://example.atlassian.net"}]`
		if req.Params.Name == "searchJiraIssuesUsingJql" {
			result = `{"issues":[{"key":"STRAT-42","fields":{"summary":"Revisar propuesta","status":{"name":"En curso"}}}]}`
		}
		if req.Params.Name == "list_meetings" {
			result = `<meetings_data count="1"><meeting id="meet-1" title="Atlas &amp; Team" date="2026-10-10"/></meetings_data>`
		}
		if req.Params.Name == "get_meetings" {
			result = `<meetings><meeting id="meet-1" title="Atlas &amp; Team" date="2026-10-10"><summary>Decidimos mantener el alcance actual. Ana preparará la propuesta.</summary><transcript>private discussion details</transcript></meeting></meetings>`
		}
		b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{"content": []any{map[string]string{"type": "text", "text": result}}}})
		w.Write(b)
	}))
	defer server.Close()
	c := &Connectors{Config: ConnectorConfig{Jira: OAuthConfig{ServerURL: server.URL, AccessToken: "secret"}, Granola: OAuthConfig{ServerURL: server.URL, AccessToken: "secret"}}, Client: server.Client(), StateDir: t.TempDir(), allowTestHTTP: true}
	jira, err := c.Jira(context.Background())
	if err != nil || !strings.Contains(jira, "STRAT-42") {
		t.Fatalf("Jira %q %v", jira, err)
	}
	meetings, err := c.Granola(context.Background())
	if err != nil || !strings.Contains(meetings, "Atlas & Team") || !strings.Contains(meetings, "2026-10-10") || !strings.Contains(meetings, "Ana preparará la propuesta") || strings.Contains(meetings, "private discussion details") {
		t.Fatalf("Granola %q %v", meetings, err)
	}
	if len(calls) != 4 {
		t.Fatalf("unexpected calls %v", calls)
	}
	_, err = c.tool(context.Background(), c.Config.Jira, "createJiraIssue", map[string]any{})
	if err == nil || len(calls) != 4 {
		t.Fatal("write was sent to provider")
	}
}

func TestConnectorDestinationsAndPartialCoverage(t *testing.T) {
	c := &Connectors{Config: ConnectorConfig{Jira: OAuthConfig{ServerURL: "https://attacker.invalid", AccessToken: "secret"}}}
	if _, err := c.Jira(context.Background()); err == nil {
		t.Fatal("credential destination accepted")
	}
	s := New("http://127.0.0.1:1", "key", "instructions", t.TempDir(), map[int64]bool{7: true})
	s.Sources = c
	result := s.readSources(context.Background(), "/resumen")
	if !strings.Contains(result, "Jira: no disponible") || !strings.Contains(result, "Granola: no disponible") {
		t.Fatalf("missing coverage %q", result)
	}
}

func TestUnknownSourceSchemaIsNotAnEmptyInbox(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		json.NewDecoder(r.Body).Decode(&req)
		if req["method"] == "notifications/initialized" {
			w.WriteHeader(202)
			return
		}
		if req["method"] == "initialize" {
			io.WriteString(w, `{"result":{"protocolVersion":"2025-03-26"}}`)
			return
		}
		io.WriteString(w, `{"result":{"content":[{"type":"text","text":"{}"}]}}`)
	}))
	defer server.Close()
	c := &Connectors{Config: ConnectorConfig{Jira: OAuthConfig{ServerURL: server.URL, AccessToken: "secret", CloudID: "cloud-1"}}, Client: server.Client(), allowTestHTTP: true}
	if _, err := c.Jira(context.Background()); err == nil {
		t.Fatal("unknown payload presented as zero issues")
	}
}

func TestGranolaMissingConnectionIsAnErrorNotSourceContent(t *testing.T) {
	connectors := NewConnectors(ConnectorConfig{}, t.TempDir())
	if _, err := connectors.Granola(context.Background()); err == nil {
		t.Fatal("expected unavailable Granola connection to be reported as an error")
	}
}

func TestCredentialsDoNotCrossProviderBoundary(t *testing.T) {
	c := &Connectors{Config: ConnectorConfig{Jira: OAuthConfig{ServerURL: "https://mcp.granola.ai/mcp", AccessToken: "jira-secret"}}}
	if _, err := c.Jira(context.Background()); err == nil {
		t.Fatal("Jira credential sent to another provider")
	}
}

func TestRovoSearchPassesNaturalLanguageToReadOnlyMCP(t *testing.T) {
	var gotName string
	var gotArgs map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID     int    `json:"id"`
			Method string `json:"method"`
			Params struct {
				Name      string         `json:"name"`
				Arguments map[string]any `json:"arguments"`
			} `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatal(err)
		}
		switch req.Method {
		case "initialize":
			io.WriteString(w, `{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2025-03-26","capabilities":{}}}`)
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "tools/call":
			gotName, gotArgs = req.Params.Name, req.Params.Arguments
			io.WriteString(w, `{"jsonrpc":"2.0","id":2,"result":{"content":[{"type":"text","text":"{\"results\":[{\"title\":\"Plan de lanzamiento\",\"snippet\":\"Revisar tareas pendientes\"}]}"}]}}`)
		default:
			t.Errorf("unexpected MCP method %q", req.Method)
		}
	}))
	defer server.Close()
	c := &Connectors{Config: ConnectorConfig{Jira: OAuthConfig{ServerURL: server.URL, AccessToken: "secret", CloudID: "cloud-1"}}, Client: server.Client(), allowTestHTTP: true}

	const question = "¿Qué tareas importantes tengo pendientes esta semana?"
	result, err := c.Rovo(context.Background(), question)
	if err != nil {
		t.Fatal(err)
	}
	if gotName != "search" || gotArgs["cloudId"] != "cloud-1" || gotArgs["query"] != question || gotArgs["targetApp"] != "ALL" || gotArgs["mode"] != "STANDARD" || gotArgs["limit"] != float64(10) {
		t.Fatalf("unexpected Rovo call %q %#v", gotName, gotArgs)
	}
	if !strings.Contains(result, "Plan de lanzamiento") || !strings.Contains(result, "Revisar tareas pendientes") {
		t.Fatalf("Rovo results were not preserved for synthesis: %q", result)
	}
}

func TestRovoResolvesCloudIDFromActualAtlassianResourceEnvelope(t *testing.T) {
	var searchCloudID string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID     int    `json:"id"`
			Method string `json:"method"`
			Params struct {
				Name      string         `json:"name"`
				Arguments map[string]any `json:"arguments"`
			} `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatal(err)
		}
		switch req.Method {
		case "initialize":
			io.WriteString(w, `{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2025-03-26","capabilities":{}}}`)
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "tools/call":
			var text string
			switch req.Params.Name {
			case "getAccessibleAtlassianResources":
				text = `{"data":{"resources":[{"cloudId":"stratecode-cloud","url":"https://stratecode.atlassian.net"}]}}`
			case "search":
				searchCloudID = req.Params.Arguments["cloudId"].(string)
				text = `{"results":[{"title":"Roadmap","snippet":"Plan aprobado"}]}`
			default:
				t.Errorf("unexpected MCP tool %q", req.Params.Name)
			}
			b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{"content": []any{map[string]string{"type": "text", "text": text}}}})
			w.Write(b)
		default:
			t.Errorf("unexpected MCP method %q", req.Method)
		}
	}))
	defer server.Close()
	c := &Connectors{Config: ConnectorConfig{Jira: OAuthConfig{ServerURL: server.URL, AccessToken: "secret"}}, Client: server.Client(), allowTestHTTP: true}
	if _, err := c.Rovo(context.Background(), "¿Qué plan está aprobado?"); err != nil {
		t.Fatal(err)
	}
	if searchCloudID != "stratecode-cloud" {
		t.Fatalf("Rovo search used cloud id %q", searchCloudID)
	}
}

func TestRovoEmptySearchDoesNotBecomeSynthesisContext(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID     int    `json:"id"`
			Method string `json:"method"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatal(err)
		}
		switch req.Method {
		case "initialize":
			io.WriteString(w, `{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2025-03-26","capabilities":{}}}`)
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "tools/call":
			io.WriteString(w, `{"jsonrpc":"2.0","id":2,"result":{"structuredContent":{"results":[]}}}`)
		default:
			t.Errorf("unexpected MCP method %q", req.Method)
		}
	}))
	defer server.Close()
	c := &Connectors{Config: ConnectorConfig{Jira: OAuthConfig{ServerURL: server.URL, AccessToken: "secret", CloudID: "cloud-1"}}, Client: server.Client(), allowTestHTTP: true}
	result, err := c.Rovo(context.Background(), "¿Qué tareas quedan pendientes?")
	if err != nil || result != "" {
		t.Fatalf("empty Rovo results=%q err=%v", result, err)
	}
}
