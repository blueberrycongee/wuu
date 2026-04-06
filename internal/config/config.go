package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

const (
	localPrimaryConfig   = ".wuu.json"
	localFallbackConfig  = "wuu.json"
	globalConfigRelative = ".config/wuu/config.json"
)

// Config holds CLI runtime settings.
type Config struct {
	DefaultProvider string                    `json:"default_provider"`
	Providers       map[string]ProviderConfig `json:"providers"`
	Agent           AgentConfig               `json:"agent"`
}

// ProviderConfig configures one model gateway.
type ProviderConfig struct {
	Type      string            `json:"type"`
	BaseURL   string            `json:"base_url"`
	APIKey    string            `json:"api_key,omitempty"`
	APIKeyEnv string            `json:"api_key_env,omitempty"`
	Model     string            `json:"model"`
	Headers   map[string]string `json:"headers,omitempty"`
}

// AgentConfig controls behavior of the local tool loop.
type AgentConfig struct {
	MaxSteps     int     `json:"max_steps"`
	Temperature  float64 `json:"temperature"`
	SystemPrompt string  `json:"system_prompt"`
}

// Load reads config with priority: .wuu.json, wuu.json, ~/.config/wuu/config.json.
func Load() (Config, string, error) {
	workdir, err := os.Getwd()
	if err != nil {
		return Config{}, "", fmt.Errorf("get cwd: %w", err)
	}
	return LoadFrom(workdir, os.Getenv("HOME"))
}

// LoadFrom reads config from deterministic directories (test-friendly).
func LoadFrom(workdir, home string) (Config, string, error) {
	candidates := []string{
		filepath.Join(workdir, localPrimaryConfig),
		filepath.Join(workdir, localFallbackConfig),
	}
	if home != "" {
		candidates = append(candidates, filepath.Join(home, globalConfigRelative))
	}

	for _, candidate := range candidates {
		if _, err := os.Stat(candidate); err == nil {
			cfg, readErr := readConfig(candidate)
			if readErr != nil {
				return Config{}, "", readErr
			}
			applyDefaults(&cfg)
			if validateErr := cfg.Validate(); validateErr != nil {
				return Config{}, "", validateErr
			}
			return cfg, candidate, nil
		}
	}

	return Config{}, "", fmt.Errorf("config not found, run `wuu init` to create %s", localPrimaryConfig)
}

func readConfig(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("read config %s: %w", path, err)
	}

	var cfg Config
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cfg); err != nil {
		return Config{}, fmt.Errorf("parse config %s: %w", path, err)
	}

	return cfg, nil
}

// ResolveProvider returns explicit provider or default one.
func (c Config) ResolveProvider(name string) (ProviderConfig, string, error) {
	if len(c.Providers) == 0 {
		return ProviderConfig{}, "", errors.New("providers is empty")
	}

	if name != "" {
		p, ok := c.Providers[name]
		if !ok {
			return ProviderConfig{}, "", fmt.Errorf("provider %q not found", name)
		}
		return p, name, nil
	}

	p, ok := c.Providers[c.DefaultProvider]
	if !ok {
		return ProviderConfig{}, "", fmt.Errorf("default provider %q not found", c.DefaultProvider)
	}
	return p, c.DefaultProvider, nil
}

// Validate performs semantic checks.
func (c Config) Validate() error {
	if len(c.Providers) == 0 {
		return errors.New("providers is required")
	}
	if c.DefaultProvider == "" {
		return errors.New("default_provider is required")
	}
	if _, ok := c.Providers[c.DefaultProvider]; !ok {
		return fmt.Errorf("default_provider %q not found in providers", c.DefaultProvider)
	}

	for name, provider := range c.Providers {
		if provider.Type == "" {
			return fmt.Errorf("providers.%s.type is required", name)
		}
		if provider.BaseURL == "" {
			return fmt.Errorf("providers.%s.base_url is required", name)
		}
		if provider.Model == "" {
			return fmt.Errorf("providers.%s.model is required", name)
		}
	}

	if c.Agent.MaxSteps <= 0 {
		return errors.New("agent.max_steps must be > 0")
	}
	if c.Agent.Temperature < 0 || c.Agent.Temperature > 2 {
		return errors.New("agent.temperature must be in [0,2]")
	}
	if c.Agent.SystemPrompt == "" {
		return errors.New("agent.system_prompt is required")
	}

	return nil
}

// Default returns a practical starter config.
func Default() Config {
	return Config{
		DefaultProvider: "openai",
		Providers: map[string]ProviderConfig{
			"openai": {
				Type:      "openai-compatible",
				BaseURL:   "https://api.openai.com/v1",
				APIKeyEnv: "OPENAI_API_KEY",
				Model:     "gpt-4.1",
			},
			"openrouter": {
				Type:      "openai-compatible",
				BaseURL:   "https://openrouter.ai/api/v1",
				APIKeyEnv: "OPENROUTER_API_KEY",
				Model:     "openai/gpt-4.1-mini",
				Headers: map[string]string{
					"HTTP-Referer": "https://github.com/blueberrycongee/wuu",
					"X-Title":      "wuu",
				},
			},
		},
		Agent: AgentConfig{
			MaxSteps:    8,
			Temperature: 0.2,
			SystemPrompt: "You are a pragmatic CLI coding agent. Use tools when needed. " +
				"When writing files, prefer minimal safe changes. Always explain what changed.",
		},
	}
}

// TemplateJSON returns a formatted starter config file.
func TemplateJSON() (string, error) {
	cfg := Default()
	buf, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return "", err
	}
	return string(buf) + "\n", nil
}

func applyDefaults(cfg *Config) {
	if cfg.Agent.MaxSteps == 0 {
		cfg.Agent.MaxSteps = 8
	}
	if cfg.Agent.Temperature == 0 {
		cfg.Agent.Temperature = 0.2
	}
	if cfg.Agent.SystemPrompt == "" {
		cfg.Agent.SystemPrompt = Default().Agent.SystemPrompt
	}
}
