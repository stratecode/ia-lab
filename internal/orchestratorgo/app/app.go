package app

import (
	"context"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5/middleware"

	"github.com/stratecode/lab/internal/orchestratorgo/config"
	"github.com/stratecode/lab/internal/orchestratorgo/httpapi"
	"github.com/stratecode/lab/internal/orchestratorgo/logging"
	"github.com/stratecode/lab/internal/orchestratorgo/research"
	"github.com/stratecode/lab/internal/orchestratorgo/store"
	"github.com/stratecode/lab/internal/orchestratorgo/worker"
)

type Runtime struct {
	Config   config.Config
	Router   http.Handler
	Postgres *store.PostgresStore
	Redis    *store.RedisStore
	Worker   *worker.ShadowWorker
	Research *research.Service
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
	shadowWorker := worker.New(cfg, postgres, redisStore)
	researchService := research.New()
	server := &httpapi.Server{
		Config:        cfg,
		Postgres:      postgres,
		Redis:         redisStore,
		Research:      researchService,
		Now:           time.Now,
		Version:       "0.1.0-go-shadow",
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
	if err := shadowWorker.Start(backgroundCtx); err != nil {
		_ = redisStore.Close()
		postgres.Close()
		return nil, err
	}
	return &Runtime{
		Config:   cfg,
		Router:   router,
		Postgres: postgres,
		Redis:    redisStore,
		Worker:   shadowWorker,
		Research: researchService,
	}, nil
}

func (r *Runtime) Close() {
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
