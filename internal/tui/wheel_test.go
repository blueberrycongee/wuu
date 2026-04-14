package tui

import (
	"testing"

	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/providers"
	tea "github.com/charmbracelet/bubbletea"
)

// TestMouseWheelUp_DuringStreamingWithQueuedEvents is a regression test for
// the bug where scrolling up while streaming is active (and new stream
// events are queued) would actually move the viewport DOWN instead of up.
//
// Root cause: the tea.MouseMsg handler drains queued stream events at its
// very first line. When autoFollow is true, each drained tool/content
// event causes refreshViewport to call GotoBottom over the newly-grown
// content. Only AFTER the drain does the wheel delta get applied, so
// the net movement is downward even though the user asked for upward.
func TestMouseWheelUp_DuringStreamingWithQueuedEvents(t *testing.T) {
	m := NewModel(Config{
		Provider:   "test",
		Model:      "test-model",
		ConfigPath: "/tmp/.wuu.json",
		StreamRunner: &agent.StreamRunner{
			Client: &echoStreamClient{answer: func(msgs []providers.ChatMessage) string { return msgs[len(msgs)-1].Content }},
			Model:  "test-model",
		},
	})
	m.width = 100
	m.height = 20

	// Seed enough tool cards so the viewport is scrolled (autoFollow true).
	idx := m.appendEntry("assistant", "running tools")
	for i := 0; i < 40; i++ {
		m.entries[idx].ToolCalls = append(m.entries[idx].ToolCalls, ToolCallEntry{
			Name:      "list_files",
			Args:      `{"path":"thirdparty"}`,
			Result:    "ok",
			Status:    ToolCallDone,
			Collapsed: true,
		})
	}
	m.relayout()
	m.refreshViewport(true)

	// Simulate active streaming with several queued tool events pending.
	m.streaming = true
	m.streamCh = make(chan providers.StreamEvent, 10)
	for i := 0; i < 5; i++ {
		m.streamCh <- providers.StreamEvent{
			Type: providers.EventToolUseStart,
			ToolCall: &providers.ToolCall{
				Name: "list_files",
				ID:   "t" + string(rune('0'+i)),
			},
		}
	}

	initialOffset := m.viewport.YOffset
	if !m.autoFollow {
		t.Fatalf("setup error: autoFollow should be true when pinned to bottom")
	}

	// User scrolls wheel UP in the middle of the chat viewport.
	updated, _ := m.Update(tea.MouseMsg{
		Action: tea.MouseActionPress,
		Button: tea.MouseButtonWheelUp,
		X:      m.layout.Chat.X + 10,
		Y:      m.layout.Chat.Y + m.layout.Chat.Height/2,
	})
	after := updated.(Model)

	// After a wheel-up, YOffset MUST be strictly less than it was before.
	// Buggy behavior: drain ran first, GotoBottom-ed over newly-added
	// content, wheel delta applied from a higher YOffset — net increase.
	if after.viewport.YOffset >= initialOffset {
		t.Fatalf("wheel up moved viewport the wrong way during streaming: before=%d after=%d",
			initialOffset, after.viewport.YOffset)
	}

	// autoFollow must be false after a user-initiated upward scroll;
	// otherwise subsequent refreshes yank the user back to the bottom.
	if after.autoFollow {
		t.Fatalf("autoFollow should be cleared after user scrolls up; got autoFollow=true")
	}
}
