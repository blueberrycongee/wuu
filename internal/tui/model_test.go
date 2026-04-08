package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/providers"
	tea "github.com/charmbracelet/bubbletea"
)

func TestSubmitPromptFlow(t *testing.T) {
	m := NewModel(Config{
		Provider:   "test",
		Model:      "test-model",
		ConfigPath: "/tmp/.wuu.json",
		RunPrompt: func(_ctx context.Context, prompt string) (string, error) {
			return "answer to: " + prompt, nil
		},
	})
	m.width = 120
	m.height = 40
	m.relayout()

	m.input.SetValue("hello world")
	nextModel, cmd := m.submit(false)
	if cmd == nil {
		t.Fatal("expected async command from submit")
	}
	next := nextModel.(Model)
	if !next.pendingRequest {
		t.Fatal("expected pendingRequest=true after submit")
	}

	msg := cmd()
	afterModel, streamCmd := next.Update(msg)
	after := afterModel.(Model)
	if after.pendingRequest {
		t.Fatal("expected pendingRequest=false after model response")
	}
	if !after.streaming {
		t.Fatal("expected streaming=true before stream ticks")
	}

	for after.streaming {
		if streamCmd == nil {
			t.Fatal("expected stream tick command while streaming")
		}
		tick := streamCmd()
		afterModel, streamCmd = after.Update(tick)
		after = afterModel.(Model)
	}
	content := renderEntries(after.entries)
	if !strings.Contains(content, "USER\nhello world") {
		t.Fatalf("missing user entry: %s", content)
	}
	if !strings.Contains(content, "ASSISTANT\nanswer to: hello world") {
		t.Fatalf("missing assistant entry: %s", content)
	}
}

func TestJumpToBottomToggle(t *testing.T) {
	m := NewModel(Config{
		Provider:   "test",
		Model:      "test-model",
		ConfigPath: "/tmp/.wuu.json",
		RunPrompt: func(_ctx context.Context, prompt string) (string, error) {
			return prompt, nil
		},
	})
	m.width = 100
	m.height = 16
	for i := 0; i < 30; i++ {
		m.appendEntry("assistant", "line")
	}
	m.relayout()
	m.refreshViewport(true)

	if m.showJump {
		t.Fatal("expected no jump hint at bottom")
	}

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyPgUp})
	paged := updated.(Model)
	if !paged.showJump {
		t.Fatal("expected jump hint after page up")
	}

	updated, _ = paged.Update(tea.KeyMsg{Type: tea.KeyCtrlJ})
	jumped := updated.(Model)
	if jumped.showJump {
		t.Fatal("expected jump hint cleared after jump-to-bottom")
	}

	updated, _ = paged.Update(tea.MouseMsg{
		Action: tea.MouseActionPress,
		Button: tea.MouseButtonLeft,
		X:      4,
		Y:      paged.height - 1,
	})
	clicked := updated.(Model)
	if clicked.showJump {
		t.Fatal("expected jump hint cleared after mouse click")
	}
}

func TestRelayoutFitsWindow(t *testing.T) {
	m := NewModel(Config{
		Provider:   "test",
		Model:      "test-model",
		ConfigPath: "/tmp/.wuu.json",
		RunPrompt: func(_ctx context.Context, prompt string) (string, error) {
			return prompt, nil
		},
	})

	m.width = 80
	m.height = 24
	m.relayout()

	l := computeLayout(m.width, m.height, m.inputLines)
	borderH := 0
	if !l.Compact {
		borderH = 2
	}
	// Chat has no border, only input does.
	totalHeight := l.Header.Height + l.Footer.Height + l.Chat.Height + l.Input.Height + borderH
	if totalHeight > m.height {
		t.Fatalf("layout exceeds window height: used=%d window=%d", totalHeight, m.height)
	}

	// Chat has no border, viewport uses full width.
	if m.viewport.Width > m.width {
		t.Fatalf("layout exceeds window width: used=%d window=%d", m.viewport.Width, m.width)
	}
}

func TestMouseClickPositionsCursor(t *testing.T) {
	m := NewModel(Config{
		Provider:   "test",
		Model:      "test-model",
		ConfigPath: "/tmp/.wuu.json",
		RunPrompt: func(_ctx context.Context, prompt string) (string, error) {
			return prompt, nil
		},
	})
	m.width = 100
	m.height = 24
	m.relayout()

	m.input.SetValue("hello world")
	m.input.SetCursor(0) // cursor at start

	// Click at column 7 inside the input area.
	// Non-compact: border adds 1 col on left, prompt "> " adds 2 cols.
	// So to hit text column 4, click at X = 1 (border) + 2 (prompt) + 4 = 7.
	inputY := m.layout.Input.Y + 1 // +1 for top border in non-compact
	updated, _ := m.Update(tea.MouseMsg{
		Action: tea.MouseActionPress,
		Button: tea.MouseButtonLeft,
		X:      7,
		Y:      inputY,
	})
	after := updated.(Model)

	li := after.input.LineInfo()
	if li.CharOffset != 4 {
		t.Fatalf("expected cursor at column 4, got %d", li.CharOffset)
	}
}

func TestMouseClickPositionsCursorMultiLine(t *testing.T) {
	m := NewModel(Config{
		Provider:   "test",
		Model:      "test-model",
		ConfigPath: "/tmp/.wuu.json",
		RunPrompt: func(_ctx context.Context, prompt string) (string, error) {
			return prompt, nil
		},
	})
	m.width = 100
	m.height = 24
	m.relayout()

	m.input.SetValue("first line\nsecond line")
	m.input.CursorStart() // cursor at start of first line

	// Click on second line (row 1), column 3.
	borderOff := 1 // non-compact
	promptW := 2
	inputY := m.layout.Input.Y + borderOff + 1 // +1 for second row
	clickX := m.layout.Input.X + borderOff + promptW + 3

	updated, _ := m.Update(tea.MouseMsg{
		Action: tea.MouseActionPress,
		Button: tea.MouseButtonLeft,
		X:      clickX,
		Y:      inputY,
	})
	after := updated.(Model)

	if after.input.Line() != 1 {
		t.Fatalf("expected cursor on line 1, got %d", after.input.Line())
	}
	li := after.input.LineInfo()
	if li.CharOffset != 3 {
		t.Fatalf("expected cursor at column 3, got %d", li.CharOffset)
	}
}

func renderEntries(entries []transcriptEntry) string {
	var b strings.Builder
	for i, e := range entries {
		if i > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString(e.Role)
		b.WriteString("\n")
		b.WriteString(e.Content)
	}
	return b.String()
}

func TestRenderThinkingBlock_Active(t *testing.T) {
	result := renderThinkingBlock("analyzing...", false, false, 2*time.Second, 80, 0)
	if !strings.Contains(result, "Thinking...") {
		t.Fatalf("expected 'Thinking...' in output: %s", result)
	}
	if !strings.Contains(result, "2.0s") {
		t.Fatalf("expected elapsed time in output: %s", result)
	}
}

func TestRenderThinkingBlock_Done_Collapsed(t *testing.T) {
	result := renderThinkingBlock("analyzed the code", true, false, 3200*time.Millisecond, 80, 0)
	if !strings.Contains(result, "Thought for 3.2s") {
		t.Fatalf("expected 'Thought for 3.2s' in output: %s", result)
	}
	// Content should NOT be visible when not expanded.
	if strings.Contains(result, "analyzed the code") {
		t.Fatalf("content should be hidden when collapsed: %s", result)
	}
}

func TestRenderThinkingBlock_Done_Expanded(t *testing.T) {
	result := renderThinkingBlock("analyzed the code", true, true, 3200*time.Millisecond, 80, 0)
	if !strings.Contains(result, "Thought for 3.2s") {
		t.Fatalf("expected 'Thought for 3.2s' in output: %s", result)
	}
	if !strings.Contains(result, "analyzed the code") {
		t.Fatalf("content should be visible when expanded: %s", result)
	}
}

func TestRenderToolCard_Running(t *testing.T) {
	tc := ToolCallEntry{
		Name:   "run_shell",
		Args:   `{"cmd":"go build ./..."}`,
		Status: ToolCallRunning,
	}
	result := renderToolCard(tc, 80)
	if !strings.Contains(result, "run_shell") {
		t.Fatalf("expected tool name in output: %s", result)
	}
	if !strings.Contains(result, "running") {
		t.Fatalf("expected running status: %s", result)
	}
}

func TestNewModel_RequestTimeout(t *testing.T) {
	timeout := 2 * time.Second
	m := NewModel(Config{
		Provider:       "test",
		Model:          "test-model",
		ConfigPath:     "/tmp/.wuu.json",
		RequestTimeout: timeout,
		RunPrompt: func(_ctx context.Context, prompt string) (string, error) {
			return prompt, nil
		},
	})

	if m.requestTimeout != timeout {
		t.Fatalf("expected requestTimeout=%s, got %s", timeout, m.requestTimeout)
	}
}

func TestRenderToolCard_Done_Collapsed(t *testing.T) {
	tc := ToolCallEntry{
		Name:      "read_file",
		Args:      `{"path":"model.go"}`,
		Result:    "48 lines read",
		Status:    ToolCallDone,
		Collapsed: true,
	}
	result := renderToolCard(tc, 80)
	if !strings.Contains(result, "read_file") {
		t.Fatalf("expected tool name: %s", result)
	}
	if !strings.Contains(result, "done") {
		t.Fatalf("expected done status: %s", result)
	}
}

func TestRenderToolCard_Error(t *testing.T) {
	tc := ToolCallEntry{
		Name:   "run_shell",
		Status: ToolCallError,
	}
	result := renderToolCard(tc, 80)
	if !strings.Contains(result, "error") {
		t.Fatalf("expected error status: %s", result)
	}
}

func TestFormatUserEntryContent_WithImages(t *testing.T) {
	got := formatUserEntryContent("show me", 2)
	want := "show me\n[Image #1]\n[Image #2]"
	if got != want {
		t.Fatalf("formatUserEntryContent() = %q, want %q", got, want)
	}
}

func TestSubmit_ImageRequiresStreamingMode(t *testing.T) {
	m := NewModel(Config{
		Provider:   "test",
		Model:      "test-model",
		ConfigPath: "/tmp/.wuu.json",
		RunPrompt: func(_ctx context.Context, prompt string) (string, error) {
			return "answer to: " + prompt, nil
		},
	})
	m.pendingImages = []providers.InputImage{
		{MediaType: "image/png", Data: "AAA"},
	}

	nextModel, cmd := m.submit(false)
	if cmd != nil {
		t.Fatal("expected no command when image submit is unsupported")
	}
	next := nextModel.(Model)
	if next.statusLine != "image paste requires streaming mode" {
		t.Fatalf("unexpected status line: %q", next.statusLine)
	}
	if len(next.pendingImages) != 1 {
		t.Fatalf("expected pending image to remain, got %d", len(next.pendingImages))
	}
}

func TestStripUserImagePlaceholderLines(t *testing.T) {
	input := "please review\n[Image #1]\n[Image #2]"
	got := stripUserImagePlaceholderLines(input)
	if got != "please review" {
		t.Fatalf("stripUserImagePlaceholderLines() = %q", got)
	}
}
