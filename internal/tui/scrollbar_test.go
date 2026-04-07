package tui

import (
	"strings"
	"testing"
)

func TestScrollbarContentFitsViewport(t *testing.T) {
	// When content fits in viewport, no scrollbar should be shown.
	result := renderScrollbar(10, 10, 10, 0)
	if result != "" {
		t.Fatalf("expected empty string when content fits viewport, got %q", result)
	}
}

func TestScrollbarContentSmallerThanViewport(t *testing.T) {
	result := renderScrollbar(10, 5, 10, 0)
	if result != "" {
		t.Fatalf("expected empty string when content < viewport, got %q", result)
	}
}

func TestScrollbarZeroHeight(t *testing.T) {
	result := renderScrollbar(0, 20, 10, 0)
	if result != "" {
		t.Fatalf("expected empty string for zero height, got %q", result)
	}
}

func TestScrollbarBasicRendering(t *testing.T) {
	// 10-line viewport, 20 lines of content, at top.
	result := renderScrollbar(10, 20, 10, 0)
	if result == "" {
		t.Fatal("expected non-empty scrollbar")
	}
	lines := strings.Split(result, "\n")
	if len(lines) != 10 {
		t.Fatalf("expected 10 lines, got %d", len(lines))
	}
}

func TestScrollbarThumbAtTop(t *testing.T) {
	// At offset 0, thumb should start at line 0.
	result := renderScrollbar(10, 20, 10, 0)
	lines := strings.Split(result, "\n")
	// First line should be thumb.
	if !strings.Contains(lines[0], scrollbarThumb) {
		t.Fatalf("expected thumb at line 0, got %q", lines[0])
	}
}

func TestScrollbarThumbAtBottom(t *testing.T) {
	// At max offset, thumb should end at last line.
	result := renderScrollbar(10, 20, 10, 10) // offset = contentSize - viewportSize
	lines := strings.Split(result, "\n")
	lastLine := lines[len(lines)-1]
	if !strings.Contains(lastLine, scrollbarThumb) {
		t.Fatalf("expected thumb at last line, got %q", lastLine)
	}
}

func TestScrollbarThumbMinSize(t *testing.T) {
	// Very large content: thumb should be at least 1 character.
	result := renderScrollbar(10, 1000, 10, 0)
	lines := strings.Split(result, "\n")
	thumbCount := 0
	for _, line := range lines {
		if strings.Contains(line, scrollbarThumb) {
			thumbCount++
		}
	}
	if thumbCount < 1 {
		t.Fatal("expected at least 1 thumb line")
	}
}

func TestScrollbarThumbProportional(t *testing.T) {
	// 20-line viewport, 40 lines of content: thumb should be ~10 lines (half).
	result := renderScrollbar(20, 40, 20, 0)
	lines := strings.Split(result, "\n")
	thumbCount := 0
	for _, line := range lines {
		if strings.Contains(line, scrollbarThumb) {
			thumbCount++
		}
	}
	if thumbCount != 10 {
		t.Fatalf("expected 10 thumb lines for 50%% ratio, got %d", thumbCount)
	}
}

func TestScrollbarTrackLines(t *testing.T) {
	// Non-thumb lines should be track.
	result := renderScrollbar(10, 20, 10, 0)
	lines := strings.Split(result, "\n")
	for _, line := range lines {
		if !strings.Contains(line, scrollbarThumb) && !strings.Contains(line, scrollbarTrack) {
			t.Fatalf("line should be either thumb or track, got %q", line)
		}
	}
}
