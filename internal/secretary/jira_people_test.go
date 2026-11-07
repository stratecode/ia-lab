package secretary

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestJiraNamedPeopleTasks(t *testing.T) {
	for _, ambiguous := range []bool{false, true} {
		t.Run(fmt.Sprint(ambiguous), func(t *testing.T) {
			var searches []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "/user/search") {
					name := r.URL.Query().Get("query")
					if name != "Valentina" && name != "Gonzalo" {
						t.Errorf("unexpected person %q", name)
					}
					users := []map[string]any{{"displayName": name, "accountId": "id-" + name, "active": true}}
					if ambiguous {
						users = append(users, map[string]any{"displayName": name + " Other", "accountId": "other", "active": true})
					}
					json.NewEncoder(w).Encode(users)
					return
				}
				var body struct {
					JQL string `json:"jql"`
				}
				json.NewDecoder(r.Body).Decode(&body)
				searches = append(searches, body.JQL)
				if strings.Contains(body.JQL, "statusCategory = Done") {
					fmt.Fprint(w, `{"issues":[]}`)
					return
				}
				fmt.Fprint(w, `{"issues":[{"key":"LAB-1","fields":{"summary":"Trabajo verificado","status":{"name":"En curso"}}}]}`)
			}))
			defer server.Close()
			c := &Connectors{Config: ConnectorConfig{Jira: JiraRESTConfig{AccessToken: "synthetic", CloudID: "cloud-1"}}, Client: server.Client(), allowTestHTTP: true, jiraAPIBase: server.URL}
			result, err := c.JiraQueryResult(context.Background(), "Hola Donna, me resumes como van Valentina y Gonzalo con sus tareas?")
			if ambiguous {
				if err == nil || len(searches) != 0 {
					t.Fatalf("ambiguous identity queried: %v %v", searches, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(searches) != 4 || !strings.Contains(searches[0], `assignee = "id-Valentina"`) || !strings.Contains(searches[0], "statusCategory != Done") || !strings.Contains(searches[1], "statusCategoryChangedDate >= -14d") || !strings.Contains(searches[2], `assignee = "id-Gonzalo"`) || !strings.Contains(searches[3], "statusCategoryChangedDate >= -14d") {
				t.Fatalf("wrong assignee queries: %v", searches)
			}
			if !result.DirectAnswer || !strings.Contains(result.Text, "Valentina") || !strings.Contains(result.Text, "Gonzalo") || !strings.Contains(result.Text, "en curso") {
				t.Fatalf("missing verified group summary: %+v", result)
			}
		})
	}
}

func TestJiraPeopleRecognition(t *testing.T) {
	for _, tt := range []struct {
		query string
		count int
	}{
		{"¿Cómo va Emilia con sus tareas?", 1},
		{"Tareas de Valentina Lagos y Gonzalo Sánchez", 2},
		{"¿En qué está trabajando hoy Valentina?", 1},
		{"Solo Jira: ¿qué tareas tiene Valentina en curso en el proyecto PMI?", 1},
		{"Mis tareas de hoy", 0},
		{"Tareas de mi", 0},
		{"Estado de LAB-12 y tareas de Valentina", 0},
		{"Busca el plan Atlas", 0},
	} {
		if got := jiraPeople(tt.query); len(got) != tt.count {
			t.Errorf("jiraPeople(%q)=%v", tt.query, got)
		}
	}
}

func TestJiraNamedPersonQueryHonorsProjectScope(t *testing.T) {
	var searches []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/user/search") {
			fmt.Fprint(w, `[{"displayName":"Valentina Lagos","accountId":"valentina","active":true}]`)
			return
		}
		var request struct {
			JQL string `json:"jql"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		searches = append(searches, request.JQL)
		fmt.Fprint(w, `{"issues":[]}`)
	}))
	defer server.Close()
	c := &Connectors{Config: ConnectorConfig{Jira: JiraRESTConfig{AccessToken: "synthetic", CloudID: "cloud-1"}}, Client: server.Client(), allowTestHTTP: true, jiraAPIBase: server.URL}
	_, err := c.JiraQueryResult(context.Background(), "Tareas de Valentina. Proyecto PMI.")
	if err != nil {
		t.Fatal(err)
	}
	if len(searches) != 2 {
		t.Fatalf("JQL searches = %v, want open and recently completed searches", searches)
	}
	for _, search := range searches {
		if !strings.Contains(search, `project = "PMI"`) || !strings.Contains(search, `assignee = "valentina"`) {
			t.Errorf("person query escaped its PMI scope: %q", search)
		}
	}
}

func TestJiraNamedPersonUsesApprovedIdentityMapping(t *testing.T) {
	var searches []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/user/search") {
			searches = append(searches, "user-search:"+r.URL.Query().Get("query"))
			fmt.Fprint(w, `[]`)
			return
		}
		var request struct {
			JQL string `json:"jql"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		searches = append(searches, request.JQL)
		fmt.Fprint(w, `{"issues":[{"key":"SW-87","fields":{"summary":"Catálogo único de literales","status":{"name":"En curso"}}}]}`)
	}))
	defer server.Close()
	c := &Connectors{Config: ConnectorConfig{Jira: JiraRESTConfig{AccessToken: "synthetic", CloudID: "cloud-1"}}, Client: server.Client(), allowTestHTTP: true, jiraAPIBase: server.URL}
	ctx := context.WithValue(context.Background(), jiraKnowledgeContextKey{}, []KnowledgeItem{{
		ID: "identity-gonzalo", OperatorID: 7, Type: KnowledgeEntityAlias, EntityType: EntityPerson,
		Subject: "Gonzalo Sanchez", Value: "account-123", Aliases: []string{"Gonzalo", "gonzalo.sanchez"},
		ExternalRefs: map[string]string{"jira": "account-123"}, Status: KnowledgeApproved,
	}})
	result, err := c.JiraQueryResult(ctx, "¿En qué está trabajando Gonzalo?")
	if err != nil {
		t.Fatal(err)
	}
	if len(searches) != 2 || !strings.Contains(searches[0], `assignee = "account-123"`) || !strings.Contains(searches[1], `assignee = "account-123"`) || strings.Contains(strings.Join(searches, " "), "user-search:") {
		t.Fatalf("approved identity was not used: %v", searches)
	}
	if !strings.Contains(result.Text, "SW-87") {
		t.Fatalf("mapped person task missing: %q", result.Text)
	}
}

func TestJiraCurrentWorkQueryUsesOnlyInProgressIssuesInProject(t *testing.T) {
	var searches []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/user/search") {
			fmt.Fprint(w, `[{"displayName":"Valentina Lagos","accountId":"valentina","active":true}]`)
			return
		}
		var request struct {
			JQL string `json:"jql"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		searches = append(searches, request.JQL)
		fmt.Fprint(w, `{"issues":[]}`)
	}))
	defer server.Close()
	c := &Connectors{Config: ConnectorConfig{Jira: JiraRESTConfig{AccessToken: "synthetic", CloudID: "cloud-1"}}, Client: server.Client(), allowTestHTTP: true, jiraAPIBase: server.URL}
	result, err := c.JiraQueryResult(context.Background(), "Cómo va Valentina con sus tareas en curso. Proyecto PMI.")
	if err != nil {
		t.Fatal(err)
	}
	if len(searches) != 1 || !strings.Contains(searches[0], `project = "PMI"`) || !strings.Contains(searches[0], `assignee = "valentina"`) || !strings.Contains(searches[0], `status = "In Progress"`) {
		t.Fatalf("JQL did not isolate current PMI work: %v", searches)
	}
	if !strings.Contains(result.Text, "PMI") || !strings.Contains(result.Text, "En curso") || !strings.Contains(result.Text, "no veo tareas") {
		t.Fatalf("empty current-work answer did not identify project and status: %q", result.Text)
	}
}

func TestJiraWaitingCategoryIsNotReportedAsCurrentWork(t *testing.T) {
	var issue jiraIssue
	if err := json.Unmarshal([]byte(`{"fields":{"status":{"name":"Waiting","statusCategory":{"key":"indeterminate"}}}}`), &issue); err != nil {
		t.Fatal(err)
	}
	if got := jiraWorkState(issue); got != "En espera" {
		t.Fatalf("jiraWorkState(Waiting) = %q, want En espera", got)
	}
}

func TestJiraNamedPeopleEmptyAndUnavailable(t *testing.T) {
	for _, status := range []int{200, 403} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "/user/search") {
					fmt.Fprint(w, `[{"displayName":"Emilia","accountId":"emilia","active":true}]`)
					return
				}
				w.WriteHeader(status)
				fmt.Fprint(w, `{"issues":[]}`)
			}))
			defer server.Close()
			c := &Connectors{Config: ConnectorConfig{Jira: JiraRESTConfig{AccessToken: "synthetic", CloudID: "cloud-1"}}, Client: server.Client(), allowTestHTTP: true, jiraAPIBase: server.URL}
			result, err := c.JiraQueryResult(context.Background(), "¿Cómo va Emilia con sus tareas?")
			if status != 200 {
				if err == nil {
					t.Fatal("HTTP failure reported as empty")
				}
				return
			}
			if err != nil || !result.DirectAnswer || result.ResultCount == 0 || !strings.Contains(result.Text, "no devolvió incidencias") {
				t.Fatalf("empty verified search lost: %+v %v", result, err)
			}
		})
	}
}

func TestJiraPeopleSummarizesVerifiedStateAndDeadlines(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/user/search") {
			fmt.Fprint(w, `[{"displayName":"Valentina","accountId":"valentina","active":true}]`)
			return
		}
		var request struct {
			JQL string `json:"jql"`
		}
		json.NewDecoder(r.Body).Decode(&request)
		if strings.Contains(request.JQL, "statusCategory = Done") {
			fmt.Fprint(w, `{"issues":[{"key":"LAB-5","fields":{"summary":"Trabajo terminado","status":{"name":"Hecho","statusCategory":{"key":"done"}}}}]}`)
			return
		}
		fmt.Fprint(w, `{"issues":[
			{"key":"LAB-1","fields":{"summary":"Esperando acceso","status":{"name":"Bloqueada","statusCategory":{"key":"indeterminate"}},"duedate":"2020-01-01"}},
			{"key":"LAB-2","fields":{"summary":"Definir alcance","status":{"name":"Sin planificar","statusCategory":{"key":"new"}}}},
			{"key":"LAB-3","fields":{"summary":"Preparar entrega","status":{"name":"En curso","statusCategory":{"key":"indeterminate"}},"duedate":"2020-01-02"}},
			{"key":"LAB-4","fields":{"summary":"Revisar propuesta","status":{"name":"Por hacer","statusCategory":{"key":"new"}}}}
		]}`)
	}))
	defer server.Close()
	c := &Connectors{Config: ConnectorConfig{Jira: JiraRESTConfig{AccessToken: "synthetic", CloudID: "cloud-1"}}, Client: server.Client(), allowTestHTTP: true, jiraAPIBase: server.URL}
	result, err := c.JiraQueryResult(context.Background(), "¿Cómo va Valentina con sus tareas?")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"1 bloqueada", "1 sin planificar", "1 en curso", "1 por hacer", "1 pasó a Done", "2 vencidas", "LAB-1", "LAB-3", "Esperando acceso"} {
		if !strings.Contains(result.Text, want) {
			t.Errorf("missing %q in %q", want, result.Text)
		}
	}
	if strings.Contains(result.Text, "LAB-5 ·") || strings.Contains(result.Text, "LAB-4 ·") {
		t.Fatalf("raw Jira listing leaked into summary: %q", result.Text)
	}
}

func TestJiraNewCategoryDoesNotMeanUnplanned(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/user/search") {
			fmt.Fprint(w, `[{"displayName":"Gonzalo","accountId":"gonzalo","active":true}]`)
			return
		}
		var request struct {
			JQL string `json:"jql"`
		}
		json.NewDecoder(r.Body).Decode(&request)
		if strings.Contains(request.JQL, "statusCategory = Done") {
			fmt.Fprint(w, `{"issues":[]}`)
			return
		}
		fmt.Fprint(w, `{"issues":[{"key":"LAB-8","fields":{"summary":"Plan de trabajo","status":{"name":"Por hacer","statusCategory":{"key":"new"}}}}]}`)
	}))
	defer server.Close()
	c := &Connectors{Config: ConnectorConfig{Jira: JiraRESTConfig{AccessToken: "synthetic", CloudID: "cloud-1"}}, Client: server.Client(), allowTestHTTP: true, jiraAPIBase: server.URL}
	result, err := c.JiraQueryResult(context.Background(), "Tareas de Gonzalo")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(result.Text, "1 sin planificar") || strings.Contains(result.Text, "1 bloqueada") {
		t.Fatalf("inferred unsupported workflow state: %q", result.Text)
	}
}

func TestJiraPeopleHighlightsUpcomingDeadlineWithoutInventingBlocker(t *testing.T) {
	due := time.Now().AddDate(0, 0, 1).Format("2006-01-02")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/user/search") {
			fmt.Fprint(w, `[{"displayName":"Gonzalo","accountId":"gonzalo","active":true}]`)
			return
		}
		var request struct {
			JQL string `json:"jql"`
		}
		json.NewDecoder(r.Body).Decode(&request)
		if strings.Contains(request.JQL, "statusCategory = Done") {
			fmt.Fprint(w, `{"issues":[]}`)
			return
		}
		fmt.Fprintf(w, `{"issues":[{"key":"LAB-9","fields":{"summary":"Entrega de mañana","status":{"name":"En curso","statusCategory":{"key":"indeterminate"}},"duedate":%q}}]}`, due)
	}))
	defer server.Close()
	c := &Connectors{Config: ConnectorConfig{Jira: JiraRESTConfig{AccessToken: "synthetic", CloudID: "cloud-1"}}, Client: server.Client(), allowTestHTTP: true, jiraAPIBase: server.URL}
	result, err := c.JiraQueryResult(context.Background(), "Tareas de Gonzalo")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"vence en los próximos dos días", "LAB-9", "Entrega de mañana"} {
		if !strings.Contains(result.Text, want) {
			t.Errorf("missing %q in %q", want, result.Text)
		}
	}
	if strings.Contains(result.Text, "1 bloqueada") {
		t.Fatalf("upcoming deadline became a blocker: %q", result.Text)
	}
}

func TestJiraPeopleGivesAUsefulReadingOfOpenWork(t *testing.T) {
	var searches []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/user/search") {
			fmt.Fprint(w, `[{"displayName":"gonzalo.sanchez","accountId":"gonzalo","active":true}]`)
			return
		}
		var request struct {
			JQL string `json:"jql"`
		}
		json.NewDecoder(r.Body).Decode(&request)
		searches = append(searches, request.JQL)
		if strings.Contains(request.JQL, "statusCategory = Done") {
			fmt.Fprint(w, `{"issues":[{"key":"LAB-40","fields":{"summary":"Entrega cerrada","status":{"name":"Hecho","statusCategory":{"key":"done"}}}}]}`)
			return
		}
		fmt.Fprint(w, `{"issues":[
			{"key":"LAB-11","fields":{"summary":"Integrar pagos con el banco","status":{"name":"En curso","statusCategory":{"key":"indeterminate"}},"updated":"2026-10-20T10:00:00.000+0200"}},
			{"key":"LAB-12","fields":{"summary":"Validar conciliación","status":{"name":"Por hacer","statusCategory":{"key":"new"}},"updated":"2026-10-19T10:00:00.000+0200"}},
			{"key":"LAB-13","fields":{"summary":"Preparar pruebas de regresión","status":{"name":"Por hacer","statusCategory":{"key":"new"}},"updated":"2026-10-18T10:00:00.000+0200"}}
		]}`)
	}))
	defer server.Close()
	c := &Connectors{Config: ConnectorConfig{Jira: JiraRESTConfig{AccessToken: "synthetic", CloudID: "cloud-1"}}, Client: server.Client(), allowTestHTTP: true, jiraAPIBase: server.URL}
	result, err := c.JiraQueryResult(context.Background(), "¿Cómo va Gonzalo con sus tareas?")
	if err != nil {
		t.Fatal(err)
	}
	if len(searches) != 2 || !strings.Contains(searches[0], "statusCategory != Done") || !strings.Contains(searches[1], "statusCategoryChangedDate >= -14d") {
		t.Fatalf("must separate current work from recent completions: %v", searches)
	}
	for _, want := range []string{"**Gonzalo**", "3 abiertas", "1 en curso", "2 por hacer", "1 pasó a Done", "cola por hacer", "LAB-11", "Integrar pagos con el banco", "no tienen fecha de entrega"} {
		if !strings.Contains(result.Text, want) {
			t.Errorf("missing %q in %q", want, result.Text)
		}
	}
	if strings.Contains(result.Text, "gonzalo.sanchez") || strings.Contains(result.Text, "100 tareas recientes") {
		t.Fatalf("database export leaked into answer: %q", result.Text)
	}
}

func TestJiraPeopleMarksIncompleteOpenWorkSample(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/user/search") {
			fmt.Fprint(w, `[{"displayName":"Emilia","accountId":"emilia","active":true}]`)
			return
		}
		var request struct {
			JQL string `json:"jql"`
		}
		json.NewDecoder(r.Body).Decode(&request)
		if strings.Contains(request.JQL, "statusCategory = Done") {
			fmt.Fprint(w, `{"issues":[]}`)
			return
		}
		fmt.Fprint(w, `{"issues":[{"key":"LAB-99","fields":{"summary":"Revisar la integración","status":{"name":"En curso","statusCategory":{"key":"indeterminate"}}}}],"nextPageToken":"more"}`)
	}))
	defer server.Close()
	c := &Connectors{Config: ConnectorConfig{Jira: JiraRESTConfig{AccessToken: "synthetic", CloudID: "cloud-1"}}, Client: server.Client(), allowTestHTTP: true, jiraAPIBase: server.URL}
	result, err := c.JiraQueryResult(context.Background(), "Tareas de Emilia")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.Text, "al menos 1 abierta") || !strings.Contains(result.Text, "Muestra incompleta") {
		t.Fatalf("truncated search claimed complete coverage: %q", result.Text)
	}
}
