package config

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/knadh/koanf/providers/env"
	"github.com/knadh/koanf/v2"
)

type Config struct {
	APIHost                     string
	APIPort                     int
	Mode                        string
	LogLevel                    string
	SafeMode                    bool
	WorkspaceRoot               string
	PostgresHost                string
	PostgresPort                int
	PostgresDB                  string
	PostgresUser                string
	PostgresPassword            string
	RedisURL                    string
	RateLimitPerKey             int
	QueueMaxGlobal              int
	QueueMaxPerAgent            int
	WorkerHeartbeatInterval     int
	WorkerDequeueTimeoutSeconds int
	BruteForceMaxAttempts       int
	BruteForceWindowSeconds     int
	BruteForceBlockDurationSecs int
	OpenAIToolsModelID          string
}

func Load() (Config, error) {
	k := koanf.New(".")
	if err := k.Load(env.Provider("", ".", func(s string) string {
		return strings.ToLower(strings.ReplaceAll(s, "_", "."))
	}), nil); err != nil {
		return Config{}, err
	}

	cfg := Config{
		APIHost:                     getString(k, "lab.orchestrator.host", "127.0.0.1"),
		APIPort:                     getInt(k, "lab.orchestrator.port", 8100),
		Mode:                        strings.ToLower(getString(k, "lab.orchestrator.mode", "all")),
		LogLevel:                    strings.ToLower(getString(k, "lab.orchestrator.log.level", "info")),
		SafeMode:                    getBool(k, "lab.orchestrator.safe.mode", true),
		WorkspaceRoot:               getString(k, "lab.workspace.root", "/srv/ai-lab/orchestrator/workspaces"),
		PostgresHost:                getString(k, "lab.postgres.host", "127.0.0.1"),
		PostgresPort:                getInt(k, "lab.postgres.port", 5432),
		PostgresDB:                  getString(k, "lab.postgres.db", "orchestrator"),
		PostgresUser:                getString(k, "lab.postgres.user", "orchestrator"),
		PostgresPassword:            getString(k, "lab.postgres.password", ""),
		RedisURL:                    getString(k, "lab.redis.url", "redis://127.0.0.1:6379/0"),
		RateLimitPerKey:             getInt(k, "lab.rate.limit.per.key", 20),
		QueueMaxGlobal:              getInt(k, "lab.queue.max.global", 200),
		QueueMaxPerAgent:            getInt(k, "lab.queue.max.per.agent", 50),
		WorkerHeartbeatInterval:     getInt(k, "lab.worker.heartbeat.interval", 30),
		WorkerDequeueTimeoutSeconds: getInt(k, "lab.worker.dequeue.timeout.seconds", 2),
		BruteForceMaxAttempts:       getInt(k, "lab.brute.force.max.attempts", 10),
		BruteForceWindowSeconds:     getInt(k, "lab.brute.force.window", 60),
		BruteForceBlockDurationSecs: getInt(k, "lab.brute.force.block.duration", 300),
		OpenAIToolsModelID:          "orchestrator-tools",
	}

	if cfg.PostgresPassword == "" {
		return Config{}, fmt.Errorf("LAB_POSTGRES_PASSWORD is required")
	}
	return cfg, nil
}

func (c Config) WorkerEnabled() bool {
	return c.Mode == "all" || c.Mode == "worker"
}

func (c Config) ListenAddress() string {
	return fmt.Sprintf("%s:%d", c.APIHost, c.APIPort)
}

func (c Config) PostgresDSN() string {
	return fmt.Sprintf(
		"postgres://%s:%s@%s:%d/%s",
		c.PostgresUser,
		c.PostgresPassword,
		c.PostgresHost,
		c.PostgresPort,
		c.PostgresDB,
	)
}

func getString(k *koanf.Koanf, key, fallback string) string {
	value := strings.TrimSpace(k.String(key))
	if value == "" {
		return fallback
	}
	return value
}

func getInt(k *koanf.Koanf, key string, fallback int) int {
	value := strings.TrimSpace(k.String(key))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func getBool(k *koanf.Koanf, key string, fallback bool) bool {
	value := strings.TrimSpace(strings.ToLower(k.String(key)))
	if value == "" {
		return fallback
	}
	switch value {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return fallback
	}
}
