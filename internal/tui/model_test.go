package tui

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/agent"
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
		X:      paged.width - 5,
		Y:      0,
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

	l := computeLayout(m.width, m.height, m.inputLines, 0)
	totalHeight := l.Header.Height + l.Chat.Height + l.Input.Height
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

	// Click inside the input area.
	// Prompt "> " adds 2 cols.
	// To hit text column 4, click at X = 2 (prompt) + 4 = 6.
	inputY := m.layout.Input.Y
	updated, _ := m.Update(tea.MouseMsg{
		Action: tea.MouseActionPress,
		Button: tea.MouseButtonLeft,
		X:      6,
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
	promptW := 2
	inputY := m.layout.Input.Y + 1 // +1 for second row
	clickX := m.layout.Input.X + promptW + 3

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

func TestMouseDragScrollbarThumbTracksMotion(t *testing.T) {
	m := newScrollableModelForScrollbarTest(t)

	thumbPos, thumbSize, _, _, ok := scrollbarThumbGeometry(
		m.layout.Chat.Height,
		m.viewport.TotalLineCount(),
		m.viewport.Height,
		m.viewport.YOffset,
	)
	if !ok {
		t.Fatal("expected visible scrollbar thumb")
	}
	if thumbSize < 1 {
		t.Fatalf("expected thumb size >= 1, got %d", thumbSize)
	}

	clickX := m.layout.Chat.X + m.layout.Chat.Width - 1
	pressY := m.layout.Chat.Y + thumbPos

	updated, _ := m.Update(tea.MouseMsg{
		Action: tea.MouseActionPress,
		Button: tea.MouseButtonLeft,
		X:      clickX,
		Y:      pressY,
	})
	dragging := updated.(Model)
	if !dragging.scrollbarDragging {
		t.Fatal("expected scrollbarDragging=true after pressing thumb")
	}

	motionY := dragging.layout.Chat.Y + dragging.layout.Chat.Height - 1
	updated, _ = dragging.Update(tea.MouseMsg{
		Action: tea.MouseActionMotion,
		Button: tea.MouseButtonLeft,
		// Simulate fast drag: pointer can drift away from scrollbar column.
		X: dragging.layout.Chat.X,
		Y: motionY,
	})
	afterMotion := updated.(Model)
	maxOffset := max(0, afterMotion.viewport.TotalLineCount()-afterMotion.viewport.Height)
	if afterMotion.viewport.YOffset != maxOffset {
		t.Fatalf("expected viewport offset %d after drag motion, got %d", maxOffset, afterMotion.viewport.YOffset)
	}
	if !afterMotion.scrollbarDragging {
		t.Fatal("expected scrollbarDragging=true during drag")
	}

	updated, _ = afterMotion.Update(tea.MouseMsg{
		Action: tea.MouseActionRelease,
		Button: tea.MouseButtonLeft,
		X:      clickX,
		Y:      motionY,
	})
	afterRelease := updated.(Model)
	if afterRelease.scrollbarDragging {
		t.Fatal("expected scrollbarDragging=false after release")
	}
}

func TestMouseClickScrollbarTrackJumpsProportionally(t *testing.T) {
	m := newScrollableModelForScrollbarTest(t)

	thumbPos, thumbSize, _, _, ok := scrollbarThumbGeometry(
		m.layout.Chat.Height,
		m.viewport.TotalLineCount(),
		m.viewport.Height,
		m.viewport.YOffset,
	)
	if !ok {
		t.Fatal("expected visible scrollbar thumb")
	}

	row := thumbPos + thumbSize
	if row >= m.layout.Chat.Height {
		row = thumbPos - 1
	}
	if row < 0 || row >= m.layout.Chat.Height {
		t.Fatalf("failed to choose a track row outside thumb: row=%d", row)
	}

	clickX := m.layout.Chat.X + m.layout.Chat.Width - 1
	clickY := m.layout.Chat.Y + row
	updated, _ := m.Update(tea.MouseMsg{
		Action: tea.MouseActionPress,
		Button: tea.MouseButtonLeft,
		X:      clickX,
		Y:      clickY,
	})
	after := updated.(Model)
	if after.scrollbarDragging {
		t.Fatal("expected scrollbarDragging=false after track click")
	}

	maxOffset := max(0, after.viewport.TotalLineCount()-after.viewport.Height)
	_, afterThumbSize, afterTrackSpace, _, ok := scrollbarThumbGeometry(
		after.layout.Chat.Height,
		after.viewport.TotalLineCount(),
		after.viewport.Height,
		after.viewport.YOffset,
	)
	if !ok {
		t.Fatal("expected visible scrollbar geometry after track click")
	}
	targetThumbPos := row - afterThumbSize/2
	if targetThumbPos < 0 {
		targetThumbPos = 0
	} else if targetThumbPos > afterTrackSpace {
		targetThumbPos = afterTrackSpace
	}
	want := scrollbarOffsetForThumbPos(targetThumbPos, afterTrackSpace, maxOffset)
	if after.viewport.YOffset != want {
		t.Fatalf("expected viewport offset %d after track click, got %d", want, after.viewport.YOffset)
	}
}

func TestMouseDragScrollbarReanchorsWhenThumbGeometryChanges(t *testing.T) {
	m := newScrollableModelForScrollbarTest(t)
	maxOffset := max(0, m.viewport.TotalLineCount()-m.viewport.Height)
	m.setViewportOffset(maxOffset / 2)

	thumbPos, _, _, _, ok := scrollbarThumbGeometry(
		m.layout.Chat.Height,
		m.viewport.TotalLineCount(),
		m.viewport.Height,
		m.viewport.YOffset,
	)
	if !ok {
		t.Fatal("expected visible scrollbar thumb")
	}
	clickX := m.layout.Chat.X + m.layout.Chat.Width - 1
	pressY := m.layout.Chat.Y + thumbPos
	updated, _ := m.Update(tea.MouseMsg{
		Action: tea.MouseActionPress,
		Button: tea.MouseButtonLeft,
		X:      clickX,
		Y:      pressY,
	})
	dragging := updated.(Model)
	if !dragging.scrollbarDragging {
		t.Fatal("expected scrollbarDragging=true after thumb press")
	}
	offsetBeforeGrowth := dragging.viewport.YOffset

	dragging.appendEntry("ASSISTANT", strings.Repeat("new line\n", 120)+"end")
	dragging.refreshViewport(false)
	updated, _ = dragging.Update(tea.MouseMsg{
		Action: tea.MouseActionMotion,
		Button: tea.MouseButtonLeft,
		X:      clickX,
		Y:      pressY,
	})
	after := updated.(Model)

	diff := after.viewport.YOffset - offsetBeforeGrowth
	if diff < 0 {
		diff = -diff
	}
	if diff > 1 {
		t.Fatalf("expected offset stable after geometry change with zero drag delta, got before=%d after=%d", offsetBeforeGrowth, after.viewport.YOffset)
	}
}

func TestMouseMotionScrollbarHoverState(t *testing.T) {
	m := newScrollableModelForScrollbarTest(t)
	rightX := m.layout.Chat.X + m.layout.Chat.Width - 1
	hoverRow := min(2, m.layout.Chat.Height-1)

	updated, _ := m.Update(tea.MouseMsg{
		Action: tea.MouseActionMotion,
		Button: tea.MouseButtonNone,
		X:      rightX,
		Y:      m.layout.Chat.Y + hoverRow,
	})
	hovered := updated.(Model)
	if !hovered.scrollbarHoverActive {
		t.Fatal("expected hover active while pointer is on scrollbar")
	}
	if hovered.scrollbarHoverRow != hoverRow {
		t.Fatalf("expected hover row %d, got %d", hoverRow, hovered.scrollbarHoverRow)
	}

	updated, _ = hovered.Update(tea.MouseMsg{
		Action: tea.MouseActionMotion,
		Button: tea.MouseButtonNone,
		X:      hovered.layout.Chat.X,
		Y:      hovered.layout.Chat.Y + hoverRow,
	})
	afterLeave := updated.(Model)
	if afterLeave.scrollbarHoverActive {
		t.Fatal("expected hover inactive after leaving scrollbar")
	}
	if afterLeave.scrollbarHoverRow != -1 {
		t.Fatalf("expected hover row reset to -1, got %d", afterLeave.scrollbarHoverRow)
	}
}

func TestMouseHoverScrollbarWithinHitboxTolerance(t *testing.T) {
	m := newScrollableModelForScrollbarTest(t)
	rightX := m.layout.Chat.X + m.layout.Chat.Width - 1
	leftToleranceX := rightX - 1
	if leftToleranceX < m.layout.Chat.X {
		leftToleranceX = m.layout.Chat.X
	}
	hoverY := m.layout.Chat.Y + min(2, m.layout.Chat.Height-1)

	updated, _ := m.Update(tea.MouseMsg{
		Action: tea.MouseActionMotion,
		Button: tea.MouseButtonNone,
		X:      leftToleranceX,
		Y:      hoverY,
	})
	after := updated.(Model)
	if !after.scrollbarHoverActive {
		t.Fatal("expected hover active inside scrollbar hitbox tolerance")
	}
}

func TestMouseAltClickScrollbarAnchorJumpsToUserMessage(t *testing.T) {
	m := NewModel(Config{
		Provider:   "test",
		Model:      "test-model",
		ConfigPath: "/tmp/.wuu.json",
		RunPrompt: func(_ctx context.Context, prompt string) (string, error) {
			return prompt, nil
		},
	})
	m.width = 100
	m.height = 20
	m.relayout()

	for i := 0; i < 3; i++ {
		m.appendEntry("USER", fmt.Sprintf("user %d", i))
		m.appendEntry("ASSISTANT", strings.Repeat("line\n", 20)+"end")
	}
	m.refreshViewport(false)

	if len(m.userMessageLineAnchors) < 2 {
		t.Fatalf("expected at least 2 user anchors, got %d", len(m.userMessageLineAnchors))
	}
	maxOffset := max(0, m.viewport.TotalLineCount()-m.viewport.Height)
	target := 1
	if m.userMessageLineAnchors[target] >= maxOffset {
		target = 0
	}
	anchorRows := contentLinesToScrollbarRows(
		m.userMessageLineAnchors,
		m.layout.Chat.Height,
		m.viewport.TotalLineCount(),
	)
	clickX := m.layout.Chat.X + m.layout.Chat.Width - 1
	clickY := m.layout.Chat.Y + anchorRows[target]

	updated, _ := m.Update(tea.MouseMsg{
		Action: tea.MouseActionPress,
		Button: tea.MouseButtonLeft,
		Alt:    true,
		X:      clickX,
		Y:      clickY,
	})
	after := updated.(Model)

	want := after.userMessageLineAnchors[target]
	maxOffset = max(0, after.viewport.TotalLineCount()-after.viewport.Height)
	if want > maxOffset {
		want = maxOffset
	}
	if after.viewport.YOffset != want {
		t.Fatalf("expected viewport offset %d after anchor click, got %d", want, after.viewport.YOffset)
	}
}

// TestMouseDragSelectionAutoScrollsPastEdge covers the bug where a
// drag-select held past the chat viewport's bottom edge couldn't
// extend the selection into off-screen content. The motion handler
// must (a) scroll the viewport on each motion event that lands
// outside the chat area, and (b) keep scrolling on a recurring
// tick when the user holds the cursor still past the edge — that
// second part is the part the user explicitly asked for.
func TestMouseDragSelectionAutoScrollsPastEdge(t *testing.T) {
	m := newScrollableModelForScrollbarTest(t)
	m.setViewportOffset(0)
	startOffset := m.viewport.YOffset

	// Press inside the chat area to begin a selection.
	pressX := m.layout.Chat.X + 2
	pressY := m.layout.Chat.Y + 1
	updated, _ := m.Update(tea.MouseMsg{
		Action: tea.MouseActionPress,
		Button: tea.MouseButtonLeft,
		X:      pressX,
		Y:      pressY,
	})
	dragging := updated.(Model)
	if !dragging.selection.IsDragging {
		t.Fatal("expected drag to start after press in chat area")
	}

	// Drag past the bottom of the chat area. The motion handler
	// must scroll the viewport AND start the auto-scroll ticker.
	belowY := dragging.layout.Chat.Y + dragging.layout.Chat.Height + 2
	updated, cmd := dragging.Update(tea.MouseMsg{
		Action: tea.MouseActionMotion,
		Button: tea.MouseButtonLeft,
		X:      pressX + 4,
		Y:      belowY,
	})
	afterMotion := updated.(Model)
	if afterMotion.viewport.YOffset <= startOffset {
		t.Fatalf("expected motion past edge to scroll viewport: start=%d after=%d",
			startOffset, afterMotion.viewport.YOffset)
	}
	if !afterMotion.selectionAutoScroll.active {
		t.Fatal("expected auto-scroll state to be active after dragging past edge")
	}
	if afterMotion.selectionAutoScroll.dir != 1 {
		t.Fatalf("expected dir=+1 (down), got %d", afterMotion.selectionAutoScroll.dir)
	}
	if cmd == nil {
		t.Fatal("expected motion past edge to return a tick Cmd")
	}

	// Now simulate "user holds the mouse still past the edge":
	// no further motion events arrive, but the recurring tick
	// must keep advancing the viewport AND extending the
	// selection focus into newly-revealed content.
	offsetBeforeTick := afterMotion.viewport.YOffset
	maxOffset := max(0, afterMotion.viewport.TotalLineCount()-afterMotion.viewport.Height)

	current := afterMotion
	for i := 0; i < 3 && current.viewport.YOffset < maxOffset; i++ {
		next, _ := current.Update(selectionAutoScrollMsg{seq: current.selectionAutoScroll.seq})
		current = next.(Model)
	}

	if current.viewport.YOffset <= offsetBeforeTick {
		t.Fatalf("expected ticks with no further motion to keep scrolling: before=%d after=%d",
			offsetBeforeTick, current.viewport.YOffset)
	}
	if !current.selection.hasSelection() {
		t.Fatal("expected selection to be extended by auto-scroll ticks")
	}
	// Focus row should track the bottom edge of the (now scrolled) viewport.
	wantFocusRow := current.viewport.YOffset + current.layout.Chat.Height - 1
	if current.selection.Focus == nil || current.selection.Focus.Row != wantFocusRow {
		gotRow := -1
		if current.selection.Focus != nil {
			gotRow = current.selection.Focus.Row
		}
		t.Fatalf("expected focus row %d after auto-scroll, got %d", wantFocusRow, gotRow)
	}

	// Stale ticks (from a previous burst, seq mismatch) must be no-ops.
	staleSeq := current.selectionAutoScroll.seq - 1
	offsetBeforeStale := current.viewport.YOffset
	updated, _ = current.Update(selectionAutoScrollMsg{seq: staleSeq})
	stale := updated.(Model)
	if stale.viewport.YOffset != offsetBeforeStale {
		t.Fatalf("expected stale tick to be a no-op: before=%d after=%d",
			offsetBeforeStale, stale.viewport.YOffset)
	}

	// Moving the cursor back inside the viewport must stop the
	// ticker (active=false, seq bumped so any in-flight tick exits).
	insideY := current.layout.Chat.Y + 1
	updated, _ = current.Update(tea.MouseMsg{
		Action: tea.MouseActionMotion,
		Button: tea.MouseButtonLeft,
		X:      pressX + 4,
		Y:      insideY,
	})
	stopped := updated.(Model)
	if stopped.selectionAutoScroll.active {
		t.Fatal("expected auto-scroll to stop after cursor returned inside viewport")
	}

	// Release should also clear the auto-scroll state defensively.
	updated, _ = stopped.Update(tea.MouseMsg{
		Action: tea.MouseActionRelease,
		Button: tea.MouseButtonLeft,
		X:      pressX + 4,
		Y:      insideY,
	})
	released := updated.(Model)
	if released.selection.IsDragging {
		t.Fatal("expected drag to finish after release")
	}
	if released.selectionAutoScroll.active {
		t.Fatal("expected auto-scroll to be cleared after release")
	}
}

func newScrollableModelForScrollbarTest(t *testing.T) Model {
	t.Helper()
	m := NewModel(Config{
		Provider:   "test",
		Model:      "test-model",
		ConfigPath: "/tmp/.wuu.json",
		RunPrompt: func(_ctx context.Context, prompt string) (string, error) {
			return prompt, nil
		},
	})
	m.width = 100
	m.height = 20
	m.relayout()

	for i := 0; i < 3; i++ {
		m.appendEntry("USER", fmt.Sprintf("user %d", i))
		m.appendEntry("ASSISTANT", strings.Repeat("line\n", 20)+"end")
	}
	m.refreshViewport(false)
	m.setViewportOffset(0)
	return m
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

func TestSummarizeQueuedMessages_ShowsPreviewAndOverflowCount(t *testing.T) {
	msgs := []queuedMessage{
		{Text: "first queued"},
		{Text: "second queued"},
		{Text: "third queued"},
	}
	got := summarizeQueuedMessages(msgs)
	want := "first queued | second queued | +1"
	if got != want {
		t.Fatalf("summarizeQueuedMessages() = %q, want %q", got, want)
	}
}

func TestSummarizeQueuedMessage_InlinesImages(t *testing.T) {
	got := summarizeQueuedMessage(queuedMessage{
		Text: "check this",
		Images: []providers.InputImage{
			{MediaType: "image/png", Data: "AAA"},
		},
	})
	want := "check this [Image #1]"
	if got != want {
		t.Fatalf("summarizeQueuedMessage() = %q, want %q", got, want)
	}
}

func TestView_ShowsSteerAndQueuePreview(t *testing.T) {
	m := NewModel(Config{
		Provider:   "test",
		Model:      "test-model",
		ConfigPath: "/tmp/.wuu.json",
		RunPrompt: func(_ctx context.Context, prompt string) (string, error) {
			return "answer to: " + prompt, nil
		},
	})
	m.width = 180
	m.height = 24
	m.relayout()
	m.pendingSteers = []queuedMessage{{Text: "steer now"}}
	m.messageQueue = []queuedMessage{{Text: "queued after steer"}}

	view := m.View()
	if !strings.Contains(view, "steer:1") {
		t.Fatalf("expected steer hint in header, got: %s", view)
	}
	if !strings.Contains(view, "queue:1") {
		t.Fatalf("expected queue hint in header, got: %s", view)
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

func TestStreamReconnectEventUpdatesStatusLine(t *testing.T) {
	m := NewModel(Config{
		Provider:   "test",
		Model:      "test-model",
		ConfigPath: "/tmp/.wuu.json",
		RunPrompt: func(_ctx context.Context, _prompt string) (string, error) {
			return "", nil
		},
	})
	m.streaming = true
	m.pendingRequest = true
	m.streamCh = make(chan providers.StreamEvent)

	updated, _ := m.Update(streamEventMsg{
		event: providers.StreamEvent{
			Type:    providers.EventReconnect,
			Content: "Reconnecting... 1/6",
		},
	})
	after := updated.(Model)
	if after.statusLine != "Reconnecting... 1/6" {
		t.Fatalf("unexpected status line: %q", after.statusLine)
	}
}

func TestSubmitBusyTabQueuesMessage(t *testing.T) {
	m := NewModel(Config{
		Provider:   "test",
		Model:      "test-model",
		ConfigPath: "/tmp/.wuu.json",
		RunPrompt: func(_ctx context.Context, prompt string) (string, error) {
			return "answer to: " + prompt, nil
		},
	})
	m.pendingRequest = true
	m.input.SetValue("tab queued")

	nextModel, cmd := m.submit(true)
	if cmd != nil {
		t.Fatal("expected no command while request is pending")
	}
	next := nextModel.(Model)
	if len(next.messageQueue) != 1 {
		t.Fatalf("expected 1 queued message, got %d", len(next.messageQueue))
	}
	if next.messageQueue[0].Text != "tab queued" {
		t.Fatalf("unexpected queued text: %q", next.messageQueue[0].Text)
	}
	if len(next.pendingSteers) != 0 {
		t.Fatalf("expected no pending steers, got %d", len(next.pendingSteers))
	}
}

func TestSubmitBusyEnterQueuesSteerAndCancelsStream(t *testing.T) {
	m := NewModel(Config{
		Provider:   "test",
		Model:      "test-model",
		ConfigPath: "/tmp/.wuu.json",
		RunPrompt: func(_ctx context.Context, prompt string) (string, error) {
			return "answer to: " + prompt, nil
		},
	})
	m.pendingRequest = true
	cancelCalled := false
	m.cancelStream = func() { cancelCalled = true }
	m.input.SetValue("steer now")

	nextModel, cmd := m.submit(false)
	if cmd != nil {
		t.Fatal("expected no command while request is pending")
	}
	if !cancelCalled {
		t.Fatal("expected cancelStream to be called for steer")
	}
	next := nextModel.(Model)
	if len(next.pendingSteers) != 1 {
		t.Fatalf("expected 1 pending steer, got %d", len(next.pendingSteers))
	}
	if next.pendingSteers[0].Text != "steer now" {
		t.Fatalf("unexpected steer text: %q", next.pendingSteers[0].Text)
	}
	if len(next.messageQueue) != 0 {
		t.Fatalf("expected no queued follow-up, got %d", len(next.messageQueue))
	}
	if next.statusLine != "steering (1 pending)" {
		t.Fatalf("unexpected status line: %q", next.statusLine)
	}
}

func TestDrainQueuePrioritizesPendingSteers(t *testing.T) {
	m := NewModel(Config{
		Provider:   "test",
		Model:      "test-model",
		ConfigPath: "/tmp/.wuu.json",
		RunPrompt: func(_ctx context.Context, prompt string) (string, error) {
			return "answer to: " + prompt, nil
		},
	})
	m.pendingSteers = []queuedMessage{
		{Text: "first steer"},
		{Text: "second steer"},
	}
	m.messageQueue = []queuedMessage{
		{Text: "queued follow-up"},
	}

	nextModel, cmd := m.drainQueue()
	if cmd == nil {
		t.Fatal("expected async command from drainQueue")
	}
	next := nextModel.(Model)
	if len(next.pendingSteers) != 0 {
		t.Fatalf("expected pending steers drained, got %d", len(next.pendingSteers))
	}
	if len(next.messageQueue) != 1 {
		t.Fatalf("expected queued follow-up preserved, got %d", len(next.messageQueue))
	}
	if len(next.chatHistory) == 0 {
		t.Fatal("expected steer message appended to history")
	}
	last := next.chatHistory[len(next.chatHistory)-1]
	if last.Role != "user" {
		t.Fatalf("expected last message role user, got %q", last.Role)
	}
	if last.Content != "first steer\nsecond steer" {
		t.Fatalf("unexpected merged steer content: %q", last.Content)
	}
}

type blockingCompactStreamClient struct {
	compactSleep time.Duration
	chatCalls    atomic.Int32
}

func (c *blockingCompactStreamClient) Chat(_ context.Context, _ providers.ChatRequest) (providers.ChatResponse, error) {
	c.chatCalls.Add(1)
	time.Sleep(c.compactSleep)
	return providers.ChatResponse{Content: "summary"}, nil
}

func (c *blockingCompactStreamClient) StreamChat(_ context.Context, _ providers.ChatRequest) (<-chan providers.StreamEvent, error) {
	ch := make(chan providers.StreamEvent, 2)
	ch <- providers.StreamEvent{Type: providers.EventContentDelta, Content: "ok"}
	ch <- providers.StreamEvent{Type: providers.EventDone}
	close(ch)
	return ch, nil
}

func TestSendMessage_DoesNotBlockOnCompaction(t *testing.T) {
	client := &blockingCompactStreamClient{compactSleep: 500 * time.Millisecond}
	m := NewModel(Config{
		Provider:   "test",
		Model:      "test-model",
		ConfigPath: "/tmp/.wuu.json",
		StreamRunner: &agent.StreamRunner{
			Client: client,
			Model:  "test-model",
		},
	})
	m.maxContextTokens = 10 // force compaction path
	m.chatHistory = []providers.ChatMessage{
		{Role: "user", Content: strings.Repeat("seed ", 40)},
		{Role: "assistant", Content: strings.Repeat("seed ", 40)},
		{Role: "user", Content: strings.Repeat("seed ", 40)},
		{Role: "assistant", Content: strings.Repeat("seed ", 40)},
	}

	start := time.Now()
	nextModel, cmd := m.sendMessage(queuedMessage{
		Text: strings.Repeat("long message ", 20),
	})
	elapsed := time.Since(start)

	if cmd == nil {
		t.Fatal("expected async command from sendMessage")
	}
	if elapsed > 150*time.Millisecond {
		t.Fatalf("sendMessage blocked too long: %s", elapsed)
	}

	next := nextModel.(Model)
	if !next.pendingRequest {
		t.Fatal("expected pendingRequest=true")
	}

	// Let background goroutine run and trigger compaction call.
	deadline := time.Now().Add(2 * time.Second)
	for client.chatCalls.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if got := client.chatCalls.Load(); got == 0 {
		t.Fatal("expected compaction chat call in background")
	}
}

// TestComputeLayout_ReservesInlineStatusLine locks in the layout invariant
// that computeLayout reserves exactly one line below the chat viewport for
// the inline status indicator. If this slot is removed from the math, the
// View() total row count will exceed the terminal height and bubbletea will
// truncate, breaking the header-first render order.
func TestComputeLayout_ReservesInlineStatusLine(t *testing.T) {
	const termWidth, termHeight = 100, 40
	const inputLines = 3

	// No worker panel.
	l := computeLayout(termWidth, termHeight, inputLines, 0)
	// Expected total lines in View(): header + chat + inlineStatus + sep + input
	// = 1 + chatH + 1 + 1 + inputLines = termHeight
	expected := 1 + l.Chat.Height + 1 + 1 + inputLines
	if expected != termHeight {
		t.Fatalf("no-worker layout row count mismatch: header(1) + chat(%d) + status(1) + sep(1) + input(%d) = %d, want %d",
			l.Chat.Height, inputLines, expected, termHeight)
	}

	// With worker panel (2 workers → 3 rows: title + 2 rows).
	const workerRows = 3
	lw := computeLayout(termWidth, termHeight, inputLines, workerRows)
	// Expected: header + chat + inlineStatus + sep + panel + sep + input
	expectedW := 1 + lw.Chat.Height + 1 + 1 + workerRows + 1 + inputLines
	if expectedW != termHeight {
		t.Fatalf("worker-panel layout row count mismatch: header(1) + chat(%d) + status(1) + sep(1) + panel(%d) + sep(1) + input(%d) = %d, want %d",
			lw.Chat.Height, workerRows, inputLines, expectedW, termHeight)
	}

	// Worker panel steals from chat, not from the inline status slot.
	if lw.Chat.Height != l.Chat.Height-workerRows-1 {
		t.Fatalf("worker panel should only steal from chat height: noWorker=%d, withWorker=%d, delta=%d, want delta=%d",
			l.Chat.Height, lw.Chat.Height, l.Chat.Height-lw.Chat.Height, workerRows+1)
	}
}

// TestInlineSpinMsg_DoesNotRebuildViewport locks in the fix: inlineSpinMsg
// must not trigger a viewport content rebuild. If it does, the viewport's
// YOffset will shift on every 150ms spinner tick and break bubbletea's
// line-diff, causing tool card flicker and scrollbar jitter.
func TestInlineSpinMsg_DoesNotRebuildViewport(t *testing.T) {
	m := NewModel(Config{
		Provider:   "test",
		Model:      "test-model",
		ConfigPath: "/tmp/.wuu.json",
		RunPrompt: func(_ctx context.Context, prompt string) (string, error) {
			return prompt, nil
		},
	})
	m.width = 100
	m.height = 40
	m.relayout()

	// Simulate streaming state: add content and point streamTarget at
	// the active assistant entry. In the pre-fix code, refreshViewport
	// would render renderInlineStatus inside the viewport for the
	// streamTarget entry when streaming=true, adding an extra line.
	m.streaming = true
	m.statusLine = "streaming"
	for i := 0; i < 60; i++ {
		m.appendEntry("assistant", fmt.Sprintf("line %d", i))
	}
	m.streamTarget = len(m.entries) - 1 // point at last assistant entry
	m.refreshViewport(true)

	// Scroll to top and disable auto-follow so we can observe entry[0].
	m.viewport.SetYOffset(0)
	m.autoFollow = false

	// Mutate entry[0] with a unique marker. If inlineSpinMsg triggers
	// refreshViewport, the viewport content gets rebuilt from m.entries
	// and the marker appears in the visible window (YOffset=0 → top).
	// If it doesn't call refreshViewport, the stale content remains.
	const marker = "SPIN_REBUILD_CANARY"
	m.entries[0].Content = marker
	beforeFrame := m.inlineSpinFrame

	// Dispatch the spinner tick.
	updated, _ := m.Update(inlineSpinMsg{})
	after := updated.(Model)

	if after.inlineSpinFrame != beforeFrame+1 {
		t.Fatalf("expected inlineSpinFrame to advance by 1: before=%d after=%d",
			beforeFrame, after.inlineSpinFrame)
	}
	// The canary must NOT appear in the viewport — that would mean
	// refreshViewport was called, rebuilding content from m.entries.
	if strings.Contains(after.viewport.View(), marker) {
		t.Fatal("inlineSpinMsg must not call refreshViewport: canary marker appeared in viewport content after spinner tick")
	}
}

// TestView_InlineStatusRenderedOutsideViewport verifies the inline status
// indicator lives in a View() segment below the viewport, not as part of
// the viewport content. The viewport content must be free of "Generating"/
// "Working" text so scrolling cannot affect spinner visibility.
func TestView_InlineStatusRenderedOutsideViewport(t *testing.T) {
	m := NewModel(Config{
		Provider:   "test",
		Model:      "test-model",
		ConfigPath: "/tmp/.wuu.json",
		RunPrompt: func(_ctx context.Context, prompt string) (string, error) {
			return prompt, nil
		},
	})
	m.width = 100
	m.height = 40
	m.streaming = true
	m.statusLine = "streaming"
	m.appendEntry("user", "hello")
	m.appendEntry("assistant", "partial reply")
	m.streamTarget = len(m.entries) - 1
	m.relayout()

	viewportContent := m.viewport.View()
	if strings.Contains(viewportContent, "Generating") {
		t.Fatal("viewport content must not contain inline status 'Generating' — it should be rendered outside the viewport")
	}

	fullView := m.View()
	if !strings.Contains(fullView, "Generating") {
		t.Fatalf("full View() must contain inline status 'Generating'; got:\n%s", fullView)
	}
}
