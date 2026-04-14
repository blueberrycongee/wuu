package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMaybePersistResult_UnderThreshold(t *testing.T) {
	result := "short output"
	got := MaybePersistResult("", "test_tool", "call-1", result, 100)
	if got != result {
		t.Errorf("expected passthrough, got %q", got)
	}
}

func TestMaybePersistResult_OverThreshold_NoSessionDir(t *testing.T) {
	result := strings.Repeat("x", 200)
	got := MaybePersistResult("", "test_tool", "call-1", result, 100)
	if !strings.Contains(got, "[truncated") {
		t.Errorf("expected truncation fallback, got %q", got)
	}
	if len(got) > 200 {
		t.Errorf("expected truncated output, got length %d", len(got))
	}
}

func TestMaybePersistResult_OverThreshold_WithSessionDir(t *testing.T) {
	sessionDir := t.TempDir()
	result := strings.Repeat("a", 200) + strings.Repeat("z", 200)

	got := MaybePersistResult(sessionDir, "shell", "call-42", result, 100)

	// Should contain the reference markers.
	if !strings.Contains(got, "saved to disk") {
		t.Fatalf("expected disk reference, got:\n%s", got)
	}
	if !strings.Contains(got, "read_file") {
		t.Error("expected read_file instruction in reference")
	}

	// Verify file was written.
	path := filepath.Join(sessionDir, "tool-results", "call-42.txt")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("expected persisted file at %s: %v", path, err)
	}
	if string(data) != result {
		t.Error("persisted content doesn't match original")
	}
}

func TestMaybePersistResult_DefaultThreshold(t *testing.T) {
	// Under default threshold (50K) should pass through.
	result := strings.Repeat("x", 40_000)
	got := MaybePersistResult("", "test", "c1", result, 0)
	if got != result {
		t.Error("expected passthrough for result under default threshold")
	}

	// Over default threshold should trigger persistence or truncation.
	big := strings.Repeat("x", 60_000)
	got = MaybePersistResult("", "test", "c2", big, 0)
	if got == big {
		t.Error("expected result to be modified when over default threshold")
	}
}

func TestBuildReference_Preview(t *testing.T) {
	content := strings.Repeat("H", 3000) + strings.Repeat("T", 2000)
	ref := buildReference("/tmp/result.txt", content, len(content))

	if !strings.Contains(ref, "/tmp/result.txt") {
		t.Error("reference should contain file path")
	}
	if !strings.Contains(ref, "5000 chars") {
		t.Error("reference should contain size")
	}
	// Head preview should be present.
	if !strings.Contains(ref, "first ~2000") {
		t.Error("reference should contain head preview marker")
	}
	// Tail preview should be present for large content.
	if !strings.Contains(ref, "last ~1000") {
		t.Error("reference should contain tail preview marker")
	}
}
