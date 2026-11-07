package secretary

import (
	"regexp"
	"strings"
)

var issueKeyPattern = regexp.MustCompile(`\b[A-Z][A-Z0-9]+-\d+\b`)

// verifySourcedIdentifiers rejects issue keys introduced by model synthesis.
// It is deliberately narrow: it does not claim to prove every sentence.
func verifySourcedIdentifiers(answer string, results []CapabilityResult) (string, bool) {
	var available strings.Builder
	for _, result := range results {
		available.WriteString(strings.ToUpper(result.Text))
		available.WriteByte('\n')
		for _, evidence := range result.Evidence {
			available.WriteString(strings.ToUpper(evidence.ID + " " + evidence.Title + " " + evidence.URL))
			available.WriteByte('\n')
		}
	}
	providerText := available.String()
	for _, key := range issueKeyPattern.FindAllString(strings.ToUpper(answer), -1) {
		if !strings.Contains(providerText, key) {
			return "No puedo verificar esa respuesta con los datos consultados; contiene una referencia a una incidencia que la fuente no devolvió.", true
		}
	}
	return answer, false
}
