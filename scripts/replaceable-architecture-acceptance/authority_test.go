package acceptance_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/composition"
	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/extensions"
	"github.com/blueberrycongee/wuu/internal/persistence"
	pluginpkg "github.com/blueberrycongee/wuu/internal/plugin"
	"github.com/blueberrycongee/wuu/internal/pluginprotocol"
	wuuruntime "github.com/blueberrycongee/wuu/internal/runtime"
	"github.com/blueberrycongee/wuu/internal/session"
	"github.com/stretchr/testify/require"
)

func acceptanceExternalStateOptions(t *testing.T, root, home, id, version, data string) wuuruntime.StateOptions {
	t.Helper()
	fixture := os.Getenv("WUU_REPLACEABLE_FIXTURE")
	if fixture == "" {
		t.Skip("run the external provider acceptance runner")
	}
	packageRoot := filepath.Join(home, "plugins", id)
	if _, err := os.Stat(packageRoot); os.IsNotExist(err) {
		require.NoError(t, os.CopyFS(packageRoot, os.DirFS(fixture)))
	}
	node, err := exec.LookPath("node")
	require.NoError(t, err)
	manifest, err := json.Marshal(pluginpkg.Manifest{SchemaVersion: 1, ID: id, Version: version, Runtime: &pluginpkg.RuntimeSpec{Protocol: "wuu-plugin-v1", Command: node, Args: []string{"provider.ts"}, Env: map[string]string{"WUU_ACCEPTANCE_STATE": data}}})
	require.NoError(t, err)
	manifestPath := filepath.Join(packageRoot, "plugin.json")
	require.NoError(t, os.WriteFile(manifestPath, manifest, 0600))
	item, err := pluginpkg.LoadManifest(manifestPath, "user")
	require.NoError(t, err)
	settings := &extensions.Settings{}
	require.NoError(t, settings.RecordGrant(extensions.Grant{SubjectID: item.SubjectID, Fingerprint: item.Fingerprint, Scope: extensions.GrantScopeUser, Permissions: item.EffectivePermissions, ApprovedAt: time.Now().UTC()}))
	modules := []string{"wuu.session-state", "wuu.session.default", id}
	return wuuruntime.StateOptions{RootDir: root, HomeDir: home, Config: config.Config{Extensions: settings, Composition: &composition.Config{Version: composition.Version, Modules: &modules, Services: []composition.Binding{{Service: persistence.ServiceName, Major: 1, Provider: id}}}}}
}

// A provider identity or version is not proof of a shared write authority. A
// copied dataset must not become writable while the original generation lives.
func TestAcceptanceExternalAuthoritySwitch(t *testing.T) {
	for _, identity := range []string{"same-id-update", "different-id"} {
		for _, data := range []string{"empty", "stale-copy", "current-copy", "same-revision-divergent-copy"} {
			t.Run(identity+"/"+data, func(t *testing.T) {
				ctx := context.Background()
				home, root := t.TempDir(), t.TempDir()
				t.Setenv("WUU_HOME", home)
				firstFile, secondFile := filepath.Join(home, "first.json"), filepath.Join(home, "second.json")
				firstOpts := acceptanceExternalStateOptions(t, root, home, "acceptance.authority-a", "1.0.0", firstFile)
				first, err := wuuruntime.OpenState(ctx, firstOpts)
				require.NoError(t, err)
				t.Cleanup(func() { require.NoError(t, first.Close()) })
				const thread = "authority-thread"
				_, err = first.Sessions.CreateInitialized(session.Session{ID: thread, CWD: root, Title: "before copy"}, []session.HistoryRecord{{Role: "user", Content: "preserve identity"}})
				require.NoError(t, err)
				if data == "stale-copy" {
					copy, err := os.ReadFile(firstFile)
					require.NoError(t, err)
					require.NoError(t, os.WriteFile(secondFile, copy, 0600))
				}
				binding := pluginprotocol.DriverBinding{Profile: "explicit", ProviderID: "acceptance.explicit-driver", ContractVersion: 1, DriverID: "retained-loop", DriverVersion: "1.0.0"}
				require.NoError(t, first.Sessions.BindDriver(thread, binding))
				require.NoError(t, first.Sessions.SaveDriverCheckpoint(thread, session.DriverCheckpointRecord{ContractVersion: 1, DriverID: binding.DriverID, DriverVersion: binding.DriverVersion, State: json.RawMessage(`{"continuation":"after-copy"}`)}))
				require.NoError(t, first.Sessions.SaveModelInputReceipt(thread, session.ModelInputReceiptRecord{OperationID: "after-copy-model", ContractVersion: 1, DriverID: binding.DriverID, DriverVersion: binding.DriverVersion, Payload: json.RawMessage("{\n  \"original\": [ 1, 2 ]\n}\n")}))
				view, err := first.Documents.Read(ctx, persistence.Query{Namespace: "acceptance.authority"})
				require.NoError(t, err)
				tx := persistence.Transaction{ID: "after-copy-durable-operation", ExpectedRevision: view.Revision, Writes: []persistence.Write{{Namespace: "acceptance.authority", Key: "receipt", Value: json.RawMessage(`{"preserved":true}`)}}}
				receipt, err := first.Documents.Commit(ctx, tx)
				require.NoError(t, err)
				if data == "current-copy" || data == "same-revision-divergent-copy" {
					copy, err := os.ReadFile(firstFile)
					require.NoError(t, err)
					if data == "same-revision-divergent-copy" {
						var fork struct {
							Revision  string                 `json:"revision"`
							Documents []persistence.Document `json:"documents"`
							Receipts  json.RawMessage        `json:"receipts"`
						}
						require.NoError(t, json.Unmarshal(copy, &fork))
						fork.Documents = append(fork.Documents, persistence.Document{Namespace: "acceptance.divergent", Key: "same-revision", Value: json.RawMessage(`{"different":true}`)})
						copy, err = json.Marshal(fork)
						require.NoError(t, err)
					}
					require.NoError(t, os.WriteFile(secondFile, copy, 0600))
				}
				secondID := "acceptance.authority-a"
				if identity == "different-id" {
					secondID = "acceptance.authority-b"
				}
				secondOpts := acceptanceExternalStateOptions(t, root, home, secondID, "2.0.0", secondFile)
				second, err := wuuruntime.OpenState(ctx, secondOpts)
				if err != nil {
					require.Nil(t, second)
					require.ErrorIs(t, err, persistence.ErrMigrationRequired, "refusal must identify an incompatible authority, not an unrelated startup failure")
					t.Logf("replacement refused: %v", err)
					_, err = first.Sessions.UpdateTitle(thread, "original authority survived rejection")
					require.NoError(t, err)
					retained, found, err := first.Sessions.LoadDriverBinding(thread)
					require.NoError(t, err)
					require.True(t, found)
					require.Equal(t, binding, retained)
					retried, err := first.Documents.Commit(ctx, tx)
					require.NoError(t, err)
					require.Equal(t, receipt, retried, "failed admission must preserve exact durable receipt retries")
					return
				}
				t.Cleanup(func() { require.NoError(t, second.Close()) })
				_, found, err := second.Sessions.Find(thread)
				require.NoError(t, err)
				bindingFound := false
				var inputs []session.ModelInputReceiptRecord
				if found {
					_, bindingFound, err = second.Sessions.LoadDriverBinding(thread)
					require.NoError(t, err)
					inputs, err = second.Sessions.LoadModelInputReceipts(thread)
					require.NoError(t, err)
				}
				raw, callErr := second.Host.CallService(ctx, "wuu.session-state", persistence.ServiceName, "export", json.RawMessage(`{}`))
				require.Nil(t, callErr)
				var snapshot persistence.Snapshot
				require.NoError(t, json.Unmarshal(raw, &snapshot))
				receiptFound := false
				for _, stored := range snapshot.Receipts {
					if stored.ID == receipt.ID {
						receiptFound = true
					}
				}
				if found {
					_, err = second.Sessions.UpdateTitle(thread, "second independent writer")
				} else {
					_, err = second.Sessions.CreateInitialized(session.Session{ID: thread, CWD: root, Title: "second independent writer"}, nil)
				}
				require.NoError(t, err)
				_, err = first.Sessions.UpdateTitle(thread, "first still writing")
				require.NoError(t, err)
				a, found, err := first.Sessions.Find(thread)
				require.NoError(t, err)
				require.True(t, found)
				b, found, err := second.Sessions.Find(thread)
				require.NoError(t, err)
				require.True(t, found)
				t.Fatalf("replacement silently admitted independent authority: explicit_binding=%t raw_model_receipts=%d durable_receipt=%t; both generations write same thread independently: A=%q B=%q", bindingFound, len(inputs), receiptFound, a.Title, b.Title)
			})
		}
	}
}
