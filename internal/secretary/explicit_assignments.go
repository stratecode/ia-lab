package secretary

import (
	"regexp"
	"strings"
)

var (
	explicitActionSection = regexp.MustCompile(`(?i)^\s*(?:#{1,6}\s*)?(?:pr[oó]ximos\s+pasos|next\s+steps|action\s+items)\s*:?\s*$`)
	explicitOwnerSection  = regexp.MustCompile(`(?i)^\s*#{1,6}\s*(?:proyectos?|projects?|tareas?|acciones?)\s+(?:de\s+)?([^:#]+?)\s*:?\s*$`)
	explicitActionHeading = regexp.MustCompile(`^\s*[-*]\s+(.+?)\s*\(([^()]+)\)\s*$`)
	explicitOwnerPrefix   = regexp.MustCompile(`^\s*[-*]\s+([^:]{2,64}):\s+(.+?)\s*$`)
	markdownHeading       = regexp.MustCompile(`^\s*#{1,6}\s+`)
	markdownBullet        = regexp.MustCompile(`^\s*[-*]\s+`)
	explicitDueDate       = regexp.MustCompile(`(?i)\b(?:para|antes\s+(?:de|del)|hasta|durante)\s+(?:el\s+)?(?:lunes|martes|mi[eé]rcoles|jueves|viernes|s[aá]bado|domingo|hoy|mañana|la\s+mañana|la\s+tarde|el\s+\d{1,2}(?:\s+de\s+[[:alpha:]áéíóú]+)?|\d{1,2}[/-]\d{1,2}(?:[/-]\d{2,4})?)\b`)
)

var explicitQueryStopWords = map[string]struct{}{
	"que": {}, "quien": {}, "quienes": {}, "cual": {}, "cuales": {}, "como": {},
	"tarea": {}, "tareas": {}, "pendiente": {}, "pendientes": {}, "responsable": {}, "responsables": {},
	"asignada": {}, "asignado": {}, "asignadas": {}, "asignados": {}, "compromiso": {}, "compromisos": {},
	"acordado": {}, "acordados": {}, "acordada": {}, "acordadas": {}, "acordaron": {}, "separado": {}, "separada": {}, "separados": {}, "separadas": {},
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
	dueDate string
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
	hasOwnerFilter := false
	for owner := range ownerWords {
		if owner != "" && strings.Contains(query, owner) {
			hasOwnerFilter = true
			break
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
		if ownerRequested || termMatched || len(queryWords) == 0 && !hasOwnerFilter {
			selected = append(selected, action)
		}
	}
	if len(selected) == 0 {
		return "", false
	}

	lines := make([]string, 0, len(selected))
	for _, action := range selected {
		line := "- " + strings.Join(action.owners, ", ") + ": " + action.title
		if action.dueDate != "" && !strings.Contains(normalizedIntentText(action.title), normalizedIntentText(action.dueDate)) {
			line += " (plazo: " + action.dueDate + ")"
		}
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
	sectionOwner := ""
	for _, line := range strings.Split(source, "\n") {
		trimmed := strings.TrimSpace(line)
		if match := explicitOwnerSection.FindStringSubmatch(trimmed); len(match) == 2 {
			inActionSection = true
			sectionOwner = strings.TrimSpace(match[1])
			current = -1
			continue
		}
		if explicitActionSection.MatchString(trimmed) {
			inActionSection = true
			sectionOwner = ""
			current = -1
			continue
		}
		if markdownHeading.MatchString(trimmed) {
			inActionSection = false
			sectionOwner = ""
			current = -1
			continue
		}
		if !inActionSection && markdownBullet.MatchString(trimmed) {
			if match := explicitOwnerPrefix.FindStringSubmatch(trimmed); len(match) == 3 {
				title := strings.Trim(strings.TrimSpace(match[2]), "*")
				actions = append(actions, newExplicitAction(title, splitExplicitOwners(match[1])))
			}
			continue
		}
		if !inActionSection || trimmed == "" {
			continue
		}
		if match := explicitActionHeading.FindStringSubmatch(trimmed); len(match) == 3 {
			title := strings.Trim(strings.TrimSpace(match[1]), "*")
			owners := splitExplicitOwners(match[2])
			if len(owners) == 0 && sectionOwner != "" {
				owners = []string{sectionOwner}
			}
			if len(owners) == 0 {
				current = -1
				continue
			}
			actions = append(actions, newExplicitAction(title, owners))
			current = len(actions) - 1
			continue
		}
		if markdownBullet.MatchString(trimmed) {
			if match := explicitOwnerPrefix.FindStringSubmatch(trimmed); len(match) == 3 {
				title := strings.Trim(strings.TrimSpace(match[2]), "*")
				actions = append(actions, newExplicitAction(title, splitExplicitOwners(match[1])))
				current = len(actions) - 1
				continue
			}
			title := strings.Trim(markdownBullet.ReplaceAllString(trimmed, ""), "*")
			if sectionOwner != "" && title != "" {
				actions = append(actions, newExplicitAction(title, []string{sectionOwner}))
				current = len(actions) - 1
				continue
			}
			current = -1
			continue
		}
		if current >= 0 {
			actions[current].details = append(actions[current].details, trimmed)
		}
	}
	return actions
}

func newExplicitAction(title string, owners []string) explicitAction {
	action := explicitAction{title: title, owners: owners}
	if due := explicitDueDate.FindString(title); due != "" {
		action.dueDate = due
	}
	return action
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
