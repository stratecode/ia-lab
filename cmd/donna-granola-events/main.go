package main

import (
	"context"
	"errors"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/stratecode/lab/internal/donnagranola"
)

func main() {
	listen := listenAddress(os.Getenv)
	host, _, err := net.SplitHostPort(listen)
	if err != nil || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() {
		log.Fatal("loopback webhook listener required")
	}
	secret := strings.TrimSpace(os.Getenv("LAB_GRANOLA_WEBHOOK_SECRET"))
	apiKey := strings.TrimSpace(os.Getenv("LAB_GRANOLA_API_KEY"))
	notificationToken := strings.TrimSpace(os.Getenv("LAB_GRANOLA_NOTIFICATION_TOKEN"))
	if apiKey == "" || len(notificationToken) < 32 {
		log.Fatal("Granola API and notification credentials required")
	}
	if secret != "" && donnagranola.ValidateSigningSecret(secret) != nil {
		log.Fatal("invalid Granola webhook signing secret")
	}
	operatorID, err := strconv.ParseInt(strings.TrimSpace(os.Getenv("LAB_GRANOLA_OPERATOR_ID")), 10, 64)
	if err != nil || operatorID <= 0 {
		log.Fatal("valid Granola operator ID required")
	}
	stateDir := env("STATE_DIRECTORY", "/var/lib/donna-granola-events")
	queue, err := donnagranola.OpenEventQueue(stateDir)
	if err != nil {
		log.Fatal("Granola event queue unavailable")
	}
	sink, err := donnagranola.NewSecretaryNotificationSink(env("LAB_GRANOLA_SECRETARY_URL", "http://127.0.0.1:8082"), notificationToken)
	if err != nil {
		log.Fatal("Granola notification sink configuration invalid")
	}
	worker := &donnagranola.Worker{
		Queue: queue, Notes: donnagranola.NewGranolaClient(apiKey), Sink: sink,
		Operator: donnagranola.OperatorContext{OperatorID: operatorID, TelegramChatID: operatorID},
		OnRetry: func(eventID, category string, attempt int) {
			log.Printf("granola_event_retry event_id=%s category=%s attempt=%d", eventID, category, attempt)
		},
		OnFail: func(eventID, category string) {
			log.Printf("granola_event_failed event_id=%s category=%s", eventID, category)
		},
	}
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer cancel()
	go func() {
		if err := worker.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
			log.Print("Granola worker stopped")
			cancel()
		}
	}()
	server := &http.Server{
		Addr: listen, Handler: donnagranola.NewWebhookHandler(queue, donnagranola.StaticSecret(secret), time.Now),
		ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 10 * time.Second, IdleTimeout: 30 * time.Second,
	}
	go func() {
		<-ctx.Done()
		shutdown, done := context.WithTimeout(context.Background(), 5*time.Second)
		defer done()
		_ = server.Shutdown(shutdown)
	}()
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal("Granola webhook service failed")
	}
}

func listenAddress(lookup func(string) string) string {
	if value := strings.TrimSpace(lookup("LAB_DONNA_GRANOLA_LISTEN")); value != "" {
		return value
	}
	return "127.0.0.1:8085"
}

func env(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}
