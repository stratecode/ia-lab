package secretary

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type fakeVoice struct {
	transcript string
	spoken     string
	err        error
}

func (f *fakeVoice) Transcribe(context.Context, []byte) (string, error) { return f.transcript, f.err }
func (f *fakeVoice) Synthesize(_ context.Context, text string) ([]byte, error) {
	f.spoken = text
	return testWAV(1), f.err
}

type fakeConverter struct{ err error }

func (f fakeConverter) ToWAV(_ context.Context, in []byte) ([]byte, error)   { return in, f.err }
func (fakeConverter) ToOGGOpus(_ context.Context, in []byte) ([]byte, error) { return in, nil }

// testWAV builds a mono 16 kHz 16-bit PCM WAV of the given number of seconds.
func testWAV(seconds int) []byte {
	data := make([]byte, 32000*seconds)
	header := make([]byte, 44)
	copy(header[0:], "RIFF")
	binary.LittleEndian.PutUint32(header[4:], uint32(36+len(data)))
	copy(header[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(header[16:], 16)
	binary.LittleEndian.PutUint16(header[20:], 1)
	binary.LittleEndian.PutUint16(header[22:], 1)
	binary.LittleEndian.PutUint32(header[24:], 16000)
	binary.LittleEndian.PutUint32(header[28:], 32000)
	binary.LittleEndian.PutUint16(header[32:], 2)
	binary.LittleEndian.PutUint16(header[34:], 16)
	copy(header[36:], "data")
	binary.LittleEndian.PutUint32(header[40:], uint32(len(data)))
	return append(header, data...)
}

func voiceService(voice *fakeVoice, conv fakeConverter) *Service {
	s := New("http://127.0.0.1:1", "secret-key", "instructions", "/tmp/unused", map[int64]bool{7: true})
	s.Voice, s.VoiceAudio = voice, conv
	return s
}

func upload(t *testing.T, field string) (*bytes.Buffer, string) {
	t.Helper()
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	if field != "" {
		part, _ := writer.CreateFormFile(field, "voice.webm")
		part.Write([]byte("audio-bytes"))
	}
	writer.WriteField("model", "whisper-1")
	writer.Close()
	return body, writer.FormDataContentType()
}

func do(s *Service, method, path, contentType string, body io.Reader, key string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, body)
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	if key != "" {
		request.Header.Set("Authorization", "Bearer "+key)
	}
	recorder := httptest.NewRecorder()
	s.Handler().ServeHTTP(recorder, request)
	return recorder
}

func TestAudioEndpointsRequireTheServiceKey(t *testing.T) {
	s := voiceService(&fakeVoice{transcript: "hola"}, fakeConverter{})
	body, ctype := upload(t, "file")
	if got := do(s, "POST", "/v1/audio/transcriptions", ctype, body, "").Code; got != http.StatusUnauthorized {
		t.Errorf("transcription without key = %d", got)
	}
	if got := do(s, "POST", "/v1/audio/speech", "application/json", strings.NewReader(`{"input":"hola"}`), "wrong").Code; got != http.StatusUnauthorized {
		t.Errorf("speech with a wrong key = %d", got)
	}
}

func TestTranscriptionReturnsTheOpenAIShape(t *testing.T) {
	s := voiceService(&fakeVoice{transcript: "Qué tengo pendiente en Jira"}, fakeConverter{})
	body, ctype := upload(t, "file")
	rec := do(s, "POST", "/v1/audio/transcriptions", ctype, body, "secret-key")
	if rec.Code != 200 || strings.TrimSpace(rec.Body.String()) != `{"text":"Qué tengo pendiente en Jira"}` {
		t.Fatalf("code %d body %q", rec.Code, rec.Body.String())
	}
	if !strings.HasPrefix(rec.Header().Get("Content-Type"), "application/json") {
		t.Errorf("content type = %q", rec.Header().Get("Content-Type"))
	}
}

func TestTranscriptionRejectsBadRequestsWithoutLeakingDetails(t *testing.T) {
	body, ctype := upload(t, "")
	if got := do(voiceService(&fakeVoice{}, fakeConverter{}), "POST", "/v1/audio/transcriptions", ctype, body, "secret-key").Code; got != http.StatusBadRequest {
		t.Errorf("missing file = %d", got)
	}
	body, ctype = upload(t, "file")
	if got := do(voiceService(&fakeVoice{}, fakeConverter{err: errors.New("boom")}), "POST", "/v1/audio/transcriptions", ctype, body, "secret-key").Code; got != http.StatusUnprocessableEntity {
		t.Errorf("unconvertible audio = %d", got)
	}
	body, ctype = upload(t, "file")
	rec := do(voiceService(&fakeVoice{err: errors.New("whisper down")}, fakeConverter{}), "POST", "/v1/audio/transcriptions", ctype, body, "secret-key")
	if rec.Code != http.StatusBadGateway || strings.Contains(rec.Body.String(), "whisper down") {
		t.Errorf("whisper failure = %d %q", rec.Code, rec.Body.String())
	}
	if got := do(voiceService(&fakeVoice{}, fakeConverter{}), "GET", "/v1/audio/transcriptions", "", nil, "secret-key").Code; got != http.StatusMethodNotAllowed {
		t.Errorf("GET = %d", got)
	}
	unconfigured := New("http://127.0.0.1:1", "secret-key", "i", "/tmp/unused", nil)
	body, ctype = upload(t, "file")
	if got := do(unconfigured, "POST", "/v1/audio/transcriptions", ctype, body, "secret-key").Code; got != http.StatusServiceUnavailable {
		t.Errorf("unconfigured = %d", got)
	}
}

func TestSpeechSpeaksTheNormalisedTextAndReturnsWAV(t *testing.T) {
	voice := &fakeVoice{}
	s := voiceService(voice, fakeConverter{})
	rec := do(s, "POST", "/v1/audio/speech", "application/json", strings.NewReader(`{"model":"tts-1","voice":"donna","input":"**Tienes 25 abiertas.** Mira PMI-67 el 2026-11-12."}`), "secret-key")
	if rec.Code != 200 || rec.Header().Get("Content-Type") != "audio/wav" || !validWAV(rec.Body.Bytes()) {
		t.Fatalf("code %d type %q", rec.Code, rec.Header().Get("Content-Type"))
	}
	want := "Tienes veinticinco abiertas. Mira pe eme i sesenta y siete el doce de noviembre de dos mil veintiséis."
	if voice.spoken != want {
		t.Errorf("Piper received %q, want %q", voice.spoken, want)
	}
}

func TestSpeechRejectsEmptyAndFailingRequests(t *testing.T) {
	s := voiceService(&fakeVoice{}, fakeConverter{})
	if got := do(s, "POST", "/v1/audio/speech", "application/json", strings.NewReader(`{"input":"   "}`), "secret-key").Code; got != http.StatusBadRequest {
		t.Errorf("empty input = %d", got)
	}
	if got := do(s, "POST", "/v1/audio/speech", "application/json", strings.NewReader(`not json`), "secret-key").Code; got != http.StatusBadRequest {
		t.Errorf("bad JSON = %d", got)
	}
	if got := do(s, "POST", "/v1/audio/speech", "application/json", strings.NewReader(`{"input":"🔍"}`), "secret-key").Code; got != http.StatusUnprocessableEntity {
		t.Errorf("nothing speakable = %d", got)
	}
	failing := voiceService(&fakeVoice{err: errors.New("piper down")}, fakeConverter{})
	rec := do(failing, "POST", "/v1/audio/speech", "application/json", strings.NewReader(`{"input":"hola"}`), "secret-key")
	if rec.Code != http.StatusBadGateway || strings.Contains(rec.Body.String(), "piper down") {
		t.Errorf("piper failure = %d %q", rec.Code, rec.Body.String())
	}
}

func TestWAVDurationAndFastWhisperRouting(t *testing.T) {
	if got := wavSeconds(testWAV(5)); got < 4.99 || got > 5.01 {
		t.Errorf("duration = %v", got)
	}
	if wavSeconds([]byte("not a wav")) < 1e6 {
		t.Error("unreadable audio must go to the full-window instance")
	}
	hits := map[string]int{}
	var prompt string
	serve := func(name string) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			hits[name]++
			r.ParseMultipartForm(1 << 20)
			prompt = r.FormValue("prompt")
			io.WriteString(w, `{"text":"hola"}`)
		}))
	}
	slow, fast := serve("slow"), serve("fast")
	defer slow.Close()
	defer fast.Close()
	client := NewHTTPVoiceClient(slow.URL, "http://127.0.0.1:1", nil)
	client.FastWhisperURL = fast.URL
	client.Vocabulary = "vocabulario de prueba"
	for seconds, want := range map[int]string{3: "fast", 10: "fast", 30: "slow"} {
		hits["fast"], hits["slow"] = 0, 0
		if _, err := client.Transcribe(context.Background(), testWAV(seconds)); err != nil {
			t.Fatal(err)
		}
		if hits[want] != 1 {
			t.Errorf("%d s clip went to %v, want %s", seconds, hits, want)
		}
	}
	if prompt != "vocabulario de prueba" {
		t.Errorf("vocabulary prompt = %q", prompt)
	}
}
