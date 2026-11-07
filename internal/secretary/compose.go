package secretary

import (
	"fmt"
	"strings"
	"time"
	"unicode"
)

// This file is Donna's presentation layer. Facts are computed here, in code,
// from structured data; the model is only used for language. Everything is
// deterministic so it can be tested against fixed expectations.

type jiraBucket int

const (
	bucketBlocked jiraBucket = iota
	bucketInProgress
	bucketWaiting
	bucketTodo
	bucketBacklog
	bucketDone
)

func classifyJiraIssue(issue jiraIssue) jiraBucket {
	name := normalizedIntentText(issue.Fields.Status.Name)
	switch {
	case strings.Contains(name, "block") || strings.Contains(name, "bloque"):
		return bucketBlocked
	case strings.Contains(name, "waiting") || strings.Contains(name, "espera"):
		return bucketWaiting
	case strings.Contains(name, "backlog"):
		return bucketBacklog
	}
	switch issue.Fields.Status.Category.Key {
	case "done":
		return bucketDone
	case "indeterminate":
		return bucketInProgress
	}
	return bucketTodo
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// shortTitle trims a title to max runes at a word boundary.
func shortTitle(title string, max int) string {
	title = strings.Join(strings.Fields(title), " ")
	runes := []rune(title)
	if len(runes) <= max {
		return title
	}
	cut := string(runes[:max])
	if i := strings.LastIndex(cut, " "); i > max/2 {
		cut = cut[:i]
	}
	return strings.TrimRight(cut, " ,.;:-") + "…"
}

func joinSpanish(items []string) string {
	switch len(items) {
	case 0:
		return ""
	case 1:
		return items[0]
	}
	return strings.Join(items[:len(items)-1], ", ") + " y " + items[len(items)-1]
}

// compactJiraSummary turns a list of issues into at most three short lines:
// how many there are by state, what deserves attention, and an offer to expand.
// subject is "Tienes" for the operator's own work and "Hay" otherwise. sampleFull
// is true when the query hit its page limit, so the count is a lower bound.
func compactJiraSummary(issues []jiraIssue, now time.Time, subject string, sampleFull bool) string {
	body := compactJiraBody(issues, now, subject, sampleFull)
	if body == "" || body == "No hay incidencias abiertas." {
		return body
	}
	return body + " ¿Detallo alguna?"
}

// compactJiraBody is the summary without the closing offer, so several bodies
// (one per person) can share a single offer.
func compactJiraBody(issues []jiraIssue, now time.Time, subject string, sampleFull bool) string {
	if len(issues) == 0 {
		return ""
	}
	counts := map[jiraBucket]int{}
	var blocked, inProgress, overdue, soon []jiraIssue
	today := now.Format("2006-01-02")
	soonLimit := now.AddDate(0, 0, 2).Format("2006-01-02")
	withDue := 0
	for _, issue := range issues {
		bucket := classifyJiraIssue(issue)
		if bucket == bucketDone {
			continue
		}
		counts[bucket]++
		switch bucket {
		case bucketBlocked:
			blocked = append(blocked, issue)
		case bucketInProgress:
			inProgress = append(inProgress, issue)
		}
		if due := issue.Fields.Due; due != "" {
			withDue++
			if due < today {
				overdue = append(overdue, issue)
			} else if due <= soonLimit {
				soon = append(soon, issue)
			}
		}
	}
	open := 0
	for _, n := range counts {
		open += n
	}
	if open == 0 {
		return "No hay incidencias abiertas."
	}
	var parts []string
	if n := counts[bucketInProgress]; n > 0 {
		parts = append(parts, fmt.Sprintf("%d en curso", n))
	}
	if n := counts[bucketBlocked]; n > 0 {
		parts = append(parts, fmt.Sprintf("%d %s", n, plural(n, "bloqueada", "bloqueadas")))
	}
	if n := counts[bucketWaiting]; n > 0 {
		parts = append(parts, fmt.Sprintf("%d en espera", n))
	}
	if n := counts[bucketTodo] + counts[bucketBacklog]; n > 0 {
		parts = append(parts, fmt.Sprintf("%d por hacer", n))
	}
	lead := fmt.Sprintf("%s %d %s", subject, open, plural(open, "abierta", "abiertas"))
	if sampleFull {
		lead = fmt.Sprintf("%s al menos %d abiertas", subject, open)
	}
	lines := []string{lead + ": " + joinSpanish(parts) + "."}

	seen := map[string]bool{}
	var notable []string
	add := func(list []jiraIssue) {
		for _, issue := range list {
			if len(notable) >= 3 || seen[issue.Key] || classifyJiraIssue(issue) == bucketDone {
				continue
			}
			seen[issue.Key] = true
			notable = append(notable, fmt.Sprintf("%s (%s)", issue.Key, shortTitle(issue.Fields.Summary, 50)))
		}
	}
	add(overdue)
	add(soon)
	add(blocked)
	add(inProgress)
	if open <= 3 {
		add(issues)
	}
	if len(notable) > 0 {
		lines = append(lines, "Destacan "+joinSpanish(notable)+".")
	}
	switch {
	case len(overdue) > 0:
		lines[len(lines)-1] += fmt.Sprintf(" %d %s.", len(overdue), plural(len(overdue), "vencida", "vencidas"))
	case len(soon) > 0:
		lines[len(lines)-1] += fmt.Sprintf(" %d %s en dos días.", len(soon), plural(len(soon), "vence", "vencen"))
	case withDue == 0:
		lines[len(lines)-1] += " Ninguna tiene fecha límite."
	}
	return strings.Join(lines, " ")
}

// wantsFullListing reports whether the operator asked for the complete list
// instead of the usual short summary.
func wantsFullListing(question string) bool {
	return containsAny(normalizedIntentText(question), "detalle", "detalla", "todas", "todos", "lista", "listado", "completo", "completa", "enumera")
}

var conversationalAckWords = map[string]bool{
	"gracias": true, "muchas": true, "vale": true, "ok": true, "okey": true, "perfecto": true, "genial": true,
	"entendido": true, "listo": true,
	"adios": true, "hasta": true, "luego": true, "donna": true, "bien": true, "estupendo": true, "guay": true, "de": true, "nada": true,
}

// conversationalAck answers a message that is only a courtesy or a
// confirmation. It returns false for anything that could carry a request.
func conversationalAck(text string) (string, bool) {
	normalized := normalizedIntentText(text)
	words := strings.Fields(normalized)
	if len(words) == 0 || len(words) > 4 {
		return "", false
	}
	for _, word := range words {
		if !conversationalAckWords[word] {
			return "", false
		}
	}
	has := func(w string) bool {
		for _, word := range words {
			if word == w {
				return true
			}
		}
		return false
	}
	pick := func(options ...string) string { return options[len(normalized)%len(options)] }
	switch {
	case has("gracias"):
		return pick("De nada.", "Para eso estoy.", "A ti."), true
	case has("adios") || has("hasta"):
		return pick("Hasta luego.", "¡Hasta pronto!", "Aquí estaré."), true
	default:
		return pick("Perfecto.", "Hecho.", "Vale."), true
	}
}

// fillerMarkers identify closing sentences that add nothing. They are matched
// on accent-free lowercase text.
var fillerMarkers = []string{
	"que informacion o accion necesitas",
	"estoy lista para seguir",
	"aclara lo que necesitas",
	"si necesitas aclarar algun punto",
	"si necesitas mas detalles",
	"necesitas mas detalles sobre algun aspecto",
	"podrias confirmar si necesitas",
	"avisame para profundizar",
	"no dudes en avisarme",
}

// polishAnswer drops generic filler sentences from a model answer. It never
// removes the whole answer: if every sentence is filler the original is kept.
func polishAnswer(answer string) string {
	trimmed := strings.TrimSpace(answer)
	if trimmed == "" {
		return answer
	}
	var kept []string
	removed := false
	for _, line := range strings.Split(trimmed, "\n") {
		var sentences []string
		for _, sentence := range splitSentences(line) {
			if isFillerSentence(sentence) {
				removed = true
				continue
			}
			sentences = append(sentences, sentence)
		}
		if joined := strings.TrimSpace(strings.Join(sentences, " ")); joined != "" {
			kept = append(kept, joined)
		} else if strings.TrimSpace(line) == "" {
			kept = append(kept, "")
		}
	}
	result := strings.TrimSpace(strings.Join(kept, "\n"))
	if !removed || result == "" {
		return answer
	}
	return result
}

func isFillerSentence(sentence string) bool {
	normalized := normalizedIntentText(sentence)
	for _, marker := range fillerMarkers {
		if strings.Contains(normalized, marker) {
			return true
		}
	}
	return false
}

func splitSentences(line string) []string {
	var sentences []string
	var current strings.Builder
	runes := []rune(line)
	for i, r := range runes {
		current.WriteRune(r)
		if r == '.' || r == '?' || r == '!' {
			next := i + 1
			if next >= len(runes) || unicode.IsSpace(runes[next]) {
				sentences = append(sentences, strings.TrimSpace(current.String()))
				current.Reset()
			}
		}
	}
	if rest := strings.TrimSpace(current.String()); rest != "" {
		sentences = append(sentences, rest)
	}
	return sentences
}

func countWords(text string) int { return len(strings.Fields(text)) }

// answerWordLimit is the length contract for a model-written answer. Short by
// default; more room when the operator asks for explanation or the full list.
func answerWordLimit(question string, hasSources bool) int {
	normalized := normalizedIntentText(question)
	switch {
	case wantsFullListing(question), containsAny(normalized, "explica", "explicame", "analiza", "analisis", "desglosa", "paso a paso", "en detalle"):
		return 250
	case hasSources:
		return 90
	default:
		return 70
	}
}

// trimToSentences keeps whole sentences up to maxWords, and always at least
// the first one, so a trimmed answer never ends mid-thought.
func trimToSentences(text string, maxWords int) string {
	if countWords(text) <= maxWords {
		return text
	}
	var kept []string
	words := 0
	for _, line := range strings.Split(strings.TrimSpace(text), "\n") {
		for _, sentence := range splitSentences(line) {
			n := countWords(sentence)
			if len(kept) > 0 && words+n > maxWords {
				return strings.Join(kept, " ")
			}
			kept = append(kept, sentence)
			words += n
		}
	}
	return strings.Join(kept, " ")
}
