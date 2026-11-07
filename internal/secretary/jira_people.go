package secretary

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// Recognize explicit responsibility requests before the generic full-text search.
// Keep names separate: a shared ten-issue limit can hide an entire person's work.
var jiraPeoplePatterns = []*regexp.Regexp{
	// "cómo van Valentina y Gonzalo": only capitalised proper names, so that
	// "cómo va el proyecto" is never mistaken for a person.
	regexp.MustCompile(`\b(?:[Cc]ómo|[Cc]omo)\s+(?:va|van)\s+(\p{Lu}[\p{L}'-]*(?:\s+(?:y|e)\s+\p{Lu}[\p{L}'-]*)*)\s*[?!.]*\s*$`),
	regexp.MustCompile(`(?i)\b(?:como|cómo)\s+van?\s+(.+?)\s+con\s+sus\s+tareas\b`),
	regexp.MustCompile(`(?i)\b(?:qué|que)\s+(?:tareas|pendientes|incidencias)\s+(?:tiene|tienen)\s+([^?!.;]+?)\s+(?:en curso|en proceso|en marcha|en el proyecto|en proyecto|del proyecto)\b`),
	regexp.MustCompile(`(?i)\b(?:tareas|pendientes|incidencias)\s+(?:de|asignadas a|asignados a)\s+([^?!.;]+)`),
	regexp.MustCompile(`(?i)\b(?:en qué|en que)\s+(?:está|esta)\s+trabajando(?:\s+hoy)?\s+([^?!.;]+)`),
}
var jiraPersonName = regexp.MustCompile(`^[\p{L}][\p{L} .'-]{1,79}$`)
var jiraProjectInText = regexp.MustCompile(`(?i)\bproyecto\s+([A-Z][A-Z0-9]{1,15})\b`)

type jiraKnowledgeContextKey struct{}

func withJiraKnowledge(ctx context.Context, items []KnowledgeItem) context.Context {
	return context.WithValue(ctx, jiraKnowledgeContextKey{}, items)
}

func approvedJiraPerson(query string, items []KnowledgeItem) (jiraPerson, bool) {
	resolution := ResolveEntity(query, EntityPerson, items)
	if resolution.Status != EntityResolved || resolution.Item == nil {
		return jiraPerson{}, false
	}
	item := resolution.Item
	accountID := ""
	for provider, externalID := range item.ExternalRefs {
		if strings.EqualFold(provider, "jira") || strings.EqualFold(provider, "atlassian") {
			accountID = strings.TrimSpace(externalID)
			break
		}
	}
	if accountID == "" {
		accountID = strings.TrimSpace(item.Value)
	}
	if !jiraAccountID.MatchString(accountID) {
		return jiraPerson{}, false
	}
	return jiraPerson{AccountID: accountID, DisplayName: item.Subject, Active: true}, true
}

func jiraPeople(query string) []string {
	if len([]rune(query)) > 2000 || jiraIssueKeyMention.MatchString(strings.ToUpper(query)) || containsAny(normalizedIntentText(query), "mis tareas", "mis pendientes", "mi tarea") {
		return nil
	}
	for _, pattern := range jiraPeoplePatterns {
		match := pattern.FindStringSubmatch(query)
		if len(match) == 0 {
			continue
		}
		parts := regexp.MustCompile(`(?i)\s+y\s+|,`).Split(match[1], -1)
		if len(parts) > 4 {
			return nil
		}
		var names []string
		for _, part := range parts {
			name := strings.Trim(strings.TrimSpace(part), " ?!.,;:")
			if !jiraPersonName.MatchString(name) || regexp.MustCompile(`^(mi|mis|mí|mías|mios|todos|todas|equipo|hoy|mañana|manana|esta semana)$`).MatchString(strings.ToLower(name)) {
				return nil
			}
			names = append(names, name)
		}
		return names
	}
	return nil
}

func jiraProjectScope(query string) string {
	if issue := jiraIssueKeyMention.FindString(strings.ToUpper(query)); issue != "" {
		return strings.SplitN(issue, "-", 2)[0]
	}
	if match := jiraProjectInText.FindStringSubmatch(query); len(match) == 2 {
		return strings.ToUpper(match[1])
	}
	return ""
}

func defaultJiraProjectForPerson(name string) string {
	// The operator's saved scope for Valentina is the PMI project.
	switch normalizedIntentText(name) {
	case "valentina", "valentina lagos":
		return "PMI"
	default:
		return ""
	}
}

type jiraPerson struct {
	AccountID   string `json:"accountId"`
	DisplayName string `json:"displayName"`
	Active      bool   `json:"active"`
}

func (c *Connectors) resolveJiraPerson(ctx context.Context, name string) (person jiraPerson, returnErr error) {
	done := conversationStep(ctx, "jira.person")
	defer func() { done(returnErr) }()
	if !regexp.MustCompile(`^[A-Za-z0-9-]{1,100}$`).MatchString(c.Config.Jira.CloudID) {
		return jiraPerson{}, errors.New("Jira cloud_id no válido")
	}
	base := "https://api.atlassian.com"
	if c.allowTestHTTP {
		u, err := url.Parse(c.jiraAPIBase)
		if err == nil && u.Scheme == "http" && (u.Hostname() == "127.0.0.1" || u.Hostname() == "localhost") {
			base = strings.TrimRight(c.jiraAPIBase, "/")
		}
	}
	endpoint := base + "/ex/jira/" + url.PathEscape(c.Config.Jira.CloudID) + "/rest/api/3/user/search?" + url.Values{"query": {name}, "maxResults": {"1000"}}.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return jiraPerson{}, err
	}
	req.Header.Set("Authorization", "Bearer "+c.Config.Jira.AccessToken)
	req.Header.Set("Accept", "application/json")
	client := c.Client
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	resp, err := conversationHTTP(ctx, client, req, "jira.person")
	if err != nil {
		return jiraPerson{}, errors.New("Jira búsqueda de personas no disponible")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return jiraPerson{}, fmt.Errorf("Jira búsqueda de personas HTTP %d", resp.StatusCode)
	}
	var users []jiraPerson
	if json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(&users) != nil {
		return jiraPerson{}, errors.New("Jira respuesta de personas no válida")
	}
	if len(users) >= 1000 {
		return jiraPerson{}, errors.New("Jira: demasiadas coincidencias; indica el nombre completo")
	}
	var matches []jiraPerson
	for _, user := range users {
		if user.Active && jiraAccountID.MatchString(user.AccountID) {
			matches = append(matches, user)
		}
	}
	if len(matches) != 1 {
		return jiraPerson{}, fmt.Errorf("Jira: no puedo identificar de forma unívoca a %s (%d coincidencias activas); indica su nombre completo", name, len(matches))
	}
	return matches[0], nil
}

func (c *Connectors) jiraPeopleResult(ctx context.Context, query string, names []string) (CapabilityResult, error) {
	var people []jiraPerson
	knowledge, _ := ctx.Value(jiraKnowledgeContextKey{}).([]KnowledgeItem)
	for _, name := range names {
		person, mapped := approvedJiraPerson(name, knowledge)
		if !mapped {
			var err error
			person, err = c.resolveJiraPerson(ctx, name)
			if err != nil {
				return CapabilityResult{}, err
			}
		}
		people = append(people, person)
	}
	result := CapabilityResult{DirectAnswer: true, ResultCount: len(people)}
	normalized := normalizedIntentText(query)
	currentWork := containsAny(normalized, "en curso", "en proceso", "en marcha")
	includeDone := !containsAny(normalized, "pendientes", "en curso", "en proceso", "en marcha")
	heading := "Esto es lo que sigue abierto en Jira:"
	if currentWork {
		heading = "Jira: tareas en curso"
		if project := jiraProjectScope(query); project != "" {
			heading += " en " + project
		}
		heading += ":"
	} else if includeDone {
		heading = "La foto útil de Jira, separando lo abierto de lo cerrado recientemente:"
	}
	blocks := []string{heading}
	var compact []string
	for i, person := range people {
		baseJQL := `assignee = "` + person.AccountID + `"`
		if project := jiraProjectScope(query); project != "" {
			baseJQL += ` AND project = "` + project + `"`
		}
		openFilter := `statusCategory != Done`
		if containsAny(normalized, "en curso", "en proceso", "en marcha") {
			openFilter = `status = "In Progress"`
		}
		open, err := c.queryJiraPage(ctx, baseJQL+" AND "+openFilter+" ORDER BY updated DESC", 100)
		if err != nil {
			return CapabilityResult{}, err
		}
		var done jiraSearchResponse
		if includeDone {
			done, err = c.queryJiraPage(ctx, baseJQL+" AND statusCategory = Done AND statusCategoryChangedDate >= -14d ORDER BY updated DESC", 100)
			if err != nil {
				return CapabilityResult{}, err
			}
		}
		if open.Partial || done.Partial {
			result.Partial = true
		}
		for _, issue := range append(open.Issues, done.Issues...) {
			result.Evidence = append(result.Evidence, EvidenceRef{Provider: "Jira", ID: issue.Key, Title: issue.Fields.Summary, RetrievedAt: time.Now()})
		}
		label := jiraPersonLabel(names[i], person.DisplayName)
		if currentWork && len(open.Issues) == 0 {
			blocks = append(blocks, "**"+short(label)+"**: no veo tareas con estado En curso.")
			compact = append(compact, short(label)+": no veo tareas con estado En curso.")
		} else {
			blocks = append(blocks, summarizeJiraPerson(label, open, done, includeDone))
			compact = append(compact, compactJiraPerson(label, open, done, includeDone, time.Now()))
		}
	}
	if len(result.Evidence) > 0 {
		blocks = append(blocks, "Fuente: Jira. El estado visible refleja el registro, no confirma el progreso real.")
	}
	if result.Partial {
		blocks = append(blocks, "Aviso: Jira no devolvió todas las páginas; esta lista es parcial.")
	}
	result.Text = strings.Join(blocks, "\n")
	result.Compact = strings.Join(compact, "\n") + "\n¿Detallo alguna?"
	if result.Partial {
		result.Compact += " (Jira no devolvió todas las páginas; las cifras son mínimos.)"
	}
	return result, nil
}

func jiraPersonLabel(requested, display string) string {
	display = strings.TrimSpace(display)
	if display == "" || (!strings.Contains(display, " ") && containsAny(display, ".", "_", "@")) {
		return requested
	}
	return display
}

func jiraWorkState(issue jiraIssue) string {
	state := normalizedIntentText(issue.Fields.Status.Name)
	switch {
	case containsAny(state, "bloquead", "blocked", "impedid"):
		return "Bloqueada"
	case containsAny(state, "sin planificar", "no planificad", "unplanned"):
		return "Sin planificar"
	case issue.Fields.Status.Category.Key == "done" || containsAny(state, "hecho", "cerrad", "finalizad", "done", "resolved"):
		return "Terminadas"
	case containsAny(state, "waiting", "en espera", "espera"):
		return "En espera"
	case containsAny(state, "en curso", "en proceso", "in progress", "desarrollo"):
		return "En curso"
	case issue.Fields.Status.Category.Key == "new" || containsAny(state, "por hacer", "pendiente", "to do", "backlog"):
		return "Por hacer"
	default:
		return "Estado no clasificado"
	}
}

func summarizeJiraPerson(name string, open, done jiraSearchResponse, includeDone bool) string {
	if len(open.Issues) == 0 && len(done.Issues) == 0 {
		return "**" + short(name) + "**: Jira no devolvió incidencias para esta consulta."
	}
	order := []string{"Bloqueada", "Sin planificar", "En curso", "En espera", "Por hacer", "Estado no clasificado"}
	counts := map[string]int{}
	var blocked, unplanned, overdue, upcoming, stale, inProgress, waiting, todo []jiraIssue
	now := time.Now()
	today := now.Format("2006-01-02")
	soon := now.AddDate(0, 0, 2).Format("2006-01-02")
	staleBefore := now.AddDate(0, 0, -7).Format("2006-01-02")
	missingDue := 0
	for _, issue := range open.Issues {
		state := jiraWorkState(issue)
		if state == "Terminadas" {
			continue
		}
		counts[state]++
		switch state {
		case "Bloqueada":
			blocked = append(blocked, issue)
		case "Sin planificar":
			unplanned = append(unplanned, issue)
		case "En curso":
			inProgress = append(inProgress, issue)
		case "En espera":
			waiting = append(waiting, issue)
		case "Por hacer":
			todo = append(todo, issue)
		}
		if len(issue.Fields.Updated) >= 10 && issue.Fields.Updated[:10] < staleBefore && state == "En curso" {
			stale = append(stale, issue)
		}
		if issue.Fields.Due == "" {
			missingDue++
		} else {
			if issue.Fields.Due < today {
				overdue = append(overdue, issue)
			} else if issue.Fields.Due <= soon {
				upcoming = append(upcoming, issue)
			}
		}
	}
	var stateCounts []string
	for _, state := range order {
		if counts[state] > 0 {
			stateCounts = append(stateCounts, fmt.Sprintf("%d %s", counts[state], strings.ToLower(state)))
		}
	}
	openCount := len(open.Issues)
	openLabel := fmt.Sprintf("%d abiertas", openCount)
	if openCount == 0 {
		openLabel = "ninguna tarea abierta"
	} else if openCount == 1 {
		openLabel = "1 abierta"
	}
	if open.NextPageToken != "" {
		openLabel = "al menos " + openLabel
	}
	first := "**" + short(name) + "**: " + openLabel
	if len(stateCounts) > 0 {
		first += " (" + strings.Join(stateCounts, ", ") + ")"
	}
	if includeDone {
		recentCount := len(done.Issues)
		if done.NextPageToken != "" {
			first += fmt.Sprintf("; al menos %d pasaron a Done en los últimos 14 días", recentCount)
		} else if recentCount == 1 {
			first += "; 1 pasó a Done en los últimos 14 días"
		} else if recentCount == 0 {
			first += "; sin pasos a Done registrados en los últimos 14 días"
		} else {
			first += fmt.Sprintf("; %d pasaron a Done en los últimos 14 días", recentCount)
		}
	}
	lines := []string{first + "."}
	if open.NextPageToken != "" || done.NextPageToken != "" {
		lines = append(lines, "Muestra incompleta: Jira tiene más resultados; esos números son mínimos.")
	}
	if len(overdue) > 0 {
		lines = append(lines, fmt.Sprintf("Hay %d vencidas: %s.", len(overdue), jiraIssueKeys(overdue, 3)))
	}
	if len(upcoming) > 0 {
		lines = append(lines, fmt.Sprintf("%s vence en los próximos dos días.", jiraIssueKeys(upcoming, 2)))
	}
	var reading string
	if len(blocked) > 0 {
		reading = "Primero aclararía el bloqueo explícito de " + jiraIssueBrief(blocked[0]) + "."
	} else if len(overdue) > 0 {
		reading = "Lo urgente es revisar " + jiraIssueBrief(overdue[0]) + ": ya venció."
	} else if len(upcoming) > 0 {
		reading = "Confirmaría la entrega de " + jiraIssueBrief(upcoming[0]) + " antes de su vencimiento."
	} else if len(unplanned) > 0 {
		reading = "Antes de darla por encaminada, concretaría el plan de " + jiraIssueBrief(unplanned[0]) + "."
	} else if len(waiting) > 0 && len(inProgress) == 0 {
		reading = "No la contaría como trabajo en curso: " + jiraIssueBrief(waiting[0]) + " sigue en espera."
	} else if len(todo) > len(inProgress) {
		reading = "La cola por hacer supera al trabajo en curso; concretaría cuáles son las dos siguientes prioridades."
	} else if len(stale) > 0 {
		reading = "Pediría una actualización de " + jiraIssueBrief(stale[0]) + "; Jira no registra cambios desde " + stale[0].Fields.Updated[:10] + "."
	} else if len(inProgress) >= 5 {
		reading = "Hay varios frentes en marcha; aclararía cuáles son las dos prioridades reales."
	} else if openCount > 0 {
		reading = "Los estados muestran actividad, pero por sí solos no dicen si el trabajo avanza según lo previsto."
	}
	if reading != "" {
		lines = append(lines, reading)
	}
	if openCount > 0 {
		if examples := jiraOpenExamples(blocked, overdue, upcoming, stale, inProgress, waiting, todo, open.Issues); len(examples) > 0 {
			lines = append(lines, "Para ponerle nombre: "+strings.Join(examples, "; ")+".")
		}
		if missingDue == openCount {
			lines = append(lines, "Las abiertas no tienen fecha de entrega en Jira; no puedo decir si van en plazo.")
		} else if missingDue > openCount/2 {
			lines = append(lines, fmt.Sprintf("%d de %d abiertas no tienen fecha de entrega en Jira; el plazo queda poco claro.", missingDue, openCount))
		}
	}
	return strings.Join(lines, " ")
}

func jiraIssueBrief(issue jiraIssue) string {
	return "[" + issue.Key + "] " + short(issue.Fields.Summary)
}

func jiraOpenExamples(groups ...[]jiraIssue) []string {
	seen := map[string]bool{}
	var examples []string
	for _, group := range groups {
		for _, issue := range group {
			if seen[issue.Key] {
				continue
			}
			seen[issue.Key] = true
			examples = append(examples, jiraIssueBrief(issue)+" ("+strings.ToLower(jiraWorkState(issue))+")")
			break
		}
		if len(examples) == 2 {
			break
		}
	}
	return examples
}

func jiraIssueKeys(issues []jiraIssue, limit int) string {
	keys := make([]string, 0, limit)
	for _, issue := range issues {
		if len(keys) == limit {
			break
		}
		keys = append(keys, issue.Key)
	}
	return strings.Join(keys, ", ")
}

// compactJiraPerson is the short status line for one person: open work by
// state, what stands out, and how much closed recently.
func compactJiraPerson(name string, open, done jiraSearchResponse, includeDone bool, now time.Time) string {
	if len(open.Issues) == 0 && len(done.Issues) == 0 {
		return short(name) + ": Jira no devolvió incidencias."
	}
	line := name + ": sin tareas abiertas."
	if len(open.Issues) > 0 {
		line = compactJiraBody(open.Issues, now, short(name)+" tiene", open.NextPageToken != "")
	}
	if includeDone {
		switch n := len(done.Issues); {
		case n == 0:
			line += " Sin cierres en 14 días."
		case done.NextPageToken != "":
			line += fmt.Sprintf(" Al menos %d cerradas en 14 días.", n)
		default:
			line += fmt.Sprintf(" %d %s en 14 días.", n, plural(n, "cerrada", "cerradas"))
		}
	}
	return line
}
