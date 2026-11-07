package secretary

import (
	"errors"
	"strings"
)

// OperatorContext is derived from an authenticated channel adapter, never from
// user supplied mapping fields.
type OperatorContext struct {
	OperatorID     int64
	Channel        string
	ConversationID string
}

func NewOperatorContext(operatorID int64, channel, conversationID string) (OperatorContext, error) {
	principal := OperatorContext{OperatorID: operatorID, Channel: strings.TrimSpace(channel), ConversationID: strings.TrimSpace(conversationID)}
	if principal.OperatorID <= 0 || principal.ConversationID == "" || len(principal.ConversationID) > 200 {
		return OperatorContext{}, errors.New("trusted operator and conversation required")
	}
	if principal.Channel != "telegram" && principal.Channel != "openwebui" {
		return OperatorContext{}, errors.New("unsupported channel")
	}
	return principal, nil
}
