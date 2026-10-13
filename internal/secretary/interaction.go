package secretary

import "strings"

type InteractionMeaning string

const (
	InteractionLookup    InteractionMeaning = "lookup"
	InteractionAgreement InteractionMeaning = "agreement"
	InteractionAction    InteractionMeaning = "action"
	InteractionNeutral   InteractionMeaning = "neutral"
)

type InteractionEventKind string

const (
	EventIntentRecognized InteractionEventKind = "intent_recognized"
	EventStageChanged     InteractionEventKind = "stage_changed"
	EventLookupStarted    InteractionEventKind = "lookup_started"
	EventActionRecognized InteractionEventKind = "action_recognized"
	EventActionStarted    InteractionEventKind = "action_started"
	EventWorkDelayed      InteractionEventKind = "work_delayed"
	EventCompleted        InteractionEventKind = "completed"
	EventBlocked          InteractionEventKind = "blocked"
)

type InteractionBlockReason string

const (
	BlockUnavailable       InteractionBlockReason = "unavailable"
	BlockConnectorFailure  InteractionBlockReason = "connector_failure"
	BlockProcessingFailure InteractionBlockReason = "processing_failure"
	BlockTimeout           InteractionBlockReason = "timeout"
)

// InteractionEvent is a channel-neutral progress signal. Source, Action, and
// Stage are short labels, never user text, prompts, IDs, or connector payloads.
type InteractionEvent struct {
	Kind    InteractionEventKind
	Meaning InteractionMeaning
	Source  string
	Action  string
	Stage   string
	Reason  InteractionBlockReason
}

type InteractionObserver func(InteractionEvent)

func interactionEmoji(event InteractionEvent) string {
	if event.Kind != EventIntentRecognized {
		return ""
	}
	switch event.Meaning {
	case InteractionLookup:
		return "🔍"
	case InteractionAgreement:
		return "✅"
	case InteractionAction:
		return "👍"
	default:
		return ""
	}
}

func interactionProgressText(event InteractionEvent) string {
	switch event.Kind {
	case EventLookupStarted:
		source := boundedLabel(event.Source)
		if source == "" {
			return ""
		}
		if event.Meaning == InteractionAction {
			return "Buscando en " + source + "; esta conexión solo permite consultar."
		}
		return "Déjame revisar " + source + "."
	case EventWorkDelayed:
		stage := strings.TrimSpace(event.Stage)
		if stage == "" {
			return ""
		}
		return "Esto está tardando más de lo previsto; sigo " + stage + "."
	default:
		return ""
	}
}

func interactionMeaning(intent secretaryIntent, text string) InteractionMeaning {
	query := normalizedIntentText(text)
	if explicitAgreement(query) {
		return InteractionAgreement
	}
	if explicitActionRequest(query) {
		return InteractionAction
	}
	switch intent {
	case intentCapability, intentMemory:
		return InteractionLookup
	default:
		return InteractionNeutral
	}
}

func explicitAgreement(query string) bool {
	return containsAny(query,
		"estoy de acuerdo",
		"de acuerdo contigo",
		"me parece bien",
		"me parece correcto",
		"coincido contigo",
		"lo apruebo",
		"acepto la propuesta",
	)
}

func explicitActionRequest(query string) bool {
	words := strings.Fields(query)
	for len(words) > 0 && (words[0] == "oye" || words[0] == "porfa") {
		words = words[1:]
	}
	if len(words) >= 2 && words[0] == "por" && words[1] == "favor" {
		words = words[2:]
	}
	if len(words) == 0 {
		return false
	}
	if words[0] == "puedes" || words[0] == "podrias" {
		words = words[1:]
		if len(words) == 0 {
			return false
		}
	}
	return containsAny(words[0],
		"cierra", "cerrar",
		"actualiza", "actualizar",
		"crea", "crear",
		"envia", "enviar",
		"manda", "mandar",
		"responde", "responder",
		"borra", "borrar",
		"elimina", "eliminar",
		"cancela", "cancelar",
		"pon", "programa", "programar",
		"agenda", "agendar",
		"recuerda", "recordar", "avisame",
		"llama", "llamar",
		"escribe", "escribir",
		"prepara", "preparar",
		"genera", "generar",
		"anade", "anadir",
		"marca", "marcar",
		"asigna", "asignar",
		"mueve", "mover",
		"despliega", "desplegar",
		"apaga", "apagar",
		"reinicia", "reiniciar",
	)
}
