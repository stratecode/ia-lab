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
	asksAboutPriorConversation := containsAny(query, "hablamos", "comentamos", "conversacion", "te conte", "te dije", "pregunte", "recuerdas", "acordamos") && containsAny(query, "semana pasada", "semana anterior", "ayer", "esta semana", "hoy", "otro dia")
	if asksAboutPriorConversation {
		return intentMemory
	}
	if !hasMeeting && containsAny(query, "que hablamos", "que comentamos", "que acordamos") {
		return intentMemory
	}
	if containsAny(query, "granola") {
		return intentCapability
	}
	if hasMeeting && containsAny(query, "pendiente", "accion", "decision", "resum", "recuerd", "ultima", "reciente", "acordamos", "hablamos", "compromiso", "tarea", "seguimiento", "ejecut", "realizar", "llevar a cabo", "que salio", "que se dijo", "quien", "responsable", "revisa", "quedo", "quedaron", "hacer", "envia", "mandar", "compart", "prepar", "crear", "investig", "implementar", "terminar", "coordinar") {
		return intentCapability
	}
	if containsAny(query, "ya me habias dicho", "me habias respondido", "me respondiste antes", "ya me contestaste", "te pregunte antes", "que te pregunte", "que me dijiste", "que hablamos antes", "que hablamos la semana", "que hablamos esta semana", "que hablamos ayer", "que hablamos hoy", "que comentamos la semana", "que comentamos ayer", "que acordamos la semana", "hablamos anteriormente", "en otra conversacion", "recuerdas que te", "recuerdame que te dije") {
		return intentMemory
	}
	if hasMeeting {
		return intentAmbiguous
	}
	if containsAny(query, "tarea", "pendiente", "accion", "hacer", "prioridad", "toca", "siguiente paso") {
		return intentAmbiguous
	}
	return intentGeneral
}

// rovoTaskIntent keeps planning and pending-work questions on the broader
// Atlassian source, even when the user mentions meetings. Meeting summaries
// without an action request continue to use Granola.
func rovoTaskIntent(text string) bool {
	query := normalizedIntentText(text)
	if jiraIssueKeyMention.MatchString(strings.ToUpper(text)) {
		return true
	}
	return containsAny(query,
		"rovo", "jira", "confluence", "plan", "planificacion", "planificar",
		"tarea", "pendiente", "actividad", "accion", "hacer", "ejecut", "realizar", "llevar a cabo",
		"acciones por hacer", "que debo hacer", "que tengo que hacer",
		"siguiente paso", "prioridad", "priorizar",
	)
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
