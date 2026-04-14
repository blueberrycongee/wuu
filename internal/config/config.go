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

// ErrConfigNotFound is returned by LoadFrom when none of the candidate
// config files exist on disk. Callers should use errors.Is to
// distinguish a missing config (where running onboarding is the right
// recovery) from a present-but-broken config (where overwriting it
// would silently destroy the user's work).
var ErrConfigNotFound = errors.New("config not found")

// HookEntry defines a single hook command bound to a lifecycle event.
type HookEntry struct {
	Matcher string `json:"matcher,omitempty"` // tool name pattern, "*" or empty = match all
	Type    string `json:"type,omitempty"`    // "command" (default) or "prompt"
	Command string `json:"command,omitempty"` // for type=command — shell command
	Prompt  string `json:"prompt,omitempty"`  // for type=prompt — evaluation prompt
	Model   string `json:"model,omitempty"`   // for type=prompt — model to use
	Timeout int    `json:"timeout,omitempty"` // seconds, default 30
}

// Config holds CLI runtime settings.
type Config struct {
	DefaultProvider string                    `json:"default_provider"`
	Providers       map[string]ProviderConfig `json:"providers"`
	Agent           AgentConfig               `json:"agent"`
	Hooks           map[string][]HookEntry    `json:"hooks,omitempty"`
	Memory          MemoryConfig              `json:"memory,omitempty"`
}

// MemoryConfig overrides the defaults for memory file discovery
// (CLAUDE.md / AGENTS.md auto-loading). All fields are optional;
// empty values fall back to memory.DefaultOptions().
type MemoryConfig struct {
	// Filenames to look for in priority order. Default:
	// ["AGENTS.md", "AGENTS.override.md", "CLAUDE.md"].
	Filenames []string `json:"filenames,omitempty"`
	// ProjectRootMarkers stop the upward walk through ancestors.
	// Default: [".git", ".hg", ".jj", ".svn"].
	ProjectRootMarkers []string `json:"project_root_markers,omitempty"`
	// UserDirs are scanned for user-level memory. Tilde-expanded.
	// Default: ["~/.config/wuu", "~/.claude", "~/.codex"].
	UserDirs []string `json:"user_dirs,omitempty"`
	// Disable turns off memory loading entirely.
	Disable bool `json:"disable,omitempty"`
}

// ProviderConfig configures one model gateway.
type ProviderConfig struct {
	Type         string            `json:"type"`
	BaseURL      string            `json:"base_url"`
	APIKey       string            `json:"api_key,omitempty"`
	APIKeyEnv    string            `json:"api_key_env,omitempty"`
	AuthToken    string            `json:"auth_token,omitempty"`
	AuthTokenEnv string            `json:"auth_token_env,omitempty"`
	Model        string            `json:"model"`
	Headers      map[string]string `json:"headers,omitempty"`
	// StreamConnectTimeoutMS bounds dial/TLS/response-header wait for one
	// streaming connection attempt. It does not cap the whole turn.
	StreamConnectTimeoutMS int `json:"stream_connect_timeout_ms,omitempty"`
	// StreamIdleTimeoutMS bounds silence after the streaming response has
	// started. It does not affect the initial connect stage.
	StreamIdleTimeoutMS int `json:"stream_idle_timeout_ms,omitempty"`
	// ContextWindow optionally overrides wuu's built-in registry for
	// this provider's model. Use it for new models wuu doesn't know
	// about yet, custom finetunes, private deployments, or proxies
	// that rename the upstream model. Zero means "use the registry".
	ContextWindow int `json:"context_window,omitempty"`
}

// AgentConfig controls behavior of the local tool loop.
type AgentConfig struct {
	MaxSteps         int     `json:"max_steps"`
	MaxContextTokens int     `json:"max_context_tokens"`
	Temperature      float64 `json:"temperature"`
	SystemPrompt     string  `json:"system_prompt"`
	// DisableAutoCompact turns off the proactive auto-compact pass
	// that fires when the conversation reaches ~90% of the model's
	// context window. The reactive overflow recovery (compact triggered
	// by an actual context_length_exceeded error) still runs. Use this
	// when you want full control over compact via the slash command,
	// or when you're debugging compact behavior itself.
	DisableAutoCompact bool `json:"disable_auto_compact,omitempty"`
	// CatwalkAutoupdate enables the background fetch from charm.land's
	// catwalk service to refresh the model→context-window registry
	// between wuu builds. Disabled by default — wuu's embedded
	// snapshot is already curated and the remote fetch isn't needed
	// unless the user is on the bleeding edge of new models. When
	// disabled, only the embedded data ships with each wuu binary
	// is used.
	CatwalkAutoupdate bool `json:"catwalk_autoupdate,omitempty"`
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

	return Config{}, "", fmt.Errorf("%w, run `wuu init` to create %s", ErrConfigNotFound, localPrimaryConfig)
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
		if provider.StreamConnectTimeoutMS < 0 {
			return fmt.Errorf("providers.%s.stream_connect_timeout_ms cannot be negative", name)
		}
		if provider.StreamIdleTimeoutMS < 0 {
			return fmt.Errorf("providers.%s.stream_idle_timeout_ms cannot be negative", name)
		}
	}

	if c.Agent.MaxSteps < 0 {
		return errors.New("agent.max_steps cannot be negative (use 0 for unlimited)")
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
			"codex": {
				Type:      "codex",
				BaseURL:   "https://api.openai.com/v1",
				APIKeyEnv: "OPENAI_API_KEY",
				Model:     "gpt-5-codex",
			},
			"anthropic": {
				Type:      "anthropic",
				BaseURL:   "https://api.anthropic.com",
				APIKeyEnv: "ANTHROPIC_API_KEY",
				Model:     "claude-3-5-sonnet-latest",
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
			// 0 = unlimited. Aligned with Claude Code, which has no
			// default step cap; the model decides when to stop. Users
			// who want a runaway safety net can set this explicitly.
			MaxSteps:    0,
			Temperature: 0.2,
			SystemPrompt: "You are a pragmatic CLI coding agent. Use tools when needed. " +
				"The main interactive agent is read-oriented: inspect code, reason about changes, and delegate file mutations or shell commands to workers when execution is needed. " +
				"Always explain what changed or what decision you made. " +
				"Think in three comment buckets: 'what', 'why', and future-intent/status comments. " +
				"Do not write 'what' comments that merely restate the code. " +
				"Write 'why' comments only when they preserve a non-obvious rationale or tradeoff, and keep them sparse, factual, and up to the standard of top-tier open-source projects. " +
				"Do not leave future-intent/status comments such as 'I will do it later' or other speculative notes. Treat every comment as long-lived documentation that future agents will read, so avoid anything misleading or not true at the time it is written.",
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

// UpdateProviderModel changes the model field for a named provider in
// the config file at configPath and writes it back. It operates on the
// raw JSON to preserve unknown fields and formatting.
func UpdateProviderModel(configPath, providerName, newModel string) error {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return fmt.Errorf("read config: %w", err)
	}

	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("parse config: %w", err)
	}

	providers, ok := raw["providers"].(map[string]any)
	if !ok {
		return fmt.Errorf("providers section not found")
	}
	provider, ok := providers[providerName].(map[string]any)
	if !ok {
		return fmt.Errorf("provider %q not found", providerName)
	}
	provider["model"] = newModel

	out, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	return os.WriteFile(configPath, append(out, '\n'), 0644)
}

func applyDefaults(cfg *Config) {
	// max_steps = 0 means unlimited (no step cap, the model decides
	// when to stop). Aligned with Claude Code's default behavior.
	// Users who set an explicit positive value get a hard cap.
	if cfg.Agent.Temperature == 0 {
		cfg.Agent.Temperature = 0.2
	}
	if cfg.Agent.SystemPrompt == "" {
		cfg.Agent.SystemPrompt = Default().Agent.SystemPrompt
	}
}
