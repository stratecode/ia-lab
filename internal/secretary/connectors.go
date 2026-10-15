package secretary

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

type OAuthConfig struct {
	ServerURL     string `json:"server_url"`
	AccessToken   string `json:"access_token"`
	RefreshToken  string `json:"refresh_token,omitempty"`
	ClientID      string `json:"client_id,omitempty"`
	ClientSecret  string `json:"client_secret,omitempty"`
	TokenEndpoint string `json:"token_endpoint,omitempty"`
	ExpiresAt     int64  `json:"expires_at,omitempty"`
	CloudID       string `json:"cloud_id,omitempty"`
}
type ConnectorConfig struct {
	Jira    OAuthConfig `json:"jira"`
	Granola OAuthConfig `json:"granola"`
}
type Connectors struct {
	Config        ConnectorConfig
	Client        *http.Client
	StateDir      string
	mu            sync.Mutex
	allowTestHTTP bool
}

func NewConnectors(config ConnectorConfig, stateDir string) *Connectors {
	c := &Connectors{Config: config, StateDir: stateDir, Client: &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error { return errors.New("connector redirects disabled") }}}
	var cached ConnectorConfig
	if raw, err := os.ReadFile(filepath.Join(stateDir, "oauth-cache.json")); err == nil && json.Unmarshal(raw, &cached) == nil {
		for _, pair := range [][2]*OAuthConfig{{&c.Config.Jira, &cached.Jira}, {&c.Config.Granola, &cached.Granola}} {
			if pair[0].ServerURL == pair[1].ServerURL && pair[0].ClientID == pair[1].ClientID && pair[1].ExpiresAt > pair[0].ExpiresAt {
				*pair[0] = *pair[1]
			}
		}
	}
	return c
}
func (c *Connectors) Status() map[string]bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return map[string]bool{"Jira": c.Config.Jira.AccessToken != "", "Rovo": c.Config.Jira.AccessToken != "", "Granola": c.Config.Granola.AccessToken != ""}
}
func (c *Connectors) validServer(raw string) bool {
	if raw == "https://mcp.atlassian.com/v2/mcp" || raw == "https://mcp.granola.ai/mcp" {
		return true
	}
	if c.allowTestHTTP {
		u, err := url.Parse(raw)
		return err == nil && u.Scheme == "http" && u.Hostname() == "127.0.0.1"
	}
	return false
}
func (c *Connectors) access(ctx context.Context, config *OAuthConfig) error {
	if !c.validServer(config.ServerURL) {
		return errors.New("connector destination rejected")
	}
	if config.ExpiresAt == 0 || config.ExpiresAt > time.Now().Unix()+60 {
		return nil
	}
	endpoint, err := url.Parse(config.TokenEndpoint)
	expected := "auth.atlassian.com"
	if config.ServerURL == "https://mcp.granola.ai/mcp" {
		expected = "mcp-auth.granola.ai"
	}
	if err != nil || endpoint.Scheme != "https" || endpoint.Hostname() != expected || endpoint.User != nil || config.RefreshToken == "" {
		return errors.New("connector reauthentication required")
	}
	values := url.Values{"grant_type": {"refresh_token"}, "refresh_token": {config.RefreshToken}, "client_id": {config.ClientID}, "resource": {config.ServerURL}}
	if config.ClientSecret != "" {
		values.Set("client_secret", config.ClientSecret)
	}
	request, _ := http.NewRequestWithContext(ctx, "POST", config.TokenEndpoint, strings.NewReader(values.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := c.Client.Do(request)
	if err != nil {
		return errors.New("OAuth refresh unavailable")
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return fmt.Errorf("OAuth refresh HTTP %d", response.StatusCode)
	}
	var token struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int64  `json:"expires_in"`
	}
	if json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&token) != nil || token.AccessToken == "" {
		return errors.New("invalid OAuth refresh")
	}
	config.AccessToken = token.AccessToken
	if token.RefreshToken != "" {
		config.RefreshToken = token.RefreshToken
	}
	config.ExpiresAt = time.Now().Unix() + token.ExpiresIn
	return atomicJSON(filepath.Join(c.StateDir, "oauth-cache.json"), c.Config)
}

type mcpSession struct {
	owner            *Connectors
	config           OAuthConfig
	session, version string
	id               int
}

func (s *mcpSession) rpc(ctx context.Context, method string, params any, notification bool) (json.RawMessage, error) {
	payload := map[string]any{"jsonrpc": "2.0", "method": method, "params": params}
	if !notification {
		s.id++
		payload["id"] = s.id
	}
	raw, _ := json.Marshal(payload)
	request, _ := http.NewRequestWithContext(ctx, "POST", s.config.ServerURL, bytes.NewReader(raw))
	request.Header.Set("Authorization", "Bearer "+s.config.AccessToken)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json, text/event-stream")
	if s.session != "" {
		request.Header.Set("Mcp-Session-Id", s.session)
	}
	if s.version != "" {
		request.Header.Set("MCP-Protocol-Version", s.version)
	}
	response, err := s.owner.Client.Do(request)
	if err != nil {
		return nil, errors.New("MCP transport unavailable")
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("MCP HTTP %d", response.StatusCode)
	}
	if id := response.Header.Get("Mcp-Session-Id"); id != "" {
		s.session = id
	}
	if notification {
		return nil, nil
	}
	var envelope struct {
		Result json.RawMessage `json:"result"`
		Error  json.RawMessage `json:"error"`
	}
	if strings.HasPrefix(response.Header.Get("Content-Type"), "text/event-stream") {
		scan := bufio.NewScanner(io.LimitReader(response.Body, 2<<20))
		scan.Buffer(make([]byte, 4096), 2<<20)
		for scan.Scan() {
			line := scan.Bytes()
			if bytes.HasPrefix(line, []byte("data:")) && json.Unmarshal(bytes.TrimSpace(line[5:]), &envelope) == nil && len(envelope.Result) > 0 {
				break
			}
		}
	} else if json.NewDecoder(io.LimitReader(response.Body, 2<<20)).Decode(&envelope) != nil {
		return nil, errors.New("invalid MCP response")
	}
	if (len(envelope.Error) > 0 && string(envelope.Error) != "null") || len(envelope.Result) == 0 {
		return nil, errors.New("MCP operation rejected")
	}
	return envelope.Result, nil
}
func (c *Connectors) session(ctx context.Context, config OAuthConfig) (*mcpSession, error) {
	if !c.validServer(config.ServerURL) || config.AccessToken == "" {
		return nil, errors.New("connector not authorised")
	}
	s := &mcpSession{owner: c, config: config}
	raw, err := s.rpc(ctx, "initialize", map[string]any{"protocolVersion": "2025-03-26", "capabilities": map[string]any{}, "clientInfo": map[string]string{"name": "stratecode-secretary", "version": "0.1.0"}}, false)
	if err != nil {
		return nil, err
	}
	var init struct {
		Version string `json:"protocolVersion"`
	}
	json.Unmarshal(raw, &init)
	s.version = init.Version
	_, err = s.rpc(ctx, "notifications/initialized", map[string]any{}, true)
	return s, err
}

var readTools = map[string]bool{"getAccessibleAtlassianResources": true, "searchJiraIssuesUsingJql": true, "getJiraIssue": true, "search": true, "list_meetings": true, "get_meetings": true, "query_granola_meetings": true}
var rovoScopeMention = regexp.MustCompile(`\b(?:read|search|write|delete|manage):[a-z-]+:agent-interface\b`)

func (c *Connectors) tool(ctx context.Context, config OAuthConfig, name string, args map[string]any) (json.RawMessage, error) {
	if !readTools[name] {
		return nil, errors.New("tool outside read-only allowlist")
	}
	s, err := c.session(ctx, config)
	if err != nil {
		return nil, err
	}
	return s.callTool(ctx, name, args)
}
func (s *mcpSession) callTool(ctx context.Context, name string, args map[string]any) (json.RawMessage, error) {
	if !readTools[name] {
		return nil, errors.New("tool outside read-only allowlist")
	}
	raw, err := s.rpc(ctx, "tools/call", map[string]any{"name": name, "arguments": args}, false)
	if err != nil {
		return nil, err
	}
	var result struct {
		IsError    bool            `json:"isError"`
		Structured json.RawMessage `json:"structuredContent"`
		Content    []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if json.Unmarshal(raw, &result) != nil {
		return nil, errors.New("connector tool failed")
	}
	if result.IsError {
		var missing []string
		seen := map[string]bool{}
		for _, item := range result.Content {
			for _, scope := range rovoScopeMention.FindAllString(item.Text, -1) {
				if !seen[scope] {
					missing = append(missing, scope)
					seen[scope] = true
				}
			}
		}
		if len(missing) > 0 {
			return nil, fmt.Errorf("Rovo missing required scopes: %s", strings.Join(missing, ", "))
		}
		return nil, errors.New("connector tool failed")
	}
	if len(result.Structured) > 0 && string(result.Structured) != "null" {
		return result.Structured, nil
	}
	for _, item := range result.Content {
		if item.Type == "text" && json.Valid([]byte(item.Text)) {
			return json.RawMessage(item.Text), nil
		}
		if item.Type == "text" {
			return json.Marshal(item.Text)
		}
	}
	return nil, errors.New("structured source data required")
}
func (c *Connectors) Jira(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.Config.Jira.AccessToken == "" {
		return "Jira: no conectado", nil
	}
	if c.Config.Jira.ServerURL != "https://mcp.atlassian.com/v2/mcp" && !c.allowTestHTTP {
		return "", errors.New("Jira credential destination rejected")
	}
	if err := c.access(ctx, &c.Config.Jira); err != nil {
		return "", err
	}
	s, err := c.session(ctx, c.Config.Jira)
	if err != nil {
		return "", err
	}
	cloud := c.Config.Jira.CloudID
	if cloud == "" {
		raw, err := s.callTool(ctx, "getAccessibleAtlassianResources", map[string]any{})
		if err != nil {
			return "", err
		}
		var sites []struct {
			ID string `json:"id"`
		}
		if json.Unmarshal(raw, &sites) != nil || len(sites) != 1 {
			return "", errors.New("select a Jira cloud_id before querying")
		}
		cloud = sites[0].ID
	}
	raw, err := s.callTool(ctx, "searchJiraIssuesUsingJql", map[string]any{"cloudId": cloud, "jql": "assignee = currentUser() AND statusCategory != Done ORDER BY updated DESC", "maxResults": 5, "fields": []string{"summary", "status", "duedate"}})
	if err != nil {
		return "", err
	}
	var data struct {
		Issues []struct {
			Key    string `json:"key"`
			Fields struct {
				Summary string `json:"summary"`
				Status  struct {
					Name string `json:"name"`
				} `json:"status"`
				Due string `json:"duedate"`
			} `json:"fields"`
		} `json:"issues"`
		IsLast *bool `json:"isLast"`
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || len(fields["issues"]) == 0 || string(fields["issues"]) == "null" || json.Unmarshal(raw, &data) != nil {
		return "", errors.New("unexpected Jira source schema")
	}
	lines := []string{"Jira · hasta 5 pendientes asignados a tu cuenta (muestra, no listado completo):"}
	if len(data.Issues) == 0 {
		lines = append(lines, "La consulta no devolvió incidencias.")
	}
	for i, item := range data.Issues {
		if i == 5 {
			break
		}
		if item.Key == "" {
			return "", errors.New("Jira source ID missing")
		}
		due := item.Fields.Due
		if due == "" {
			due = "sin fecha"
		}
		lines = append(lines, fmt.Sprintf("[%s] %s · %s · %s", short(item.Key), short(item.Fields.Summary), short(item.Fields.Status.Name), short(due)))
	}
	return strings.Join(lines, "\n"), nil
}

// Rovo searches Jira and Confluence together so the shared router can answer
// natural-language questions about plans, tasks, and pending activities.
func (c *Connectors) Rovo(ctx context.Context, query string) (string, error) {
	result, err := c.RovoResult(ctx, query)
	return result.Text, err
}

func (c *Connectors) RovoResult(ctx context.Context, query string) (CapabilityResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.Config.Jira.AccessToken == "" {
		return CapabilityResult{}, errors.New("Rovo no conectado")
	}
	if c.Config.Jira.ServerURL != "https://mcp.atlassian.com/v2/mcp" && !c.allowTestHTTP {
		return CapabilityResult{}, errors.New("Rovo credential destination rejected")
	}
	query = strings.TrimSpace(query)
	if query == "" || len([]rune(query)) > 2000 {
		return CapabilityResult{}, errors.New("Rovo query length rejected")
	}
	if err := c.access(ctx, &c.Config.Jira); err != nil {
		return CapabilityResult{}, err
	}
	s, err := c.session(ctx, c.Config.Jira)
	if err != nil {
		return CapabilityResult{}, err
	}
	cloud := c.Config.Jira.CloudID
	if cloud == "" {
		cloud, err = accessibleCloudID(ctx, s)
		if err != nil {
			return CapabilityResult{}, err
		}
	}
	if issueKey := jiraIssueKeyMention.FindString(strings.ToUpper(query)); issueKey != "" {
		issueRaw, err := s.callTool(ctx, "getJiraIssue", map[string]any{
			"cloudId": cloud, "issueIdOrKey": issueKey,
			"fields": []string{"summary", "status", "assignee", "duedate", "updated", "project"},
			"updateHistory": false,
		})
		if err != nil {
			return CapabilityResult{}, err
		}
		line, issueEvidence, ok := parseJiraIssueEvidence(issueRaw, EvidenceRef{ID: issueKey, Provider: "Jira"})
		if !ok {
			return CapabilityResult{}, errors.New("Jira issue fields could not be verified")
		}
		return CapabilityResult{
			Text: "Jira · incidencia verificada en la consulta actual:\n" + line,
			ResultCount: 1, Evidence: []EvidenceRef{issueEvidence}, DirectAnswer: true,
		}, nil
	}
	raw, err := s.callTool(ctx, "search", map[string]any{
		"cloudId": cloud, "query": query, "targetApp": "JIRA", "mode": "STANDARD", "limit": 10,
	})
	if err != nil {
		return CapabilityResult{}, err
	}
	if rovoHasNoResults(raw) {
		return CapabilityResult{}, nil
	}
	evidence := parseRovoEvidence(raw)
	verified := make([]string, 0, 5)
	verifiedEvidence := make([]EvidenceRef, 0, 5)
	for _, candidate := range evidence {
		if !jiraIssueKey.MatchString(candidate.ID) || len(verified) == 5 {
			continue
		}
		issueRaw, err := s.callTool(ctx, "getJiraIssue", map[string]any{
			"cloudId": cloud, "issueIdOrKey": candidate.ID,
			"fields": []string{"summary", "status", "assignee", "duedate", "updated", "project"},
			"updateHistory": false,
		})
		if err != nil {
			continue
		}
		line, issueEvidence, ok := parseJiraIssueEvidence(issueRaw, candidate)
		if !ok {
			continue
		}
		verified = append(verified, line)
		verifiedEvidence = append(verifiedEvidence, issueEvidence)
	}
	if len(verified) > 0 {
		return CapabilityResult{
			Text:        "Jira · incidencias verificadas en la consulta actual:\n" + strings.Join(verified, "\n"),
			ResultCount: len(verified), Evidence: verifiedEvidence, DirectAnswer: true,
		}, nil
	}
	text := strings.TrimSpace(string(raw))
	if len(text) > 12000 {
		text = string([]rune(text)[:12000])
	}
	if text == "" || text == `""` {
		return CapabilityResult{}, nil
	}
	count := len(evidence)
	if count == 0 {
		count = 1
	}
	return CapabilityResult{Text: "Rovo · resultados de búsqueda en Jira (sin verificación de campos):\n" + text, ResultCount: count, Evidence: evidence}, nil
}

var jiraIssueKey = regexp.MustCompile(`^[A-Z][A-Z0-9]*-[0-9]+$`)
var jiraIssueKeyMention = regexp.MustCompile(`\b[A-Z][A-Z0-9]*-[0-9]+\b`)

func parseJiraIssueEvidence(raw json.RawMessage, candidate EvidenceRef) (string, EvidenceRef, bool) {
	var root any
	if json.Unmarshal(raw, &root) != nil {
		return "", EvidenceRef{}, false
	}
	var issue map[string]any
	var find func(any)
	find = func(value any) {
		if issue != nil {
			return
		}
		switch node := value.(type) {
		case map[string]any:
			if key, _ := node["key"].(string); key == candidate.ID {
				if _, ok := node["fields"].(map[string]any); ok {
					issue = node
					return
				}
			}
			for _, child := range node {
				find(child)
			}
		case []any:
			for _, child := range node {
				find(child)
			}
		}
	}
	find(root)
	if issue == nil {
		return "", EvidenceRef{}, false
	}
	fields, _ := issue["fields"].(map[string]any)
	readName := func(value any, field string) string {
		row, _ := value.(map[string]any)
		name, _ := row[field].(string)
		return strings.TrimSpace(name)
	}
	summary, _ := fields["summary"].(string)
	status := readName(fields["status"], "name")
	assignee := readName(fields["assignee"], "displayName")
	if assignee == "" {
		assignee = readName(fields["assignee"], "name")
	}
	due, _ := fields["duedate"].(string)
	updated, _ := fields["updated"].(string)
	if strings.TrimSpace(summary) == "" || status == "" {
		return "", EvidenceRef{}, false
	}
	if assignee == "" {
		assignee = "sin asignar"
	}
	if strings.TrimSpace(due) == "" {
		due = "sin vencimiento"
	}
	issueLabel := candidate.ID
	if validEvidenceURL(candidate.URL) {
		issueLabel = fmt.Sprintf("[%s](%s)", candidate.ID, candidate.URL)
	}
	line := fmt.Sprintf("%s %s · Estado: %s · Responsable: %s · Vencimiento: %s", issueLabel, short(summary), short(status), short(assignee), short(due))
	if updated != "" {
		line += " · Actualizada: " + short(updated)
	}
	candidate.Provider = "Jira"
	candidate.Title = summary
	candidate.RetrievedAt = time.Now()
	return line, candidate, true
}

func parseRovoEvidence(raw json.RawMessage) []EvidenceRef {
	var root any
	if json.Unmarshal(raw, &root) != nil {
		return nil
	}
	var evidence []EvidenceRef
	seen := map[string]bool{}
	addRecord := func(node map[string]any) {
		ref := EvidenceRef{Provider: "Rovo", RetrievedAt: time.Now()}
		for key, child := range node {
			text, ok := child.(string)
			if !ok {
				continue
			}
			switch strings.ToLower(key) {
			case "key", "issuekey":
				if jiraIssueKey.MatchString(strings.TrimSpace(text)) {
					ref.ID = strings.TrimSpace(text)
				}
			case "id", "pageid", "contentid":
				if ref.ID == "" {
					ref.ID = strings.TrimSpace(text)
				}
			case "title", "summary", "name":
				if ref.Title == "" {
					ref.Title = strings.TrimSpace(text)
				}
			case "url", "weburl", "link":
				if validEvidenceURL(text) {
					ref.URL = text
				}
			}
		}
		if ref.ID == "" && ref.URL == "" {
			return
		}
		identity := ref.ID + "|" + ref.URL
		if !seen[identity] {
			seen[identity] = true
			evidence = append(evidence, ref)
		}
	}
	var findResults func(any)
	findResults = func(value any) {
		switch node := value.(type) {
		case map[string]any:
			for key, child := range node {
				lower := strings.ToLower(key)
				if lower == "results" || lower == "items" || lower == "issues" || lower == "matches" {
					if rows, ok := child.([]any); ok {
						for _, row := range rows {
							if record, ok := row.(map[string]any); ok {
								addRecord(record)
							}
						}
					}
					continue
				}
				if lower == "text" {
					if encoded, ok := child.(string); ok {
						var nested any
						if json.Unmarshal([]byte(encoded), &nested) == nil {
							findResults(nested)
						}
					}
					continue
				}
				findResults(child)
			}
		case []any:
			for _, child := range node {
				findResults(child)
			}
		}
	}
	findResults(root)
	return evidence
}

func validEvidenceURL(raw string) bool {
	u, err := url.Parse(strings.TrimSpace(raw))
	return err == nil && u.Scheme == "https" && u.Host != "" && u.User == nil
}

func accessibleCloudID(ctx context.Context, s *mcpSession) (string, error) {
	raw, err := s.callTool(ctx, "getAccessibleAtlassianResources", map[string]any{})
	if err != nil {
		return "", err
	}
	var response struct {
		ID      string `json:"id"`
		CloudID string `json:"cloudId"`
		Data    struct {
			Resources []struct {
				ID      string `json:"id"`
				CloudID string `json:"cloudId"`
			} `json:"resources"`
		} `json:"data"`
		Resources []struct {
			ID      string `json:"id"`
			CloudID string `json:"cloudId"`
		} `json:"resources"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		return "", errors.New("unexpected Atlassian sites response")
	}
	resources := response.Resources
	if len(resources) == 0 {
		resources = response.Data.Resources
	}
	if len(resources) == 1 {
		if resources[0].CloudID != "" {
			return resources[0].CloudID, nil
		}
		if resources[0].ID != "" {
			return resources[0].ID, nil
		}
	}
	if len(resources) == 0 && (response.CloudID != "" || response.ID != "") {
		if response.CloudID != "" {
			return response.CloudID, nil
		}
		return response.ID, nil
	}
	return "", errors.New("select a single Atlassian cloud_id before querying")
}

func rovoHasNoResults(raw json.RawMessage) bool {
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return false
	}
	var inspect func(any) (bool, bool)
	inspect = func(node any) (bool, bool) {
		switch typed := node.(type) {
		case map[string]any:
			found := false
			for key, child := range typed {
				if key == "results" || key == "items" || key == "issues" || key == "matches" {
					if rows, ok := child.([]any); ok {
						found = true
						if len(rows) > 0 {
							return false, true
						}
						continue
					}
				}
				if empty, nestedFound := inspect(child); nestedFound {
					if !empty {
						return false, true
					}
					found = true
				}
			}
			return found, found
		case []any:
			for _, child := range typed {
				if empty, found := inspect(child); found {
					if !empty {
						return false, true
					}
					return true, true
				}
			}
		}
		return false, false
	}
	empty, found := inspect(value)
	return found && empty
}
func (c *Connectors) Granola(ctx context.Context) (string, error) {
	result, err := c.GranolaResult(ctx)
	return result.Text, err
}

var granolaCitation = regexp.MustCompile(`\[\[(\d+)\]\]\((https://notes\.granola\.ai/d/[^)]+)\)`)

func (c *Connectors) GranolaQueryResult(ctx context.Context, query string) (CapabilityResult, error) {
	query = strings.TrimSpace(query)
	if query == "" || len([]rune(query)) > 2000 {
		return CapabilityResult{}, errors.New("Granola query length rejected")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.Config.Granola.AccessToken == "" {
		return CapabilityResult{}, errors.New("Granola no conectado")
	}
	if c.Config.Granola.ServerURL != "https://mcp.granola.ai/mcp" && !c.allowTestHTTP {
		return CapabilityResult{}, errors.New("Granola credential destination rejected")
	}
	if err := c.access(ctx, &c.Config.Granola); err != nil {
		return CapabilityResult{}, err
	}
	s, err := c.session(ctx, c.Config.Granola)
	if err != nil {
		return CapabilityResult{}, err
	}
	raw, err := s.rpc(ctx, "tools/list", map[string]any{}, false)
	if err != nil {
		return CapabilityResult{}, err
	}
	var tools struct {
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
	}
	if json.Unmarshal(raw, &tools) != nil {
		return CapabilityResult{}, errors.New("unexpected Granola tools schema")
	}
	queryAvailable := false
	for _, tool := range tools.Tools {
		if tool.Name == "query_granola_meetings" {
			queryAvailable = true
			break
		}
	}
	if !queryAvailable {
		return CapabilityResult{}, errors.New("Granola query tool unavailable")
	}
	raw, err = s.callTool(ctx, "query_granola_meetings", map[string]any{"query": query})
	if err != nil {
		return CapabilityResult{}, err
	}
	var text string
	if json.Unmarshal(raw, &text) != nil {
		return CapabilityResult{}, errors.New("unexpected Granola query response")
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return CapabilityResult{}, nil
	}
	evidence := make([]EvidenceRef, 0, 3)
	seen := map[string]bool{}
	for _, match := range granolaCitation.FindAllStringSubmatch(text, 10) {
		if len(match) != 3 || !validEvidenceURL(match[2]) || seen[match[2]] {
			continue
		}
		seen[match[2]] = true
		id := strings.TrimPrefix(match[2], "https://notes.granola.ai/d/")
		evidence = append(evidence, EvidenceRef{Provider: "Granola", ID: id, URL: match[2], RetrievedAt: time.Now()})
	}
	return CapabilityResult{Text: text, ResultCount: len(evidence), Evidence: evidence, DirectAnswer: true}, nil
}

func (c *Connectors) GranolaResult(ctx context.Context) (CapabilityResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.Config.Granola.AccessToken == "" {
		return CapabilityResult{}, errors.New("Granola no conectado")
	}
	if c.Config.Granola.ServerURL != "https://mcp.granola.ai/mcp" && !c.allowTestHTTP {
		return CapabilityResult{}, errors.New("Granola credential destination rejected")
	}
	if err := c.access(ctx, &c.Config.Granola); err != nil {
		return CapabilityResult{}, err
	}
	s, err := c.session(ctx, c.Config.Granola)
	if err != nil {
		return CapabilityResult{}, err
	}
	// Verify the actual remote schema before choosing arguments; do not guess.
	raw, err := s.rpc(ctx, "tools/list", map[string]any{}, false)
	if err != nil {
		return CapabilityResult{}, err
	}
	var tools struct {
		Tools []struct {
			Name   string `json:"name"`
			Schema struct {
				Required []string `json:"required"`
			} `json:"inputSchema"`
		} `json:"tools"`
	}
	json.Unmarshal(raw, &tools)
	found := map[string]bool{}
	for _, tool := range tools.Tools {
		if tool.Name == "list_meetings" || tool.Name == "get_meetings" {
			found[tool.Name] = true
			if len(tool.Schema.Required) > 0 {
				return CapabilityResult{}, fmt.Errorf("Granola %s schema needs explicit arguments", tool.Name)
			}
		}
	}
	if !found["list_meetings"] || !found["get_meetings"] {
		return CapabilityResult{}, errors.New("Granola meeting summary tools unavailable")
	}
	raw, err = s.callTool(ctx, "list_meetings", map[string]any{"time_range": "last_7_days"})
	if err != nil {
		return CapabilityResult{}, err
	}
	meetings, valid := parseGranolaMeetings(raw)
	if !valid {
		return CapabilityResult{}, errors.New("unexpected Granola source schema")
	}
	if len(meetings) == 0 {
		return CapabilityResult{}, nil
	}
	ids := make([]string, 0, 5)
	for i, item := range meetings {
		if i == 5 {
			break
		}
		if item.ID == "" {
			return CapabilityResult{}, errors.New("Granola source ID missing")
		}
		ids = append(ids, item.ID)
	}
	raw, err = s.callTool(ctx, "get_meetings", map[string]any{"meeting_ids": ids})
	if err != nil {
		return CapabilityResult{}, err
	}
	summaries, valid := parseGranolaSummaries(raw)
	if !valid {
		return CapabilityResult{}, errors.New("unexpected Granola meeting detail schema")
	}
	lines := []string{"Notas de Granola (últimos 7 días; muestra de hasta 5 reuniones):"}
	evidence := make([]EvidenceRef, 0, len(ids))
	for _, item := range meetings {
		if len(ids) == 0 {
			break
		}
		if !containsString(ids, item.ID) {
			continue
		}
		summary := summaries[item.ID]
		if summary == "" {
			summary = "Sin resumen disponible en la nota."
		}
		lines = append(lines, fmt.Sprintf("%s · %s\n%s", short(item.Title), short(item.Date), summary))
		evidence = append(evidence, EvidenceRef{Provider: "Granola", ID: item.ID, Title: item.Title, URL: item.URL, RetrievedAt: time.Now()})
	}
	return CapabilityResult{Text: strings.Join(lines, "\n\n"), ResultCount: len(evidence), Evidence: evidence}, nil
}

type granolaMeeting struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Date  string `json:"date"`
	URL   string `json:"url"`
}

var (
	granolaMeetingsTag  = regexp.MustCompile(`<meetings_data\b([^>]*)>`)
	granolaMeetingTag   = regexp.MustCompile(`<meeting\b([^>]*)>`)
	granolaMeetingBlock = regexp.MustCompile(`(?s)<meeting\b([^>]*)>(.*?)</meeting>`)
	granolaSummaryTag   = regexp.MustCompile(`(?s)<summary\b[^>]*>(.*?)</summary>`)
	granolaMarkupTag    = regexp.MustCompile(`<[^>]*>`)
	granolaAttribute    = regexp.MustCompile(`([A-Za-z_][A-Za-z0-9_-]*)="([^"]*)"`)
)

func parseGranolaSummaries(raw json.RawMessage) (map[string]string, bool) {
	var text string
	if json.Unmarshal(raw, &text) != nil {
		return nil, false
	}
	matches := granolaMeetingBlock.FindAllStringSubmatch(text, 6)
	if len(matches) == 0 {
		return nil, false
	}
	summaries := make(map[string]string, len(matches))
	for _, match := range matches {
		attrs := map[string]string{}
		for _, pair := range granolaAttribute.FindAllStringSubmatch(match[1], -1) {
			attrs[pair[1]] = html.UnescapeString(pair[2])
		}
		if attrs["id"] == "" {
			continue
		}
		summary := granolaSummaryTag.FindStringSubmatch(match[2])
		if len(summary) != 2 {
			continue
		}
		clean := html.UnescapeString(granolaMarkupTag.ReplaceAllString(summary[1], "\n"))
		lines := []string{}
		for _, line := range strings.Split(clean, "\n") {
			if line = strings.TrimSpace(line); line != "" {
				lines = append(lines, line)
			}
		}
		summaries[attrs["id"]] = truncateGranolaSummary(strings.Join(lines, "\n"))
	}
	return summaries, true
}

func truncateGranolaSummary(text string) string {
	runes := []rune(strings.TrimSpace(text))
	if len(runes) > 1600 {
		return string(runes[:1600]) + "…"
	}
	return string(runes)
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func parseGranolaMeetings(raw json.RawMessage) ([]granolaMeeting, bool) {
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) == nil && len(fields["meetings"]) > 0 && string(fields["meetings"]) != "null" {
		var meetings []granolaMeeting
		if json.Unmarshal(fields["meetings"], &meetings) == nil {
			return meetings, true
		}
	}
	var text string
	if json.Unmarshal(raw, &text) != nil {
		return nil, false
	}
	root := granolaMeetingsTag.FindStringSubmatch(text)
	if len(root) != 2 {
		return nil, false
	}
	var meetings []granolaMeeting
	for _, match := range granolaMeetingTag.FindAllStringSubmatch(text, 6) {
		attrs := map[string]string{}
		for _, pair := range granolaAttribute.FindAllStringSubmatch(match[1], -1) {
			attrs[pair[1]] = html.UnescapeString(pair[2])
		}
		meetings = append(meetings, granolaMeeting{ID: attrs["id"], Title: attrs["title"], Date: attrs["date"], URL: attrs["url"]})
	}
	return meetings, true
}

func short(text string) string {
	runes := []rune(strings.Join(strings.Fields(text), " "))
	if len(runes) > 140 {
		return string(runes[:140]) + "…"
	}
	return string(runes)
}
