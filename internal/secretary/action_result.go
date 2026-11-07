package secretary

import (
	"regexp"
	"strings"
)

// ActionResult records the effect verified by the service, not the effect
// described by the model. The current secretary has no external write executor.
type ActionResult struct {
	Status            ActionStatus
	ReceiptID         string
	Verified          bool
	HasSourceEvidence bool
}

type ActionStatus string

const (
	ActionNotExecuted ActionStatus = "not_executed"
	ActionCommitted   ActionStatus = "committed"
)

func (r ActionResult) permitsSuccessClaim() bool {
	return r.Status == ActionCommitted && r.Verified && strings.TrimSpace(r.ReceiptID) != ""
}

var externalWriteClaim = regexp.MustCompile(`\b(?:he|hemos|se ha|acabo de|ya esta|ya quedo|esta|quedo)\s+(?:cread[oa]|actualizad[oa]|cerrad[oa]|borrad[oa]|eliminad[oa]|enviad[oa]|asignad[oa]|movid[oa]|agendad[oa]|programad[oa]|registrad[oa]|publicad[oa]|desplegad[oa]|hecho|crear|enviar|actualizar)\b`)
var passiveWriteClaim = regexp.MustCompile(`\b(?:tarea|incidencia|ticket|correo|email|mensaje|evento)\s+(?:cread[oa]|actualizad[oa]|cerrad[oa]|borrad[oa]|eliminad[oa]|enviad[oa]|asignad[oa]|movid[oa]|agendad[oa]|programad[oa])\b`)
var externalWriteCommand = regexp.MustCompile(`(?:^|\s)(?:puedes|podrias)\s+(?:encargarte\s+de\s+)?(?:crear|actualizar|cerrar|borrar|eliminar|enviar|asignar|mover|agendar|programar)\b|(?:^|\s)(?:quiero|necesito)\s+que\s+(?:crees|actualices|cierres|borres|elimines|envies|asignes|muevas|agendes|programes)\b`)
var externalWriteObject = regexp.MustCompile(`\b(?:crear|crees|actualizar|actualices|cerrar|cierres|borrar|borres|eliminar|elimines|enviar|envies|asignar|asignes|mover|muevas|agendar|agendes|programar|programes)\s+(?:(?:un|una|el|la)\s+)?(?:nueva?\s+)?(?:tarea|incidencia|ticket|correo|email|mensaje|evento)\b`)

func requestsExternalWrite(text string) bool {
	query := normalizedIntentText(text)
	if containsAny(query, "como crear", "como actualizar", "como se crea", "como se actualiza", "que pasos", "crear un resumen", "crear una lista", "crear un borrador") {
		return false
	}
	return externalWriteCommand.MatchString(query) && (externalWriteObject.MatchString(query) || jiraIssueKeyMention.MatchString(strings.ToUpper(text)))
}

func confirmsExternalActionOffer(text string, history []Turn) bool {
	confirmation := normalizedIntentText(text)
	switch confirmation {
	case "si hazlo", "hazlo", "si adelante", "adelante", "vale hazlo", "si confirmo", "confirmo", "de acuerdo hazlo", "ejecutalo":
	default:
		return false
	}
	for i := len(history) - 1; i >= 0; i-- {
		if history[i].Role != "assistant" {
			continue
		}
		previous := normalizedIntentText(history[i].Content)
		return strings.Contains(previous, "donna solo tiene acceso de lectura") || offersExternalWrite(previous)
	}
	return false
}

// externalWriteOffer matches an offer phrase with the write verb right next to
// it ("puedo crear", "quieres que te cree"). Requiring proximity keeps a
// sentence such as "no puedo decir si van en plazo" from being read as an offer
// just because an issue title elsewhere in the answer starts with "Crear".
var externalWriteOffer = regexp.MustCompile(`\b(?:puedo|podria|me encargo(?: de)?|quieres que)\s+(?:\w+\s+){0,3}(?:crear|actualizar|cerrar|borrar|eliminar|enviar|asignar|mover|agendar|programar|desplegar)\b`)

func offersExternalWrite(answer string) bool {
	normalized := normalizedIntentText(answer)
	return externalWriteOffer.MatchString(normalized) &&
		containsAny(normalized, "jira", "tarea", "incidencia", "correo", "email", "mensaje", "evento", "calendario", "reunion")
}

func claimsUnverifiedWrite(answer string) bool {
	normalized := normalizedIntentText(answer)
	for _, location := range externalWriteClaim.FindAllStringIndex(normalized, -1) {
		prefix := strings.Fields(normalized[:location[0]])
		if len(prefix) > 0 && (prefix[len(prefix)-1] == "no" || prefix[len(prefix)-1] == "nunca") {
			continue
		}
		return true
	}
	return false
}

func verifyActionAnswer(answer string, result ActionResult) (string, bool) {
	if result.permitsSuccessClaim() || (!claimsUnverifiedWrite(answer) && !offersExternalWrite(answer) && (result.HasSourceEvidence || !passiveWriteClaim.MatchString(normalizedIntentText(answer)))) {
		return answer, false
	}
	return "No he aplicado ningún cambio externo. Donna solo tiene acceso de lectura; no hay una operación verificada que confirme esa acción.", true
}
