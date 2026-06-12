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
	WorkspaceRetentionHours     int
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
	OpenAIBaseURL               string
	OpenAIReferenceAPIKey       string
	OpenAIReferenceModel        string
	OpenAIJudgeModel            string
	OpenAITimeoutSeconds        int
	WebSearchBaseURL            string
	AllowedURLSchemes           []string
	AllowedLocalRoots           []string
	MaxDocumentBytes            int
	MaxImageBytes               int
	MaxArtifactTextChars        int
	DocsSidecarURL              string
	ImagesSidecarURL            string
	TelegramBotToken            string
	TelegramAllowedUsers        []int64
	LlamaPlannerBaseURL         string
	LlamaPlannerAPIKey          string
	LlamaUtilityBaseURL         string
	LlamaUtilityAPIKey          string
	LlamaCodeBaseURL            string
	LlamaCodeAPIKey             string
	LlamaTimeoutSeconds         int
	AiderTaskPath               string
	AiderTimeoutSeconds         int
	DefaultGitBranch            string
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
		WorkspaceRetentionHours:     getInt(k, "lab.workspace.retention.hours", 24),
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
		OpenAIBaseURL:               getString(k, "lab.openai.base.url", "https://api.openai.com/v1"),
		OpenAIReferenceAPIKey:       getString(k, "lab.openai.reference.api.key", ""),
		OpenAIReferenceModel:        getString(k, "lab.openai.reference.model", "gpt-4o-mini"),
		OpenAIJudgeModel:            getString(k, "lab.openai.judge.model", "gpt-4o-mini"),
		OpenAITimeoutSeconds:        getInt(k, "lab.openai.timeout.seconds", 45),
		WebSearchBaseURL:            getString(k, "lab.web.search.base.url", "https://html.duckduckgo.com/html/"),
		AllowedURLSchemes:           splitCSV(getString(k, "lab.capabilities.allowed.url.schemes", "http,https,file")),
		AllowedLocalRoots:           splitCSV(getString(k, "lab.capabilities.allowed.local.roots", "/srv/ai-lab,/tmp")),
		MaxDocumentBytes:            getInt(k, "lab.capabilities.max.document.bytes", 20000000),
		MaxImageBytes:               getInt(k, "lab.capabilities.max.image.bytes", 10000000),
		MaxArtifactTextChars:        getInt(k, "lab.capabilities.max.artifact.text.chars", 16000),
		DocsSidecarURL:              getString(k, "lab.capabilities.docs.sidecar.url", ""),
		ImagesSidecarURL:            getString(k, "lab.capabilities.images.sidecar.url", ""),
		TelegramBotToken:            getString(k, "lab.telegram.bot.token", ""),
		TelegramAllowedUsers:        splitCSVInt64(getString(k, "lab.telegram.allowed.users", "")),
		LlamaPlannerBaseURL:         getString(k, "lab.llama.planner.base.url", "http://127.0.0.1:8082/v1"),
		LlamaPlannerAPIKey:          getString(k, "lab.llama.planner.api.key", "local-planner"),
		LlamaUtilityBaseURL:         getString(k, "lab.llama.utility.base.url", "http://127.0.0.1:8083/v1"),
		LlamaUtilityAPIKey:          getString(k, "lab.llama.utility.api.key", "local-utility"),
		LlamaCodeBaseURL:            getString(k, "lab.llama.code.base.url", "http://127.0.0.1:8080/v1"),
		LlamaCodeAPIKey:             getString(k, "lab.llama.code.api.key", "local-code"),
		LlamaTimeoutSeconds:         getInt(k, "lab.llama.timeout.seconds", 90),
		AiderTaskPath:               getString(k, "lab.aider.task.path", "/usr/local/bin/aider-task"),
		AiderTimeoutSeconds:         getInt(k, "lab.aider.timeout.seconds", 1800),
		DefaultGitBranch:            getString(k, "lab.aider.default.branch", "main"),
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

func splitCSV(value string) []string {
	items := []string{}
	for _, item := range strings.Split(value, ",") {
		clean := strings.TrimSpace(item)
		if clean != "" {
			items = append(items, clean)
		}
	}
	return items
}

func splitCSVInt64(value string) []int64 {
	items := []int64{}
	for _, item := range strings.Split(value, ",") {
		clean := strings.TrimSpace(item)
		if clean == "" {
			continue
		}
		parsed, err := strconv.ParseInt(clean, 10, 64)
		if err != nil {
			continue
		}
		items = append(items, parsed)
	}
	return items
}
