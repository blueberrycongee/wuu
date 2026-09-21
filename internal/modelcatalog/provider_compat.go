package modelcatalog

import (
	"strings"

	"github.com/blueberrycongee/wuu/internal/config"
)

const (
	kimiForCodingProviderID      = "kimi-for-coding"
	kimiCodePlanCNProviderID     = "kimi-code-plan-cn"
	kimiCodePlanGlobalProviderID = "kimi-code-plan-global"
)

func isKimiCodingProviderID(providerID string) bool {
	switch normalizeID(providerID) {
	case kimiForCodingProviderID, kimiCodePlanCNProviderID, kimiCodePlanGlobalProviderID:
		return true
	default:
		return false
	}
}

// applyProviderCompatibilityDefaults adds request-transport defaults that
// models.dev does not describe. These are deliberately kept separate from
// catalog facts such as modalities, limits, and reasoning efforts. Explicit
// user headers and model options always win over these defaults.
func applyProviderCompatibilityDefaults(providerID string, provider config.ProviderConfig, modelIDs ...string) config.ProviderConfig {
	switch {
	case isKimiCodingProviderID(providerID):
		provider.Headers = mergeHeaders(
			map[string]string{"User-Agent": "KimiCLI/1.5"},
			provider.Headers,
		)
		if isAnthropicCompatibleProvider(provider) {
			provider.NPM = "@ai-sdk/anthropic"
		}
		provider = applyModelCompatibilityDefaults(provider, map[string]any{
			"force_adaptive_thinking": true,
			"anthropic_default_betas": false,
		})
		if model, ok := provider.Models["k3"]; ok {
			model.Options = mergeModelOptions(map[string]any{
				"allow_empty_signature": true,
				"thinking_replay":       "full",
			}, model.Options)
			provider.Models["k3"] = model
		}
	case providerID == "minimax" || providerID == "minimax-cn" || providerID == "minimax-coding-plan" || providerID == "minimax-cn-coding-plan":
		provider = applyModelCompatibilityDefaults(provider, map[string]any{
			"anthropic_default_betas": false,
		})
	case providerID == "deepseek":
		if isAnthropicCompatibleProvider(provider) {
			provider.NPM = "@ai-sdk/anthropic"
			if replaceKnownEndpoint(provider.BaseURL, "https://api.deepseek.com", "https://api.deepseek.com/anthropic") {
				provider.API = "https://api.deepseek.com/anthropic"
				provider.BaseURL = provider.API
			}
		} else if usesDeepSeekResponsesModel(provider, modelIDs...) && strings.TrimSpace(provider.WireAPI) == "" {
			provider.WireAPI = "responses"
		}
	case providerID == "zai-coding-plan":
		if isAnthropicCompatibleProvider(provider) {
			provider.NPM = "@ai-sdk/anthropic"
			if replaceKnownEndpoint(provider.BaseURL, "https://api.z.ai/api/coding/paas/v4", "https://api.z.ai/api/anthropic") {
				provider.API = "https://api.z.ai/api/anthropic"
				provider.BaseURL = provider.API
			}
		} else if strings.EqualFold(strings.TrimSpace(provider.WireAPI), "responses") &&
			replaceKnownEndpoint(provider.BaseURL, "https://api.z.ai/api/coding/paas/v4", "https://api.z.ai/api/v1") {
			provider.API = "https://api.z.ai/api/v1"
			provider.BaseURL = provider.API
		}
	case providerID == "xai":
		if strings.TrimSpace(provider.BaseURL) == "" {
			provider.BaseURL = "https://api.x.ai/v1"
		}
		if providerUsesLatestGrokResponsesModel(provider, modelIDs...) && strings.TrimSpace(provider.WireAPI) == "" {
			provider.WireAPI = "responses"
		}
	}
	return provider
}

func isAnthropicCompatibleProvider(provider config.ProviderConfig) bool {
	switch normalizeID(provider.Type) {
	case "anthropic", "anthropic-official", "claude":
		return true
	default:
		return false
	}
}

func replaceKnownEndpoint(current string, known ...string) bool {
	current = strings.TrimRight(strings.ToLower(strings.TrimSpace(current)), "/")
	if current == "" {
		return true
	}
	for _, endpoint := range known {
		if current == strings.TrimRight(strings.ToLower(strings.TrimSpace(endpoint)), "/") {
			return true
		}
	}
	return false
}

func providerUsesModel(provider config.ProviderConfig, target string, modelIDs ...string) bool {
	target = strings.TrimSpace(target)
	for _, modelID := range modelIDs {
		if strings.EqualFold(strings.TrimSpace(modelID), target) {
			return true
		}
	}
	if len(modelIDs) == 0 && strings.EqualFold(strings.TrimSpace(provider.Model), target) {
		return true
	}
	return false
}

func providerUsesLatestGrokResponsesModel(provider config.ProviderConfig, modelIDs ...string) bool {
	return providerUsesModel(provider, "grok-4.6", modelIDs...) || providerUsesModel(provider, "grok-4.7", modelIDs...)
}

func usesDeepSeekResponsesModel(provider config.ProviderConfig, modelIDs ...string) bool {
	ids := modelIDs
	if len(ids) == 0 {
		ids = []string{provider.Model}
	}
	for _, modelID := range ids {
		if isDeepSeekV4Family(modelID) {
			return true
		}
	}
	return false
}

func isDeepSeekV4Family(modelID string) bool {
	id := strings.ToLower(strings.TrimSpace(modelID))
	if idx := strings.LastIndex(id, "/"); idx >= 0 {
		id = id[idx+1:]
	}
	id = strings.TrimPrefix(id, "~")
	return strings.Contains(id, "deepseek-v4") || id == "deepseek-flash" || strings.HasPrefix(id, "deepseek-flash-")
}

func applyModelCompatibilityDefaults(provider config.ProviderConfig, defaults map[string]any) config.ProviderConfig {
	for id, model := range provider.Models {
		model.Options = mergeModelOptions(defaults, model.Options)
		provider.Models[id] = model
	}
	return provider
}

// applyOfficialCatalogCorrections carries model facts that have been published
// by the vendors but have not yet reached every models.dev snapshot. Keep these
// entries narrow and remove them after the upstream catalog contains equivalent
// or newer metadata.
func applyOfficialCatalogCorrections(data *catalogData) {
	if data == nil {
		return
	}
	for i := range data.Providers {
		provider := &data.Providers[i]
		switch normalizeID(provider.ID) {
		case "openai":
			applyOpenAIGPT6AstraCatalog(provider)
		case "deepseek":
			applyDeepSeekOfficialCatalog(provider)
		case kimiForCodingProviderID, kimiCodePlanCNProviderID, kimiCodePlanGlobalProviderID:
			applyKimiCodingOfficialCatalog(provider)
		case "zai", "zai-coding-plan":
			upsertOfficialModel(provider, Model{
				ID:               "glm-5.3",
				Name:             "GLM-5.3",
				Family:           "glm",
				Reasoning:        true,
				ReasoningOptions: officialEffortOptions(false, "low", "high", "max"),
				Attachment:       officialBool(false),
				ToolCall:         officialBool(true),
				StructuredOutput: officialBool(true),
				Temperature:      officialBool(true),
				Interleaved:      map[string]any{"field": "reasoning_content"},
				Modalities:       &Modalities{Input: []string{"text"}, Output: []string{"text"}},
				Limit:            &Limit{Context: 1_000_000, Output: 131_072},
				SupportedEfforts: []string{"low", "high", "max"},
				DefaultVariant:   "max",
			})
		case "xai":
			provider.API = "https://api.x.ai/v1"
			upsertOfficialModel(provider, Model{
				ID:               "grok-4.5",
				Name:             "Grok 4.5",
				Family:           "grok",
				Reasoning:        true,
				ReasoningOptions: officialEffortOptions(false, "low", "medium", "high"),
				Attachment:       officialBool(true),
				ToolCall:         officialBool(true),
				StructuredOutput: officialBool(true),
				Temperature:      officialBool(true),
				Modalities:       &Modalities{Input: []string{"text", "image"}, Output: []string{"text"}},
				Limit:            &Limit{Context: 500_000},
				SupportedEfforts: []string{"low", "medium", "high"},
				DefaultVariant:   "high",
			})
			upsertOfficialModel(provider, Model{
				ID:               "grok-4.6",
				Name:             "Grok 4.6",
				Family:           "grok",
				Reasoning:        true,
				ReasoningOptions: officialEffortOptions(false, "low", "medium", "high", "xhigh"),
				Attachment:       officialBool(true),
				ToolCall:         officialBool(true),
				StructuredOutput: officialBool(true),
				Temperature:      officialBool(true),
				Modalities:       &Modalities{Input: []string{"text", "image"}, Output: []string{"text"}},
				Limit:            &Limit{Context: 500_000},
				SupportedEfforts: []string{"low", "medium", "high", "xhigh"},
				DefaultVariant:   "high",
			})
			upsertOfficialModel(provider, Model{
				ID:               "grok-4.7",
				Name:             "Grok 4.7",
				Family:           "grok",
				Reasoning:        true,
				ReasoningOptions: officialEffortOptions(false, "low", "medium", "high", "xhigh"),
				Attachment:       officialBool(true),
				ToolCall:         officialBool(true),
				StructuredOutput: officialBool(true),
				Temperature:      officialBool(true),
				Modalities:       &Modalities{Input: []string{"text", "image"}, Output: []string{"text"}},
				Limit:            &Limit{Context: 500_000},
				SupportedEfforts: []string{"low", "medium", "high", "xhigh"},
				DefaultVariant:   "high",
			})
		}
	}
}

func applyDeepSeekOfficialCatalog(provider *Provider) {
	if provider == nil {
		return
	}
	flash := Model{
		ID:               "deepseek-flash",
		Name:             "DeepSeek V4.1 Flash",
		Family:           "deepseek-flash",
		ReleaseDate:      "2026-09-10",
		Reasoning:        true,
		ReasoningOptions: officialEffortOptions(true, "low", "high", "max"),
		Attachment:       officialBool(true),
		ToolCall:         officialBool(true),
		StructuredOutput: officialBool(true),
		Temperature:      officialBool(true),
		Interleaved:      map[string]any{"field": "reasoning_content"},
		Modalities:       &Modalities{Input: []string{"text", "image"}, Output: []string{"text"}},
		Limit:            &Limit{Context: 1_000_000, Output: 384_000},
		SupportedEfforts: []string{"none", "low", "high", "max"},
		DefaultVariant:   "high",
	}
	upsertOfficialModel(provider, flash)
	legacyFlash := flash
	legacyFlash.ID = "deepseek-v4-flash"
	legacyFlash.Name = "DeepSeek V4 Flash"
	legacyFlash.APIID = "deepseek-flash"
	upsertOfficialModel(provider, legacyFlash)
	legacyVision := flash
	legacyVision.ID = "deepseek-v4-flash-vision-exp"
	legacyVision.Name = "DeepSeek V4 Flash Vision Exp"
	legacyVision.APIID = "deepseek-flash"
	upsertOfficialModel(provider, legacyVision)
	pro := Model{
		ID:               "deepseek-v4-pro",
		Name:             "DeepSeek V4 Pro",
		Family:           "deepseek-thinking",
		ReleaseDate:      "2026-08-12",
		Reasoning:        true,
		ReasoningOptions: officialEffortOptions(true, "low", "high", "max"),
		Attachment:       officialBool(false),
		ToolCall:         officialBool(true),
		StructuredOutput: officialBool(true),
		Temperature:      officialBool(true),
		Interleaved:      map[string]any{"field": "reasoning_content"},
		Modalities:       &Modalities{Input: []string{"text"}, Output: []string{"text"}},
		Limit:            &Limit{Context: 1_000_000, Output: 384_000},
		SupportedEfforts: []string{"none", "low", "high", "max"},
		DefaultVariant:   "high",
	}
	upsertOfficialModel(provider, pro)
}

func applyKimiCodingOfficialCatalog(provider *Provider) {
	if provider == nil {
		return
	}
	kimiForCoding, ok := modelByID(*provider, "kimi-for-coding")
	if !ok {
		return
	}
	kimiForCoding.Name = "Kimi K2.8"
	kimiForCoding.Family = "kimi-k2"
	if kimiForCoding.Limit == nil || kimiForCoding.Limit.Context == 0 {
		kimiForCoding.Limit = &Limit{Context: 1_048_576, Output: 32_768}
	}
	if kimiForCoding.Modalities == nil {
		kimiForCoding.Modalities = &Modalities{Input: []string{"text", "image", "video"}, Output: []string{"text"}}
	}
	if kimiForCoding.Attachment == nil {
		kimiForCoding.Attachment = officialBool(true)
	}
	if len(kimiForCoding.ReasoningOptions) == 0 {
		kimiForCoding.ReasoningOptions = officialEffortOptions(true, "low", "high", "max")
	}
	kimiForCoding.Reasoning = true
	upsertOfficialModel(provider, kimiForCoding)
	preview := kimiForCoding
	preview.ID = "kimi-k2.8-preview"
	preview.APIID = "kimi-for-coding"
	preview.Name = "Kimi K2.8 Preview"
	upsertOfficialModel(provider, preview)
}

func modelByID(provider Provider, id string) (Model, bool) {
	for _, model := range provider.Models {
		if strings.EqualFold(strings.TrimSpace(model.ID), id) {
			return model, true
		}
	}
	return Model{}, false
}

func applyOpenAIGPT6AstraCatalog(provider *Provider) {
	if provider == nil {
		return
	}
	limit := &Limit{Context: 1_050_000, Input: 922_000, Output: 128_000}
	modalities := &Modalities{Input: []string{"text", "image"}, Output: []string{"text"}}
	efforts := []string{"low", "medium", "high", "xhigh", "max"}
	cost := map[string]any{
		"input":       10,
		"cache_read":  1,
		"cache_write": 12.5,
		"output":      50,
		"tiers": []any{
			map[string]any{
				"cache_read":  2,
				"cache_write": 25,
				"input":       20,
				"output":      75,
				"tier": map[string]any{
					"size": 272000,
					"type": "context",
				},
			},
		},
	}
	fastCost := map[string]any{
		"input":       20,
		"cache_read":  2,
		"cache_write": 25,
		"output":      100,
		"tiers": []any{
			map[string]any{
				"cache_read":  4,
				"cache_write": 50,
				"input":       40,
				"output":      150,
				"tier": map[string]any{
					"size": 272000,
					"type": "context",
				},
			},
		},
	}
	upsertOfficialModel(provider, Model{
		ID:               "gpt-6-astra",
		Name:             "GPT-6 Astra",
		Family:           "gpt-astra",
		ReleaseDate:      "2026-09-03",
		Reasoning:        true,
		ReasoningOptions: officialEffortOptions(false, efforts...),
		Attachment:       officialBool(true),
		ToolCall:         officialBool(true),
		StructuredOutput: officialBool(true),
		Temperature:      officialBool(false),
		Modalities:       modalities,
		Cost:             cost,
		Limit:            limit,
		SupportedEfforts: append([]string(nil), efforts...),
		DefaultVariant:   "low",
	})
	upsertOfficialModel(provider, Model{
		ID:               "gpt-6-astra-fast",
		APIID:            "gpt-6-astra",
		Name:             "GPT-6 Astra Fast",
		Family:           "gpt-astra",
		ReleaseDate:      "2026-09-03",
		Reasoning:        true,
		ReasoningOptions: officialEffortOptions(false, efforts...),
		Attachment:       officialBool(true),
		ToolCall:         officialBool(true),
		StructuredOutput: officialBool(true),
		Temperature:      officialBool(false),
		Modalities:       modalities,
		Cost:             fastCost,
		Limit:            limit,
		Options:          map[string]any{"serviceTier": "priority"},
		SupportedEfforts: append([]string(nil), efforts...),
		DefaultVariant:   "low",
	})
}

func upsertOfficialModel(provider *Provider, correction Model) {
	if provider == nil {
		return
	}
	for i := range provider.Models {
		if !strings.EqualFold(strings.TrimSpace(provider.Models[i].ID), correction.ID) {
			continue
		}
		existing := provider.Models[i]
		// Pricing and transport-specific overrides can remain fresher upstream;
		// the fields above are the official capability correction.
		correction.Cost = existing.Cost
		correction.Provider = existing.Provider
		correction.Options = existing.Options
		correction.Headers = existing.Headers
		if correction.Status == "" {
			correction.Status = existing.Status
		}
		if correction.ReleaseDate == "" {
			correction.ReleaseDate = existing.ReleaseDate
		}
		provider.Models[i] = correction
		return
	}
	provider.Models = append(provider.Models, correction)
}

func officialEffortOptions(toggle bool, efforts ...string) []map[string]any {
	options := make([]map[string]any, 0, 2)
	if toggle {
		options = append(options, map[string]any{"type": "toggle"})
	}
	options = append(options, map[string]any{"type": "effort", "values": append([]string(nil), efforts...)})
	return options
}

func officialBool(value bool) *bool {
	return &value
}
