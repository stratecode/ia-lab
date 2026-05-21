package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"github.com/stratecode/lab/internal/cognitivegateway"
	"github.com/stratecode/lab/internal/orchestratorgo/logging"
)

func main() {
	configPath := flag.String("config", "", "Path to cognitive gateway JSON config")
	flag.Parse()
	if *configPath == "" {
		fmt.Fprintln(os.Stderr, "--config is required")
		os.Exit(2)
	}
	cfg, err := cognitivegateway.LoadConfig(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to load config: %v\n", err)
		os.Exit(1)
	}
	logging.Configure(cfg.LogLevel)
	zerolog.TimeFieldFormat = zerolog.TimeFormatUnix

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := cognitivegateway.Run(ctx, cfg); err != nil {
		log.Error().Err(err).Msg("cognitive gateway failed")
		os.Exit(1)
	}
}
