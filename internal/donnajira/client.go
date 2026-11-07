package donnajira

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

var (
	jiraProjectKeyPattern = regexp.MustCompile(`^[A-Z][A-Z0-9_]{1,9}$`)
	jiraIssueKeyPattern   = regexp.MustCompile(`^[A-Z][A-Z0-9_]{1,9}-[1-9][0-9]*$`)
)

type JiraClient struct {
	CloudID       string
	AccessToken   string
	ProjectKeys   []string
	IssueTypes    []string
	BaseURL       string
	AllowTestHTTP bool
	HTTPClient    *http.Client
}

func (c JiraClient) AllowedOptions() JiraOptions {
	return JiraOptions{ProjectKeys: append([]string(nil), c.ProjectKeys...), IssueTypes: append([]string(nil), c.IssueTypes...)}
}

func (c JiraClient) SearchTaskCandidates(ctx context.Context, projectKey, query string, limit int) ([]MatchCandidate, error) {
	if !jiraProjectKeyPattern.MatchString(projectKey) || !containsExact(c.ProjectKeys, projectKey) || limit < 1 || limit > 10 {
		return nil, ErrActionInvalid
	}
	query = strings.TrimSpace(query)
	if query == "" || len([]rune(query)) > 240 || strings.ContainsAny(query, "\r\n\x00") {
		return nil, ErrActionInvalid
	}
	escaped := strings.ReplaceAll(query, `\`, `\\`)
	escaped = strings.ReplaceAll(escaped, `"`, `\"`)
	jql := `project = "` + projectKey + `" AND summary ~ "\"` + escaped + `\"" ORDER BY updated DESC`
	body, err := json.Marshal(map[string]any{
		"jql":        jql,
		"maxResults": limit,
		"fields":     []string{"summary", "status"},
	})
	if err != nil {
		return nil, ErrActionInvalid
	}
	response, err := c.do(ctx, http.MethodPost, "/search/jql", body)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("Jira search failed with HTTP %d", response.StatusCode)
	}
	var result struct {
		Issues []struct {
			Key    string `json:"key"`
			Fields struct {
				Summary string `json:"summary"`
				Status  struct {
					Name string `json:"name"`
				} `json:"status"`
			} `json:"fields"`
		} `json:"issues"`
	}
	if err := decodeBoundedJSON(response.Body, &result); err != nil {
		return nil, err
	}
	if len(result.Issues) > limit {
		return nil, errors.New("Jira search returned too many candidates")
	}
	candidates := make([]MatchCandidate, 0, len(result.Issues))
	for _, issue := range result.Issues {
		if !jiraIssueKeyPattern.MatchString(issue.Key) || strings.TrimSpace(issue.Fields.Summary) == "" {
			return nil, errors.New("Jira search returned an invalid issue")
		}
		candidates = append(candidates, MatchCandidate{IssueKey: issue.Key, Summary: issue.Fields.Summary, Status: issue.Fields.Status.Name})
	}
	return candidates, nil
}

func (c JiraClient) CreateIssue(ctx context.Context, draft TaskDraft) (MatchCandidate, error) {
	if draft.Kind != ActionCreate || !jiraProjectKeyPattern.MatchString(draft.ProjectKey) || !containsExact(c.ProjectKeys, draft.ProjectKey) || !containsExact(c.IssueTypes, strings.TrimSpace(draft.IssueType)) || strings.TrimSpace(draft.Summary) == "" {
		return MatchCandidate{}, ErrActionInvalid
	}
	fields := map[string]any{
		"project":   map[string]string{"key": draft.ProjectKey},
		"issuetype": map[string]string{"name": strings.TrimSpace(draft.IssueType)},
		"summary":   strings.TrimSpace(draft.Summary),
	}
	if description := strings.TrimSpace(draft.Description); description != "" {
		fields["description"] = atlassianDocument(description)
	}
	if accountID := strings.TrimSpace(draft.AssigneeAccountID); accountID != "" {
		fields["assignee"] = map[string]string{"accountId": accountID}
	}
	if dueDate := strings.TrimSpace(draft.DueDate); dueDate != "" {
		if _, err := time.Parse("2006-01-02", dueDate); err != nil {
			return MatchCandidate{}, ErrActionInvalid
		}
		fields["duedate"] = dueDate
	}
	body, err := json.Marshal(map[string]any{"fields": fields})
	if err != nil {
		return MatchCandidate{}, ErrActionInvalid
	}
	response, err := c.do(ctx, http.MethodPost, "/issue", body)
	if err != nil {
		return MatchCandidate{}, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return MatchCandidate{}, fmt.Errorf("Jira create failed with HTTP %d", response.StatusCode)
	}
	var result struct {
		Key string `json:"key"`
	}
	if err := decodeBoundedJSON(response.Body, &result); err != nil {
		return MatchCandidate{}, err
	}
	if !jiraIssueKeyPattern.MatchString(result.Key) {
		return MatchCandidate{}, errors.New("Jira create returned an invalid issue key")
	}
	return MatchCandidate{IssueKey: result.Key, Summary: strings.TrimSpace(draft.Summary)}, nil
}

func containsExact(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func (c JiraClient) AddComment(ctx context.Context, issueKey, comment string) (string, error) {
	if !jiraIssueKeyPattern.MatchString(issueKey) || strings.TrimSpace(comment) == "" || len([]rune(comment)) > 4000 {
		return "", ErrActionInvalid
	}
	body, err := json.Marshal(map[string]any{"body": atlassianDocument(comment)})
	if err != nil {
		return "", ErrActionInvalid
	}
	response, err := c.do(ctx, http.MethodPost, "/issue/"+url.PathEscape(issueKey)+"/comment", body)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", fmt.Errorf("Jira comment failed with HTTP %d", response.StatusCode)
	}
	var result struct {
		ID string `json:"id"`
	}
	if err := decodeBoundedJSON(response.Body, &result); err != nil {
		return "", err
	}
	if strings.TrimSpace(result.ID) == "" {
		return "", errors.New("Jira comment response has no ID")
	}
	return result.ID, nil
}

func (c JiraClient) GetIssue(ctx context.Context, issueKey string) (MatchCandidate, error) {
	if !jiraIssueKeyPattern.MatchString(issueKey) {
		return MatchCandidate{}, ErrActionInvalid
	}
	response, err := c.do(ctx, http.MethodGet, "/issue/"+url.PathEscape(issueKey)+"?fields=summary,status", nil)
	if err != nil {
		return MatchCandidate{}, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return MatchCandidate{}, fmt.Errorf("Jira issue read failed with HTTP %d", response.StatusCode)
	}
	var result struct {
		Key    string `json:"key"`
		Fields struct {
			Summary string `json:"summary"`
			Status  struct {
				Name string `json:"name"`
			} `json:"status"`
		} `json:"fields"`
	}
	if err := decodeBoundedJSON(response.Body, &result); err != nil {
		return MatchCandidate{}, err
	}
	if !jiraIssueKeyPattern.MatchString(result.Key) || strings.TrimSpace(result.Fields.Summary) == "" {
		return MatchCandidate{}, errors.New("Jira issue response is incomplete")
	}
	return MatchCandidate{IssueKey: result.Key, Summary: result.Fields.Summary, Status: result.Fields.Status.Name}, nil
}

func (c JiraClient) GetComment(ctx context.Context, issueKey, commentID string) (string, error) {
	if !jiraIssueKeyPattern.MatchString(issueKey) || strings.TrimSpace(commentID) == "" || strings.ContainsAny(commentID, "/?#") {
		return "", ErrActionInvalid
	}
	response, err := c.do(ctx, http.MethodGet, "/issue/"+url.PathEscape(issueKey)+"/comment/"+url.PathEscape(commentID), nil)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", fmt.Errorf("Jira comment read failed with HTTP %d", response.StatusCode)
	}
	var result struct {
		ID   string `json:"id"`
		Body struct {
			Content []struct {
				Content []struct {
					Text string `json:"text"`
				} `json:"content"`
			} `json:"content"`
		} `json:"body"`
	}
	if err := decodeBoundedJSON(response.Body, &result); err != nil {
		return "", err
	}
	if result.ID != commentID {
		return "", errors.New("Jira comment response ID mismatch")
	}
	var parts []string
	for _, paragraph := range result.Body.Content {
		for _, part := range paragraph.Content {
			if strings.TrimSpace(part.Text) != "" {
				parts = append(parts, part.Text)
			}
		}
	}
	return strings.Join(parts, ""), nil
}

func (c JiraClient) do(ctx context.Context, method, path string, body []byte) (*http.Response, error) {
	if strings.TrimSpace(c.CloudID) == "" || strings.TrimSpace(c.AccessToken) == "" || strings.HasPrefix(path, "//") || strings.Contains(path, "..") {
		return nil, ErrActionInvalid
	}
	base, err := c.apiBase()
	if err != nil {
		return nil, err
	}
	endpoint := strings.TrimRight(base, "/") + path
	request, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, ErrActionInvalid
	}
	request.Header.Set("Authorization", "Bearer "+c.AccessToken)
	request.Header.Set("Accept", "application/json")
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	client := c.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}
	clientCopy := *client
	clientCopy.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response, err := clientCopy.Do(request)
	if err != nil {
		return nil, errors.New("Jira transport unavailable")
	}
	return response, nil
}

func (c JiraClient) apiBase() (string, error) {
	if override := strings.TrimSpace(c.BaseURL); override != "" {
		parsed, err := url.Parse(override)
		if err != nil || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
			return "", ErrActionInvalid
		}
		if !c.AllowTestHTTP || (parsed.Scheme != "http" && parsed.Scheme != "https") ||
			(parsed.Scheme == "http" && parsed.Hostname() != "127.0.0.1" && parsed.Hostname() != "localhost") {
			return "", ErrActionInvalid
		}
		return strings.TrimRight(override, "/"), nil
	}
	if c.AllowTestHTTP {
		return "", ErrActionInvalid
	}
	return "https://api.atlassian.com/ex/jira/" + url.PathEscape(c.CloudID) + "/rest/api/3", nil
}

func atlassianDocument(text string) map[string]any {
	paragraphs := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	content := make([]map[string]any, 0, len(paragraphs))
	for _, paragraph := range paragraphs {
		content = append(content, map[string]any{
			"type":    "paragraph",
			"content": []map[string]string{{"type": "text", "text": paragraph}},
		})
	}
	return map[string]any{"type": "doc", "version": 1, "content": content}
}

func decodeBoundedJSON(reader io.Reader, target any) error {
	payload, err := io.ReadAll(io.LimitReader(reader, 1<<20))
	if err != nil || len(payload) == 1<<20 {
		return errors.New("invalid Jira REST response")
	}
	if err := json.Unmarshal(payload, target); err != nil {
		return errors.New("invalid Jira REST response")
	}
	return nil
}
