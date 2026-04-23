package agent

import (
	"testing"

	"github.com/blueberrycongee/wuu/internal/providers"
)

func BenchmarkFilterEphemeralHistory(b *testing.B) {
	msgs := []providers.ChatMessage{
		{Role: "system", Content: "You are wuu."},
		{Role: "user", Content: "Hello"},
		{Role: "assistant", Content: "Hi there"},
		{Role: "user", Name: "wuu_system_reminder", Content: "<system-reminder>\n# Environment\n- CWD: /tmp\n</system-reminder>"},
		{Role: "assistant", Content: "I see"},
		{Role: "user", Content: "Do something"},
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = filterEphemeralHistory(msgs)
	}
}
