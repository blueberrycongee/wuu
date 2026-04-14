package markdown

import (
	"strings"
	"testing"
)

func TestStreamCollector_Progressive(t *testing.T) {
	c := NewStreamCollector(80, DefaultStyles())

	// Push partial line — no commit yet.
	c.Push("Hello ")
	out := c.CommitCompleteLines()
	if out != "" {
		t.Fatalf("expected empty for partial input, got %q", out)
	}

	// Complete the line.
	c.Push("world\n")
	out = c.CommitCompleteLines()
	if out == "" {
		t.Fatal("expected output after newline")
	}
	if !strings.Contains(out, "Hello world") {
		t.Fatalf("expected 'Hello world', got %q", out)
	}

	// Push another line — full output includes both lines.
	c.Push("Second line\n")
	out = c.CommitCompleteLines()
	if !strings.Contains(out, "Hello world") || !strings.Contains(out, "Second line") {
		t.Fatalf("expected full output with both lines, got %q", out)
	}
}

func TestStreamCollector_Finalize(t *testing.T) {
	c := NewStreamCollector(80, DefaultStyles())
	c.Push("No trailing newline")
	out := c.CommitCompleteLines()
	if out != "" {
		t.Fatalf("expected empty without newline, got %q", out)
	}

	// Finalize should flush remaining content.
	out = c.Finalize()
	if out == "" {
		t.Fatal("expected finalize to produce output")
	}
	if !strings.Contains(out, "No trailing newline") {
		t.Fatalf("expected content in finalize, got %q", out)
	}
}

func TestStreamCollector_CodeBlock(t *testing.T) {
	c := NewStreamCollector(80, DefaultStyles())
	c.Push("```go\n")
	c.Push("package main\n")
	c.Push("```\n")
	out := c.CommitCompleteLines()
	if out == "" {
		t.Fatal("expected code block output")
	}
	if !strings.Contains(out, "package") {
		t.Fatalf("expected code block content, got %q", out)
	}
}

func TestStreamCollector_CommitWithTrailing_PartialLine(t *testing.T) {
	c := NewStreamCollector(80, DefaultStyles())

	// Push partial line — CommitWithTrailing should return it immediately.
	c.Push("Hello ")
	out := c.CommitWithTrailing()
	if out == "" {
		t.Fatal("expected partial text from CommitWithTrailing, got empty")
	}
	if !strings.Contains(out, "Hello") {
		t.Fatalf("expected 'Hello' in output, got %q", out)
	}

	// Push more — trailing grows.
	c.Push("world")
	out = c.CommitWithTrailing()
	if !strings.Contains(out, "Hello world") {
		t.Fatalf("expected 'Hello world' in trailing, got %q", out)
	}
}

func TestStreamCollector_CommitWithTrailing_CompletePlusPartial(t *testing.T) {
	c := NewStreamCollector(80, DefaultStyles())

	// Push a complete line + partial next line.
	c.Push("First line\nSecond ")
	out := c.CommitWithTrailing()
	if out == "" {
		t.Fatal("expected output, got empty")
	}
	// Should contain the rendered first line AND the raw trailing text.
	if !strings.Contains(out, "First line") {
		t.Fatalf("expected 'First line' in output, got %q", out)
	}
	if !strings.Contains(out, "Second") {
		t.Fatalf("expected trailing 'Second' in output, got %q", out)
	}
}

func TestStreamCollector_CommitWithTrailing_NoTrailingAfterNewline(t *testing.T) {
	c := NewStreamCollector(80, DefaultStyles())

	// Push content ending with newline — no trailing partial.
	c.Push("Complete line\n")
	out := c.CommitWithTrailing()
	if out == "" {
		t.Fatal("expected output, got empty")
	}
	if !strings.Contains(out, "Complete line") {
		t.Fatalf("expected 'Complete line' in output, got %q", out)
	}
}

func TestStreamCollector_CommitWithTrailing_Empty(t *testing.T) {
	c := NewStreamCollector(80, DefaultStyles())
	out := c.CommitWithTrailing()
	if out != "" {
		t.Fatalf("expected empty for empty buffer, got %q", out)
	}
}

func TestStreamCollector_TableStreaming(t *testing.T) {
	c := NewStreamCollector(80, DefaultStyles())

	// Stream in a table line by line.
	c.Push("| Name | Age |\n")
	out1 := c.CommitCompleteLines()
	// First line alone — goldmark sees it as a paragraph, not a table.
	// That's fine; it will be replaced.

	c.Push("|------|-----|\n")
	out2 := c.CommitCompleteLines()
	// Now goldmark recognizes a table. The output should contain
	// box-drawing characters, replacing the previous paragraph.
	if !strings.Contains(out2, "┌") {
		t.Fatalf("expected table after separator, got %q (prev: %q)", out2, out1)
	}

	c.Push("| Alice | 30 |\n")
	out3 := c.CommitCompleteLines()
	if !strings.Contains(out3, "Alice") {
		t.Fatalf("expected table with data, got %q", out3)
	}
	// Full output replaces previous — should still have box-drawing.
	if !strings.Contains(out3, "┌") {
		t.Fatalf("expected complete table with borders, got %q", out3)
	}
}
