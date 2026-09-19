package providers

import (
	"strings"
)

// KnownContextWindowFor returns the context window size in tokens for a model
// only when wuu has explicit model metadata for it.
//
// Resolution order:
//
//  1. Wuu's exact local corrections for provider docs that are newer
//     or more precise than the embedded catalog snapshot.
//  2. Catwalk's curated, community-maintained model index (loaded
//     from charm.land/catwalk's embedded snapshot at process start;
//     a future remote sync path will refresh it without restart).
//     This covers the broadest set of models with the freshest data.
//  3. Wuu's hand-rolled substring registry below — kept as a robust
//     fallback for models catwalk hasn't shipped yet, plus generic
//     family rules ("claude-sonnet" → 200k) that catch any vendor's
//     proxy-renamed variant.
//
// If neither source recognizes the model, ok is false. Agent runtimes use this
// path for proactive auto-compact so BYOK models with unknown limits do not get
// silently treated as 64k-context models.
func KnownContextWindowFor(model string) (window int, ok bool) {
	if model == "" {
		return 0, false
	}

	lower := strings.ToLower(strings.TrimSpace(model))
	// OpenRouter and similar gateways prefix model names with the
	// upstream vendor: "anthropic/claude-sonnet-4-5". Strip the prefix
	// for matching but keep the original around for exact lookups.
	stripped := lower
	if idx := strings.LastIndex(lower, "/"); idx >= 0 {
		stripped = lower[idx+1:]
	}

	// Layer 0: exact local corrections for provider docs that are newer
	// or more precise than the embedded catalog snapshot.
	if window, ok := exactContextWindowOverride(lower, stripped); ok {
		return window, true
	}

	// Layer 1: catwalk curated index.
	if w := catwalkLookup(model); w > 0 {
		return w, true
	}

	// Layer 2: wuu's hand-rolled substring registry.
	for _, entry := range contextWindowRegistry {
		// Patterns are stored lowercased; case-insensitive substring
		// match against either the full or stripped model id.
		if strings.Contains(lower, entry.pattern) || strings.Contains(stripped, entry.pattern) {
			return entry.size, true
		}
	}
	return 0, false
}

// ContextWindowFor returns the context window size in tokens for the given model
// identifier. Unknown models fall back to defaultContextWindow for legacy callers
// and display code. Runtime proactive auto-compact should use
// KnownContextWindowFor instead so unknown BYOK models remain reactive-only.
func ContextWindowFor(model string) int {
	if w, ok := KnownContextWindowFor(model); ok {
		return w
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

func exactContextWindowOverride(lower, stripped string) (int, bool) {
	for _, entry := range exactContextWindowOverrides {
		if lower == entry.pattern || stripped == entry.pattern {
			return entry.size, true
		}
	}
	return 0, false
}

var exactContextWindowOverrides = []contextWindowEntry{
	// MiniMax official docs list MiniMax-M3 at 1M context and M2-series
	// text models at 204,800. Keep these exact overrides ahead of the
	// embedded catalog because older catalog snapshots have reported
	// MiniMax-M3 as 200k.
	{"minimax-m3", 1_000_000},
	{"minimax-m2.7-highspeed", 204_800},
	{"minimax-m2.7", 204_800},
	{"minimax-m2.5-highspeed", 204_800},
	{"minimax-m2.5", 204_800},
	{"minimax-m2.1-highspeed", 204_800},
	{"minimax-m2.1", 204_800},
	{"minimax-m2", 204_800},
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
	// catwalk holds the precise per-id windows (e.g. claude-opus-4-6
	// is actually 1M, not 200k); these entries are only the fallback
	// when catwalk doesn't recognize a name.
	{"claude-opus-4", 200_000},
	{"claude-sonnet-4", 200_000},
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

	// --- OpenAI GPT-6 -----------------------------------------------
	{"gpt-6", 1_050_000},

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
	{"deepseek-v4", 1_000_000},
	{"deepseek-flash", 1_000_000},
	{"deepseek-v3", 64_000},
	{"deepseek-r1", 64_000},
	{"deepseek-coder", 64_000},
	{"deepseek-chat", 64_000},
	{"deepseek", 64_000},

	// --- GLM --------------------------------------------------------
	{"glm-5.2", 1_000_000},
	{"glm-5.3", 1_000_000},

	// --- xAI Grok ---------------------------------------------------
	{"grok-4", 500_000},

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
	{"qwen3.8", 1_000_000},
	{"qwen3.7", 1_000_000},
	{"qwen3.6", 1_000_000},
	{"qwen3.5", 1_000_000},
	{"qwen3", 128_000},
	{"qwen2.5", 128_000},
	{"qwen", 32_000},
}

// MaxOutputTokensFor returns the default max output tokens for the given model.
func MaxOutputTokensFor(model string) int {
	if model == "" {
		return defaultMaxOutputTokens
	}
	lower := strings.ToLower(strings.TrimSpace(model))
	stripped := lower
	if idx := strings.LastIndex(lower, "/"); idx >= 0 {
		stripped = lower[idx+1:]
	}
	for _, entry := range maxOutputTokensRegistry {
		if strings.Contains(lower, entry.pattern) || strings.Contains(stripped, entry.pattern) {
			return entry.size
		}
	}
	return defaultMaxOutputTokens
}

const defaultMaxOutputTokens = 16_000

var maxOutputTokensRegistry = []contextWindowEntry{
	// Anthropic Claude 4.6.
	// Start at 16K; providers can override this for models with larger
	// documented output windows.
	{"opus-4-6", 16_000},
	{"sonnet-4-6", 16_000},
	// Anthropic Claude 4.x
	{"opus-4", 32_000},
	{"sonnet-4", 32_000},
	{"haiku-4", 32_000},
	// Anthropic Claude 3.7
	{"claude-3-7-sonnet", 32_000},
	// Anthropic Claude 3.5
	{"claude-3-5-sonnet", 8_192},
	{"claude-3-5-haiku", 8_192},
	// Anthropic Claude 3
	{"claude-3-opus", 4_096},
	{"claude-3-sonnet", 8_192},
	{"claude-3-haiku", 4_096},
	// Generic Claude fallback
	{"claude", 16_000},
	// OpenAI
	{"gpt-6", 128_000},
	{"gpt-5", 32_000},
	{"gpt-4o", 16_384},
	{"gpt-4.1", 32_768},
	{"gpt-4-turbo", 4_096},
	{"gpt-4", 4_096},
	{"o3", 100_000},
	{"o1", 32_768},
	// DeepSeek
	{"deepseek-v4", 384_000},
	{"deepseek-flash", 384_000},
	{"deepseek", 8_192},
	// GLM
	{"glm-5.2", 128_000},
	{"glm-5.3", 131_072},
	// xAI documents 128K as Grok 4.6's default request generation budget,
	// not as a fixed model-level output ceiling.
	{"grok-4.6", 128_000},
	// MiniMax-M3 documents 128K as the recommended Chat Completions output
	// cap, with larger values allowed by explicit request.
	{"minimax-m3", 131_072},
	// Gemini
	{"gemini-2.5-pro", 65_536},
	{"gemini-2", 8_192},
	{"gemini", 8_192},
}
