package bridge

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
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
		path += "&execution_target=" + executionTarget
	}
	var out domain.TaskListResponse
	if err := c.requestJSON(ctx, http.MethodGet, path, nil, &out); err != nil {
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
