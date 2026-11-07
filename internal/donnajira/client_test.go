package donnajira

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestJiraClientSearchIsProjectScopedAndEscapesJQLTerms(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/ex/jira/cloud-1/rest/api/3/search/jql" {
			t.Fatalf("request=%s %s", r.Method, r.URL.Path)
		}
		var body struct {
			JQL        string   `json:"jql"`
			Fields     []string `json:"fields"`
			MaxResults int      `json:"maxResults"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(body.JQL, `project = "PMI"`) || !strings.Contains(body.JQL, `summary ~`) || strings.Count(body.JQL, `\"`) < 3 {
			t.Errorf("unsafe or unscoped JQL: %s", body.JQL)
		}
		if body.MaxResults != 5 || len(body.Fields) == 0 {
			t.Errorf("search body=%+v", body)
		}
		io.WriteString(w, `{"issues":[{"key":"PMI-42","fields":{"summary":"Preparar informe","status":{"name":"Open"}}}]}`)
	}))
	defer server.Close()

	client := JiraClient{CloudID: "cloud-1", AccessToken: "secret", ProjectKeys: []string{"PMI"}, IssueTypes: []string{"Task"}, BaseURL: server.URL + "/ex/jira/cloud-1/rest/api/3", AllowTestHTTP: true, HTTPClient: server.Client()}
	issues, err := client.SearchTaskCandidates(context.Background(), "PMI", `informe" OR status = "Done`, 5)
	if err != nil || len(issues) != 1 || issues[0].IssueKey != "PMI-42" {
		t.Fatalf("issues=%+v err=%v", issues, err)
	}
}

func TestJiraClientCreateAndCommentUseOnlyTypedFields(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		switch r.URL.Path {
		case "/ex/jira/cloud-1/rest/api/3/issue":
			if r.Method != http.MethodPost || r.Header.Get("Authorization") != "Bearer write-secret" {
				t.Fatalf("create request=%s auth=%q", r.Method, r.Header.Get("Authorization"))
			}
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			fields := body["fields"].(map[string]any)
			for _, field := range []string{"project", "issuetype", "summary", "description"} {
				if _, ok := fields[field]; !ok {
					t.Errorf("create payload missing %q: %+v", field, fields)
				}
			}
			if _, ok := fields["arbitrary"]; ok {
				t.Errorf("arbitrary field leaked into payload: %+v", fields)
			}
			io.WriteString(w, `{"id":"12345","key":"PMI-43"}`)
		case "/ex/jira/cloud-1/rest/api/3/issue/PMI-42/comment":
			if r.Method != http.MethodPost {
				t.Fatalf("comment method=%s", r.Method)
			}
			var body struct {
				Body map[string]any `json:"body"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if body.Body["type"] != "doc" {
				t.Errorf("comment must use Atlassian document format: %+v", body)
			}
			io.WriteString(w, `{"id":"comment-9"}`)
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	client := JiraClient{CloudID: "cloud-1", AccessToken: "write-secret", ProjectKeys: []string{"PMI"}, IssueTypes: []string{"Task"}, BaseURL: server.URL + "/ex/jira/cloud-1/rest/api/3", AllowTestHTTP: true, HTTPClient: server.Client()}
	created, err := client.CreateIssue(context.Background(), TaskDraft{Kind: ActionCreate, ProjectKey: "PMI", IssueType: "Task", Summary: "Preparar informe", Description: "Contexto", AssigneeAccountID: "account-7", DueDate: "2026-11-07"})
	if err != nil || created.IssueKey != "PMI-43" {
		t.Fatalf("created=%+v err=%v", created, err)
	}
	commentID, err := client.AddComment(context.Background(), "PMI-42", "Contexto nuevo. Fuente: https://notes.example.test/note-123")
	if err != nil || commentID != "comment-9" || requests != 2 {
		t.Fatalf("commentID=%q requests=%d err=%v", commentID, requests, err)
	}
}

func TestJiraClientRejectsInvalidProjectAndIssueKeysBeforeRequest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("invalid identifiers must be rejected before a Jira request")
	}))
	defer server.Close()
	client := JiraClient{CloudID: "cloud-1", AccessToken: "write-secret", ProjectKeys: []string{"PMI"}, IssueTypes: []string{"Task"}, BaseURL: server.URL + "/ex/jira/cloud-1/rest/api/3", AllowTestHTTP: true, HTTPClient: server.Client()}

	if _, err := client.SearchTaskCandidates(context.Background(), `PMI" OR project = "OPS`, "informe", 5); err == nil {
		t.Fatal("invalid project key was accepted")
	}
	if _, err := client.SearchTaskCandidates(context.Background(), "OPS", "informe", 5); err == nil {
		t.Fatal("project outside the configured allowlist was accepted")
	}
	if _, err := client.CreateIssue(context.Background(), TaskDraft{Kind: ActionCreate, ProjectKey: "PMI", IssueType: "Bug", Summary: "issue type not configured"}); err == nil {
		t.Fatal("issue type outside the configured allowlist was accepted")
	}
	if _, err := client.GetIssue(context.Background(), "PMI-1/delete"); err == nil {
		t.Fatal("invalid issue key was accepted")
	}
}
