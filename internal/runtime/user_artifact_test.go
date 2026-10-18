package runtime

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/blueberrycongee/wuu/internal/tools"
	"github.com/blueberrycongee/wuu/internal/userspace"
)

func TestUserHostArtifactsPublishAndReadWithinProductDataRoot(t *testing.T) {
	home := t.TempDir()
	b := userspace.Binding{UserSpaceID: "local", ProductDomain: "personal"}
	dataRoot := b.State(home)
	stateDir, err := userHostWorkspaceStateDir(home, b.Root(home), b)
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "result.txt")
	if err := os.WriteFile(file, []byte("synthetic scoped artifact"), 0600); err != nil {
		t.Fatal(err)
	}
	part, err := newArtifactPublisher(dataRoot)(context.Background(), tools.ArtifactPublishRequest{Path: file, StateDir: stateDir, ThreadID: "main", CallID: "synthetic", CWD: filepath.Dir(file)})
	if err != nil {
		t.Fatal(err)
	}
	content, _, err := ReadManagedArtifact(dataRoot, stateDir, "main", part.URI, 1024)
	if err != nil || string(content) != "synthetic scoped artifact" {
		t.Fatalf("read: %q %v", content, err)
	}
	if _, _, err := ReadManagedArtifact(home, stateDir, "main", part.URI, 1024); err == nil {
		t.Fatal("Coding root read a Personal artifact")
	}
}
