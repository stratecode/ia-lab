package secretary

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
	"unicode"
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
	ID   int64  `json:"message_id"`
	From User   `json:"from"`
	Chat Chat   `json:"chat"`
	Text string `json:"text"`
	// ReplyTo is the message this one answers; check-in answers are tied to the
	// question through it.
	ReplyTo *Message `json:"reply_to_message,omitempty"`
	Voice   *Voice   `json:"voice,omitempty"`
	Audio   *Voice   `json:"audio,omitempty"`
}

type CallbackQuery struct {
	ID      string   `json:"id"`
	From    User     `json:"from"`
	Message *Message `json:"message"`
	Data    string   `json:"data"`
}

func (m *Message) audioFile() *Voice {
	if m == nil {
		return nil
	}
	if m.Voice != nil {
		return m.Voice
	}
	return m.Audio
}

type Update struct {
	ID            int64          `json:"update_id"`
	Message       *Message       `json:"message"`
	CallbackQuery *CallbackQuery `json:"callback_query,omitempty"`
}

func (s *Service) Permitted(m Message) bool {
	return m.Chat.Type == "private" && m.Chat.ID == m.From.ID && s.Allowed[m.From.ID]
}

type Bot struct {
	Base, Token          string
	Client               *http.Client
	Service              *Service
	OnEvent              func(string)
	Speech               SpeechProcessor
	Audio                AudioConverter
	Metrics              *VoiceMetrics
	TranscriptionEnabled bool
	VoiceMaxDuration     time.Duration
	ProgressDelay        time.Duration
	acknowledgements     atomic.Uint64
}

var telegramAcknowledgements = [...]string{
	"Lo tengo; me pongo con ello.",
	"Vale, lo reviso ahora.",
	"Entendido. Estoy con ello.",
	"De acuerdo, voy a comprobarlo.",
	"Me ocupo; enseguida te cuento.",
}

var telegramProgressPrefixes = [...]string{
	"Sigo con ello:",
	"Aún estoy",
	"Me está llevando algo más; sigo",
	"Continúo",
}

func (b *Bot) nextAcknowledgement() string {
	index := b.acknowledgements.Add(1) - 1
	return telegramAcknowledgements[index%uint64(len(telegramAcknowledgements))]
}

func (b *Bot) progressTracker(ctx context.Context, chat int64) *progressTracker {
	tracker := newProgressTracker(b.ProgressDelay, func(message string) {
		if err := b.send(ctx, chat, message); err != nil {
			b.event("telegram_send_error")
		}
	})
	index := b.acknowledgements.Add(1) - 1
	tracker.SetPrefix(telegramProgressPrefixes[index%uint64(len(telegramProgressPrefixes))])
	return tracker
}

func (b *Bot) progressCallback(ctx context.Context, chat int64, tracker *progressTracker) func(string) {
	return func(message string) {
		const prefix = "Voy a revisar "
		if strings.HasPrefix(message, prefix) {
			stage := strings.TrimSuffix(strings.TrimPrefix(message, prefix), ".")
			tracker.SetStage("consultando " + stage)
			if err := b.send(ctx, chat, message); err != nil {
				b.event("telegram_send_error")
			}
			return
		}
		tracker.SetStage(message)
	}
}

func telegramReaction(meaning InteractionMeaning) string {
	return interactionEmoji(InteractionEvent{Kind: EventIntentRecognized, Meaning: meaning})
}

const (
	maxTelegramFileBytes  = 20 * 1024 * 1024
	maxTelegramVoiceBytes = 49_000_000
)

var telegramFilePathSegment = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)

func (b *Bot) call(ctx context.Context, method string, payload any, out any) (returnErr error) {
	done := conversationStep(ctx, "telegram."+method)
	defer func() { done(returnErr) }()
	data, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, "POST", b.Base+"/bot"+b.Token+"/"+method, bytes.NewReader(data))
	if err != nil {
		return errors.New("invalid Telegram endpoint")
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := conversationHTTP(ctx, b.Client, req, "telegram."+method)
	if err != nil {
		return errTelegramTransport
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return telegramHTTPError{Status: res.StatusCode}
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

// errTelegramTransport means the request may have reached Telegram: the outcome
// of the call is unknown.
var errTelegramTransport = errors.New("Telegram transport unavailable")

type telegramHTTPError struct{ Status int }

func (e telegramHTTPError) Error() string { return fmt.Sprintf("Telegram HTTP %d", e.Status) }

const proactiveSendTimeout = 20 * time.Second

// Send delivers one proactive message as a single Telegram message and says
// exactly what is known about the outcome. A rejection is retryable; a
// transport error or 5xx may have been accepted, so it is reported as uncertain.
func (b *Bot) Send(ctx context.Context, chatID int64, text string) ProactiveSendOutcome {
	text = truncateTextRunes(strings.TrimSpace(text), 3500)
	if text == "" {
		return SendFailed
	}
	ctx, cancel := context.WithTimeout(ctx, proactiveSendTimeout)
	defer cancel()
	err := b.call(ctx, "sendMessage", map[string]any{"chat_id": chatID, "text": telegramHTML(text), "parse_mode": "HTML"}, nil)
	if err == nil {
		return SendDelivered
	}
	var httpErr telegramHTTPError
	if errors.As(err, &httpErr) && httpErr.Status >= 400 && httpErr.Status < 500 {
		return SendFailed
	}
	if err.Error() == "Telegram request rejected" || err.Error() == "invalid Telegram endpoint" {
		return SendFailed
	}
	return SendUncertain
}

// SendCheckin sends one check-in question and returns Telegram's message id.
func (b *Bot) SendCheckin(ctx context.Context, chatID int64, text string) (int64, ProactiveSendOutcome) {
	text = truncateTextRunes(strings.TrimSpace(text), 3500)
	if text == "" {
		return 0, SendFailed
	}
	ctx, cancel := context.WithTimeout(ctx, proactiveSendTimeout)
	defer cancel()
	var sent struct {
		MessageID int64 `json:"message_id"`
	}
	err := b.call(ctx, "sendMessage", map[string]any{"chat_id": chatID, "text": telegramHTML(text), "parse_mode": "HTML"}, &sent)
	if err == nil {
		return sent.MessageID, SendDelivered
	}
	var httpErr telegramHTTPError
	if errors.As(err, &httpErr) && httpErr.Status >= 400 && httpErr.Status < 500 {
		return 0, SendFailed
	}
	if err.Error() == "Telegram request rejected" || err.Error() == "invalid Telegram endpoint" {
		return 0, SendFailed
	}
	return 0, SendUncertain
}

func (b *Bot) event(name string) {
	if b.OnEvent != nil {
		b.OnEvent(name)
	}
}

const telegramAnswerChunkRunes = 700

func (b *Bot) send(ctx context.Context, chat int64, text string) error {
	for _, message := range splitTelegramMessages(text) {
		if err := b.call(ctx, "sendMessage", map[string]any{"chat_id": chat, "text": telegramHTML(message), "parse_mode": "HTML"}, nil); err != nil {
			return err
		}
	}
	return nil
}

func splitTelegramMessages(text string) []string {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	if len([]rune(text)) <= telegramAnswerChunkRunes {
		return []string{text}
	}

	var units []string
	for _, paragraph := range strings.Split(text, "\n\n") {
		if len([]rune(paragraph)) <= telegramAnswerChunkRunes {
			units = append(units, paragraph)
			continue
		}
		for _, line := range strings.Split(paragraph, "\n") {
			if len([]rune(line)) <= telegramAnswerChunkRunes {
				units = append(units, line)
				continue
			}
			units = append(units, splitTelegramLongLine(line)...)
		}
	}

	var messages []string
	current := ""
	for _, unit := range units {
		unit = strings.TrimSpace(unit)
		if unit == "" {
			continue
		}
		if current == "" {
			current = unit
			continue
		}
		joined := current + "\n\n" + unit
		if len([]rune(joined)) <= telegramAnswerChunkRunes {
			current = joined
			continue
		}
		messages = append(messages, current)
		current = unit
	}
	if current != "" {
		messages = append(messages, current)
	}
	return balanceTelegramBoldMarkers(messages)
}

func balanceTelegramBoldMarkers(messages []string) []string {
	balanced := make([]string, len(messages))
	open := false
	for i, message := range messages {
		prefix := ""
		if open {
			prefix = "**"
		}
		state := open
		for remaining := message; ; {
			marker := strings.Index(remaining, "**")
			if marker < 0 {
				break
			}
			state = !state
			remaining = remaining[marker+2:]
		}
		suffix := ""
		if state {
			suffix = "**"
		}
		balanced[i] = prefix + message + suffix
		open = state
	}
	return balanced
}

func splitTelegramLongLine(line string) []string {
	runes := []rune(line)
	var sentences []string
	start := 0
	for i, r := range runes {
		if r != '.' && r != '?' && r != '!' {
			continue
		}
		if i+1 < len(runes) && !unicode.IsSpace(runes[i+1]) {
			continue
		}
		if sentence := strings.TrimSpace(string(runes[start : i+1])); sentence != "" {
			sentences = append(sentences, sentence)
		}
		start = i + 1
	}
	if tail := strings.TrimSpace(string(runes[start:])); tail != "" {
		sentences = append(sentences, tail)
	}

	var chunks []string
	current := ""
	for _, sentence := range sentences {
		if len([]rune(sentence)) > telegramAnswerChunkRunes {
			if current != "" {
				chunks = append(chunks, current)
				current = ""
			}
			chunks = append(chunks, splitTelegramWords(sentence)...)
			continue
		}
		joined := sentence
		if current != "" {
			joined = current + " " + sentence
		}
		if len([]rune(joined)) <= telegramAnswerChunkRunes {
			current = joined
			continue
		}
		chunks = append(chunks, current)
		current = sentence
	}
	if current != "" {
		chunks = append(chunks, current)
	}
	return chunks
}

func splitTelegramWords(text string) []string {
	var chunks []string
	current := ""
	for _, word := range strings.Fields(text) {
		joined := word
		if current != "" {
			joined = current + " " + word
		}
		if len([]rune(joined)) <= telegramAnswerChunkRunes {
			current = joined
			continue
		}
		if current != "" {
			chunks = append(chunks, current)
		}
		current = word
		if len([]rune(current)) > telegramAnswerChunkRunes {
			runes := []rune(current)
			for len(runes) > telegramAnswerChunkRunes {
				chunks = append(chunks, string(runes[:telegramAnswerChunkRunes]))
				runes = runes[telegramAnswerChunkRunes:]
			}
			current = string(runes)
		}
	}
	if current != "" {
		chunks = append(chunks, current)
	}
	return chunks
}

// telegramHTML supports the bold Markdown emitted by the secretary and escapes
// every other character so model output cannot become Telegram markup.
func telegramHTML(text string) string {
	var out strings.Builder
	for len(text) > 0 {
		start := strings.Index(text, "**")
		if start < 0 {
			out.WriteString(html.EscapeString(text))
			break
		}
		out.WriteString(html.EscapeString(text[:start]))
		afterStart := text[start+2:]
		end := strings.Index(afterStart, "**")
		if end <= 0 {
			out.WriteString("**")
			text = afterStart
			continue
		}
		out.WriteString("<b>")
		out.WriteString(html.EscapeString(afterStart[:end]))
		out.WriteString("</b>")
		text = afterStart[end+2:]
	}
	return out.String()
}

// processUpdate applies authorization, text and voice handling to queued updates.
func (b *Bot) processUpdate(ctx context.Context, update Update) (returnErr error) {
	ctx = withConversationTrace(ctx, b.Service.diagnostics, fmt.Sprintf("telegram-%d", update.ID))
	done := conversationStep(ctx, "telegram.process")
	defer func() { done(returnErr) }()
	if update.CallbackQuery != nil {
		return b.processJiraCallback(ctx, update.CallbackQuery)
	}
	authDone := conversationStep(ctx, "authorization")
	if update.Message == nil || !b.Service.Permitted(*update.Message) {
		authDone(errors.New("rejected"))
		b.event("telegram_rejected")
		return nil
	}
	authDone(nil)
	m := update.Message
	if m.audioFile() != nil {
		tracker := b.progressTracker(ctx, m.Chat.ID)
		defer tracker.Close()
		tracker.SetStage("descargando el audio")
		if err := b.processVoiceWithProgress(ctx, m, tracker); err != nil {
			b.event("telegram_voice_error")
			return err
		}
		return nil
	}
	if strings.TrimSpace(m.Text) == "" {
		if err := b.send(ctx, m.Chat.ID, "Aún no puedo leer este tipo de archivo. Envíame una nota de voz o escribe el texto."); err != nil {
			b.event("telegram_send_error")
			return err
		}
		b.event("telegram_reply_sent")
		return nil
	}
	if j := b.Service.Journal; j != nil && m.ReplyTo != nil && m.Chat.ID == m.From.ID {
		if recorded, err := j.Answer(m.ReplyTo.ID, m.Text, time.Now()); err != nil {
			b.event("journal_state_error")
			return b.send(ctx, m.Chat.ID, "No he podido apuntar tu respuesta. Reenvíala.")
		} else if recorded {
			b.event("journal_answer_recorded")
			return b.send(ctx, m.Chat.ID, "Anotado.")
		}
	}
	if b.Service.JiraActions != nil {
		principal := JiraActionPrincipal{OperatorID: m.From.ID, Channel: "telegram", ConversationID: strconv.FormatInt(m.Chat.ID, 10)}
		proposal, handled, err := b.Service.ClarifyJiraTaskProposal(ctx, principal, m.Text)
		if handled {
			if err != nil {
				return b.send(ctx, m.Chat.ID, "No he podido continuar la propuesta de Jira. No he escrito nada.")
			}
			if proposal.Preview != nil {
				return b.sendJiraActionPreview(ctx, m.Chat.ID, *proposal.Preview)
			}
			return b.send(ctx, m.Chat.ID, proposal.Message)
		}
	}
	_ = b.call(ctx, "sendChatAction", map[string]any{"chat_id": m.Chat.ID, "action": "typing"}, nil)
	observer, tracker := b.interactionObserver(ctx, m.Chat.ID, m.ID)
	defer tracker.Close()
	answer, err := b.Service.reply(ctx, m.From.ID, "telegram", strconv.FormatInt(m.Chat.ID, 10), m.Text, nil, nil, observer)
	if err != nil {
		b.event("model_reply_error")
		answer = "No he podido completar la respuesta. Reenvía tu consulta; no he ejecutado acciones externas."
	}
	tracker.Close()
	if err := b.sendAnswer(ctx, m.Chat.ID, m.From.ID, answer); err != nil {
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
	go b.runTimerScheduler(ctx)
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
		updateCtx := withConversationTrace(ctx, b.Service.diagnostics, fmt.Sprintf("telegram-%d", update.ID))
		if err := b.processUpdate(updateCtx, update); err != nil {
			if ctx.Err() != nil {
				return nil
			}
			if !waitTelegramRetry(ctx) {
				return nil
			}
			continue
		}
		queueDone := conversationStep(updateCtx, "queue.complete")
		completeErr := queue.Complete(update.ID)
		queueDone(completeErr)
		if completeErr != nil {
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

func (b *Bot) processVoice(ctx context.Context, message *Message) error {
	var tracker *progressTracker
	if message != nil {
		tracker = b.progressTracker(ctx, message.Chat.ID)
		tracker.SetStage("recibiendo la nota de voz")
		defer tracker.Close()
	}
	return b.processVoiceWithProgress(ctx, message, tracker)
}

func (b *Bot) processVoiceWithProgress(ctx context.Context, message *Message, tracker *progressTracker) (returnErr error) {
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
	if message == nil || message.audioFile() == nil || !b.Service.Permitted(*message) {
		return errors.New("unauthorized voice message")
	}
	if !b.TranscriptionEnabled {
		requestResult = VoiceResultError
		return b.sendVoiceFallback(ctx, message.Chat.ID, "La transcripción de audio está desactivada ahora mismo. Envíame el texto.", errors.New("voice handling is disabled"))
	}
	if b.Speech == nil || b.Audio == nil {
		requestResult = VoiceResultError
		return b.sendVoiceFallback(ctx, message.Chat.ID, "No puedo transcribir el audio ahora mismo. Envíame el texto.", errors.New("voice response is unavailable"))
	}
	voice := message.audioFile()
	maxDuration := b.VoiceMaxDuration
	if maxDuration <= 0 {
		maxDuration = 120 * time.Second
	}
	if voice.FileID == "" || voice.Duration <= 0 || time.Duration(voice.Duration) > maxDuration/time.Second || voice.FileSize < 0 || voice.FileSize > maxTelegramFileBytes {
		requestResult = VoiceResultError
		return b.sendVoiceFallback(ctx, message.Chat.ID, "No he podido procesar ese audio. Envíame una nota de voz más corta o escribe el texto.", errors.New("invalid or oversized Telegram audio metadata"))
	}
	tracker.SetStage("descargando el audio")
	var file struct {
		FilePath string `json:"file_path"`
	}
	if err := b.call(ctx, "getFile", map[string]any{"file_id": voice.FileID}, &file); err != nil {
		return b.sendVoiceFallback(ctx, message.Chat.ID, "No he podido descargar el audio de Telegram. Reenvíalo o escríbeme la consulta.", errors.New("telegram voice download failed"))
	}
	fileURL, err := telegramFileURL(b.Base, b.Token, file.FilePath)
	if err != nil {
		return b.sendVoiceFallback(ctx, message.Chat.ID, "No he podido descargar el audio de Telegram. Reenvíalo o escríbeme la consulta.", errors.New("telegram voice download failed"))
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, fileURL, nil)
	if err != nil {
		return b.sendVoiceFallback(ctx, message.Chat.ID, "No he podido descargar el audio de Telegram. Reenvíalo o escríbeme la consulta.", errors.New("telegram voice download failed"))
	}
	client := *b.Client
	baseURL, _ := url.Parse(b.Base)
	client.CheckRedirect = func(request *http.Request, via []*http.Request) error {
		if len(via) >= 5 || request.URL.Scheme != baseURL.Scheme || !strings.EqualFold(request.URL.Host, baseURL.Host) {
			return http.ErrUseLastResponse
		}
		return nil
	}
	downloadDone := conversationStep(ctx, "audio.download")
	response, err := conversationHTTP(ctx, &client, request, "audio.download")
	downloadDone(err)
	if err != nil {
		return b.sendVoiceFallback(ctx, message.Chat.ID, "No he podido descargar el audio de Telegram. Reenvíalo o escríbeme la consulta.", errors.New("telegram voice download failed"))
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK || response.ContentLength > maxTelegramFileBytes {
		return b.sendVoiceFallback(ctx, message.Chat.ID, "No he podido descargar el audio de Telegram. Reenvíalo o escríbeme la consulta.", errors.New("telegram voice download failed"))
	}
	ogg, err := io.ReadAll(io.LimitReader(response.Body, maxTelegramFileBytes+1))
	if len(ogg) > maxTelegramFileBytes {
		requestResult = VoiceResultError
		return b.sendVoiceFallback(ctx, message.Chat.ID, "El audio supera el límite de tamaño. Envíame una nota más corta o escribe el texto.", errors.New("Telegram audio exceeds the size limit"))
	}
	if err != nil || len(ogg) == 0 {
		return b.sendVoiceFallback(ctx, message.Chat.ID, "No he podido descargar el audio de Telegram. Reenvíalo o escríbeme la consulta.", errors.New("telegram voice download failed"))
	}
	sttStarted := time.Now()
	tracker.SetStage("transcribiendo la nota de voz")
	convertDone := conversationStep(ctx, "audio.convert")
	wav, sttErr := b.Audio.ToWAV(ctx, ogg)
	convertDone(sttErr)
	if sttErr != nil {
		b.observeVoicePhase(ctx, VoicePhaseSTT, sttStarted, sttErr)
		requestResult = voiceMetricResult(sttErr)
		sendStarted := time.Now()
		sendErr := b.send(ctx, message.Chat.ID, "No he podido entender la nota de voz. ¿Puedes enviar otra con más claridad?")
		b.observeVoicePhase(ctx, VoicePhaseTelegramSend, sendStarted, sendErr)
		return sendErr
	}
	transcribeDone := conversationStep(ctx, "audio.transcribe")
	transcript, sttErr := b.Speech.Transcribe(ctx, wav)
	transcribeDone(sttErr)
	if sttErr == nil && strings.TrimSpace(transcript) == "" {
		sttErr = errors.New("empty speech transcript")
	}
	b.observeVoicePhase(ctx, VoicePhaseSTT, sttStarted, sttErr)
	var unsure *LowConfidenceError
	if errors.As(sttErr, &unsure) {
		// Whisper was guessing. Searching Jira with a guess is worse than asking again,
		// so show what it thought it heard and ask for a repeat.
		requestResult = VoiceResultError
		sendStarted := time.Now()
		sendErr := b.send(ctx, message.Chat.ID, "No estoy segura de haberte entendido; creo que has dicho «"+unsure.Transcript+"». ¿Me lo repites o me lo escribes?")
		b.observeVoicePhase(ctx, VoicePhaseTelegramSend, sendStarted, sendErr)
		return sendErr
	}
	if sttErr != nil {
		requestResult = voiceMetricResult(sttErr)
		sendStarted := time.Now()
		sendErr := b.send(ctx, message.Chat.ID, "No he podido entender la nota de voz. ¿Puedes enviar otra con más claridad?")
		b.observeVoicePhase(ctx, VoicePhaseTelegramSend, sendStarted, sendErr)
		return sendErr
	}
	// Show what was understood, so a recognition mistake is visible instead of turning
	// into a confusing answer.
	_ = b.send(ctx, message.Chat.ID, "Te he entendido: «"+transcript+"»")
	tracker.Close()
	observer, interactionTracker := b.interactionObserver(ctx, message.Chat.ID, message.ID)
	defer interactionTracker.Close()
	llmStarted := time.Now()
	answer, llmErr := b.Service.reply(ctx, message.From.ID, "telegram", strconv.FormatInt(message.Chat.ID, 10), transcript, nil, nil, observer)
	b.observeVoicePhase(ctx, VoicePhaseLLM, llmStarted, llmErr)
	if llmErr != nil {
		requestResult = voiceMetricResult(llmErr)
		answer = "No he podido completar la respuesta. Reenvía tu consulta por texto; no he ejecutado acciones externas."
	}
	sendStarted := time.Now()
	sendErr := b.sendAnswer(ctx, message.Chat.ID, message.From.ID, answer)
	b.observeVoicePhase(ctx, VoicePhaseTelegramSend, sendStarted, sendErr)
	return sendErr
}

func (b *Bot) sendVoiceFallback(ctx context.Context, chat int64, message string, cause error) error {
	if err := b.send(ctx, chat, message); err != nil {
		return err
	}
	return cause
}

func (b *Bot) interactionObserver(ctx context.Context, chat, messageID int64) (InteractionObserver, *interactionProgressTracker) {
	render := func(event InteractionEvent) {
		if emoji := interactionEmoji(event); emoji != "" {
			payload := map[string]any{
				"chat_id":    chat,
				"message_id": messageID,
				"reaction":   []map[string]string{{"type": "emoji", "emoji": emoji}},
			}
			if err := b.call(ctx, "setMessageReaction", payload, nil); err != nil {
				b.event("telegram_reaction_error")
			}
		}
		if message := interactionProgressText(event); message != "" {
			if err := b.send(ctx, chat, message); err != nil {
				b.event("telegram_send_error")
			}
		}
	}
	tracker := newInteractionProgressTracker(b.ProgressDelay, render)
	return tracker.Observe, tracker
}

func (b *Bot) sendVoiceOnly(ctx context.Context, chat int64, text string) error {
	if b.Speech == nil || b.Audio == nil {
		return errors.New("voice response is unavailable")
	}
	started := time.Now()
	wav, err := b.Speech.Synthesize(ctx, text)
	var ogg []byte
	if err == nil {
		ogg, err = b.Audio.ToOGGOpus(ctx, wav)
	}
	b.observeVoicePhase(ctx, VoicePhaseTTS, started, err)
	if err != nil {
		return errors.New("telegram speech synthesis failed")
	}
	return b.sendVoice(ctx, chat, ogg)
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
		if err := b.send(ctx, timer.ChatID, timer.alertText()); err != nil {
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
