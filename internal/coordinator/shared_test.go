package coordinator

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEnsureSharedDir_CreatesAllSubdirs(t *testing.T) {
	root := t.TempDir()

	if err := EnsureSharedDir(root); err != nil {
		t.Fatalf("EnsureSharedDir failed: %v", err)
	}

	for _, sub := range SharedSubdirs {
		path := filepath.Join(root, ".wuu", SharedDirName, sub)
		info, err := os.Stat(path)
		if err != nil {
			t.Errorf("expected %s to exist: %v", path, err)
			continue
		}
		if !info.IsDir() {
			t.Errorf("expected %s to be a directory", path)
		}
	}
}

func TestEnsureSharedDir_IdempotentOnExisting(t *testing.T) {
	root := t.TempDir()

	// First call creates everything.
	if err := EnsureSharedDir(root); err != nil {
		t.Fatalf("first EnsureSharedDir failed: %v", err)
	}

	// Drop a file inside one of the subdirs to simulate a worker
	// having written something. The second call must NOT touch it.
	canary := filepath.Join(root, ".wuu", SharedDirName, "findings", "auth.md")
	if err := os.WriteFile(canary, []byte("worker output"), 0o644); err != nil {
		t.Fatalf("write canary: %v", err)
	}

	// Second call should be a no-op for existing dirs.
	if err := EnsureSharedDir(root); err != nil {
		t.Fatalf("second EnsureSharedDir failed: %v", err)
	}

	// Canary file is still there with original content.
	got, err := os.ReadFile(canary)
	if err != nil {
		t.Fatalf("read canary: %v", err)
	}
	if string(got) != "worker output" {
		t.Errorf("canary content changed: got %q", got)
	}
}

func TestSharedDirPath_ReturnsExpectedPath(t *testing.T) {
	got := SharedDirPath("/tmp/myproject")
	want := filepath.Join("/tmp/myproject", ".wuu", "shared")
	if got != want {
		t.Errorf("SharedDirPath = %q, want %q", got, want)
	}
}
