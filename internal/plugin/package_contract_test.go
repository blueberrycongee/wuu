package plugin

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/blueberrycongee/wuu/internal/extensions"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
)

func TestPackageContractTracksEntryContentButNotEnvironmentValues(t *testing.T) {
	root := t.TempDir()
	entry := filepath.Join(root, "bin", "plugin")
	if err := os.MkdirAll(filepath.Dir(entry), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(entry, []byte("version-one"), 0o755); err != nil {
		t.Fatal(err)
	}
	item := Plugin{
		Manifest: Manifest{
			ID: "runtime-kit",
			Runtime: &RuntimeSpec{
				Protocol: pluginhost.ProtocolName,
				Command:  entry,
				Env:      map[string]string{"PLUGIN_TOKEN": "secret-one"},
			},
			RuntimePath: "bin/plugin",
		},
		Source: "project",
		Root:   root,
	}
	first, err := item.PackageContract()
	if err != nil {
		t.Fatal(err)
	}
	if first.SubjectID != "plugin:project:runtime-kit" || !reflect.DeepEqual(first.Permissions, []string{extensions.PermProcessSpawn}) {
		t.Fatalf("package contract = %+v", first)
	}

	item.Runtime.Env["PLUGIN_TOKEN"] = "secret-two"
	secretChanged, err := item.PackageContract()
	if err != nil {
		t.Fatal(err)
	}
	if secretChanged.Fingerprint != first.Fingerprint {
		t.Fatal("secret value unexpectedly changed package fingerprint")
	}

	if err := os.WriteFile(entry, []byte("version-two"), 0o755); err != nil {
		t.Fatal(err)
	}
	entryChanged, err := item.PackageContract()
	if err != nil {
		t.Fatal(err)
	}
	if entryChanged.Fingerprint == first.Fingerprint {
		t.Fatal("runtime entry content did not change package fingerprint")
	}
}

func TestPackageContractTracksEveryRegularPackageFile(t *testing.T) {
	root := t.TempDir()
	manifestPath := filepath.Join(root, ManifestFilename)
	chunkPath := filepath.Join(root, "dist", "desktop-chunk.js")
	if err := os.MkdirAll(filepath.Dir(chunkPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifestPath, []byte(`{"id":"desktop-kit"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(chunkPath, []byte("version one"), 0o644); err != nil {
		t.Fatal(err)
	}

	item, err := LoadManifest(manifestPath, "user")
	if err != nil {
		t.Fatal(err)
	}
	first := item.Fingerprint

	if err := os.WriteFile(chunkPath, []byte("version two"), 0o644); err != nil {
		t.Fatal(err)
	}
	second, err := item.PackageContract()
	if err != nil {
		t.Fatal(err)
	}
	if second.Fingerprint == first {
		t.Fatal("transitive desktop chunk did not change package fingerprint")
	}
}
