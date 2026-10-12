package secretary

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestVoiceMetricsExposeDurationsAndBoundedLabels(t *testing.T) {
	metrics := NewVoiceMetrics()
	for _, phase := range []VoicePhase{VoicePhaseSTT, VoicePhaseLLM, VoicePhaseTTS, VoicePhaseTelegramSend} {
		metrics.ObservePhase(phase, VoiceResultOK, 125*time.Millisecond)
	}
	metrics.ObservePhase(VoicePhase("transcript-marker"), VoiceResult("user-id-marker"), 250*time.Millisecond)
	metrics.ObserveRequest(VoiceResultError, 500*time.Millisecond)
	metrics.ObserveRequest(VoiceResult("filename-marker"), time.Second)

	response := httptest.NewRecorder()
	metrics.Handler().ServeHTTP(response, httptest.NewRequest("GET", "/metrics", nil))
	body := response.Body.String()
	for _, want := range []string{
		`lab_secretary_voice_phase_duration_seconds_count{phase="stt"}`,
		`lab_secretary_voice_phase_calls_total{phase="llm",result="ok"}`,
		`lab_secretary_voice_phase_calls_total{phase="other",result="other"}`,
		`lab_secretary_voice_request_duration_seconds_count{result="error"}`,
		`lab_secretary_voice_request_duration_seconds_count{result="other"}`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("metrics missing %q:\n%s", want, body)
		}
	}
	for _, forbidden := range []string{"transcript-marker", "user-id-marker", "filename-marker", "bot-token-marker", "chat-id-marker"} {
		if strings.Contains(body, forbidden) {
			t.Errorf("metric exposition leaked %q", forbidden)
		}
	}
}

func TestVoiceFlowObservesBoundedPhaseMetrics(t *testing.T) {
	bot, speech, _, _ := newVoiceTestBot(t, voiceTestConfig{voiceEnabled: true, fileBytes: []byte("ogg"), fileSize: 3, duration: 3})
	speech.transcript = "transcript-marker"
	bot.Metrics = NewVoiceMetrics()
	message := &Message{From: User{ID: 7}, Chat: Chat{ID: 7, Type: "private"}, Voice: &Voice{FileID: "file-1", Duration: 3, FileSize: 3}}
	if err := bot.processVoice(context.Background(), message); err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	bot.Metrics.Handler().ServeHTTP(response, httptest.NewRequest("GET", "/metrics", nil))
	body := response.Body.String()
	for _, want := range []string{
		`lab_secretary_voice_phase_calls_total{phase="stt",result="ok"} 1`,
		`lab_secretary_voice_phase_calls_total{phase="llm",result="ok"} 1`,
		`lab_secretary_voice_phase_calls_total{phase="tts",result="ok"} 1`,
		`lab_secretary_voice_phase_calls_total{phase="telegram_send",result="ok"} 2`,
		`lab_secretary_voice_request_duration_seconds_count{result="ok"} 1`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("flow metrics missing %q:\n%s", want, body)
		}
	}
	for _, forbidden := range []string{"transcript-marker", "secret-token", "voice.ogg", "chat_id=", "user_id="} {
		if strings.Contains(body, forbidden) {
			t.Errorf("flow metrics leaked %q", forbidden)
		}
	}
}
