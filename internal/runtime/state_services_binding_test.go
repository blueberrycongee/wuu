package runtime

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/blueberrycongee/wuu/internal/composition"
	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/credentialstore"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/pluginsettings"
)

// Native defaults and explicitly selected alternatives must use declared calls;
// disabling a selected implementation must not reopen its former backing path.
func TestRuntimeStateServicesUseSelectedGraph(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	modules := []string{StateServicesConsumer, credentialstore.FileModule, pluginsettings.Module, config.Module, ArtifactModule, DataQueryModule}
	host, _, err := buildPluginHost(nil, t.TempDir(), "", home, filepath.Join(home, "workspaces", "test"), nil, startPluginClient, nil, nil, &composition.Config{Version: composition.Version, Modules: &modules}, "")
	if err != nil {
		t.Fatal(err)
	}
	defer host.Close(ctx)
	if err := host.Activate(ctx); err != nil {
		t.Fatal(err)
	}
	runtime := &Session{PluginHost: host}
	binding, release, err := runtime.RetainStateServices()
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if err := binding.Credentials.Set(ctx, "test", "entry", []byte("value")); err != nil {
		t.Fatal(err)
	}
	if got, err := binding.Credentials.Get(ctx, "test", "entry"); err != nil || string(got) != "value" {
		t.Fatalf("selected credentials: %q %v", got, err)
	}
	if _, err := binding.Config.Read(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := binding.Settings.ReadState(ctx, "test.subject", pluginsettings.ScopeUser); err != nil {
		t.Fatal(err)
	}
	outcome, found := host.RetirePlugin(ctx, credentialstore.FileModule, nil)
	if !found || outcome.Err != nil {
		t.Fatalf("retire: %+v %v", outcome, found)
	}
	if _, err := binding.Credentials.Get(ctx, "test", "entry"); err == nil {
		t.Fatal("disabled credentials fell back")
	}
	if _, err := host.CallService(ctx, StateServicesConsumer, pluginhost.KernelDataQueryService, "request", nil); err == nil {
		t.Fatal("undeclared index read accepted")
	}
}
