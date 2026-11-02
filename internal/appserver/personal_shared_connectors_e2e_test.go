package appserver

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/credentialstore"
	pluginpkg "github.com/blueberrycongee/wuu/internal/plugin"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	runtimepkg "github.com/blueberrycongee/wuu/internal/runtime"
)

func TestPersonalSharedMCPRealBundleOwnerReceiptChain(t *testing.T) {
	if os.Getenv("WUU_CONNECTOR_CHAIN_E2E") != "1" {
		t.Skip("set WUU_CONNECTOR_CHAIN_E2E=1 for real bundled process integration")
	}
	for _, scenario := range []string{"approve", "reject", "cancel", "unknown", "cold_pending", "background", "revoked", "disabled"} {
		t.Run(scenario, func(t *testing.T) { connectorChain(t, "mcp_"+scenario) })
	}
}

// Only the external OAuth/MCP server and model are synthetic. A real Coding
// runtime logs in once; separate Personal runtimes use the same selected store.
func sharedConnectorChainFixture(t *testing.T, home, configPath, scenario string, cfg config.Config, store credentialstore.Store, effects *atomic.Int32) []pluginpkg.Plugin {
	t.Helper()
	var base string
	var exchanges atomic.Int32
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/oauth-protected-resource":
			_ = json.NewEncoder(w).Encode(map[string]any{"resource": base, "authorization_servers": []string{base}})
			return
		case "/.well-known/oauth-authorization-server":
			_ = json.NewEncoder(w).Encode(map[string]any{"issuer": base, "authorization_endpoint": base + "/authorize", "token_endpoint": base + "/token"})
			return
		case "/token":
			exchanges.Add(1)
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "synthetic-shared-mcp-token", "token_type": "Bearer", "expires_in": 3600})
			return
		}
		if r.Header.Get("Authorization") != "Bearer synthetic-shared-mcp-token" {
			w.WriteHeader(401)
			return
		}
		if r.Method != "POST" {
			w.WriteHeader(405)
			return
		}
		var request struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params struct {
				Name string `json:"name"`
			} `json:"params"`
		}
		if json.NewDecoder(r.Body).Decode(&request) != nil {
			w.WriteHeader(400)
			return
		}
		if len(request.ID) == 0 {
			w.WriteHeader(202)
			return
		}
		var result any
		switch request.Method {
		case "initialize":
			result = map[string]any{"protocolVersion": "2025-03-26", "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]any{"name": "shared-fixture", "version": "1"}}
		case "tools/list":
			result = map[string]any{"tools": []any{map[string]any{"name": "gmail.send", "description": "Synthetic send", "inputSchema": map[string]any{"type": "object"}}, map[string]any{"name": "read", "description": "Synthetic read", "inputSchema": map[string]any{"type": "object"}}}}
		case "tools/call":
			if request.Params.Name == "gmail.send" {
				effects.Add(1)
				if scenario == "unknown" {
					w.WriteHeader(503)
					return
				}
			}
			result = map[string]any{"content": []any{map[string]any{"type": "text", "text": "Synthetic provider receipt"}}}
		default:
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": request.ID, "error": map[string]any{"code": -32601, "message": "unsupported"}})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": result})
	}))
	base = remote.URL
	t.Cleanup(remote.Close)
	packages := pluginpkg.Discover(home, os.Getenv("WUU_HOME"))
	on := true
	definition := config.MCPServerConfig{URL: base, Transport: "http", Enabled: &on, OAuth: &config.MCPOAuthConfig{ClientID: "synthetic-client", RedirectURI: "http://127.0.0.1/callback"}, Connector: &config.MCPConnectorDescriptor{Title: "Shared fixture"}, ToolOverrides: map[string]config.MCPToolOverride{"read": {ReadOnly: &on}}}
	found := false
	for i := range packages {
		if packages[i].ID == "account-connectors" {
			packages[i].MCPServers = map[string]config.MCPServerConfig{"fixture": definition}
			found = true
		}
	}
	if !found {
		t.Fatal("bundled account connector package missing")
	}
	credentials := runtimepkg.NativeProviderFactory{ID: credentialstore.OSModule, DefaultEnabled: true, Build: func(runtimepkg.NativeProviderContext) (pluginhost.Client, error) {
		return credentialstore.NewService(credentialstore.OSModule, func() (credentialstore.Store, error) { return store, nil }), nil
	}}
	coding, err := runtimepkg.NewSession(runtimepkg.Options{RootDir: filepath.Join(home, "coding"), HomeDir: home, ConfigPath: configPath, ConfigLoadMode: runtimepkg.ConfigLoadFile, Config: cfg, DiscoveredPlugins: &packages, NativeProviders: []runtimepkg.NativeProviderFactory{credentials}, NonInteractive: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = coding.Cleanup() })
	ctx := context.Background()
	name := "plugin.account-connectors.fixture"
	start, err := coding.StartMCPAuth(ctx, ctx, name, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = coding.FinishMCPAuth(ctx, name, start.State, "synthetic-code"); err != nil {
		t.Fatal(err)
	}
	if err = coding.Toolkit.MCPManager().Connect(ctx, name); err != nil {
		t.Fatal(err)
	}
	read := false
	for _, tool := range coding.Toolkit.MCPManager().NativeTools() {
		if tool.Definition.Name == "read" {
			_, err = tool.Client.CallTool(ctx, "read", json.RawMessage(`{}`))
			read = err == nil
		}
	}
	if !read {
		t.Fatal("Coding could not call shared MCP")
	}
	t.Cleanup(func() {
		if exchanges.Load() != 1 {
			t.Errorf("OAuth grants exchanged %d times; want one shared login", exchanges.Load())
		}
		t.Log(fmt.Sprintf("shared login exchanges=%d", exchanges.Load()))
	})
	return packages
}
