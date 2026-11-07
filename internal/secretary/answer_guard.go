package secretary

import "strings"

// sourceAnswerForQuestion removes model-added advice when the operator asked
// for a fact. It never edits the source evidence or authorizes an action.
func sourceAnswerForQuestion(question, answer string) string {
	query := normalizedIntentText(question)
	if containsAny(query, "proxim", "prioridad", "accion", "paso", "tarea", "pendiente", "recomienda", "plan") {
		return answer
	}
	lines := strings.Split(answer, "\n")
	kept := make([]string, 0, len(lines))
	suppress := false
	for _, line := range lines {
		heading := normalizedIntentText(strings.Trim(strings.TrimSpace(line), "*# :\t"))
		if strings.HasPrefix(heading, "fuente") || strings.HasPrefix(heading, "referencia") {
			suppress = false
		}
		if strings.HasPrefix(heading, "proximo paso") || strings.HasPrefix(heading, "siguiente paso") || strings.HasPrefix(heading, "prioridades") || strings.HasPrefix(heading, "accion sugerida") || strings.HasPrefix(heading, "recomendacion") {
			suppress = true
			continue
		}
		if !suppress || answerLineRelevant(query, line) {
			kept = append(kept, line)
		}
	}
	return strings.TrimSpace(strings.Join(kept, "\n"))
}

func answerLineRelevant(query, line string) bool {
	stop := map[string]bool{"para": true, "sobre": true, "esta": true, "este": true, "como": true, "cuando": true, "quien": true, "que": true, "una": true, "uno": true, "del": true, "las": true, "los": true, "por": true, "con": true, "segun": true, "reunion": true, "reuniones": true, "acuerdo": true, "acordo": true, "decidio": true, "decidimos": true, "dijo": true, "hizo": true, "hablo": true}
	queryTerms := map[string]bool{}
	for _, term := range strings.Fields(query) {
		if len(term) >= 4 && !stop[term] {
			queryTerms[term] = true
		}
	}
	for _, term := range strings.Fields(normalizedIntentText(line)) {
		if len(term) >= 4 && !stop[term] && queryTerms[term] {
			return true
		}
	}
	return false
}
