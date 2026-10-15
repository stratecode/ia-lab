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
	meetings, err := c.GranolaResult(context.Background())
	if err != nil || !strings.Contains(meetings.Text, "Atlas & Team") || !strings.Contains(meetings.Text, "2026-10-10") || !strings.Contains(meetings.Text, "Ana preparará la propuesta") || strings.Contains(meetings.Text, "private discussion details") {
		t.Fatalf("Granola %q %v", meetings.Text, err)
	}
	if meetings.ResultCount != 1 || len(meetings.Evidence) != 1 || meetings.Evidence[0].Provider != "Granola" || meetings.Evidence[0].ID != "meet-1" || meetings.Evidence[0].Title != "Atlas & Team" {
		t.Fatalf("Granola evidence missing provider-backed meeting reference: %+v", meetings)
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

func TestGranolaQueryPassesQuestionAndPreservesCitation(t *testing.T) {
	const query = "¿Qué decisiones y responsables aparecen en Stonecare el 14 de octubre?"
	var gotQuery string
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
		case "tools/list":
			io.WriteString(w, `{"jsonrpc":"2.0","id":2,"result":{"tools":[{"name":"query_granola_meetings","inputSchema":{"type":"object","required":["query"]}}]}}`)
		case "tools/call":
			if req.Params.Name != "query_granola_meetings" {
				t.Errorf("unexpected Granola tool %q", req.Params.Name)
			}
			gotQuery, _ = req.Params.Arguments["query"].(string)
			text := "Iris revisa textos y Fran implementa los cambios. [[1]](https://notes.granola.ai/d/meeting-1)"
			b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{"content": []any{map[string]string{"type": "text", "text": text}}}})
			w.Write(b)
		default:
			t.Errorf("unexpected MCP method %q", req.Method)
		}
	}))
	defer server.Close()
	c := &Connectors{Config: ConnectorConfig{Granola: OAuthConfig{ServerURL: server.URL, AccessToken: "secret"}}, Client: server.Client(), allowTestHTTP: true}
	result, err := c.GranolaQueryResult(context.Background(), query)
	if err != nil {
		t.Fatal(err)
	}
	if gotQuery != query || !result.DirectAnswer || !strings.Contains(result.Text, "Iris revisa") {
		t.Fatalf("query=%q result=%+v", gotQuery, result)
	}
	if result.ResultCount != 1 || len(result.Evidence) != 1 || result.Evidence[0].ID != "meeting-1" || result.Evidence[0].URL != "https://notes.granola.ai/d/meeting-1" {
		t.Fatalf("citation not preserved as evidence: %+v", result)
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
	var gotSearchArgs map[string]any
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
			if req.Params.Name == "search" {
				gotSearchArgs = req.Params.Arguments
			}
			text := `{"results":[{"key":"STRAT-42","title":"Plan de lanzamiento","snippet":"Revisar tareas pendientes","url":"https://stratecode.atlassian.net/browse/STRAT-42"}]}`
			if req.Params.Name == "getJiraIssue" {
				if req.Params.Arguments["issueIdOrKey"] != "STRAT-42" || req.Params.Arguments["updateHistory"] != false {
					t.Errorf("unexpected exact Jira lookup arguments: %#v", req.Params.Arguments)
				}
				text = `{"key":"STRAT-42","fields":{"summary":"Plan de lanzamiento","status":{"name":"En curso"},"assignee":{"displayName":"Ana Pérez"},"duedate":null}}`
			} else if req.Params.Name != "search" {
				t.Errorf("unexpected Jira tool %q", req.Params.Name)
			}
			b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{"content": []any{map[string]string{"type": "text", "text": text}}}})
			w.Write(b)
		default:
			t.Errorf("unexpected MCP method %q", req.Method)
		}
	}))
	defer server.Close()
	c := &Connectors{Config: ConnectorConfig{Jira: OAuthConfig{ServerURL: server.URL, AccessToken: "secret", CloudID: "cloud-1"}}, Client: server.Client(), allowTestHTTP: true}

	const question = "¿Qué tareas importantes tengo pendientes esta semana?"
	result, err := c.RovoResult(context.Background(), question)
	if err != nil {
		t.Fatal(err)
	}
	if gotName != "getJiraIssue" || gotArgs["cloudId"] != "cloud-1" || gotArgs["issueIdOrKey"] != "STRAT-42" || gotArgs["updateHistory"] != false {
		t.Fatalf("unexpected Rovo call %q %#v", gotName, gotArgs)
	}
	if gotSearchArgs["query"] != question || gotSearchArgs["targetApp"] != "JIRA" || gotSearchArgs["limit"] != float64(10) {
		t.Fatalf("Jira search was not scoped to relevant issue results: %#v", gotSearchArgs)
	}
	if !strings.Contains(result.Text, "Plan de lanzamiento") || !strings.Contains(result.Text, "En curso") || !strings.Contains(result.Text, "Ana Pérez") || !strings.Contains(result.Text, "sin vencimiento") {
		t.Fatalf("verified Jira fields were not preserved: %q", result.Text)
	}
	if result.ResultCount != 1 || len(result.Evidence) != 1 || result.Evidence[0].ID != "STRAT-42" || result.Evidence[0].URL != "https://stratecode.atlassian.net/browse/STRAT-42" {
		t.Fatalf("Rovo evidence was not sourced from the provider response: %+v", result)
	}
}

func TestRovoEvidenceDoesNotPromoteRequestIDsToSourceEvidence(t *testing.T) {
	raw := json.RawMessage(`{"request":{"id":"request-123"},"results":[{"title":"Plan sin referencia estable"}]}`)
	if evidence := parseRovoEvidence(raw); len(evidence) != 0 {
		t.Fatalf("non-source request metadata became evidence: %+v", evidence)
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
