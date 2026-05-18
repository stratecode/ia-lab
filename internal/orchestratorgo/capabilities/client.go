package capabilities

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type Client struct {
	httpClient        *http.Client
	docsBaseURL       string
	imagesBaseURL     string
	maxDocumentBytes  int
	maxImageBytes     int
	maxArtifactChars  int
	allowedURLSchemes []string
	allowedLocalRoots []string
}

type Options struct {
	DocsBaseURL       string
	ImagesBaseURL     string
	MaxDocumentBytes  int
	MaxImageBytes     int
	MaxArtifactChars  int
	AllowedURLSchemes []string
	AllowedLocalRoots []string
	TimeoutSeconds    int
}

type SourceRef struct {
	Title    string         `json:"title"`
	URI      string         `json:"uri"`
	Kind     string         `json:"kind"`
	Snippet  string         `json:"snippet,omitempty"`
	Metadata map[string]any `json:"metadata,omitempty"`
}

type Artifact struct {
	ArtifactType string         `json:"artifact_type"`
	Title        string         `json:"title,omitempty"`
	URI          string         `json:"uri,omitempty"`
	MediaType    string         `json:"media_type,omitempty"`
	ContentText  string         `json:"content_text,omitempty"`
	Metadata     map[string]any `json:"metadata,omitempty"`
}

type Result struct {
	Status       string         `json:"status"`
	Summary      string         `json:"summary"`
	Output       map[string]any `json:"output"`
	SourceRefs   []SourceRef    `json:"source_refs"`
	Artifacts    []Artifact     `json:"artifacts"`
	ErrorMessage string         `json:"error_message,omitempty"`
}

func New(options Options) *Client {
	timeout := time.Duration(options.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 20 * time.Second
	}
	return &Client{
		httpClient:        &http.Client{Timeout: timeout},
		docsBaseURL:       strings.TrimRight(strings.TrimSpace(options.DocsBaseURL), "/"),
		imagesBaseURL:     strings.TrimRight(strings.TrimSpace(options.ImagesBaseURL), "/"),
		maxDocumentBytes:  options.MaxDocumentBytes,
		maxImageBytes:     options.MaxImageBytes,
		maxArtifactChars:  options.MaxArtifactChars,
		allowedURLSchemes: options.AllowedURLSchemes,
		allowedLocalRoots: options.AllowedLocalRoots,
	}
}

func (c *Client) ReadDocument(ctx context.Context, location string) (*Result, error) {
	if c.docsBaseURL == "" {
		return nil, fmt.Errorf("document sidecar is not configured")
	}
	payload := map[string]any{
		"location":            location,
		"max_bytes":           c.maxDocumentBytes,
		"max_chars":           c.maxArtifactChars,
		"allowed_url_schemes": c.allowedURLSchemes,
		"allowed_local_roots": c.allowedLocalRoots,
	}
	return c.post(ctx, c.docsBaseURL+"/document/read", payload)
}

func (c *Client) AnalyzeImage(ctx context.Context, location string) (*Result, error) {
	if c.imagesBaseURL == "" {
		return nil, fmt.Errorf("image sidecar is not configured")
	}
	payload := map[string]any{
		"location":            location,
		"max_bytes":           c.maxImageBytes,
		"max_chars":           c.maxArtifactChars,
		"allowed_url_schemes": c.allowedURLSchemes,
		"allowed_local_roots": c.allowedLocalRoots,
	}
	return c.post(ctx, c.imagesBaseURL+"/image/analyze", payload)
}

func (c *Client) post(ctx context.Context, endpoint string, payload map[string]any) (*Result, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
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
		return nil, fmt.Errorf("sidecar request failed with status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var result Result
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, err
	}
	if result.Output == nil {
		result.Output = map[string]any{}
	}
	return &result, nil
}
