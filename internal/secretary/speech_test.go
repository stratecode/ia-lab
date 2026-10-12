package secretary

import (
	"bytes"
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

func TestHTTPVoiceClientTranscribePostsSpanishWAVAndParsesJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/inference" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Errorf("parse multipart: %v", err)
			return
		}
		if got := r.FormValue("language"); got != "es" {
			t.Errorf("language = %q, want es", got)
		}
		if got := r.FormValue("response_format"); got != "json" {
			t.Errorf("response_format = %q, want json", got)
		}
		if got := r.FormValue("temperature"); got != "0" {
			t.Errorf("temperature = %q, want 0", got)
		}
		file, header, err := r.FormFile("file")
		if err != nil {
			t.Errorf("file field: %v", err)
			return
		}
		defer file.Close()
		if header.Filename != "voice.wav" {
			t.Errorf("filename = %q, want fixed voice.wav", header.Filename)
		}
		got, err := io.ReadAll(file)
		if err != nil || !bytes.Equal(got, []byte("wav-payload")) {
			t.Errorf("uploaded bytes = %q, err = %v", got, err)
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"text":"  Hola mundo.  "}`)
	}))
	defer server.Close()

	client := NewHTTPVoiceClient(server.URL, server.URL, server.Client())
	got, err := client.Transcribe(context.Background(), []byte("wav-payload"))
	if err != nil {
		t.Fatal(err)
	}
	if got != "Hola mundo." {
		t.Fatalf("transcript = %q, want trimmed transcript", got)
	}
}

func TestHTTPVoiceClientSynthesizePostsJSONAndReturnsWAV(t *testing.T) {
	wantWAV := []byte("RIFF\x00\x00\x00\x00WAVEfixture")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/synthesize" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Content-Type"); !strings.HasPrefix(got, "application/json") {
			t.Errorf("Content-Type = %q", got)
		}
		var body struct {
			Text string `json:"text"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request: %v", err)
		}
		if body.Text != "Buenos días" {
			t.Errorf("text = %q", body.Text)
		}
		w.Header().Set("Content-Type", "audio/wav")
		w.Write(wantWAV)
	}))
	defer server.Close()

	client := NewHTTPVoiceClient(server.URL, server.URL, server.Client())
	got, err := client.Synthesize(context.Background(), "Buenos días")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, wantWAV) {
		t.Fatalf("WAV = %q, want %q", got, wantWAV)
	}
}

func TestHTTPVoiceClientRejectsMalformedEmptyAndFailedResponses(t *testing.T) {
	tests := []struct {
		name       string
		status     int
		body       string
		wantErr    bool
		transcribe bool
	}{
		{name: "malformed json", status: http.StatusOK, body: "{bad", wantErr: true, transcribe: true},
		{name: "empty transcript", status: http.StatusOK, body: `{"text":"  "}`, wantErr: true, transcribe: true},
		{name: "whisper error", status: http.StatusBadGateway, body: "private upstream detail", wantErr: true, transcribe: true},
		{name: "empty synthesis", status: http.StatusOK, body: "", wantErr: true},
		{name: "invalid wav", status: http.StatusOK, body: "not a wav", wantErr: true},
		{name: "piper error", status: http.StatusInternalServerError, body: "private upstream detail", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.status)
				if tt.transcribe {
					io.WriteString(w, tt.body)
				} else {
					w.Write([]byte(tt.body))
				}
			}))
			defer server.Close()
			client := NewHTTPVoiceClient(server.URL, server.URL, server.Client())
			var err error
			if tt.transcribe {
				_, err = client.Transcribe(context.Background(), []byte("audio"))
			} else {
				_, err = client.Synthesize(context.Background(), "hello")
			}
			if (err != nil) != tt.wantErr {
				t.Fatalf("error = %v, wantErr %v", err, tt.wantErr)
			}
			if err != nil && strings.Contains(err.Error(), "private upstream detail") {
				t.Fatalf("error leaked upstream body: %v", err)
			}
		})
	}
}

func TestHTTPVoiceClientHonorsCancellationAndResponseLimit(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/inference" {
			close(started)
			<-release
			return
		}
		w.Header().Set("Content-Type", "audio/wav")
		w.Write([]byte("RIFF\x00\x00\x00\x00WAVE12345"))
	}))
	defer server.Close()
	client := NewHTTPVoiceClient(server.URL, server.URL, server.Client())
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := client.Transcribe(ctx, []byte("audio"))
		done <- err
	}()
	select {
	case <-started:
		cancel()
		close(release)
	case <-time.After(2 * time.Second):
		t.Fatal("Whisper request did not start")
	}
	if err := <-done; err == nil {
		t.Fatal("cancelled request unexpectedly succeeded")
	}

	client.maxSpeechResponseBytes = 8
	if _, err := client.Synthesize(context.Background(), "hello"); err == nil {
		t.Fatal("oversized Piper response unexpectedly succeeded")
	}
}

func TestLocalEndpointRejectsNonLoopbackSpeechHosts(t *testing.T) {
	for _, endpoint := range []string{"http://example.com:8083", "http://192.168.1.40:8083"} {
		t.Run(endpoint, func(t *testing.T) {
			if _, err := localEndpoint(endpoint, "inference"); err == nil {
				t.Fatalf("non-loopback endpoint %q was accepted", endpoint)
			}
		})
	}
	for _, endpoint := range []string{"http://127.0.0.1:8083", "http://[::1]:8083", "http://localhost:8083"} {
		t.Run(endpoint, func(t *testing.T) {
			if _, err := localEndpoint(endpoint, "inference"); err != nil {
				t.Fatalf("loopback endpoint %q rejected: %v", endpoint, err)
			}
		})
	}
}

func TestFFmpegAudioConverterUsesPipesForWAVAndOGGOpus(t *testing.T) {
	dir := t.TempDir()
	fake := filepath.Join(dir, "ffmpeg-fake.sh")
	script := `#!/bin/sh
has_rate=0
has_codec=0
has_ogg=0
has_wav=0
last=""
for arg in "$@"; do
  if [ "$last" = "-ar" ] && [ "$arg" = "16000" ]; then has_rate=1; fi
  if [ "$last" = "-c:a" ] && [ "$arg" = "libopus" ]; then has_codec=1; fi
  if [ "$last" = "-f" ] && [ "$arg" = "ogg" ]; then has_ogg=1; fi
  if [ "$last" = "-f" ] && [ "$arg" = "wav" ]; then has_wav=1; fi
  last="$arg"
done
if [ "$has_rate" = "1" ] && [ "$has_wav" = "0" ]; then exit 31; fi
if [ "$has_codec" = "1" ] && [ "$has_ogg" = "0" ]; then exit 32; fi
cat
`
	if err := os.WriteFile(fake, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	converter := FFmpegAudioConverter{Binary: fake}
	for name, convert := range map[string]func(context.Context, []byte) ([]byte, error){
		"to wav":      converter.ToWAV,
		"to ogg opus": converter.ToOGGOpus,
	} {
		t.Run(name, func(t *testing.T) {
			got, err := convert(context.Background(), []byte("audio-pipe"))
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != "audio-pipe" {
				t.Fatalf("pipe output = %q", got)
			}
		})
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "ffmpeg-fake.sh" {
		t.Fatalf("converter created files: %v", entries)
	}
}

func TestFFmpegAudioConverterCancellationAndFailureAreSanitized(t *testing.T) {
	dir := t.TempDir()
	slow := filepath.Join(dir, "slow.sh")
	if err := os.WriteFile(slow, []byte("#!/bin/sh\nsleep 5\ncat\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := (FFmpegAudioConverter{Binary: slow}).ToWAV(ctx, []byte("audio"))
	if err == nil {
		t.Fatal("cancelled ffmpeg command unexpectedly succeeded")
	}

	failing := filepath.Join(dir, "failing.sh")
	if err := os.WriteFile(failing, []byte("#!/bin/sh\necho private-audio-marker >&2\nexit 9\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	_, err = (FFmpegAudioConverter{Binary: failing}).ToWAV(context.Background(), []byte("audio"))
	if err == nil || strings.Contains(err.Error(), "private-audio-marker") {
		t.Fatalf("ffmpeg failure not safely reported: %v", err)
	}
}
