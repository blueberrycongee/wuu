package compact

import (
	"fmt"
	"strings"
	"testing"

	"github.com/blueberrycongee/wuu/internal/providers"
)

// makeHistory builds a synthetic history for token-estimation benches.
func makeHistory(nMsgs, contentBytes int) []providers.ChatMessage {
	msgs := make([]providers.ChatMessage, 0, nMsgs)
	body := strings.Repeat("The quick brown fox jumps over the lazy dog. ", contentBytes/45+1)
	for i := 0; i < nMsgs; i++ {
		role := "assistant"
		if i%2 == 0 {
			role = "user"
		}
		m := providers.ChatMessage{
			Role:    role,
			Content: body[:contentBytes],
		}
		if i%3 == 0 {
			m.ToolCalls = []providers.ToolCall{
				{
					ID:        fmt.Sprintf("tc_%d", i),
					Name:      "run_shell",
					Arguments: `{"command":"echo hello","cwd":"/tmp"}`,
				},
			}
		}
		msgs = append(msgs, m)
	}
	return msgs
}

// BenchmarkEstimateMessagesTokens measures the per-turn cost of the
// proactive-compact pre-check. Called every agent loop iteration.
func BenchmarkEstimateMessagesTokens(b *testing.B) {
	for _, shape := range []struct {
		name       string
		nMsgs, len int
	}{
		{"50msg_1KB", 50, 1024},
		{"100msg_5KB", 100, 5 * 1024},
		{"200msg_10KB", 200, 10 * 1024},
	} {
		msgs := makeHistory(shape.nMsgs, shape.len)
		b.Run(shape.name, func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_ = EstimateMessagesTokens(msgs)
			}
		})
	}
}

// BenchmarkEstimateMessagesTokens_Simple measures the bare cost for
// uniform short messages (no tool calls, no mixed roles).
func BenchmarkEstimateMessagesTokens_Simple(b *testing.B) {
	for _, n := range []int{10, 100, 500} {
		msgs := make([]providers.ChatMessage, n)
		for i := range msgs {
			msgs[i] = providers.ChatMessage{Role: "user", Content: "Short message here."}
		}
		b.Run(fmt.Sprintf("%dmsgs", n), func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_ = EstimateMessagesTokens(msgs)
			}
		})
	}
}
