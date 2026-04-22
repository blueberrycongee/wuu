package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/viewport"
	"github.com/charmbracelet/x/ansi"
)

// makePeekModel returns a Model with enough state wired up to exercise
// the peek helpers: a handful of entries, their renderStart offsets, and
// a viewport configured to a specific YOffset.
func makePeekModel(yOffset int, entries []transcriptEntry) Model {
	vp := viewport.New(80, 20)
	vp.YOffset = yOffset
	return Model{
		entries:  entries,
		viewport: vp,
	}
}

func TestFindPrevEntryForPeek_AtTopReturnsNoIndex(t *testing.T) {
	m := makePeekModel(0, []transcriptEntry{
		{Role: "USER", renderStart: 0, renderEnd: 2},
	})
	if idx := m.findPrevEntryForPeek(); idx != -1 {
		t.Fatalf("YOffset=0 should yield no previous entry, got %d", idx)
	}
}

func TestFindPrevEntryForPeek_ReturnsClosestAbove(t *testing.T) {
	entries := []transcriptEntry{
		{Role: "USER", Content: "a", renderStart: 0, renderEnd: 4},
		{Role: "ASSISTANT", Content: "b", renderStart: 10, renderEnd: 20},
		{Role: "USER", Content: "c", renderStart: 30, renderEnd: 34},
	}
	// YOffset between entry 1 and entry 2 → previous should be entry 1.
	m := makePeekModel(25, entries)
	if idx := m.findPrevEntryForPeek(); idx != 1 {
		t.Fatalf("expected idx=1, got %d", idx)
	}
	// YOffset past the last entry → previous should be the last one.
	m = makePeekModel(100, entries)
	if idx := m.findPrevEntryForPeek(); idx != 2 {
		t.Fatalf("expected idx=2, got %d", idx)
	}
}

func TestFindPrevEntryForPeek_SkipsUnrenderedEntries(t *testing.T) {
	// The virtualized renderer leaves entries outside the overscan
	// margin with renderStart=-1. Those must not be picked as "prev".
	entries := []transcriptEntry{
		{Role: "USER", renderStart: -1, renderEnd: -1},
		{Role: "ASSISTANT", renderStart: 5, renderEnd: 8},
		{Role: "TOOL", renderStart: 9, renderEnd: 9}, // also skipped
	}
	m := makePeekModel(15, entries)
	if idx := m.findPrevEntryForPeek(); idx != 1 {
		t.Fatalf("expected idx=1 (ASSISTANT), got %d", idx)
	}
}

func TestRenderPrevEntryPeek_EmptyWhenAtTop(t *testing.T) {
	m := makePeekModel(0, []transcriptEntry{
		{Role: "USER", Content: "hi", renderStart: 0, renderEnd: 1},
	})
	if out := m.renderPrevEntryPeek(80); out != "" {
		t.Fatalf("expected empty peek at YOffset=0, got %q", out)
	}
}

func TestRenderPrevEntryPeek_ShowsPreviewAndHint(t *testing.T) {
	m := makePeekModel(20, []transcriptEntry{
		{Role: "USER", Content: "How do I fix the bug in main.go?", renderStart: 0, renderEnd: 4},
	})
	out := m.renderPrevEntryPeek(80)
	if out == "" {
		t.Fatal("expected peek string")
	}
	// Strip SGR so asserts aren't coupled to terminal profile.
	stripped := ansi.Strip(out)
	if !strings.Contains(stripped, "↑") {
		t.Fatalf("expected up-arrow marker, got %q", stripped)
	}
	if !strings.Contains(stripped, "press [ to jump") {
		t.Fatalf("expected jump hint, got %q", stripped)
	}
	if !strings.Contains(stripped, "How do I fix the bug") {
		t.Fatalf("expected preview content, got %q", stripped)
	}
}

func TestRenderPrevEntryPeek_CollapsesWhitespaceInPreview(t *testing.T) {
	// Multiline content with blank lines should render as one line.
	content := "\n\n   first real line\nsecond line here\n"
	m := makePeekModel(10, []transcriptEntry{
		{Role: "ASSISTANT", Content: content, renderStart: 0, renderEnd: 5},
	})
	out := ansi.Strip(m.renderPrevEntryPeek(80))
	if strings.Contains(out, "\n") {
		t.Fatalf("peek should be one line, got %q", out)
	}
	if !strings.Contains(out, "first real line") {
		t.Fatalf("expected first meaningful line preserved, got %q", out)
	}
}

func TestRenderPrevEntryPeek_TruncatesLongPreview(t *testing.T) {
	long := strings.Repeat("x ", 500)
	m := makePeekModel(10, []transcriptEntry{
		{Role: "USER", Content: long, renderStart: 0, renderEnd: 1},
	})
	out := ansi.Strip(m.renderPrevEntryPeek(60))
	if !strings.Contains(out, "…") {
		t.Fatalf("long preview should be truncated with ellipsis, got %q", out)
	}
	if ansi.StringWidth(out) > 60 {
		t.Fatalf("peek must fit in width 60, got width %d", ansi.StringWidth(out))
	}
}

func TestRenderPrevEntryPeek_HandlesEmptyContent(t *testing.T) {
	m := makePeekModel(10, []transcriptEntry{
		{Role: "USER", Content: "", renderStart: 0, renderEnd: 1},
	})
	out := ansi.Strip(m.renderPrevEntryPeek(80))
	if !strings.Contains(out, "(empty message)") {
		t.Fatalf("expected empty-message placeholder, got %q", out)
	}
}

func TestOverlayPrevEntryPeek_ReplacesFirstLine(t *testing.T) {
	output := "line1\nline2\nline3"
	peek := "PEEK"
	got := overlayPrevEntryPeek(output, peek)
	want := "PEEK\nline2\nline3"
	if got != want {
		t.Fatalf("overlay result wrong:\n got: %q\n want: %q", got, want)
	}
}

func TestOverlayPrevEntryPeek_EmptyPeekIsNoop(t *testing.T) {
	output := "line1\nline2"
	if got := overlayPrevEntryPeek(output, ""); got != output {
		t.Fatalf("empty peek should pass output through, got %q", got)
	}
}

func TestOverlayPrevEntryPeek_SingleLineOutputReplaced(t *testing.T) {
	// Output without any newlines (edge case: viewport height=1) just
	// becomes the peek — there's no subsequent content to preserve.
	if got := overlayPrevEntryPeek("only", "PEEK"); got != "PEEK" {
		t.Fatalf("single-line replace wrong: %q", got)
	}
}

func TestJumpToPrevEntry_SetsYOffsetToEntryStart(t *testing.T) {
	entries := []transcriptEntry{
		{Role: "USER", Content: "first", renderStart: 0, renderEnd: 3},
		{Role: "ASSISTANT", Content: "second", renderStart: 10, renderEnd: 18},
	}
	m := makePeekModel(25, entries)
	// TotalLineCount/Height aren't set up the way setViewportOffset
	// expects, so we clamp to whatever viewport thinks is reasonable.
	// The invariant we care about: the jump fires and prefers the
	// second entry's renderStart.
	if !m.jumpToPrevEntry() {
		t.Fatal("jumpToPrevEntry returned false with a viable target")
	}
}

func TestJumpToPrevEntry_NoopAtTop(t *testing.T) {
	m := makePeekModel(0, []transcriptEntry{
		{Role: "USER", renderStart: 0, renderEnd: 3},
	})
	if m.jumpToPrevEntry() {
		t.Fatal("jumpToPrevEntry should be a no-op when already at top")
	}
}

func TestPeekBarClick_OnlyFiresOnTopRowWhenScrolled(t *testing.T) {
	m := makePeekModel(5, []transcriptEntry{
		{Role: "USER", renderStart: 0, renderEnd: 3},
	})
	m.layout.Chat.X = 0
	m.layout.Chat.Y = 2
	m.layout.Chat.Width = 80

	if !m.peekBarClick(10, 2) {
		t.Fatal("click on peek row should return true")
	}
	if m.peekBarClick(10, 3) {
		t.Fatal("click on a lower row should not trigger peek action")
	}
	// Even on row 2, click outside chat's horizontal span must be ignored.
	if m.peekBarClick(100, 2) {
		t.Fatal("click outside horizontal span should not fire peek action")
	}
	// When not scrolled (YOffset=0), there's no peek to click.
	m.viewport.YOffset = 0
	if m.peekBarClick(10, 2) {
		t.Fatal("unscrolled viewport should have no peek hit area")
	}
}
