package semantic

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

var (
	secretKeyPattern   = regexp.MustCompile(`(?i)(api[_-]?key|authorization|bearer|credential|password|private[_-]?key|secret|token)`)
	secretValuePattern = regexp.MustCompile(`(?i)(-----BEGIN [A-Z ]*PRIVATE KEY-----|sk-[A-Za-z0-9_-]{20,}|xox[baprs]-[A-Za-z0-9-]{10,})`)
)

func SanitizeText(input string) string {
	lines := strings.Split(input, "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		if secretKeyPattern.MatchString(line) || secretValuePattern.MatchString(line) {
			out = append(out, "[REDACTED_SECRET]")
			continue
		}
		out = append(out, line)
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

func SanitizeMap(input map[string]any) map[string]any {
	if input == nil {
		return map[string]any{}
	}
	out := make(map[string]any, len(input))
	for key, value := range input {
		if secretKeyPattern.MatchString(key) {
			out[key] = "[REDACTED_SECRET]"
			continue
		}
		out[key] = sanitizeValue(value)
	}
	return out
}

func sanitizeValue(value any) any {
	switch typed := value.(type) {
	case string:
		if secretValuePattern.MatchString(typed) {
			return "[REDACTED_SECRET]"
		}
		return SanitizeText(typed)
	case map[string]any:
		return SanitizeMap(typed)
	case []any:
		out := make([]any, 0, len(typed))
		for _, item := range typed {
			out = append(out, sanitizeValue(item))
		}
		return out
	default:
		return value
	}
}

func CompactMetadata(input map[string]any) string {
	clean := SanitizeMap(input)
	if len(clean) == 0 {
		return ""
	}
	keys := make([]string, 0, len(clean))
	for key := range clean {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	ordered := make(map[string]any, len(clean))
	for _, key := range keys {
		ordered[key] = clean[key]
	}
	raw, err := json.Marshal(ordered)
	if err != nil {
		return fmt.Sprintf("%v", ordered)
	}
	return string(raw)
}
