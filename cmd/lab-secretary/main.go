package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/stratecode/lab/internal/secretary"
)

func main() {
	listen := env("LAB_SECRETARY_LISTEN", "127.0.0.1:8082")
	host, _, err := net.SplitHostPort(listen)
	if err != nil || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() {
		log.Fatal("loopback listener required")
	}
	allowed := map[int64]bool{}
	for _, id := range strings.Split(os.Getenv("LAB_TELEGRAM_ALLOWED_USERS"), ",") {
		n, err := strconv.ParseInt(strings.TrimSpace(id), 10, 64)
		if err != nil || n <= 0 {
			log.Fatal("valid Telegram allowlist required")
		}
		allowed[n] = true
	}
	if os.Getenv("LAB_TELEGRAM_BOT_TOKEN") == "" || os.Getenv("LAB_SECRETARY_MODEL_KEY") == "" {
		log.Fatal("Telegram and model credentials required")
	}
	instructions, err := os.ReadFile(env("LAB_SECRETARY_INSTRUCTIONS", "/etc/lab-secretary/instructions.md"))
	if err != nil {
		log.Fatal("instructions file required")
	}
	service := secretary.New(env("LAB_SECRETARY_UPSTREAM", "http://127.0.0.1:8081"), os.Getenv("LAB_SECRETARY_MODEL_KEY"), string(instructions), env("STATE_DIRECTORY", "/var/lib/lab-secretary"), allowed)
	var connectorConfig secretary.ConnectorConfig
	encoded := env("LAB_SECRETARY_CONNECTORS", "e30=")
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || json.Unmarshal(raw, &connectorConfig) != nil {
		log.Fatal("invalid private connector configuration")
	}
	service.Sources = secretary.NewConnectors(connectorConfig, service.StateDir)
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer cancel()
	server := &http.Server{Addr: listen, Handler: service.Handler(), ReadHeaderTimeout: 10 * time.Second}
	voiceMaxDuration := 120 * time.Second
	voiceEnabled := env("LAB_SECRETARY_VOICE_ENABLED", "false") == "true"
	if voiceEnabled {
		seconds, err := strconv.Atoi(env("LAB_SECRETARY_VOICE_MAX_DURATION_SECONDS", "120"))
		if err != nil || seconds < 1 || seconds > 3600 {
			log.Fatal("valid voice duration required")
		}
		voiceMaxDuration = time.Duration(seconds) * time.Second
	}
	bot := &secretary.Bot{Base: "https://api.telegram.org", Token: os.Getenv("LAB_TELEGRAM_BOT_TOKEN"), Client: &http.Client{Timeout: 40 * time.Second}, Service: service, Metrics: service.VoiceMetrics(), VoiceMaxDuration: voiceMaxDuration, OnEvent: func(event string) { log.Print(event) }}
	if voiceEnabled {
		whisperURL := env("LAB_WHISPER_URL", "")
		piperURL := env("LAB_PIPER_URL", "")
		if whisperURL != "" && piperURL != "" {
			bot.Speech = secretary.NewHTTPVoiceClient(whisperURL, piperURL, nil)
			bot.Audio = secretary.FFmpegAudioConverter{Binary: env("LAB_FFMPEG_BINARY", "/usr/bin/ffmpeg")}
			bot.VoiceEnabled = true
		} else {
			log.Print("secretary_voice_disabled_missing_endpoint")
		}
	}
	if env("LAB_SECRETARY_TELEGRAM_ENABLED", "true") == "true" {
		go func() {
			if err := bot.Run(ctx); err != nil {
				log.Print("Telegram startup failed; HTTP interface remains available")
			}
		}()
	} else {
		log.Print("telegram_polling_disabled")
	}
	go func() {
		<-ctx.Done()
		shutdown, done := context.WithTimeout(context.Background(), 5*time.Second)
		defer done()
		server.Shutdown(shutdown)
	}()
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal("HTTP service failed")
	}
}
func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
