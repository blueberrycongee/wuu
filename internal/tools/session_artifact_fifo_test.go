//go:build unix

package tools

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestSessionArtifactRootRejectsFIFO(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session")
	root, err := newSessionArtifactRoot(path)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if err := os.Mkdir(filepath.Join(path, "tool-results"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(filepath.Join(path, "tool-results", "pipe"), 0600); err != nil {
		t.Fatal(err)
	}
	if f, err := root.open("tool-results/pipe"); err == nil {
		f.Close()
		t.Fatal("opened FIFO as an artifact")
	}
}
