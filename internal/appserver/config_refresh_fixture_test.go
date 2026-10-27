package appserver

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/credentialstore"
	pluginpkg "github.com/blueberrycongee/wuu/internal/plugin"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/runtime"
)

// Refresh fixtures need constructor-owned factories across successor graphs.
// Keep their file and package locations, but never let configuration-only tests
// submit inference to an external account.
func newConfigRefreshRuntime(t *testing.T, previous *runtime.Session, factories ...runtime.NativeProviderFactory) *runtime.Session {
	t.Helper()
	cfg, _, err := config.LoadPath(previous.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("configuration fixture unexpectedly submitted inference")
		http.Error(w, "inference is not part of this fixture", http.StatusBadRequest)
	}))
	t.Cleanup(endpoint.Close)
	for name, provider := range cfg.Providers {
		provider.BaseURL = endpoint.URL + "/v1"
		provider.APIKey = "fixture-only"
		provider.APIKeyEnv = ""
		cfg.Providers[name] = provider
	}
	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(previous.ConfigPath, raw, 0600); err != nil {
		t.Fatal(err)
	}
	home := previous.HomeDir
	if home == "" {
		home = os.Getenv("HOME")
	}
	wuuHome := previous.WuuHome
	if wuuHome == "" {
		wuuHome = t.TempDir()
	}
	credentials := runtime.NativeProviderFactory{ID: credentialstore.OSModule, DefaultEnabled: true, Build: func(runtime.NativeProviderContext) (pluginhost.Client, error) {
		return credentialstore.NewService(credentialstore.OSModule, func() (credentialstore.Store, error) { return nativeCredentialFixture(t, home), nil }), nil
	}}
	var catalog *[]pluginpkg.Plugin
	if previous.Plugins != nil {
		catalog = &previous.Plugins
	}
	selected, err := runtime.NewSession(runtime.Options{RootDir: previous.RootDir, HomeDir: home, WuuHome: wuuHome, Config: cfg, ConfigPath: previous.ConfigPath, ConfigLoadMode: runtime.ConfigLoadFile, NonInteractive: true, NativeProviders: append([]runtime.NativeProviderFactory{credentials}, factories...), DiscoveredPlugins: catalog})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := selected.Cleanup(); err != nil {
			t.Error(err)
		}
	})
	return selected
}

// Settings fixtures keep their declared endpoints and authentication fields so
// update assertions inspect the exact configuration under test. They perform
// no inference; discovery cases provide their own loopback HTTP endpoints.
func newSettingsRefreshRuntime(t *testing.T, previous *runtime.Session) *runtime.Session {
	t.Helper()
	raw, err := os.ReadFile(previous.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	return writeProviderSettingsConfig(t, previous, string(raw))
}
