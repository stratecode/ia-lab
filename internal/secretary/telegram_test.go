package secretary

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestVoiceUpdateUnmarshalsTelegramVoice(t *testing.T) {
	var update Update
	if err := json.Unmarshal([]byte(`{"update_id":4,"message":{"message_id":3,"from":{"id":7},"chat":{"id":7,"type":"private"},"voice":{"file_id":"file-1","duration":8,"file_size":1234}}}`), &update); err != nil {
		t.Fatal(err)
	}
	if update.Message == nil || update.Message.Voice == nil || update.Message.Voice.FileID != "file-1" || update.Message.Voice.Duration != 8 || update.Message.Voice.FileSize != 1234 {
		t.Fatalf("voice update not decoded: %+v", update)
	}
}

func TestTelegramRendersInteractionReactionAndProgress(t *testing.T) {
	for meaning, want := range map[InteractionMeaning]string{
		InteractionLookup:    "🔍",
		InteractionAgreement: "✅",
		InteractionAction:    "👍",
	} {
		if got := telegramReaction(meaning); got != want {
			t.Errorf("telegramReaction(%q) = %q, want %q", meaning, got, want)
		}
	}

	type apiRequest struct {
		method  string
		payload map[string]json.RawMessage
	}
	var requests []apiRequest
	telegramAPI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var payload map[string]json.RawMessage
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Errorf("invalid Telegram payload: %v", err)
		}
		method := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
		requests = append(requests, apiRequest{method: method, payload: payload})
		io.WriteString(w, `{"ok":true,"result":true}`)
	}))
	defer telegramAPI.Close()
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"choices":[{"message":{"content":"Hay dos tareas importantes."}}]}`)
	}))
	defer model.Close()
	service := New(model.URL, "model-key", "instructions", t.TempDir(), map[int64]bool{7: true})
	service.Capabilities = []ReadCapability{{
		Name: "Jira", Description: "tareas actuales",
		Match: func(query string) bool { return strings.Contains(strings.ToLower(query), "jira") },
		Read: func(context.Context, string) (CapabilityResult, error) {
			return CapabilityResult{Text: "Tarea A y tarea B.", ResultCount: 1}, nil
		},
	}}
	bot := &Bot{Base: telegramAPI.URL, Token: "bot-token", Client: telegramAPI.Client(), Service: service}
	update := Update{Message: &Message{ID: 42, From: User{ID: 7}, Chat: Chat{ID: 7, Type: "private"}, Text: "¿Qué tareas importantes tengo en Jira?"}}
	if err := bot.processUpdate(context.Background(), update); err != nil {
		t.Fatal(err)
	}

	var reactionIndex, progressIndex, finalIndex = -1, -1, -1
	reactionCount := 0
	for i, request := range requests {
		switch request.method {
		case "setMessageReaction":
			reactionCount++
			reactionIndex = i
			var chatID, messageID int64
			_ = json.Unmarshal(request.payload["chat_id"], &chatID)
			_ = json.Unmarshal(request.payload["message_id"], &messageID)
			var reaction []map[string]string
			_ = json.Unmarshal(request.payload["reaction"], &reaction)
			if chatID != 7 || messageID != 42 || len(reaction) != 1 || reaction[0]["emoji"] != "🔍" || reaction[0]["type"] != "emoji" {
				t.Fatalf("invalid reaction target/payload: chat=%d message=%d reaction=%v", chatID, messageID, reaction)
			}
		case "sendMessage":
			var text string
			_ = json.Unmarshal(request.payload["text"], &text)
			if strings.Contains(text, "Déjame revisar Jira.") {
				progressIndex = i
			}
			if strings.Contains(text, "Hay dos tareas importantes.") {
				finalIndex = i
			}
			if strings.Contains(text, "Recibido") || strings.Contains(text, "voy enseguida") {
				t.Fatalf("generic acknowledgement should not be sent: %q", text)
			}
		}
	}
	if reactionCount != 1 || reactionIndex < 0 || progressIndex <= reactionIndex || finalIndex <= progressIndex {
		t.Fatalf("reaction/progress/final order invalid: count=%d reaction=%d progress=%d final=%d requests=%+v", reactionCount, reactionIndex, progressIndex, finalIndex, requests)
	}
}

func TestUnauthorizedTelegramMessageHasNoSideEffects(t *testing.T) {
	var apiCalls, modelCalls int
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		apiCalls++
		io.WriteString(w, `{"ok":true,"result":true}`)
	}))
	defer api.Close()
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		modelCalls++
		io.WriteString(w, `{"choices":[{"message":{"content":"No."}}]}`)
	}))
	defer model.Close()
	service := New(model.URL, "model-key", "instructions", t.TempDir(), map[int64]bool{7: true})
	bot := &Bot{Base: api.URL, Token: "bot-token", Client: api.Client(), Service: service}
	update := Update{Message: &Message{ID: 50, From: User{ID: 8}, Chat: Chat{ID: 8, Type: "private"}, Text: "¿Puedes revisar Jira?"}}
	if err := bot.processUpdate(context.Background(), update); err != nil {
		t.Fatal(err)
	}
	if apiCalls != 0 || modelCalls != 0 {
		t.Fatalf("unauthorized update caused side effects: Telegram=%d model=%d", apiCalls, modelCalls)
	}
}

func TestTelegramReactionFailureStillDeliversReply(t *testing.T) {
	var methods []string
	var events []string
	telegramAPI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
		methods = append(methods, method)
		if method == "setMessageReaction" {
			http.Error(w, `{"ok":false,"description":"reaction unavailable"}`, http.StatusBadRequest)
			return
		}
		io.WriteString(w, `{"ok":true,"result":true}`)
	}))
	defer telegramAPI.Close()
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"choices":[{"message":{"content":"Resultado verificado."}}]}`)
	}))
	defer model.Close()
	service := New(model.URL, "model-key", "instructions", t.TempDir(), map[int64]bool{7: true})
	service.Capabilities = []ReadCapability{{
		Name: "Correo", Description: "mensajes recientes",
		Match: func(query string) bool { return strings.Contains(strings.ToLower(query), "correo") },
		Read: func(context.Context, string) (CapabilityResult, error) {
			return CapabilityResult{Text: "Un correo pendiente.", ResultCount: 1}, nil
		},
	}}
	bot := &Bot{Base: telegramAPI.URL, Token: "bot-token", Client: telegramAPI.Client(), Service: service, OnEvent: func(name string) { events = append(events, name) }}
	update := Update{Message: &Message{ID: 43, From: User{ID: 7}, Chat: Chat{ID: 7, Type: "private"}, Text: "¿Tengo algún correo pendiente?"}}
	if err := bot.processUpdate(context.Background(), update); err != nil {
		t.Fatalf("reaction failure should not abort the reply: %v", err)
	}
	if !strings.Contains(strings.Join(methods, ","), "setMessageReaction") {
		t.Fatalf("reaction endpoint was not attempted: %v", methods)
	}
	if eventsCount(events, "telegram_reaction_error") != 1 || eventsCount(events, "telegram_reply_sent") != 1 {
		t.Fatalf("reaction failure should be isolated and reply delivered: %v", events)
	}
	if eventsCount(methods, "sendMessage") < 2 {
		t.Fatalf("expected contextual progress and final answer despite reaction failure: %v", methods)
	}
}

func eventsCount(values []string, want string) int {
	count := 0
	for _, value := range values {
		if value == want {
			count++
		}
	}
	return count
}

type voiceTestSpeech struct {
	transcript string
	wav        []byte
	transErr   error
	synthErr   error
	events     *[]string
	transCalls int
	synthCalls int
}

func (s *voiceTestSpeech) Transcribe(context.Context, []byte) (string, error) {
	s.transCalls++
	if s.transErr != nil {
		return "", s.transErr
	}
	return s.transcript, nil
}

func (s *voiceTestSpeech) Synthesize(context.Context, string) ([]byte, error) {
	s.synthCalls++
	if s.events != nil {
		*s.events = append(*s.events, "synthesize")
	}
	if s.synthErr != nil {
		return nil, s.synthErr
	}
	return s.wav, nil
}

type voiceTestAudio struct {
	toWAVErr   error
	toOGGErr   error
	toWAVCalls int
	toOGGCalls int
}

func (a *voiceTestAudio) ToWAV(context.Context, []byte) ([]byte, error) {
	a.toWAVCalls++
	if a.toWAVErr != nil {
		return nil, a.toWAVErr
	}
	return []byte("wav-input"), nil
}

func (a *voiceTestAudio) ToOGGOpus(context.Context, []byte) ([]byte, error) {
	a.toOGGCalls++
	if a.toOGGErr != nil {
		return nil, a.toOGGErr
	}
	return []byte("ogg-reply"), nil
}

type voiceTestState struct {
	events       []string
	modelCalls   int
	getFileCalls int
	downloads    int
	textReplies  []string
	parseModes   []string
	voiceReplies int
}

type voiceTestConfig struct {
	voiceEnabled bool
	fileBytes    []byte
	fileSize     int64
	duration     int
	apiFailure   string
	transErr     error
	synthErr     error
	convertErr   error
}

func newVoiceTestBot(t *testing.T, cfg voiceTestConfig) (*Bot, *voiceTestSpeech, *voiceTestAudio, *voiceTestState) {
	t.Helper()
	state := &voiceTestState{}
	speech := &voiceTestSpeech{transcript: "¿Qué tengo pendiente hoy?", wav: []byte("RIFF\x00\x00\x00\x00WAVErespuesta"), transErr: cfg.transErr, synthErr: cfg.synthErr, events: &state.events}
	audio := &voiceTestAudio{}
	if cfg.convertErr != nil {
		audio.toWAVErr = cfg.convertErr
	}
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		state.modelCalls++
		var payload struct {
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("decode model request: %v", err)
		}
		if len(payload.Messages) == 0 || payload.Messages[len(payload.Messages)-1].Content != speech.transcript {
			t.Errorf("transcript was not passed to model: %+v", payload.Messages)
		}
		io.WriteString(w, `{"choices":[{"message":{"content":"Respuesta de prueba"}}]}`)
	}))
	t.Cleanup(model.Close)
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if cfg.apiFailure != "" && strings.HasSuffix(r.URL.Path, "/"+cfg.apiFailure) {
			http.Error(w, "private api detail", http.StatusInternalServerError)
			return
		}
		switch r.Method + " " + strings.TrimPrefix(r.URL.Path, "/botsecret-token/") {
		case "POST getFile":
			state.getFileCalls++
			state.events = append(state.events, "getFile")
			io.WriteString(w, `{"ok":true,"result":{"file_path":"voice/file_1.oga"}}`)
		case "GET /file/botsecret-token/voice/file_1.oga":
			state.downloads++
			state.events = append(state.events, "download")
			w.Write(cfg.fileBytes)
		case "POST sendMessage":
			var body struct {
				Text      string `json:"text"`
				ParseMode string `json:"parse_mode"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decode sendMessage: %v", err)
			}
			state.textReplies = append(state.textReplies, body.Text)
			state.parseModes = append(state.parseModes, body.ParseMode)
			state.events = append(state.events, "sendMessage")
			io.WriteString(w, `{"ok":true,"result":{}}`)
		case "POST sendVoice":
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				t.Errorf("parse sendVoice multipart: %v", err)
			}
			file, header, err := r.FormFile("voice")
			if err != nil {
				t.Errorf("sendVoice file: %v", err)
			} else {
				data, readErr := io.ReadAll(file)
				file.Close()
				if readErr != nil || string(data) != "ogg-reply" || header.Filename != "voice.ogg" {
					t.Errorf("unexpected voice payload %q, filename %q, err %v", data, header.Filename, readErr)
				}
			}
			state.voiceReplies++
			state.events = append(state.events, "sendVoice")
			io.WriteString(w, `{"ok":true,"result":{}}`)
		default:
			t.Errorf("unexpected Telegram request %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(api.Close)
	service := New(model.URL, "model-key", "instructions", t.TempDir(), map[int64]bool{7: true})
	bot := &Bot{Base: api.URL, Token: "secret-token", Client: api.Client(), Service: service, Speech: speech, Audio: audio, TranscriptionEnabled: cfg.voiceEnabled}
	return bot, speech, audio, state
}

func TestUnauthorizedVoiceDoesNotDownloadFile(t *testing.T) {
	bot, speech, _, state := newVoiceTestBot(t, voiceTestConfig{voiceEnabled: true, fileBytes: []byte("ogg"), fileSize: 3, duration: 3})
	message := &Message{From: User{ID: 9}, Chat: Chat{ID: 9, Type: "private"}, Voice: &Voice{FileID: "unauthorized", Duration: 3, FileSize: 3}}
	if err := bot.processVoice(context.Background(), message); err == nil {
		t.Fatal("unauthorized message was accepted")
	}
	if state.getFileCalls != 0 || state.downloads != 0 || speech.transCalls != 0 || state.modelCalls != 0 {
		t.Fatalf("unauthorized voice triggered work: state=%+v transcript calls=%d", state, speech.transCalls)
	}
}

func TestVoiceDisabledDoesNotDownloadOrCallInference(t *testing.T) {
	bot, speech, _, state := newVoiceTestBot(t, voiceTestConfig{fileBytes: []byte("ogg"), fileSize: 3, duration: 3})
	message := &Message{From: User{ID: 7}, Chat: Chat{ID: 7, Type: "private"}, Voice: &Voice{FileID: "file-1", Duration: 3, FileSize: 3}}
	if err := bot.processVoice(context.Background(), message); err == nil {
		t.Fatal("disabled voice should be rejected")
	}
	if state.getFileCalls != 0 || state.downloads != 0 || speech.transCalls != 0 || state.modelCalls != 0 || len(state.textReplies) != 0 || state.voiceReplies != 0 {
		t.Fatalf("disabled voice triggered work or sent a different modality: state=%+v speech=%d", state, speech.transCalls)
	}
}

func TestVoiceRejectsOversizedAndOverDurationUpdates(t *testing.T) {
	tests := []struct {
		name string
		cfg  voiceTestConfig
	}{
		{name: "oversized metadata", cfg: voiceTestConfig{voiceEnabled: true, fileBytes: []byte("ogg"), fileSize: 20*1024*1024 + 1, duration: 3}},
		{name: "oversized downloaded body", cfg: voiceTestConfig{voiceEnabled: true, fileBytes: make([]byte, 20*1024*1024+1), fileSize: 0, duration: 3}},
		{name: "over duration", cfg: voiceTestConfig{voiceEnabled: true, fileBytes: []byte("ogg"), fileSize: 3, duration: 121}},
		{name: "duration integer overflow", cfg: voiceTestConfig{voiceEnabled: true, fileBytes: []byte("ogg"), fileSize: 3, duration: int(^uint(0) >> 1)}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bot, speech, _, state := newVoiceTestBot(t, tt.cfg)
			message := &Message{From: User{ID: 7}, Chat: Chat{ID: 7, Type: "private"}, Voice: &Voice{FileID: "file-1", Duration: tt.cfg.duration, FileSize: tt.cfg.fileSize}}
			if err := bot.processVoice(context.Background(), message); err == nil {
				t.Fatal("invalid voice should be rejected")
			}
			if speech.transCalls != 0 || state.modelCalls != 0 || state.voiceReplies != 0 {
				t.Fatalf("rejected voice triggered inference: state=%+v speech calls=%d", state, speech.transCalls)
			}
			if len(state.textReplies) != 0 {
				t.Fatalf("rejected voice sent a text reply: %+v", state)
			}
		})
	}
}

func TestVoiceTranscribesAndRepliesWithTextOnly(t *testing.T) {
	bot, speech, audio, state := newVoiceTestBot(t, voiceTestConfig{voiceEnabled: true, fileBytes: []byte("ogg-input"), fileSize: 9, duration: 4})
	message := &Message{From: User{ID: 7}, Chat: Chat{ID: 7, Type: "private"}, Voice: &Voice{FileID: "file-1", Duration: 4, FileSize: 9}}
	if err := bot.processUpdate(context.Background(), Update{Message: message}); err != nil {
		t.Fatal(err)
	}
	wantEvents := []string{"getFile", "download", "sendMessage"}
	if strings.Join(state.events, ",") != strings.Join(wantEvents, ",") {
		t.Fatalf("flow events = %v, want %v", state.events, wantEvents)
	}
	if speech.transCalls != 1 || speech.synthCalls != 0 || audio.toWAVCalls != 1 || audio.toOGGCalls != 0 || state.modelCalls != 1 || state.voiceReplies != 0 {
		t.Fatalf("unexpected flow counts: state=%+v speech=%+v audio=%+v", state, speech, audio)
	}
	if len(state.textReplies) != 1 || strings.TrimSpace(state.textReplies[0]) == "" {
		t.Fatalf("voice request should receive one text answer: %+v", state.textReplies)
	}
	if len(state.parseModes) != 1 || state.parseModes[0] != "HTML" {
		t.Fatalf("Telegram reply did not enable safe Markdown rendering: %v", state.parseModes)
	}
}

func TestTelegramSendsDelayedProgressForSlowModel(t *testing.T) {
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(100 * time.Millisecond)
		io.WriteString(w, `{"choices":[{"message":{"content":"Respuesta lista"}}]}`)
	}))
	defer model.Close()
	var sent []string
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/sendMessage") {
			var body struct {
				Text string `json:"text"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decode Telegram message: %v", err)
			}
			sent = append(sent, body.Text)
		}
		io.WriteString(w, `{"ok":true,"result":true}`)
	}))
	defer api.Close()

	service := New(model.URL, "key", "instructions", t.TempDir(), map[int64]bool{7: true})
	bot := &Bot{Base: api.URL, Token: "token", Client: api.Client(), Service: service, ProgressDelay: 10 * time.Millisecond}
	message := &Message{From: User{ID: 7}, Chat: Chat{ID: 7, Type: "private"}, Text: "Hola"}
	if err := bot.processUpdate(context.Background(), Update{Message: message}); err != nil {
		t.Fatal(err)
	}
	if len(sent) != 2 || !strings.Contains(sent[0], "sintetizando la respuesta") || sent[1] != "Respuesta lista" {
		t.Fatalf("slow model messages = %v, want one current-stage update and final answer", sent)
	}
}

func TestVoiceSTTFailureDoesNotCallModel(t *testing.T) {
	bot, speech, _, state := newVoiceTestBot(t, voiceTestConfig{voiceEnabled: true, fileBytes: []byte("ogg"), fileSize: 3, duration: 3, transErr: io.ErrUnexpectedEOF})
	message := &Message{From: User{ID: 7}, Chat: Chat{ID: 7, Type: "private"}, Voice: &Voice{FileID: "file-1", Duration: 3, FileSize: 3}}
	if err := bot.processUpdate(context.Background(), Update{Message: message}); err != nil {
		t.Fatal(err)
	}
	if state.modelCalls != 0 || speech.synthCalls != 0 || len(state.textReplies) != 1 || state.voiceReplies != 0 {
		t.Fatalf("failed transcription should report in text without TTS: state=%+v speech=%+v", state, speech)
	}
}

func TestTelegramTextEscapesHTMLAndRendersBoldMarkdown(t *testing.T) {
	got := telegramHTML("**Importante**: <revisar> & aprobar")
	want := "<b>Importante</b>: &lt;revisar&gt; &amp; aprobar"
	if got != want {
		t.Fatalf("telegramHTML() = %q, want %q", got, want)
	}
}

func TestVoiceTransportErrorsNeverContainBotToken(t *testing.T) {
	bot, _, _, _ := newVoiceTestBot(t, voiceTestConfig{voiceEnabled: true, fileBytes: []byte("ogg"), fileSize: 3, duration: 3, apiFailure: "getFile"})
	message := &Message{From: User{ID: 7}, Chat: Chat{ID: 7, Type: "private"}, Voice: &Voice{FileID: "file-1", Duration: 3, FileSize: 3}}
	err := bot.processVoice(context.Background(), message)
	if err == nil || strings.Contains(err.Error(), "secret-token") {
		t.Fatalf("unsafe Telegram voice error: %v", err)
	}
}

func TestPollingPersistsOffsetRejectsOtherUsersAndDeliversReply(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	modelCalls := 0
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		modelCalls++
		io.WriteString(w, `{"choices":[{"message":{"content":"Respuesta real"}}]}`)
	}))
	defer model.Close()
	polls, rejected := 0, 0
	var sent []string
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch strings.TrimPrefix(r.URL.Path, "/botsecret/") {
		case "getMe":
			io.WriteString(w, `{"ok":true,"result":{"id":1}}`)
		case "getWebhookInfo":
			io.WriteString(w, `{"ok":true,"result":{"url":""}}`)
		case "getUpdates":
			polls++
			if polls == 1 {
				io.WriteString(w, `{"ok":true,"result":[{"update_id":10,"message":{"from":{"id":8},"chat":{"id":8,"type":"private"},"text":"No"}},{"update_id":11,"message":{"from":{"id":7},"chat":{"id":99,"type":"group"},"text":"No"}},{"update_id":12,"message":{"from":{"id":7},"chat":{"id":7,"type":"private"},"text":"Hola"}}]}`)
			} else {
				var body struct {
					Offset int64 `json:"offset"`
				}
				json.NewDecoder(r.Body).Decode(&body)
				if body.Offset != 13 {
					t.Errorf("checkpoint not used: %d", body.Offset)
				}
				io.WriteString(w, `{"ok":true,"result":[]}`)
				cancel()
			}
		case "sendMessage":
			var body struct {
				ChatID int64  `json:"chat_id"`
				Text   string `json:"text"`
			}
			json.NewDecoder(r.Body).Decode(&body)
			if body.ChatID != 7 {
				t.Errorf("unexpected delivery: %+v", body)
			}
			sent = append(sent, body.Text)
			io.WriteString(w, `{"ok":true,"result":{}}`)
		case "sendChatAction":
			io.WriteString(w, `{"ok":true,"result":true}`)
		default:
			t.Errorf("unexpected operation %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer api.Close()
	dir := t.TempDir()
	s := New(model.URL, "key", "instructions", dir, map[int64]bool{7: true})
	b := Bot{Base: api.URL, Token: "secret", Client: api.Client(), Service: s, OnEvent: func(event string) {
		if event == "telegram_rejected" {
			rejected++
		}
	}}
	if err := b.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if len(sent) != 1 || sent[0] != "Respuesta real" || modelCalls != 1 || rejected != 2 {
		t.Fatalf("sent %v model %d rejected %d", sent, modelCalls, rejected)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "offset.json"))
	if err != nil || string(raw) != "13" {
		t.Fatalf("offset %s %v", raw, err)
	}
	stat, _ := os.Stat(filepath.Join(dir, "telegram-7.json"))
	if stat.Mode().Perm() != 0600 {
		t.Fatal("private history permissions")
	}
}

func TestTelegramTransportErrorNeverContainsBotToken(t *testing.T) {
	b := Bot{Base: "http://127.0.0.1:1", Token: "private-token", Client: &http.Client{Timeout: time.Second}}
	err := b.call(context.Background(), "getMe", nil, nil)
	if err == nil || strings.Contains(err.Error(), "private-token") {
		t.Fatalf("unsafe error %v", err)
	}
}
