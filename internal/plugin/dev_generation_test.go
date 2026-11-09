package plugin

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/pluginsettings"
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

func TestDevGenerationRetainsStorageOwnerWithoutBundledPrivileges(t *testing.T) {
	home, source := t.TempDir(), t.TempDir()
	installed := findDevGenerationTestPlugin(Discover("", home), "personal")
	if installed == nil {
		t.Fatal("bundled fixture unavailable")
	}
	writeDevGenerationTestFile(t, filepath.Join(source, "plugin.json"), `{"id":"personal","version":"1"}`)
	authorization := writeDevGenerationTestAuthorization(t, home, "personal", source)
	for i := 0; i < 2; i++ {
		published, err := PublishDevGeneration(home, source, source, authorization)
		if err != nil {
			t.Fatal(err)
		}
		found := findDevGenerationTestPlugin(Discover("", home), "personal")
		if found == nil || found.StorageSubject() != installed.SubjectID || published.StorageSubject() != installed.SubjectID || found.SubjectID != "plugin:dev:personal" || found.Official || !found.AuthorizedDev || found.Source != "dev" {
			t.Fatalf("lost storage continuity or elevated provenance: %+v", found)
		}
	}
	path := filepath.Join(home, "dev", "generations", "personal", devGenerationReceiptFile)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var receipt devGenerationReceipt
	if err := json.Unmarshal(raw, &receipt); err != nil {
		t.Fatal(err)
	}
	receipt.StorageSubjectID = "plugin:bundled:memory"
	raw, _ = json.Marshal(receipt)
	writeDevGenerationTestFile(t, path, string(raw))
	if found := findDevGenerationTestPlugin(Discover("", home), "personal"); found == nil || found.AuthorizedDev {
		t.Fatalf("tampered ownership binding admitted: %+v", found)
	}
}

func TestDevGenerationPinsOwnerAcrossInstalledPackageChanges(t *testing.T) {
	home, source := t.TempDir(), t.TempDir()
	writeDevGenerationTestFile(t, filepath.Join(source, "plugin.json"), `{"id":"owner-fixture","version":"1"}`)
	authorization := writeDevGenerationTestAuthorization(t, home, "owner-fixture", source)
	first, err := PublishDevGeneration(home, source, source, authorization)
	if err != nil {
		t.Fatal(err)
	}
	writeDevGenerationTestFile(t, filepath.Join(home, "plugins", "owner-fixture", "plugin.json"), `{"id":"owner-fixture","version":"2"}`)
	next, err := PublishDevGeneration(home, source, source, authorization)
	if err != nil {
		t.Fatal(err)
	}
	if next.StorageSubject() != first.SubjectID {
		t.Fatalf("an installed namesake redirected existing development data: %+v", next)
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

// Legacy development receipts predate persistent storage bindings. Recovery must
// use a normal authenticated publication, never rewrite ownership journals or
// reinterpret every historical development identity during discovery.
func TestLegacyDevGenerationRepublishRecoversCompletedStorageTransfer(t *testing.T) {
	home, source := t.TempDir(), t.TempDir()
	writeDevGenerationTestFile(t, filepath.Join(source, "plugin.json"), `{"id":"personal","version":"1"}`)
	authorization := writeDevGenerationTestAuthorization(t, home, "personal", source)
	published, err := PublishDevGeneration(home, source, source, authorization)
	if err != nil {
		t.Fatal(err)
	}
	receiptPath := filepath.Join(home, "dev", "generations", "personal", devGenerationReceiptFile)
	raw, err := os.ReadFile(receiptPath)
	if err != nil {
		t.Fatal(err)
	}
	var receipt devGenerationReceipt
	if err := json.Unmarshal(raw, &receipt); err != nil {
		t.Fatal(err)
	}
	receipt.StorageSubjectID = ""
	receipt.Signature = signDevGenerationReceipt(receipt, authorization.Token)
	raw, err = json.Marshal(receipt)
	if err != nil {
		t.Fatal(err)
	}
	writeDevGenerationTestFile(t, receiptPath, string(raw))
	legacy := findDevGenerationTestPlugin(Discover("", home), "personal")
	if legacy == nil || !legacy.AuthorizedDev || legacy.StorageSubject() != "plugin:dev:personal" || legacy.Fingerprint != published.Fingerprint {
		t.Fatalf("legacy fixture not admitted: %+v", legacy)
	}
	dataRoot := t.TempDir()
	transfer := pluginsettings.StateTransfer{ID: "personal-records-v1-owner", SourceSubjectID: "plugin:bundled:memory", TargetSubjectID: "plugin:bundled:personal", KeyPrefix: "personal.records.v1.", Scope: pluginsettings.ScopeUser}
	if _, err := pluginsettings.UpdateState(dataRoot, "", transfer.SourceSubjectID, transfer.Scope, func(values map[string]string) error {
		values[transfer.KeyPrefix+"updated"] = "old"
		values[transfer.KeyPrefix+"forgotten"] = "old"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := pluginsettings.MoveStatePrefix(dataRoot, "", transfer); err != nil {
		t.Fatal(err)
	}
	if _, err := pluginsettings.UpdateState(dataRoot, "", transfer.TargetSubjectID, transfer.Scope, func(values map[string]string) error {
		values[transfer.KeyPrefix+"updated"] = "new"
		delete(values, transfer.KeyPrefix+"forgotten")
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	candidate := transfer
	candidate.TargetSubjectID = legacy.StorageSubject()
	if err := pluginsettings.CheckStateTransfer(context.Background(), dataRoot, "", candidate); err == nil {
		t.Fatal("legacy identity took existing ownership")
	}
	// The normal preflight uses the same intended owner as publication, while
	// discovery of the old signed receipt remains unchanged until commit.
	candidate.TargetSubjectID = DevStorageSubject(home, "personal")
	if candidate.TargetSubjectID != transfer.TargetSubjectID {
		t.Fatalf("republish owner: %q", candidate.TargetSubjectID)
	}
	if err := pluginsettings.CheckStateTransfer(context.Background(), dataRoot, "", candidate); err != nil {
		t.Fatalf("republish preflight: %v", err)
	}
	repaired, err := PublishDevGeneration(home, source, source, authorization)
	if err != nil {
		t.Fatal(err)
	}
	if repaired.StorageSubject() != transfer.TargetSubjectID || repaired.SubjectID != legacy.SubjectID || !repaired.AuthorizedDev || repaired.Official {
		t.Fatalf("republication changed execution trust: %+v", repaired)
	}
	current := findDevGenerationTestPlugin(Discover("", home), "personal")
	if current == nil || current.StorageSubject() != repaired.StorageSubject() {
		t.Fatalf("signed binding was not retained: %+v", current)
	}
	candidate.TargetSubjectID = current.StorageSubject()
	if err := pluginsettings.MoveStatePrefix(dataRoot, "", candidate); err != nil {
		t.Fatal(err)
	}
	owner, err := pluginsettings.ReadState(dataRoot, "", transfer.TargetSubjectID, transfer.Scope)
	if err != nil || len(owner.Values) != 1 || owner.Values[transfer.KeyPrefix+"updated"] != "new" {
		t.Fatalf("republication replayed backups: %+v %v", owner, err)
	}
	legacyState, err := pluginsettings.ReadState(dataRoot, "", legacy.SubjectID, transfer.Scope)
	if err != nil || len(legacyState.Values) != 0 {
		t.Fatalf("legacy namespace received data: %+v %v", legacyState, err)
	}
	receiptAfter, err := os.ReadFile(receiptPath)
	if err != nil || bytes.Equal(raw, receiptAfter) {
		t.Fatalf("legacy receipt not upgraded: %v", err)
	}
}
