package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/stratecode/lab/internal/donnaidentity"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	listenAddress := strings.TrimSpace(os.Getenv("DONNA_IDENTITY_LISTEN_ADDR"))
	if listenAddress == "" {
		listenAddress = "127.0.0.1:8092"
	}
	stateDirectory := strings.TrimSpace(os.Getenv("DONNA_IDENTITY_STATE_DIR"))
	if stateDirectory == "" {
		return errors.New("DONNA_IDENTITY_STATE_DIR is required")
	}
	serviceToken := os.Getenv("DONNA_IDENTITY_SERVICE_TOKEN")
	if len(serviceToken) < 32 {
		return errors.New("DONNA_IDENTITY_SERVICE_TOKEN must contain at least 32 characters")
	}
	store, err := donnaidentity.NewJSONStore(stateDirectory)
	if err != nil {
		return fmt.Errorf("initialize identity store: %w", err)
	}
	listener, err := net.Listen("tcp", listenAddress)
	if err != nil {
		return fmt.Errorf("listen for identity service: %w", err)
	}
	server := &http.Server{
		Handler:           donnaidentity.NewHandler(store, donnaidentity.HeaderAuthenticator{Token: serviceToken}),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	serveErr := make(chan error, 1)
	go func() {
		serveErr <- server.Serve(listener)
	}()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	select {
	case err := <-serveErr:
		if !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("identity service stopped: %w", err)
		}
		return nil
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("shut down identity service: %w", err)
		}
		err := <-serveErr
		if !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("identity service stopped during shutdown: %w", err)
		}
		return nil
	}
}
