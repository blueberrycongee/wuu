package appserver

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/blueberrycongee/wuu/internal/composition"
	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/mcp"
)

func TestLazyPluginMCPSurvivesCatalogAndConfigReload(t *testing.T) {
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("WWW-Authenticate", `Bearer resource_metadata="`+"http://"+r.Host+`/.well-known/oauth-protected-resource"`)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	t.Cleanup(endpoint.Close)
	prior := newTestRuntime(t, &fakeClient{})
	preparePluginRuntimePathsForTest(t, prior)
	cfg, _, err := config.LoadPath(prior.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Composition = &composition.Config{Version: 3, Profile: "desktop"}
	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(prior.ConfigPath, raw, 0600); err != nil {
		t.Fatal(err)
	}
	rt := newConfigRefreshRuntime(t, prior)
	out := &lockedBuffer{}
	srv := newOwnerHandlerFixture(rt, out)
	if err := srv.refreshConfigIfChanged(); err != nil {
		t.Fatal(err)
	}
	source := t.TempDir()
	manifest := fmt.Sprintf(`{"id":"reload-connector","mcp_servers":{"docs":{"url":%q},"account":{"url":%q}}}`, endpoint.URL, endpoint.URL)
	if err := os.WriteFile(filepath.Join(source, "plugin.json"), []byte(manifest), 0600); err != nil {
		t.Fatal(err)
	}
	callPluginPackageRPC(t, srv, "install-reload", MethodPluginPackageInstall, PluginPackageInstallParams{Path: source})
	if response := responseByID(t, parseOutput(t, out.String()), "install-reload"); response["error"] != nil {
		t.Fatal(response["error"])
	}
	cfg, _, err = config.LoadPath(rt.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Composition.Patches = append(cfg.Composition.Patches, composition.Patch{Modules: map[string]bool{"reload-connector": true}})
	raw, err = json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(rt.ConfigPath, raw, 0600); err != nil {
		t.Fatal(err)
	}
	callPluginPackageRPC(t, srv, "refresh-reload", MethodExtensionCatalogRefresh, nil)
	if response := responseByID(t, parseOutput(t, out.String()), "refresh-reload"); response["error"] != nil {
		t.Fatal(response["error"])
	}
	for index := range 3 {
		name := "plugin.reload-connector.account"
		callPluginPackageRPC(t, srv, fmt.Sprintf("reload-auth-%d", index), MethodMCPAuthStatus, map[string]any{"name": name})
		response := responseByID(t, parseOutput(t, out.String()), fmt.Sprintf("reload-auth-%d", index))
		if response["error"] != nil {
			t.Fatalf("lazy plugin MCP server lost its manager after reload %d: %+v", index, response)
		}
		if err := srv.refreshConfigIfChanged(); err != nil {
			t.Fatal(err)
		}
	}
	// Plugin-owned definitions stay package-managed; only the package edits them.
	callPluginPackageRPC(t, srv, "reload-read", MethodMCPDefinitionRead, map[string]any{"name": "plugin.reload-connector.account"})
	if response := responseByID(t, parseOutput(t, out.String()), "reload-read"); response["error"] == nil {
		t.Fatal("plugin MCP definition was exposed as editable")
	}
	callPluginPackageRPC(t, srv, "reload-connect", MethodMCPConnect, map[string]any{"name": "plugin.reload-connector.account"})
	if response := responseByID(t, parseOutput(t, out.String()), "reload-connect"); response["error"] == nil {
		t.Fatal("unauthorized server connected without login")
	}
	callPluginPackageRPC(t, srv, "reload-signout", MethodMCPAuthRemove, map[string]any{"name": "plugin.reload-connector.account"})
	if response := responseByID(t, parseOutput(t, out.String()), "reload-signout"); response["error"] != nil {
		t.Fatal(response["error"])
	}
	callPluginPackageRPC(t, srv, "refresh-signout", MethodExtensionCatalogRefresh, nil)
	if response := responseByID(t, parseOutput(t, out.String()), "refresh-signout"); response["error"] != nil {
		t.Fatal(response["error"])
	}
	manager, err := srv.mcpManager()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"plugin.reload-connector.docs", "plugin.reload-connector.account"} {
		if status := manager.Status()[name]; status.State != mcp.MCPServerStateAuthRequired || status.Connected || status.ToolCount != 0 {
			t.Fatalf("unauthorized server must remain available for explicit login, not tool use: %+v", status)
		}
	}
}

func TestMCPRemovalWhileUnrelatedTurnRuns(t *testing.T) {
	prior := newTestRuntime(t, &fakeClient{})
	preparePluginRuntimePathsForTest(t, prior)
	cfg, _, err := config.LoadPath(prior.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	disabled := false
	cfg.MCPServers = map[string]config.MCPServerConfig{"removed": {URL: "https://example.test/mcp", Enabled: &disabled}, "kept": {URL: "https://example.test/other", Enabled: &disabled}}
	raw, _ := json.Marshal(cfg)
	if err := os.WriteFile(prior.ConfigPath, raw, 0600); err != nil {
		t.Fatal(err)
	}
	rt := newConfigRefreshRuntime(t, prior)
	out := &lockedBuffer{}
	srv := newOwnerHandlerFixture(rt, out)
	srv.threads = map[string]*threadState{"unrelated": {ID: "unrelated", running: true}}
	callPluginPackageRPC(t, srv, "read", MethodMCPDefinitionRead, map[string]any{"name": "removed"})
	definition := remarshal[MCPDefinitionResult](t, responseByID(t, parseOutput(t, out.String()), "read")["result"])
	for _, revision := range []string{"stale", definition.Revision} {
		callPluginPackageRPC(t, srv, revision, MethodMCPConfigure, map[string]any{"servers": map[string]any{"removed": nil}, "revision": revision})
		response := responseByID(t, parseOutput(t, out.String()), revision)
		if revision == "stale" {
			if response["error"] == nil {
				t.Fatal("stale removal accepted")
			}
			continue
		}
		if response["error"] != nil {
			t.Fatalf("unrelated turn blocked removal: %+v", response)
		}
	}
	saved, _, err := config.LoadPath(rt.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := saved.MCPServers["removed"]; exists {
		t.Fatal("removed definition survived")
	}
	if _, exists := saved.MCPServers["kept"]; !exists {
		t.Fatal("unrelated definition removed")
	}
	if !srv.threads["unrelated"].running {
		t.Fatal("unrelated turn was stopped")
	}
}
