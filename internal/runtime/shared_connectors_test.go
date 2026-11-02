package runtime

import (
	"testing"

	"github.com/blueberrycongee/wuu/internal/config"
	pluginpkg "github.com/blueberrycongee/wuu/internal/plugin"
	"github.com/blueberrycongee/wuu/internal/userspace"
)

func TestUserHostOnlyAdmitsLocalAccountConnectors(t *testing.T) {
	on := true
	cfg := config.Config{MCPServers: map[string]config.MCPServerConfig{
		"project":              {Command: "must-not-run"},
		"plugin.accounts.docs": {Enabled: &on},
	}}
	items := []pluginpkg.Plugin{{Manifest: pluginpkg.Manifest{ID: "accounts", MCPServers: map[string]config.MCPServerConfig{
		"docs":     {URL: "https://example.invalid/mcp", Connector: &config.MCPConnectorDescriptor{Title: "Documents"}},
		"terminal": {Command: "must-not-run"},
	}}}}
	for _, owner := range []string{"local", "other"} {
		binding := userspace.Binding{UserSpaceID: owner, ProductDomain: "personal"}
		filtered := userHostConfig(cfg, binding)
		manager, err := startUserMCPManager(filtered, items, nil, binding)
		if err != nil {
			t.Fatal(err)
		}
		if manager == nil {
			if owner == "local" {
				t.Fatal("local connector unavailable")
			}
			continue
		}
		t.Cleanup(func() { _ = manager.Close() })
		if _, ok := manager.Config("project"); ok {
			t.Fatal("project MCP entered Personal")
		}
		if _, ok := manager.Config("plugin.accounts.terminal"); ok {
			t.Fatal("non-connector plugin MCP entered Personal")
		}
		if _, ok := manager.Config("plugin.accounts.docs"); ok != (owner == "local") {
			t.Fatal("account owner boundary failed")
		}
	}
}
