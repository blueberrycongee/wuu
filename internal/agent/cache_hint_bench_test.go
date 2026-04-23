package agent

import (
	"testing"

	"github.com/blueberrycongee/wuu/internal/providers"
)

func BenchmarkBuildCacheHint(b *testing.B) {
	msgs := make([]providers.ChatMessage, 100)
	msgs[0] = providers.ChatMessage{Role: "system", Content: "You are wuu, a pragmatic CLI coding assistant."}
	for i := 1; i < len(msgs); i++ {
		if i%3 == 1 {
			msgs[i] = providers.ChatMessage{Role: "user", Content: "Do something"}
		} else if i%3 == 2 {
			msgs[i] = providers.ChatMessage{Role: "assistant", Content: "Here is the result."}
		} else {
			msgs[i] = providers.ChatMessage{Role: "tool", Name: "read_file", Content: "package main\n\nfunc main() {}"}
		}
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = buildCacheHint(msgs)
	}
}
