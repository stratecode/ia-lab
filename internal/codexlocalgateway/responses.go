package codexlocalgateway

import (
	"encoding/json"
	"errors"
	"strings"
)

func flattenInput(raw json.RawMessage) (string, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return "", errors.New("input is required")
	}

	var asString string
	if err := json.Unmarshal(raw, &asString); err == nil {
		return asString, nil
	}

	var values []any
	if err := json.Unmarshal(raw, &values); err != nil {
		return "", errors.New("input must be a string or text array")
	}
	parts := make([]string, 0, len(values))
	for _, value := range values {
		text := textFromValue(value)
		if strings.TrimSpace(text) != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "\n"), nil
}

func textFromValue(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case map[string]any:
		return textFromMap(typed)
	default:
		return ""
	}
}

func textFromMap(value map[string]any) string {
	if text, ok := value["text"].(string); ok {
		return text
	}
	content, ok := value["content"]
	if !ok {
		return ""
	}
	switch typed := content.(type) {
	case string:
		return typed
	case []any:
		parts := make([]string, 0, len(typed))
		for _, item := range typed {
			if itemText := textFromValue(item); strings.TrimSpace(itemText) != "" {
				parts = append(parts, itemText)
			}
		}
		return strings.Join(parts, "\n")
	default:
		return ""
	}
}

func remapModel(body []byte, publicModel, upstreamModel string) []byte {
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return body
	}
	model, _ := payload["model"].(string)
	if model == "" || model == publicModel {
		payload["model"] = upstreamModel
		remapped, err := json.Marshal(payload)
		if err == nil {
			return remapped
		}
	}
	return body
}
