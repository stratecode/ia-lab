package secretary

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	maxTranscriptionUpload = 25 << 20
	maxSpeechInputRunes    = 4000
)

// handleTranscription is the OpenAI-compatible speech-to-text endpoint that
// Open WebUI's call mode uses. It is stateless: the audio is converted in
// memory, sent to the local Whisper and never stored or logged.
func (s *Service) handleTranscription(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST required", http.StatusMethodNotAllowed)
		return
	}
	if s.Voice == nil || s.VoiceAudio == nil {
		http.Error(w, "speech recognition is not configured", http.StatusServiceUnavailable)
		return
	}
	started := time.Now()
	r.Body = http.MaxBytesReader(w, r.Body, maxTranscriptionUpload)
	if err := r.ParseMultipartForm(8 << 20); err != nil {
		http.Error(w, "multipart audio upload required", http.StatusBadRequest)
		return
	}
	file, _, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "audio file required", http.StatusBadRequest)
		return
	}
	defer file.Close()
	audio, err := io.ReadAll(io.LimitReader(file, maxTranscriptionUpload))
	if err != nil || len(audio) == 0 {
		http.Error(w, "audio file required", http.StatusBadRequest)
		return
	}
	metrics := s.VoiceMetrics()
	wav, err := s.VoiceAudio.ToWAV(r.Context(), audio)
	if err != nil {
		metrics.ObservePhase(VoicePhaseSTT, VoiceResultError, time.Since(started))
		http.Error(w, "unsupported audio", http.StatusUnprocessableEntity)
		return
	}
	text, err := s.Voice.Transcribe(r.Context(), wav)
	var unsure *LowConfidenceError
	if errors.As(err, &unsure) {
		metrics.ObservePhase(VoicePhaseSTT, VoiceResultError, time.Since(started))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "No te he entendido bien; creo que has dicho: " + unsure.Transcript + ". Repítelo.", "text": ""})
		return
	}
	if err != nil {
		metrics.ObservePhase(VoicePhaseSTT, VoiceResultError, time.Since(started))
		http.Error(w, "transcription failed", http.StatusBadGateway)
		return
	}
	metrics.ObservePhase(VoicePhaseSTT, VoiceResultOK, time.Since(started))
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"text": text})
}

// handleSpeech is the OpenAI-compatible text-to-speech endpoint. The text is
// rewritten to be spoken (numbers, issue keys, dates, no Markdown) before it
// reaches Piper. The reply is a WAV file; response_format is accepted and ignored.
func (s *Service) handleSpeech(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST required", http.StatusMethodNotAllowed)
		return
	}
	if s.Voice == nil {
		http.Error(w, "speech synthesis is not configured", http.StatusServiceUnavailable)
		return
	}
	var request struct {
		Input string `json:"input"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&request); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	text := strings.TrimSpace(request.Input)
	if text == "" {
		http.Error(w, "input required", http.StatusBadRequest)
		return
	}
	if runes := []rune(text); len(runes) > maxSpeechInputRunes {
		text = string(runes[:maxSpeechInputRunes])
	}
	spoken := SpokenText(text)
	if spoken == "" {
		http.Error(w, "nothing to say", http.StatusUnprocessableEntity)
		return
	}
	started := time.Now()
	audio, err := s.Voice.Synthesize(r.Context(), spoken)
	if err != nil {
		s.VoiceMetrics().ObservePhase(VoicePhaseTTS, VoiceResultError, time.Since(started))
		http.Error(w, "synthesis failed", http.StatusBadGateway)
		return
	}
	s.VoiceMetrics().ObservePhase(VoicePhaseTTS, VoiceResultOK, time.Since(started))
	w.Header().Set("Content-Type", "audio/wav")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(audio)
}
