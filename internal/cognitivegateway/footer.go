package cognitivegateway

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

func buildFooter(state *contextState, modelDuration time.Duration, responseTokens int, totalDuration time.Duration) string {
	return fmt.Sprintf(
		"[Mode: %s][Initial %dt][Parsed %dt][Model response %s][Response %dt][Total response %s]",
		state.Mode,
		state.InitialTokens,
		state.Estimated,
		formatSeconds(modelDuration),
		responseTokens,
		formatSeconds(totalDuration),
	)
}

func formatSeconds(duration time.Duration) string {
	seconds := duration.Seconds()
	text := fmt.Sprintf("%.2fs", seconds)
	text = strings.TrimRight(strings.TrimRight(text, "0"), ".")
	return strings.ReplaceAll(text, ".", ",")
}

func responseTokenEstimate(payload []byte) int {
	var parsed struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(payload, &parsed); err != nil || len(parsed.Choices) == 0 {
		return (len(payload) + 3) / 4
	}
	total := 0
	for _, choice := range parsed.Choices {
		total += len([]rune(choice.Message.Content))
	}
	return (total + 3) / 4
}

func appendFooterToContent(payload []byte, footer string) ([]byte, error) {
	var parsed map[string]any
	if err := json.Unmarshal(payload, &parsed); err != nil {
		return nil, err
	}
	choices, ok := parsed["choices"].([]any)
	if !ok || len(choices) == 0 {
		return nil, fmt.Errorf("response has no choices")
	}
	first, ok := choices[0].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("response choice has unexpected shape")
	}
	message, ok := first["message"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("response choice has no message")
	}
	content, _ := message["content"].(string)
	message["content"] = strings.TrimRight(content, "\n") + "\n\n" + footer
	parsed["cognitive_gateway"] = map[string]any{"footer": footer}
	return json.Marshal(parsed)
}

func addGatewayMetadata(payload []byte, footer string) ([]byte, error) {
	var parsed map[string]any
	if err := json.Unmarshal(payload, &parsed); err != nil {
		return nil, err
	}
	parsed["cognitive_gateway"] = map[string]any{"footer": footer}
	return json.Marshal(parsed)
}
