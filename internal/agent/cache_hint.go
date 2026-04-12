package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"

	"github.com/blueberrycongee/wuu/internal/providers"
)

const (
	volatileTurnUserRole    = "user"
	conversationSummaryHead = "[Conversation summary]"
)

// buildCacheHint derives a light-weight, provider-agnostic cache hint
// from the current conversation state.
//
// Design goals:
//   - keep it conversation-shaped, not provider-shaped
//   - preserve a stable prefix across the tool loop of the current turn
//   - let a compacted summary become the new stable history root
//     without introducing a heavier session-part model
//
// The stable prefix is everything before the most recent user message.
// That makes the whole current turn (latest user prompt plus any
// assistant tool calls / tool results produced while answering it)
// intentionally volatile, while older context remains cache-friendly.
//
// After compact rewrites history, the synthetic conversation summary at
// the front of the prompt becomes the best stable anchor we have. We
// treat that summary-bearing system message as part of the cache root so
// providers can keep reusing it across the remainder of the session.
//
// PromptCacheKey is a conversation-scoped hash derived from the stable
// history root (system prompt, including a compact summary when present,
// plus the first stable non-system message when available). That keeps
// the key stable across ordinary turns, while still rotating when
// compaction rewrites the durable prefix.
func buildCacheHint(messages []providers.ChatMessage) *providers.CacheHint {
	if len(messages) == 0 {
		return nil
	}

	systemCount := systemMessageCount(messages)
	lastUser := lastUserMessageIndex(messages)
	stablePrefixMessages := stablePrefixMessageCount(messages, systemCount, lastUser)
	hasCompactSummary := leadingSystemHasCompactSummary(messages)

	hint := &providers.CacheHint{
		StableSystem:         systemCount > 0,
		StablePrefixMessages: stablePrefixMessages,
		HasCompactSummary:    hasCompactSummary,
	}
	hint.PromptCacheKey = buildPromptCacheKey(messages, systemCount, stablePrefixMessages)

	if !hint.StableSystem && hint.StablePrefixMessages == 0 && hint.PromptCacheKey == "" && !hint.HasCompactSummary {
		return nil
	}
	return hint
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
		if strings.EqualFold(messages[i].Role, volatileTurnUserRole) {
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

func leadingSystemHasCompactSummary(messages []providers.ChatMessage) bool {
	if len(messages) == 0 || !strings.EqualFold(messages[0].Role, "system") {
		return false
	}
	return strings.HasPrefix(strings.TrimSpace(messages[0].Content), conversationSummaryHead)
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
