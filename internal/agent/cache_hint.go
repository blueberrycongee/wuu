package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"

	"github.com/blueberrycongee/wuu/internal/providers"
)

const volatileTurnUserRole = "user"

// buildCacheHint derives a light-weight, provider-agnostic cache hint
// from the current conversation state.
//
// Design goals:
//   - keep it conversation-shaped, not provider-shaped
//   - preserve a stable prefix across the tool loop of the current turn
//   - avoid coupling cache behavior to TUI/session plumbing
//
// The stable prefix is everything before the most recent user message.
// That makes the whole current turn (latest user prompt plus any
// assistant tool calls / tool results produced while answering it)
// intentionally volatile, while older context remains cache-friendly.
//
// PromptCacheKey is a conversation-scoped hash derived from the
// earliest stable anchors (system prompt + first non-system message).
// It stays stable across ordinary turns without needing a separate
// session-ID plumbed through the stack.
func buildCacheHint(messages []providers.ChatMessage) *providers.CacheHint {
	if len(messages) == 0 {
		return nil
	}

	hint := &providers.CacheHint{
		StableSystem: systemMessageCount(messages) > 0,
	}
	lastUser := lastUserMessageIndex(messages)
	stablePrefixMessages := lastUser - systemMessageCount(messages)
	if stablePrefixMessages > 0 {
		hint.StablePrefixMessages = stablePrefixMessages
	}
	hint.PromptCacheKey = buildPromptCacheKey(messages)

	if !hint.StableSystem && hint.StablePrefixMessages == 0 && hint.PromptCacheKey == "" {
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

func buildPromptCacheKey(messages []providers.ChatMessage) string {
	var b strings.Builder
	b.WriteString("wuu:v1\n")

	added := false
	for _, msg := range messages {
		if strings.EqualFold(msg.Role, "system") {
			writeCacheKeyMessage(&b, msg)
			added = true
			continue
		}
		writeCacheKeyMessage(&b, msg)
		added = true
		break
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
