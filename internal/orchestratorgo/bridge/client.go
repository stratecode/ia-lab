package bridge

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/stratecode/lab/internal/orchestratorgo/domain"
)

type Client struct {
	baseURL    string
	apiKey     string
	httpClient *http.Client
}

func NewClient(baseURL, apiKey string, timeout time.Duration) *Client {
	if timeout <= 0 {
		timeout = 20 * time.Second
	}
	return &Client{
		baseURL: strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		apiKey:  strings.TrimSpace(apiKey),
		httpClient: &http.Client{
			Timeout: timeout,
		},
	}
}

func (c *Client) Register(ctx context.Context, body domain.LocalBridgeRegisterRequest) (*domain.LocalBridgeResponse, error) {
	var out domain.LocalBridgeResponse
	if err := c.requestJSON(ctx, http.MethodPost, "/bridges/register", body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) Heartbeat(ctx context.Context, bridgeID, status string) (*domain.LocalBridgeResponse, error) {
	var out domain.LocalBridgeResponse
	if err := c.requestJSON(ctx, http.MethodPost, "/bridges/"+bridgeID+"/heartbeat", domain.LocalBridgeHeartbeatRequest{Status: status}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) ListBridges(ctx context.Context) (*domain.LocalBridgeListResponse, error) {
	var out domain.LocalBridgeListResponse
	if err := c.requestJSON(ctx, http.MethodGet, "/bridges", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) ClaimNext(ctx context.Context, bridgeID string) (*domain.LocalBridgeTaskClaimResponse, error) {
	req, err := c.newRequest(ctx, http.MethodPost, "/bridges/"+bridgeID+"/claim-next", nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("bridge claim failed with status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	if strings.TrimSpace(string(body)) == "" || strings.TrimSpace(string(body)) == "null" {
		return nil, nil
	}
	var out domain.LocalBridgeTaskClaimResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) SubmitResult(ctx context.Context, bridgeID, taskID string, body domain.LocalBridgeResultRequest) error {
	return c.requestJSON(ctx, http.MethodPost, "/bridges/"+bridgeID+"/tasks/"+taskID+"/result", body, nil)
}

func (c *Client) ListTasks(ctx context.Context, executionTarget string) (*domain.TaskListResponse, error) {
	path := "/tasks?limit=20"
	if strings.TrimSpace(executionTarget) != "" {
		path += "&execution_target=" + url.QueryEscape(executionTarget)
	}
	var out domain.TaskListResponse
	if err := c.requestJSON(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) ListTasksFiltered(ctx context.Context, params map[string]string) (*domain.TaskListResponse, error) {
	values := url.Values{}
	values.Set("limit", "50")
	for key, value := range params {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		values.Set(key, value)
	}
	var out domain.TaskListResponse
	if err := c.requestJSON(ctx, http.MethodGet, "/tasks?"+values.Encode(), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) GetTask(ctx context.Context, taskID string) (*domain.TaskResponse, error) {
	var out domain.TaskResponse
	if err := c.requestJSON(ctx, http.MethodGet, "/tasks/"+strings.TrimSpace(taskID), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) GetTaskTree(ctx context.Context, taskID string) (*domain.TaskTreeResponse, error) {
	var out domain.TaskTreeResponse
	if err := c.requestJSON(ctx, http.MethodGet, "/tasks/"+strings.TrimSpace(taskID)+"/tree", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) CreateTask(ctx context.Context, body domain.TaskCreateRequest) (*domain.TaskResponse, error) {
	var out domain.TaskResponse
	if err := c.requestJSON(ctx, http.MethodPost, "/tasks", body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) CancelTask(ctx context.Context, taskID string) (*domain.TaskResponse, error) {
	var out domain.TaskResponse
	if err := c.requestJSON(ctx, http.MethodPost, "/tasks/"+strings.TrimSpace(taskID)+"/cancel", map[string]any{}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) ArchiveTask(ctx context.Context, taskID string) (*domain.TaskResponse, error) {
	var out domain.TaskResponse
	if err := c.requestJSON(ctx, http.MethodPost, "/tasks/"+strings.TrimSpace(taskID)+"/archive", map[string]any{}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) UnarchiveTask(ctx context.Context, taskID string) (*domain.TaskResponse, error) {
	var out domain.TaskResponse
	if err := c.requestJSON(ctx, http.MethodPost, "/tasks/"+strings.TrimSpace(taskID)+"/unarchive", map[string]any{}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) ListApprovals(ctx context.Context) (*domain.ApprovalListResponse, error) {
	var out domain.ApprovalListResponse
	if err := c.requestJSON(ctx, http.MethodGet, "/approvals", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) ListInitiatives(ctx context.Context, includeArchived bool) (*domain.InitiativeListResponse, error) {
	path := "/initiatives?limit=100"
	if includeArchived {
		path += "&archived=include"
	}
	var out domain.InitiativeListResponse
	if err := c.requestJSON(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) CreateInitiative(ctx context.Context, body domain.InitiativeCreateRequest) (*domain.InitiativeResponse, error) {
	var out domain.InitiativeResponse
	if err := c.requestJSON(ctx, http.MethodPost, "/initiatives", body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) GetInitiative(ctx context.Context, initiativeID string) (*domain.InitiativeResponse, []domain.InitiativePhaseReviewResponse, error) {
	var out struct {
		Initiative *domain.InitiativeResponse          `json:"initiative"`
		Reviews    []domain.InitiativePhaseReviewResponse `json:"reviews"`
	}
	if err := c.requestJSON(ctx, http.MethodGet, "/initiatives/"+strings.TrimSpace(initiativeID), nil, &out); err != nil {
		return nil, nil, err
	}
	return out.Initiative, out.Reviews, nil
}

func (c *Client) GetInitiativeArtifacts(ctx context.Context, initiativeID string) ([]domain.ArtifactResponse, error) {
	var out domain.InitiativeArtifactsResponse
	if err := c.requestJSON(ctx, http.MethodGet, "/initiatives/"+strings.TrimSpace(initiativeID)+"/artifacts", nil, &out); err != nil {
		return nil, err
	}
	return out.Items, nil
}

func (c *Client) ListInitiativeTasks(ctx context.Context, initiativeID string) (*domain.InitiativeTaskListResponse, error) {
	var out domain.InitiativeTaskListResponse
	if err := c.requestJSON(ctx, http.MethodGet, "/initiatives/"+strings.TrimSpace(initiativeID)+"/tasks", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) AdvanceInitiative(ctx context.Context, initiativeID, feedback string) (*domain.InitiativeResponse, error) {
	var out struct {
		Initiative *domain.InitiativeResponse `json:"initiative"`
	}
	if err := c.requestJSON(ctx, http.MethodPost, "/initiatives/"+strings.TrimSpace(initiativeID)+"/advance", domain.InitiativeAdvanceRequest{Feedback: strings.TrimSpace(feedback)}, &out); err != nil {
		return nil, err
	}
	return out.Initiative, nil
}

func (c *Client) ApproveInitiativePhase(ctx context.Context, initiativeID string, phase domain.InitiativePhase, operator, feedback string) (*domain.InitiativeResponse, error) {
	var out domain.InitiativeResponse
	path := fmt.Sprintf("/initiatives/%s/approve/%s", strings.TrimSpace(initiativeID), strings.TrimSpace(string(phase)))
	if err := c.requestJSON(ctx, http.MethodPost, path, domain.InitiativeReviewRequest{Operator: strings.TrimSpace(operator), Feedback: strings.TrimSpace(feedback)}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) RejectInitiativePhase(ctx context.Context, initiativeID string, phase domain.InitiativePhase, operator, feedback string) (*domain.InitiativeResponse, error) {
	var out domain.InitiativeResponse
	path := fmt.Sprintf("/initiatives/%s/reject/%s", strings.TrimSpace(initiativeID), strings.TrimSpace(string(phase)))
	if err := c.requestJSON(ctx, http.MethodPost, path, domain.InitiativeReviewRequest{Operator: strings.TrimSpace(operator), Feedback: strings.TrimSpace(feedback)}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) GenerateInitiativeTasks(ctx context.Context, initiativeID, feedback string) (*domain.InitiativeResponse, *domain.InitiativeTaskListResponse, error) {
	var out struct {
		Initiative *domain.InitiativeResponse   `json:"initiative"`
		Tasks      []domain.InitiativeTaskLinkResponse `json:"tasks"`
		Total      int                          `json:"total"`
	}
	path := fmt.Sprintf("/initiatives/%s/tasks/generate", strings.TrimSpace(initiativeID))
	if err := c.requestJSON(ctx, http.MethodPost, path, domain.InitiativeAdvanceRequest{Feedback: strings.TrimSpace(feedback)}, &out); err != nil {
		return nil, nil, err
	}
	return out.Initiative, &domain.InitiativeTaskListResponse{Items: out.Tasks, Total: out.Total}, nil
}

func (c *Client) LaunchInitiativeTasks(ctx context.Context, initiativeID string, taskIDs []string, groups []string, modeOverrides map[string]string) (*domain.InitiativeResponse, *domain.TaskListResponse, error) {
	var out struct {
		Initiative *domain.InitiativeResponse `json:"initiative"`
		Tasks      []domain.TaskResponse      `json:"tasks"`
		Total      int                        `json:"total"`
	}
	path := fmt.Sprintf("/initiatives/%s/tasks/launch", strings.TrimSpace(initiativeID))
	if err := c.requestJSON(ctx, http.MethodPost, path, domain.InitiativeLaunchTasksRequest{
		TaskIDs:       taskIDs,
		Groups:        groups,
		ModeOverrides: modeOverrides,
	}, &out); err != nil {
		return nil, nil, err
	}
	return out.Initiative, &domain.TaskListResponse{Items: out.Tasks, Total: out.Total}, nil
}

func (c *Client) UpdateInitiativeTaskMode(ctx context.Context, initiativeID, taskID, mode string) (*domain.InitiativeTaskLinkResponse, error) {
	var out domain.InitiativeTaskLinkResponse
	path := fmt.Sprintf("/initiatives/%s/tasks/%s/mode", strings.TrimSpace(initiativeID), strings.TrimSpace(taskID))
	if err := c.requestJSON(ctx, http.MethodPost, path, domain.InitiativeTaskModeRequest{ExecutionMode: strings.TrimSpace(mode)}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) ResolveApproval(ctx context.Context, approvalID, operator string, approve bool) (*domain.ApprovalResponse, error) {
	path := "/approvals/" + strings.TrimSpace(approvalID)
	if approve {
		path += "/approve"
	} else {
		path += "/reject"
	}
	var out domain.ApprovalResponse
	if err := c.requestJSON(ctx, http.MethodPost, path, domain.ApprovalResolveRequest{Operator: strings.TrimSpace(operator)}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) Health(ctx context.Context) (*domain.HealthResponse, error) {
	var out domain.HealthResponse
	if err := c.requestJSON(ctx, http.MethodGet, "/health", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) GetTaskSources(ctx context.Context, taskID string) ([]domain.ArtifactResponse, error) {
	var out struct {
		Items []domain.ArtifactResponse `json:"items"`
		Total int                       `json:"total"`
	}
	if err := c.requestJSON(ctx, http.MethodGet, "/tasks/"+strings.TrimSpace(taskID)+"/sources", nil, &out); err != nil {
		return nil, err
	}
	return out.Items, nil
}

func (c *Client) ListModels(ctx context.Context) ([]string, error) {
	var out struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := c.requestJSON(ctx, http.MethodGet, "/v1/models", nil, &out); err != nil {
		return nil, err
	}
	models := make([]string, 0, len(out.Data))
	for _, item := range out.Data {
		if value := strings.TrimSpace(item.ID); value != "" {
			models = append(models, value)
		}
	}
	return models, nil
}

func (c *Client) ChatCompletion(ctx context.Context, modelID, prompt string) (*ChatResponse, error) {
	body := map[string]any{
		"model": firstNonEmptyString(strings.TrimSpace(modelID), "orchestrator-tools"),
		"messages": []map[string]any{
			{"role": "user", "content": strings.TrimSpace(prompt)},
		},
	}
	var out ChatResponse
	if err := c.requestJSON(ctx, http.MethodPost, "/v1/chat/completions", body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

type ChatResponse struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Model   string `json:"model"`
	Choices []struct {
		Index   int `json:"index"`
		Message struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
}

func (c *Client) ChatText(ctx context.Context, modelID, prompt string) (string, error) {
	response, err := c.ChatCompletion(ctx, modelID, prompt)
	if err != nil {
		return "", err
	}
	if len(response.Choices) == 0 {
		return "", nil
	}
	return strings.TrimSpace(response.Choices[0].Message.Content), nil
}

func (c *Client) requestJSON(ctx context.Context, method, path string, input any, out any) error {
	req, err := c.newRequest(ctx, method, path, input)
	if err != nil {
		return err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("%s %s failed with status %d: %s", method, path, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	if out == nil || len(body) == 0 {
		return nil
	}
	return json.Unmarshal(body, out)
}

func (c *Client) newRequest(ctx context.Context, method, path string, input any) (*http.Request, error) {
	var body io.Reader
	if input != nil {
		raw, err := json.Marshal(input)
		if err != nil {
			return nil, err
		}
		body = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	if input != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return req, nil
}
