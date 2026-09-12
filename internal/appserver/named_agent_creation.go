package appserver

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/blueberrycongee/wuu/internal/agentengine"
)

// Validate the human creation boundary without restricting internal identity
// storage or probing a paid provider. The same local catalog powers the picker.
func (s *Server) validateNamedAgentCreation(params *ChannelAgentCreateParams) error {
	engineID := agentengine.NormalizeEngineID(params.EngineOverride)
	if s.rt == nil || !s.rt.EngineAvailable(engineID) {
		return agentengine.ErrUnknownEngine
	}
	if engineID != agentengine.EngineWuu {
		return errors.New("collaboration requires a BYOK model on the Wuu execution runtime")
	}
	params.EngineOverride = string(engineID)
	params.ProviderOverride = strings.TrimSpace(params.ProviderOverride)
	params.ModelOverride = strings.TrimSpace(params.ModelOverride)
	params.EffortOverride = strings.TrimSpace(params.EffortOverride)
	if params.ProviderOverride == "" || params.ModelOverride == "" {
		return errors.New("choose a configured provider and model for this agent")
	}
	cfg, _, err := s.rt.LoadEffectiveConfig()
	if err != nil {
		return err
	}
	provider, name, err := cfg.ResolveProvider(params.ProviderOverride)
	if err != nil {
		return err
	}
	if !providerHasAuth(name, provider, os.Getenv("HOME")) {
		return fmt.Errorf("provider %q needs credentials before creating an agent", name)
	}
	provider = s.withCachedCodexModels(name, provider)
	if model, ok := provider.Models[params.ModelOverride]; ok && model.Disabled {
		return fmt.Errorf("model %q is disabled for provider %q", params.ModelOverride, name)
	}
	for _, model := range providerModelSummaries(name, provider) {
		if model.ID != params.ModelOverride {
			continue
		}
		efforts := model.SupportedEfforts
		if len(model.Variants) > 0 {
			efforts = make([]string, 0, len(model.Variants))
			for _, variant := range model.Variants {
				efforts = append(efforts, variant.ID)
			}
		}
		if len(efforts) == 0 && (params.EffortOverride == "" || (params.EffortOverride == "none" && model.Capabilities.Reasoning)) {
			return nil
		}
		if len(efforts) == 1 && params.EffortOverride == "" {
			params.EffortOverride = efforts[0]
			return nil
		}
		for _, effort := range efforts {
			if effort != "" && effort == params.EffortOverride {
				return nil
			}
		}
		if params.EffortOverride == "" {
			return fmt.Errorf("choose a reasoning effort for model %q", model.ID)
		}
		return fmt.Errorf("model %q does not support reasoning effort %q", model.ID, params.EffortOverride)
	}
	return fmt.Errorf("model %q is not configured or available for provider %q", params.ModelOverride, name)
}
