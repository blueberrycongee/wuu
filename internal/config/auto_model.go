package config

import (
	"fmt"
	"strings"
)

// AutoModelID is a local selection, never an upstream model identifier.
const AutoModelID = "wuu/auto"

type AutoModelConfig struct {
	Enabled    bool            `json:"enabled"`
	Default    bool            `json:"default"`
	Classifier ModelRoleConfig `json:"classifier"`
	Simple     ModelRoleConfig `json:"simple"`
	Medium     ModelRoleConfig `json:"medium"`
	Complex    ModelRoleConfig `json:"complex"`
}

func (a AutoModelConfig) Selections() []ModelRoleConfig {
	return []ModelRoleConfig{a.Classifier, a.Simple, a.Medium, a.Complex}
}

func (c Config) ValidateAutoModel() error {
	for _, p := range c.Providers {
		if p.Model == AutoModelID {
			return fmt.Errorf("Auto must be selected through agent.auto_model, not an upstream provider model")
		}
	}
	a := c.Agent.AutoModel
	if a == nil || !a.Enabled {
		return nil
	}
	for i, selection := range a.Selections() {
		name := []string{"classifier", "simple", "medium", "complex"}[i]
		if strings.TrimSpace(selection.Provider) == "" || strings.TrimSpace(selection.Model) == "" || selection.Model == AutoModelID {
			return fmt.Errorf("auto model %s requires an explicit provider and execution model", name)
		}
		p, _, err := c.ResolveProvider(selection.Provider)
		if err != nil {
			return fmt.Errorf("auto model %s: %w", name, err)
		}
		if err := validateSelectionOptions("agent.auto_model."+name, selection, p, selection.Model); err != nil {
			return err
		}
		if m, ok := p.Models[selection.Model]; ok && m.Disabled {
			return fmt.Errorf("auto model %s: model %q is disabled", name, selection.Model)
		}
	}
	return nil
}

func (c Config) AutoModelSelection() (ModelRoleConfig, error) {
	if c.Agent.AutoModel == nil || !c.Agent.AutoModel.Enabled {
		return ModelRoleConfig{}, fmt.Errorf("Auto is not configured or is disabled")
	}
	if err := c.ValidateAutoModel(); err != nil {
		return ModelRoleConfig{}, err
	}
	return c.Agent.AutoModel.Medium, nil
}
