package providers

import "testing"

// TestContextWindowFor_KnownModelsAreReasonable verifies that the
// function returns a "sensible" window for well-known canonical model
// names. Bounds rather than exact values, so a future catwalk bump
// that nudges (say) 200_000 → 198_000 doesn't break the suite. The
// point of the test is "the resolution chain works", not "this exact
// integer is forever".
func TestContextWindowFor_KnownModelsAreReasonable(t *testing.T) {
	cases := []struct {
		model string
		min   int
		max   int
	}{
		// Anthropic Claude 4.x (catwalk says some are 1M)
		{"claude-sonnet-4-5", 150_000, 1_500_000},
		{"claude-opus-4", 150_000, 1_500_000},
		{"claude-haiku-4-5", 150_000, 1_500_000},
		{"claude-sonnet-4-6", 150_000, 1_500_000},
		{"claude-opus-4-6", 150_000, 1_500_000},
		// Anthropic Claude 3.x
		{"claude-3-5-sonnet-20241022", 150_000, 250_000},
		{"claude-3-5-haiku-20241022", 150_000, 250_000},
		// OpenAI
		{"gpt-4o", 100_000, 250_000},
		{"gpt-4o-mini", 100_000, 250_000},
		{"gpt-4-turbo", 100_000, 250_000},
		{"o1", 100_000, 250_000},
		{"o3", 100_000, 250_000},
		// Google
		{"gemini-1.5-pro", 500_000, 3_000_000},
		{"gemini-2.5-pro", 500_000, 3_000_000},
		// DeepSeek. catwalk's "deepseek-v3" exact id only exists in
		// the aihubmix provider, whose data is unreliable, so the
		// sanity-cap filter drops it and we fall through to wuu's
		// substring registry (deepseek → 64k). The OpenRouter form
		// "deepseek/deepseek-chat" has good catwalk data (~163k).
		{"deepseek-chat", 30_000, 200_000},
		{"deepseek/deepseek-chat", 30_000, 200_000},
	}
	for _, tc := range cases {
		t.Run(tc.model, func(t *testing.T) {
			got := ContextWindowFor(tc.model)
			if got < tc.min || got > tc.max {
				t.Fatalf("ContextWindowFor(%q) = %d, want in [%d, %d]",
					tc.model, got, tc.min, tc.max)
			}
		})
	}
}

// TestContextWindowFor_FallsBackToSubstringRegistry exercises the
// path where catwalk doesn't have an entry for a model name and we
// fall back to wuu's hand-rolled substring registry. We pick a fake
// model id that genuinely doesn't appear anywhere in catwalk but
// that wuu's substring rules will recognize via family matching.
func TestContextWindowFor_FallsBackToSubstringRegistry(t *testing.T) {
	// "secretcorp/internal-claude-sonnet-prod" — a hypothetical proxy
	// alias. catwalk has no exact or substring match for this id.
	// Wuu's hand-rolled "claude-sonnet" rule should win and return
	// 200k.
	got := ContextWindowFor("secretcorp/internal-claude-sonnet-prod")
	if got != 200_000 {
		t.Fatalf("expected wuu substring registry's claude-sonnet rule to fire (200k), got %d", got)
	}
}

func TestContextWindowFor_DefaultForUnknown(t *testing.T) {
	cases := []string{
		"some-brand-new-model-2030",
		"my-private-llm-7b",
		"completely-made-up",
	}
	for _, m := range cases {
		t.Run(m, func(t *testing.T) {
			if got := ContextWindowFor(m); got != defaultContextWindow {
				t.Fatalf("ContextWindowFor(%q) = %d, want default %d",
					m, got, defaultContextWindow)
			}
		})
	}
}

func TestContextWindowFor_EmptyString(t *testing.T) {
	if got := ContextWindowFor(""); got != defaultContextWindow {
		t.Fatalf("expected default for empty string, got %d", got)
	}
}

func TestContextWindowFor_CaseInsensitive(t *testing.T) {
	lower := ContextWindowFor("claude-sonnet-4-5")
	upper := ContextWindowFor("Claude-Sonnet-4-5")
	if lower == 0 || upper == 0 {
		t.Fatalf("expected non-zero, got lower=%d upper=%d", lower, upper)
	}
	if lower != upper {
		t.Fatalf("case sensitivity leak: lower=%d upper=%d", lower, upper)
	}
}

func TestContextWindowFor_TrimsWhitespace(t *testing.T) {
	if a, b := ContextWindowFor("gpt-4o"), ContextWindowFor("  gpt-4o  "); a != b {
		t.Fatalf("expected whitespace to be trimmed: %d vs %d", a, b)
	}
}

func TestContextWindowFor_OpenRouterPrefixStripped(t *testing.T) {
	// catwalk's id is "claude-sonnet-4-5"; OpenRouter routes it as
	// "anthropic/claude-sonnet-4-5". The vendor prefix should not
	// stop the lookup from finding it.
	bare := ContextWindowFor("claude-sonnet-4-5")
	prefixed := ContextWindowFor("anthropic/claude-sonnet-4-5")
	if bare == 0 || prefixed == 0 {
		t.Fatalf("expected both to resolve, got bare=%d prefixed=%d", bare, prefixed)
	}
	if bare != prefixed {
		t.Fatalf("vendor prefix changed result: bare=%d prefixed=%d", bare, prefixed)
	}
}
