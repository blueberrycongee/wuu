package markdown

import (
	"strings"
	"testing"
)

func TestStreamCollector_CommitRendersMarkdown(t *testing.T) {
	c := NewStreamCollector(80, DefaultStyles())

	// Push partial line — Commit renders markdown (may trim trailing space).
	c.Push("Hello ")
	if !c.Dirty() {
		t.Fatal("expected dirty after Push")
	}
	out := c.Commit()
	if !strings.Contains(out, "Hello") {
		t.Fatalf("expected 'Hello' in rendered output, got %q", out)
	}
	if c.Dirty() {
		t.Fatal("expected clean after Commit")
	}

	// Push more — full accumulated text returned.
	c.Push("world")
	out = c.Commit()
	if !strings.Contains(out, "Hello") || !strings.Contains(out, "world") {
		t.Fatalf("expected 'Hello' and 'world' in output, got %q", out)
	}
}

func TestStreamCollector_DirtyTracking(t *testing.T) {
	c := NewStreamCollector(80, DefaultStyles())
	if c.Dirty() {
		t.Fatal("new collector should not be dirty")
	}

	c.Push("x")
	if !c.Dirty() {
		t.Fatal("expected dirty after Push")
	}

	c.Commit()
	if c.Dirty() {
		t.Fatal("expected clean after Commit")
	}

	// No push → not dirty.
	c.Push("")
	// Empty push still sets dirty (by design — caller decides).
}

func TestStreamCollector_CommitRendersCodeBlock(t *testing.T) {
	c := NewStreamCollector(80, DefaultStyles())
	c.Push("```go\npackage main\n```\n")

	// Commit now renders markdown (not raw), so code should be present.
	out := c.Commit()
	if !strings.Contains(out, "package") {
		t.Fatalf("expected code content in commit, got %q", out)
	}
}

func TestStreamCollector_Finalize_RendersMarkdown(t *testing.T) {
	c := NewStreamCollector(80, DefaultStyles())
	c.Push("**bold** text\n")

	final := c.Finalize()
	if final == "" {
		t.Fatal("expected finalize output")
	}
	if !strings.Contains(final, "bold") {
		t.Fatalf("expected 'bold' in finalize, got %q", final)
	}
}

func TestStreamCollector_Finalize_NoTrailingNewline(t *testing.T) {
	c := NewStreamCollector(80, DefaultStyles())
	c.Push("No trailing newline")
	out := c.Finalize()
	if out == "" {
		t.Fatal("expected finalize output")
	}
	if !strings.Contains(out, "No trailing newline") {
		t.Fatalf("expected content in finalize, got %q", out)
	}
}

func TestStreamCollector_Finalize_Empty(t *testing.T) {
	c := NewStreamCollector(80, DefaultStyles())
	out := c.Finalize()
	if out != "" {
		t.Fatalf("expected empty for empty buffer, got %q", out)
	}
}

func TestStreamCollector_Commit_Empty(t *testing.T) {
	c := NewStreamCollector(80, DefaultStyles())
	out := c.Commit()
	if out != "" {
		t.Fatalf("expected empty for empty buffer, got %q", out)
	}
}

func TestStreamCollector_MultilineThenFinalize(t *testing.T) {
	c := NewStreamCollector(80, DefaultStyles())

	c.Push("Line one\n")
	c.Push("Line two\n")
	c.Push("Line three")

	// Commit renders all accumulated text.
	rendered := c.Commit()
	if !strings.Contains(rendered, "Line one") || !strings.Contains(rendered, "Line three") {
		t.Fatalf("expected all lines in rendered, got %q", rendered)
	}

	// Finalize also renders markdown.
	final := c.Finalize()
	if !strings.Contains(final, "Line one") || !strings.Contains(final, "Line three") {
		t.Fatalf("expected all lines in final, got %q", final)
	}
}

func TestStreamCollector_CommitAndFinalizeConsistent(t *testing.T) {
	// Commit and Finalize should produce the same rendered output for
	// the same input — no visual jump between streaming and final.
	c := NewStreamCollector(80, DefaultStyles())
	c.Push("Hello **world**\n")

	committed := c.Commit()
	finalized := c.Finalize()

	// Both should contain "world" with bold rendering.
	if !strings.Contains(committed, "world") {
		t.Fatalf("committed missing 'world': %q", committed)
	}
	if !strings.Contains(finalized, "world") {
		t.Fatalf("finalized missing 'world': %q", finalized)
	}
}
