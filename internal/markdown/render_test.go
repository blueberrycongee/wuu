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
	if !strings.Contains(got, "─") {
		t.Fatalf("expected horizontal rule, got %q", got)
	}
}

func TestRender_EmptyInput(t *testing.T) {
	got := Render("", 80, DefaultStyles())
	if got != "" {
		t.Fatalf("expected empty output for empty input, got %q", got)
	}
}
