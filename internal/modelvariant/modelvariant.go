package modelvariant

import (
	"sort"
	"strings"

	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/provideroptions"
)

type Variant struct {
	ID      string
	Options map[string]any
}

type Selection struct {
	Variant         string
	DisplayEffort   string
	LegacyEffort    string
	ProviderOptions map[string]any
}

func Resolve(provider config.ProviderConfig, model, variant, legacyEffort string) Selection {
	return ResolveForProvider("", provider, model, variant, legacyEffort)
}

func ResolveForProvider(providerName string, provider config.ProviderConfig, model, variant, legacyEffort string) Selection {
	variant = strings.TrimSpace(variant)
	legacyEffort = strings.TrimSpace(legacyEffort)
	if strings.EqualFold(variant, "none") || (variant == "" && strings.EqualFold(legacyEffort, "none")) {
		desc := compatDescriptor(providerName, provider, model)
		if isGLM53(desc.APIID) || AnthropicRequiresBoundThinking(desc.APIID) {
			// These models cannot disable reasoning. Migrate the retired switch to
			// the least expensive valid tier rather than falling back to max.
			variant = "low"
			legacyEffort = ""
		}
	}
	baseOptions := BaseOptionsForProvider(providerName, provider, model)

	if variant == "" && legacyEffort != "" {
		if _, ok := OptionsForProvider(providerName, provider, model, legacyEffort); ok {
			variant = legacyEffort
		}
	}
	if variant == "" {
		if candidate := DefaultVariantForProvider(providerName, provider, model); candidate != "" {
			if _, ok := OptionsForProvider(providerName, provider, model, candidate); ok {
				variant = candidate
			}
		}
	}
	if variant != "" {
		if options, ok := OptionsForProvider(providerName, provider, model, variant); ok {
			return Selection{
				Variant:         variant,
				DisplayEffort:   variant,
				ProviderOptions: mergeOptions(baseOptions, options),
			}
		}
	}
	return Selection{
		DisplayEffort:   legacyEffort,
		LegacyEffort:    legacyEffort,
		ProviderOptions: baseOptions,
	}
}

func DefaultVariantForProvider(_ string, provider config.ProviderConfig, model string) string {
	modelCfg, ok := provider.Models[strings.TrimSpace(model)]
	if !ok {
		return ""
	}
	if value := strings.TrimSpace(modelCfg.DefaultVariant); value != "" {
		return value
	}
	return strings.TrimSpace(modelCfg.DefaultEffort)
}

func Options(provider config.ProviderConfig, model, variant string) (map[string]any, bool) {
	return OptionsForProvider("", provider, model, variant)
}

func OptionsForProvider(providerName string, provider config.ProviderConfig, model, variant string) (map[string]any, bool) {
	variant = strings.TrimSpace(variant)
	if variant == "" {
		return nil, false
	}
	for _, summary := range SummariesForProvider(providerName, provider, model) {
		if summary.ID == variant {
			return CloneOptions(summary.Options), true
		}
	}
	return nil, false
}

func Summaries(provider config.ProviderConfig, model string) []Variant {
	return SummariesForProvider("", provider, model)
}

func SummariesForProvider(providerName string, provider config.ProviderConfig, model string) []Variant {
	model = strings.TrimSpace(model)
	if model == "" {
		return nil
	}
	if modelCfg, ok := provider.Models[model]; ok && len(modelCfg.Variants) > 0 {
		return variantsFromOptionMap(modelCfg.Variants)
	}
	return variantsFromOptionMap(inferredOptionsForProvider(providerName, provider, model))
}

func SupportedEffortsForProvider(providerName string, provider config.ProviderConfig, model string, modelCfg config.ProviderModelConfig) []string {
	if values := normalizedEffortList(modelCfg.SupportedEfforts); len(values) > 0 {
		return values
	}
	if values := EffortIDs(SummariesForProvider(providerName, provider, model)); len(values) > 0 {
		return values
	}
	return nil
}

func EffortIDs(variants []Variant) []string {
	if len(variants) == 0 {
		return nil
	}
	out := make([]string, 0, len(variants))
	for _, variant := range variants {
		id := strings.TrimSpace(variant.ID)
		if id == "" || id == "default" {
			continue
		}
		out = append(out, id)
	}
	return out
}

func CloneOptions(options map[string]any) map[string]any {
	return provideroptions.CloneWithoutDisabled(options)
}

func mergeOptions(base, override map[string]any) map[string]any {
	return provideroptions.MergeWithoutDisabled(base, override)
}

func variantsFromOptionMap(variants map[string]map[string]any) []Variant {
	if len(variants) == 0 {
		return nil
	}
	out := make([]Variant, 0, len(variants))
	for id, options := range variants {
		id = strings.TrimSpace(id)
		if id == "" || variantDisabled(options) {
			continue
		}
		out = append(out, Variant{
			ID:      id,
			Options: CloneOptions(options),
		})
	}
	sort.Slice(out, func(i, j int) bool {
		leftRank := variantRank(out[i].ID)
		rightRank := variantRank(out[j].ID)
		if leftRank != rightRank {
			return leftRank < rightRank
		}
		return strings.ToLower(out[i].ID) < strings.ToLower(out[j].ID)
	})
	return out
}

func variantDisabled(options map[string]any) bool {
	if len(options) == 0 {
		return false
	}
	disabled, ok := options["disabled"].(bool)
	return ok && disabled
}

func variantRank(id string) int {
	switch strings.ToLower(strings.TrimSpace(id)) {
	case "none":
		return 0
	case "minimal":
		return 1
	case "low":
		return 2
	case "medium":
		return 3
	case "high":
		return 4
	case "xhigh":
		return 5
	case "max":
		return 6
	default:
		return 100
	}
}

func normalizedEffortList(values []string) []string {
	seen := make(map[string]bool, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		effort := strings.TrimSpace(value)
		if effort == "" || seen[effort] {
			continue
		}
		seen[effort] = true
		out = append(out, effort)
	}
	return out
}

func isCodexProviderType(providerType string) bool {
	providerType = strings.ToLower(strings.TrimSpace(providerType))
	providerType = strings.ReplaceAll(providerType, "_", "-")
	return providerType == "openai-codex" ||
		providerType == "codex-subscription" ||
		providerType == "chatgpt-codex" ||
		providerType == "codex-cli" ||
		providerType == "codex"
}
