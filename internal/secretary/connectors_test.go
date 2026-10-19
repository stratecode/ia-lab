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

func TestGranolaMCPKeepsReadOnlyToolsAndRejectsJiraTools(t *testing.T) {
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
		result := `[]`
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
	c := &Connectors{Config: ConnectorConfig{Granola: OAuthConfig{ServerURL: server.URL, AccessToken: "secret"}}, Client: server.Client(), StateDir: t.TempDir(), allowTestHTTP: true}
	var err error
	meetings, err := c.GranolaResult(context.Background())
	if err != nil || !strings.Contains(meetings.Text, "Atlas & Team") || !strings.Contains(meetings.Text, "2026-10-10") || !strings.Contains(meetings.Text, "Ana preparará la propuesta") || strings.Contains(meetings.Text, "private discussion details") {
		t.Fatalf("Granola %q %v", meetings.Text, err)
	}
	if meetings.ResultCount != 1 || len(meetings.Evidence) != 1 || meetings.Evidence[0].Provider != "Granola" || meetings.Evidence[0].ID != "meet-1" || meetings.Evidence[0].Title != "Atlas & Team" {
		t.Fatalf("Granola evidence missing provider-backed meeting reference: %+v", meetings)
	}
	if len(calls) != 2 {
		t.Fatalf("unexpected calls %v", calls)
	}
	_, err = c.tool(context.Background(), c.Config.Granola, "searchJiraIssuesUsingJql", map[string]any{})
	if err == nil || len(calls) != 2 {
		t.Fatal("Jira tool was sent through Granola MCP")
	}
}

func TestConnectorDestinationsAndPartialCoverage(t *testing.T) {
	c := &Connectors{Config: ConnectorConfig{Jira: JiraRESTConfig{AccessToken: "secret"}}}
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
	c := &Connectors{Config: ConnectorConfig{Jira: JiraRESTConfig{AccessToken: "secret", CloudID: "cloud-1"}}, Client: server.Client(), allowTestHTTP: true, jiraAPIBase: server.URL}
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

func TestGranolaSummaryTruncationPreservesProjectOwnersAndNextSteps(t *testing.T) {
	const project = "# Proyectos Fran\n- SiteWatchman: revisar el XLS de inventario.\n\n"
	const actions = "# Próximos Pasos\n- Corregir la regla XLS de SiteWatchman antes del viernes.\n"
	if !explicitOwnerSection.MatchString("# Proyectos Fran") || !explicitActionSection.MatchString("# Próximos Pasos") {
		t.Fatal("test headings do not match the explicit assignment section rules")
	}
	longNarrative := strings.Repeat("Detalle de contexto de la reunión. ", 100)
	got := truncateGranolaSummary(project + longNarrative + "\n" + actions)
	if !strings.Contains(got, "# Próximos Pasos") || !strings.Contains(got, "# Proyectos Fran") {
		t.Fatalf("summary omitted structured sections: %q", got)
	}

	if len([]rune(got)) > 4000 {
		t.Fatalf("summary exceeded bounded context: %d runes", len([]rune(got)))
	}
	answer, ok := explicitAssignmentAnswer("¿Qué tarea tiene Fran para SiteWatchman y cuándo?", got, nil)
	if !ok {
		t.Fatalf("expected preserved project owner and next step to yield an assignment; summary ended with %q", got[max(0, len(got)-180):])
	}
	for _, expected := range []string{"Fran: Corregir la regla XLS de SiteWatchman", "antes del viernes"} {
		if !strings.Contains(answer, expected) {
			t.Errorf("answer omitted %q: %s", expected, answer)
		}
	}
}

func TestJiraUsesRESTJQLInsteadOfMCP(t *testing.T) {
	var gotJQL string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/ex/jira/cloud-1/rest/api/3/search/jql" {
			t.Errorf("Jira request = %s %s, want POST /ex/jira/cloud-1/rest/api/3/search/jql", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer service-token" {
			t.Error("Jira REST request did not use the configured service token")
		}
		var body struct {
			JQL string `json:"jql"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request: %v", err)
		}
		gotJQL = body.JQL
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"issues":[{"key":"STRAT-42","fields":{"summary":"Revisar propuesta","status":{"name":"En curso"},"duedate":null}}],"isLast":true}`)
	}))
	defer server.Close()
	c := &Connectors{
		Config: ConnectorConfig{Jira: JiraRESTConfig{AccessToken: "service-token", CloudID: "cloud-1", OperatorAccountID: "operator-123"}},
		Client: server.Client(), allowTestHTTP: true, jiraAPIBase: server.URL,
	}
	got, err := c.Jira(context.Background())
	if err != nil || !strings.Contains(got, "STRAT-42") {
		t.Fatalf("Jira REST result = %q, %v", got, err)
	}
	if gotJQL != `assignee = "operator-123" AND statusCategory != Done ORDER BY updated DESC` {
		t.Fatalf("JQL = %q", gotJQL)
	}
}

func TestJiraPersonalSearchRequiresConfiguredOperatorIdentity(t *testing.T) {
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		io.WriteString(w, `{"issues":[]}`)
	}))
	defer server.Close()
	connectors := &Connectors{
		Config: ConnectorConfig{Jira: JiraRESTConfig{AccessToken: "service-token", CloudID: "cloud-1"}},
		Client: server.Client(), allowTestHTTP: true, jiraAPIBase: server.URL,
	}
	if _, err := connectors.Jira(context.Background()); err == nil || !strings.Contains(err.Error(), "operator_account_id") {
		t.Fatalf("Jira() err = %v, want missing operator identity error", err)
	}
	if called {
		t.Fatal("Jira queried currentUser() for the service account")
	}
}

func TestJiraPrivateConfigDecodesOperatorAccountID(t *testing.T) {
	var config ConnectorConfig
	if err := json.Unmarshal([]byte(`{"jira":{"access_token":"synthetic-token","cloud_id":"cloud-1","operator_account_id":"operator-123"}}`), &config); err != nil {
		t.Fatal(err)
	}
	if config.Jira.OperatorAccountID != "operator-123" {
		t.Fatalf("operator_account_id = %q", config.Jira.OperatorAccountID)
	}
}

func TestCompileJiraJQLUsesSafeIssueAndTaskFilters(t *testing.T) {
	tests := []struct{ query, want string }{
		{"Dame el estado de SW-76", "key = SW-76 ORDER BY updated DESC"},
		{"¿Qué tareas tengo pendientes para esta semana?", `assignee = "operator-123" AND statusCategory != Done ORDER BY updated DESC`},
		{"¿Qué tarea en proceso requiere de mí?", `assignee = "operator-123" AND statusCategory = "In Progress" ORDER BY updated DESC`},
		{"¿Tengo alguna tarea en proceso que requiera de mí?", `assignee = "operator-123" AND statusCategory = "In Progress" ORDER BY updated DESC`},
		{"Busca el plan Atlas migración", `text ~ "atlas" AND text ~ "migracion" ORDER BY updated DESC`},
	}
	for _, tt := range tests {
		got, err := compileJiraJQL(tt.query, "operator-123")
		if err != nil || got != tt.want {
			t.Errorf("compileJiraJQL(%q) = %q, %v; want %q", tt.query, got, err, tt.want)
		}
	}
	if _, err := compileJiraJQL(strings.Repeat("x", 2001), "operator-123"); err == nil {
		t.Fatal("oversized natural-language query was accepted")
	}
	if _, err := compileJiraJQL("¿Qué tareas tengo pendientes?", ""); err == nil {
		t.Fatal("personal tasks fell back to the service account identity")
	}
	if _, err := compileJiraJQL("¿Qué tareas tengo pendientes?", `operator-123" OR assignee IS NOT EMPTY`); err == nil {
		t.Fatal("unsafe account id was accepted")
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
			io.WriteString(w, `{"jsonrpc":"2.0","id":2,"result":{"tools":[{"name":"query_granola_meetings","inputSchema":{"type":"object","required":["query"]}},{"name":"get_meetings","inputSchema":{"type":"object","required":["meeting_ids"]}}]}}`)
		case "tools/call":
			var text string
			if req.Params.Name == "query_granola_meetings" {
				gotQuery, _ = req.Params.Arguments["query"].(string)
				text = "Una respuesta orientativa. [[1]](https://notes.granola.ai/d/meeting-1)"
			} else if req.Params.Name == "get_meetings" {
				text = `<meetings><meeting id="meeting-1" title="Stonecare"><summary>Iris revisa textos y Fran implementa los cambios.</summary></meeting></meetings>`
			} else {
				t.Errorf("unexpected Granola tool %q", req.Params.Name)
			}
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
	if gotQuery != query || result.DirectAnswer || !strings.Contains(result.Text, "Iris revisa") || strings.Contains(result.Text, "respuesta orientativa") {
		t.Fatalf("query=%q result=%+v", gotQuery, result)
	}
	if result.ResultCount != 1 || len(result.Evidence) != 1 || result.Evidence[0].ID != "meeting-1" || result.Evidence[0].URL != "https://notes.granola.ai/d/meeting-1" {
		t.Fatalf("citation not preserved as evidence: %+v", result)
	}
}

func TestGranolaQueryUsesCitedMeetingNotesInsteadOfGeneratedAnswer(t *testing.T) {
	const query = "¿Quién revisó los textos y qué tenía que entregar Fran?"
	var queryCalls, detailCalls int
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
			io.WriteString(w, `{"jsonrpc":"2.0","id":2,"result":{"tools":[{"name":"query_granola_meetings","inputSchema":{"type":"object","required":["query"]}},{"name":"get_meetings","inputSchema":{"type":"object","required":["meeting_ids"]}}]}}`)
		case "tools/call":
			var text string
			switch req.Params.Name {
			case "query_granola_meetings":
				queryCalls++
				if req.Params.Arguments["query"] != query {
					t.Errorf("query = %v, want %q", req.Params.Arguments["query"], query)
				}
				text = "Fran debía enviar las imágenes definitivas. [[1]](https://notes.granola.ai/d/meeting-1)"
			case "get_meetings":
				detailCalls++
				ids, ok := req.Params.Arguments["meeting_ids"].([]any)
				if !ok || len(ids) != 1 || ids[0] != "meeting-1" {
					t.Errorf("meeting_ids = %v, want [meeting-1]", req.Params.Arguments["meeting_ids"])
				}
				text = `<meetings><meeting id="meeting-1" title="Stonecare"><summary>Próximos pasos: Enviar imágenes adaptadas a Fran (Paula, Alba). Revisar textos en inglés y castellano (Iris). Aplicar cambios y dejar la web lista para el viernes (Fran).</summary><transcript>private transcript omitted</transcript></meeting></meetings>`
			default:
				t.Errorf("unexpected Granola tool %q", req.Params.Name)
			}
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
	if queryCalls != 1 || detailCalls != 1 {
		t.Fatalf("query calls=%d detail calls=%d, want one each", queryCalls, detailCalls)
	}
	if result.DirectAnswer {
		t.Fatal("provider-generated query answer must not be treated as source-of-truth")
	}
	if !strings.Contains(result.Text, "Paula, Alba") || !strings.Contains(result.Text, "(Fran)") || strings.Contains(result.Text, "Fran debía enviar las imágenes") || strings.Contains(result.Text, "private transcript omitted") {
		t.Fatalf("result did not use only the cited note summary: %q", result.Text)
	}
	if result.ResultCount != 1 || len(result.Evidence) != 1 || result.Evidence[0].ID != "meeting-1" {
		t.Fatalf("citation evidence missing: %+v", result)
	}
}

func TestCredentialsDoNotCrossProviderBoundary(t *testing.T) {
	c := &Connectors{Config: ConnectorConfig{Jira: JiraRESTConfig{AccessToken: "jira-secret"}}}
	if _, err := c.Jira(context.Background()); err == nil {
		t.Fatal("Jira credential sent to another provider")
	}
}
