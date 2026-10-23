package acceptance_test

import (
	"context"
	"crypto/sha256"
	"database/sql"
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
	wuuruntime "github.com/blueberrycongee/wuu/internal/runtime"
	"github.com/blueberrycongee/wuu/internal/session"
	"github.com/blueberrycongee/wuu/internal/statepath"
	"github.com/stretchr/testify/require"
)

// The public production opener must enforce the stopped-writer gate and run
// every converter against the selected authority, including a discovered process.
func TestAcceptanceOpenStateLegacyMigration(t *testing.T) {
	for _, kind := range []string{"sqlite", "jsonl", "external"} {
		t.Run(kind, func(t *testing.T) {
			ctx := context.Background()
			home, root := t.TempDir(), t.TempDir()
			t.Setenv("WUU_HOME", home)
			selectedID := persistence.SQLiteModule
			if kind == "jsonl" {
				selectedID = persistence.JSONLModule
			}
			cfg := config.Config{}
			var installed pluginpkg.Plugin
			if kind == "external" {
				fixture := os.Getenv("WUU_REPLACEABLE_FIXTURE")
				if fixture == "" {
					t.Skip("external provider requires node scripts/replaceable-architecture-external.mjs")
				}
				selectedID = "acceptance.process-state"
				packageRoot := filepath.Join(home, "plugins", selectedID)
				require.NoError(t, os.CopyFS(packageRoot, os.DirFS(fixture)))
				node, err := exec.LookPath("node")
				require.NoError(t, err)
				manifest, err := json.Marshal(pluginpkg.Manifest{SchemaVersion: 1, ID: selectedID, Version: "1.0.0", Runtime: &pluginpkg.RuntimeSpec{Protocol: "wuu-plugin-v1", Command: node, Args: []string{"provider.ts"}, Env: map[string]string{"WUU_ACCEPTANCE_STATE": filepath.Join(home, "external-state.json")}}})
				require.NoError(t, err)
				manifestPath := filepath.Join(packageRoot, "plugin.json")
				require.NoError(t, os.WriteFile(manifestPath, manifest, 0600))
				installed, err = pluginpkg.LoadManifest(manifestPath, "user")
				require.NoError(t, err)
				cfg.Extensions = &extensions.Settings{}
				require.NoError(t, cfg.Extensions.RecordGrant(extensions.Grant{SubjectID: installed.SubjectID, Fingerprint: installed.Fingerprint, Scope: extensions.GrantScopeUser, Permissions: installed.EffectivePermissions, ApprovedAt: time.Now().UTC()}))
			}
			modules := []string{"wuu.session-state", selectedID}
			cfg.Composition = &composition.Config{Version: composition.Version, Modules: &modules, Services: []composition.Binding{{Service: persistence.ServiceName, Major: 1, Provider: selectedID}}}
			opts := wuuruntime.StateOptions{RootDir: root, HomeDir: home, Config: cfg}
			dir := statepath.SessionsDir(home)
			require.NoError(t, os.MkdirAll(dir, 0700))
			source := filepath.Join(dir, "sessions.sqlite3")
			db, err := sql.Open("sqlite", source)
			require.NoError(t, err)
			fixtureSQL, err := os.ReadFile("fixtures/legacy.sql")
			require.NoError(t, err)
			_, err = db.Exec(string(fixtureSQL))
			require.NoError(t, err)
			raw := []byte("{\n \"n\":9007199254740993, \"text\":\"\\u4f60\"\n}\n")
			_, err = db.Exec(`INSERT INTO session_model_input_receipts VALUES ('thread','operation',1,'external.driver','2.3.4',?,'2026-10-22T01:02:04Z')`, raw)
			require.NoError(t, err)
			require.NoError(t, db.Close())
			before, err := os.ReadFile(source)
			require.NoError(t, err)
			presence, first, err := session.AcquireAppServerPresence(dir)
			require.NoError(t, err)
			require.True(t, first)
			t.Cleanup(func() { require.NoError(t, presence.Release()) })
			require.NoError(t, presence.FinalizeStartup())
			refused, err := wuuruntime.OpenState(ctx, opts)
			if refused != nil {
				require.NoError(t, refused.Close())
			}
			require.ErrorIs(t, err, persistence.ErrMigrationRequired, "full production opener must reject a live old writer")
			require.NoError(t, presence.Release())
			state, err := wuuruntime.OpenState(ctx, opts)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, state.Close()) })
			provider, found := state.Host.ServiceRegistry().ProviderPluginID(persistence.ServiceName, 1)
			require.True(t, found)
			require.Equal(t, selectedID, provider)
			migrated, err := session.LegacySQLiteMigrationRecorded(ctx, state.Documents)
			require.NoError(t, err)
			require.True(t, migrated)
			thread, found, err := state.Sessions.Find("thread")
			require.NoError(t, err)
			require.True(t, found)
			require.Equal(t, "Legacy identity", thread.Title)
			inputs, err := state.Sessions.LoadModelInputReceipts("thread")
			require.NoError(t, err)
			require.Len(t, inputs, 1)
			require.Equal(t, raw, []byte(inputs[0].Payload))
			_, err = state.Sessions.UpdateTitle("thread", "production selected state")
			require.NoError(t, err)
			require.NoError(t, state.Close())
			state, err = wuuruntime.OpenState(ctx, opts)
			require.NoError(t, err)
			thread, found, err = state.Sessions.Find("thread")
			require.NoError(t, err)
			require.True(t, found)
			require.Equal(t, "production selected state", thread.Title)
			require.NoError(t, state.Close())
			_, _, err = state.Sessions.Find("thread")
			require.Error(t, err, "released authority must not reopen a local default")
			after, err := os.ReadFile(source)
			require.NoError(t, err)
			require.Equal(t, sha256.Sum256(before), sha256.Sum256(after))
			if kind != "sqlite" {
				_, err = os.Stat(filepath.Join(dir, "documents", "state.sqlite3"))
				require.True(t, os.IsNotExist(err), "no default SQLite provider side effect")
			}
			if kind == "external" {
				cfg.Extensions.SetDisabled(installed.SubjectID, true)
			} else {
				modules = []string{"wuu.session-state"}
			}
			denied, err := wuuruntime.OpenState(ctx, opts)
			if denied != nil {
				require.NoError(t, denied.Close())
			}
			require.Error(t, err, "disabled selected authority cannot silently fall back")
		})
	}
}
