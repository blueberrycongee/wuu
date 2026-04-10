package providers

import "testing"

func TestContextWindowFor(t *testing.T) {
	cases := []struct {
		model string
		want  int
	}{
		// Anthropic — exact + variant + family fallback
		{"claude-opus-4-6-1m", 1_000_000},
		{"claude-opus-4-1m-20251022", 1_000_000},
		{"claude-opus-4-6-20251022", 200_000},
		{"claude-sonnet-4-5", 200_000},
		{"claude-sonnet-4-5-20250929", 200_000},
		{"claude-haiku-4-5", 200_000},
		{"claude-3-7-sonnet-20250219", 200_000},
		{"claude-3-haiku-20240307", 200_000},

		// OpenAI
		{"gpt-4o", 128_000},
		{"gpt-4o-mini", 128_000},
		{"gpt-4o-2024-11-20", 128_000},
		{"gpt-4-turbo", 128_000},
		{"gpt-4", 8_192},
		{"gpt-4-32k-0613", 32_000},
		{"gpt-3.5-turbo", 16_000},
		{"gpt-5", 400_000},
		{"gpt-5-pro", 400_000},

		// OpenAI reasoning
		{"o1", 200_000},
		{"o1-mini", 128_000},
		{"o1-preview", 200_000},
		{"o3", 200_000},
		{"o3-mini", 200_000},

		// DeepSeek
		{"deepseek-v3", 64_000},
		{"deepseek-r1", 64_000},
		{"deepseek-chat", 64_000},

		// Gemini
		{"gemini-1.5-pro", 2_000_000},
		{"gemini-1.5-flash", 1_000_000},
		{"gemini-2.0-flash", 2_000_000},
		{"gemini-2.5-pro", 2_000_000},

		// OpenRouter-style namespaced IDs — the prefix should be
		// stripped before substring matching.
		{"anthropic/claude-sonnet-4-5", 200_000},
		{"anthropic/claude-opus-4-1m", 1_000_000},
		{"openai/gpt-4o", 128_000},
		{"deepseek/deepseek-v3", 64_000},
		{"google/gemini-1.5-pro", 2_000_000},

		// Llama
		{"meta-llama/llama-3.1-70b", 128_000},
		{"llama-3.1-405b-instruct", 128_000},

		// Unknown → conservative default
		{"some-brand-new-model-2026", defaultContextWindow},
		{"random-id", defaultContextWindow},
		{"", defaultContextWindow},
	}
	for _, tc := range cases {
		t.Run(tc.model, func(t *testing.T) {
			got := ContextWindowFor(tc.model)
			if got != tc.want {
				t.Fatalf("ContextWindowFor(%q) = %d, want %d", tc.model, got, tc.want)
			}
		})
	}
}

func TestContextWindowFor_CaseInsensitive(t *testing.T) {
	if got := ContextWindowFor("Claude-Sonnet-4-5"); got != 200_000 {
		t.Fatalf("expected 200000 for Claude-Sonnet-4-5, got %d", got)
	}
	if got := ContextWindowFor("GPT-4O"); got != 128_000 {
		t.Fatalf("expected 128000 for GPT-4O, got %d", got)
	}
}

func TestContextWindowFor_TrimsWhitespace(t *testing.T) {
	if got := ContextWindowFor("  gpt-4o  "); got != 128_000 {
		t.Fatalf("expected whitespace to be trimmed, got %d", got)
	}
}
