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
	"os/exec"
	"strings"
	"time"
)

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
	whisperURL             string
	piperURL               string
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
	if err := writer.WriteField("response_format", "json"); err != nil {
		return "", errors.New("speech transcription failed")
	}
	if err := writer.WriteField("temperature", "0"); err != nil {
		return "", errors.New("speech transcription failed")
	}
	if err := writer.WriteField("language", "es"); err != nil {
		return "", errors.New("speech transcription failed")
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
	endpoint, err := localEndpoint(c.whisperURL, "inference")
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
		Text string `json:"text"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return "", errors.New("speech transcription failed")
	}
	transcript := strings.TrimSpace(result.Text)
	if transcript == "" {
		return "", errors.New("speech transcription failed")
	}
	return transcript, nil
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
