package acceptance_test

import (
	"context"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/composition"
	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/persistence"
	wuuruntime "github.com/blueberrycongee/wuu/internal/runtime"
	"github.com/blueberrycongee/wuu/internal/session"
	"github.com/stretchr/testify/require"
)

// Multiple admitted handles are valid when they observe one real authority;
// rejection of independent copies must not become a blanket single-open rule.
func TestAcceptanceSharedNativeAuthority(t *testing.T) {
	for _, provider := range []string{persistence.SQLiteModule, persistence.JSONLModule} {
		t.Run(provider, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			home, root := t.TempDir(), t.TempDir()
			t.Setenv("WUU_HOME", home)
			modules := []string{"wuu.session-state", provider}
			opts := wuuruntime.StateOptions{RootDir: root, HomeDir: home, Config: config.Config{Composition: &composition.Config{Version: composition.Version, Modules: &modules, Services: []composition.Binding{{Service: persistence.ServiceName, Major: 1, Provider: provider}}}}}
			first, err := wuuruntime.OpenState(ctx, opts)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, first.Close()) })
			_, err = first.Sessions.CreateInitialized(session.Session{ID: "shared-thread", CWD: root, Title: "first handle"}, nil)
			require.NoError(t, err)
			second, err := wuuruntime.OpenState(ctx, opts)
			require.NoError(t, err, "shared storage must admit another live handle")
			t.Cleanup(func() { require.NoError(t, second.Close()) })
			_, err = second.Sessions.UpdateTitle("shared-thread", "second handle")
			require.NoError(t, err)
			shared, found, err := first.Sessions.Find("shared-thread")
			require.NoError(t, err)
			require.True(t, found)
			require.Equal(t, "second handle", shared.Title)
			require.NoError(t, first.Close())
			_, err = second.Sessions.UpdateTitle("shared-thread", "remaining handle")
			require.NoError(t, err, "one member closing must not retire the other")
			require.NoError(t, second.Close())
			reopened, err := wuuruntime.OpenState(ctx, opts)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, reopened.Close()) })
			shared, found, err = reopened.Sessions.Find("shared-thread")
			require.NoError(t, err)
			require.True(t, found)
			require.Equal(t, "remaining handle", shared.Title)
		})
	}
}
