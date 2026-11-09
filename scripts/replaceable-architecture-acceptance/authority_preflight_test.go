package acceptance_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/credentialstore"
	"github.com/blueberrycongee/wuu/internal/loopdriver"
	"github.com/blueberrycongee/wuu/internal/permissionpolicy"
	"github.com/blueberrycongee/wuu/internal/persistence"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/pluginsettings"
	wuuruntime "github.com/blueberrycongee/wuu/internal/runtime"
	"github.com/blueberrycongee/wuu/internal/session"
	"github.com/stretchr/testify/require"
)

// Admission must fail while the candidate is still prepared. Rejecting only
// after ActivatePluginGeneration's durable commit would already lose the old
// configuration and stop its background work.
func TestAcceptanceAuthorityPreflightPreservesActiveGeneration(t *testing.T) {
	for _, data := range []string{"empty", "current-copy"} {
		t.Run(data, func(t *testing.T) {
			home, root := t.TempDir(), t.TempDir()
			t.Setenv("WUU_HOME", home)
			firstFile, secondFile := filepath.Join(home, "first.json"), filepath.Join(home, "second.json")
			first := acceptanceExternalStateOptions(t, root, home, "acceptance.authority-a", "1.0.0", firstFile)
			second := acceptanceExternalStateOptions(t, root, home, "acceptance.authority-b", "1.0.0", secondFile)
			for _, grant := range second.Config.Extensions.Grants {
				require.NoError(t, first.Config.Extensions.RecordGrant(grant))
			}
			modules := []string{"wuu.session-state", "wuu.session.default", "acceptance.authority-a", "acceptance.authority-b", "kernel", loopdriver.DefaultDriverID, "wuu.session-execution", "wuu.tool-execution", wuuruntime.StateServicesConsumer, credentialstore.FileModule, pluginsettings.Module, config.Module, permissionpolicy.Module, wuuruntime.ArtifactModule, wuuruntime.DataQueryModule}
			first.Config.Composition.Modules = &modules
			first.Config.DefaultProvider = "fixture"
			first.Config.Providers = map[string]config.ProviderConfig{"fixture": {Type: "openai-compatible", BaseURL: "http://127.0.0.1:1/v1", APIKey: "fixture", Model: "fixture"}}
			original, err := json.Marshal(first.Config)
			require.NoError(t, err)
			configPath := filepath.Join(home, "config.json")
			require.NoError(t, os.WriteFile(configPath, original, 0600))
			rt, err := wuuruntime.NewSession(wuuruntime.Options{RootDir: root, HomeDir: home, ConfigPath: configPath, Config: first.Config, NoTools: true, NonInteractive: true})
			require.NoError(t, err)
			t.Cleanup(func() { _, err := rt.Cleanup(); require.NoError(t, err) })
			old, err := rt.RetainState()
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, old.Close()) })
			_, err = old.Sessions.CreateInitialized(session.Session{ID: "preflight-thread", CWD: root, Title: "old generation"}, nil)
			require.NoError(t, err)
			var candidateBytes []byte
			if data == "current-copy" {
				candidateBytes, err = os.ReadFile(firstFile)
				require.NoError(t, err)
				require.NoError(t, os.WriteFile(secondFile, candidateBytes, 0600))
			}
			next := first.Config
			nextComposition := *first.Config.Composition
			next.Composition = &nextComposition
			next.Composition.Services = second.Config.Composition.Services
			commitCalls := 0
			candidate, preflightErr := rt.PreflightExtensionPolicy(next)
			// Match the public caller sequence, so an incorrectly admitted candidate
			// exposes the durable commit and retirement instead of silently leaking it.
			if candidate != nil {
				_ = rt.ActivatePluginGeneration(candidate, func() error {
					commitCalls++
					changed, err := json.Marshal(next)
					if err != nil {
						return err
					}
					return os.WriteFile(configPath, changed, 0600)
				})
			}
			require.ErrorIs(t, preflightErr, persistence.ErrMigrationRequired)
			require.Nil(t, candidate)
			require.Zero(t, commitCalls, "an invalid candidate must not reach durable configuration commit")
			persisted, err := os.ReadFile(configPath)
			require.NoError(t, err)
			require.Equal(t, original, persisted)
			require.Same(t, old.Host, rt.PluginHost)
			for _, status := range old.Host.Statuses() {
				if status.ID == "acceptance.authority-a" {
					require.Equal(t, pluginhost.StateActive, status.State)
				}
			}
			_, err = old.Sessions.UpdateTitle("preflight-thread", "old generation remains writable")
			require.NoError(t, err, "failed preparation must not StopBackground on the old generation")
			actual, readErr := os.ReadFile(secondFile)
			if data == "empty" {
				require.ErrorIs(t, readErr, os.ErrNotExist)
			} else {
				require.NoError(t, readErr)
				require.Equal(t, candidateBytes, actual, "preflight must not write to the rejected source")
			}
		})
	}
}
