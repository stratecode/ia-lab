package secretary

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type User struct {
	ID int64 `json:"id"`
}
type Chat struct {
	ID   int64  `json:"id"`
	Type string `json:"type"`
}
type Message struct {
	ID   int64  `json:"message_id"`
	From User   `json:"from"`
	Chat Chat   `json:"chat"`
	Text string `json:"text"`
}
type Update struct {
	ID      int64    `json:"update_id"`
	Message *Message `json:"message"`
}

func (s *Service) Permitted(m Message) bool {
	return m.Chat.Type == "private" && m.Chat.ID == m.From.ID && s.Allowed[m.From.ID]
}

type Bot struct {
	Base, Token string
	Client      *http.Client
	Service     *Service
	OnEvent     func(string)
}

func (b *Bot) call(ctx context.Context, method string, payload any, out any) error {
	data, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, "POST", b.Base+"/bot"+b.Token+"/"+method, bytes.NewReader(data))
	if err != nil {
		return errors.New("invalid Telegram endpoint")
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := b.Client.Do(req)
	if err != nil {
		return errors.New("Telegram transport unavailable")
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return fmt.Errorf("Telegram HTTP %d", res.StatusCode)
	}
	var envelope struct {
		OK     bool            `json:"ok"`
		Result json.RawMessage `json:"result"`
	}
	if json.NewDecoder(io.LimitReader(res.Body, 2<<20)).Decode(&envelope) != nil || !envelope.OK {
		return errors.New("Telegram request rejected")
	}
	if out != nil {
		return json.Unmarshal(envelope.Result, out)
	}
	return nil
}
func (b *Bot) event(name string) {
	if b.OnEvent != nil {
		b.OnEvent(name)
	}
}
func (b *Bot) send(ctx context.Context, chat int64, text string) error {
	runes := []rune(text)
	for len(runes) > 0 {
		n := len(runes)
		if n > 3500 {
			n = 3500
		}
		if err := b.call(ctx, "sendMessage", map[string]any{"chat_id": chat, "text": string(runes[:n])}, nil); err != nil {
			return err
		}
		runes = runes[n:]
	}
	return nil
}
func (b *Bot) Run(ctx context.Context) error {
	var me struct {
		ID int64 `json:"id"`
	}
	if err := b.call(ctx, "getMe", map[string]any{}, &me); err != nil {
		return err
	}
	var webhook struct {
		URL string `json:"url"`
	}
	if err := b.call(ctx, "getWebhookInfo", map[string]any{}, &webhook); err != nil {
		return err
	}
	if webhook.URL != "" {
		return errors.New("existing webhook must be reviewed before polling")
	}
	state := filepath.Join(b.Service.StateDir, "offset.json")
	var offset int64
	if data, err := os.ReadFile(state); err == nil {
		if json.Unmarshal(data, &offset) != nil {
			return errors.New("invalid polling state")
		}
	} else if !os.IsNotExist(err) {
		return errors.New("cannot read polling state")
	}
	b.event("telegram_ready")
	for ctx.Err() == nil {
		var updates []Update
		err := b.call(ctx, "getUpdates", map[string]any{"offset": offset, "timeout": 25, "allowed_updates": []string{"message"}}, &updates)
		if err != nil {
			b.event("telegram_poll_error: " + err.Error())
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(3 * time.Second):
				continue
			}
		}
		for _, update := range updates {
			// Checkpoint before execution: no automatic replay of a reply after restart.
			// A crash during inference can lose this update; the operator must resend.
			offset = update.ID + 1
			if err := atomicJSON(state, offset); err != nil {
				return errors.New("cannot persist polling offset")
			}
			if update.Message == nil || !b.Service.Permitted(*update.Message) {
				b.event("telegram_rejected")
				continue
			}
			m := update.Message
			if strings.TrimSpace(m.Text) == "" {
				if err = b.send(ctx, m.Chat.ID, "Por ahora recibo texto. La voz se incorporará después."); err != nil {
					b.event("telegram_send_error")
				}
				continue
			}
			_ = b.call(ctx, "sendChatAction", map[string]any{"chat_id": m.Chat.ID, "action": "typing"}, nil)
			answer, replyErr := b.Service.Reply(ctx, m.From.ID, m.Text)
			if replyErr != nil {
				b.event("model_reply_error")
				answer = "No he podido completar la respuesta. Reenvía tu consulta; no he ejecutado acciones externas."
			}
			if err = b.send(ctx, m.Chat.ID, answer); err != nil {
				b.event("telegram_send_error")
			} else {
				b.event("telegram_reply_sent")
			}
		}
	}
	return nil
}
