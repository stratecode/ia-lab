package planner

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

type Service struct {
	baseURL string
	apiKey  string
	client  *http.Client
}

type Result struct {
	Summary  string           `json:"summary"`
	Plan     string           `json:"plan"`
	Subtasks []map[string]any `json:"subtasks"`
	Raw      string           `json:"raw_response"`
}

func New(baseURL, apiKey string, timeoutSeconds int) *Service {
	timeout := time.Duration(timeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 90 * time.Second
	}
	return &Service{
		baseURL: strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		apiKey:  strings.TrimSpace(apiKey),
		client:  &http.Client{Timeout: timeout},
	}
}

func (s *Service) CreatePlan(ctx context.Context, description string, metadata map[string]any) (*Result, error) {
	if s.baseURL == "" {
		return nil, fmt.Errorf("planner base URL is not configured")
	}
	metadataJSON, _ := json.Marshal(metadata)
	capabilityContext := ""
	if items, ok := metadata["capability_context"].([]string); ok && len(items) > 0 {
		capabilityContext = "\n\nContexto adicional de capacidades:\n" + strings.Join(items[:minInt(5, len(items))], "\n\n")
	}
	prompt := strings.Join([]string{
		"Eres un planner operativo para un orquestador multiagente.",
		`Devuelve SOLO JSON valido con esta forma exacta: {"summary":"...","plan":"...","subtasks":[{"title":"...","description":"...","assigned_agent":"coder","priority":"normal","requires_approval":false}]}`,
		"Reglas:",
		"- assigned_agent solo puede ser 'planner', 'researcher', 'coder' o 'reviewer'",
		"- No incluyas markdown ni texto fuera del JSON",
		"- Crea subtareas ejecutables y concretas",
		"- Usa requires_approval=true solo si la subtarea parece destructiva o sensible",
		"",
		"Objetivo:",
		description,
		"",
		"Metadata:",
		string(metadataJSON) + capabilityContext,
	}, "\n")
	body := map[string]any{
		"model": "planner",
		"messages": []map[string]string{
			{"role": "user", "content": prompt},
		},
		"temperature":     0.1,
		"max_tokens":      1600,
		"response_format": map[string]any{"type": "json_object"},
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.baseURL+"/chat/completions", bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if s.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+s.apiKey)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("planner request failed with status %d: %s", resp.StatusCode, strings.TrimSpace(string(payload)))
	}
	var completion struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(payload, &completion); err != nil {
		return nil, err
	}
	if len(completion.Choices) == 0 {
		return nil, fmt.Errorf("planner returned no choices")
	}
	content := strings.TrimSpace(completion.Choices[0].Message.Content)
	if content == "" {
		return nil, fmt.Errorf("planner returned empty content")
	}
	content = stripCodeFence(content)
	var parsed Result
	if err := json.Unmarshal([]byte(content), &parsed); err != nil {
		return nil, fmt.Errorf("invalid planner output: %w", err)
	}
	parsed.Raw = content
	if strings.TrimSpace(parsed.Summary) == "" || strings.TrimSpace(parsed.Plan) == "" {
		return nil, fmt.Errorf("planner output missing summary or plan")
	}
	return &parsed, nil
}

func stripCodeFence(input string) string {
	value := strings.TrimSpace(input)
	value = strings.TrimPrefix(value, "```json")
	value = strings.TrimPrefix(value, "```")
	value = strings.TrimSuffix(value, "```")
	return strings.TrimSpace(value)
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
