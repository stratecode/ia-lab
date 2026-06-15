package codexlocalgateway

import (
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	defaultAddr                    = "127.0.0.1:8180"
	defaultUpstreamBase            = "http://127.0.0.1:8080/v1"
	defaultModel                   = "qwen-local-code"
	defaultUpstreamModel           = "codex-local"
	defaultTimeoutSeconds          = 180
	defaultMaxBodyBytes            = 2 * 1024 * 1024
	defaultRateLimitRPM            = 30
	defaultRateLimitBurst          = 3
	defaultMaxOutputTokens         = 1024
	defaultContextWindow           = 12000
	defaultAutoCompact             = 8000
	defaultMaxPromptChars          = 30000
	defaultResponseStoreTTLMinutes = 30
	defaultResponseStoreMaxEntries = 256
)

type Config struct {
	Addr                    string
	PublicBaseURL           string
	APIKey                  string
	UpstreamBaseURL         string
	UpstreamAPIKey          string
	Model                   string
	UpstreamModel           string
	RequestTimeout          time.Duration
	MaxBodyBytes            int64
	RateLimitRPM            int
	RateLimitBurst          int
	DefaultMaxOutputTokens  int
	ContextWindow           int
	AutoCompactTokenLimit   int
	MaxPromptChars          int
	ResponseStoreTTL        time.Duration
	ResponseStoreMaxEntries int
	LogLevel                string
}

func LoadConfigFromEnv() Config {
	timeoutSeconds := envInt("CODEX_GATEWAY_REQUEST_TIMEOUT_SECONDS", defaultTimeoutSeconds)
	return Config{
		Addr:                    envString("CODEX_GATEWAY_ADDR", defaultAddr),
		PublicBaseURL:           strings.TrimRight(os.Getenv("CODEX_GATEWAY_PUBLIC_BASE_URL"), "/"),
		APIKey:                  os.Getenv("CODEX_GATEWAY_API_KEY"),
		UpstreamBaseURL:         strings.TrimRight(envString("CODEX_GATEWAY_UPSTREAM_BASE_URL", defaultUpstreamBase), "/"),
		UpstreamAPIKey:          os.Getenv("CODEX_GATEWAY_UPSTREAM_API_KEY"),
		Model:                   envString("CODEX_GATEWAY_MODEL", defaultModel),
		UpstreamModel:           envString("CODEX_GATEWAY_UPSTREAM_MODEL", defaultUpstreamModel),
		RequestTimeout:          time.Duration(timeoutSeconds) * time.Second,
		MaxBodyBytes:            int64(envInt("CODEX_GATEWAY_MAX_BODY_BYTES", defaultMaxBodyBytes)),
		RateLimitRPM:            envInt("CODEX_GATEWAY_RATE_LIMIT_RPM", defaultRateLimitRPM),
		RateLimitBurst:          envInt("CODEX_GATEWAY_RATE_LIMIT_BURST", defaultRateLimitBurst),
		DefaultMaxOutputTokens:  envInt("CODEX_GATEWAY_DEFAULT_MAX_OUTPUT_TOKENS", defaultMaxOutputTokens),
		ContextWindow:           envInt("CODEX_GATEWAY_CONTEXT_WINDOW", defaultContextWindow),
		AutoCompactTokenLimit:   envInt("CODEX_GATEWAY_AUTO_COMPACT_TOKEN_LIMIT", defaultAutoCompact),
		MaxPromptChars:          envInt("CODEX_GATEWAY_MAX_PROMPT_CHARS", defaultMaxPromptChars),
		ResponseStoreTTL:        time.Duration(envInt("CODEX_GATEWAY_RESPONSE_STORE_TTL_MINUTES", defaultResponseStoreTTLMinutes)) * time.Minute,
		ResponseStoreMaxEntries: envInt("CODEX_GATEWAY_RESPONSE_STORE_MAX_ENTRIES", defaultResponseStoreMaxEntries),
		LogLevel:                envString("CODEX_GATEWAY_LOG_LEVEL", "info"),
	}
}

func (c Config) normalized() Config {
	if c.Addr == "" {
		c.Addr = defaultAddr
	}
	c.PublicBaseURL = strings.TrimRight(c.PublicBaseURL, "/")
	if c.UpstreamBaseURL == "" {
		c.UpstreamBaseURL = defaultUpstreamBase
	}
	c.UpstreamBaseURL = strings.TrimRight(c.UpstreamBaseURL, "/")
	if c.Model == "" {
		c.Model = defaultModel
	}
	if c.UpstreamModel == "" {
		c.UpstreamModel = defaultUpstreamModel
	}
	if c.RequestTimeout <= 0 {
		c.RequestTimeout = defaultTimeoutSeconds * time.Second
	}
	if c.MaxBodyBytes <= 0 {
		c.MaxBodyBytes = defaultMaxBodyBytes
	}
	if c.RateLimitRPM <= 0 {
		c.RateLimitRPM = defaultRateLimitRPM
	}
	if c.RateLimitBurst <= 0 {
		c.RateLimitBurst = defaultRateLimitBurst
	}
	if c.DefaultMaxOutputTokens <= 0 {
		c.DefaultMaxOutputTokens = defaultMaxOutputTokens
	}
	if c.ContextWindow <= 0 {
		c.ContextWindow = defaultContextWindow
	}
	if c.AutoCompactTokenLimit <= 0 {
		c.AutoCompactTokenLimit = defaultAutoCompact
	}
	if c.MaxPromptChars <= 0 {
		c.MaxPromptChars = defaultMaxPromptChars
	}
	if c.ResponseStoreTTL <= 0 {
		c.ResponseStoreTTL = defaultResponseStoreTTLMinutes * time.Minute
	}
	if c.ResponseStoreMaxEntries <= 0 {
		c.ResponseStoreMaxEntries = defaultResponseStoreMaxEntries
	}
	return c
}

func envString(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func envInt(key string, fallback int) int {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return parsed
}
