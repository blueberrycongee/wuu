package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
)

// TestMain forces lipgloss into TrueColor mode so style.Render
// actually emits ANSI escapes during the test run. Without this the
// renderer auto-detects "no terminal attached" and silently strips
// all color, which makes our highlight assertions impossible.
func init() {
	lipgloss.SetColorProfile(termenv.TrueColor)
}

// fullContent fixture with 10 distinct lines so we can verify
// content-row addressing across scroll positions.
const fixtureContent = "line0\nline1\nline2\nline3\nline4\nline5\nline6\nline7\nline8\nline9"

func TestScreenToViewportCoords_AccountsForContentPadding(t *testing.T) {
	m := &Model{}
	m.layout.Chat.X = 10
	m.layout.Chat.Y = 4
	m.layout.Chat.Height = 5
	m.viewport.YOffset = 7

	row, col := m.screenToViewportCoords(10+contentPadLeft+3, 6)
	if row != 9 {
		t.Fatalf("row: got %d, want %d", row, 9)
	}
	if col != 3 {
		t.Fatalf("col: got %d, want %d", col, 3)
	}

	_, col = m.screenToViewportCoords(10, 6)
	if col != 0 {
		t.Fatalf("col before content area: got %d, want 0", col)
	}
}

func TestMouseSelectionStartsAfterDragThresholdFromContentArea(t *testing.T) {
	m := NewModel(Config{Provider: "test", Model: "test-model", ConfigPath: "/tmp/.wuu.json"})
	m.width = 100
	m.height = 20
	m.relayout()

	pressX := m.layout.Chat.X + contentPadLeft
	pressY := m.layout.Chat.Y
	updated, _ := m.Update(tea.MouseMsg{
		Action: tea.MouseActionPress,
		Button: tea.MouseButtonLeft,
		X:      pressX,
		Y:      pressY,
	})
	pressed := updated.(Model)

	if pressed.selection.Anchor != nil {
		t.Fatal("expected no selection anchor before drag threshold is crossed")
	}
	if !pressed.pendingChatClick.active {
		t.Fatal("expected pending chat click after mouse press")
	}

	updated, _ = pressed.Update(tea.MouseMsg{
		Action: tea.MouseActionMotion,
		Button: tea.MouseButtonLeft,
		X:      pressX + chatSelectionDragThreshold + 1,
		Y:      pressY,
	})
	after := updated.(Model)

	if after.selection.Anchor == nil {
		t.Fatal("expected selection anchor after drag threshold is crossed")
	}
	if after.selection.Anchor.Col != 0 {
		t.Fatalf("expected selection anchor col 0 at content start, got %d", after.selection.Anchor.Col)
	}
}

func TestSelectionBaseColForLine_TracksIndentation(t *testing.T) {
	if got := selectionBaseColForLine("  plain"); got != contentPadLeft {
		t.Fatalf("expected base col %d for plain line, got %d", contentPadLeft, got)
	}
	if got := selectionBaseColForLine("    ❯"); got != contentPadLeft+2 {
		t.Fatalf("expected base col %d for user label line, got %d", contentPadLeft+2, got)
	}
	if got := selectionBaseColForLine("   x"); got != contentPadLeft+1 {
		t.Fatalf("expected base col %d for user bubble line, got %d", contentPadLeft+1, got)
	}
}

func TestScreenToViewportCoords_UsesRenderedLineIndent(t *testing.T) {
	m := &Model{}
	m.layout.Chat.X = 10
	m.layout.Chat.Y = 4
	m.layout.Chat.Height = 5
	m.viewport.YOffset = 0
	m.renderedContent = "  plain\n    ❯\n   user"

	row, col := m.screenToViewportCoords(10+contentPadLeft+1, 4)
	if row != 0 || col != 1 {
		t.Fatalf("row0 col mismatch: got row=%d col=%d", row, col)
	}

	row, col = m.screenToViewportCoords(10+contentPadLeft+2, 5)
	if row != 1 || col != 0 {
		t.Fatalf("row1 col mismatch: got row=%d col=%d", row, col)
	}

	row, col = m.screenToViewportCoords(10+contentPadLeft+2, 6)
	if row != 2 || col != 1 {
		t.Fatalf("row2 col mismatch: got row=%d col=%d", row, col)
	}
}

func TestSelectionSelectedText_UsesPerLineBaseIndent(t *testing.T) {
	sel := &selectionState{
		Anchor: &selectionPoint{Row: 1, Col: 0},
		Focus:  &selectionPoint{Row: 2, Col: 0},
	}
	content := "  plain\n    ❯\n   user"
	got := sel.selectedText(content)
	want := "❯\nu"
	if got != want {
		t.Fatalf("selectedText with per-line indent: got %q, want %q", got, want)
	}
}

func TestIsInChatArea_IncludesFullChatWidth(t *testing.T) {
	m := &Model{}
	m.layout.Chat = layoutRect{X: 0, Y: 0, Width: 80, Height: 10}

	insideRight := m.layout.Chat.X + m.layout.Chat.Width - 1
	if !m.isInChatArea(insideRight, 0) {
		t.Fatalf("expected x=%d inside chat area", insideRight)
	}
	if m.isInChatArea(insideRight+1, 0) {
		t.Fatalf("expected x=%d outside chat area", insideRight+1)
	}
}

func TestSelection_SelectedTextUsesVisualColumnsForWideRunes(t *testing.T) {
	sel := &selectionState{
		Anchor: &selectionPoint{Row: 0, Col: 0},
		Focus:  &selectionPoint{Row: 0, Col: 1},
	}

	got := sel.selectedText("你好a")
	want := "你"
	if got != want {
		t.Fatalf("selectedText wide rune: got %q, want %q", got, want)
	}
}

func TestSelection_SelectedTextUsesVisualColumnsWithANSI(t *testing.T) {
	line := lipgloss.NewStyle().Foreground(lipgloss.Color("#00FF00")).Render("你好a")
	sel := &selectionState{
		Anchor: &selectionPoint{Row: 0, Col: 2},
		Focus:  &selectionPoint{Row: 0, Col: 3},
	}

	got := sel.selectedText(line)
	want := "好"
	if got != want {
		t.Fatalf("selectedText ANSI wide rune: got %q, want %q", got, want)
	}
}

func TestHighlightLineRange_UsesVisualColumnsForWideRunes(t *testing.T) {
	out := highlightLineRange("你好a", 2, 4)
	stripped := ansi.Strip(out)
	if stripped != "你好a" {
		t.Fatalf("strip: got %q, want %q", stripped, "你好a")
	}
	// The bg-only highlight wraps the selected wide rune with the
	// selection bg open + close sequences without disturbing fg.
	bgOpen := selectionBgSGROpen()
	bgClose := selectionBgSGRClose()
	if !strings.Contains(out, bgOpen+"好"+bgClose) {
		t.Fatalf("expected wide rune wrapped in bg-only selection sequences, got %q", out)
	}
}

func TestHighlightLineRange_PreservesPaddingAlignment(t *testing.T) {
	line := strings.Repeat(" ", contentPadLeft) + "你好a"

	out := highlightLineRange(line, contentPadLeft+2, contentPadLeft+4)
	stripped := ansi.Strip(out)
	if stripped != line {
		t.Fatalf("strip: got %q, want %q", stripped, line)
	}
	bgOpen := selectionBgSGROpen()
	bgClose := selectionBgSGRClose()
	if !strings.Contains(out, bgOpen+"好"+bgClose) {
		t.Fatalf("expected highlighted wide rune after padding, got %q", out)
	}
}

func TestSelection_SelectedTextAcrossMultipleLines(t *testing.T) {
	sel := &selectionState{
		Anchor: &selectionPoint{Row: 2, Col: 0},
		Focus:  &selectionPoint{Row: 4, Col: 4},
	}
	got := sel.selectedText(fixtureContent)
	want := "line2\nline3\nline4"
	if got != want {
		t.Fatalf("selectedText: got %q, want %q", got, want)
	}
}

func TestSelection_SelectedTextSurvivesAcrossScrollPosition(t *testing.T) {
	// Selection covers absolute content rows 5-7. Even though the
	// "visible window" the renderer might produce is row 0-4 (after
	// the user has scrolled), selectedText reads from the FULL
	// content and returns the right substring.
	sel := &selectionState{
		Anchor: &selectionPoint{Row: 5, Col: 0},
		Focus:  &selectionPoint{Row: 7, Col: 4},
	}
	got := sel.selectedText(fixtureContent)
	want := "line5\nline6\nline7"
	if got != want {
		t.Fatalf("selectedText after scroll: got %q, want %q", got, want)
	}
}

func TestOverlaySelection_TranslatesContentRowsToVisibleWindow(t *testing.T) {
	// Visible window contains content rows [3, 4, 5] because the
	// view was scrolled to YOffset=3. The selection covers content
	// rows 4-5, which should map to local viewport rows 1-2.
	visibleWindow := "line3\nline4\nline5"
	sel := &selectionState{
		Anchor: &selectionPoint{Row: 4, Col: 0},
		Focus:  &selectionPoint{Row: 5, Col: 4},
	}

	out := overlaySelection(visibleWindow, sel, 3, 80)
	lines := strings.Split(out, "\n")

	// Row 0 of the visible window (content row 3) is NOT in the
	// selection range and should be untouched.
	if lines[0] != "line3" {
		t.Errorf("row 0 should be untouched, got %q", lines[0])
	}
	// Rows 1 and 2 should have ANSI codes injected (the highlight).
	if !strings.Contains(lines[1], "\x1b[") {
		t.Errorf("row 1 should be highlighted (got %q)", lines[1])
	}
	if !strings.Contains(lines[2], "\x1b[") {
		t.Errorf("row 2 should be highlighted (got %q)", lines[2])
	}
}

func TestOverlaySelection_ClipsBeyondVisibleWindow(t *testing.T) {
	// Selection covers content rows 0-9 but only rows 3-5 are visible.
	// overlaySelection should silently skip the off-screen rows
	// without panicking.
	visibleWindow := "line3\nline4\nline5"
	sel := &selectionState{
		Anchor: &selectionPoint{Row: 0, Col: 0},
		Focus:  &selectionPoint{Row: 9, Col: 4},
	}

	out := overlaySelection(visibleWindow, sel, 3, 80)
	if strings.Count(out, "\n") != 2 {
		t.Fatalf("expected 3 visible lines, got: %q", out)
	}
	// All three visible rows are inside [0, 9], so all three should
	// be highlighted.
	for i, line := range strings.Split(out, "\n") {
		if !strings.Contains(line, "\x1b[") {
			t.Errorf("row %d should be highlighted (got %q)", i, line)
		}
	}
}

func TestOverlaySelection_SelectionAboveVisibleWindow(t *testing.T) {
	// Selection covers rows 0-1, but visible window starts at row 5.
	// Nothing should be highlighted; the visible window comes back
	// completely unchanged.
	visibleWindow := "line5\nline6\nline7"
	sel := &selectionState{
		Anchor: &selectionPoint{Row: 0, Col: 0},
		Focus:  &selectionPoint{Row: 1, Col: 4},
	}

	out := overlaySelection(visibleWindow, sel, 5, 80)
	if out != visibleWindow {
		t.Fatalf("off-screen selection should leave window untouched, got: %q", out)
	}
}

func TestOverlaySelection_SelectionBelowVisibleWindow(t *testing.T) {
	// Mirror of the above: selection on rows 8-9, visible window
	// shows rows 0-2. Nothing should be highlighted.
	visibleWindow := "line0\nline1\nline2"
	sel := &selectionState{
		Anchor: &selectionPoint{Row: 8, Col: 0},
		Focus:  &selectionPoint{Row: 9, Col: 4},
	}

	out := overlaySelection(visibleWindow, sel, 0, 80)
	if out != visibleWindow {
		t.Fatalf("off-screen selection should leave window untouched, got: %q", out)
	}
}

func TestOverlaySelection_NoSelectionPassThrough(t *testing.T) {
	out := overlaySelection("hello\nworld", nil, 0, 80)
	if out != "hello\nworld" {
		t.Fatalf("nil selection should pass through, got %q", out)
	}
}

// --- Bug-coverage tests for the colored-text selection fix ---
//
// These pin the behaviors that were broken in the old implementation:
//
//   1. Foreground color preserved AFTER the highlighted region.
//      The old slicer dropped leading SGR state when cutting from
//      the colEnd point, so the post-highlight tail was rendered
//      without its original color.
//
//   2. Foreground color preserved INSIDE the highlighted region.
//      The old highlight used a lipgloss style with bg + fg, and
//      lipgloss closed with `\x1b[0m` (full reset), wiping any fg
//      that had been active before the highlight.
//
//   3. Highlight stays connected across mid-selection SGR resets.
//      Markdown / syntax highlighting commonly emit `\x1b[0m` mid
//      line; without re-emitting the bg open after each reset, the
//      highlight visibly broke at every reset point.

func TestHighlightLineRange_PreservesForegroundAfterHighlight(t *testing.T) {
	// "helloworld" all in red. Highlight cols [3, 7] = "lowo".
	// After the highlight closes, the rest of the line ("rld") must
	// still render as red.
	red := lipgloss.NewStyle().Foreground(lipgloss.Color("#FF0000"))
	line := red.Render("helloworld")

	out := highlightLineRange(line, 3, 7)
	if got := ansi.Strip(out); got != "helloworld" {
		t.Fatalf("strip mismatch: got %q, want %q", got, "helloworld")
	}

	// Find the bg-close sequence and look at what comes after it.
	// The "after" portion must contain a foreground color SGR that
	// re-establishes red, otherwise the tail is rendered in the
	// terminal default color.
	bgClose := selectionBgSGRClose()
	idx := strings.Index(out, bgClose)
	if idx < 0 {
		t.Fatalf("expected bg-close sequence in output, got %q", out)
	}
	tail := out[idx+len(bgClose):]
	// The "after" tail must visually render "rld" in red. We assert
	// this by checking that the bytes between the bg-close and the
	// next plain "r" character contain at least one SGR sequence
	// that mentions a red-ish foreground (any of 31, 91, 38;2;255,
	// or 38;5;... — we only need to confirm the slicer preserved
	// SOMETHING).
	if !strings.Contains(tail, "\x1b[") {
		t.Fatalf("post-highlight tail lost its SGR state, got tail %q", tail)
	}
}

func TestHighlightLineRange_PreservesForegroundInsideHighlight(t *testing.T) {
	// Highlight a slice that has its own foreground color. The output
	// inside the bg-open / bg-close pair must still contain the fg
	// SGR — otherwise the original color was wiped by the highlight.
	red := lipgloss.NewStyle().Foreground(lipgloss.Color("#FF0000"))
	line := red.Render("colored")

	out := highlightLineRange(line, 1, 5)
	bgOpen := selectionBgSGROpen()
	bgClose := selectionBgSGRClose()
	openIdx := strings.Index(out, bgOpen)
	closeIdx := strings.Index(out, bgClose)
	if openIdx < 0 || closeIdx < 0 || closeIdx < openIdx {
		t.Fatalf("malformed output (missing or out-of-order bg sequences): %q", out)
	}
	inside := out[openIdx+len(bgOpen) : closeIdx]
	if !strings.Contains(inside, "\x1b[") {
		t.Fatalf("highlighted region dropped its fg SGR state, inside=%q", inside)
	}
}

func TestHighlightLineRange_StaysConnectedAcrossMidSelectionReset(t *testing.T) {
	// Build a line that has an SGR reset in the middle of what we
	// will highlight: "AAAA<reset>BBBB". Highlight cols [1, 7] which
	// straddles the reset. The bg open must be re-emitted after the
	// reset so the highlight stays continuous across the boundary.
	line := "\x1b[31mAAAA\x1b[0m BBBB"

	out := highlightLineRange(line, 1, 7)
	// Count how many bg-open occurrences land between the first
	// bg-open and the bg-close — there must be MORE THAN ONE,
	// because the slicer encountered a `\x1b[0m` mid-slice and we
	// re-emitted bg-open after it.
	bgOpen := selectionBgSGROpen()
	bgClose := selectionBgSGRClose()
	openIdx := strings.Index(out, bgOpen)
	closeIdx := strings.Index(out, bgClose)
	if openIdx < 0 || closeIdx < 0 || closeIdx < openIdx {
		t.Fatalf("missing bg sequences: %q", out)
	}
	region := out[openIdx : closeIdx+len(bgClose)]
	if strings.Count(region, bgOpen) < 2 {
		t.Fatalf("expected bg-open to be re-emitted after mid-slice reset, got region=%q", region)
	}
}

func TestSelectionBgSGRClose_DoesNotFullReset(t *testing.T) {
	// The bg close MUST be `\x1b[49m` (default bg, leave fg alone),
	// not `\x1b[0m` (full reset). A full reset would wipe whatever
	// fg color was active when the highlight ended, defeating the
	// whole point of bg-only overlay.
	if got := selectionBgSGRClose(); got != "\x1b[49m" {
		t.Fatalf("bg close must be \\x1b[49m, got %q", got)
	}
}

func TestSelectionBgSGROpen_HasNoFullResetNoForeground(t *testing.T) {
	// The bg open MUST set ONLY the background. It must not contain
	// `\x1b[0m` (would wipe ambient fg) and must not contain a `38;`
	// foreground intro (would override ambient fg).
	open := selectionBgSGROpen()
	if strings.Contains(open, "\x1b[0m") {
		t.Errorf("bg open must not contain a full reset: %q", open)
	}
	// A foreground SGR starts with \x1b[38; — check the sequence prefix,
	// not a naive substring (which false-positives when the red channel
	// of the bg color happens to be 38).
	if strings.HasPrefix(open, "\x1b[38;") {
		t.Errorf("bg open must not set a foreground color: %q", open)
	}
}

func TestStripBackgroundSGR_RemovesBackgroundKeepsForeground(t *testing.T) {
	in := "\x1b[1;38;2;1;2;3;48;2;4;5;6mX\x1b[0m"
	out := stripBackgroundSGR(in)
	if !strings.Contains(out, "38;2;1;2;3") {
		t.Fatalf("expected foreground SGR to be preserved, got %q", out)
	}
	if strings.Contains(out, "48;") {
		t.Fatalf("expected background SGR to be removed, got %q", out)
	}
}

func TestStripBackgroundSGR_RemovesColonBackgroundKeepsForeground(t *testing.T) {
	in := "\x1b[1;38:2::1:2:3;48:2::4:5:6mX\x1b[0m"
	out := stripBackgroundSGR(in)
	if !strings.Contains(out, "38:2::1:2:3") {
		t.Fatalf("expected colon foreground SGR to be preserved, got %q", out)
	}
	if strings.Contains(out, "48:") {
		t.Fatalf("expected colon background SGR to be removed, got %q", out)
	}
}

func TestHighlightLineRange_StripsExistingBackgroundFromSelectionSlice(t *testing.T) {
	prev := currentTheme
	applyTheme(darkTheme)
	t.Cleanup(func() { applyTheme(prev) })

	line := userContentStyle.Render("hello")
	out := highlightLineRange(line, 0, 5)

	bgOpen := selectionBgSGROpen()
	bgClose := selectionBgSGRClose()
	openIdx := strings.Index(out, bgOpen)
	closeIdx := strings.Index(out, bgClose)
	if openIdx < 0 || closeIdx < 0 || closeIdx < openIdx {
		t.Fatalf("missing selection bg wrapper in output: %q", out)
	}
	inside := out[openIdx+len(bgOpen) : closeIdx]
	if strings.Contains(inside, "48;2;47;56;66") {
		t.Fatalf("selected slice should drop original user-bubble background, got %q", inside)
	}
}
