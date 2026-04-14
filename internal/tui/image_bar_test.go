package tui

import (
	"strings"
	"testing"
)

func TestRenderImageBar_Empty(t *testing.T) {
	got := renderImageBar(0, 0, false, 80)
	if got != "" {
		t.Fatalf("expected empty string for no images, got %q", got)
	}
}

func TestRenderImageBar_SingleImage(t *testing.T) {
	got := renderImageBar(1, 0, false, 80)
	if !strings.Contains(got, "[Image #1]") {
		t.Fatalf("expected [Image #1] in output, got %q", got)
	}
	if !strings.Contains(got, "↓:select") {
		t.Fatalf("expected ↓:select hint, got %q", got)
	}
}

func TestRenderImageBar_MultipleImages(t *testing.T) {
	got := renderImageBar(3, 0, false, 120)
	for i := 1; i <= 3; i++ {
		pill := "[Image #" + string(rune('0'+i)) + "]"
		if !strings.Contains(got, pill) {
			t.Fatalf("expected %s in output, got %q", pill, got)
		}
	}
}

func TestRenderImageBar_FocusedHints(t *testing.T) {
	got := renderImageBar(2, 0, true, 120)
	if !strings.Contains(got, "backspace:remove") {
		t.Fatalf("expected backspace:remove in focused mode, got %q", got)
	}
	if !strings.Contains(got, "←→:navigate") {
		t.Fatalf("expected ←→:navigate in focused mode with multiple images, got %q", got)
	}
	if !strings.Contains(got, "esc:back") {
		t.Fatalf("expected esc:back in focused mode, got %q", got)
	}
}

func TestRenderImageBar_SingleImageFocusedNoNavigate(t *testing.T) {
	got := renderImageBar(1, 0, true, 120)
	if strings.Contains(got, "←→:navigate") {
		t.Fatalf("should not show navigate hint for single image, got %q", got)
	}
	if !strings.Contains(got, "backspace:remove") {
		t.Fatalf("expected backspace:remove, got %q", got)
	}
}
