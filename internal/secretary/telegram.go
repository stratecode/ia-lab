package secretary

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
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
type Voice struct {
	FileID   string `json:"file_id"`
	Duration int    `json:"duration"`
	FileSize int64  `json:"file_size"`
}
type Message struct {
	ID    int64  `json:"message_id"`
	From  User   `json:"from"`
	Chat  Chat   `json:"chat"`
	Text  string `json:"text"`
	Voice *Voice `json:"voice,omitempty"`
}
type Update struct {
	ID      int64    `json:"update_id"`
	Message *Message `json:"message"`
}

func (s *Service) Permitted(m Message) bool {
	return m.Chat.Type == "private" && m.Chat.ID == m.From.ID && s.Allowed[m.From.ID]
}

type Bot struct {
	Base, Token      string
	Client           *http.Client
	Service          *Service
	OnEvent          func(string)
	Speech           SpeechProcessor
	Audio            AudioConverter
	Metrics          *VoiceMetrics
	VoiceEnabled     bool
	VoiceMaxDuration time.Duration
}

const (
	maxTelegramFileBytes  = 20 * 1024 * 1024
	maxTelegramVoiceBytes = 49_000_000
)

var telegramFilePathSegment = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)

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

// processUpdate is shared by polling and webhook delivery so both transports
// apply the same private-chat allowlist, text, and voice behavior.
func (b *Bot) processUpdate(ctx context.Context, update Update) error {
	if update.Message == nil || !b.Service.Permitted(*update.Message) {
		b.event("telegram_rejected")
		return nil
	}
	m := update.Message
	if m.Voice != nil {
		if err := b.processVoice(ctx, m); err != nil {
			b.event("telegram_voice_error")
			return err
		}
		return nil
	}
	if strings.TrimSpace(m.Text) == "" {
		if err := b.send(ctx, m.Chat.ID, "Por ahora recibo texto. La voz se incorporará después."); err != nil {
			b.event("telegram_send_error")
			return err
		}
		b.event("telegram_reply_sent")
		return nil
	}
	_ = b.call(ctx, "sendChatAction", map[string]any{"chat_id": m.Chat.ID, "action": "typing"}, nil)
	answer, err := b.Service.Reply(ctx, m.From.ID, m.Text)
	if err != nil {
		b.event("model_reply_error")
		answer = "No he podido completar la respuesta. Reenvía tu consulta; no he ejecutado acciones externas."
	}
	if err := b.send(ctx, m.Chat.ID, answer); err != nil {
		b.event("telegram_send_error")
		return err
	}
	b.event("telegram_reply_sent")
	return nil
}

func (b *Bot) RunWebhookWorker(ctx context.Context, queue TelegramUpdateQueue) error {
	if queue == nil {
		return errors.New("Telegram update queue is required")
	}
	for ctx.Err() == nil {
		update, err := queue.Next(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			b.event("telegram_queue_error")
			if !waitTelegramRetry(ctx) {
				return nil
			}
			continue
		}
		if err := b.processUpdate(ctx, update); err != nil {
			if ctx.Err() != nil {
				return nil
			}
			if !waitTelegramRetry(ctx) {
				return nil
			}
			continue
		}
		if err := queue.Complete(update.ID); err != nil {
			b.event("telegram_queue_error")
			if !waitTelegramRetry(ctx) {
				return nil
			}
		}
	}
	return nil
}

func waitTelegramRetry(ctx context.Context) bool {
	timer := time.NewTimer(3 * time.Second)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
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
	go b.runTimerScheduler(ctx)
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
			_ = b.processUpdate(ctx, update)
		}
	}
	return nil
}

func (b *Bot) processVoice(ctx context.Context, message *Message) (returnErr error) {
	requestStarted := time.Now()
	requestResult := VoiceResultOK
	defer func() {
		if returnErr != nil {
			if ctx.Err() != nil {
				requestResult = VoiceResultCancelled
			} else {
				requestResult = voiceMetricResult(returnErr)
			}
		}
		if b.Metrics != nil {
			b.Metrics.ObserveRequest(requestResult, time.Since(requestStarted))
		}
	}()
	if message == nil || message.Voice == nil || !b.Service.Permitted(*message) {
		return errors.New("unauthorized voice message")
	}
	if !b.VoiceEnabled {
		return b.sendVoiceMessage(ctx, message.Chat.ID, "La recepción de notas de voz está desactivada.")
	}
	if b.Speech == nil || b.Audio == nil {
		return b.sendVoiceMessage(ctx, message.Chat.ID, "La voz no está disponible ahora mismo; puedes escribirme por texto.")
	}
	voice := message.Voice
	maxDuration := b.VoiceMaxDuration
	if maxDuration <= 0 {
		maxDuration = 120 * time.Second
	}
	if voice.FileID == "" || voice.Duration <= 0 || time.Duration(voice.Duration) > maxDuration/time.Second || voice.FileSize < 0 || voice.FileSize > maxTelegramFileBytes {
		requestResult = VoiceResultError
		return b.sendVoiceMessage(ctx, message.Chat.ID, "La nota de voz supera el límite permitido o no tiene metadatos válidos.")
	}
	var file struct {
		FilePath string `json:"file_path"`
	}
	if err := b.call(ctx, "getFile", map[string]any{"file_id": voice.FileID}, &file); err != nil {
		return errors.New("telegram voice download failed")
	}
	fileURL, err := telegramFileURL(b.Base, b.Token, file.FilePath)
	if err != nil {
		return errors.New("telegram voice download failed")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, fileURL, nil)
	if err != nil {
		return errors.New("telegram voice download failed")
	}
	client := *b.Client
	baseURL, _ := url.Parse(b.Base)
	client.CheckRedirect = func(request *http.Request, via []*http.Request) error {
		if len(via) >= 5 || request.URL.Scheme != baseURL.Scheme || !strings.EqualFold(request.URL.Host, baseURL.Host) {
			return http.ErrUseLastResponse
		}
		return nil
	}
	response, err := client.Do(request)
	if err != nil {
		return errors.New("telegram voice download failed")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK || response.ContentLength > maxTelegramFileBytes {
		return errors.New("telegram voice download failed")
	}
	ogg, err := io.ReadAll(io.LimitReader(response.Body, maxTelegramFileBytes+1))
	if len(ogg) > maxTelegramFileBytes {
		requestResult = VoiceResultError
		return b.sendVoiceMessage(ctx, message.Chat.ID, "La nota de voz supera el límite permitido de 20 MB.")
	}
	if err != nil || len(ogg) == 0 {
		return errors.New("telegram voice download failed")
	}
	sttStarted := time.Now()
	wav, sttErr := b.Audio.ToWAV(ctx, ogg)
	if sttErr != nil {
		b.observeVoicePhase(ctx, VoicePhaseSTT, sttStarted, sttErr)
		requestResult = voiceMetricResult(sttErr)
		if err := b.sendVoiceMessage(ctx, message.Chat.ID, "No he podido entender la nota de voz. ¿Puedes enviar otra con más claridad?"); err != nil {
			return errors.New("telegram voice reply failed")
		}
		return nil
	}
	transcript, sttErr := b.Speech.Transcribe(ctx, wav)
	if sttErr == nil && strings.TrimSpace(transcript) == "" {
		sttErr = errors.New("empty speech transcript")
	}
	b.observeVoicePhase(ctx, VoicePhaseSTT, sttStarted, sttErr)
	if sttErr != nil {
		requestResult = voiceMetricResult(sttErr)
		if sendErr := b.sendVoiceMessage(ctx, message.Chat.ID, "No he podido entender la nota de voz. ¿Puedes enviar otra con más claridad?"); sendErr != nil {
			return errors.New("telegram voice reply failed")
		}
		return nil
	}
	llmStarted := time.Now()
	answer, llmErr := b.Service.Reply(ctx, message.From.ID, transcript)
	b.observeVoicePhase(ctx, VoicePhaseLLM, llmStarted, llmErr)
	if llmErr != nil {
		requestResult = voiceMetricResult(llmErr)
		answer = "No he podido completar la respuesta. Reenvía tu consulta por texto; no he ejecutado acciones externas."
	}
	if err := b.sendVoiceMessage(ctx, message.Chat.ID, answer); err != nil {
		return errors.New("telegram text reply failed")
	}
	ttsStarted := time.Now()
	wavReply, ttsErr := b.Speech.Synthesize(ctx, answer)
	var oggReply []byte
	if ttsErr == nil {
		oggReply, ttsErr = b.Audio.ToOGGOpus(ctx, wavReply)
	}
	b.observeVoicePhase(ctx, VoicePhaseTTS, ttsStarted, ttsErr)
	if ttsErr != nil {
		return errors.New("telegram speech synthesis failed")
	}
	if err := b.sendVoice(ctx, message.Chat.ID, oggReply); err != nil {
		return err
	}
	return nil
}

func (b *Bot) observeVoicePhase(ctx context.Context, phase VoicePhase, started time.Time, err error) {
	if b.Metrics != nil {
		result := voiceMetricResult(err)
		if ctx.Err() != nil {
			result = VoiceResultCancelled
		}
		b.Metrics.ObservePhase(phase, result, time.Since(started))
	}
}

func voiceMetricResult(err error) VoiceResult {
	if err == nil {
		return VoiceResultOK
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return VoiceResultCancelled
	}
	return VoiceResultError
}

func (b *Bot) sendVoiceMessage(ctx context.Context, chat int64, text string) error {
	started := time.Now()
	err := b.send(ctx, chat, text)
	b.observeVoicePhase(ctx, VoicePhaseTelegramSend, started, err)
	return err
}

func telegramFileURL(base, token, filePath string) (string, error) {
	parts := strings.Split(filePath, "/")
	if filePath == "" || strings.HasPrefix(filePath, "/") {
		return "", errors.New("invalid Telegram file path")
	}
	for _, part := range parts {
		if part == "" || part == "." || part == ".." || !telegramFilePathSegment.MatchString(part) {
			return "", errors.New("invalid Telegram file path")
		}
	}
	u, err := url.Parse(base)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || token == "" {
		return "", errors.New("invalid Telegram file endpoint")
	}
	components := []string{"file", "bot" + token}
	components = append(components, parts...)
	return u.JoinPath(components...).String(), nil
}

func (b *Bot) sendVoice(ctx context.Context, chat int64, audio []byte) (returnErr error) {
	started := time.Now()
	defer func() { b.observeVoicePhase(ctx, VoicePhaseTelegramSend, started, returnErr) }()
	if len(audio) == 0 || len(audio) > maxTelegramVoiceBytes {
		return errors.New("telegram voice upload failed")
	}
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.WriteField("chat_id", fmt.Sprint(chat)); err != nil {
		return errors.New("telegram voice upload failed")
	}
	file, err := writer.CreateFormFile("voice", "voice.ogg")
	if err != nil {
		return errors.New("telegram voice upload failed")
	}
	if _, err := file.Write(audio); err != nil {
		return errors.New("telegram voice upload failed")
	}
	if err := writer.Close(); err != nil {
		return errors.New("telegram voice upload failed")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, b.Base+"/bot"+b.Token+"/sendVoice", &body)
	if err != nil {
		return errors.New("telegram voice upload failed")
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	resp, err := b.Client.Do(req)
	if err != nil {
		return errors.New("telegram voice upload failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return errors.New("telegram voice upload failed")
	}
	var envelope struct {
		OK bool `json:"ok"`
	}
	if json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(&envelope) != nil || !envelope.OK {
		return errors.New("telegram voice upload failed")
	}
	return nil
}

func (b *Bot) runTimerScheduler(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	b.deliverDueTimers(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			b.deliverDueTimers(ctx)
		}
	}
}

func (b *Bot) deliverDueTimers(ctx context.Context) {
	timers, err := b.Service.dueTimers(time.Now())
	if err != nil {
		b.event("timer_state_error")
		return
	}
	for _, timer := range timers {
		if err := b.send(ctx, timer.ChatID, "⏰ Temporizador terminado."); err != nil {
			b.event("timer_send_error")
			continue
		}
		if err := b.Service.markTimerDelivered(timer.ID); err != nil {
			b.event("timer_state_error")
			continue
		}
		b.event("timer_delivered")
	}
}
