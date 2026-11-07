package secretary

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

// SpokenText rewrites an answer so a speech synthesiser reads it the way a
// person would say it: no Markdown, no URLs or emojis, and numbers, dates, issue
// keys and percentages spelled out in Spanish. It is deterministic and never
// invents content.
func SpokenText(text string) string {
	text = stripMarkdownForSpeech(text)
	text = redactedForSpeech.ReplaceAllString(text, "dato oculto")
	text = urlForSpeech.ReplaceAllString(text, "un enlace")
	text = removeSymbolsForSpeech(text)
	text = thousandsForSpeech.ReplaceAllStringFunc(text, func(m string) string { return strings.ReplaceAll(m, ".", "") })
	text = isoDateForSpeech.ReplaceAllStringFunc(text, spokenISODate)
	text = jiraKeyForSpeech.ReplaceAllStringFunc(text, spokenIssueKey)
	text = timeForSpeech.ReplaceAllStringFunc(text, spokenTime)
	text = percentForSpeech.ReplaceAllStringFunc(text, spokenPercent)
	text = euroForSpeech.ReplaceAllStringFunc(text, spokenEuros)
	text = decimalForSpeech.ReplaceAllStringFunc(text, spokenDecimal)
	text = spellNumbersInContext(text)
	text = strings.NewReplacer("&", " y ", " / ", " o ").Replace(text)
	return tidySpeech(text)
}

var (
	redactedForSpeech   = regexp.MustCompile(`\[REDACTED:[a-z_]+\]`)
	urlForSpeech        = regexp.MustCompile(`https?://\S+`)
	isoDateForSpeech    = regexp.MustCompile(`\b(\d{4})-(\d{2})-(\d{2})\b`)
	jiraKeyForSpeech    = regexp.MustCompile(`\b([A-Z]{2,10})-(\d{1,5})\b`)
	timeForSpeech       = regexp.MustCompile(`(?:\b[Ll]as\s+)?\b([01]?\d|2[0-3]):([0-5]\d)\b`)
	percentForSpeech    = regexp.MustCompile(`(\d+(?:[.,]\d+)?)\s?%`)
	euroForSpeech       = regexp.MustCompile(`(\d+(?:[.,]\d+)?)\s?€`)
	decimalForSpeech    = regexp.MustCompile(`\b\d+[.,]\d+\b`)
	thousandsForSpeech  = regexp.MustCompile(`\b\d{1,3}(?:\.\d{3})+\b`)
	listMarkerForSpeech = regexp.MustCompile(`(?m)^\s*(?:[-*•]|\d+[.)])\s+`)
	headingForSpeech    = regexp.MustCompile(`(?m)^\s{0,3}#{1,6}\s*`)
)

var spanishMonthsSpoken = []string{"enero", "febrero", "marzo", "abril", "mayo", "junio", "julio", "agosto", "septiembre", "octubre", "noviembre", "diciembre"}

var spanishLetterNames = map[rune]string{
	'A': "a", 'B': "be", 'C': "ce", 'D': "de", 'E': "e", 'F': "efe", 'G': "ge", 'H': "hache", 'I': "i", 'J': "jota",
	'K': "ka", 'L': "ele", 'M': "eme", 'N': "ene", 'O': "o", 'P': "pe", 'Q': "cu", 'R': "erre", 'S': "ese", 'T': "te",
	'U': "u", 'V': "uve", 'W': "uve doble", 'X': "equis", 'Y': "ye", 'Z': "zeta",
}

func stripMarkdownForSpeech(text string) string {
	text = headingForSpeech.ReplaceAllString(text, "")
	text = listMarkerForSpeech.ReplaceAllString(text, "")
	text = strings.NewReplacer("**", "", "__", "", "`", "", "*", "", "~~", "").Replace(text)
	// [label](url) keeps only the label.
	text = regexp.MustCompile(`\[([^\]]+)\]\([^)]*\)`).ReplaceAllString(text, "$1")
	text = strings.ReplaceAll(text, "\r", "")
	text = regexp.MustCompile(`\n{2,}`).ReplaceAllString(text, ". ")
	text = strings.ReplaceAll(text, "\n", ". ")
	return text
}

func removeSymbolsForSpeech(text string) string {
	var b strings.Builder
	for _, r := range text {
		switch {
		case unicode.Is(unicode.So, r), unicode.Is(unicode.Sk, r), r == '‍', r == '️', r >= 0x1F000:
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func spokenISODate(match string) string {
	parts := isoDateForSpeech.FindStringSubmatch(match)
	year, _ := strconv.Atoi(parts[1])
	month, _ := strconv.Atoi(parts[2])
	day, _ := strconv.Atoi(parts[3])
	if month < 1 || month > 12 || day < 1 || day > 31 {
		return match
	}
	return fmt.Sprintf("%s de %s de %s", spokenCardinal(day, false), spanishMonthsSpoken[month-1], spokenCardinal(year, false))
}

func spokenIssueKey(match string) string {
	parts := jiraKeyForSpeech.FindStringSubmatch(match)
	letters := make([]string, 0, len(parts[1]))
	for _, r := range parts[1] {
		letters = append(letters, spanishLetterNames[r])
	}
	number, _ := strconv.Atoi(parts[2])
	return strings.Join(letters, " ") + " " + spokenCardinal(number, false)
}

func spokenTime(match string) string {
	parts := timeForSpeech.FindStringSubmatch(match)
	prefix := "las "
	if strings.HasPrefix(match, "Las") {
		prefix = "Las "
	}
	hour, _ := strconv.Atoi(parts[1])
	minute, _ := strconv.Atoi(parts[2])
	if minute == 0 {
		return prefix + spokenCardinal(hour, false) + " en punto"
	}
	return prefix + spokenCardinal(hour, false) + " y " + spokenCardinal(minute, false)
}

func spokenPercent(match string) string {
	number := percentForSpeech.FindStringSubmatch(match)[1]
	return spokenNumberToken(number) + " por ciento"
}

func spokenEuros(match string) string {
	number := euroForSpeech.FindStringSubmatch(match)[1]
	spoken := spokenNumberToken(number)
	if number == "1" {
		return "un euro"
	}
	return spoken + " euros"
}

func spokenDecimal(match string) string { return spokenNumberToken(match) }

// spokenNumberToken reads "3,5" as "tres coma cinco" and "81" as "ochenta y uno".
func spokenNumberToken(token string) string {
	token = strings.ReplaceAll(token, ".", ",")
	whole, fraction, hasFraction := strings.Cut(token, ",")
	n, err := strconv.Atoi(whole)
	if err != nil {
		return token
	}
	out := spokenCardinal(n, false)
	if hasFraction {
		digits := make([]string, 0, len(fraction))
		for _, r := range fraction {
			d, _ := strconv.Atoi(string(r))
			digits = append(digits, spokenCardinal(d, false))
		}
		out += " coma " + strings.Join(digits, " ")
	}
	return out
}

var joiningWords = map[string]bool{"en": true, "por": true, "de": true, "y": true, "que": true, "con": true, "a": true, "para": true, "o": true}

// spellNumbersInContext spells the remaining integers. A lone "1" agrees with
// the word that follows: "1 abierta" becomes "una abierta", "1 en curso" "uno en curso".
func spellNumbersInContext(text string) string {
	fields := strings.Fields(text)
	for i, field := range fields {
		core := strings.TrimRight(field, ".,;:!?)»\"")
		trail := field[len(core):]
		lead := ""
		for strings.HasPrefix(core, "(") || strings.HasPrefix(core, "¿") || strings.HasPrefix(core, "«") || strings.HasPrefix(core, "\"") {
			lead += core[:1]
			core = core[1:]
		}
		if core == "" || strings.Trim(core, "0123456789") != "" {
			continue
		}
		n, err := strconv.Atoi(core)
		if err != nil || len(core) > 9 {
			continue
		}
		feminine := false
		if n == 1 && i+1 < len(fields) {
			next := strings.ToLower(strings.Trim(fields[i+1], ".,;:!?¿¡()«»\""))
			switch {
			case joiningWords[next]:
				fields[i] = lead + "uno" + trail
				continue
			case strings.HasSuffix(next, "a") || strings.HasSuffix(next, "as"):
				feminine = true
			}
		}
		fields[i] = lead + spokenCardinal(n, feminine) + trail
	}
	return strings.Join(fields, " ")
}

var unitsSpoken = []string{"cero", "uno", "dos", "tres", "cuatro", "cinco", "seis", "siete", "ocho", "nueve", "diez", "once", "doce", "trece", "catorce", "quince",
	"dieciséis", "diecisiete", "dieciocho", "diecinueve", "veinte", "veintiuno", "veintidós", "veintitrés", "veinticuatro", "veinticinco", "veintiséis", "veintisiete", "veintiocho", "veintinueve"}
var tensSpoken = []string{"", "", "", "treinta", "cuarenta", "cincuenta", "sesenta", "setenta", "ochenta", "noventa"}
var hundredsSpoken = []string{"", "ciento", "doscientos", "trescientos", "cuatrocientos", "quinientos", "seiscientos", "setecientos", "ochocientos", "novecientos"}

// spokenCardinal spells an integer in Spanish. feminine turns "uno" into "una".
func spokenCardinal(n int, feminine bool) string {
	switch {
	case n < 0:
		return "menos " + spokenCardinal(-n, feminine)
	case n < 30:
		word := unitsSpoken[n]
		if n == 1 && feminine {
			return "una"
		}
		return word
	case n < 100:
		word := tensSpoken[n/10]
		if n%10 == 0 {
			return word
		}
		return word + " y " + spokenCardinal(n%10, feminine)
	case n == 100:
		return "cien"
	case n < 1000:
		rest := ""
		if n%100 != 0 {
			rest = " " + spokenCardinal(n%100, feminine)
		}
		hundreds := hundredsSpoken[n/100]
		if feminine && n/100 >= 2 {
			hundreds = strings.TrimSuffix(hundreds, "os") + "as"
		}
		return hundreds + rest
	case n < 1_000_000:
		thousands := n / 1000
		head := "mil"
		if thousands > 1 {
			head = spokenCardinal(thousands, feminine) + " mil"
		}
		if n%1000 == 0 {
			return head
		}
		return head + " " + spokenCardinal(n%1000, feminine)
	case n < 1_000_000_000:
		millions := n / 1_000_000
		head := "un millón"
		if millions > 1 {
			head = spokenCardinal(millions, false) + " millones"
		}
		if n%1_000_000 == 0 {
			return head
		}
		return head + " " + spokenCardinal(n%1_000_000, feminine)
	}
	digits := make([]string, 0, 12)
	for _, r := range strconv.Itoa(n) {
		d, _ := strconv.Atoi(string(r))
		digits = append(digits, unitsSpoken[d])
	}
	return strings.Join(digits, " ")
}

func tidySpeech(text string) string {
	text = regexp.MustCompile(`\s+`).ReplaceAllString(text, " ")
	text = regexp.MustCompile(`\s+([.,;:!?])`).ReplaceAllString(text, "$1")
	text = regexp.MustCompile(`([.!?])\s*\.`).ReplaceAllString(text, "$1")
	text = regexp.MustCompile(`([.]\s*){2,}`).ReplaceAllString(text, ". ")
	return strings.TrimSpace(text)
}
