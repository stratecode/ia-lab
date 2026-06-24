package browserverify

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

type Request struct {
	URL            string         `json:"url"`
	WaitFor        string         `json:"wait_for,omitempty"`
	AssertText     string         `json:"assert_text,omitempty"`
	Click          string         `json:"click,omitempty"`
	Fill           map[string]any `json:"fill,omitempty"`
	Screenshot     bool           `json:"screenshot,omitempty"`
	ConsoleErrors  bool           `json:"console_errors,omitempty"`
	TimeoutSeconds int            `json:"timeout_seconds,omitempty"`
}

type Verifier interface {
	Verify(ctx context.Context, req Request) (*capabilities.Result, error)
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

func (c *Client) Verify(ctx context.Context, req Request) (*capabilities.Result, error) {
	if strings.TrimSpace(c.baseURL) == "" {
		return nil, fmt.Errorf("browser verifier is not configured")
	}
	raw, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/browser/verify", bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("browser verifier request failed with status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
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
