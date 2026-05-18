package logging

import (
	"os"
	"strings"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

func Configure(level string) {
	zerolog.TimeFieldFormat = zerolog.TimeFormatUnix
	zerolog.SetGlobalLevel(parseLevel(level))
	log.Logger = zerolog.New(os.Stdout).With().Timestamp().Logger()
}

func parseLevel(level string) zerolog.Level {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "debug":
		return zerolog.DebugLevel
	case "warn", "warning":
		return zerolog.WarnLevel
	case "error":
		return zerolog.ErrorLevel
	default:
		return zerolog.InfoLevel
	}
}
