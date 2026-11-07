package secretary

import (
	"fmt"
	"hash/fnv"
	"strings"
	"sync/atomic"
	"time"
)

// Donna's character in code. The model gets the same character through the
// shared instructions; this file gives it a voice and judgement in the parts of
// an answer that are built deterministically, so it is consistent even when a
// small model writes flat prose.
//
// Character: perceptive, loyal and dry. She states a view when the data
// supports one, protects Fran from what could bite (overdue, blocked), keeps
// wit for low-stakes moments, and never grovels. Gravity beats wit: nothing
// urgent, overdue or failing ever gets a joke.

var characterOn atomic.Bool

func init() { characterOn.Store(true) }

// SetCharacter turns the character on (default) or off for neutral wording.
func SetCharacter(enabled bool) { characterOn.Store(enabled) }

// ParseCharacterMode maps LAB_SECRETARY_PERSONA to the on/off switch. Anything
// other than "neutral" keeps the character.
func ParseCharacterMode(value string) bool {
	return strings.ToLower(strings.TrimSpace(value)) != "neutral"
}

// pickVariant chooses one option, stable for the same text within a day so
// wording varies over time without flickering inside a conversation.
func pickVariant(seed string, now time.Time, options ...string) string {
	h := fnv.New32a()
	h.Write([]byte(seed + now.Format("2006-01-02")))
	return options[int(h.Sum32())%len(options)]
}

type ackKind int

const (
	ackThanks ackKind = iota
	ackConfirm
	ackFarewell
)

// ackReply is the one-line answer to a courtesy. Wit is allowed here because
// nothing is at stake.
func ackReply(kind ackKind, seed string, now time.Time) string {
	if !characterOn.Load() {
		switch kind {
		case ackThanks:
			return pickVariant(seed, now, "De nada.", "Para eso estoy.", "A ti.")
		case ackFarewell:
			return pickVariant(seed, now, "Hasta luego.", "¡Hasta pronto!")
		default:
			return pickVariant(seed, now, "Perfecto.", "Hecho.", "Vale.")
		}
	}
	switch kind {
	case ackThanks:
		return pickVariant(seed, now, "Faltaría más.", "Para eso estoy.", "Anotado en tu cuenta de favores.", "Sabía que lo agradecerías.")
	case ackFarewell:
		return pickVariant(seed, now, "Hasta luego. Yo vigilo.", "Aquí estaré.", "Hasta pronto. No incendies nada.")
	default:
		return pickVariant(seed, now, "Hecho.", "Dicho y hecho.", "Perfecto.", "Cuenta con ello.")
	}
}

// clarifyQuestion asks for the missing source in Donna's voice: brief, with the
// concrete options, and a nudge instead of a shrug.
func clarifyQuestion(seed string, now time.Time) string {
	if !characterOn.Load() {
		return "¿Qué fuente o información quieres que consulte?"
	}
	return pickVariant(seed, now,
		"Eso me lo tienes que afinar: ¿miro Jira, Granola o las dos?",
		"Tal cual no sé por dónde tirar. ¿Jira, Granola o las dos?")
}

var spanishMonths = []string{"enero", "febrero", "marzo", "abril", "mayo", "junio", "julio", "agosto", "septiembre", "octubre", "noviembre", "diciembre"}

// spanishDay renders 2026-11-01 as "1 de noviembre"; unknown formats are
// returned unchanged.
func spanishDay(iso string) string {
	t, err := time.Parse("2006-01-02", iso)
	if err != nil {
		return iso
	}
	return fmt.Sprintf("%d de %s", t.Day(), spanishMonths[t.Month()-1])
}

// recommendation is the one thing Donna would do first, only when the data
// establishes it: the issue that has been overdue longest, otherwise the first
// blocked one, otherwise whatever falls due soonest. With no such evidence she
// says nothing rather than inventing a priority.
func recommendation(issues []jiraIssue, now time.Time) string {
	if !characterOn.Load() {
		return ""
	}
	today := now.Format("2006-01-02")
	soonLimit := now.AddDate(0, 0, 2).Format("2006-01-02")
	var oldest, soonest *jiraIssue
	var blocked *jiraIssue
	for i := range issues {
		issue := &issues[i]
		if classifyJiraIssue(*issue) == bucketDone {
			continue
		}
		due := issue.Fields.Due
		switch {
		case due != "" && due < today:
			if oldest == nil || due < oldest.Fields.Due {
				oldest = issue
			}
		case due != "" && due <= soonLimit:
			if soonest == nil || due < soonest.Fields.Due {
				soonest = issue
			}
		}
		if blocked == nil && classifyJiraIssue(*issue) == bucketBlocked {
			blocked = issue
		}
	}
	switch {
	case oldest != nil:
		return fmt.Sprintf("Yo empezaría por %s: lleva vencida desde el %s.", oldest.Key, spanishDay(oldest.Fields.Due))
	case blocked != nil:
		return fmt.Sprintf("Yo desbloquearía antes %s: sin eso lo demás espera.", blocked.Key)
	case soonest != nil:
		return fmt.Sprintf("Lo próximo en caer es %s, el %s.", soonest.Key, spanishDay(soonest.Fields.Due))
	}
	return ""
}

var statusOpening = []string{"como va ", "como van ", "como vamos ", "como andamos ", "como anda ", "en que estado esta ", "en que punto esta ", "que tal va ", "que tal van "}
var statusDeterminers = []string{"el ", "la ", "los ", "las ", "lo ", "esto", "eso", "mi ", "mis ", "nuestro ", "nuestra "}

// statusQuestionWithoutSource recognises "¿cómo va el informe?"-style questions
// that name no person, issue key or meeting. Without a source the model can
// only produce filler, so Donna says plainly what she lacks and asks where to
// look.
func statusQuestionWithoutSource(text string) bool {
	normalized := normalizedIntentText(text)
	for _, filler := range []string{"dime ", "oye ", "y ", "oye donna "} {
		normalized = strings.TrimPrefix(normalized, filler)
	}
	if countWords(normalized) > 12 || jiraTaskIntent(text) || containsAny(normalized, "reunion", "granola", "meeting", "llamada") {
		return false
	}
	for _, opening := range statusOpening {
		if !strings.HasPrefix(normalized, opening) {
			continue
		}
		rest := strings.TrimPrefix(normalized, opening)
		for _, determiner := range statusDeterminers {
			if strings.HasPrefix(rest, determiner) {
				return true
			}
		}
	}
	return false
}

// noDataAnswer is the reply to such a question: what she lacks, then one
// concrete question.
func noDataAnswer(seed string, now time.Time) string {
	if !characterOn.Load() {
		return "No tengo información sobre eso. ¿Consulto Jira o Granola?"
	}
	return pickVariant(seed, now,
		"De eso no tengo nada delante. ¿Lo miro en Jira o en Granola?",
		"Ahí me falta información. ¿Miro Jira, Granola o las dos?")
}
