package modelcatalog

import (
	"testing"

	"github.com/blueberrycongee/wuu/internal/config"
)

func TestMatchProviderByBaseURL(t *testing.T) {
	provider, ok := MatchProvider("xiaomi", config.ProviderConfig{
		Type:    "openai-compatible",
		BaseURL: "https://token-plan-cn.xiaomimimo.com/v1/",
	})
	if !ok {
		t.Fatal("expected Xiaomi token plan provider match")
	}
	if provider.ID != "xiaomi-token-plan-cn" {
		t.Fatalf("provider ID = %q", provider.ID)
	}

	ruleName, enriched := EnrichProvider("xiaomi", config.ProviderConfig{
		Type:    "openai-compatible",
		BaseURL: "https://token-plan-cn.xiaomimimo.com/v1",
		Model:   "mimo-v2.5-pro",
	}, "mimo-v2.5-pro")
	if ruleName != "xiaomi-token-plan-cn" {
		t.Fatalf("rule provider name = %q", ruleName)
	}
	model := enriched.Models["mimo-v2.5-pro"]
	if model.Name != "MiMo-V2.5-Pro" || model.ReleaseDate != "2026-04-22" {
		t.Fatalf("unexpected model metadata: %+v", model)
	}
	if model.Reasoning == nil || !*model.Reasoning {
		t.Fatalf("expected reasoning model metadata: %+v", model)
	}
	if enriched.ContextWindow != 0 {
		t.Fatalf("provider ContextWindow = %d, want 0 without explicit provider override", enriched.ContextWindow)
	}
	if model.Limit == nil || model.Limit.Context != 1048576 {
		t.Fatalf("model limit context = %+v, want 1048576", model.Limit)
	}
}

func TestMatchProviderTreatsTerminalV1AsOptional(t *testing.T) {
	provider, ok := MatchProvider("minimax", config.ProviderConfig{
		Type:    "anthropic",
		BaseURL: "https://api.minimaxi.com/anthropic",
		Model:   "MiniMax-M3",
	})
	if !ok {
		t.Fatal("expected MiniMax provider match without terminal /v1")
	}
	if provider.ID != "minimax-cn" {
		t.Fatalf("provider ID = %q, want minimax-cn", provider.ID)
	}

	ruleName, enriched := EnrichProvider("minimax", config.ProviderConfig{
		Type:    "anthropic",
		BaseURL: "https://api.minimaxi.com/anthropic",
		Model:   "MiniMax-M3",
	}, "MiniMax-M3")
	if ruleName != "minimax-cn" {
		t.Fatalf("rule provider name = %q", ruleName)
	}
	model := enriched.Models["MiniMax-M3"]
	// 1M live-verified 2026-07-06 (648k-token request accepted, ~2M rejected);
	// the launch-era 512k snapshot value systematically halved the compact
	// threshold.
	if model.Limit == nil || model.Limit.Context != 1_048_576 {
		t.Fatalf("model limit context = %+v, want 1048576", model.Limit)
	}
	if enriched.ContextWindow != 0 {
		t.Fatalf("provider ContextWindow = %d, want 0 without explicit provider override", enriched.ContextWindow)
	}
	if got := model.Options["anthropic_default_betas"]; got != false {
		t.Fatalf("MiniMax default anthropic_default_betas = %#v, want false", got)
	}

	_, overridden := EnrichProvider("minimax", config.ProviderConfig{
		Type:    "anthropic",
		BaseURL: "https://api.minimaxi.com/anthropic",
		Model:   "MiniMax-M3",
		Models: map[string]config.ProviderModelConfig{
			"MiniMax-M3": {Options: map[string]any{"anthropic_default_betas": true}},
		},
	}, "MiniMax-M3")
	if got := overridden.Models["MiniMax-M3"].Options["anthropic_default_betas"]; got != true {
		t.Fatalf("explicit MiniMax anthropic_default_betas = %#v, want true", got)
	}
}

func TestEnrichProviderInheritsKnownOpenAIModelForCompatibleGateway(t *testing.T) {
	provider := config.ProviderConfig{
		Type:      "openai-compatible",
		BaseURL:   "https://gateway.example.test/codex/v1",
		APIKeyEnv: "GATEWAY_API_KEY",
		Model:     "gpt-5.6-sol",
	}

	ruleName, enriched := EnrichProvider("gateway", provider, provider.Model)

	if ruleName != "gateway" {
		t.Fatalf("rule provider name = %q, want gateway", ruleName)
	}
	if enriched.Type != provider.Type || enriched.BaseURL != provider.BaseURL || enriched.APIKeyEnv != provider.APIKeyEnv {
		t.Fatalf("connection identity changed: %+v", enriched)
	}
	if enriched.API != "" || enriched.NPM != "" {
		t.Fatalf("catalog provider connection metadata leaked into gateway: api=%q npm=%q", enriched.API, enriched.NPM)
	}
	if len(enriched.Models) != 1 {
		t.Fatalf("models = %d, want only selected model: %+v", len(enriched.Models), enriched.Models)
	}
	model, ok := enriched.Models[provider.Model]
	if !ok {
		t.Fatalf("selected model metadata missing: %+v", enriched.Models)
	}
	if model.Name != "GPT-5.6 Sol" || model.Family != "gpt-sol" || model.ReleaseDate != "2026-07-09" {
		t.Fatalf("unexpected selected model identity: %+v", model)
	}
	if model.Reasoning == nil || !*model.Reasoning {
		t.Fatalf("reasoning metadata missing: %+v", model)
	}
	if model.Limit == nil || model.Limit.Context != 1_050_000 || model.Limit.Input != 922_000 || model.Limit.Output != 128_000 {
		t.Fatalf("unexpected selected model limits: %+v", model.Limit)
	}
}

func TestCatalogMatchesOpenCodeDefaultVisibility(t *testing.T) {
	provider, ok := MatchProvider("openai", config.ProviderConfig{Type: "openai"})
	if !ok {
		t.Fatal("expected OpenAI provider match")
	}

	var hasHiddenChatAlias bool
	var fastMode Model
	for _, model := range provider.Models {
		if model.ID == "gpt-5-chat-latest" {
			hasHiddenChatAlias = true
		}
		if model.ID == "gpt-5.5-fast" {
			fastMode = model
		}
	}
	if hasHiddenChatAlias {
		t.Fatal("catalog should hide OpenCode's invalid OpenAI chat alias")
	}
	if fastMode.ID == "" || fastMode.APIID != "gpt-5.5" || fastMode.Options["serviceTier"] != "priority" {
		t.Fatalf("unexpected experimental mode metadata: %+v", fastMode)
	}
}

func TestCatalogCarriesGPT56FamilyMetadata(t *testing.T) {
	provider, ok := MatchProvider("openai", config.ProviderConfig{Type: "openai"})
	if !ok {
		t.Fatal("expected OpenAI provider match")
	}

	models := make(map[string]Model, len(provider.Models))
	for _, model := range provider.Models {
		models[model.ID] = model
	}
	for _, id := range []string{"gpt-5.6", "gpt-5.6-sol", "gpt-5.6-terra", "gpt-5.6-luna"} {
		model, ok := models[id]
		if !ok {
			t.Fatalf("missing %s from OpenAI catalog", id)
		}
		if model.Limit == nil || model.Limit.Context != 1_050_000 || model.Limit.Output != 128_000 {
			t.Fatalf("unexpected %s limits: %+v", id, model.Limit)
		}
		if got := reasoningEfforts(model); !equalStrings(got, []string{"none", "low", "medium", "high", "xhigh", "max"}) {
			t.Fatalf("unexpected %s efforts: %v", id, got)
		}
	}
}

func TestCatalogCarriesGPT6AstraMetadata(t *testing.T) {
	provider, ok := MatchProvider("openai", config.ProviderConfig{Type: "openai"})
	if !ok {
		t.Fatal("expected OpenAI provider match")
	}

	models := make(map[string]Model, len(provider.Models))
	for _, model := range provider.Models {
		models[model.ID] = model
	}
	astra, ok := models["gpt-6-astra"]
	if !ok {
		t.Fatal("missing gpt-6-astra from OpenAI catalog")
	}
	if astra.Name != "GPT-6 Astra" || astra.Family != "gpt-astra" || astra.ReleaseDate != "2026-09-03" {
		t.Fatalf("unexpected Astra identity: %+v", astra)
	}
	if astra.Limit == nil || astra.Limit.Context != 1_050_000 || astra.Limit.Input != 922_000 || astra.Limit.Output != 128_000 {
		t.Fatalf("unexpected Astra limits: %+v", astra.Limit)
	}
	if astra.Temperature == nil || *astra.Temperature {
		t.Fatalf("Astra should reject temperature: %+v", astra.Temperature)
	}
	if got := reasoningEfforts(astra); !equalStrings(got, []string{"low", "medium", "high", "xhigh", "max"}) {
		t.Fatalf("unexpected Astra efforts: %v", got)
	}
	if astra.DefaultVariant != "low" {
		t.Fatalf("Astra default variant = %q, want low", astra.DefaultVariant)
	}

	fast, ok := models["gpt-6-astra-fast"]
	if !ok {
		t.Fatal("missing gpt-6-astra-fast from OpenAI catalog")
	}
	if fast.APIID != "gpt-6-astra" || fast.Options["serviceTier"] != "priority" {
		t.Fatalf("unexpected Astra Fast metadata: %+v", fast)
	}
}

func TestCodexSubscriptionCatalogExposesAstraFastAlias(t *testing.T) {
	codexProvider, ok := CodexSubscriptionCatalogProvider("gpt-6-astra")
	if !ok {
		t.Fatal("expected Codex subscription catalog metadata for gpt-6-astra")
	}
	var hasFast bool
	for _, model := range codexProvider.Models {
		if model.ID == "gpt-6-astra-fast" && model.APIID == "gpt-6-astra" {
			hasFast = true
		}
	}
	if !hasFast {
		t.Fatalf("Codex subscription catalog did not expose gpt-6-astra-fast alias: %+v", codexProvider.Models)
	}
}

func TestCatalogSnapshotMatchesOpenCodeDefaultVisibleCounts(t *testing.T) {
	providers, err := Providers()
	if err != nil {
		t.Fatalf("Providers: %v", err)
	}
	if len(providers) != 222 {
		t.Fatalf("provider count = %d, want 222", len(providers))
	}

	modelCount := 0
	for _, provider := range providers {
		for _, model := range provider.Models {
			modelCount++
			switch model.Status {
			case "deprecated", "alpha":
				t.Fatalf("catalog should hide %s model %s/%s", model.Status, provider.ID, model.ID)
			}
			if model.ID == "gpt-5-chat-latest" && (provider.ID == "openai" || provider.ID == "github-copilot" || provider.ID == "openrouter") {
				t.Fatalf("catalog should hide invalid chat alias %s/%s", provider.ID, model.ID)
			}
			if provider.ID == "openrouter" && model.ID == "openai/gpt-5-chat" {
				t.Fatalf("catalog should hide invalid OpenRouter chat alias")
			}
		}
	}
	if modelCount != 7714 {
		t.Fatalf("model count = %d, want 7714", modelCount)
	}
}

func TestDeepSeekOfficialCatalogCorrectionsExposeVisionModels(t *testing.T) {
	provider, ok := ProviderByID("deepseek")
	if !ok {
		t.Fatal("expected DeepSeek provider")
	}

	want := map[string]struct {
		name     string
		image    bool
		release  string
		efforts  []string
		defaultV string
		apiID    string
	}{
		"deepseek-flash": {
			name:     "DeepSeek V4.1 Flash",
			image:    true,
			release:  "2026-09-10",
			efforts:  []string{"low", "high", "max"},
			defaultV: "high",
		},
		"deepseek-v4-flash": {
			name:     "DeepSeek V4 Flash",
			image:    true,
			release:  "2026-09-10",
			efforts:  []string{"low", "high", "max"},
			defaultV: "high",
			apiID:    "deepseek-flash",
		},
		"deepseek-v4-flash-vision-exp": {
			name:     "DeepSeek V4 Flash Vision Exp",
			image:    true,
			release:  "2026-09-10",
			efforts:  []string{"low", "high", "max"},
			defaultV: "high",
			apiID:    "deepseek-flash",
		},
	}
	for _, model := range provider.Models {
		spec, exists := want[model.ID]
		if !exists {
			continue
		}
		if model.Name != spec.name || model.ReleaseDate != spec.release {
			t.Fatalf("%s identity = name=%q release=%q, want name=%q release=%q", model.ID, model.Name, model.ReleaseDate, spec.name, spec.release)
		}
		if model.Attachment == nil || *model.Attachment != spec.image {
			t.Fatalf("%s attachment = %+v, want %v", model.ID, model.Attachment, spec.image)
		}
		if model.Modalities == nil || !stringSliceContains(model.Modalities.Input, "image") {
			t.Fatalf("%s modalities = %+v, want image input", model.ID, model.Modalities)
		}
		if got := reasoningEfforts(model); !equalStrings(got, spec.efforts) {
			t.Fatalf("%s efforts = %v, want %v", model.ID, got, spec.efforts)
		}
		if model.DefaultVariant != spec.defaultV {
			t.Fatalf("%s default variant = %q, want %q", model.ID, model.DefaultVariant, spec.defaultV)
		}
		if model.APIID != spec.apiID {
			t.Fatalf("%s api id = %q, want %q", model.ID, model.APIID, spec.apiID)
		}
		delete(want, model.ID)
	}
	if len(want) != 0 {
		t.Fatalf("missing DeepSeek catalog corrections: %v", want)
	}
}

func TestKimiCodingCatalogExposesK28Alias(t *testing.T) {
	provider, ok := ProviderByID("kimi-code-plan-cn")
	if !ok {
		t.Fatal("expected Kimi coding-plan provider")
	}
	want := map[string]struct {
		name   string
		apiID  string
		ctx    int
		output int
	}{
		"kimi-for-coding": {
			name:   "Kimi K2.8",
			ctx:    1_048_576,
			output: 32_768,
		},
		"kimi-k2.8-preview": {
			name:   "Kimi K2.8 Preview",
			apiID:  "kimi-for-coding",
			ctx:    1_048_576,
			output: 32_768,
		},
	}
	for _, model := range provider.Models {
		spec, exists := want[model.ID]
		if !exists {
			continue
		}
		if model.Name != spec.name || model.APIID != spec.apiID {
			t.Fatalf("%s identity = name=%q api=%q, want name=%q api=%q", model.ID, model.Name, model.APIID, spec.name, spec.apiID)
		}
		if model.Limit == nil || model.Limit.Context != spec.ctx || model.Limit.Output != spec.output {
			t.Fatalf("%s limits = %+v, want context=%d output=%d", model.ID, model.Limit, spec.ctx, spec.output)
		}
		if got := reasoningEfforts(model); !equalStrings(got, []string{"low", "high", "max"}) {
			t.Fatalf("%s efforts = %v, want low,high,max", model.ID, got)
		}
		delete(want, model.ID)
	}
	if len(want) != 0 {
		t.Fatalf("missing Kimi coding catalog models: %v", want)
	}

	_, enriched := EnrichProvider("kimi-for-coding", config.ProviderConfig{
		Type:  "anthropic",
		Model: "kimi-k2.8-preview",
	}, "kimi-k2.8-preview")
	model := enriched.Models["kimi-k2.8-preview"]
	if model.Options["force_adaptive_thinking"] != true {
		t.Fatalf("unexpected K2.8 compatibility options: %+v", model.Options)
	}
}

func TestAlibabaCatalogIncludesQwen38(t *testing.T) {
	for _, providerID := range []string{"alibaba", "alibaba-cn"} {
		provider, ok := ProviderByID(providerID)
		if !ok {
			t.Fatalf("expected %s provider", providerID)
		}
		want := map[string]bool{"qwen3.8-max": false, "qwen3.8-flash": false}
		for _, model := range provider.Models {
			if _, exists := want[model.ID]; !exists {
				continue
			}
			if model.Limit == nil || model.Limit.Context != 1_000_000 || model.Limit.Output != 131_072 {
				t.Fatalf("%s/%s limits = %+v, want 1M/131072", providerID, model.ID, model.Limit)
			}
			if model.Modalities == nil || !stringSliceContains(model.Modalities.Input, "video") {
				t.Fatalf("%s/%s modalities = %+v, want video input", providerID, model.ID, model.Modalities)
			}
			want[model.ID] = true
		}
		for id, found := range want {
			if !found {
				t.Fatalf("%s missing %s", providerID, id)
			}
		}
	}
}

func TestKimiK3UsesUpstreamCatalogAndProviderCompatibility(t *testing.T) {
	provider, ok := ProviderByID("kimi-code-plan-cn")
	if !ok {
		t.Fatal("expected Kimi For Coding provider")
	}
	if provider.API != "https://api.kimi.com/coding/v1" || provider.NPM != "@ai-sdk/openai-compatible" {
		t.Fatalf("unexpected Kimi provider transport: %+v", provider)
	}
	if len(provider.Headers) != 0 || len(provider.ModelOptions) != 0 {
		t.Fatalf("raw catalog must not contain Wuu transport defaults: options=%+v headers=%+v", provider.ModelOptions, provider.Headers)
	}
	var k3 Model
	for _, model := range provider.Models {
		if model.ID == "k3" {
			k3 = model
			break
		}
	}
	if k3.ID == "" {
		t.Fatal("expected built-in K3 model")
	}
	if k3.Limit == nil || k3.Limit.Context != 1_048_576 || k3.Limit.Output != 131_072 {
		t.Fatalf("unexpected K3 limits: %+v", k3.Limit)
	}
	if got := reasoningEfforts(k3); !equalStrings(got, []string{"low", "high", "max"}) {
		t.Fatalf("unexpected K3 efforts: %v", got)
	}
	if k3.DefaultVariant != "" || len(k3.Variants) != 0 {
		t.Fatalf("K3 must use upstream reasoning options without Wuu variants: default=%q variants=%+v", k3.DefaultVariant, k3.Variants)
	}

	ruleName, enriched := EnrichProvider("kimi-for-coding", config.ProviderConfig{
		Type:  "anthropic",
		Model: "k3",
	}, "k3")
	if ruleName != "kimi-code-plan-cn" || enriched.BaseURL != "https://api.kimi.com/coding/v1" || enriched.NPM != "@ai-sdk/anthropic" {
		t.Fatalf("unexpected enriched K3 provider: rule=%q provider=%+v", ruleName, enriched)
	}
	model := enriched.Models["k3"]
	if model.Limit == nil || model.Limit.Context != 1_048_576 || model.Limit.Output != 131_072 {
		t.Fatalf("unexpected enriched K3 limits: %+v", model.Limit)
	}
	if model.Options["allow_empty_signature"] != true || model.Options["thinking_replay"] != "full" ||
		model.Options["force_adaptive_thinking"] != true || model.Options["anthropic_default_betas"] != false {
		t.Fatalf("unexpected K3 compatibility options: %+v", model.Options)
	}
	if enriched.Headers["User-Agent"] != "KimiCLI/1.5" {
		t.Fatalf("unexpected K3 headers: %+v", enriched.Headers)
	}
}

func TestOfficialCatalogAddsGrok47(t *testing.T) {
	provider, ok := ProviderByID("xai")
	if !ok {
		t.Fatal("expected xAI provider")
	}
	var model Model
	for _, candidate := range provider.Models {
		if candidate.ID == "grok-4.7" {
			model = candidate
			break
		}
	}
	if model.ID != "grok-4.7" || model.Name != "Grok 4.7" {
		t.Fatalf("missing grok-4.7 catalog entry: %+v", model)
	}
	if !model.Reasoning || model.Limit == nil || model.Limit.Context != 500_000 {
		t.Fatalf("unexpected grok-4.7 metadata: %+v", model)
	}
	if got := reasoningEfforts(model); !equalStrings(got, []string{"low", "medium", "high", "xhigh"}) {
		t.Fatalf("grok-4.7 efforts = %v", got)
	}
	if model.DefaultVariant != "high" {
		t.Fatalf("grok-4.7 default variant = %q", model.DefaultVariant)
	}
}

func TestGrokEffortsComeFromUpstreamCatalog(t *testing.T) {
	provider, ok := ProviderByID("xai")
	if !ok {
		t.Fatal("expected xAI provider")
	}
	want := map[string][]string{
		"grok-4.3":                   {"none", "low", "medium", "high"},
		"grok-4.20-multi-agent-0309": {"low", "medium", "high", "xhigh"},
	}
	for _, model := range provider.Models {
		efforts, exists := want[model.ID]
		if !exists {
			continue
		}
		if got := reasoningEfforts(model); !equalStrings(got, efforts) {
			t.Fatalf("%s efforts = %v, want %v", model.ID, got, efforts)
		}
		delete(want, model.ID)
	}
	if len(want) != 0 {
		t.Fatalf("missing upstream Grok models: %v", want)
	}
}

func TestKimiProviderDefaultsApplyWithoutK3ModelOverrides(t *testing.T) {
	_, enriched := EnrichProvider("kimi-for-coding", config.ProviderConfig{
		Type:  "anthropic",
		Model: "kimi-for-coding",
	}, "kimi-for-coding")

	model := enriched.Models["kimi-for-coding"]
	if model.Options["force_adaptive_thinking"] != true || model.Options["anthropic_default_betas"] != false {
		t.Fatalf("unexpected Kimi provider options: %+v", model.Options)
	}
	if _, exists := model.Options["allow_empty_signature"]; exists {
		t.Fatalf("K3-only empty signature option leaked to generic Kimi model: %+v", model.Options)
	}
	if enriched.Headers["User-Agent"] != "KimiCLI/1.5" {
		t.Fatalf("unexpected Kimi provider headers: %+v", enriched.Headers)
	}

	_, overridden := EnrichProvider("kimi-for-coding", config.ProviderConfig{
		Type:    "anthropic",
		Model:   "k2p7",
		Headers: map[string]string{"User-Agent": "custom-client"},
		Models: map[string]config.ProviderModelConfig{
			"k2p7": {Options: map[string]any{"anthropic_default_betas": true}},
		},
	}, "k2p7")
	if overridden.Headers["User-Agent"] != "custom-client" || overridden.Models["k2p7"].Options["anthropic_default_betas"] != true {
		t.Fatalf("user Kimi overrides lost: provider=%+v model=%+v", overridden.Headers, overridden.Models["k2p7"].Options)
	}
}

func reasoningEfforts(model Model) []string {
	for _, option := range model.ReasoningOptions {
		if option["type"] != "effort" {
			continue
		}
		switch values := option["values"].(type) {
		case []string:
			return append([]string(nil), values...)
		case []any:
			out := make([]string, 0, len(values))
			for _, value := range values {
				if effort, ok := value.(string); ok {
					out = append(out, effort)
				}
			}
			return out
		}
	}
	return nil
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func TestModelConfigCarriesCapabilityMetadata(t *testing.T) {
	provider, ok := MatchProvider("openai", config.ProviderConfig{Type: "openai"})
	if !ok {
		t.Fatal("expected OpenAI provider match")
	}
	enriched := MergeProvider(config.ProviderConfig{Type: "openai"}, provider, "text-embedding-3-large", "o3")

	embedding := enriched.Models["text-embedding-3-large"]
	if embedding.ToolCall == nil || *embedding.ToolCall {
		t.Fatalf("expected embedding model to carry tool_call=false: %+v", embedding)
	}
	if embedding.Modalities == nil || !stringSliceContains(embedding.Modalities.Input, "text") {
		t.Fatalf("expected embedding modalities: %+v", embedding.Modalities)
	}

	o3 := enriched.Models["o3"]
	if o3.ToolCall == nil || !*o3.ToolCall || o3.Modalities == nil || !stringSliceContains(o3.Modalities.Input, "image") || !stringSliceContains(o3.Modalities.Input, "pdf") {
		t.Fatalf("expected o3 tool and media capability metadata: %+v", o3)
	}
}

func stringSliceContains(values []string, needle string) bool {
	for _, value := range values {
		if value == needle {
			return true
		}
	}
	return false
}

func TestMatchProviderDoesNotUseWireTypeForCustomEndpoint(t *testing.T) {
	if provider, ok := MatchProvider("zhipu2", config.ProviderConfig{
		Type:    "anthropic",
		BaseURL: "https://open.bigmodel.cn/api/anthropic",
		Model:   "glm-5.1",
	}); ok {
		t.Fatalf("custom Anthropic-compatible endpoint matched %q", provider.ID)
	}
}

func TestMatchProviderDoesNotUseProviderNameForCustomEndpoint(t *testing.T) {
	for _, tc := range []struct {
		name     string
		provider config.ProviderConfig
	}{
		{
			name: "openai",
			provider: config.ProviderConfig{
				Type:    "openai-compatible",
				BaseURL: "https://proxy.example/v1",
			},
		},
		{
			name: "claude",
			provider: config.ProviderConfig{
				Type:    "anthropic",
				BaseURL: "https://proxy.example/anthropic",
			},
		},
		{
			name: "gemini",
			provider: config.ProviderConfig{
				Type:    "openai-compatible",
				BaseURL: "https://proxy.example/v1",
			},
		},
	} {
		if provider, ok := MatchProvider(tc.name, tc.provider); ok {
			t.Fatalf("custom endpoint %q matched %q", tc.name, provider.ID)
		}
	}
}

func TestMatchProviderDoesNotTreatCodexSubscriptionAsOpenCodeZen(t *testing.T) {
	if provider, ok := MatchProvider("openai-codex", config.ProviderConfig{
		Type:    "openai-codex",
		BaseURL: "https://chatgpt.com/backend-api/codex",
		Model:   "gpt-5.5",
	}); ok {
		t.Fatalf("Codex subscription provider matched %q", provider.ID)
	}

	codexProvider, ok := CodexSubscriptionCatalogProvider("gpt-5.5")
	if !ok {
		t.Fatal("expected Codex subscription catalog metadata")
	}
	var hasFast bool
	for _, model := range codexProvider.Models {
		if model.ID == "gpt-5.5-fast" && model.APIID == "gpt-5.5" {
			hasFast = true
		}
	}
	if !hasFast {
		t.Fatalf("Codex subscription catalog did not expose gpt-5.5-fast alias: %+v", codexProvider.Models)
	}

	_, enriched := EnrichProvider("openai-codex", config.ProviderConfig{
		Type:    "openai-codex",
		BaseURL: "https://chatgpt.com/backend-api/codex",
		Model:   "gpt-5.5",
	}, "gpt-5.5")
	if enriched.APIKeyEnv != "" {
		t.Fatalf("Codex subscription provider inherited API key env %q", enriched.APIKeyEnv)
	}

	provider, ok := MatchProvider("opencode", config.ProviderConfig{
		Type:    "openai-compatible",
		BaseURL: "https://opencode.ai/zen/v1",
	})
	if !ok || provider.ID != "opencode" {
		t.Fatalf("OpenCode Zen provider match = %q, %v", provider.ID, ok)
	}
}

func TestMatchProviderDisambiguatesDuplicateEndpointByName(t *testing.T) {
	for _, tc := range []struct {
		name string
		want string
	}{
		{name: "fireworks-ai", want: "fireworks-ai"},
	} {
		provider, ok := MatchProvider(tc.name, config.ProviderConfig{
			Type:    "openai-compatible",
			BaseURL: "https://api.fireworks.ai/inference/v1",
		})
		if !ok || provider.ID != tc.want {
			t.Fatalf("provider %q matched %q, %v; want %q", tc.name, provider.ID, ok, tc.want)
		}
	}

	provider, ok := MatchProvider("minimax-coding-plan", config.ProviderConfig{
		Type:    "anthropic",
		BaseURL: "https://api.minimax.io/anthropic/v1",
	})
	if !ok || provider.ID != "minimax-coding-plan" {
		t.Fatalf("MiniMax coding plan matched %q, %v", provider.ID, ok)
	}
}

func TestMergeProviderCarriesModelOptionsAndHeaders(t *testing.T) {
	provider, ok := MatchProvider("anthropic", config.ProviderConfig{Type: "anthropic"})
	if !ok {
		t.Fatal("expected Anthropic provider match")
	}
	enriched := MergeProvider(config.ProviderConfig{Type: "anthropic"}, provider, "claude-opus-4-8-fast")
	model := enriched.Models["claude-opus-4-8-fast"]
	if model.ID != "claude-opus-4-8" || model.Options["speed"] != "fast" {
		t.Fatalf("unexpected fast model metadata: %+v", model)
	}
	if enriched.Headers["anthropic-beta"] != "fast-mode-2026-02-01" {
		t.Fatalf("unexpected merged headers: %+v", enriched.Headers)
	}
}

func TestMergeProviderPromotesSelectedModelProviderOverride(t *testing.T) {
	ruleName, enriched := EnrichProvider("zenmux", config.ProviderConfig{
		Type:  "openai-compatible",
		Model: "anthropic/claude-opus-4.7",
	}, "anthropic/claude-opus-4.7")
	if ruleName != "zenmux" {
		t.Fatalf("rule provider name = %q", ruleName)
	}
	if enriched.Type != "anthropic" {
		t.Fatalf("provider type = %q, want anthropic", enriched.Type)
	}
	if enriched.API != "https://zenmux.ai/api/anthropic/v1" || enriched.BaseURL != "https://zenmux.ai/api/anthropic/v1" {
		t.Fatalf("provider endpoint not promoted: api=%q base=%q", enriched.API, enriched.BaseURL)
	}
	if enriched.NPM != "@ai-sdk/anthropic" {
		t.Fatalf("provider npm = %q", enriched.NPM)
	}
	if enriched.APIKeyEnv != "ZENMUX_API_KEY" {
		t.Fatalf("provider api_key_env = %q", enriched.APIKeyEnv)
	}
	if enriched.ContextWindow != 0 {
		t.Fatalf("provider ContextWindow = %d, want 0 without explicit provider override", enriched.ContextWindow)
	}
	model := enriched.Models["anthropic/claude-opus-4.7"]
	if model.Limit == nil || model.Limit.Context != 1000000 {
		t.Fatalf("model limit context = %+v, want 1000000", model.Limit)
	}
}

// TestMergeModelConfigUserContextWinsOverCatalog locks the joint guard on the
// two spellings of the context fact (ContextWindow / Limit.Context): when the
// user wrote either one, the catalog must not fill the other spelling, or
// budget lookups that read ContextWindow first would shadow the user's value.
func TestMergeModelConfigUserContextWinsOverCatalog(t *testing.T) {
	catalog := config.ProviderModelConfig{
		ContextWindow: 1_000_000,
		Limit:         &config.ProviderModelLimitConfig{Context: 1_000_000, Output: 128_000},
	}

	userLimit := config.ProviderModelConfig{
		Limit: &config.ProviderModelLimitConfig{Context: 60_000},
	}
	merged := MergeModelConfig(userLimit, catalog)
	if merged.Limit.Context != 60_000 {
		t.Fatalf("Limit.Context = %d, want user 60000", merged.Limit.Context)
	}
	if merged.ContextWindow != 0 {
		t.Fatalf("ContextWindow = %d, want 0 (catalog must not fill the other spelling)", merged.ContextWindow)
	}
	if merged.Limit.Output != 128_000 {
		t.Fatalf("Limit.Output = %d, want catalog 128000 (independent field still merges)", merged.Limit.Output)
	}

	userWindow := config.ProviderModelConfig{ContextWindow: 60_000}
	merged = MergeModelConfig(userWindow, catalog)
	if merged.ContextWindow != 60_000 {
		t.Fatalf("ContextWindow = %d, want user 60000", merged.ContextWindow)
	}
	if merged.Limit == nil || merged.Limit.Context != 0 {
		t.Fatalf("Limit = %+v, want Context 0 (catalog must not fill the other spelling)", merged.Limit)
	}
}

func TestMergeModelConfigPreservesCatalogCompatibilityMaps(t *testing.T) {
	catalog := config.ProviderModelConfig{
		Options: map[string]any{
			"allow_empty_signature": true,
			"thinking":              map[string]any{"type": "adaptive", "display": "summarized"},
		},
		Headers: map[string]string{"User-Agent": "KimiCLI/1.5"},
		Variants: map[string]map[string]any{
			"max": {"effort": "max", "thinking": map[string]any{"type": "adaptive"}},
		},
	}
	user := config.ProviderModelConfig{
		Options: map[string]any{
			"custom":   true,
			"thinking": map[string]any{"display": "omitted"},
		},
		Headers: map[string]string{"X-Custom": "value"},
		Variants: map[string]map[string]any{
			"custom": {"effort": "high"},
			"max":    {"thinking": map[string]any{"display": "omitted"}},
		},
	}

	merged := MergeModelConfig(user, catalog)
	if merged.Options["allow_empty_signature"] != true || merged.Options["custom"] != true {
		t.Fatalf("options did not merge: %+v", merged.Options)
	}
	thinking, _ := merged.Options["thinking"].(map[string]any)
	if thinking["type"] != "adaptive" || thinking["display"] != "omitted" {
		t.Fatalf("nested options did not merge with user precedence: %+v", thinking)
	}
	if merged.Headers["User-Agent"] != "KimiCLI/1.5" || merged.Headers["X-Custom"] != "value" {
		t.Fatalf("headers did not merge: %+v", merged.Headers)
	}
	if merged.Variants["custom"]["effort"] != "high" || merged.Variants["max"]["effort"] != "max" {
		t.Fatalf("variants did not merge: %+v", merged.Variants)
	}
	maxThinking, _ := merged.Variants["max"]["thinking"].(map[string]any)
	if maxThinking["type"] != "adaptive" || maxThinking["display"] != "omitted" {
		t.Fatalf("nested variant options did not merge with user precedence: %+v", maxThinking)
	}
}
