package markdown

import (
	"strings"
	"testing"
)

func TestRender_Paragraph(t *testing.T) {
	got := Render("Hello world", 80, DefaultStyles())
	if !strings.Contains(got, "Hello world") {
		t.Fatalf("expected 'Hello world', got %q", got)
	}
}

func TestRender_Heading(t *testing.T) {
	got := Render("# Title\n\nBody", 80, DefaultStyles())
	if !strings.Contains(got, "Title") {
		t.Fatalf("expected 'Title' in output, got %q", got)
	}
	if !strings.Contains(got, "Body") {
		t.Fatalf("expected 'Body' in output, got %q", got)
	}
}

func TestRender_CodeBlock(t *testing.T) {
	input := "```go\npackage main\n```"
	got := Render(input, 80, DefaultStyles())
	if !strings.Contains(got, "package") {
		t.Fatalf("expected code content, got %q", got)
	}
	// Should have 4-space indent.
	if !strings.Contains(got, "    ") {
		t.Fatalf("expected 4-space indent in code block, got %q", got)
	}
}

func TestRender_NestedCodeFence(t *testing.T) {
	// This is the exact bug case: code fence inside code fence.
	input := "```md\n## Advanced Usage\n\n```bash\nwuu tui --resume\n```\n```\n\nSome text after."
	got := Render(input, 80, DefaultStyles())
	if !strings.Contains(got, "Some text after") {
		t.Fatalf("nested code fence broke rendering, got %q", got)
	}
}

func TestRender_UnorderedList(t *testing.T) {
	input := "- Item 1\n- Item 2\n- Item 3"
	got := Render(input, 80, DefaultStyles())
	if !strings.Contains(got, "Item 1") || !strings.Contains(got, "Item 3") {
		t.Fatalf("expected list items, got %q", got)
	}
}

func TestRender_OrderedList(t *testing.T) {
	input := "1. First\n2. Second"
	got := Render(input, 80, DefaultStyles())
	if !strings.Contains(got, "First") || !strings.Contains(got, "Second") {
		t.Fatalf("expected ordered list items, got %q", got)
	}
}

func TestRender_Blockquote(t *testing.T) {
	input := "> This is a quote"
	got := Render(input, 80, DefaultStyles())
	if !strings.Contains(got, "This is a quote") {
		t.Fatalf("expected blockquote content, got %q", got)
	}
}

func TestRender_InlineCode(t *testing.T) {
	input := "Use `fmt.Println` to print"
	got := Render(input, 80, DefaultStyles())
	if !strings.Contains(got, "fmt.Println") {
		t.Fatalf("expected inline code, got %q", got)
	}
}

func TestRender_Bold(t *testing.T) {
	input := "This is **bold** text"
	got := Render(input, 80, DefaultStyles())
	if !strings.Contains(got, "bold") {
		t.Fatalf("expected bold text, got %q", got)
	}
}

func TestRender_Link(t *testing.T) {
	input := "[Click here](https://example.com)"
	got := Render(input, 80, DefaultStyles())
	if !strings.Contains(got, "Click here") {
		t.Fatalf("expected link text, got %q", got)
	}
	if !strings.Contains(got, "example.com") {
		t.Fatalf("expected link URL, got %q", got)
	}
}

func TestRender_ThematicBreak(t *testing.T) {
	input := "Before\n\n---\n\nAfter"
	got := Render(input, 80, DefaultStyles())
	if !strings.Contains(got, "Before") || !strings.Contains(got, "After") {
		t.Fatalf("expected content around thematic break, got %q", got)
	}
	if !strings.Contains(got, strings.Repeat("─", thematicBreakMaxWidth)) {
		t.Fatalf("expected horizontal rule, got %q", got)
	}
	if strings.Contains(got, strings.Repeat("─", thematicBreakMaxWidth+1)) {
		t.Fatalf("expected capped horizontal rule length, got %q", got)
	}
}

func TestRender_TightListItemsSeparated(t *testing.T) {
	input := "- Alpha\n- Beta\n- Gamma"
	got := Render(input, 80, DefaultStyles())
	lines := strings.Split(got, "\n")
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines for 3 list items, got %d: %q", len(lines), got)
	}
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			t.Fatalf("unexpected blank line in tight list: %q", got)
		}
	}
}

func TestRender_TightOrderedListItemsSeparated(t *testing.T) {
	input := "1. One\n2. Two\n3. Three"
	got := Render(input, 80, DefaultStyles())
	lines := strings.Split(got, "\n")
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines for 3 ordered items, got %d: %q", len(lines), got)
	}
}

func TestRender_HeadingNoHashPrefix(t *testing.T) {
	got := Render("# Title", 80, DefaultStyles())
	if strings.Contains(got, "#") {
		t.Fatalf("heading should not contain '#' prefix, got %q", got)
	}
	if !strings.Contains(got, "Title") {
		t.Fatalf("heading should contain text 'Title', got %q", got)
	}
}

func TestRender_EmptyInput(t *testing.T) {
	got := Render("", 80, DefaultStyles())
	if got != "" {
		t.Fatalf("expected empty output for empty input, got %q", got)
	}
}

func TestRender_StrikethroughDisabled(t *testing.T) {
	input := "This costs ~100 dollars"
	got := Render(input, 80, DefaultStyles())
	if !strings.Contains(got, "~100") {
		t.Fatalf("tilde should be preserved literally, got %q", got)
	}
}

func TestRender_Table(t *testing.T) {
	input := "| Name | Age |\n|------|-----|\n| Alice | 30 |\n| Bob | 25 |"
	got := Render(input, 80, DefaultStyles())
	if !strings.Contains(got, "┌") {
		t.Fatalf("expected box-drawing top border, got %q", got)
	}
	if !strings.Contains(got, "│") {
		t.Fatalf("expected box-drawing vertical border, got %q", got)
	}
	if !strings.Contains(got, "├") {
		t.Fatalf("expected box-drawing separator, got %q", got)
	}
	if !strings.Contains(got, "└") {
		t.Fatalf("expected box-drawing bottom border, got %q", got)
	}
	if !strings.Contains(got, "Alice") || !strings.Contains(got, "Bob") {
		t.Fatalf("expected table cell content, got %q", got)
	}
}

func TestRender_TableAlignment(t *testing.T) {
	input := "| Left | Center | Right |\n|:-----|:------:|------:|\n| a | b | c |"
	got := Render(input, 80, DefaultStyles())
	if !strings.Contains(got, "a") || !strings.Contains(got, "b") || !strings.Contains(got, "c") {
		t.Fatalf("expected aligned cell content, got %q", got)
	}
}

func TestRender_TableFitsTerminalWidth(t *testing.T) {
	// Build a table with a very long cell that would overflow if not wrapped.
	long := strings.Repeat("alpha beta gamma delta ", 5) // ~115 chars
	input := "| Name | Description |\n|------|-------------|\n| Alice | " + long + " |"
	got := Render(input, 50, DefaultStyles())
	for _, line := range strings.Split(got, "\n") {
		if w := strings.Count(line, "│"); w == 0 {
			continue
		}
		// Lines containing │ should fit within terminal width.
		if visW := lipglossWidth(line); visW > 50 {
			t.Errorf("table line exceeds terminal width 50: got %d (%q)", visW, line)
		}
	}
}

func TestRender_TableWrapsLongCells(t *testing.T) {
	// Long enough to wrap but short enough to stay in horizontal mode
	// (≤ maxRowLines after wrapping).
	long := "this cell wraps to two lines for sure"
	input := "| Name | Note |\n|------|------|\n| A | " + long + " |"
	got := Render(input, 30, DefaultStyles())

	// Vertical fallback would have no top box border.
	if !strings.Contains(got, "┌") {
		t.Fatalf("expected horizontal layout, got %q", got)
	}

	// The Note column should wrap, so we expect more than one data
	// row line between the header separator and the bottom border.
	rowCount := 0
	inData := false
	for _, line := range strings.Split(got, "\n") {
		if strings.Contains(line, "├") {
			inData = true
			continue
		}
		if strings.Contains(line, "└") {
			inData = false
			continue
		}
		if inData && strings.Contains(line, "│") {
			rowCount++
		}
	}
	if rowCount < 2 {
		t.Fatalf("expected wrapped row to span multiple lines, got %d lines: %q", rowCount, got)
	}
}

func TestRender_TableVerticalCentering(t *testing.T) {
	// Force one cell to wrap to 3 lines and the other to 1 line —
	// the short cell should be vertically centered (blank above and
	// below).
	long := "alpha beta gamma delta epsilon zeta"
	input := "| Short | Long |\n|-------|------|\n| X | " + long + " |"
	got := Render(input, 30, DefaultStyles())

	lines := strings.Split(got, "\n")
	var dataLines []string
	inData := false
	for _, line := range lines {
		if strings.Contains(line, "├") {
			inData = true
			continue
		}
		if strings.Contains(line, "└") {
			inData = false
			continue
		}
		if inData && strings.Contains(line, "│") {
			dataLines = append(dataLines, line)
		}
	}
	if len(dataLines) < 3 {
		t.Fatalf("expected row to span at least 3 lines, got %d: %q", len(dataLines), got)
	}
	// In a 3-line row with 1-line short cell, X should be on the
	// middle line. So line 0 and line 2 should NOT contain "X".
	if strings.Contains(dataLines[0], "X") {
		t.Errorf("short cell should be vertically centered, but appears on first line: %q", dataLines[0])
	}
	if strings.Contains(dataLines[len(dataLines)-1], "X") {
		t.Errorf("short cell should be vertically centered, but appears on last line: %q", dataLines[len(dataLines)-1])
	}
	// Should appear on the middle line.
	mid := dataLines[len(dataLines)/2]
	if !strings.Contains(mid, "X") {
		t.Errorf("expected X on middle line, got: %q", mid)
	}
}

func TestWrapAnsi_PreservesStyleAcrossLines(t *testing.T) {
	// Hand-crafted ANSI input: bold "hello world example" wrapping to width 7.
	// reflow should re-open the bold sequence on each wrapped line.
	input := "\x1b[1mhello world example\x1b[0m"
	lines := wrapAnsi(input, 7, false)
	if len(lines) < 2 {
		t.Fatalf("expected at least 2 wrapped lines, got %d: %#v", len(lines), lines)
	}
	for i, line := range lines {
		if !strings.Contains(line, "\x1b[") {
			t.Errorf("wrapped line %d lost ANSI sequence: %q", i, line)
		}
	}
}

func TestRender_TableVerticalFallback(t *testing.T) {
	// Tiny terminal forces vertical layout.
	input := "| Name | Description |\n|------|-------------|\n| Alice | a fairly long description that won't fit horizontally in a tiny terminal |"
	got := Render(input, 20, DefaultStyles())
	// Vertical fallback uses key:value format, no top box-drawing border.
	if strings.Contains(got, "┌") {
		t.Errorf("expected vertical fallback (no ┌ border) for narrow terminal, got %q", got)
	}
	if !strings.Contains(got, "Name:") || !strings.Contains(got, "Description:") {
		t.Errorf("expected key:value format, got %q", got)
	}
}

// lipglossWidth is a thin wrapper to keep test imports light.
func lipglossWidth(s string) int {
	return runeDisplayWidth(s)
}

// runeDisplayWidth approximates display width by counting runes,
// treating box-drawing chars as width-1. Good enough for the table
// fit tests above where we just want a coarse upper bound.
func runeDisplayWidth(s string) int {
	n := 0
	for range s {
		n++
	}
	return n
}
