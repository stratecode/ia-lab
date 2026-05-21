package cognitivegateway

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"
)

func LoadConfig(path string) (Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}
	var cfg Config
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return Config{}, err
	}
	if strings.TrimSpace(cfg.ListenAddr) == "" {
		return Config{}, fmt.Errorf("listen_addr is required")
	}
	if cfg.DefaultMode == "" {
		cfg.DefaultMode = ModeCode
	}
	if cfg.RequestTimeoutSeconds <= 0 {
		cfg.RequestTimeoutSeconds = 180
	}
	if !strings.Contains(string(raw), `"response_footer"`) {
		cfg.ResponseFooter.Enabled = true
	}
	if !strings.Contains(string(raw), `"operational_ir"`) {
		cfg.OperationalIR.Enabled = true
		cfg.OperationalIR.MaxChars = 6000
		cfg.OperationalIR.PlanJSONataRequired = true
	}
	if cfg.OperationalIR.MaxChars <= 0 {
		cfg.OperationalIR.MaxChars = 6000
	}
	if len(cfg.Backends) == 0 {
		return Config{}, fmt.Errorf("at least one backend is required")
	}
	if len(cfg.Modes) == 0 {
		return Config{}, fmt.Errorf("at least one mode is required")
	}
	for mode, modeCfg := range cfg.Modes {
		if strings.TrimSpace(modeCfg.RouteTo) == "" {
			return Config{}, fmt.Errorf("mode %s missing route_to", mode)
		}
		if _, ok := cfg.Backends[modeCfg.RouteTo]; !ok {
			return Config{}, fmt.Errorf("mode %s routes to unknown backend %q", mode, modeCfg.RouteTo)
		}
	}
	return cfg, nil
}

func (c Config) Timeout() time.Duration {
	return time.Duration(c.RequestTimeoutSeconds) * time.Second
}

func (c Config) IsAcceptedAPIKey(value string) bool {
	if len(c.AcceptedAPIKeys) == 0 {
		return true
	}
	token := strings.TrimSpace(strings.TrimPrefix(value, "Bearer "))
	for _, accepted := range c.AcceptedAPIKeys {
		if token != "" && token == strings.TrimSpace(accepted) {
			return true
		}
	}
	return false
}
