package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
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
	mode, err := parseTelegramMode(os.Getenv("LAB_SECRETARY_TELEGRAM_MODE"))
	if err != nil {
		log.Fatal("invalid Telegram mode")
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
	mux := http.NewServeMux()
	mux.Handle("/", service.Handler())
	voiceMaxDuration := 120 * time.Second
	voiceEnabled := env("LAB_SECRETARY_VOICE_ENABLED", "false") == "true"
	if voiceEnabled {
		seconds, err := strconv.Atoi(env("LAB_SECRETARY_VOICE_MAX_DURATION_SECONDS", "120"))
		if err != nil || seconds < 1 || seconds > 3600 {
			log.Fatal("valid voice duration required")
		}
		voiceMaxDuration = time.Duration(seconds) * time.Second
	}
	progressDelay, err := parseProgressDelay(os.Getenv("LAB_SECRETARY_PROGRESS_DELAY_SECONDS"))
	if err != nil {
		log.Fatal("valid secretary progress delay required")
	}
	bot := &secretary.Bot{Base: "https://api.telegram.org", Token: os.Getenv("LAB_TELEGRAM_BOT_TOKEN"), Client: &http.Client{Timeout: 40 * time.Second}, Service: service, Metrics: service.VoiceMetrics(), VoiceMaxDuration: voiceMaxDuration, ProgressDelay: progressDelay, OnEvent: func(event string) { log.Print(event) }}
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
	var queue secretary.TelegramUpdateQueue
	webhookSecret := ""
	if mode == "webhook" {
		webhookSecret = os.Getenv("LAB_TELEGRAM_WEBHOOK_SECRET")
		if webhookSecret == "" {
			log.Fatal("Telegram webhook secret required in webhook mode")
		}
		queue, err = secretary.OpenTelegramUpdateQueue(service.StateDir)
		if err != nil {
			log.Fatal("Telegram webhook queue unavailable")
		}
	}
	runTelegram, err := configureTelegram(string(mode), mux, bot, queue, webhookSecret)
	if err != nil {
		log.Fatal("Telegram mode configuration failed")
	}
	server := &http.Server{Addr: listen, Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	if mode != "disabled" {
		go func() {
			if err := runTelegram(ctx); err != nil {
				log.Print("Telegram startup failed; HTTP interface remains available")
			}
		}()
	} else {
		log.Print("telegram_disabled")
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

type telegramMode string

const (
	telegramModeDisabled telegramMode = "disabled"
	telegramModePolling  telegramMode = "polling"
	telegramModeWebhook  telegramMode = "webhook"
)

func parseTelegramMode(value string) (telegramMode, error) {
	if value == "" {
		return telegramModePolling, nil
	}
	mode := telegramMode(strings.TrimSpace(value))
	switch mode {
	case telegramModeDisabled, telegramModePolling, telegramModeWebhook:
		return mode, nil
	default:
		return "", fmt.Errorf("unsupported Telegram mode %q", value)
	}
}

func parseProgressDelay(value string) (time.Duration, error) {
	if strings.TrimSpace(value) == "" {
		return 5 * time.Second, nil
	}
	seconds, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || seconds < 1 || seconds > 120 {
		return 0, fmt.Errorf("progress delay must be between 1 and 120 seconds")
	}
	return time.Duration(seconds) * time.Second, nil
}

func configureTelegram(mode string, mux *http.ServeMux, bot *secretary.Bot, queue secretary.TelegramUpdateQueue, webhookSecret string) (func(context.Context) error, error) {
	parsed, err := parseTelegramMode(mode)
	if err != nil {
		return nil, err
	}
	switch parsed {
	case telegramModeDisabled:
		return func(context.Context) error { return nil }, nil
	case telegramModePolling:
		if bot == nil {
			return nil, errors.New("Telegram bot is required in polling mode")
		}
		return bot.Run, nil
	case telegramModeWebhook:
		if mux == nil || bot == nil || queue == nil || webhookSecret == "" {
			return nil, errors.New("webhook mode requires an HTTP mux, bot, queue, and secret")
		}
		mux.Handle("/telegram/webhook", secretary.TelegramWebhook{Queue: queue, Secret: webhookSecret})
		return func(ctx context.Context) error { return bot.RunWebhookWorker(ctx, queue) }, nil
	default:
		return nil, errors.New("unsupported Telegram mode")
	}
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
