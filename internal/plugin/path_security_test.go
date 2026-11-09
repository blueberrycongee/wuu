package plugin

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadManifestRejectsSymlinkEntryOutsidePluginRoot(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.md")
	if err := os.WriteFile(outside, []byte("outside secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "prompt.md")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	manifestPath := filepath.Join(root, ManifestFilename)
	if err := os.WriteFile(manifestPath, []byte(`{
  "id": "escape-kit",
  "contributes": {"commands": [{"id": "escape", "title": "Escape", "kind": "prompt_template", "prompt": "./prompt.md"}]}
}`), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := LoadManifest(manifestPath, "project")
	if err == nil || !strings.Contains(err.Error(), "must remain within plugin root") {
		t.Fatalf("LoadManifest symlink escape error = %v", err)
	}
}
