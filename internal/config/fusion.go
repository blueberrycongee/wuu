package config

import (
	"fmt"
	"strings"
)

type FusionConfig struct {
	Enabled  bool            `json:"enabled"`
	Default  bool            `json:"default"`
	Lead     ModelRoleConfig `json:"lead"`
	Sidekick ModelRoleConfig `json:"sidekick"`
}

// FusionSelection is the credential-free pair pinned to a conversation.
type FusionSelection struct {
	Lead     ModelRoleConfig `json:"lead"`
	Sidekick ModelRoleConfig `json:"sidekick"`
}

func (c Config) ValidateFusionPair(pair FusionSelection) error {
	for role, selection := range map[string]ModelRoleConfig{"lead": pair.Lead, "sidekick": pair.Sidekick} {
		if strings.TrimSpace(selection.Model) == "wuu/fusion" {
			return fmt.Errorf("Fusion requires a real %s model", role)
		}
		if err := validateConfiguredModelSelection(c, "agent.fusion."+role, selection); err != nil {
			return err
		}
		if c.Providers[selection.Provider].Models[selection.Model].Disabled {
			return fmt.Errorf("Fusion %s model is disabled", role)
		}
	}
	return nil
}

func (c Config) FusionPair() (FusionSelection, error) {
	if c.Agent.Fusion == nil || !c.Agent.Fusion.Enabled {
		return FusionSelection{}, fmt.Errorf("Fusion is disabled; configure its Lead and Sidekick models in settings")
	}
	pair := FusionSelection{Lead: c.Agent.Fusion.Lead, Sidekick: c.Agent.Fusion.Sidekick}
	return pair, c.ValidateFusionPair(pair)
}
