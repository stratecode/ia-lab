package secretary

import (
	"math/big"
	"regexp"
	"sort"
	"strings"
)

// Guardrail categories are a closed set so they can be metric labels and audit
// fields without ever carrying user content.
const (
	guardPrivateKey = "private_key"
	guardToken      = "token"
	guardCard       = "card"
	guardIBAN       = "iban"
	guardNationalID = "national_id"
)

var guardrailCategories = []string{guardPrivateKey, guardToken, guardCard, guardIBAN, guardNationalID}

var (
	privateKeyPattern = regexp.MustCompile(`(?s)-----BEGIN [A-Z ]*PRIVATE KEY-----.*?(?:-----END [A-Z ]*PRIVATE KEY-----|$)`)
	tokenPatterns     = []*regexp.Regexp{
		regexp.MustCompile(`\b(?:AKIA|ASIA)[0-9A-Z]{16}\b`),
		regexp.MustCompile(`\bgh[pousr]_[A-Za-z0-9]{30,}\b`),
		regexp.MustCompile(`\bxox[abprs]-[A-Za-z0-9-]{10,}\b`),
		regexp.MustCompile(`\bATATT[A-Za-z0-9_\-=]{20,}\b`),
		regexp.MustCompile(`\bsk-[A-Za-z0-9_\-]{20,}\b`),
		regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\b`),
		regexp.MustCompile(`\b\d{6,10}:[A-Za-z0-9_-]{35}\b`),
		regexp.MustCompile(`(?i)\bbearer\s+[A-Za-z0-9._~+/=-]{20,}`),
	}
	cardPattern       = regexp.MustCompile(`\b\d(?:[ -]?\d){12,18}\b`)
	ibanPattern       = regexp.MustCompile(`\b[A-Z]{2}\d{2}(?: ?[A-Z0-9]){11,30}\b`)
	nationalIDPattern = regexp.MustCompile(`\b(?:\d{8}|[XYZxyz]\d{7})[- ]?[A-Za-z]\b`)
)

// GuardrailReport counts redactions per category. It never stores the text
// that was removed.
type GuardrailReport map[string]int

// Total returns the number of redactions across categories.
func (r GuardrailReport) Total() int {
	total := 0
	for _, n := range r {
		total += n
	}
	return total
}

// Categories returns the categories that fired, sorted for stable output.
func (r GuardrailReport) Categories() []string {
	out := make([]string, 0, len(r))
	for category, n := range r {
		if n > 0 {
			out = append(out, category)
		}
	}
	sort.Strings(out)
	return out
}

func guardMarker(category string) string { return "[REDACTED:" + category + "]" }

// RedactSensitive removes credentials and high-risk personal identifiers from
// text that is about to leave the service boundary (for example towards the
// model). Emails, names and phone numbers are intentionally kept: Donna's job
// is to reason over exactly that Jira and Granola data. Every detector has to
// validate its match (checksum or fixed prefix) so ordinary numbers such as
// issue counts, dates and ticket keys are not touched.
func RedactSensitive(text string) (string, GuardrailReport) {
	report := GuardrailReport{}
	if text == "" {
		return text, report
	}
	out := text
	out = replaceCounting(out, privateKeyPattern, guardPrivateKey, report, nil)
	for _, pattern := range tokenPatterns {
		out = replaceCounting(out, pattern, guardToken, report, nil)
	}
	out = replaceCounting(out, ibanPattern, guardIBAN, report, validIBAN)
	out = replaceCounting(out, cardPattern, guardCard, report, validCard)
	out = replaceCounting(out, nationalIDPattern, guardNationalID, report, validSpanishID)
	return out, report
}

func replaceCounting(text string, pattern *regexp.Regexp, category string, report GuardrailReport, valid func(string) bool) string {
	return pattern.ReplaceAllStringFunc(text, func(match string) string {
		if valid != nil && !valid(match) {
			return match
		}
		report[category]++
		return guardMarker(category)
	})
}

func digitsOnly(value string) string {
	var b strings.Builder
	for _, r := range value {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func validCard(match string) bool {
	digits := digitsOnly(match)
	if len(digits) < 13 || len(digits) > 19 {
		return false
	}
	// Reject runs of one repeated digit, which are placeholders.
	if strings.Count(digits, digits[:1]) == len(digits) {
		return false
	}
	sum, double := 0, false
	for i := len(digits) - 1; i >= 0; i-- {
		n := int(digits[i] - '0')
		if double {
			n *= 2
			if n > 9 {
				n -= 9
			}
		}
		sum += n
		double = !double
	}
	return sum%10 == 0
}

func validIBAN(match string) bool {
	compact := strings.ToUpper(strings.ReplaceAll(match, " ", ""))
	if len(compact) < 15 || len(compact) > 34 {
		return false
	}
	rearranged := compact[4:] + compact[:4]
	var digits strings.Builder
	for _, r := range rearranged {
		switch {
		case r >= '0' && r <= '9':
			digits.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			digits.WriteString(big.NewInt(int64(r - 'A' + 10)).String())
		default:
			return false
		}
	}
	n, ok := new(big.Int).SetString(digits.String(), 10)
	return ok && new(big.Int).Mod(n, big.NewInt(97)).Int64() == 1
}

func validSpanishID(match string) bool {
	compact := strings.ToUpper(strings.NewReplacer("-", "", " ", "").Replace(match))
	if len(compact) != 9 {
		return false
	}
	body, letter := compact[:8], compact[8]
	switch compact[0] {
	case 'X':
		body = "0" + compact[1:8]
	case 'Y':
		body = "1" + compact[1:8]
	case 'Z':
		body = "2" + compact[1:8]
	}
	n := 0
	for _, r := range body {
		if r < '0' || r > '9' {
			return false
		}
		n = n*10 + int(r-'0')
	}
	return "TRWAGMYFPDXBNJZSQVHLCKE"[n%23] == letter
}

// guardTurns returns a copy of turns with every content redacted. The input
// slice is never mutated because turns also back the in-memory history, and
// operator-visible history must stay unchanged.
func guardTurns(turns []Turn) ([]Turn, GuardrailReport) {
	total := GuardrailReport{}
	out := make([]Turn, len(turns))
	for i, turn := range turns {
		redacted, report := RedactSensitive(turn.Content)
		turn.Content = redacted
		out[i] = turn
		for category, n := range report {
			total[category] += n
		}
	}
	return out, total
}
