package providers

import (
	"strings"
)

// ContextWindowFor returns the context window size in tokens for the
// given model identifier. The lookup is provider-agnostic — it
// inspects the model name string only, so the same registry serves
// direct Anthropic / direct OpenAI / OpenRouter / third-party proxies
// equally well as long as they don't rewrite model names.
//
// Resolution order:
//   1. Exact match against the registry (cheapest, used for canonical IDs).
//   2. Substring match in the order entries are listed (most specific
//      patterns first), so e.g. "gpt-4o-mini" matches the "gpt-4o" rule
//      and "claude-opus-4-1m" matches the "claude-opus-4-1m" rule
//      before falling through to the generic "claude-opus".
//   3. defaultContextWindow if nothing matched.
//
// The registry intentionally errs on the side of UNDERREPORTING (a
// smaller window than the model actually has) for unknown variants,
// so the proactive auto-compact triggers a bit early instead of
// missing the threshold and only catching the failure reactively via
// providers.IsContextOverflow.
func ContextWindowFor(model string) int {
	if model == "" {
		return defaultContextWindow
	}
	lower := strings.ToLower(strings.TrimSpace(model))
	// OpenRouter and similar gateways prefix model names with the
	// upstream vendor: "anthropic/claude-sonnet-4-5". Strip the prefix
	// for matching but keep the original around for exact lookups.
	stripped := lower
	if idx := strings.LastIndex(lower, "/"); idx >= 0 {
		stripped = lower[idx+1:]
	}

	for _, entry := range contextWindowRegistry {
		// Patterns are stored lowercased; case-insensitive substring
		// match against either the full or stripped model id.
		if strings.Contains(lower, entry.pattern) || strings.Contains(stripped, entry.pattern) {
			return entry.size
		}
	}
	return defaultContextWindow
}

// defaultContextWindow is the fallback when no registry entry matches.
// 64k is small enough to make us conservative (early auto-compact) on
// unknown models while still being generous enough to feel reasonable
// for typical "I'm using a brand new model" first-run experience.
const defaultContextWindow = 64_000

type contextWindowEntry struct {
	pattern string // lowercase substring match
	size    int    // tokens
}

// contextWindowRegistry is the model → window lookup table. Order
// matters: more specific patterns must come before more generic ones,
// since ContextWindowFor returns the first match.
//
// When adding new entries, prefer the largest verifiable window for
// the family — the proactive compact threshold is a percentage so
// being slightly generous here is fine; being wrong by more than 2x
// is what hurts.
var contextWindowRegistry = []contextWindowEntry{
	// --- Anthropic Claude 4.x ---------------------------------------
	// Long-context Opus variant must come BEFORE the generic
	// "claude-opus-4" rule.
	{"claude-opus-4-6-1m", 1_000_000},
	{"claude-opus-4-1m", 1_000_000},
	{"claude-opus-4-6", 200_000},
	{"claude-opus-4", 200_000},
	{"claude-sonnet-4-6", 200_000},
	{"claude-sonnet-4-5", 200_000},
	{"claude-sonnet-4", 200_000},
	{"claude-haiku-4-5", 200_000},
	{"claude-haiku-4", 200_000},

	// --- Anthropic Claude 3.x ---------------------------------------
	{"claude-3-7-sonnet", 200_000},
	{"claude-3-5-sonnet", 200_000},
	{"claude-3-5-haiku", 200_000},
	{"claude-3-opus", 200_000},
	{"claude-3-sonnet", 200_000},
	{"claude-3-haiku", 200_000},

	// Generic catch-alls for any other Claude variant we forgot.
	{"claude-opus", 200_000},
	{"claude-sonnet", 200_000},
	{"claude-haiku", 200_000},
	{"claude", 200_000},

	// --- OpenAI GPT-5 -----------------------------------------------
	{"gpt-5", 400_000},

	// --- OpenAI GPT-4 -----------------------------------------------
	{"gpt-4o-mini", 128_000},
	{"gpt-4o", 128_000},
	{"gpt-4-turbo", 128_000},
	{"gpt-4.1", 1_000_000},
	{"gpt-4-32k", 32_000},
	{"gpt-4", 8_192},

	// --- OpenAI o-series reasoning ----------------------------------
	{"o3-mini", 200_000},
	{"o3", 200_000},
	{"o1-mini", 128_000},
	{"o1", 200_000},

	// --- OpenAI GPT-3.5 ---------------------------------------------
	{"gpt-3.5-turbo-16k", 16_000},
	{"gpt-3.5-turbo", 16_000},

	// --- DeepSeek ---------------------------------------------------
	{"deepseek-v3", 64_000},
	{"deepseek-r1", 64_000},
	{"deepseek-coder", 64_000},
	{"deepseek-chat", 64_000},
	{"deepseek", 64_000},

	// --- Google Gemini ----------------------------------------------
	{"gemini-2.5-pro", 2_000_000},
	{"gemini-2.0", 2_000_000},
	{"gemini-2", 2_000_000},
	{"gemini-1.5-pro", 2_000_000},
	{"gemini-1.5-flash", 1_000_000},
	{"gemini-1.5", 1_000_000},
	{"gemini-1", 32_000},
	{"gemini", 32_000},

	// --- Mistral ----------------------------------------------------
	{"mistral-large", 128_000},
	{"mistral-medium", 32_000},
	{"mistral-small", 32_000},
	{"mistral", 32_000},

	// --- Meta Llama -------------------------------------------------
	{"llama-3.3", 128_000},
	{"llama-3.2", 128_000},
	{"llama-3.1-405b", 128_000},
	{"llama-3.1-70b", 128_000},
	{"llama-3.1", 128_000},
	{"llama-3", 8_192},
	{"llama", 8_192},

	// --- Qwen -------------------------------------------------------
	{"qwen3", 128_000},
	{"qwen2.5", 128_000},
	{"qwen", 32_000},
}
