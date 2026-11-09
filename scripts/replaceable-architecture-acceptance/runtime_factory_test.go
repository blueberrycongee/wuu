package acceptance_test

import (
	"context"
	"github.com/blueberrycongee/wuu/internal/composition"
	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/persistence"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	wuuruntime "github.com/blueberrycongee/wuu/internal/runtime"
	"github.com/stretchr/testify/require"
	"path/filepath"
	"testing"
)

// Embedding applications must be able to explicitly select their supplied
// provider through the same production admission path as discovered packages.
func TestAcceptanceOpenStateNativeFactorySelection(t *testing.T) {
	ctx := context.Background()
	const providerID = "acceptance.native-state"
	modules := []string{"wuu.session-state", "wuu.session.default", providerID}
	home := t.TempDir()
	t.Setenv("WUU_HOME", home)
	state, err := wuuruntime.OpenState(ctx, wuuruntime.StateOptions{
		RootDir: t.TempDir(), HomeDir: home,
		Config: config.Config{Composition: &composition.Config{Version: composition.Version, Modules: &modules, Services: []composition.Binding{{Service: persistence.ServiceName, Major: 1, Provider: providerID}}}},
		NativeProviders: []wuuruntime.NativeProviderFactory{{ID: providerID, Build: func(input wuuruntime.NativeProviderContext) (pluginhost.Client, error) {
			return persistence.NewService(providerID, filepath.Join(input.WuuHome, "custom-state"), "jsonl"), nil
		}}},
	})
	require.NoError(t, err, "supplied native provider must be available during initial composition selection")
	t.Cleanup(func() { require.NoError(t, state.Close()) })
	created, err := state.Sessions.CreateWithMetadata("custom selected provider", "/workspace")
	require.NoError(t, err)
	got, found, err := state.Sessions.Find(created.ID)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, created.ID, got.ID)
	selected, found := state.Host.ServiceRegistry().ProviderPluginID(persistence.ServiceName, 1)
	require.True(t, found)
	require.Equal(t, providerID, selected)
}
