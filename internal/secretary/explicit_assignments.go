package secretary

import (
	"regexp"
	"strings"
)

var (
	explicitActionSection = regexp.MustCompile(`(?i)^\s*(?:#{1,6}\s*)?(?:pr[oó]ximos\s+pasos|next\s+steps|action\s+items)\s*:?\s*$`)
	explicitOwnerSection  = regexp.MustCompile(`(?i)^\s*#{1,6}\s*(?:proyectos?|projects?|tareas?|acciones?)\s+(?:de\s+)?([^:#]+?)\s*:?\s*$`)
	explicitActionHeading = regexp.MustCompile(`^\s*[-*]\s+(.+?)\s*\(([^()]+)\)\s*[.!;:]?\s*$`)
	explicitOwnerPrefix   = regexp.MustCompile(`^\s*[-*]\s+([^:]{2,64}):\s+(.+?)\s*$`)
	projectOwnerPrefix    = regexp.MustCompile(`^\s*[-*]\s+([^:(]+?)(?:\s+\([^)]*\))?:\s+`)
	markdownHeading       = regexp.MustCompile(`^\s*#{1,6}\s+`)
	markdownBullet        = regexp.MustCompile(`^\s*[-*]\s+`)
	explicitDueDate       = regexp.MustCompile(`(?i)\b(?:para|antes\s+(?:de|del)|hasta|durante)\s+(?:el\s+)?(?:lunes|martes|mi[eé]rcoles|jueves|viernes|s[aá]bado|domingo|hoy|mañana|la\s+mañana|la\s+tarde|el\s+\d{1,2}(?:\s+de\s+[[:alpha:]áéíóú]+)?|\d{1,2}[/-]\d{1,2}(?:[/-]\d{2,4})?)\b`)
)

var explicitQueryStopWords = map[string]struct{}{
	"que": {}, "quien": {}, "quienes": {}, "cual": {}, "cuales": {}, "como": {},
	"tarea": {}, "tareas": {}, "pendiente": {}, "pendientes": {}, "responsable": {}, "responsables": {},
	"enero": {}, "febrero": {}, "marzo": {}, "abril": {}, "mayo": {}, "junio": {},
	"julio": {}, "agosto": {}, "septiembre": {}, "octubre": {}, "noviembre": {}, "diciembre": {},
	"plazo": {}, "plazos": {}, "fecha": {}, "fechas": {}, "aparece": {}, "aparecen": {}, "mencionaron": {},
	"asignada": {}, "asignado": {}, "asignadas": {}, "asignados": {}, "compromiso": {}, "compromisos": {},
	"acordado": {}, "acordados": {}, "acordada": {}, "acordadas": {}, "acordaron": {}, "agrupada": {}, "agrupadas": {}, "agrupado": {}, "agrupados": {}, "separado": {}, "separada": {}, "separados": {}, "separadas": {},
	"enviar": {}, "envie": {}, "envia": {}, "entregar": {}, "revisar": {}, "revision": {}, "hacer": {},
	"debia": {}, "debe": {}, "deben": {}, "deberia": {}, "deberian": {}, "debo": {}, "tenia": {}, "tiene": {},
	"segun": {}, "reunion": {}, "reuniones": {}, "hoy": {}, "ayer": {}, "esta": {}, "manana": {}, "para": {},
	"por": {}, "con": {}, "las": {}, "los": {}, "una": {}, "del": {}, "desde": {}, "de": {}, "el": {},
	"la": {}, "y": {}, "o": {}, "a": {}, "me": {}, "mi": {}, "mis": {}, "yo": {}, "lo": {}, "dejar": {},
}

type explicitAction struct {
	title   string
	owners  []string
	details []string
	dueDate string
}

func explicitAssignmentAnswer(question, source string, evidence []EvidenceRef) (string, bool) {
	query := normalizedIntentText(question)
	if !containsAny(query, "tarea", "pendiente", "responsable", "asignad", "quien", "compromiso", "enviar", "entregar", "subir", "sube", "subio", "revis", "hacer", "debia", "debe") {
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
		if strings.Trim(word, "0123456789") == "" {
			continue
		}
		if _, isOwner := ownerWords[word]; isOwner {
			continue
		}
		if len(word) >= 4 {
			queryWords = append(queryWords, word)
		}
	}
	type scoredAction struct {
		action explicitAction
		score  int
	}
	var selected []explicitAction
	var scored []scoredAction
	bestScore := 0
	var ownerFallback []explicitAction
	for _, action := range actions {
		ownerRequested := false
		for _, owner := range action.owners {
			if owner != "" && strings.Contains(query, normalizedIntentText(owner)) {
				ownerRequested = true
				break
			}
		}
		matchScore := 0
		text := normalizedIntentText(action.title)
		for _, word := range queryWords {
			if strings.Contains(text, word) {
				matchScore++
			}
		}
		if hasOwnerFilter && len(queryWords) == 0 {
			if ownerRequested {
				selected = append(selected, action)
			}
		} else if hasOwnerFilter {
			if ownerRequested {
				ownerFallback = append(ownerFallback, action)
			}
		}
		if matchScore > 0 {
			if matchScore > bestScore {
				bestScore = matchScore
				scored = scored[:0]
			}
			if matchScore == bestScore {
				scored = append(scored, scoredAction{action: action, score: matchScore})
			}
		}
	}
	if bestScore > 0 {
		for _, candidate := range scored {
			if candidate.score == bestScore {
				selected = append(selected, candidate.action)
			}
		}
	} else if !hasOwnerFilter && len(queryWords) == 0 {
		selected = actions
	}
	if len(selected) == 0 && hasOwnerFilter {
		selected = ownerFallback
	}
	if len(selected) == 0 {
		return "", false
	}

	lines := make([]string, 0, len(selected))
	for _, action := range selected {
		owner := strings.Join(action.owners, ", ")
		if owner == "" {
			owner = "Sin responsable explícito"
		}
		line := "- " + owner + ": " + action.title
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
	projectOwners := map[string]string{}
	for _, line := range strings.Split(source, "\n") {
		trimmed := strings.TrimSpace(line)
		if match := explicitOwnerSection.FindStringSubmatch(trimmed); len(match) == 2 {
			inActionSection = false
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
		if !inActionSection && sectionOwner != "" && markdownBullet.MatchString(trimmed) {
			if match := projectOwnerPrefix.FindStringSubmatch(trimmed); len(match) == 2 {
				projectOwners[normalizedIntentText(strings.TrimSpace(match[1]))] = sectionOwner
			}
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
		indent := len(line) - len(strings.TrimLeft(line, " \t"))
		if match := explicitActionHeading.FindStringSubmatch(trimmed); len(match) == 3 {
			title := strings.Trim(strings.TrimSpace(match[1]), "*")
			owners := splitExplicitOwners(match[2])
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
			if indent > 0 {
				if current >= 0 {
					actions[current].details = append(actions[current].details, strings.Trim(markdownBullet.ReplaceAllString(trimmed, ""), "*"))
				}
				continue
			}
			title := strings.Trim(markdownBullet.ReplaceAllString(trimmed, ""), "*")
			owners := ownersForProject(title, projectOwners)
			actions = append(actions, newExplicitAction(title, owners))
			current = len(actions) - 1
			continue
		}
		if current >= 0 {
			actions[current].details = append(actions[current].details, trimmed)
		}
	}
	return actions
}

func ownersForProject(action string, projectOwners map[string]string) []string {
	normalized := normalizedIntentText(action)
	var owners []string
	seen := map[string]bool{}
	for project, owner := range projectOwners {
		if project != "" && strings.Contains(normalized, project) && owner != "" && !seen[owner] {
			owners = append(owners, owner)
			seen[owner] = true
		}
	}
	return owners
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
