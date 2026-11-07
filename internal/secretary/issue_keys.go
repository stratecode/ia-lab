package secretary

import (
	"regexp"
	"strconv"
	"strings"
)

// DefaultIssuePrefixes are the Jira project keys of the operator's sites. They can
// be overridden with LAB_SECRETARY_JIRA_KEYS.
var DefaultIssuePrefixes = []string{"PMI", "STRAT", "COC", "SW"}

// issuePrefixAliases are the ways a speech recogniser tends to write a project
// key. Aliases are matched on accent-free lowercase words.
var issuePrefixAliases = map[string]string{
	"estrat":   "STRAT",
	"es strat": "STRAT",
	"pe eme i": "PMI",
	"peemei":   "PMI",
}

// NormalizeIssueKeys rewrites how people say or type an issue key into the key
// itself, so the Jira routing recognises it: "STRAT 166", "estrat ciento sesenta y
// seis", "PMI guion 67" and "pe eme i sesenta y siete" all become STRAT-166 or
// PMI-67. Only the configured project prefixes are touched.
func NormalizeIssueKeys(text string, prefixes []string) string {
	if len(prefixes) == 0 || strings.TrimSpace(text) == "" {
		return text
	}
	known := map[string]string{}
	for _, prefix := range prefixes {
		prefix = strings.ToUpper(strings.TrimSpace(prefix))
		if prefix != "" {
			known[strings.ToLower(prefix)] = prefix
		}
	}
	for alias, prefix := range issuePrefixAliases {
		if _, ok := known[strings.ToLower(prefix)]; ok {
			known[alias] = prefix
		}
	}
	if len(known) == 0 {
		return text
	}
	tokens := tokenizeKeepingSpaces(text)
	var out strings.Builder
	for i := 0; i < len(tokens); {
		prefix, used := matchPrefix(tokens, i, known)
		if used == 0 {
			out.WriteString(tokens[i].raw)
			i++
			continue
		}
		j := i + used
		// Optional "guion", "-" or a dash glued to the prefix.
		if j < len(tokens) && (tokens[j].word == "guion" || tokens[j].word == "-" || tokens[j].word == "guion medio") {
			j++
		}
		number, consumed := parseIssueNumber(tokens, j)
		if consumed == 0 {
			out.WriteString(tokens[i].raw)
			i++
			continue
		}
		out.WriteString(tokens[i].lead + prefix + "-" + strconv.Itoa(number) + tokens[j+consumed-1].trail)
		i = j + consumed
	}
	return out.String()
}

type speechToken struct {
	raw, lead, word, trail string
}

var tokenPattern = regexp.MustCompile(`(\s*)([^\s]+)`)

// tokenizeKeepingSpaces splits text into words that remember their leading space
// and trailing punctuation, so the text can be rebuilt without losing either.
func tokenizeKeepingSpaces(text string) []speechToken {
	var tokens []speechToken
	for _, match := range tokenPattern.FindAllStringSubmatch(text, -1) {
		spaceBefore, body := match[1], match[2]
		core := strings.TrimRight(body, ".,;:!?)»\"")
		trail := body[len(core):]
		lead := spaceBefore
		for len(core) > 0 && strings.ContainsRune("(¿¡«\"", rune(core[0])) {
			lead += core[:1]
			core = core[1:]
		}
		tokens = append(tokens, speechToken{raw: match[0], lead: lead, word: normalizedIntentText(core), trail: trail})
	}
	return tokens
}

func matchPrefix(tokens []speechToken, i int, known map[string]string) (string, int) {
	// Longest alias first: "pe eme i" is three words, "es strat" two.
	for length := 3; length >= 1; length-- {
		if i+length > len(tokens) {
			continue
		}
		parts := make([]string, 0, length)
		for _, token := range tokens[i : i+length] {
			parts = append(parts, token.word)
		}
		if prefix, ok := known[strings.Join(parts, " ")]; ok {
			return prefix, length
		}
	}
	// "PMI-67" already glued: nothing to do, the router handles it.
	return "", 0
}

var (
	spokenUnits    = map[string]int{"cero": 0, "uno": 1, "un": 1, "una": 1, "dos": 2, "tres": 3, "cuatro": 4, "cinco": 5, "seis": 6, "siete": 7, "ocho": 8, "nueve": 9, "diez": 10, "once": 11, "doce": 12, "trece": 13, "catorce": 14, "quince": 15, "dieciseis": 16, "diecisiete": 17, "dieciocho": 18, "diecinueve": 19, "veinte": 20, "veintiuno": 21, "veintidos": 22, "veintitres": 23, "veinticuatro": 24, "veinticinco": 25, "veintiseis": 26, "veintisiete": 27, "veintiocho": 28, "veintinueve": 29}
	spokenTens     = map[string]int{"treinta": 30, "cuarenta": 40, "cincuenta": 50, "sesenta": 60, "setenta": 70, "ochenta": 80, "noventa": 90}
	spokenHundreds = map[string]int{"cien": 100, "ciento": 100, "doscientos": 200, "trescientos": 300, "cuatrocientos": 400, "quinientos": 500, "seiscientos": 600, "setecientos": 700, "ochocientos": 800, "novecientos": 900}
)

var issueDigits = regexp.MustCompile(`^\d{1,5}$`)

// parseIssueNumber reads digits ("166"), digits split by Whisper ("60 y 7" is read as 67)
// or Spanish number words ("ciento sesenta y seis") starting at tokens[start].
func parseIssueNumber(tokens []speechToken, start int) (int, int) {
	if start >= len(tokens) {
		return 0, 0
	}
	if issueDigits.MatchString(tokens[start].word) {
		value, _ := strconv.Atoi(tokens[start].word)
		// "60 y 7": tens followed by "y" and a single digit.
		if value >= 20 && value%10 == 0 && value < 100 && start+2 < len(tokens) && tokens[start+1].word == "y" && issueDigits.MatchString(tokens[start+2].word) {
			if unit, _ := strconv.Atoi(tokens[start+2].word); unit >= 1 && unit <= 9 {
				return value + unit, 3
			}
		}
		return value, 1
	}
	total, current, consumed, sawNumber := 0, 0, 0, false
	for i := start; i < len(tokens); i++ {
		word := tokens[i].word
		switch {
		case word == "y" && sawNumber && i+1 < len(tokens) && isSpokenUnit(tokens[i+1].word) && current%100 >= 30 && current%10 == 0:
			consumed++
			continue
		case spokenHundreds[word] > 0:
			current += spokenHundreds[word]
		case spokenTens[word] > 0:
			current += spokenTens[word]
		default:
			if value, ok := spokenUnits[word]; ok {
				current += value
			} else if word == "mil" {
				if current == 0 {
					current = 1
				}
				total += current * 1000
				current = 0
			} else {
				i = len(tokens)
				continue
			}
		}
		sawNumber = true
		consumed++
	}
	// A trailing "y" that was consumed without its unit does not belong to the key.
	for consumed > 0 && tokens[start+consumed-1].word == "y" {
		consumed--
	}
	if !sawNumber || consumed == 0 {
		return 0, 0
	}
	return total + current, consumed
}

func isSpokenUnit(word string) bool {
	value, ok := spokenUnits[word]
	return ok && value >= 1 && value <= 9
}
