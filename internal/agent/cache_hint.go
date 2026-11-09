package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strings"

	"github.com/blueberrycongee/wuu/internal/compact"
	"github.com/blueberrycongee/wuu/internal/providers"
)

const (
	volatileTurnUserRole = "user"
)

// buildCacheHint derives a light-weight, provider-agnostic cache hint
// from the current conversation state.
//
// Design goals:
//   - keep it conversation-shaped, not provider-shaped
//   - preserve a stable prefix across the tool loop of the current turn
//   - expose a turn-stable breakpoint through the latest visible user
//     message so provider adapters can cache intra-turn tool loops
//   - let a compacted summary become the new stable history root
//     without introducing a heavier session-part model
//
// The stable prefix is everything before the most recent visible user-role
// message in the request. Hidden request context is model-visible, but it is
// not user intent and must not move the volatile turn boundary.
//
// The turn prefix extends the cacheable boundary through that latest visible
// user message. It is not part of the cross-turn stable prefix, but it is
// valuable for providers with explicit cache markers because a single user
// turn can expand into multiple assistant/tool round-trips with the same
// leading user request.
//
// After compact rewrites history, the synthetic conversation summary at
// the front of the prompt becomes the best stable anchor we have. We
// treat that summary-bearing system message as part of the cache root so
// providers can keep reusing it across the remainder of the session.
//
// PromptCacheKey falls back to a hash derived from the stable history
// root. Thread-aware runners override it with the durable thread ID so
// OpenAI-compatible prompt-cache routing stays stable across compaction
// and ordinary turns, matching Codex's thread-scoped key design.
func buildCacheHint(messages []providers.ChatMessage) *providers.CacheHint {
	if len(messages) == 0 {
		return nil
	}

	systemCount := systemMessageCount(messages)
	lastUser := lastUserMessageIndex(messages)
	stablePrefixMessages := stablePrefixMessageCount(messages, systemCount, lastUser)
	turnPrefixMessages := turnPrefixMessageCount(messages, systemCount, lastUser, stablePrefixMessages)
	hasCompactSummary := leadingSystemHasCompactSummary(messages)

	hint := &providers.CacheHint{
		StableSystem:         systemCount > 0,
		StablePrefixMessages: stablePrefixMessages,
		TurnPrefixMessages:   turnPrefixMessages,
		HasCompactSummary:    hasCompactSummary,
	}
	hint.PromptCacheKey = buildPromptCacheKey(messages, systemCount, stablePrefixMessages)

	if !hint.StableSystem && hint.StablePrefixMessages == 0 && hint.TurnPrefixMessages == 0 && hint.PromptCacheKey == "" && !hint.HasCompactSummary {
		return nil
	}
	return hint
}

func applyPromptCacheKeyOverride(hint **providers.CacheHint, key string) {
	key = strings.TrimSpace(key)
	if key == "" {
		return
	}
	if *hint == nil {
		*hint = &providers.CacheHint{}
	}
	(*hint).PromptCacheKey = key
}

func systemMessageCount(messages []providers.ChatMessage) int {
	count := 0
	for _, msg := range messages {
		if !strings.EqualFold(msg.Role, "system") {
			break
		}
		count++
	}
	return count
}

func lastUserMessageIndex(messages []providers.ChatMessage) int {
	for i := len(messages) - 1; i >= 0; i-- {
		if !messages[i].Hidden && strings.EqualFold(messages[i].Role, volatileTurnUserRole) {
			return i
		}
	}
	return -1
}

func stablePrefixMessageCount(messages []providers.ChatMessage, systemCount, lastUser int) int {
	if lastUser < 0 {
		stable := len(messages) - systemCount
		if stable < 0 {
			return 0
		}
		return stable
	}
	stable := lastUser - systemCount
	if stable < 0 {
		return 0
	}
	return stable
}

func turnPrefixMessageCount(messages []providers.ChatMessage, systemCount, lastUser, stablePrefix int) int {
	if lastUser < 0 {
		return stablePrefix
	}
	turnPrefix := lastUser - systemCount + 1
	if turnPrefix < 0 {
		return 0
	}
	maxPrefix := len(messages) - systemCount
	if turnPrefix > maxPrefix {
		return maxPrefix
	}
	return turnPrefix
}

func leadingSystemHasCompactSummary(messages []providers.ChatMessage) bool {
	for _, msg := range messages {
		if !strings.EqualFold(msg.Role, "system") {
			break
		}
		if compact.IsConversationSummaryContent(msg.Content) {
			return true
		}
	}
	return false
}

func buildPromptCacheKey(messages []providers.ChatMessage, systemCount, stablePrefixMessages int) string {
	var b strings.Builder
	b.WriteString("wuu:v2\n")

	added := false
	for i := 0; i < systemCount && i < len(messages); i++ {
		writeCacheKeyMessage(&b, messages[i])
		added = true
	}
	if stablePrefixMessages > 0 {
		idx := systemCount
		if idx >= 0 && idx < len(messages) {
			writeCacheKeyMessage(&b, messages[idx])
			added = true
		}
	}
	if !added && len(messages) > 0 {
		writeCacheKeyMessage(&b, messages[0])
		added = true
	}
	if !added {
		return ""
	}

	sum := sha256.Sum256([]byte(b.String()))
	return "wuu-" + hex.EncodeToString(sum[:16])
}

func writeCacheKeyMessage(b *strings.Builder, msg providers.ChatMessage) {
	b.WriteString(strings.ToLower(strings.TrimSpace(msg.Role)))
	b.WriteByte('\n')
	b.WriteString(msg.Content)
	b.WriteByte('\n')
	if len(msg.ToolCalls) > 0 {
		for _, tc := range msg.ToolCalls {
			b.WriteString(tc.Name)
			b.WriteByte('\n')
			b.WriteString(tc.Arguments)
			b.WriteByte('\n')
		}
	}
	if msg.ToolCallID != "" {
		b.WriteString(msg.ToolCallID)
		b.WriteByte('\n')
	}
}

// cacheStablePrefixFingerprint computes a hash of the components that define
// the stable prompt-cache prefix: model, tool schemas, and system prompt.
// Used for cache-break telemetry: if the fingerprint changes between requests,
// we log which component changed.
func cacheStablePrefixFingerprint(model string, tools []providers.ToolDefinition, systemTexts []string) string {
	var b strings.Builder
	b.WriteString(strings.ToLower(strings.TrimSpace(model)))
	b.WriteByte('\n')

	// Hash tool names and schemas (order matters)
	for _, tool := range tools {
		b.WriteString(tool.Name)
		b.WriteByte('\n')
		// Include description and input schema for schema stability detection
		b.WriteString(tool.Description)
		b.WriteByte('\n')
		// Map iteration order is random in Go; sort the schema keys so the
		// fingerprint stays deterministic for identical tools.
		keys := make([]string, 0, len(tool.InputSchema))
		for k := range tool.InputSchema {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			b.WriteString(k)
			b.WriteByte('\n')
		}
	}

	// Hash system texts
	for _, text := range systemTexts {
		b.WriteString(text)
		b.WriteByte('\n')
	}

	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:])
}
