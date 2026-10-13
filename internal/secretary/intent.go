package secretary

import (
	"encoding/json"
	"strings"

	"golang.org/x/text/unicode/norm"
)

type secretaryIntent string

const (
	intentGeneral    secretaryIntent = "general"
	intentCapability secretaryIntent = "tool"
	intentMemory     secretaryIntent = "memory"
	intentClarify    secretaryIntent = "clarify"
	intentAmbiguous  secretaryIntent = "ambiguous"
)

func localIntent(text string) secretaryIntent {
	query := normalizedIntentText(text)
	hasMeeting := containsAny(query, "granola", "reunion", "reuniones")
	if hasMeeting && containsAny(query, "pendiente", "accion", "decision", "resumen", "recuerd", "ultima", "reciente", "acordamos", "hablamos", "compromiso", "tarea", "seguimiento", "que salio", "que se dijo") {
		return intentCapability
	}
	if containsAny(query, "ya me habias dicho", "me habias respondido", "me respondiste antes", "ya me contestaste", "te pregunte antes", "que te pregunte", "que me dijiste", "que hablamos antes", "hablamos anteriormente", "en otra conversacion", "recuerdas que te", "recuerdame que te dije") {
		return intentMemory
	}
	if hasMeeting {
		return intentAmbiguous
	}
	return intentGeneral
}

func normalizedIntentText(text string) string {
	plain := norm.NFD.String(strings.ToLower(text))
	var out strings.Builder
	space := true
	for _, r := range plain {
		if r >= 0x0300 && r <= 0x036f {
			continue
		}
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			out.WriteRune(r)
			space = false
			continue
		}
		if !space {
			out.WriteByte(' ')
			space = true
		}
	}
	return strings.TrimSpace(out.String())
}

func containsAny(text string, phrases ...string) bool {
	for _, phrase := range phrases {
		if strings.Contains(" "+text+" ", " "+phrase+" ") || strings.Contains(text, phrase) {
			return true
		}
	}
	return false
}

func parseIntentResponse(content string) secretaryIntent {
	content = strings.TrimSpace(content)
	start, end := strings.IndexByte(content, '{'), strings.LastIndexByte(content, '}')
	if start < 0 || end < start {
		return intentClarify
	}
	var response struct {
		Intent string `json:"intent"`
	}
	if json.Unmarshal([]byte(content[start:end+1]), &response) != nil {
		return intentClarify
	}
	switch secretaryIntent(strings.ToLower(strings.TrimSpace(response.Intent))) {
	case intentCapability, secretaryIntent("granola"), intentGeneral, intentMemory, intentClarify:
		if strings.EqualFold(strings.TrimSpace(response.Intent), "granola") {
			return intentCapability
		}
		return secretaryIntent(strings.ToLower(strings.TrimSpace(response.Intent)))
	default:
		return intentClarify
	}
}
