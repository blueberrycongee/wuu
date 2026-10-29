package runtime

import (
	"errors"
	"strings"

	"github.com/blueberrycongee/wuu/internal/composition"
	"github.com/blueberrycongee/wuu/internal/config"
)

// ConfigOverrides contains process-scoped agent settings. These values affect the
// runtime being built and are never persisted back to the config file.
type ConfigOverrides struct {
	// CompositionProfile is an explicit product selection, not inferred from identity.
	// Other configured modules, service bindings and patches remain authoritative.
	CompositionProfile string
	AgentProfile       string
	MaxTurns           int
	Effort             string
	Variant            string
	PermissionMode     string
}

// Apply overlays only the explicitly supplied settings onto selected configuration.
func (overrides ConfigOverrides) Apply(cfg *config.Config) error {
	if cfg == nil {
		return nil
	}
	if profile := strings.TrimSpace(overrides.CompositionProfile); profile != "" {
		selected := composition.Config{Version: composition.Version}
		if cfg.Composition != nil {
			selected = *cfg.Composition
		}
		selected.Profile = profile
		cfg.Composition = &selected
	}
	if strings.TrimSpace(overrides.Effort) != "" {
		cfg.Agent.Effort = strings.TrimSpace(overrides.Effort)
		if strings.TrimSpace(overrides.Variant) == "" {
			cfg.Agent.Variant = ""
		}
	}
	if strings.TrimSpace(overrides.Variant) != "" {
		cfg.Agent.Variant = strings.TrimSpace(overrides.Variant)
	}
	if strings.TrimSpace(overrides.AgentProfile) != "" {
		cfg.Agent.Name = strings.TrimSpace(overrides.AgentProfile)
	}
	if overrides.MaxTurns < 0 {
		return errors.New("max turns must be non-negative")
	}
	if overrides.MaxTurns > 0 {
		cfg.Agent.MaxSteps = overrides.MaxTurns
	}
	if strings.TrimSpace(overrides.PermissionMode) != "" {
		cfg.Agent.PermissionMode = config.NormalizePermissionMode(overrides.PermissionMode)
	}
	return nil
}
