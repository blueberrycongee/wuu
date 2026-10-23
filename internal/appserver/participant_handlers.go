package appserver

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/blueberrycongee/wuu/internal/agentcontrol"
	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/participant"
	"github.com/blueberrycongee/wuu/internal/providerfactory"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/runtime"
)

// workerProviderName returns the provider name used by worker runs.
func workerProviderName(rt *runtime.Session) string {
	if rt == nil {
		return ""
	}
	if name := strings.TrimSpace(rt.ModelRoles.Worker.Provider); name != "" {
		return name
	}
	return strings.TrimSpace(rt.ProviderName)
}

// parseParticipantModelPin splits a participant model pin into provider and
// model parts. A bare model follows the current worker provider.
func parseParticipantModelPin(value string) (providerName, modelName string) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", ""
	}
	idx := strings.Index(value, ":")
	if idx < 0 {
		return "", value
	}
	return strings.TrimSpace(value[:idx]), strings.TrimSpace(value[idx+1:])
}

// resolveParticipantModelOverride resolves the model and optional provider
// client for a named-agent Run. Invalid pins fail the Run instead of silently
// falling back to another model.
func resolveParticipantModelOverride(rt *runtimeSessionReference, participantName, rawPin, workerProviderName string) (modelOverride string, clientOverride providers.StreamClient, err error) {
	providerName, modelName := parseParticipantModelPin(rawPin)
	if providerName == "" && modelName == "" {
		return "", nil, nil
	}
	if modelName == "" {
		return "", nil, fmt.Errorf("participant %q pins model %q but model name is empty", participantName, rawPin)
	}
	if providerName == "" || providerName == workerProviderName {
		return modelName, nil, nil
	}
	if rt == nil || rt.services == nil {
		return "", nil, fmt.Errorf("participant %q pins model %q but no runtime config is available", participantName, rawPin)
	}
	cfg, loadErr := rt.loadEffectiveConfig()
	if loadErr != nil {
		return "", nil, fmt.Errorf("participant %q pins model %q but config could not be loaded: %w", participantName, rawPin, loadErr)
	}
	providerCfg, found := cfg.Providers[providerName]
	if !found {
		return "", nil, fmt.Errorf("participant %q pins model %q but provider %q is not configured", participantName, rawPin, providerName)
	}
	client, buildErr := providerfactory.BuildStreamClient(providerCfg, providerName, providerfactory.Bindings{Credentials: rt.services.Credentials, ChatGPT: rt.services.ChatGPT})
	if buildErr != nil {
		return "", nil, fmt.Errorf("participant %q pins model %q but provider %q failed to build: %w", participantName, rawPin, providerName, buildErr)
	}
	return modelName, client, nil
}

type runtimeSessionReference struct {
	services *runtime.StateServices
}

// The admitted turn owns these services for the entire callback lifetime.
func newRuntimeSessionReference(services *runtime.StateServices) *runtimeSessionReference {
	return &runtimeSessionReference{services: services}
}

func (rt *runtimeSessionReference) loadEffectiveConfig() (config.Config, error) {
	if rt == nil || rt.services == nil {
		return config.Config{}, errors.New("runtime config is unavailable")
	}
	view, err := rt.services.Config.Read(context.Background())
	return view.Config, err
}

func resolveParticipantSubagentType(explicitSubagentType string, _ participant.Participant) string {
	if value := strings.TrimSpace(explicitSubagentType); value != "" {
		return value
	}
	return agentcontrol.DefaultSubagentType
}

func participantRunSummary(values ...string) string {
	for _, value := range values {
		summary := strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
		if summary == "" {
			continue
		}
		runes := []rune(summary)
		if len(runes) > 180 {
			return string(runes[:180])
		}
		return summary
	}
	return ""
}
