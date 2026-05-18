package app

import (
	"context"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5/middleware"

	"github.com/stratecode/lab/internal/orchestratorgo/capabilities"
	"github.com/stratecode/lab/internal/orchestratorgo/config"
	"github.com/stratecode/lab/internal/orchestratorgo/httpapi"
	"github.com/stratecode/lab/internal/orchestratorgo/initiative"
	"github.com/stratecode/lab/internal/orchestratorgo/logging"
	"github.com/stratecode/lab/internal/orchestratorgo/research"
	"github.com/stratecode/lab/internal/orchestratorgo/store"
	"github.com/stratecode/lab/internal/orchestratorgo/telegram"
	"github.com/stratecode/lab/internal/orchestratorgo/worker"
)

type Runtime struct {
	Config   config.Config
	Router   http.Handler
	Postgres *store.PostgresStore
	Redis    *store.RedisStore
	Worker   *worker.RuntimeWorker
	Research *research.Service
	Bot      *telegram.Bot
	SafeMode *httpapi.SafeModeState
}

func New() (*Runtime, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}
	logging.Configure(cfg.LogLevel)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	postgres, err := store.NewPostgresStore(ctx, cfg.PostgresDSN())
	if err != nil {
		return nil, err
	}
	redisOptions, err := store.ParseRedisOptions(cfg.RedisURL)
	if err != nil {
		postgres.Close()
		return nil, err
	}
	redisStore := store.NewRedisStore(redisOptions)
	capabilityClient := capabilities.New(capabilities.Options{
		DocsBaseURL:       cfg.DocsSidecarURL,
		ImagesBaseURL:     cfg.ImagesSidecarURL,
		MaxDocumentBytes:  cfg.MaxDocumentBytes,
		MaxImageBytes:     cfg.MaxImageBytes,
		MaxArtifactChars:  cfg.MaxArtifactTextChars,
		AllowedURLSchemes: cfg.AllowedURLSchemes,
		AllowedLocalRoots: cfg.AllowedLocalRoots,
		TimeoutSeconds:    20,
	})
	researchService := research.New(research.Options{
		SearchBaseURL:        cfg.WebSearchBaseURL,
		Capabilities:         capabilityClient,
		UtilityBaseURL:       cfg.LlamaUtilityBaseURL,
		UtilityAPIKey:        cfg.LlamaUtilityAPIKey,
		UtilityTimeoutSeconds: cfg.LlamaTimeoutSeconds,
	})
	initiativeService := initiative.New(cfg, researchService)
	runtimeWorker := worker.New(cfg, postgres, redisStore, researchService)
	safeMode := httpapi.NewSafeModeState(cfg.SafeMode)
	server := &httpapi.Server{
		Config:        cfg,
		Postgres:      postgres,
		Redis:         redisStore,
		Research:      researchService,
		Initiatives:   initiativeService,
		Capabilities:  capabilityClient,
		SafeMode:      safeMode,
		Now:           time.Now,
		Version:       "0.1.0-go-runtime",
		OpenAIToolsID: cfg.OpenAIToolsModelID,
	}
	auth := httpapi.NewAuthenticator(
		postgres,
		cfg.BruteForceMaxAttempts,
		cfg.BruteForceWindowSeconds,
		cfg.BruteForceBlockDurationSecs,
	)
	router := server.Router(auth)
	router = middleware.RequestID(router)
	backgroundCtx, _ := context.WithCancel(context.Background())
	if err := runtimeWorker.Start(backgroundCtx); err != nil {
		_ = redisStore.Close()
		postgres.Close()
		return nil, err
	}
	bot := telegram.New(cfg, postgres, redisStore, researchService, initiativeService, capabilityClient, safeMode)
	if err := bot.Start(backgroundCtx); err != nil {
		runtimeWorker.Stop()
		_ = redisStore.Close()
		postgres.Close()
		return nil, err
	}
	return &Runtime{
		Config:   cfg,
		Router:   router,
		Postgres: postgres,
		Redis:    redisStore,
		Worker:   runtimeWorker,
		Research: researchService,
		Bot:      bot,
		SafeMode: safeMode,
	}, nil
}

func (r *Runtime) Close() {
	if r.Bot != nil {
		r.Bot.Stop()
	}
	if r.Worker != nil {
		r.Worker.Stop()
	}
	if r.Redis != nil {
		_ = r.Redis.Close()
	}
	if r.Postgres != nil {
		r.Postgres.Close()
	}
}
