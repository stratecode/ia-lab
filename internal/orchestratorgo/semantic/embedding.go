package semantic

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

type EmbeddingError struct {
	StatusCode int
}

func (e *EmbeddingError) Error() string {
	if e == nil {
		return "embedding request failed"
	}
	if e.StatusCode > 0 {
		return fmt.Sprintf("embedding endpoint returned status %d", e.StatusCode)
	}
	return "embedding request failed"
}

type EmbeddingClient struct {
	baseURL    string
	apiKey     string
	model      string
	dimensions int
	client     *http.Client
}

func NewEmbeddingClient(baseURL, apiKey, model string, dimensions int, timeout time.Duration) *EmbeddingClient {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return &EmbeddingClient{
		baseURL:    strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		apiKey:     strings.TrimSpace(apiKey),
		model:      strings.TrimSpace(model),
		dimensions: dimensions,
		client:     &http.Client{Timeout: timeout},
	}
}

func (c *EmbeddingClient) Embed(ctx context.Context, input string) ([]float32, error) {
	if c == nil || c.client == nil {
		return nil, fmt.Errorf("embedding client is not configured")
	}
	if c.baseURL == "" {
		return nil, fmt.Errorf("embedding base URL is not configured")
	}
	body := map[string]any{
		"model": c.model,
		"input": input,
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/embeddings", bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &EmbeddingError{StatusCode: resp.StatusCode}
	}
	var parsed struct {
		Data []struct {
			Embedding []float64 `json:"embedding"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, err
	}
	if len(parsed.Data) == 0 || len(parsed.Data[0].Embedding) == 0 {
		return nil, fmt.Errorf("embedding response missing data[0].embedding")
	}
	if c.dimensions > 0 && len(parsed.Data[0].Embedding) != c.dimensions {
		return nil, fmt.Errorf("embedding dimensions mismatch: got %d, want %d", len(parsed.Data[0].Embedding), c.dimensions)
	}
	out := make([]float32, len(parsed.Data[0].Embedding))
	for i, value := range parsed.Data[0].Embedding {
		out[i] = float32(value)
	}
	return out, nil
}
