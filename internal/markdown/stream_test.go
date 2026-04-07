package markdown

import (
	"testing"
)

func TestStreamCollector_Progressive(t *testing.T) {
	c := NewStreamCollector(80, DefaultStyles())

	// Push partial line — no commit yet.
	c.Push("Hello ")
	lines := c.CommitCompleteLines()
	if len(lines) != 0 {
		t.Fatalf("expected no lines for partial input, got %v", lines)
	}

	// Complete the line.
	c.Push("world\n")
	lines = c.CommitCompleteLines()
	if len(lines) == 0 {
		t.Fatal("expected at least one line after newline")
	}

	// Push another line.
	c.Push("Second line\n")
	lines = c.CommitCompleteLines()
	if len(lines) == 0 {
		t.Fatal("expected incremental line")
	}
}

func TestStreamCollector_Finalize(t *testing.T) {
	c := NewStreamCollector(80, DefaultStyles())
	c.Push("No trailing newline")
	lines := c.CommitCompleteLines()
	if len(lines) != 0 {
		t.Fatalf("expected no lines without newline, got %v", lines)
	}

	// Finalize should flush remaining content.
	lines = c.Finalize()
	if len(lines) == 0 {
		t.Fatal("expected finalize to produce lines")
	}
}

func TestStreamCollector_CodeBlock(t *testing.T) {
	c := NewStreamCollector(80, DefaultStyles())
	c.Push("```go\n")
	c.Push("package main\n")
	c.Push("```\n")
	lines := c.CommitCompleteLines()
	if len(lines) == 0 {
		t.Fatal("expected code block lines")
	}
}
