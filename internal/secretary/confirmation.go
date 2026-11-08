package secretary

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// A confirmation is the one-button step between "Donna understood a request to
// schedule something" and "it is scheduled". The conversation service records
// that a draft is waiting; the Telegram adapter turns it into a Confirmar button.
// Other channels use the typed command. The drafts themselves live in their own
// stores (timers, scheduled tasks) and do nothing until confirmed.

const (
	confirmTimerKind = "tmc"
	confirmTaskKind  = "stc"
	// confirmationOfferTTL matches the draft lifetime: an old offer gets no button.
	confirmationOfferTTL = timerDraftTTL
)

type pendingConfirmation struct {
	Kind, ID string
	At       time.Time
}

var confirmationID = regexp.MustCompile(`^[a-z0-9]{6,32}$`)

// offerConfirmation records that the reply being produced is a draft awaiting
// confirmation. Only Telegram renders a button; elsewhere the text says what to type.
func (s *Service) offerConfirmation(user int64, channel, conversation, kind, id string) {
	if channel != "telegram" {
		return
	}
	s.confirmMu.Lock()
	defer s.confirmMu.Unlock()
	if s.confirmations == nil {
		s.confirmations = map[string]pendingConfirmation{}
	}
	s.confirmations[fmt.Sprintf("%d:%s", user, conversation)] = pendingConfirmation{Kind: kind, ID: id, At: time.Now()}
}

// takeConfirmation returns and clears the offer for this operator and chat.
func (s *Service) takeConfirmation(user int64, conversation string) (pendingConfirmation, bool) {
	s.confirmMu.Lock()
	defer s.confirmMu.Unlock()
	key := fmt.Sprintf("%d:%s", user, conversation)
	offer, ok := s.confirmations[key]
	delete(s.confirmations, key)
	if !ok || time.Since(offer.At) > confirmationOfferTTL {
		return pendingConfirmation{}, false
	}
	return offer, true
}

// sendAnswer sends a reply and, when it is a draft awaiting confirmation, adds
// the single Confirmar button.
func (b *Bot) sendAnswer(ctx context.Context, chat, user int64, text string) error {
	offer, ok := b.Service.takeConfirmation(user, strconv.FormatInt(chat, 10))
	if !ok {
		return b.send(ctx, chat, text)
	}
	markup := map[string]any{"inline_keyboard": [][]map[string]string{{{"text": "✅ Confirmar", "callback_data": offer.Kind + ":" + offer.ID}}}}
	return b.call(ctx, "sendMessage", map[string]any{"chat_id": chat, "text": telegramHTML(text), "parse_mode": "HTML", "reply_markup": markup}, nil)
}

// processConfirmCallback handles a Confirmar press. Authorization was checked by
// the caller; the operator is always the user who pressed the button.
func (b *Bot) processConfirmCallback(ctx context.Context, query *CallbackQuery, kind, id string) error {
	if !confirmationID.MatchString(id) {
		_ = b.call(ctx, "answerCallbackQuery", map[string]any{"callback_query_id": query.ID, "text": "La acción no es válida o ha caducado."}, nil)
		return nil
	}
	var result string
	switch kind {
	case confirmTimerKind:
		result = b.Service.confirmTimerReply(query.From.ID, id)
	case confirmTaskKind:
		result = b.Service.scheduledTaskAction(query.From.ID, []string{"/confirmar-tarea", id}, "confirmar")
	default:
		return nil
	}
	short := result
	if runes := []rune(short); len(runes) > 150 {
		short = string(runes[:150])
	}
	_ = b.call(ctx, "answerCallbackQuery", map[string]any{"callback_query_id": query.ID, "text": short}, nil)
	// The button has done its job whatever the outcome; remove it so it cannot be pressed twice.
	_ = b.call(ctx, "editMessageReplyMarkup", map[string]any{"chat_id": query.Message.Chat.ID, "message_id": query.Message.ID, "reply_markup": map[string]any{"inline_keyboard": [][]map[string]string{}}}, nil)
	if err := b.send(ctx, query.Message.Chat.ID, result); err != nil {
		b.event("telegram_send_error")
		return err
	}
	b.event("confirmation_handled")
	return nil
}

func isConfirmationData(data string) (kind, id string, ok bool) {
	parts := strings.Split(data, ":")
	if len(parts) == 2 && (parts[0] == confirmTimerKind || parts[0] == confirmTaskKind) {
		return parts[0], parts[1], true
	}
	return "", "", false
}
