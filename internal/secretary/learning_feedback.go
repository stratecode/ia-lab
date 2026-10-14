package secretary

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

func (s *Service) SubmitFeedback(operatorID int64, channel, conversation string, input FeedbackInput) error {
	if s.Learning == nil || operatorID <= 0 || strings.TrimSpace(channel) == "" || strings.TrimSpace(conversation) == "" {
		return errors.New("feedback context unavailable")
	}
	if !validFeedbackLabel(input.Label) {
		return errors.New("unknown feedback label")
	}
	input.Correction = strings.TrimSpace(input.Correction)
	if len([]rune(input.Correction)) > 2000 {
		return errors.New("feedback is too long")
	}
	if input.Label != FeedbackCorrect && input.Correction == "" {
		return errors.New("feedback reason required")
	}
	trace, ok, err := s.Learning.LastTrace(operatorID, channel, conversationDigest(channel, conversation))
	if err != nil {
		return errors.New("cannot find latest interaction")
	}
	if !ok {
		return ErrNoPreviousInteraction
	}
	if trace.OperatorID != operatorID || trace.Channel != channel || trace.ConversationRef != conversationDigest(channel, conversation) {
		return errors.New("latest interaction does not belong to this conversation")
	}
	if err := s.Learning.AppendFeedback(FeedbackEvent{InteractionID: trace.ID, OperatorID: operatorID, At: time.Now().UTC(), Label: input.Label, Correction: input.Correction}); err != nil {
		return errors.New("cannot save feedback")
	}
	if input.Label == FeedbackMissingFact || input.Label == FeedbackWrongEntity || input.Label == FeedbackWrongSource || input.Label == FeedbackIncorrect {
		item := KnowledgeItem{OperatorID: operatorID, Type: KnowledgeTypeFact, Subject: "interacción " + trace.ID, Predicate: string(input.Label), Value: input.Correction, Sources: traceEvidence(trace), Confidence: 0.5, Status: KnowledgeCandidate, CreatedAt: time.Now().UTC()}
		if err := s.Learning.SaveKnowledge(item); err != nil {
			return errors.New("feedback saved but candidate creation failed")
		}
	}
	return nil
}

func traceEvidence(trace InteractionTrace) []EvidenceRef {
	var refs []EvidenceRef
	for _, capability := range trace.Capabilities {
		refs = append(refs, capability.Evidence...)
	}
	return refs
}

func (s *Service) ReviewKnowledge(operatorID int64, action, itemID string) error {
	if s.Learning == nil || operatorID <= 0 || strings.TrimSpace(itemID) == "" {
		return errors.New("knowledge review unavailable")
	}
	action = strings.TrimSpace(action)
	status := KnowledgeStatus("")
	switch action {
	case "approve", "/aprueba":
		status = KnowledgeApproved
	case "reject", "/descarta":
		status = KnowledgeRejected
	default:
		return errors.New("unknown knowledge review action")
	}
	items, err := s.Learning.ListKnowledge(operatorID, KnowledgeCandidate)
	if err != nil {
		return errors.New("cannot load knowledge candidates")
	}
	for _, item := range items {
		if item.ID != itemID {
			continue
		}
		if item.OperatorID != operatorID {
			return errors.New("candidate belongs to another operator")
		}
		item.Status = status
		item.UpdatedAt = time.Now().UTC()
		if err := s.Learning.SaveKnowledge(item); err != nil {
			return errors.New("cannot review knowledge candidate")
		}
		return nil
	}
	return errors.New("candidate not found")
}

func (s *Service) learningCommand(user int64, channel, conversation, text string) (string, bool) {
	fields := strings.Fields(text)
	if len(fields) == 0 {
		return "", false
	}
	switch fields[0] {
	case "/feedback":
		rest := strings.TrimSpace(strings.TrimPrefix(text, "/feedback"))
		input := FeedbackInput{}
		switch {
		case rest == "bien":
			input.Label = FeedbackCorrect
		case strings.HasPrefix(rest, "mal:"):
			input.Label, input.Correction = FeedbackIncorrect, strings.TrimSpace(strings.TrimPrefix(rest, "mal:"))
		case strings.HasPrefix(rest, "corrige:"):
			input.Label, input.Correction = FeedbackMissingFact, strings.TrimSpace(strings.TrimPrefix(rest, "corrige:"))
		default:
			return "Uso: /feedback bien | /feedback mal: motivo | /feedback corrige: corrección", true
		}
		if err := s.SubmitFeedback(user, channel, conversation, input); err != nil {
			return feedbackErrorText(err), true
		}
		if input.Label == FeedbackCorrect {
			return "Anotado: la respuesta anterior queda marcada como correcta.", true
		}
		return "Corrección guardada para revisión; aún no la usaré como un hecho aprobado.", true
	case "/aprendizaje":
		if s.Learning == nil {
			return "La memoria de aprendizaje no está disponible.", true
		}
		items, err := s.Learning.ListKnowledge(user, KnowledgeCandidate)
		if err != nil {
			return "No he podido cargar las correcciones pendientes.", true
		}
		if len(items) == 0 {
			return "No hay correcciones pendientes de revisión.", true
		}
		lines := make([]string, 0, len(items))
		for _, item := range items {
			lines = append(lines, fmt.Sprintf("%s: %s", item.ID, shortText(item.Value, 140)))
		}
		return strings.Join(lines, "\n"), true
	case "/aprueba", "/descarta":
		if len(fields) != 2 {
			return "Indica el ID de una corrección pendiente.", true
		}
		if err := s.ReviewKnowledge(user, fields[0], fields[1]); err != nil {
			return "No he podido revisar esa corrección: no existe como candidata pendiente.", true
		}
		if fields[0] == "/aprueba" {
			return "Corrección aprobada; podrá recuperarse como memoria verificada.", true
		}
		return "Corrección descartada.", true
	default:
		return "", false
	}
}

func shortText(value string, maxRunes int) string {
	runes := []rune(strings.TrimSpace(value))
	if len(runes) > maxRunes {
		return string(runes[:maxRunes]) + "…"
	}
	return string(runes)
}

func feedbackErrorText(err error) string {
	if errors.Is(err, ErrNoPreviousInteraction) {
		return "No hay una respuesta anterior en esta conversación a la que asociar el feedback."
	}
	return "No he podido guardar ese feedback. Comprueba el formato y que haya una respuesta anterior en esta conversación."
}

var ErrNoPreviousInteraction = errors.New("no previous interaction in this conversation")
