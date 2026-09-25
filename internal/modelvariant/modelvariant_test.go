package modelvariant

import (
	"reflect"
	"strings"
	"testing"

	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/modelcatalog"
)

func TestKimiK28PreviewUsesAdaptiveThinkingOnOpenAICompatible(t *testing.T) {
	providerName, provider := modelcatalog.EnrichProvider("kimi-code-plan-cn", config.ProviderConfig{
		Type:  "openai-compatible",
		Model: "kimi-k2.8-preview",
	}, "kimi-k2.8-preview")

	variants := SummariesForProvider(providerName, provider, "kimi-k2.8-preview")
	if got := strings.Join(variantIDs(variants), ","); got != "low,high,max" {
		t.Fatalf("K2.8 variants = %s, want low,high,max", got)
	}
	selection := ResolveForProvider(providerName, provider, "kimi-k2.8-preview", "max", "")
	if selection.ProviderOptions["reasoningEffort"] != "max" || selection.ProviderOptions["force_adaptive_thinking"] != true {
		t.Fatalf("unexpected K2.8 provider options: %+v", selection.ProviderOptions)
	}
	thinking, ok := selection.ProviderOptions["thinking"].(map[string]any)
	if !ok || thinking["type"] != "adaptive" || thinking["display"] != "summarized" {
		t.Fatalf("unexpected K2.8 thinking options: %+v", selection.ProviderOptions)
	}
}

func TestKimiK3UsesUpstreamEffortVariants(t *testing.T) {
	providerName, provider := modelcatalog.EnrichProvider("kimi-for-coding", config.ProviderConfig{
		Type:  "anthropic",
		Model: "k3",
	}, "k3")

	variants := SummariesForProvider(providerName, provider, "k3")
	if got := strings.Join(variantIDs(variants), ","); got != "low,high,max" {
		t.Fatalf("K3 variants = %s, want low,high,max", got)
	}
	selection := ResolveForProvider(providerName, provider, "k3", "max", "")
	if selection.Variant != "max" {
		t.Fatalf("K3 selected variant = %q, want max", selection.Variant)
	}
	if selection.ProviderOptions["effort"] != "max" || selection.ProviderOptions["allow_empty_signature"] != true || selection.ProviderOptions["thinking_replay"] != "full" {
		t.Fatalf("unexpected K3 provider options: %+v", selection.ProviderOptions)
	}
	thinking, ok := selection.ProviderOptions["thinking"].(map[string]any)
	if !ok || thinking["type"] != "adaptive" || thinking["display"] != "summarized" {
		t.Fatalf("unexpected K3 thinking options: %+v", selection.ProviderOptions)
	}
}

func TestKimiProviderDefaultsUseAdaptiveThinking(t *testing.T) {
	providerName, provider := modelcatalog.EnrichProvider("kimi-for-coding", config.ProviderConfig{
		Type:  "anthropic",
		Model: "k3-256k",
	}, "k3-256k")

	variants := SummariesForProvider(providerName, provider, "k3-256k")
	if got := strings.Join(variantIDs(variants), ","); got != "low,high,max" {
		t.Fatalf("Kimi k3-256k variants = %s, want low,high,max", got)
	}
	selection := ResolveForProvider(providerName, provider, "k3-256k", "high", "")
	if selection.ProviderOptions["effort"] != "high" || selection.ProviderOptions["force_adaptive_thinking"] != true {
		t.Fatalf("unexpected Kimi provider options: %+v", selection.ProviderOptions)
	}
	thinking, ok := selection.ProviderOptions["thinking"].(map[string]any)
	if !ok || thinking["type"] != "adaptive" || thinking["display"] != "summarized" {
		t.Fatalf("unexpected Kimi adaptive thinking: %+v", selection.ProviderOptions)
	}
	if _, exists := selection.ProviderOptions["allow_empty_signature"]; exists {
		t.Fatalf("K3-only empty signature option leaked to k3-256k: %+v", selection.ProviderOptions)
	}
}

func TestSummariesInferXiaomiReasoningEfforts(t *testing.T) {
	provider := config.ProviderConfig{
		Type:    "openai-compatible",
		BaseURL: "https://token-plan-cn.xiaomimimo.com/v1",
		Model:   "mimo-v2.5-pro",
	}

	variants := Summaries(provider, provider.Model)
	if got := variantIDs(variants); strings.Join(got, ",") != "low,medium,high" {
		t.Fatalf("variants = %v", got)
	}
	if got := variants[0].Options["reasoningEffort"]; got != "low" {
		t.Fatalf("low options = %#v", variants[0].Options)
	}
}

func TestSummariesInferOpenRouterNestedReasoning(t *testing.T) {
	provider := config.ProviderConfig{
		Type:    "openai-compatible",
		BaseURL: "https://openrouter.ai/api/v1",
		Model:   "openai/gpt-5.5",
	}

	options, ok := Options(provider, provider.Model, "high")
	if !ok {
		t.Fatal("expected high variant")
	}
	reasoning, ok := options["reasoning"].(map[string]any)
	if !ok || reasoning["effort"] != "high" {
		t.Fatalf("expected nested reasoning effort, got %#v", options)
	}
}

func TestSummariesInferOpenRouterNonOpenAIReasoningEfforts(t *testing.T) {
	provider := config.ProviderConfig{
		Type:    "openai-compatible",
		BaseURL: "https://openrouter.ai/api/v1",
		Model:   "anthropic/claude-sonnet-4.6",
	}

	variants := Summaries(provider, provider.Model)
	if got := variantIDs(variants); strings.Join(got, ",") != "low,medium,high" {
		t.Fatalf("variants = %v", got)
	}
	options, ok := Options(provider, provider.Model, "medium")
	if !ok {
		t.Fatal("expected medium variant")
	}
	reasoning, ok := options["reasoning"].(map[string]any)
	if !ok || reasoning["effort"] != "medium" {
		t.Fatalf("expected nested reasoning effort, got %#v", options)
	}
}

func TestSummariesUseExplicitXAIReasoningEfforts(t *testing.T) {
	tests := []struct {
		model string
		want  string
	}{
		{model: "grok-4.3", want: "none,low,medium,high"},
		{model: "grok-4.20-multi-agent-0309", want: "low,medium,high,xhigh"},
	}
	for _, tc := range tests {
		t.Run(tc.model, func(t *testing.T) {
			providerName, provider := modelcatalog.EnrichProvider("xai", config.ProviderConfig{
				Type:  "openai-compatible",
				Model: tc.model,
			}, tc.model)
			variants := SummariesForProvider(providerName, provider, tc.model)
			if got := strings.Join(variantIDs(variants), ","); got != tc.want {
				t.Fatalf("variants = %q, want %q: %+v", got, tc.want, variants)
			}
		})
	}
}

func TestGrok46FallbackReasoningEfforts(t *testing.T) {
	reasoning := true
	provider := config.ProviderConfig{
		Type:  "openai-compatible",
		NPM:   "@ai-sdk/xai",
		Model: "grok-4.6",
		Models: map[string]config.ProviderModelConfig{
			"grok-4.6": {Reasoning: &reasoning},
		},
	}

	variants := SummariesForProvider("xai", provider, "grok-4.6")
	if got := strings.Join(variantIDs(variants), ","); got != "low,medium,high,xhigh" {
		t.Fatalf("variants = %q, want low,medium,high,xhigh", got)
	}
}

func TestGrok47FallbackReasoningEfforts(t *testing.T) {
	reasoning := true
	provider := config.ProviderConfig{
		Type:  "openai-compatible",
		NPM:   "@ai-sdk/xai",
		Model: "grok-4.7",
		Models: map[string]config.ProviderModelConfig{
			"grok-4.7": {Reasoning: &reasoning},
		},
	}

	variants := SummariesForProvider("xai", provider, "grok-4.7")
	if got := strings.Join(variantIDs(variants), ","); got != "low,medium,high,xhigh" {
		t.Fatalf("variants = %q, want low,medium,high,xhigh", got)
	}
}

func TestSummariesMatchProviderCompatForGenericOpenAICompatible(t *testing.T) {
	provider := config.ProviderConfig{
		Type:  "openai-compatible",
		Model: "gpt-5.5",
	}

	variants := Summaries(provider, provider.Model)
	if got := variantIDs(variants); strings.Join(got, ",") != "low,medium,high" {
		t.Fatalf("variants = %v", got)
	}
}

func TestSummariesMatchProviderCompatForOpenAIProvider(t *testing.T) {
	provider := config.ProviderConfig{
		Type:  "openai",
		Model: "gpt-5.5",
	}

	options, ok := Options(provider, provider.Model, "xhigh")
	if !ok {
		t.Fatal("expected xhigh variant")
	}
	if got := options["reasoningEffort"]; got != "xhigh" {
		t.Fatalf("reasoningEffort = %#v", got)
	}
	if got := options["reasoningSummary"]; got != "auto" {
		t.Fatalf("reasoningSummary = %#v", got)
	}
	include, ok := options["include"].([]any)
	if !ok || len(include) != 1 || include[0] != "reasoning.encrypted_content" {
		t.Fatalf("include = %#v", options["include"])
	}
}

func TestSummariesIncludeMaxForGPT56OpenAIFallback(t *testing.T) {
	for _, model := range []string{"gpt-5.6", "gpt-5.6-sol", "gpt-5.6-terra", "gpt-5.6-luna", "gpt-6-sol", "gpt-6-luna"} {
		t.Run(model, func(t *testing.T) {
			provider := config.ProviderConfig{Type: "openai", Model: model}
			variants := Summaries(provider, model)
			if got := strings.Join(variantIDs(variants), ","); got != "none,low,medium,high,xhigh,max" {
				t.Fatalf("variants = %q", got)
			}
			options, ok := Options(provider, model, "max")
			if !ok || options["reasoningEffort"] != "max" {
				t.Fatalf("max options = %#v, ok=%v", options, ok)
			}
		})
	}
}

func TestSummariesIncludeAstraOpenAIFallback(t *testing.T) {
	provider := config.ProviderConfig{Type: "openai", Model: "gpt-6-astra"}
	variants := Summaries(provider, provider.Model)
	if got := strings.Join(variantIDs(variants), ","); got != "low,medium,high,xhigh,max" {
		t.Fatalf("variants = %q", got)
	}
	options, ok := Options(provider, provider.Model, "low")
	if !ok || options["reasoningEffort"] != "low" {
		t.Fatalf("low options = %#v, ok=%v", options, ok)
	}
}

func TestSummariesUseDeclaredGPT56OpenAIEfforts(t *testing.T) {
	reasoning := true
	provider := config.ProviderConfig{
		Type:  "openai",
		Model: "gpt-5.6-sol",
		Models: map[string]config.ProviderModelConfig{
			"gpt-5.6-sol": {
				Reasoning:        &reasoning,
				SupportedEfforts: []string{"low", "medium", "high", "xhigh", "max", "ultra"},
			},
		},
	}

	variants := Summaries(provider, provider.Model)
	if got := strings.Join(variantIDs(variants), ","); got != "low,medium,high,xhigh,max,ultra" {
		t.Fatalf("variants = %q", got)
	}
}

func TestSummariesUseCatalogEffortsForGPT56CompatibleGateway(t *testing.T) {
	reasoning := true
	provider := config.ProviderConfig{
		Type:  "openai-compatible",
		Model: "gpt-5.6-sol",
		Models: map[string]config.ProviderModelConfig{
			"gpt-5.6-sol": {
				Reasoning: &reasoning,
				ReasoningOptions: []map[string]any{{
					"type":   "effort",
					"values": []any{"none", "low", "medium", "high", "xhigh", "max"},
				}},
			},
		},
	}

	variants := SummariesForProvider("gateway", provider, provider.Model)
	if got := strings.Join(variantIDs(variants), ","); got != "none,low,medium,high,xhigh,max" {
		t.Fatalf("variants = %q", got)
	}
	options, ok := OptionsForProvider("gateway", provider, provider.Model, "max")
	if !ok || options["reasoningEffort"] != "max" {
		t.Fatalf("max options = %#v, ok=%v", options, ok)
	}
}

func TestSummariesMatchProviderCompatForBedrockMantle(t *testing.T) {
	reasoning := true
	provider := config.ProviderConfig{
		Type:  "openai-compatible",
		NPM:   "@ai-sdk/amazon-bedrock/mantle",
		Model: "openai.gpt-5.5",
		Models: map[string]config.ProviderModelConfig{
			"openai.gpt-5.5": {
				Reasoning: &reasoning,
			},
		},
	}

	selection := Resolve(provider, provider.Model, "high", "")
	if got := selection.ProviderOptions["store"]; got != false {
		t.Fatalf("store = %#v", got)
	}
	if got := selection.ProviderOptions["reasoningSummary"]; got != "auto" {
		t.Fatalf("reasoningSummary = %#v", got)
	}
	include, ok := selection.ProviderOptions["include"].([]any)
	if !ok || len(include) != 1 || include[0] != "reasoning.encrypted_content" {
		t.Fatalf("include = %#v", selection.ProviderOptions["include"])
	}
}

func TestSummariesMatchProviderCompatForExcludedReasoningModels(t *testing.T) {
	reasoning := true
	for _, tc := range []struct {
		name     string
		provider config.ProviderConfig
	}{
		{
			name: "deepseek",
			provider: config.ProviderConfig{
				Type:  "openai-compatible",
				NPM:   "@ai-sdk/openai-compatible",
				Model: "deepseek-chat",
				Models: map[string]config.ProviderModelConfig{
					"deepseek-chat": {Reasoning: &reasoning},
				},
			},
		},
		{
			name: "minimax",
			provider: config.ProviderConfig{
				Type:  "openai-compatible",
				NPM:   "@ai-sdk/openai-compatible",
				Model: "minimax-model",
				Models: map[string]config.ProviderModelConfig{
					"minimax-model": {Reasoning: &reasoning},
				},
			},
		},
		{
			name: "glm anthropic",
			provider: config.ProviderConfig{
				Type:  "anthropic",
				Model: "glm-5.1",
				Models: map[string]config.ProviderModelConfig{
					"glm-5.1": {Reasoning: &reasoning},
				},
			},
		},
		{
			name: "unversioned gpt chat",
			provider: config.ProviderConfig{
				Type:    "openai-compatible",
				BaseURL: "https://openrouter.ai/api/v1",
				Model:   "openai/gpt-5-chat",
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if variants := Summaries(tc.provider, tc.provider.Model); len(variants) != 0 {
				t.Fatalf("variants = %+v", variants)
			}
		})
	}
}

func TestSummariesMatchProviderCompatForMiniMaxM3(t *testing.T) {
	reasoning := true
	for _, tc := range []struct {
		name             string
		npm              string
		wantBaseThinking bool
	}{
		{name: "anthropic", npm: "@ai-sdk/anthropic", wantBaseThinking: true},
		{name: "openai compatible", npm: "@ai-sdk/openai-compatible"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			provider := config.ProviderConfig{
				Type:  "anthropic",
				NPM:   tc.npm,
				Model: "minimax-m3",
				Models: map[string]config.ProviderModelConfig{
					"minimax-m3": {
						Reasoning: &reasoning,
					},
				},
			}

			variants := Summaries(provider, provider.Model)
			if got := variantIDs(variants); strings.Join(got, ",") != "none,thinking" {
				t.Fatalf("variants = %v", got)
			}
			options, ok := Options(provider, provider.Model, "thinking")
			if !ok {
				t.Fatal("expected thinking variant")
			}
			thinking, ok := options["thinking"].(map[string]any)
			if !ok || thinking["type"] != "adaptive" {
				t.Fatalf("thinking options = %#v", options)
			}

			selection := Resolve(provider, provider.Model, "", "")
			thinking, ok = selection.ProviderOptions["thinking"].(map[string]any)
			if tc.wantBaseThinking {
				if !ok || thinking["type"] != "adaptive" {
					t.Fatalf("base thinking options = %#v", selection.ProviderOptions)
				}
				if _, ok := selection.ProviderOptions["anthropicToolSearch"]; ok {
					t.Fatalf("base thinking should not inject anthropic tool_search opt-in: %#v", selection.ProviderOptions)
				}
			} else if ok {
				t.Fatalf("openai-compatible should use native default thinking: %#v", selection.ProviderOptions)
			} else if _, ok := selection.ProviderOptions["anthropicToolSearch"]; ok {
				t.Fatalf("openai-compatible should not enable anthropic tool_search: %#v", selection.ProviderOptions)
			}
		})
	}
}

func TestResolveKeepsExplicitMiniMaxThinkingOption(t *testing.T) {
	reasoning := true
	provider := config.ProviderConfig{
		Type:  "anthropic",
		NPM:   "@ai-sdk/anthropic",
		Model: "minimax-m3",
		Models: map[string]config.ProviderModelConfig{
			"minimax-m3": {
				Reasoning: &reasoning,
				Options:   map[string]any{"thinking": map[string]any{"type": "disabled"}},
			},
		},
	}

	selection := Resolve(provider, provider.Model, "", "")
	thinking, ok := selection.ProviderOptions["thinking"].(map[string]any)
	if !ok || thinking["type"] != "disabled" {
		t.Fatalf("explicit thinking option was overwritten: %#v", selection.ProviderOptions)
	}
}

func TestResolveMatchesProviderCompatForZAIAndZhipuThinking(t *testing.T) {
	for _, providerID := range []string{"zai-coding-plan", "zai", "zhipuai-coding-plan", "zhipuai"} {
		t.Run(providerID, func(t *testing.T) {
			provider := config.ProviderConfig{
				Type:  "openai-compatible",
				NPM:   "@ai-sdk/openai-compatible",
				Model: "glm-4.6",
			}

			selection := ResolveForProvider(providerID, provider, provider.Model, "", "")
			thinking, ok := selection.ProviderOptions["thinking"].(map[string]any)
			if !ok {
				t.Fatalf("thinking = %#v; options=%#v", selection.ProviderOptions["thinking"], selection.ProviderOptions)
			}
			if thinking["type"] != "enabled" || thinking["clear_thinking"] != false {
				t.Fatalf("thinking = %#v", thinking)
			}

			provider.Models = map[string]config.ProviderModelConfig{
				provider.Model: {Options: map[string]any{"thinking": map[string]any{"type": "disabled"}}},
			}
			selection = ResolveForProvider(providerID, provider, provider.Model, "", "")
			thinking, ok = selection.ProviderOptions["thinking"].(map[string]any)
			if !ok || thinking["type"] != "disabled" {
				t.Fatalf("explicit thinking option was overwritten: %#v", selection.ProviderOptions)
			}
		})
	}
}

func TestResolveDeepSeekV4EffortEnablesThinking(t *testing.T) {
	providerName, provider := modelcatalog.EnrichProvider("deepseek", config.ProviderConfig{
		Type:  "openai-compatible",
		Model: "deepseek-v4-pro",
	}, "deepseek-v4-pro")

	selection := ResolveForProvider(providerName, provider, "deepseek-v4-pro", "high", "")
	if got := selection.ProviderOptions["reasoningEffort"]; got != "high" {
		t.Fatalf("reasoningEffort = %#v", got)
	}
	thinking, ok := selection.ProviderOptions["thinking"].(map[string]any)
	if !ok || thinking["type"] != "enabled" {
		t.Fatalf("thinking = %#v; options=%#v", selection.ProviderOptions["thinking"], selection.ProviderOptions)
	}
}

func TestDeepSeekV4UsesVendorEffortTiers(t *testing.T) {
	for _, model := range []string{"deepseek-flash", "deepseek-v4-flash", "deepseek-v4-pro", "deepseek-v4-flash-vision-exp"} {
		t.Run(model, func(t *testing.T) {
			providerName, provider := modelcatalog.EnrichProvider("deepseek", config.ProviderConfig{
				Type:  "openai-compatible",
				Model: model,
			}, model)

			want := "none,low,high,max"
			variants := SummariesForProvider(providerName, provider, model)
			if got := strings.Join(variantIDs(variants), ","); got != want {
				t.Fatalf("variants = %q, want %s", got, want)
			}

			selection := ResolveForProvider(providerName, provider, model, "max", "")
			thinking, ok := selection.ProviderOptions["thinking"].(map[string]any)
			if !ok || thinking["type"] != "enabled" {
				t.Fatalf("max thinking = %#v; options=%#v", selection.ProviderOptions["thinking"], selection.ProviderOptions)
			}
			if got := selection.ProviderOptions["reasoningEffort"]; got != "max" {
				t.Fatalf("max reasoningEffort = %#v", got)
			}

			low := ResolveForProvider(providerName, provider, model, "low", "")
			if low.Variant != "low" || low.ProviderOptions["reasoningEffort"] != "low" {
				t.Fatalf("saved low effort must remain low, got %#v", low)
			}

			off := ResolveForProvider(providerName, provider, model, "none", "")
			thinking, ok = off.ProviderOptions["thinking"].(map[string]any)
			if !ok || thinking["type"] != "disabled" {
				t.Fatalf("none thinking = %#v; options=%#v", off.ProviderOptions["thinking"], off.ProviderOptions)
			}
		})
	}
}

func TestGLM52UsesVendorEffortTiers(t *testing.T) {
	providerName, provider := modelcatalog.EnrichProvider("zai", config.ProviderConfig{
		Type:  "openai-compatible",
		Model: "glm-5.2",
	}, "glm-5.2")

	variants := SummariesForProvider(providerName, provider, "glm-5.2")
	if got := strings.Join(variantIDs(variants), ","); got != "none,high,max" {
		t.Fatalf("variants = %q, want none,high,max", got)
	}

	selection := ResolveForProvider(providerName, provider, "glm-5.2", "max", "")
	if got := selection.ProviderOptions["reasoningEffort"]; got != "max" {
		t.Fatalf("max reasoningEffort = %#v", got)
	}
	thinking, ok := selection.ProviderOptions["thinking"].(map[string]any)
	if !ok || thinking["type"] != "enabled" {
		t.Fatalf("max thinking = %#v; options=%#v", selection.ProviderOptions["thinking"], selection.ProviderOptions)
	}

	off := ResolveForProvider(providerName, provider, "glm-5.2", "none", "")
	thinking, ok = off.ProviderOptions["thinking"].(map[string]any)
	if !ok || thinking["type"] != "disabled" {
		t.Fatalf("none thinking = %#v; options=%#v", off.ProviderOptions["thinking"], off.ProviderOptions)
	}
}

func TestGLM53UsesAlwaysOnVendorEffortTiers(t *testing.T) {
	reasoning := true
	provider := config.ProviderConfig{
		Type:  "openai-compatible",
		NPM:   "@ai-sdk/openai-compatible",
		Model: "glm-5.3",
		Models: map[string]config.ProviderModelConfig{
			"glm-5.3": {Reasoning: &reasoning},
		},
	}

	variants := SummariesForProvider("zai", provider, "glm-5.3")
	if got := strings.Join(variantIDs(variants), ","); got != "low,high,max" {
		t.Fatalf("variants = %q, want low,high,max", got)
	}

	migrated := ResolveForProvider("zai", provider, "glm-5.3", "none", "")
	if migrated.Variant != "low" || migrated.ProviderOptions["reasoningEffort"] != "low" {
		t.Fatalf("legacy none was not migrated to low: %#v", migrated)
	}
}

func TestResolveMatchesProviderCompatForAlibabaReasoning(t *testing.T) {
	reasoning := true
	provider := config.ProviderConfig{
		Type:  "openai-compatible",
		NPM:   "@ai-sdk/openai-compatible",
		Model: "qwen-plus",
		Models: map[string]config.ProviderModelConfig{
			"qwen-plus": {Reasoning: &reasoning},
		},
	}

	for _, providerID := range []string{
		"alibaba", "alibaba-cn", "alibaba-coding-plan", "alibaba-coding-plan-cn", "alibaba-token-plan", "alibaba-token-plan-cn",
	} {
		selection := ResolveForProvider(providerID, provider, provider.Model, "", "")
		if got := selection.ProviderOptions["enable_thinking"]; got != true {
			t.Fatalf("%s enable_thinking = %#v; options=%#v", providerID, got, selection.ProviderOptions)
		}
	}

	provider.Model = "kimi-k2-thinking"
	provider.Models = map[string]config.ProviderModelConfig{
		"kimi-k2-thinking": {Reasoning: &reasoning},
	}
	selection := ResolveForProvider("alibaba-cn", provider, provider.Model, "", "")
	if _, ok := selection.ProviderOptions["enable_thinking"]; ok {
		t.Fatalf("kimi-k2-thinking should use provider default thinking: %#v", selection.ProviderOptions)
	}

	provider.Model = "qwen-plus"
	provider.Models = map[string]config.ProviderModelConfig{
		"qwen-plus": {Reasoning: &reasoning, Options: map[string]any{"enable_thinking": false}},
	}
	selection = ResolveForProvider("alibaba", provider, provider.Model, "", "")
	if got := selection.ProviderOptions["enable_thinking"]; got != false {
		t.Fatalf("explicit enable_thinking = %#v; options=%#v", got, selection.ProviderOptions)
	}
}

func TestResolveMatchesProviderCompatForGoogleThinkingConfigGating(t *testing.T) {
	for _, tc := range []struct {
		name      string
		npm       string
		reasoning bool
		want      map[string]any
	}{
		{name: "google without reasoning", npm: "@ai-sdk/google"},
		{
			name:      "google with reasoning",
			npm:       "@ai-sdk/google",
			reasoning: true,
			want:      map[string]any{"includeThoughts": true},
		},
		{name: "vertex without reasoning", npm: "@ai-sdk/google-vertex"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reasoning := tc.reasoning
			provider := config.ProviderConfig{
				Type:  "openai-compatible",
				NPM:   tc.npm,
				Model: "gemini-2.0-flash",
				Models: map[string]config.ProviderModelConfig{
					"gemini-2.0-flash": {Reasoning: &reasoning},
				},
			}

			selection := Resolve(provider, provider.Model, "", "")
			thinking, ok := selection.ProviderOptions["thinkingConfig"].(map[string]any)
			if tc.want == nil {
				if ok {
					t.Fatalf("thinkingConfig should be unset: %#v", selection.ProviderOptions)
				}
				return
			}
			if !ok || !optionEqual(thinking, tc.want) {
				t.Fatalf("thinkingConfig = %#v, want %#v", thinking, tc.want)
			}
		})
	}
}

func TestSummariesMatchProviderCompatForMistralReasoningWhitelist(t *testing.T) {
	reasoning := true
	tests := []struct {
		model string
		want  string
	}{
		{model: "mistral-small-latest", want: "high"},
		{model: "mistral-medium-3.5", want: "high"},
		{model: "mistral-large-latest", want: ""},
	}

	for _, tc := range tests {
		t.Run(tc.model, func(t *testing.T) {
			provider := config.ProviderConfig{
				Type:  "openai-compatible",
				NPM:   "@ai-sdk/mistral",
				Model: tc.model,
				Models: map[string]config.ProviderModelConfig{
					tc.model: {Reasoning: &reasoning},
				},
			}

			variants := SummariesForProvider("mistral", provider, provider.Model)
			if got := strings.Join(variantIDs(variants), ","); got != tc.want {
				t.Fatalf("variants = %q, want %q: %+v", got, tc.want, variants)
			}
			if tc.want != "" {
				options, ok := OptionsForProvider("mistral", provider, provider.Model, tc.want)
				if !ok || options["reasoningEffort"] != tc.want {
					t.Fatalf("options = %#v, ok=%v", options, ok)
				}
			}
		})
	}
}

func TestResolveMatchesProviderCompatSamplingDefaults(t *testing.T) {
	tests := []struct {
		name        string
		provider    config.ProviderConfig
		providerID  string
		wantTemp    float64
		wantTopP    any
		wantTopK    any
		wantOptions map[string]any
	}{
		{
			name: "minimax m2.1",
			provider: config.ProviderConfig{
				Type:  "openai-compatible",
				Model: "minimax-m2.1",
			},
			wantTemp: 1.0,
			wantTopP: 0.95,
			wantTopK: 40,
		},
		{
			name: "minimax m2",
			provider: config.ProviderConfig{
				Type:  "openai-compatible",
				Model: "minimax-m2",
			},
			wantTemp: 1.0,
			wantTopP: 0.95,
			wantTopK: 20,
		},
		{
			name: "kimi k2 base",
			provider: config.ProviderConfig{
				Type:  "openai-compatible",
				Model: "kimi-k2-0905-preview",
			},
			wantTemp: 0.6,
		},
		{
			name: "kimi k2.5",
			provider: config.ProviderConfig{
				Type:  "openai-compatible",
				Model: "kimi-k2.5",
			},
			wantTemp: 1.0,
			wantTopP: 0.95,
		},
		{
			name:       "opencode kimi thinking",
			providerID: "opencode",
			provider: config.ProviderConfig{
				Type:  "openai-compatible",
				Model: "kimi-k2-thinking",
			},
			wantTemp: 1.0,
			wantOptions: map[string]any{
				"chat_template_args": map[string]any{"enable_thinking": true},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			selection := ResolveForProvider(tc.providerID, tc.provider, tc.provider.Model, "", "")
			if got := selection.ProviderOptions["temperature"]; got != tc.wantTemp {
				t.Fatalf("temperature = %#v, want %#v; options=%#v", got, tc.wantTemp, selection.ProviderOptions)
			}
			if tc.wantTopP == nil {
				if _, ok := selection.ProviderOptions["topP"]; ok {
					t.Fatalf("topP should be unset: %#v", selection.ProviderOptions)
				}
			} else if got := selection.ProviderOptions["topP"]; got != tc.wantTopP {
				t.Fatalf("topP = %#v, want %#v; options=%#v", got, tc.wantTopP, selection.ProviderOptions)
			}
			if tc.wantTopK == nil {
				if _, ok := selection.ProviderOptions["topK"]; ok {
					t.Fatalf("topK should be unset: %#v", selection.ProviderOptions)
				}
			} else if got := selection.ProviderOptions["topK"]; got != tc.wantTopK {
				t.Fatalf("topK = %#v, want %#v; options=%#v", got, tc.wantTopK, selection.ProviderOptions)
			}
			for key, want := range tc.wantOptions {
				if got := selection.ProviderOptions[key]; !optionEqual(got, want) {
					t.Fatalf("%s = %#v, want %#v; options=%#v", key, got, want, selection.ProviderOptions)
				}
			}
		})
	}
}

func TestResolveDoesNotOverwriteConfiguredSamplingOptions(t *testing.T) {
	provider := config.ProviderConfig{
		Type:  "openai-compatible",
		Model: "minimax-m2.1",
		Models: map[string]config.ProviderModelConfig{
			"minimax-m2.1": {
				Options: map[string]any{
					"temperature": 0.7,
					"topP":        0.8,
					"topK":        7,
				},
			},
		},
	}

	selection := Resolve(provider, provider.Model, "", "")
	if got := selection.ProviderOptions["temperature"]; got != 0.7 {
		t.Fatalf("temperature = %#v; options=%#v", got, selection.ProviderOptions)
	}
	if got := selection.ProviderOptions["topP"]; got != 0.8 {
		t.Fatalf("topP = %#v; options=%#v", got, selection.ProviderOptions)
	}
	if got := selection.ProviderOptions["topK"]; got != 7 {
		t.Fatalf("topK = %#v; options=%#v", got, selection.ProviderOptions)
	}
}

func TestSummariesMatchProviderCompatForAnthropicOpus47Aliases(t *testing.T) {
	provider := config.ProviderConfig{
		Type:  "anthropic",
		Model: "claude-4.7-opus",
	}

	variants := Summaries(provider, provider.Model)
	if got := variantIDs(variants); strings.Join(got, ",") != "low,medium,high,xhigh,max" {
		t.Fatalf("variants = %v", got)
	}
	options, ok := Options(provider, provider.Model, "xhigh")
	if !ok {
		t.Fatal("expected xhigh variant")
	}
	thinking, ok := options["thinking"].(map[string]any)
	if !ok || thinking["type"] != "adaptive" || thinking["display"] != "summarized" {
		t.Fatalf("thinking options = %#v", options)
	}
}

func TestSummariesMatchProviderCompatForAnthropicAdaptiveFamilies(t *testing.T) {
	tests := []struct {
		name         string
		apiID        string
		wantVariants string
		wantOptions  map[string]any
	}{
		{
			name:         "opus 4.5 hyphen",
			apiID:        "claude-opus-4-5-20251101",
			wantVariants: "low,medium,high",
			wantOptions:  map[string]any{"effort": "high"},
		},
		{
			name:         "opus 4.5 dot",
			apiID:        "claude-opus-4.5-20251101",
			wantVariants: "low,medium,high",
			wantOptions:  map[string]any{"effort": "high"},
		},
		{
			name:         "sonnet 4.6 hyphen",
			apiID:        "claude-sonnet-4-6",
			wantVariants: "low,medium,high,max",
			wantOptions: map[string]any{
				"thinking": map[string]any{"type": "adaptive"},
				"effort":   "high",
			},
		},
		{
			name:         "sonnet 4.6 dot",
			apiID:        "claude-sonnet-4.6",
			wantVariants: "low,medium,high,max",
			wantOptions: map[string]any{
				"thinking": map[string]any{"type": "adaptive"},
				"effort":   "high",
			},
		},
		{
			name:         "opus 4.7 hyphen",
			apiID:        "claude-opus-4-7",
			wantVariants: "low,medium,high,xhigh,max",
			wantOptions: map[string]any{
				"thinking": map[string]any{"type": "adaptive", "display": "summarized"},
				"effort":   "high",
			},
		},
		{
			name:         "opus 4.7 dot",
			apiID:        "claude-opus-4.7",
			wantVariants: "low,medium,high,xhigh,max",
			wantOptions: map[string]any{
				"thinking": map[string]any{"type": "adaptive", "display": "summarized"},
				"effort":   "high",
			},
		},
		{
			name:         "opus 4.8 hyphen",
			apiID:        "claude-opus-4-8",
			wantVariants: "low,medium,high,xhigh,max",
			wantOptions: map[string]any{
				"thinking": map[string]any{"type": "adaptive", "display": "summarized"},
				"effort":   "high",
			},
		},
		{
			name:         "opus 4.8 dot",
			apiID:        "claude-opus-4.8",
			wantVariants: "low,medium,high,xhigh,max",
			wantOptions: map[string]any{
				"thinking": map[string]any{"type": "adaptive", "display": "summarized"},
				"effort":   "high",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			provider := config.ProviderConfig{
				Type:  "anthropic",
				NPM:   "@ai-sdk/anthropic",
				Model: tc.apiID,
			}

			variants := SummariesForProvider("anthropic", provider, provider.Model)
			if got := strings.Join(variantIDs(variants), ","); got != tc.wantVariants {
				t.Fatalf("variants = %q, want %q: %+v", got, tc.wantVariants, variants)
			}
			options, ok := OptionsForProvider("anthropic", provider, provider.Model, "high")
			if !ok || !optionEqual(options, tc.wantOptions) {
				t.Fatalf("options = %#v, ok=%v", options, ok)
			}
		})
	}
}

func TestSummariesMatchProviderCompatForGatewayAnthropicAdaptive(t *testing.T) {
	provider := config.ProviderConfig{
		Type:  "openai-compatible",
		NPM:   "@ai-sdk/gateway",
		Model: "anthropic/claude-opus-4-8",
	}

	variants := SummariesForProvider("gateway", provider, provider.Model)
	if got := strings.Join(variantIDs(variants), ","); got != "low,medium,high,xhigh,max" {
		t.Fatalf("variants = %q: %+v", got, variants)
	}
	options, ok := OptionsForProvider("gateway", provider, provider.Model, "high")
	want := map[string]any{
		"thinking": map[string]any{"type": "adaptive", "display": "summarized"},
		"effort":   "high",
	}
	if !ok || !optionEqual(options, want) {
		t.Fatalf("options = %#v, ok=%v", options, ok)
	}
}

func TestSummariesMatchProviderCompatForGitHubCopilotAnthropicOpus47(t *testing.T) {
	provider := config.ProviderConfig{
		Type:  "anthropic",
		NPM:   "@ai-sdk/anthropic",
		Model: "claude-opus-4.7",
	}

	variants := SummariesForProvider("github-copilot", provider, provider.Model)
	if got := strings.Join(variantIDs(variants), ","); got != "medium" {
		t.Fatalf("variants = %q: %+v", got, variants)
	}
	options, ok := OptionsForProvider("github-copilot", provider, provider.Model, "medium")
	want := map[string]any{
		"thinking": map[string]any{"type": "adaptive", "display": "summarized"},
		"effort":   "medium",
	}
	if !ok || !optionEqual(options, want) {
		t.Fatalf("options = %#v, ok=%v", options, ok)
	}
}

func TestSummariesMatchProviderCompatForVertexAnthropicOpus48(t *testing.T) {
	provider := config.ProviderConfig{
		Type:  "anthropic",
		NPM:   "@ai-sdk/google-vertex/anthropic",
		Model: "claude-opus-4-8@default",
	}

	variants := SummariesForProvider("google-vertex-anthropic", provider, provider.Model)
	if got := strings.Join(variantIDs(variants), ","); got != "low,medium,high,xhigh,max" {
		t.Fatalf("variants = %q: %+v", got, variants)
	}
	options, ok := OptionsForProvider("google-vertex-anthropic", provider, provider.Model, "high")
	want := map[string]any{
		"thinking": map[string]any{"type": "adaptive", "display": "summarized"},
		"effort":   "high",
	}
	if !ok || !optionEqual(options, want) {
		t.Fatalf("options = %#v, ok=%v", options, ok)
	}
}

func TestSummariesMatchProviderCompatForBedrockAnthropicAdaptive(t *testing.T) {
	tests := []struct {
		model        string
		wantVariant  string
		wantVariants string
		wantOptions  map[string]any
	}{
		{
			model:        "anthropic.claude-sonnet-4-6",
			wantVariant:  "max",
			wantVariants: "low,medium,high,max",
			wantOptions: map[string]any{
				"reasoningConfig": map[string]any{
					"type":               "adaptive",
					"maxReasoningEffort": "max",
				},
			},
		},
		{
			model:        "anthropic.claude-opus-4-7",
			wantVariant:  "xhigh",
			wantVariants: "low,medium,high,xhigh,max",
			wantOptions: map[string]any{
				"reasoningConfig": map[string]any{
					"type":               "adaptive",
					"maxReasoningEffort": "xhigh",
					"display":            "summarized",
				},
			},
		},
		{
			model:        "anthropic.claude-opus-4.8",
			wantVariant:  "high",
			wantVariants: "low,medium,high,xhigh,max",
			wantOptions: map[string]any{
				"reasoningConfig": map[string]any{
					"type":               "adaptive",
					"maxReasoningEffort": "high",
					"display":            "summarized",
				},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.model, func(t *testing.T) {
			provider := config.ProviderConfig{
				Type:  "openai-compatible",
				NPM:   "@ai-sdk/amazon-bedrock",
				Model: tc.model,
			}

			variants := SummariesForProvider("bedrock", provider, provider.Model)
			if got := strings.Join(variantIDs(variants), ","); got != tc.wantVariants {
				t.Fatalf("variants = %q, want %q: %+v", got, tc.wantVariants, variants)
			}
			options, ok := OptionsForProvider("bedrock", provider, provider.Model, tc.wantVariant)
			if !ok || !optionEqual(options, tc.wantOptions) {
				t.Fatalf("options = %#v, ok=%v", options, ok)
			}
		})
	}
}

func TestSummariesUseProviderCompatModelMetadata(t *testing.T) {
	reasoning := true
	provider := config.ProviderConfig{
		Type:  "openai-compatible",
		NPM:   "@ai-sdk/google",
		Model: "gemini-3-flash",
		Models: map[string]config.ProviderModelConfig{
			"gemini-3-flash": {
				Reasoning: &reasoning,
				Provider:  &config.ProviderModelProviderConfig{NPM: "@ai-sdk/google"},
			},
		},
	}

	variants := Summaries(provider, provider.Model)
	if got := variantIDs(variants); strings.Join(got, ",") != "minimal,low,medium,high" {
		t.Fatalf("variants = %v", got)
	}
	options, ok := Options(provider, provider.Model, "minimal")
	if !ok {
		t.Fatal("expected minimal variant")
	}
	thinking, ok := options["thinkingConfig"].(map[string]any)
	if !ok || thinking["thinkingLevel"] != "minimal" || thinking["includeThoughts"] != true {
		t.Fatalf("thinkingConfig = %#v", options)
	}
}

func TestSummariesWrapSAPModelParams(t *testing.T) {
	reasoning := true
	provider := config.ProviderConfig{
		Type:  "openai-compatible",
		NPM:   "@jerome-benoit/sap-ai-provider-v2",
		Model: "gpt-5.5",
		Models: map[string]config.ProviderModelConfig{
			"gpt-5.5": {
				Reasoning:   &reasoning,
				ReleaseDate: "2026-04-23",
			},
		},
	}

	options, ok := Options(provider, provider.Model, "xhigh")
	if !ok {
		t.Fatal("expected xhigh variant")
	}
	params, ok := options["modelParams"].(map[string]any)
	if !ok || params["reasoning_effort"] != "xhigh" {
		t.Fatalf("modelParams = %#v", options)
	}
}

func TestResolveUsesVariantOptionsInsteadOfLegacyEffort(t *testing.T) {
	provider := config.ProviderConfig{
		Type:    "openai-compatible",
		BaseURL: "https://token-plan-cn.xiaomimimo.com/v1",
		Model:   "mimo-v2.5-pro",
	}

	selection := Resolve(provider, provider.Model, "high", "low")
	if selection.Variant != "high" || selection.LegacyEffort != "" || selection.DisplayEffort != "high" {
		t.Fatalf("unexpected selection: %+v", selection)
	}
	if got := selection.ProviderOptions["reasoningEffort"]; got != "high" {
		t.Fatalf("provider options = %#v", selection.ProviderOptions)
	}
}

func TestResolveMergesProviderCompatBaseOptions(t *testing.T) {
	provider := config.ProviderConfig{
		Type:  "openai",
		Model: "gpt-5.5",
	}

	selection := Resolve(provider, provider.Model, "high", "")
	if selection.Variant != "high" {
		t.Fatalf("selection = %+v", selection)
	}
	if got := selection.ProviderOptions["reasoningEffort"]; got != "high" {
		t.Fatalf("reasoningEffort = %#v", got)
	}
	if got := selection.ProviderOptions["reasoningSummary"]; got != "auto" {
		t.Fatalf("reasoningSummary = %#v", got)
	}
	if got := selection.ProviderOptions["textVerbosity"]; got != "low" {
		t.Fatalf("textVerbosity = %#v", got)
	}
	if got := selection.ProviderOptions["store"]; got != false {
		t.Fatalf("store = %#v", got)
	}
	if got := selection.ProviderOptions["promptCacheKeySupported"]; got != true {
		t.Fatalf("promptCacheKeySupported = %#v", got)
	}
}

func TestResolveScopesPromptCacheKeyToSupportingProviders(t *testing.T) {
	tests := []struct {
		name       string
		providerID string
		provider   config.ProviderConfig
		want       bool
	}{
		{name: "OpenAI", providerID: "openai", provider: config.ProviderConfig{Type: "openai", NPM: "@ai-sdk/openai", Model: "gpt-5.5"}, want: true},
		{name: "OpenRouter", providerID: "openrouter", provider: config.ProviderConfig{Type: "openai-compatible", NPM: "@openrouter/ai-sdk-provider", Model: "openai/gpt-5.5"}, want: true},
		{name: "xAI", providerID: "xai", provider: config.ProviderConfig{Type: "openai-compatible", NPM: "@ai-sdk/xai", Model: "grok-4"}},
		{name: "GLM", providerID: "zai", provider: config.ProviderConfig{Type: "openai-compatible", NPM: "@ai-sdk/openai-compatible", Model: "glm-4.6"}},
		{name: "Qwen", providerID: "alibaba", provider: config.ProviderConfig{Type: "openai-compatible", NPM: "@ai-sdk/openai-compatible", Model: "qwen-plus"}},
		{name: "DeepSeek", providerID: "deepseek", provider: config.ProviderConfig{Type: "openai-compatible", NPM: "@ai-sdk/openai-compatible", Model: "deepseek-chat"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			selection := ResolveForProvider(tc.providerID, tc.provider, tc.provider.Model, "", "")
			got, exists := selection.ProviderOptions["promptCacheKeySupported"]
			if tc.want {
				if got != true {
					t.Fatalf("promptCacheKeySupported = %#v; options=%#v", got, selection.ProviderOptions)
				}
			} else if exists {
				t.Fatalf("unsupported provider inherited prompt cache key: %#v", selection.ProviderOptions)
			}
		})
	}
}

func TestResolveKeepsProviderCompatDefaultOptionsWithoutVariant(t *testing.T) {
	provider := config.ProviderConfig{
		Type:  "openai-compatible",
		Model: "gpt-5.5",
	}

	selection := Resolve(provider, provider.Model, "", "")
	if selection.Variant != "" || selection.DisplayEffort != "" {
		t.Fatalf("selection = %+v", selection)
	}
	if got := selection.ProviderOptions["reasoningEffort"]; got != "medium" {
		t.Fatalf("reasoningEffort = %#v", got)
	}
	if _, ok := selection.ProviderOptions["reasoningSummary"]; ok {
		t.Fatalf("generic OpenAI-compatible should not set reasoningSummary: %#v", selection.ProviderOptions)
	}
}

func TestResolveMatchesProviderCompatForGPT5BaseOptionEdges(t *testing.T) {
	tests := []struct {
		name       string
		provider   config.ProviderConfig
		want       map[string]any
		wantAbsent []string
	}{
		{
			name: "openai gpt-5.2 text verbosity",
			provider: config.ProviderConfig{
				Type:  "openai",
				NPM:   "@ai-sdk/openai",
				Model: "gpt-5.2",
			},
			want: map[string]any{
				"store":            false,
				"reasoningEffort":  "medium",
				"reasoningSummary": "auto",
				"include":          []any{"reasoning.encrypted_content"},
				"textVerbosity":    "low",
			},
			wantAbsent: []string{"temperature", "topP", "topK", "enable_thinking", "chat_template_args"},
		},
		{
			name: "openai-compatible gpt-5.4 omits responses-only options",
			provider: config.ProviderConfig{
				Type:  "openai-compatible",
				NPM:   "@ai-sdk/openai-compatible",
				Model: "gpt-5.4",
			},
			want: map[string]any{
				"reasoningEffort": "medium",
				"textVerbosity":   "low",
			},
			wantAbsent: []string{"reasoningSummary", "include"},
		},
		{
			name: "gpt-5 chat omits reasoning defaults",
			provider: config.ProviderConfig{
				Type:  "openai",
				NPM:   "@ai-sdk/openai",
				Model: "gpt-5-chat",
			},
			want:       map[string]any{"store": false},
			wantAbsent: []string{"reasoningEffort", "textVerbosity"},
		},
		{
			name: "gpt-5.2 chat omits text verbosity",
			provider: config.ProviderConfig{
				Type:  "openai",
				NPM:   "@ai-sdk/openai",
				Model: "gpt-5.2-chat-latest",
			},
			want: map[string]any{
				"store":            false,
				"reasoningEffort":  "medium",
				"reasoningSummary": "auto",
				"include":          []any{"reasoning.encrypted_content"},
			},
			wantAbsent: []string{"textVerbosity"},
		},
		{
			name: "gpt-5 codex omits text verbosity",
			provider: config.ProviderConfig{
				Type:  "openai",
				NPM:   "@ai-sdk/openai",
				Model: "gpt-5.2-codex",
			},
			want: map[string]any{
				"store":            false,
				"reasoningEffort":  "medium",
				"reasoningSummary": "auto",
				"include":          []any{"reasoning.encrypted_content"},
			},
			wantAbsent: []string{"textVerbosity"},
		},
		{
			name: "openai gpt-6-astra defaults",
			provider: config.ProviderConfig{
				Type:  "openai",
				NPM:   "@ai-sdk/openai",
				Model: "gpt-6-astra",
			},
			want: map[string]any{
				"store":            false,
				"reasoningEffort":  "low",
				"reasoningSummary": "auto",
				"include":          []any{"reasoning.encrypted_content"},
				"textVerbosity":    "low",
			},
			wantAbsent: []string{"temperature", "topP", "topK"},
		},
		{
			name: "azure gpt-5.5 omits reasoning effort",
			provider: config.ProviderConfig{
				Type:  "openai-compatible",
				NPM:   "@ai-sdk/azure",
				Model: "gpt-5.5",
			},
			want: map[string]any{
				"store":            false,
				"reasoningSummary": "auto",
			},
			wantAbsent: []string{"reasoningEffort"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			selection := Resolve(tc.provider, tc.provider.Model, "", "")
			for key, want := range tc.want {
				if got := selection.ProviderOptions[key]; !optionEqual(got, want) {
					t.Fatalf("%s = %#v, want %#v; options=%#v", key, got, want, selection.ProviderOptions)
				}
			}
			for _, key := range tc.wantAbsent {
				if _, ok := selection.ProviderOptions[key]; ok {
					t.Fatalf("%s should be unset: %#v", key, selection.ProviderOptions)
				}
			}
		})
	}
}

func TestResolveMergesOpenRouterUsageOptions(t *testing.T) {
	provider := config.ProviderConfig{
		Type:    "openai-compatible",
		BaseURL: "https://openrouter.ai/api/v1",
		Model:   "openai/gpt-5.5",
	}

	selection := ResolveForProvider("openrouter", provider, provider.Model, "high", "")
	usage, ok := selection.ProviderOptions["usage"].(map[string]any)
	if !ok || usage["include"] != true {
		t.Fatalf("usage = %#v", selection.ProviderOptions)
	}
	reasoning, ok := selection.ProviderOptions["reasoning"].(map[string]any)
	if !ok || reasoning["effort"] != "high" {
		t.Fatalf("reasoning = %#v", selection.ProviderOptions)
	}
}

func variantIDs(variants []Variant) []string {
	out := make([]string, 0, len(variants))
	for _, variant := range variants {
		out = append(out, variant.ID)
	}
	return out
}

func optionEqual(got, want any) bool {
	return reflect.DeepEqual(got, want)
}
