package cognitivegateway

import "time"

type Config struct {
	ListenAddr             string                   `json:"listen_addr"`
	DefaultMode            Mode                     `json:"default_mode"`
	AcceptedAPIKeys        []string                 `json:"accepted_api_keys"`
	Backends               map[string]BackendConfig `json:"backends"`
	Modes                  map[Mode]ModeConfig      `json:"modes"`
	RequestTimeoutSeconds  int                      `json:"request_timeout_seconds"`
	LogLevel               string                   `json:"log_level"`
	JSONValidation         JSONValidationConfig     `json:"json_validation"`
	ContextBuilderURL      string                   `json:"context_builder_url"`
	ContextBuilderAPIKey   string                   `json:"context_builder_api_key"`
	ContextBuilderRequired bool                     `json:"context_builder_required"`
	OperationalIR          OperationalIRConfig      `json:"operational_ir"`
	ResponseFooter         ResponseFooterConfig     `json:"response_footer"`
}

type BackendConfig struct {
	BaseURL string `json:"base_url"`
	APIKey  string `json:"api_key"`
	Model   string `json:"model"`
}

type ModeConfig struct {
	RouteTo      string      `json:"route_to"`
	SystemPrompt string      `json:"system_prompt"`
	Policies     []string    `json:"policies"`
	Middlewares  []string    `json:"middlewares"`
	TokenBudget  TokenBudget `json:"token_budget"`
}

type TokenBudget struct {
	MaxPromptTokens     int `json:"max_prompt_tokens"`
	MaxCompletionTokens int `json:"max_completion_tokens"`
}

type JSONValidationConfig struct {
	WarnOnly bool `json:"warn_only"`
}

type ResponseFooterConfig struct {
	Enabled bool `json:"enabled"`
}

type OperationalIRConfig struct {
	Enabled             bool `json:"enabled"`
	MaxChars            int  `json:"max_chars"`
	PlanJSONataRequired bool `json:"plan_jsonata_required"`
}

type Mode string

const (
	ModePlan     Mode = "PLAN_MODE"
	ModeCode     Mode = "CODE_MODE"
	ModeReview   Mode = "REVIEW_MODE"
	ModeResearch Mode = "RESEARCH_MODE"
)

type ChatCompletionRequest struct {
	Model          string         `json:"model,omitempty"`
	Messages       []ChatMessage  `json:"messages"`
	Stream         bool           `json:"stream,omitempty"`
	Temperature    any            `json:"temperature,omitempty"`
	MaxTokens      any            `json:"max_tokens,omitempty"`
	ResponseFormat map[string]any `json:"response_format,omitempty"`
	Extra          map[string]any `json:"-"`
}

type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type contextState struct {
	Config        Config
	Request       ChatCompletionRequest
	Mode          Mode
	ModeConfig    ModeConfig
	BackendName   string
	Backend       BackendConfig
	Estimated     int
	InitialTokens int
	Streaming     bool
	StartedAt     time.Time
	RequestID     string
	ValidationErr string
	TaskID        string
	InitiativeID  string
	WorkspaceRoot string
	ContextError  string
	IRChars       int
}
