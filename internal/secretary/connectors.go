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
	"sort"
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
type JiraRESTConfig struct {
	AccessToken       string `json:"access_token"`
	CloudID           string `json:"cloud_id"`
	OperatorAccountID string `json:"operator_account_id"`
}
type ConnectorConfig struct {
	Jira    JiraRESTConfig `json:"jira"`
	Granola OAuthConfig    `json:"granola"`
}
type Connectors struct {
	Config        ConnectorConfig
	Client        *http.Client
	StateDir      string
	mu            sync.Mutex
	allowTestHTTP bool
	jiraAPIBase   string
}

func NewConnectors(config ConnectorConfig, stateDir string) *Connectors {
	c := &Connectors{Config: config, StateDir: stateDir, Client: &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error { return errors.New("connector redirects disabled") }}}
	var cached ConnectorConfig
	if raw, err := os.ReadFile(filepath.Join(stateDir, "oauth-cache.json")); err == nil && json.Unmarshal(raw, &cached) == nil {
		for _, pair := range [][2]*OAuthConfig{{&c.Config.Granola, &cached.Granola}} {
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
	return map[string]bool{"Jira": c.Config.Jira.AccessToken != "" && c.Config.Jira.CloudID != "", "Granola": c.Config.Granola.AccessToken != ""}
}
func (c *Connectors) validServer(raw string) bool {
	if raw == "https://mcp.granola.ai/mcp" {
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
	expected := "mcp-auth.granola.ai"
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

var readTools = map[string]bool{"list_meetings": true, "get_meetings": true, "query_granola_meetings": true}

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
	if c.Config.Jira.AccessToken == "" {
		return "Jira: no conectado", nil
	}
	jql, err := jiraAssigneeJQL(c.Config.Jira.OperatorAccountID, false)
	if err != nil {
		return "", err
	}
	result, err := c.queryJira(ctx, jql)
	if err != nil {
		return "", err
	}
	return formatJiraIssues("Jira · hasta 5 pendientes asignados a tu cuenta (muestra, no listado completo):", result.Issues), nil
}

// JiraQueryResult compiles a natural-language request into a bounded JQL query,
// then uses Jira Cloud REST directly. The configured token is sent only to
// api.atlassian.com; the MCP endpoint is never used for Jira.
func (c *Connectors) JiraQueryResult(ctx context.Context, query string) (CapabilityResult, error) {
	if c.Config.Jira.AccessToken == "" {
		return CapabilityResult{}, errors.New("Jira no conectado")
	}
	jql, err := compileJiraJQL(query, c.Config.Jira.OperatorAccountID)
	if err != nil {
		return CapabilityResult{}, err
	}
	result, err := c.queryJira(ctx, jql)
	if err != nil {
		return CapabilityResult{}, err
	}
	if len(result.Issues) == 0 {
		return CapabilityResult{}, nil
	}
	evidence := make([]EvidenceRef, 0, len(result.Issues))
	for _, issue := range result.Issues {
		evidence = append(evidence, EvidenceRef{Provider: "Jira", ID: issue.Key, Title: issue.Fields.Summary, RetrievedAt: time.Now()})
	}
	return CapabilityResult{
		Text:        formatJiraIssues("Jira · incidencias encontradas mediante Jira REST/JQL:", result.Issues),
		ResultCount: len(result.Issues), Evidence: evidence,
	}, nil
}

type jiraSearchResponse struct {
	Issues []struct {
		Key    string `json:"key"`
		Fields struct {
			Summary string `json:"summary"`
			Status  struct {
				Name string `json:"name"`
			} `json:"status"`
			Assignee *struct {
				DisplayName string `json:"displayName"`
				AccountID   string `json:"accountId"`
			} `json:"assignee"`
			Due     string `json:"duedate"`
			Updated string `json:"updated"`
		} `json:"fields"`
	} `json:"issues"`
}

func (c *Connectors) queryJira(ctx context.Context, jql string) (jiraSearchResponse, error) {
	if c.Config.Jira.AccessToken == "" {
		return jiraSearchResponse{}, errors.New("Jira no conectado")
	}
	cloudID := strings.TrimSpace(c.Config.Jira.CloudID)
	if cloudID == "" || !regexp.MustCompile(`^[A-Za-z0-9-]{1,100}$`).MatchString(cloudID) {
		return jiraSearchResponse{}, errors.New("Jira cloud_id no configurado o no válido")
	}
	base := "https://api.atlassian.com"
	if c.allowTestHTTP {
		u, err := url.Parse(c.jiraAPIBase)
		if err == nil && u.Scheme == "http" && (u.Hostname() == "127.0.0.1" || u.Hostname() == "localhost") {
			base = strings.TrimRight(c.jiraAPIBase, "/")
		}
	}
	endpoint := base + "/ex/jira/" + url.PathEscape(cloudID) + "/rest/api/3/search/jql"
	body, _ := json.Marshal(map[string]any{"jql": jql, "maxResults": 10, "fields": []string{"summary", "status", "assignee", "duedate", "updated"}})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return jiraSearchResponse{}, errors.New("Jira REST request rejected")
	}
	req.Header.Set("Authorization", "Bearer "+c.Config.Jira.AccessToken)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	client := c.Client
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return jiraSearchResponse{}, errors.New("Jira REST unavailable")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return jiraSearchResponse{}, fmt.Errorf("Jira REST HTTP %d", resp.StatusCode)
	}
	var result jiraSearchResponse
	payload, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return jiraSearchResponse{}, errors.New("invalid Jira REST response")
	}
	var envelope struct {
		Issues json.RawMessage `json:"issues"`
	}
	if json.Unmarshal(payload, &envelope) != nil || len(envelope.Issues) == 0 || envelope.Issues[0] != '[' {
		return jiraSearchResponse{}, errors.New("unexpected Jira REST response schema")
	}
	if err := json.Unmarshal(payload, &result); err != nil {
		return jiraSearchResponse{}, errors.New("invalid Jira REST response")
	}
	for _, issue := range result.Issues {
		if !jiraIssueKey.MatchString(issue.Key) {
			return jiraSearchResponse{}, errors.New("Jira issue key missing or invalid")
		}
	}
	return result, nil
}

var jiraIssueKey = regexp.MustCompile(`^[A-Z][A-Z0-9]*-[0-9]+$`)
var jiraIssueKeyMention = regexp.MustCompile(`\b[A-Z][A-Z0-9]*-[0-9]+\b`)
var jiraWord = regexp.MustCompile(`[A-Za-zÁÉÍÓÚÜÑáéíóúüñ0-9_-]{3,}`)

func compileJiraJQL(query, operatorAccountID string) (string, error) {
	query = strings.TrimSpace(query)
	if query == "" || len([]rune(query)) > 2000 {
		return "", errors.New("Jira query length rejected")
	}
	if key := jiraIssueKeyMention.FindString(strings.ToUpper(query)); key != "" {
		return "key = " + key + " ORDER BY updated DESC", nil
	}
	normalized := normalizedIntentText(query)
	if containsAny(normalized, "mis tareas", "mi tarea", "mis pendientes", "pendientes mios", "asignadas a mi", "asignados a mi", "asignada a mi", "que tengo pendiente", "que tengo pendientes", "que tareas tengo", "para mi", "de mi", "a mi cargo", "requiere de mi", "requiera de mi", "necesita de mi", "necesite de mi") {
		return jiraAssigneeJQL(operatorAccountID, containsAny(normalized, "en proceso", "en curso", "en marcha"))
	}
	stop := map[string]bool{
		"que": true, "tengo": true, "tarea": true, "tareas": true, "pendiente": true, "pendientes": true,
		"jira": true, "plan": true, "buscar": true, "busca": true, "dame": true, "muestrame": true, "cual": true, "cuales": true,
		"para": true, "sobre": true, "estado": true, "persona": true, "asignada": true, "asignado": true,
		"responsable": true, "fecha": true, "vencimiento": true, "hoy": true, "semana": true, "proxima": true,
		"proximo": true, "segun": true, "reuniones": true, "reunion": true, "mañana": true, "manana": true,
		"esta": true, "las": true, "los": true, "una": true, "con": true, "del": true, "por": true, "como": true,
		"deberia": true, "debo": true, "hacer": true, "hacerme": true, "haga": true, "toca": true, "estan": true,
		"alguna": true, "alguno": true, "requiera": true,
		"en": true, "mi": true, "me": true, "proceso": true, "curso": true, "marcha": true, "requieran": true,
		"requiere": true, "importantes": true, "importante": true, "acciones": true, "accion": true,
		"actividades": true, "actividad": true, "trabajo": true, "dia": true, "dias": true, "ultima": true,
		"ultimas": true, "mes": true, "meses": true, "pasada": true, "pasado": true, "pasados": true,
		"pasadas": true, "todo": true, "todas": true, "todos": true,
	}
	seen := map[string]bool{}
	var terms []string
	for _, word := range jiraWord.FindAllString(query, -1) {
		word = normalizedIntentText(word)
		if stop[word] || seen[word] {
			continue
		}
		seen[word] = true
		terms = append(terms, word)
		if len(terms) == 4 {
			break
		}
	}
	if len(terms) == 0 {
		return jiraAssigneeJQL(operatorAccountID, containsAny(normalized, "en proceso", "en curso", "en marcha"))
	}
	clauses := make([]string, 0, len(terms))
	for _, term := range terms {
		clauses = append(clauses, `text ~ "`+strings.ReplaceAll(term, `"`, `\\"`)+`"`)
	}
	return strings.Join(clauses, " AND ") + " ORDER BY updated DESC", nil
}

var jiraAccountID = regexp.MustCompile(`^[A-Za-z0-9:_-]{1,200}$`)

func jiraAssigneeJQL(operatorAccountID string, inProgress bool) (string, error) {
	operatorAccountID = strings.TrimSpace(operatorAccountID)
	if !jiraAccountID.MatchString(operatorAccountID) {
		return "", errors.New("Jira operator_account_id no configurado o no válido; no buscaré tareas de la cuenta de servicio")
	}
	status := `statusCategory != Done`
	if inProgress {
		status = `statusCategory = "In Progress"`
	}
	return `assignee = "` + operatorAccountID + `" AND ` + status + ` ORDER BY updated DESC`, nil
}

func formatJiraIssues(heading string, issues []struct {
	Key    string `json:"key"`
	Fields struct {
		Summary string `json:"summary"`
		Status  struct {
			Name string `json:"name"`
		} `json:"status"`
		Assignee *struct {
			DisplayName string `json:"displayName"`
			AccountID   string `json:"accountId"`
		} `json:"assignee"`
		Due     string `json:"duedate"`
		Updated string `json:"updated"`
	} `json:"fields"`
}) string {
	lines := []string{heading}
	if len(issues) == 0 {
		return heading + "\nLa consulta JQL no devolvió incidencias."
	}
	if len(issues) > 10 {
		issues = issues[:10]
	}
	for _, issue := range issues {
		assignee, due := "sin asignar", "sin vencimiento"
		if issue.Fields.Assignee != nil && issue.Fields.Assignee.DisplayName != "" {
			assignee = issue.Fields.Assignee.DisplayName
		}
		if issue.Fields.Due != "" {
			due = issue.Fields.Due
		}
		lines = append(lines, fmt.Sprintf("[%s] %s · Estado: %s · Responsable: %s · Vencimiento: %s", short(issue.Key), short(issue.Fields.Summary), short(issue.Fields.Status.Name), short(assignee), short(due)))
	}
	return strings.Join(lines, "\n")
}

func validEvidenceURL(raw string) bool {
	u, err := url.Parse(strings.TrimSpace(raw))
	return err == nil && u.Scheme == "https" && u.Host != "" && u.User == nil
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
	queryAvailable, detailsAvailable := false, false
	for _, tool := range tools.Tools {
		if tool.Name == "query_granola_meetings" {
			queryAvailable = true
		} else if tool.Name == "get_meetings" {
			detailsAvailable = true
		}
	}
	if !queryAvailable {
		return CapabilityResult{}, errors.New("Granola query tool unavailable")
	}
	if !detailsAvailable {
		return CapabilityResult{}, errors.New("Granola meeting detail tool unavailable")
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
	ids := make([]string, 0, 3)
	seen := map[string]bool{}
	for _, match := range granolaCitation.FindAllStringSubmatch(text, 10) {
		if len(match) != 3 || !validEvidenceURL(match[2]) || seen[match[2]] {
			continue
		}
		seen[match[2]] = true
		id := strings.TrimPrefix(match[2], "https://notes.granola.ai/d/")
		ids = append(ids, id)
		evidence = append(evidence, EvidenceRef{Provider: "Granola", ID: id, URL: match[2], RetrievedAt: time.Now()})
	}
	if len(ids) == 0 {
		return CapabilityResult{}, errors.New("Granola query returned no cited meeting notes")
	}
	if len(ids) > 5 {
		ids = ids[:5]
		evidence = evidence[:5]
	}
	raw, err = s.callTool(ctx, "get_meetings", map[string]any{"meeting_ids": ids})
	if err != nil {
		return CapabilityResult{}, fmt.Errorf("Granola cited meeting details unavailable: %w", err)
	}
	summaries, valid := parseGranolaSummaries(raw)
	if !valid {
		return CapabilityResult{}, errors.New("unexpected Granola meeting detail schema")
	}
	lines := make([]string, 0, len(ids))
	for _, item := range evidence {
		summary := strings.TrimSpace(summaries[item.ID])
		if summary == "" {
			return CapabilityResult{}, fmt.Errorf("Granola note %s has no readable summary", item.ID)
		}
		lines = append(lines, "Nota de reunión de Granola ("+item.ID+"):\n"+summary)
	}
	return CapabilityResult{Text: strings.Join(lines, "\n\n"), ResultCount: len(evidence), Evidence: evidence}, nil
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
	if len(runes) <= 1600 {
		return string(runes)
	}

	lines := strings.Split(string(runes), "\n")
	type section struct {
		start int
		text  string
		limit int
	}
	var projectSections, actionSections []section
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		isAction := explicitActionSection.MatchString(trimmed)
		isProject := explicitOwnerSection.MatchString(trimmed)
		if !isAction && !isProject {
			continue
		}
		end := i + 1
		for end < len(lines) && !markdownHeading.MatchString(strings.TrimSpace(lines[end])) {
			end++
		}
		candidate := strings.Join(lines[i:end], "\n")
		if isAction {
			actionSections = append(actionSections, section{start: i, text: candidate, limit: 2400})
		} else {
			projectSections = append(projectSections, section{start: i, text: candidate, limit: 800})
		}
	}
	if len(actionSections) == 0 && len(projectSections) == 0 {
		return string(runes[:1600]) + "…"
	}

	const maxRunes = 4000
	prefix := string(runes)
	if len(runes) > 1000 {
		prefix = string(runes[:1000]) + "…"
	}
	parts := []string{prefix}
	used := len([]rune(prefix))
	sections := append(projectSections, actionSections...)
	sort.Slice(sections, func(i, j int) bool { return sections[i].start < sections[j].start })
	for _, block := range sections {
		remaining := maxRunes - used - 2
		limit := block.limit
		if limit > remaining {
			limit = remaining
		}
		if limit <= 0 {
			break
		}
		blockRunes := []rune(strings.TrimSpace(block.text))
		if len(blockRunes) > limit {
			block.text = string(blockRunes[:limit]) + "…"
		}
		parts = append(parts, block.text)
		used += len([]rune(block.text)) + 2
	}
	return strings.Join(parts, "\n\n")
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
