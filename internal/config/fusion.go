package config

import (
	"fmt"
	"strings"
)

// FusionID is a local selection, never an upstream model identifier.
const FusionID = "wuu/fusion"

type FusionConfig struct {
	Enabled  bool            `json:"enabled"`
	Default  bool            `json:"default"`
	Lead     ModelRoleConfig `json:"lead"`
	Sidekick ModelRoleConfig `json:"sidekick"`
}

// FusionSelection pins the pair for a conversation without persisting credentials.
type FusionSelection struct {
	Lead     ModelRoleConfig `json:"lead"`
	Sidekick ModelRoleConfig `json:"sidekick"`
}

func (a FusionConfig) Selections() []ModelRoleConfig {
	return []ModelRoleConfig{a.Lead, a.Sidekick}
}

func (c Config) ValidateFusion() error {
	for _, p := range c.Providers {
		if strings.TrimSpace(p.Model) == FusionID {
			return fmt.Errorf("Fusion must be selected through agent.fusion, not an upstream provider model")
		}
	}
	a := c.Agent.Fusion
	if a == nil || !a.Enabled {
		return nil
	}
	for i, selection := range a.Selections() {
		name := []string{"lead", "sidekick"}[i]
		if strings.TrimSpace(selection.Provider) == "" || strings.TrimSpace(selection.Model) == "" || strings.TrimSpace(selection.Model) == FusionID {
			return fmt.Errorf("fusion model %s requires an explicit provider and execution model", name)
		}
		p, _, err := c.ResolveProvider(selection.Provider)
		if err != nil {
			return fmt.Errorf("fusion model %s: %w", name, err)
		}
		if err := validateSelectionOptions("agent.fusion."+name, selection, p, selection.Model); err != nil {
			return err
		}
		if m, ok := p.Models[selection.Model]; ok && m.Disabled {
			return fmt.Errorf("fusion model %s: model %q is disabled", name, selection.Model)
		}
	}
	return nil
}

func (c Config) FusionSelection() (ModelRoleConfig, error) {
	if c.Agent.Fusion == nil || !c.Agent.Fusion.Enabled {
		return ModelRoleConfig{}, fmt.Errorf("Fusion is not configured or is disabled")
	}
	if err := c.ValidateFusion(); err != nil {
		return ModelRoleConfig{}, err
	}
	return c.Agent.Fusion.Lead, nil
}
