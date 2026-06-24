package mcpwrap

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/stratecode/lab/internal/orchestratorgo/capabilities"
)

type Executor interface {
	Execute(ctx context.Context, capability string, payload map[string]any) (*capabilities.Result, error)
}

type Client struct {
	baseURL    string
	httpClient *http.Client
	apiKey     string
}

func NewClient(baseURL, apiKey string, timeoutSeconds int) *Client {
	timeout := time.Duration(timeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 20 * time.Second
	}
	return &Client{
		baseURL:    strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		httpClient: &http.Client{Timeout: timeout},
		apiKey:     strings.TrimSpace(apiKey),
	}
}

func (c *Client) Execute(ctx context.Context, capability string, payload map[string]any) (*capabilities.Result, error) {
	if strings.TrimSpace(c.baseURL) == "" {
		return nil, fmt.Errorf("mcp wrapper client is not configured")
	}
	path := capabilityPath(capability)
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/"+path, bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("mcp wrapper request failed with status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var result capabilities.Result
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, err
	}
	if result.Output == nil {
		result.Output = map[string]any{}
	}
	return &result, nil
}

func capabilityPath(capability string) string {
	replacer := strings.NewReplacer(".", "/")
	return replacer.Replace(strings.TrimSpace(capability))
}
