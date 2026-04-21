package markdown

import (
	"strings"
	"testing"
)

func TestStreamCollector_CommitRendersMarkdown(t *testing.T) {
	c := NewStreamCollector(80, DefaultStyles())

	// Push partial line — no newline yet, Commit returns nil.
	c.Push("Hello ")
	if !c.Dirty() {
		t.Fatal("expected dirty after Push")
	}
	out := c.Commit()
	if out != nil {
		t.Fatalf("expected nil for partial line without newline, got %v", out)
	}
	if c.Dirty() {
		t.Fatal("expected clean after Commit")
	}

	// Push more with newline — Commit returns the completed line.
	c.Push("world\n")
	out = c.Commit()
	if len(out) != 1 || !strings.Contains(out[0], "Hello") || !strings.Contains(out[0], "world") {
		t.Fatalf("expected [\"Hello world\"], got %v", out)
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

	out := c.Commit()
	if len(out) == 0 {
		t.Fatalf("expected some lines, got nil")
	}
	found := false
	for _, line := range out {
		if strings.Contains(line, "package") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected code content in commit, got %v", out)
	}
}

func TestStreamCollector_Finalize_RendersMarkdown(t *testing.T) {
	c := NewStreamCollector(80, DefaultStyles())
	c.Push("**bold** text\n")

	final := c.Finalize()
	if len(final) == 0 {
		t.Fatal("expected finalize output")
	}
	if !strings.Contains(final[0], "bold") {
		t.Fatalf("expected 'bold' in finalize, got %v", final)
	}
	// Verify markdown was actually rendered (no raw **).
	if strings.Contains(final[0], "**") {
		t.Fatalf("expected ** to be stripped, got %q", final[0])
	}
}

func TestStreamCollector_Finalize_NoTrailingNewline(t *testing.T) {
	c := NewStreamCollector(80, DefaultStyles())
	c.Push("No trailing newline")
	out := c.Finalize()
	if len(out) == 0 {
		t.Fatal("expected finalize output")
	}
	if !strings.Contains(out[0], "No trailing newline") {
		t.Fatalf("expected content in finalize, got %v", out)
	}
}

func TestStreamCollector_Finalize_Empty(t *testing.T) {
	c := NewStreamCollector(80, DefaultStyles())
	out := c.Finalize()
	if out != nil {
		t.Fatalf("expected nil for empty buffer, got %v", out)
	}
}

func TestStreamCollector_Commit_Empty(t *testing.T) {
	c := NewStreamCollector(80, DefaultStyles())
	out := c.Commit()
	if out != nil {
		t.Fatalf("expected nil for empty buffer, got %v", out)
	}
}

func TestStreamCollector_MultilineThenFinalize(t *testing.T) {
	c := NewStreamCollector(80, DefaultStyles())

	c.Push("Line one\n")
	c.Push("Line two\n")
	c.Push("Line three")

	// Commit returns only the first two lines (they end with \n).
	rendered := c.Commit()
	if len(rendered) != 2 {
		t.Fatalf("expected 2 lines, got %d: %v", len(rendered), rendered)
	}
	if !strings.Contains(rendered[0], "Line one") || !strings.Contains(rendered[1], "Line two") {
		t.Fatalf("expected Line one and Line two, got %v", rendered)
	}

	// Finalize returns the remaining third line.
	final := c.Finalize()
	if len(final) != 1 || !strings.Contains(final[0], "Line three") {
		t.Fatalf("expected [\"Line three\"], got %v", final)
	}
}

func TestStreamCollector_CommitAndFinalizeConsistent(t *testing.T) {
	// Commit and Finalize should produce the same rendered output for
	// the same input — no visual jump between streaming and final.
	c := NewStreamCollector(80, DefaultStyles())
	c.Push("Hello **world**\n")

	committed := c.Commit()
	finalized := c.Finalize()

	// Both should contain "world" with bold rendering (no **).
	if len(committed) != 1 || !strings.Contains(committed[0], "world") {
		t.Fatalf("committed missing 'world': %v", committed)
	}
	if len(finalized) != 0 {
		// Since the line was already committed, Finalize should return nothing.
		t.Fatalf("expected no new lines from Finalize after full commit, got %v", finalized)
	}
	if strings.Contains(committed[0], "**") {
		t.Fatalf("expected ** to be stripped, got %q", committed[0])
	}
}

func TestStreamCollector_NoCommitWithoutNewline(t *testing.T) {
	c := NewStreamCollector(80, DefaultStyles())
	c.Push("partial line without newline")
	out := c.Commit()
	if out != nil {
		t.Fatalf("expected nil for line without newline, got %v", out)
	}
}

func TestStreamCollector_IncrementalOnlyNewLines(t *testing.T) {
	c := NewStreamCollector(80, DefaultStyles())

	c.Push("Line1\n")
	out1 := c.Commit()
	if len(out1) != 1 || !strings.Contains(out1[0], "Line1") {
		t.Fatalf("expected [\"Line1\"], got %v", out1)
	}

	c.Push("Line2\n")
	out2 := c.Commit()
	if len(out2) != 1 || !strings.Contains(out2[0], "Line2") {
		t.Fatalf("expected [\"Line2\"], got %v", out2)
	}
	// Should NOT return Line1 again.
	for _, line := range out2 {
		if strings.Contains(line, "Line1") {
			t.Fatalf("expected only Line2, got Line1 in output: %v", out2)
		}
	}
}
