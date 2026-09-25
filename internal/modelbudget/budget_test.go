package modelbudget

import (
	"testing"

	"github.com/blueberrycongee/wuu/internal/config"
)

func TestResolveUnknownModelStaysUnknown(t *testing.T) {
	budget := Resolve("private-unknown-model", config.ProviderConfig{Type: "anthropic"}, 0)
	if budget.ContextWindowTokens != 0 || budget.ContextWindowKnown {
		t.Fatalf("unknown model should not get a synthetic context window: %+v", budget)
	}
	if budget.ContextWindowSource != SourceUnknown {
		t.Fatalf("ContextWindowSource = %q, want %q", budget.ContextWindowSource, SourceUnknown)
	}
	if budget.UsableInputTokens != 0 || budget.CompactThresholdTokens != 0 {
		t.Fatalf("unknown model should not synthesize compact thresholds: %+v", budget)
	}
}

func TestResolveProviderModelLimitAndOutputReserve(t *testing.T) {
	provider := config.ProviderConfig{
		Type:  "anthropic",
		Model: "alias",
		Models: map[string]config.ProviderModelConfig{
			"alias": {
				ID: "base-model",
			},
			"base-model": {
				Limit: &config.ProviderModelLimitConfig{
					Context: 1_000_000,
					Input:   900_000,
					Output:  128_000,
				},
			},
		},
	}
	budget := Resolve("alias", provider, 0)
	if budget.ContextWindowTokens != 1_000_000 || budget.ContextWindowSource != SourceProviderModelLimit {
		t.Fatalf("unexpected context resolution: %+v", budget)
	}
	if budget.InputLimitTokens != 900_000 {
		t.Fatalf("InputLimitTokens = %d, want 900000", budget.InputLimitTokens)
	}
	if budget.OutputLimitTokens != 128_000 || budget.OutputReserveTokens != 128_000 {
		t.Fatalf("unexpected output budget: %+v", budget)
	}
	// ceiling = min(context 1M, input cap 900k); usable subtracts the capped
	// 20k output reserve plus the 13k compact input buffer.
	if budget.UsableInputTokens != 867_000 {
		t.Fatalf("UsableInputTokens = %d, want input cap minus 20k reserve minus 13k buffer", budget.UsableInputTokens)
	}
}

func TestResolveProviderOverrideWins(t *testing.T) {
	budget := Resolve("MiniMax-M3", config.ProviderConfig{
		Type:          "anthropic",
		ContextWindow: 512_000,
	}, 1_000_000)
	if budget.ContextWindowTokens != 512_000 || budget.ContextWindowSource != SourceProviderContextWindow {
		t.Fatalf("provider override should win: %+v", budget)
	}
}

func TestResolveProviderContextOverrideWinsOverModelLimit(t *testing.T) {
	budget := Resolve("k3", config.ProviderConfig{
		Type:          "anthropic",
		ContextWindow: 272_000,
		Models: map[string]config.ProviderModelConfig{
			"k3": {
				Limit: &config.ProviderModelLimitConfig{Context: 1_048_576},
			},
			"k3-256k": {
				Limit: &config.ProviderModelLimitConfig{Context: 262_144},
			},
		},
	}, 0)
	if budget.ContextWindowTokens != 272_000 || budget.ContextWindowSource != SourceProviderContextWindow {
		t.Fatalf("explicit provider context should override the K3 model limit: %+v", budget)
	}

	budget = Resolve("k3-256k", config.ProviderConfig{
		Type:          "anthropic",
		ContextWindow: 272_000,
		Models: map[string]config.ProviderModelConfig{
			"k3-256k": {
				Limit: &config.ProviderModelLimitConfig{Context: 262_144},
			},
		},
	}, 0)
	if budget.ContextWindowTokens != 272_000 || budget.ContextWindowSource != SourceProviderContextWindow {
		t.Fatalf("explicit provider context should override the K3-256k model limit: %+v", budget)
	}
}

func TestResolveCodexProviderContextOverrideCanRaiseAutomaticLimit(t *testing.T) {
	budget := Resolve("gpt-5.6-sol", config.ProviderConfig{
		Type:          "openai-codex",
		ContextWindow: 922_000,
		Models: map[string]config.ProviderModelConfig{
			"gpt-5.6-sol": {
				ContextWindow: 400_000,
				Limit: &config.ProviderModelLimitConfig{
					Context: 400_000,
					Input:   272_000,
					Output:  128_000,
				},
			},
		},
	}, 0)
	if budget.ContextWindowTokens != 922_000 || budget.InputLimitTokens != 0 {
		t.Fatalf("explicit Codex context override was clamped to automatic metadata: %+v", budget)
	}
	if got, source := budget.EffectiveContextWindow(); got != 922_000 || source != SourceProviderContextWindow {
		t.Fatalf("EffectiveContextWindow = %d, %q; want explicit provider override", got, source)
	}
}

func TestResolveAliasUsesAPIModelCodexCap(t *testing.T) {
	provider := config.ProviderConfig{
		Type: "openai-codex",
		Models: map[string]config.ProviderModelConfig{
			"fast": {
				ID: "gpt-5.5",
				Limit: &config.ProviderModelLimitConfig{
					Context: 400_000,
					Input:   390_000,
					Output:  32_000,
				},
			},
		},
	}
	budget := Resolve("fast", provider, 0)
	if budget.InputLimitTokens != CodexSubscriptionGPT5InputCap {
		t.Fatalf("InputLimitTokens = %d, want Codex subscription cap %d", budget.InputLimitTokens, CodexSubscriptionGPT5InputCap)
	}
}

func TestCodexSubscriptionContextWindowUsesGPT6Window(t *testing.T) {
	if got := CodexSubscriptionContextWindow("gpt-6-astra", "openai-codex"); got != CodexSubscriptionGPT6ContextWindow {
		t.Fatalf("gpt-6-astra window = %d, want %d", got, CodexSubscriptionGPT6ContextWindow)
	}
	if got := CodexSubscriptionContextWindow("gpt-5.6-sol", "openai-codex"); got != CodexSubscriptionGPT6ContextWindow {
		t.Fatalf("gpt-5.6-sol window = %d, want %d", got, CodexSubscriptionGPT6ContextWindow)
	}
	if got := CodexSubscriptionContextWindow("gpt-5.5", "openai-codex"); got != CodexSubscriptionGPT5ContextWindow {
		t.Fatalf("gpt-5.5 window = %d, want %d", got, CodexSubscriptionGPT5ContextWindow)
	}
}

func TestEffectiveContextWindowUsesCodexInputCapForGPT6Astra(t *testing.T) {
	budget := Resolve("gpt-6-astra", config.ProviderConfig{
		Type:  "openai-codex",
		Model: "gpt-6-astra",
	}, 0)
	got, source := budget.EffectiveContextWindow()
	if got != CodexSubscriptionGPT6InputCap || source != SourceProviderInputLimit {
		t.Fatalf("EffectiveContextWindow = %d, %q; want %d, %q", got, source, CodexSubscriptionGPT6InputCap, SourceProviderInputLimit)
	}
}

func TestEffectiveContextWindowUsesCodexInputCapWhenModelWindowUnknown(t *testing.T) {
	budget := Resolve("gpt-5.5", config.ProviderConfig{
		Type:  "openai-codex",
		Model: "gpt-5.5",
	}, 0)
	if budget.ContextWindowTokens != 0 || budget.ContextWindowSource != SourceUnknown {
		t.Fatalf("model context window should stay unknown: %+v", budget)
	}
	got, source := budget.EffectiveContextWindow()
	if got != CodexSubscriptionGPT5InputCap || source != SourceProviderInputLimit {
		t.Fatalf("EffectiveContextWindow = %d, %q; want %d, %q", got, source, CodexSubscriptionGPT5InputCap, SourceProviderInputLimit)
	}
}

func TestEffectiveContextWindowPrefersLowerInputLimit(t *testing.T) {
	budget := Resolve("gpt-5.5", config.ProviderConfig{
		Type:  "openai-codex",
		Model: "gpt-5.5",
		Models: map[string]config.ProviderModelConfig{
			"gpt-5.5": {
				Limit: &config.ProviderModelLimitConfig{
					Context: 400_000,
					Input:   390_000,
					Output:  128_000,
				},
			},
		},
	}, 0)
	if budget.ContextWindowTokens != 400_000 || budget.InputLimitTokens != CodexSubscriptionGPT5InputCap {
		t.Fatalf("unexpected raw budget: %+v", budget)
	}
	got, source := budget.EffectiveContextWindow()
	if got != CodexSubscriptionGPT5InputCap || source != SourceProviderInputLimit {
		t.Fatalf("EffectiveContextWindow = %d, %q; want %d, %q", got, source, CodexSubscriptionGPT5InputCap, SourceProviderInputLimit)
	}
}
