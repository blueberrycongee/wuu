package modelvariant

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/blueberrycongee/wuu/internal/config"
)

// SpeedSupport reports acceleration separately from reasoning variants.
// Explicit model configuration takes precedence over catalog inference.
func SpeedSupport(provider config.ProviderConfig, model string) (supported bool, defaultSpeed string) {
	key, defaultSpeed := speedOption(provider, model)
	return key != "", defaultSpeed
}

func speedOption(provider config.ProviderConfig, model string) (string, string) {
	entry := provider.Models[model]
	if entry.FastMode != nil && !*entry.FastMode {
		return "", ""
	}
	anthropic := normalizedProviderType(provider.Type) == "anthropic" || normalizedProviderType(provider.Type) == "anthropic-official" || normalizedProviderType(provider.Type) == "claude" || strings.TrimPrefix(provider.NPM, "npm:") == "@ai-sdk/anthropic"
	optionKey := "serviceTier"
	if anthropic {
		optionKey = "speed"
	}
	key, defaultSpeed := "", "standard"
	if value, _ := entry.Options[optionKey].(string); value == "fast" || value == "priority" {
		key, defaultSpeed = optionKey, "fast"
	}
	apiModel := entry.ID
	if apiModel == "" {
		apiModel = model
	}
	// Catalog acceleration aliases share an API model. Preserve saved aliases
	// while exposing the same capability on their base model.
	for id, candidate := range provider.Models {
		apiID := candidate.ID
		if apiID == "" {
			apiID = id
		}
		if apiID != apiModel || candidate.Disabled {
			continue
		}
		if value, _ := candidate.Options[optionKey].(string); value == "fast" || value == "priority" {
			key = optionKey
		}
	}

	// The direct API documents these models; compatible endpoints opt in
	// through fast_mode or an existing speed option instead.
	endpoint, _ := url.Parse(provider.BaseURL)
	if anthropic && endpoint != nil && endpoint.Hostname() == "api.anthropic.com" {
		switch apiModel {
		case "claude-opus-5-5", "claude-opus-5", "claude-opus-4-8":
			key = "speed"
		}
	}
	if key == "" && entry.FastMode != nil && *entry.FastMode {
		key = optionKey
	}
	return key, defaultSpeed
}

// ApplySpeed overrides only the provider's speed option. Empty inherits the
// configured default; standard explicitly overrides an accelerated default.
func ApplySpeed(provider config.ProviderConfig, model, speed string, selection *Selection) error {
	if speed == "" {
		return nil
	}
	if speed != "fast" && speed != "standard" {
		return fmt.Errorf("unsupported speed %q", speed)
	}
	key, _ := speedOption(provider, model)
	if key == "" {
		if speed == "standard" {
			return nil
		}
		return fmt.Errorf("model %s does not advertise fast mode", model)
	}
	if selection.ProviderOptions == nil {
		selection.ProviderOptions = map[string]any{}
	}
	value := speed
	if key == "serviceTier" {
		value = "default"
		if speed == "fast" {
			value = "priority"
		}
	}
	selection.ProviderOptions[key] = value
	return nil
}
