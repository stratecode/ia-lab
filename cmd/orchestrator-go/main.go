package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/stratecode/lab/internal/orchestratorgo/app"
)

func main() {
	runtime, err := app.New()
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to initialize orchestrator-go: %v\n", err)
		os.Exit(1)
	}
	defer runtime.Close()

	server := &http.Server{
		Addr:              runtime.Config.ListenAddress(),
		Handler:           runtime.Router,
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		log.Info().Str("addr", server.Addr).Msg("starting orchestrator-go runtime core")
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Error().Err(err).Msg("orchestrator-go server failed")
			os.Exit(1)
		}
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		log.Error().Err(err).Msg("orchestrator-go shutdown failed")
		os.Exit(1)
	}
}
