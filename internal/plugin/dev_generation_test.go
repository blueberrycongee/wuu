package plugin

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestDiscoverAuthorizedDevGenerationRejectsUnreceiptedAndTamperedPackages(t *testing.T) {
	home := t.TempDir()
	source := t.TempDir()
	writeDevGenerationTestFile(t, filepath.Join(source, "plugin.json"), `{"schema_version":1,"id":"dev-discovery","version":"1.0.0","desktop":{"entry":"desktop.js"}}`)
	writeDevGenerationTestFile(t, filepath.Join(source, "desktop.js"), "export const version = 'one';")

	legacySpoof := filepath.Join(home, "dev", "runtime", "plugins", "dev-discovery")
	writeDevGenerationTestFile(t, filepath.Join(legacySpoof, "plugin.json"), `{"schema_version":1,"id":"dev-discovery","version":"9.0.0"}`)
	unreceipted := filepath.Join(home, "dev", "generations", "dev-discovery", "package")
	writeDevGenerationTestFile(t, filepath.Join(unreceipted, "plugin.json"), `{"schema_version":1,"id":"dev-discovery","version":"9.0.0"}`)
	if found := findDevGenerationTestPlugin(Discover("", home), "dev-discovery"); found != nil {
		t.Fatalf("unreceipted dev package was discovered: %+v", found)
	}

	authorization := writeDevGenerationTestAuthorization(t, home, "dev-discovery", source)
	published, err := PublishDevGeneration(home, source, source, authorization)
	if err != nil {
		t.Fatalf("publish authorized generation: %v", err)
	}
	found := findDevGenerationTestPlugin(Discover("", home), "dev-discovery")
	if found == nil || !found.AuthorizedDev || found.Fingerprint != published.Fingerprint || found.Source != "dev" {
		t.Fatalf("authorized dev generation was not discovered: %+v", found)
	}

	writeDevGenerationTestFile(t, filepath.Join(found.Root, "desktop.js"), "export const version = 'tampered';")
	if found := findDevGenerationTestPlugin(Discover("", home), "dev-discovery"); found != nil {
		t.Fatalf("tampered dev generation was discovered: %+v", found)
	}
}

func TestPublishDevGenerationFailurePreservesPreviousGeneration(t *testing.T) {
	home := t.TempDir()
	source := t.TempDir()
	writeDevGenerationTestFile(t, filepath.Join(source, "plugin.json"), `{"schema_version":1,"id":"dev-rollback","version":"1.0.0","desktop":{"entry":"desktop.js"}}`)
	writeDevGenerationTestFile(t, filepath.Join(source, "desktop.js"), "export const version = 'stable';")
	authorization := writeDevGenerationTestAuthorization(t, home, "dev-rollback", source)
	first, err := PublishDevGeneration(home, source, source, authorization)
	if err != nil {
		t.Fatal(err)
	}

	invalid := t.TempDir()
	writeDevGenerationTestFile(t, filepath.Join(invalid, "plugin.json"), `{"schema_version":1,"id":"wrong-id","version":"2.0.0"}`)
	if _, err := PublishDevGeneration(home, source, invalid, authorization); err == nil {
		t.Fatal("invalid replacement succeeded")
	}
	found := findDevGenerationTestPlugin(Discover("", home), "dev-rollback")
	if found == nil || found.Fingerprint != first.Fingerprint {
		t.Fatalf("failed publish did not preserve previous generation: %+v", found)
	}
}

// Model process termination at the exact rename boundary: no deferred cleanup
// runs and a fresh Discover must recover solely from the persisted files.
func TestDiscoverDevGenerationAfterInterruptedPublish(t *testing.T) {
	for _, scenario := range []string{"interrupted", "completed", "tampered", "signature-tampered", "revoked", "source-changed", "invalid-current", "unpublished", "ambiguous"} {
		t.Run(scenario, func(t *testing.T) {
			home, source := t.TempDir(), t.TempDir()
			const id = "dev-interrupted"
			writeDevGenerationTestFile(t, filepath.Join(source, "plugin.json"), `{"schema_version":1,"id":"dev-interrupted","version":"1.0.0","desktop":{"entry":"desktop.js"},"requested_permissions":["network"]}`)
			writeDevGenerationTestFile(t, filepath.Join(source, "desktop.js"), "export const version = 'stable';")
			authorization := writeDevGenerationTestAuthorization(t, home, id, source)
			first, err := PublishDevGeneration(home, source, source, authorization)
			if err != nil {
				t.Fatal(err)
			}
			root := filepath.Join(home, "dev", "generations")
			destination, backup := filepath.Join(root, id), filepath.Join(root, ".previous-interrupted")
			if err := os.Rename(destination, backup); err != nil {
				t.Fatal(err)
			}
			want := &first
			switch scenario {
			case "completed", "ambiguous":
				writeDevGenerationTestFile(t, filepath.Join(source, "desktop.js"), "export const version = 'new';")
				next, err := PublishDevGeneration(home, source, source, authorization)
				if err != nil {
					t.Fatal(err)
				}
				want = &next
				if scenario == "ambiguous" {
					if err := os.Rename(destination, filepath.Join(root, ".previous-second")); err != nil {
						t.Fatal(err)
					}
					want = nil
				}
			case "tampered":
				writeDevGenerationTestFile(t, filepath.Join(backup, "package", "desktop.js"), "tampered")
				want = nil
			case "signature-tampered":
				path := filepath.Join(backup, devGenerationReceiptFile)
				data, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				var receipt devGenerationReceipt
				if err := json.Unmarshal(data, &receipt); err != nil {
					t.Fatal(err)
				}
				receipt.Signature = "00"
				data, err = json.Marshal(receipt)
				if err != nil {
					t.Fatal(err)
				}
				writeDevGenerationTestFile(t, path, string(data))
				want = nil
			case "revoked":
				path, _ := DevAuthorizationPath(home, id)
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				want = nil
			case "source-changed":
				writeDevGenerationTestAuthorization(t, home, id, t.TempDir())
				want = nil
			case "invalid-current":
				writeDevGenerationTestFile(t, filepath.Join(destination, "receipt.json"), "invalid")
				want = nil
			case "unpublished":
				if err := os.Rename(backup, filepath.Join(root, ".publish-uncommitted")); err != nil {
					t.Fatal(err)
				}
				want = nil
			}
			untouched := filepath.Join(root, ".unrelated", "keep")
			writeDevGenerationTestFile(t, untouched, "leave alone")
			found := findDevGenerationTestPlugin(Discover("", home), id)
			if want == nil {
				if found != nil {
					t.Fatalf("unsafe recovery: %+v", found)
				}
			} else if found == nil || found.Fingerprint != want.Fingerprint || found.SubjectID != want.SubjectID || !reflect.DeepEqual(found.EffectivePermissions, want.EffectivePermissions) || !found.AuthorizedDev || found.Source != "dev" {
				t.Fatalf("interrupted publication lost the authorized generation: got %+v; want %+v", found, want)
			}
			if data, err := os.ReadFile(untouched); err != nil || string(data) != "leave alone" {
				t.Fatalf("unrelated directory changed: %q %v", data, err)
			}
			if scenario != "unpublished" {
				if _, err := os.Stat(backup); err != nil {
					t.Fatalf("recovery deleted backup: %v", err)
				}
			}
		})
	}
}

func writeDevGenerationTestAuthorization(t *testing.T, home, pluginID, directory string) DevAuthorization {
	t.Helper()
	abs, err := filepath.Abs(directory)
	if err != nil {
		t.Fatal(err)
	}
	authorization := DevAuthorization{PluginID: pluginID, Directory: abs, Token: "test-secret", CreatedAt: time.Now().UTC()}
	data, err := json.Marshal(authorization)
	if err != nil {
		t.Fatal(err)
	}
	path, err := DevAuthorizationPath(home, pluginID)
	if err != nil {
		t.Fatal(err)
	}
	writeDevGenerationTestFile(t, path, string(data))
	return authorization
}

func findDevGenerationTestPlugin(plugins []Plugin, id string) *Plugin {
	for index := range plugins {
		if plugins[index].ID == id {
			return &plugins[index]
		}
	}
	return nil
}

func writeDevGenerationTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
