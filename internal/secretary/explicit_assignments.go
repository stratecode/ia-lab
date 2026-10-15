package secretary

import (
	"regexp"
	"strings"
)

var (
	explicitActionSection = regexp.MustCompile(`(?i)^\s*(?:#{1,6}\s*)?(?:pr[oó]ximos\s+pasos|next\s+steps|action\s+items)\s*:?\s*$`)
	explicitActionHeading = regexp.MustCompile(`^\s*[-*]\s+(.+?)\s*\(([^()]+)\)\s*$`)
	markdownHeading       = regexp.MustCompile(`^\s*#{1,6}\s+`)
	markdownBullet        = regexp.MustCompile(`^\s*[-*]\s+`)
)

var explicitQueryStopWords = map[string]struct{}{
	"que": {}, "quien": {}, "quienes": {}, "cual": {}, "cuales": {}, "como": {},
	"tarea": {}, "tareas": {}, "pendiente": {}, "pendientes": {}, "responsable": {}, "responsables": {},
	"asignada": {}, "asignado": {}, "asignadas": {}, "asignados": {}, "compromiso": {}, "compromisos": {},
	"enviar": {}, "envie": {}, "envia": {}, "entregar": {}, "revisar": {}, "revision": {}, "hacer": {},
	"debia": {}, "debe": {}, "deben": {}, "deberia": {}, "deberian": {}, "debo": {}, "tenia": {}, "tiene": {},
	"segun": {}, "reunion": {}, "reuniones": {}, "hoy": {}, "ayer": {}, "esta": {}, "manana": {}, "para": {},
	"por": {}, "con": {}, "las": {}, "los": {}, "una": {}, "del": {}, "desde": {}, "de": {}, "el": {},
	"la": {}, "y": {}, "o": {}, "a": {}, "me": {}, "mi": {}, "mis": {}, "yo": {}, "lo": {}, "dejar": {},
	"web": {}, "lista": {},
}

type explicitAction struct {
	title   string
	owners  []string
	details []string
}

func explicitAssignmentAnswer(question, source string, evidence []EvidenceRef) (string, bool) {
	query := normalizedIntentText(question)
	if !containsAny(query, "tarea", "pendiente", "responsable", "asignad", "quien", "compromiso", "enviar", "entregar", "revis", "hacer", "debia", "debe") {
		return "", false
	}
	actions := parseExplicitActions(source)
	if len(actions) == 0 {
		return "", false
	}

	queryWords := make([]string, 0)
	ownerWords := make(map[string]struct{})
	for _, action := range actions {
		for _, owner := range action.owners {
			ownerWords[normalizedIntentText(owner)] = struct{}{}
		}
	}
	for _, word := range strings.Fields(query) {
		if _, stopWord := explicitQueryStopWords[word]; stopWord {
			continue
		}
		if _, isOwner := ownerWords[word]; isOwner {
			continue
		}
		if len(word) >= 4 {
			queryWords = append(queryWords, word)
		}
	}
	var selected []explicitAction
	for _, action := range actions {
		ownerRequested := false
		for _, owner := range action.owners {
			if owner != "" && strings.Contains(query, normalizedIntentText(owner)) {
				ownerRequested = true
				break
			}
		}
		termMatched := false
		text := normalizedIntentText(action.title)
		for _, word := range queryWords {
			if strings.Contains(text, word) {
				termMatched = true
				break
			}
		}
		if len(queryWords) == 0 || ownerRequested || termMatched {
			selected = append(selected, action)
		}
	}
	if len(selected) == 0 {
		return "", false
	}

	lines := make([]string, 0, len(selected))
	for _, action := range selected {
		line := "- " + strings.Join(action.owners, ", ") + ": " + action.title
		if len(action.details) > 0 {
			line += " — " + strings.Join(action.details, " ")
		}
		lines = append(lines, line)
	}
	answer := strings.Join(lines, "\n")
	for _, ref := range evidence {
		if ref.Provider != "" && validEvidenceURL(ref.URL) {
			answer += "\n\nFuente: [nota de " + ref.Provider + "](" + ref.URL + ")"
			break
		}
	}
	return answer, true
}

func parseExplicitActions(source string) []explicitAction {
	var actions []explicitAction
	current := -1
	inActionSection := false
	for _, line := range strings.Split(source, "\n") {
		trimmed := strings.TrimSpace(line)
		if explicitActionSection.MatchString(trimmed) {
			inActionSection = true
			current = -1
			continue
		}
		if markdownHeading.MatchString(trimmed) {
			inActionSection = false
			current = -1
			continue
		}
		if !inActionSection || trimmed == "" {
			continue
		}
		if match := explicitActionHeading.FindStringSubmatch(trimmed); len(match) == 3 {
			title := strings.Trim(strings.TrimSpace(match[1]), "*")
			owners := splitExplicitOwners(match[2])
			if len(owners) == 0 {
				current = -1
				continue
			}
			actions = append(actions, explicitAction{title: title, owners: owners})
			current = len(actions) - 1
			continue
		}
		if markdownBullet.MatchString(trimmed) {
			current = -1
			continue
		}
		if current >= 0 {
			actions[current].details = append(actions[current].details, trimmed)
		}
	}
	return actions
}

func splitExplicitOwners(value string) []string {
	value = strings.ReplaceAll(value, " y ", ",")
	value = strings.ReplaceAll(value, " and ", ",")
	parts := strings.Split(value, ",")
	owners := make([]string, 0, len(parts))
	for _, part := range parts {
		if owner := strings.TrimSpace(part); owner != "" {
			owners = append(owners, owner)
		}
	}
	return owners
}
