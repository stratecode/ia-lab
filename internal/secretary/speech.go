package secretary

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// DefaultVocabulary is Whisper's initial prompt: it nudges the recogniser towards
// the words Donna's conversations actually contain.
const DefaultVocabulary = "Conversación en español con Donna, la secretaria personal de Fran. Palabras: Jira, Granola, Donna, sprint, backlog."

const (
	maxWhisperResponseBytes = 1 << 20
	maxSpeechResponseBytes  = 50_000_000
	maxConvertedAudioBytes  = 50_000_000
)

// SpeechProcessor provides channel-neutral speech recognition and synthesis.
type SpeechProcessor interface {
	Transcribe(context.Context, []byte) (string, error)
	Synthesize(context.Context, string) ([]byte, error)
}

// AudioConverter converts between channel audio and the speech service formats.
type AudioConverter interface {
	ToWAV(context.Context, []byte) ([]byte, error)
	ToOGGOpus(context.Context, []byte) ([]byte, error)
}

// HTTPVoiceClient calls the loopback Whisper and Piper HTTP services.
type HTTPVoiceClient struct {
	whisperURL string
	piperURL   string
	// FastWhisperURL, when set, serves short clips (up to fastWhisperMaxSeconds)
	// from a Whisper instance that encodes a reduced audio window. It answers in
	// about half the time; longer clips keep using the full-window instance.
	FastWhisperURL string
	// Vocabulary is sent to Whisper as its initial prompt so proper nouns such as
	// Jira or team names are spelled correctly.
	Vocabulary string
	// CaptureDir, when set, keeps each clip and its transcript for a day so the
	// recogniser can be measured on the operator's real voice. Off by default.
	CaptureDir             string
	client                 *http.Client
	maxSpeechResponseBytes int64
}

// NewHTTPVoiceClient creates a client for the local speech service endpoints.
func NewHTTPVoiceClient(whisperURL, piperURL string, client *http.Client) *HTTPVoiceClient {
	if client == nil {
		client = &http.Client{Timeout: 2 * time.Minute}
	}
	clientCopy := *client
	clientCopy.CheckRedirect = func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
	}
	return &HTTPVoiceClient{
		whisperURL:             strings.TrimRight(whisperURL, "/"),
		piperURL:               strings.TrimRight(piperURL, "/"),
		client:                 &clientCopy,
		maxSpeechResponseBytes: maxSpeechResponseBytes,
	}
}

// Transcribe sends an audio payload to Whisper and returns its Spanish text.
func (c *HTTPVoiceClient) Transcribe(ctx context.Context, wav []byte) (string, error) {
	if len(wav) == 0 {
		return "", errors.New("speech transcription failed")
	}
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.WriteField("response_format", "verbose_json"); err != nil {
		return "", errors.New("speech transcription failed")
	}
	if err := writer.WriteField("temperature", "0"); err != nil {
		return "", errors.New("speech transcription failed")
	}
	if err := writer.WriteField("language", "es"); err != nil {
		return "", errors.New("speech transcription failed")
	}
	if prompt := strings.TrimSpace(c.Vocabulary); prompt != "" {
		if err := writer.WriteField("prompt", prompt); err != nil {
			return "", errors.New("speech transcription failed")
		}
	}
	part, err := writer.CreateFormFile("file", "voice.wav")
	if err != nil {
		return "", errors.New("speech transcription failed")
	}
	if _, err := part.Write(wav); err != nil {
		return "", errors.New("speech transcription failed")
	}
	if err := writer.Close(); err != nil {
		return "", errors.New("speech transcription failed")
	}
	base := c.whisperURL
	if c.FastWhisperURL != "" && wavSeconds(wav) <= fastWhisperMaxSeconds {
		base = c.FastWhisperURL
	}
	endpoint, err := localEndpoint(base, "inference")
	if err != nil {
		return "", errors.New("speech transcription failed")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, &body)
	if err != nil {
		return "", errors.New("speech transcription failed")
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	resp, err := c.client.Do(req)
	if err != nil {
		return "", errors.New("speech transcription failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return "", errors.New("speech transcription failed")
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxWhisperResponseBytes+1))
	if err != nil || int64(len(data)) > maxWhisperResponseBytes {
		return "", errors.New("speech transcription failed")
	}
	var result struct {
		Text     string `json:"text"`
		Segments []struct {
			AvgLogProb   float64 `json:"avg_logprob"`
			NoSpeechProb float64 `json:"no_speech_prob"`
		} `json:"segments"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return "", errors.New("speech transcription failed")
	}
	transcript := cleanTranscript(result.Text)
	meanLogProb := 0.0
	for _, segment := range result.Segments {
		meanLogProb += segment.AvgLogProb
	}
	if len(result.Segments) > 0 {
		meanLogProb /= float64(len(result.Segments))
	}
	c.capture(wav, transcript, meanLogProb)
	if transcript == "" {
		return "", errors.New("speech transcription failed")
	}
	if len(result.Segments) > 0 && meanLogProb < lowConfidenceLogProb {
		return transcript, &LowConfidenceError{Transcript: transcript, LogProb: meanLogProb}
	}
	return transcript, nil
}

// lowConfidenceLogProb is Whisper's own fallback threshold: below it the model is
// guessing and acting on the text would be acting on noise.
const lowConfidenceLogProb = -1.0

// LowConfidenceError reports a transcript Whisper itself was unsure about. It
// carries the best guess so the caller can show it and ask for a repeat.
type LowConfidenceError struct {
	Transcript string
	LogProb    float64
}

func (e *LowConfidenceError) Error() string { return "low confidence speech transcript" }

var bracketedNoise = regexp.MustCompile(`\[[^\]]*\]|\([^)]*(?:música|musica|aplausos|risas|ruido|silencio)[^)]*\)|♪+`)

// cleanTranscript removes the labels Whisper invents for silence or noise, such as
// "[Música]", and tidies the spaces. A clip with nothing else yields "".
func cleanTranscript(text string) string {
	text = bracketedNoise.ReplaceAllString(text, " ")
	return strings.Join(strings.Fields(text), " ")
}

// capture stores a clip and its transcript when CaptureDir is set, and removes
// captures older than a day. Failures are ignored: it must never affect a reply.
func (c *HTTPVoiceClient) capture(wav []byte, transcript string, logProb float64) {
	if c.CaptureDir == "" {
		return
	}
	if err := os.MkdirAll(c.CaptureDir, 0o700); err != nil {
		return
	}
	cutoff := time.Now().Add(-24 * time.Hour)
	if entries, err := os.ReadDir(c.CaptureDir); err == nil {
		for _, entry := range entries {
			if info, err := entry.Info(); err == nil && info.ModTime().Before(cutoff) {
				_ = os.Remove(filepath.Join(c.CaptureDir, entry.Name()))
			}
		}
	}
	name := filepath.Join(c.CaptureDir, time.Now().Format("20060102-150405.000"))
	_ = os.WriteFile(name+".wav", wav, 0o600)
	_ = os.WriteFile(name+".txt", []byte(fmt.Sprintf("logprob=%.3f\n%s\n", logProb, transcript)), 0o600)
}

// fastWhisperMaxSeconds is what a 768-frame audio context can hold (about 15 s)
// with a margin; anything longer would be cut off by the fast instance.
const fastWhisperMaxSeconds = 14.0

// wavSeconds returns the duration of a PCM WAV payload, or a very large number
// when the header cannot be read so the clip goes to the full-window instance.
func wavSeconds(wav []byte) float64 {
	if len(wav) < 44 || !validWAV(wav) {
		return 1e9
	}
	byteRate := float64(uint32(wav[28]) | uint32(wav[29])<<8 | uint32(wav[30])<<16 | uint32(wav[31])<<24)
	if byteRate <= 0 {
		return 1e9
	}
	return float64(len(wav)-44) / byteRate
}

// Synthesize asks Piper for a WAV response with a fixed maximum response size.
func (c *HTTPVoiceClient) Synthesize(ctx context.Context, text string) ([]byte, error) {
	if strings.TrimSpace(text) == "" {
		return nil, errors.New("speech synthesis failed")
	}
	endpoint, err := localEndpoint(c.piperURL, "synthesize")
	if err != nil {
		return nil, errors.New("speech synthesis failed")
	}
	body, err := json.Marshal(struct {
		Text string `json:"text"`
	}{Text: text})
	if err != nil {
		return nil, errors.New("speech synthesis failed")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, errors.New("speech synthesis failed")
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, errors.New("speech synthesis failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, errors.New("speech synthesis failed")
	}
	limit := c.maxSpeechResponseBytes
	if limit <= 0 {
		limit = maxSpeechResponseBytes
	}
	audio, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil || int64(len(audio)) > limit || !validWAV(audio) {
		return nil, errors.New("speech synthesis failed")
	}
	return audio, nil
}

func localEndpoint(base, path string) (string, error) {
	u, err := url.Parse(base)
	if err != nil || u.Host == "" || u.User != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return "", errors.New("invalid speech service endpoint")
	}
	host := u.Hostname()
	ip := net.ParseIP(host)
	if (ip == nil && !strings.EqualFold(host, "localhost")) || (ip != nil && !ip.IsLoopback()) {
		return "", errors.New("speech service must use loopback")
	}
	return u.JoinPath(path).String(), nil
}

func validWAV(data []byte) bool {
	return len(data) >= 12 && string(data[:4]) == "RIFF" && string(data[8:12]) == "WAVE"
}

// FFmpegAudioConverter invokes FFmpeg using in-memory stdin and stdout pipes.
type FFmpegAudioConverter struct {
	Binary string
}

// ToWAV converts OGG/Opus audio to mono 16 kHz PCM WAV in memory.
func (c FFmpegAudioConverter) ToWAV(ctx context.Context, input []byte) ([]byte, error) {
	return c.convert(ctx, input, "-vn", "-ac", "1", "-ar", "16000", "-f", "wav", "pipe:1")
}

// ToOGGOpus converts Piper WAV audio to Telegram-compatible OGG/Opus in memory.
func (c FFmpegAudioConverter) ToOGGOpus(ctx context.Context, input []byte) ([]byte, error) {
	return c.convert(ctx, input, "-vn", "-c:a", "libopus", "-b:a", "32k", "-f", "ogg", "pipe:1")
}

func (c FFmpegAudioConverter) convert(ctx context.Context, input []byte, outputArgs ...string) ([]byte, error) {
	if c.Binary == "" || len(input) == 0 {
		return nil, errors.New("audio conversion failed")
	}
	args := []string{"-hide_banner", "-loglevel", "error", "-i", "pipe:0"}
	args = append(args, outputArgs...)
	cmd := exec.CommandContext(ctx, c.Binary, args...)
	cmd.Stdin = bytes.NewReader(input)
	output := &boundedBuffer{limit: maxConvertedAudioBytes}
	cmd.Stdout = output
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil || output.exceeded || output.Len() == 0 {
		return nil, errors.New("audio conversion failed")
	}
	return output.Bytes(), nil
}

type boundedBuffer struct {
	bytes.Buffer
	limit    int64
	exceeded bool
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	if int64(b.Len())+int64(len(p)) > b.limit {
		b.exceeded = true
		return 0, fmt.Errorf("audio output limit exceeded")
	}
	return b.Buffer.Write(p)
}
