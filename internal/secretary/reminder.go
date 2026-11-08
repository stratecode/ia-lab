package secretary

import (
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// A reminder is a relative-time request in natural Spanish: "avísame en 10
// minutos para que revise la comida". It is handled in code, before the model,
// so Donna can never answer "entendido" without a timer actually existing.

type reminderStatus int

const (
	// reminderNone: the text is not a reminder request.
	reminderNone reminderStatus = iota
	// reminderOK: duration (and maybe a reason) were understood.
	reminderOK
	// reminderUnclear: it is a reminder request Donna cannot schedule as stated
	// (no relative duration, or outside the allowed range).
	reminderUnclear
)

type reminderRequest struct {
	Duration time.Duration
	Reason   string
}

const (
	minReminderDuration = time.Second
	maxReminderDuration = 24 * time.Hour
	maxReminderReason   = 120
)

// reminderIntentPhrases are accent-free word sequences that mean "tell me
// something later". They are all first-person: "me avisó Gonzalo" is not one.
var reminderIntentPhrases = [][]string{
	{"me", "puedes", "avisar"}, {"me", "podrias", "avisar"}, {"puedes", "avisarme"}, {"podrias", "avisarme"},
	{"me", "avisas"}, {"me", "avisaras"}, {"avisame"}, {"avisarme"},
	{"me", "puedes", "recordar"}, {"me", "podrias", "recordar"}, {"puedes", "recordarme"}, {"me", "recuerdas"},
	{"recuerdame"}, {"recordarme"}, {"me", "despiertas"}, {"despiertame"},
	{"ponme", "un", "aviso"}, {"ponme", "un", "recordatorio"}, {"ponme", "una", "alarma"}, {"ponme", "un", "temporizador"},
	{"pon", "un", "aviso"}, {"pon", "un", "recordatorio"}, {"pon", "un", "temporizador"}, {"pon", "una", "alarma"},
	{"temporizador"}, {"quiero", "un", "aviso"}, {"necesito", "un", "aviso"},
}

func init() {
	// "me pones / me puedes poner / ponerme / puedes ponerme" + a reminder noun.
	nouns := [][]string{{"un", "aviso"}, {"un", "recordatorio"}, {"una", "alarma"}, {"un", "temporizador"}, {"un", "cronometro"}}
	leads := [][]string{{"me", "pones"}, {"me", "puedes", "poner"}, {"me", "podrias", "poner"}, {"puedes", "ponerme"}, {"podrias", "ponerme"}, {"ponerme"}, {"me", "pondrias"}, {"me", "programas"}, {"me", "puedes", "programar"}, {"programame"}}
	for _, lead := range leads {
		for _, noun := range nouns {
			phrase := append(append([]string{}, lead...), noun...)
			reminderIntentPhrases = append(reminderIntentPhrases, phrase)
		}
	}
}

var glued = regexp.MustCompile(`^(\d{1,5})(s|seg|segs|m|min|mins|h|hr|hrs)$`)

func durationUnit(word string) (time.Duration, bool) {
	switch word {
	case "s", "seg", "segs", "segundo", "segundos":
		return time.Second, true
	case "m", "min", "mins", "minuto", "minutos":
		return time.Minute, true
	case "h", "hr", "hrs", "hora", "horas":
		return time.Hour, true
	}
	return 0, false
}

func wordsAt(tokens []speechToken, i int, words ...string) bool {
	if i < 0 || i+len(words) > len(tokens) {
		return false
	}
	for k, w := range words {
		if tokens[i+k].word != w {
			return false
		}
	}
	return true
}

// parseDurationPart reads one piece of a duration at tokens[i]: "10 minutos",
// "10min", "un cuarto de hora", "tres cuartos de hora", "media hora" or a bare
// "media" (as in "una hora y media").
func parseDurationPart(tokens []speechToken, i int, afterY bool) (time.Duration, int, bool) {
	switch {
	case wordsAt(tokens, i, "media", "hora"):
		return 30 * time.Minute, 2, true
	case wordsAt(tokens, i, "tres", "cuartos", "de", "hora"):
		return 45 * time.Minute, 4, true
	case wordsAt(tokens, i, "un", "cuarto", "de", "hora"):
		return 15 * time.Minute, 4, true
	case wordsAt(tokens, i, "cuarto", "de", "hora"):
		return 15 * time.Minute, 3, true
	case afterY && wordsAt(tokens, i, "media"):
		return 30 * time.Minute, 1, true
	}
	if i >= len(tokens) {
		return 0, 0, false
	}
	if m := glued.FindStringSubmatch(tokens[i].word); m != nil {
		n, _ := strconv.Atoi(m[1])
		unit, _ := durationUnit(m[2])
		return time.Duration(n) * unit, 1, true
	}
	n, consumed := parseIssueNumber(tokens, i)
	if consumed == 0 || i+consumed >= len(tokens) {
		return 0, 0, false
	}
	unit, ok := durationUnit(tokens[i+consumed].word)
	if !ok {
		return 0, 0, false
	}
	return time.Duration(n) * unit, consumed + 1, true
}

// parseDurationAt reads a possibly compound duration ("una hora y media",
// "1 hora y 15 minutos") and returns how many tokens it used.
func parseDurationAt(tokens []speechToken, i int) (time.Duration, int, bool) {
	total, pos, found := time.Duration(0), i, false
	afterY := false
	for pos < len(tokens) {
		d, n, ok := parseDurationPart(tokens, pos, afterY)
		if !ok {
			break
		}
		total, pos, found = total+d, pos+n, true
		if pos+1 < len(tokens) && tokens[pos].word == "y" {
			if _, _, next := parseDurationPart(tokens, pos+1, true); next {
				pos++
				afterY = true
				continue
			}
		}
		break
	}
	return total, pos - i, found
}

func findIntent(tokens []speechToken) (start, end int, ok bool) {
	for i := range tokens {
		for _, phrase := range reminderIntentPhrases {
			if wordsAt(tokens, i, phrase...) {
				return i, i + len(phrase), true
			}
		}
	}
	return 0, 0, false
}

var reasonConnectors = map[string]bool{
	"para": true, "que": true, "porque": true, "de": true, "por": true, "y": true, "a": true, "favor": true,
	"yo": true, "me": true, "aviso": true, "avisar": true, "recordar": true, "recordatorio": true, "temporizador": true,
	"un": true, "una": true, "el": false,
}

var prepositionTail = map[string]bool{"en": true, "dentro": true, "de": true, "unos": true, "unas": true, "como": true, "aproximadamente": true, "por": true, "favor": true}

// parseReminderRequest understands "avísame en 10 minutos para que revise la
// comida", "recuérdame revisar el horno en media hora", "ponme un temporizador de
// 5 minutos para la pasta". The reason, when present, is returned as the text the
// reminder should say.
func parseReminderRequest(text string) (reminderRequest, reminderStatus) {
	tokens := tokenizeKeepingSpaces(text)
	_, intentEnd, ok := findIntent(tokens)
	if !ok {
		return reminderRequest{}, reminderNone
	}
	// Prefer a duration after the request words; fall back to one anywhere.
	durStart, durLen, found := -1, 0, false
	var duration time.Duration
	for _, from := range []int{intentEnd, 0} {
		for i := from; i < len(tokens) && !found; i++ {
			if d, n, ok := parseDurationAt(tokens, i); ok {
				duration, durStart, durLen, found = d, i, n, true
			}
		}
	}
	if !found || duration < minReminderDuration || duration > maxReminderDuration {
		return reminderRequest{}, reminderUnclear
	}
	return reminderRequest{Duration: duration, Reason: reminderReason(tokens, intentEnd, durStart, durLen)}, reminderOK
}

// reminderReason takes what follows the duration ("para que revise la comida"),
// or, failing that, what sits between the request and the duration ("recuérdame
// revisar el horno en media hora").
func reminderReason(tokens []speechToken, intentEnd, durStart, durLen int) string {
	afterStart := durStart + durLen
	if intentEnd > afterStart {
		afterStart = intentEnd
	}
	reason, subjunctive := trimReasonTokens(tokens[afterStart:])
	if len(reason) == 0 && durStart > intentEnd {
		reason, subjunctive = trimReasonTokens(dropTail(tokens[intentEnd:durStart]))
	}
	if len(reason) == 0 {
		return ""
	}
	return formatReminderReason(reason, subjunctive)
}

func dropTail(tokens []speechToken) []speechToken {
	for len(tokens) > 0 && prepositionTail[tokens[len(tokens)-1].word] {
		tokens = tokens[:len(tokens)-1]
	}
	return tokens
}

// trimReasonTokens drops leading connectors ("para que", "de", "por favor"...)
// and reports whether the reason started with "que"/"para que", meaning its
// first verb is in the subjunctive and needs turning into an imperative.
func trimReasonTokens(tokens []speechToken) ([]speechToken, bool) {
	subjunctive := false
	for len(tokens) > 0 {
		w := tokens[0].word
		if w == "que" || (w == "para" && len(tokens) > 1 && tokens[1].word == "que") {
			subjunctive = true
		}
		if !reasonConnectors[w] {
			break
		}
		tokens = tokens[1:]
	}
	return tokens, subjunctive
}

var irregularImperative = map[string]string{
	"tenga": "ten", "haga": "haz", "vaya": "ve", "ponga": "pon", "salga": "sal", "venga": "ven", "diga": "di", "sea": "sé",
}

var personSwap = map[string]string{
	"tengo": "tienes", "mi": "tu", "mis": "tus", "me": "te", "estoy": "estás", "yo": "tú", "mio": "tuyo", "mia": "tuya",
}

// formatReminderReason turns the user's wording into what Donna says when the
// time is up: a short imperative in the second person, without inventing facts.
func formatReminderReason(tokens []speechToken, subjunctive bool) string {
	words := make([]string, 0, len(tokens))
	for i, t := range tokens {
		core := strings.Trim(strings.TrimSpace(t.raw), ".,;:!?)»\"¿¡«(")
		if core == "" {
			continue
		}
		word := core
		switch {
		case i == 0 && subjunctive:
			lower := strings.ToLower(core)
			if swapped, ok := personSwap[t.word]; ok {
				word = swapped
			} else if irregular, ok := irregularImperative[t.word]; ok {
				word = irregular
			} else if strings.HasSuffix(lower, "gue") {
				word = lower[:len(lower)-3] + "ga"
			} else if strings.HasSuffix(lower, "que") {
				word = lower[:len(lower)-3] + "ca"
			} else if strings.HasSuffix(lower, "ce") {
				word = lower[:len(lower)-2] + "za"
			} else if strings.HasSuffix(lower, "e") {
				word = lower[:len(lower)-1] + "a"
			} else if strings.HasSuffix(lower, "a") {
				word = lower[:len(lower)-1] + "e"
			} else {
				word = lower
			}
		default:
			if swapped, ok := personSwap[t.word]; ok {
				word = swapped
			}
		}
		words = append(words, word)
	}
	reason := strings.Join(words, " ")
	if runes := []rune(reason); len(runes) > maxReminderReason {
		reason = strings.TrimSpace(string(runes[:maxReminderReason]))
		if cut := strings.LastIndex(reason, " "); cut > 0 {
			reason = reason[:cut]
		}
	}
	if reason == "" {
		return ""
	}
	first := []rune(reason)
	first[0] = unicode.ToUpper(first[0])
	return string(first)
}

// reminderSetText confirms a reminder that really exists.
func reminderSetText(id string, duration time.Duration, reason string) string {
	text := "⏱ Hecho. Te aviso en " + formatTimerDuration(duration)
	if reason != "" {
		text += ": " + reason
	}
	return text + " (" + id + ")."
}

// reminderDraftText asks for confirmation. On Telegram a button does it; elsewhere
// the operator types the command. Nothing is scheduled until then.
func reminderDraftText(id string, duration time.Duration, reason, channel string) string {
	what := "¿Te aviso en " + formatTimerDuration(duration)
	if reason != "" {
		what += ": «" + reason + "»"
	}
	what += "?"
	if channel == "telegram" {
		return "⏱ " + what + " Pulsa Confirmar (o escribe /confirmar-aviso " + id + ") y lo programo; si no, no se programa nada."
	}
	return "⏱ " + what + " Confirma con /confirmar-aviso " + id + "; si no, no se programa nada. El aviso te llegará por Telegram."
}

const reminderUnclearText = "No he programado nada. Solo sé avisarte pasado un tiempo (de 1 segundo a 24 horas): por ejemplo «avísame en 20 minutos para sacar la comida». No puedo avisar a una hora concreta ni cuando ocurra algo."
