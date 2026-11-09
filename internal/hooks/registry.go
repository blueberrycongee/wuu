package hooks

import (
	"strings"
	"time"
)

const defaultTimeout = 30 * time.Second

// HookConfig is one hook entry as declared in the config file.
// This type lives in the hooks package so external packages (config.go,
// main.go) can load the raw form, convert it once at startup, and hand
// it to the registry.
type HookConfig struct {
	Matcher    string `json:"matcher,omitempty"` // tool name pattern, "*" or empty = match all
	Type       string `json:"type,omitempty"`    // "command" (default) or "prompt"
	Command    string `json:"command,omitempty"` // for type=command
	Prompt     string `json:"prompt,omitempty"`  // for type=prompt — the evaluation prompt
	Model      string `json:"model,omitempty"`   // for type=prompt — model to use (default: configured)
	Timeout    int    `json:"timeout,omitempty"` // seconds, default 30
	sessionTag string // internal: identifies session-scoped hooks for cleanup
}

// Registry holds hook configuration and matches hooks against events.
// Build it once via NewRegistry and share across dispatcher calls.
type Registry struct {
	entries     map[Event][]HookConfig
	modelClient PromptModelClient // optional; required for prompt hooks
}

// NewRegistry creates a registry from a preloaded map of events to hook
// configs. Nil input is treated as an empty registry.
func NewRegistry(entries map[Event][]HookConfig) *Registry {
	if entries == nil {
		entries = make(map[Event][]HookConfig)
	}
	return &Registry{entries: entries}
}

// Match returns all hooks whose matcher pattern matches the given tool
// name. For events without a tool name (SessionStart, Stop, etc.), pass
// an empty string — empty matchers and "*" match anything.
func (r *Registry) Match(ev Event, toolName string) []Hook {
	configs, ok := r.entries[ev]
	if !ok {
		return nil
	}

	var matched []Hook
	for _, cfg := range configs {
		if !matchesPattern(cfg.Matcher, toolName) {
			continue
		}
		timeout := defaultTimeout
		if cfg.Timeout > 0 {
			timeout = time.Duration(cfg.Timeout) * time.Second
		}
		switch cfg.Type {
		case "prompt":
			matched = append(matched, &PromptHook{
				PromptTemplate: cfg.Prompt,
				Model:          cfg.Model,
				Timeout:        timeout,
				Client:         r.modelClient,
			})
		default: // "command" or empty
			matched = append(matched, &CommandHook{
				Command: cfg.Command,
				Timeout: timeout,
			})
		}
	}
	return matched
}

// SetModelClient sets the model client used by PromptHook instances.
// Must be called before any PromptHook is executed.
func (r *Registry) SetModelClient(client PromptModelClient) {
	r.modelClient = client
}

// HasHooks returns true if any hooks are registered for the event.
// Callers use this to short-circuit input construction when no hooks
// would fire anyway.
func (r *Registry) HasHooks(ev Event) bool {
	configs, ok := r.entries[ev]
	return ok && len(configs) > 0
}

// RegisterSessionHooks adds temporary hooks (e.g. from skill frontmatter).
// Call UnregisterSessionHooks with the same tag to remove them.
func (r *Registry) RegisterSessionHooks(tag string, cfgs map[Event][]HookConfig) {
	for ev, list := range cfgs {
		for _, cfg := range list {
			cfg.sessionTag = tag
			r.entries[ev] = append(r.entries[ev], cfg)
		}
	}
}

// UnregisterSessionHooks removes all hooks registered with the given tag.
func (r *Registry) UnregisterSessionHooks(tag string) {
	for ev, cfgs := range r.entries {
		filtered := cfgs[:0]
		for _, cfg := range cfgs {
			if cfg.sessionTag != tag {
				filtered = append(filtered, cfg)
			}
		}
		r.entries[ev] = filtered
	}
}

func matchesPattern(pattern, toolName string) bool {
	if pattern == "" || pattern == "*" {
		return true
	}
	return strings.EqualFold(pattern, toolName)
}
