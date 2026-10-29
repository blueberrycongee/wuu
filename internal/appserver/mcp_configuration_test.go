package appserver

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/composition"
	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/credentialstore"
	"github.com/blueberrycongee/wuu/internal/extensions"
	"github.com/blueberrycongee/wuu/internal/mcp"
	pluginpkg "github.com/blueberrycongee/wuu/internal/plugin"
	"github.com/blueberrycongee/wuu/internal/runtime"
	"github.com/blueberrycongee/wuu/internal/tools"
)

// The management protocol may change an active package's startup preference,
// never its server definition, hidden fields, credentials, or owner lifecycle.
func TestMCPConnectorManagementUsesSelectedConfigurationAndFencesOwnership(t *testing.T) {
	srv, provider := newSelectedConfigSourceServer(t)
	srv.rt.WuuHome = t.TempDir()
	out := &lockedBuffer{}
	srv.out = out
	disabled := false
	item := pluginpkg.Plugin{Manifest: pluginpkg.Manifest{ID: "connector-fixture", MCPServers: map[string]config.MCPServerConfig{
		"docs":   {URL: "https://docs.example.test/mcp?token=hidden-token#secret", Enabled: &disabled, Headers: map[string]string{"Authorization": "hidden-header"}, OAuth: &config.MCPOAuthConfig{ClientSecret: "hidden-client-secret"}, Connector: &config.MCPConnectorDescriptor{Title: "Documents", DocumentationURL: "https://docs.example.test/setup"}},
		"setup":  {URL: "https://setup.example.test/mcp", Enabled: &disabled, Connector: &config.MCPConnectorDescriptor{Title: "Setup", SetupRequired: "Ask an administrator."}},
		"legacy": {URL: "https://legacy.example.test/mcp", Enabled: &disabled},
	}}, Source: "user", Fingerprint: "admitted"}
	srv.rt.Plugins = []pluginpkg.Plugin{item}
	srv.rt.ActivePlugins = []pluginpkg.Plugin{item}
	name := runtime.PluginMCPServerName(item.ID, "docs")
	call := func(id, method string, params any) map[string]any {
		t.Helper()
		callPluginPackageRPC(t, srv, id, method, params)
		return responseByID(t, parseOutput(t, out.String()), id)
	}
	listed := call("list", MethodMCPList, nil)
	if listed["error"] != nil {
		t.Fatal(listed)
	}
	list := remarshal[MCPListResult](t, listed["result"])
	var status MCPServerStatus
	for _, entry := range list.Servers {
		if entry.Name == name {
			status = entry
		}
	}
	if status.Connector == nil || status.Connector.Title != "Documents" || !status.Configurable || status.PluginActive == nil || !*status.PluginActive || status.ConfigurationEnabled == nil || *status.ConfigurationEnabled || status.ResourceURL != "https://docs.example.test/mcp" || status.Editable {
		t.Fatalf("connector status = %+v", status)
	}
	read := call("read", MethodMCPDefinitionRead, map[string]any{"name": name})
	if read["error"] != nil {
		t.Fatal(read)
	}
	definition := remarshal[MCPDefinitionResult](t, read["result"])
	if definition.Revision == "" || definition.Config.Enabled == nil || *definition.Config.Enabled {
		t.Fatalf("definition = %+v", definition)
	}
	if !reflect.DeepEqual(remarshal[map[string]any](t, read["result"])["config"], map[string]any{"enabled": false}) {
		t.Fatalf("package definition leaked: %+v", read)
	}
	if provider.writes != 0 {
		t.Fatal("listing or reading changed configuration before connect")
	}
	for _, secret := range []string{"hidden-token", "hidden-header", "hidden-client-secret"} {
		if strings.Contains(out.String(), secret) {
			t.Fatalf("public metadata leaked %s", secret)
		}
	}

	for index, test := range []struct {
		name     string
		value    any
		revision string
	}{
		{name, nil, definition.Revision}, {name, map[string]any{}, definition.Revision},
		{name, map[string]any{"enabled": true}, ""}, {name, map[string]any{"enabled": true}, "stale"},
		{name, map[string]any{"enabled": true, "url": ""}, definition.Revision},
		{name, map[string]any{"enabled": true, "headers": map[string]string{}}, definition.Revision},
		{name, map[string]any{"enabled": true, "oauth": nil}, definition.Revision},
		{name, map[string]any{"enabled": true, "connector": nil}, definition.Revision},
		{name, map[string]any{"enabled": true, "tool_timeout_ms": 0}, definition.Revision},
		{"plugin.connector-fixture.setup", map[string]any{"enabled": true}, definition.Revision},
		{"plugin.connector-fixture.legacy", map[string]any{"enabled": true}, definition.Revision},
		{"plugin.missing.injected", map[string]any{"enabled": true}, definition.Revision},
	} {
		response := call(fmt.Sprintf("reject-%d", index), MethodMCPConfigure, map[string]any{"servers": map[string]any{test.name: test.value}, "revision": test.revision})
		if response["error"] == nil || provider.writes != 0 {
			t.Fatalf("unsafe mutation accepted: %+v writes=%d", response, provider.writes)
		}
	}
	// A preference saved by Coding must not be presented as active in Personal.
	sharedView, _, err := srv.mcpConfiguration(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	enabled := true
	sharedView.Config.MCPServers = map[string]config.MCPServerConfig{name: {Enabled: &enabled}, "coding-only": {URL: "https://coding.example.test/mcp", Connector: &config.MCPConnectorDescriptor{Title: "Native metadata", SetupRequired: "Informational only"}}}
	sharedRaw, err := json.Marshal(sharedView.Config)
	if err != nil {
		t.Fatal(err)
	}
	sharedServices, releaseShared, err := srv.rt.RetainStateServices()
	if err != nil {
		t.Fatal(err)
	}
	sharedView, err = sharedServices.Config.CompareExchange(context.Background(), sharedView.Revision, sharedRaw)
	releaseShared()
	if err != nil {
		t.Fatal(err)
	}
	definition.Revision = sharedView.Revision
	provider.writes = 0
	nativeList := remarshal[MCPListResult](t, call("native-metadata", MethodMCPList, nil)["result"])
	for _, entry := range nativeList.Servers {
		if entry.Name == "coding-only" && (!entry.Editable || entry.Configurable || entry.ConfigurationEnabled == nil || !*entry.ConfigurationEnabled || entry.Connector == nil) {
			t.Fatalf("native metadata changed ordinary MCP behavior: %+v", entry)
		}
	}
	// User-bound hosts cannot borrow shared Coding preferences. Restart/runtime
	// filtering and list/read/write use the same unavailable management boundary.
	srv.rt.UserSpaceID, srv.rt.ProductDomain = "local", "personal"
	personal := remarshal[MCPListResult](t, call("personal-list", MethodMCPList, nil)["result"])
	for _, entry := range personal.Servers {
		if entry.Name == name && (entry.Configurable || entry.ConfigurationUnavailableReason == "" || entry.ConfigurationEnabled == nil || *entry.ConfigurationEnabled) {
			t.Fatalf("user-bound connector status = %+v", entry)
		}
	}
	if response := call("personal-read", MethodMCPDefinitionRead, map[string]any{"name": name}); response["error"] == nil {
		t.Fatalf("user-bound preference read accepted: %+v", response)
	}
	if response := call("personal-enable", MethodMCPConfigure, map[string]any{"servers": map[string]any{name: map[string]any{"enabled": true}}, "revision": definition.Revision}); response["error"] == nil || provider.writes != 0 {
		t.Fatalf("user-bound enable accepted: %+v", response)
	}
	if response := call("personal-auth", MethodMCPAuthStart, map[string]any{"name": name}); response["error"] == nil || !strings.Contains(fmt.Sprint(response["error"]), "domain-scoped") {
		t.Fatalf("user-bound host started auth from shared preference: %+v", response)
	}
	for _, method := range []string{MethodMCPAuthStatus, MethodMCPAuthCancel, MethodMCPAuthRemove, MethodMCPDisconnect} {
		params := map[string]any{"name": name}
		if method == MethodMCPAuthCancel {
			params["state"] = "coding-attempt"
		}
		response := call("personal-cleanup-"+method, method, params)
		if response["error"] == nil || !strings.Contains(fmt.Sprint(response["error"]), "domain-scoped") {
			t.Fatalf("user-bound host reached shared connector credentials: %s %+v", method, response)
		}
	}
	srv.rt.UserSpaceID, srv.rt.ProductDomain = "", ""
	// Source changes cannot borrow a previously active package with the same ID.
	srv.rt.Plugins[0].Source = "project"
	if response := call("source-change", MethodMCPConfigure, map[string]any{"servers": map[string]any{name: map[string]any{"enabled": true}}, "revision": definition.Revision}); response["error"] == nil || provider.writes != 0 {
		t.Fatalf("changed source accepted: %+v", response)
	}
	srv.rt.Plugins[0].Source = "user"
	srv.rt.ActivePlugins = nil
	if response := call("disabled-read", MethodMCPDefinitionRead, map[string]any{"name": name}); response["error"] == nil {
		t.Fatalf("disabled owner readable: %+v", response)
	}
	if response := call("disabled-write", MethodMCPConfigure, map[string]any{"servers": map[string]any{name: map[string]any{"enabled": true}}, "revision": definition.Revision}); response["error"] == nil || provider.writes != 0 {
		t.Fatalf("disabled owner enabled: %+v", response)
	}
	list = remarshal[MCPListResult](t, call("disabled-list", MethodMCPList, nil)["result"])
	for _, entry := range list.Servers {
		if entry.Name == name && (entry.Configurable || entry.PluginActive == nil || *entry.PluginActive || entry.Connector == nil) {
			t.Fatalf("disabled owner status = %+v", entry)
		}
	}
}

func TestMCPConnectorPreferencePreservesHiddenSourceFieldsOnCommittedActivationFailure(t *testing.T) {
	srv, provider := newSelectedConfigSourceServer(t)
	srv.rt.WuuHome = t.TempDir()
	out := &lockedBuffer{}
	srv.out = out
	ctx := context.Background()
	services, release, err := srv.rt.RetainStateServices()
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	view, err := services.Config.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var source map[string]any
	if err := json.Unmarshal(view.Source, &source); err != nil {
		t.Fatal(err)
	}
	name := "plugin.connector-fixture.docs"
	source["mcp_servers"] = map[string]any{name: map[string]any{"enabled": false, "tool_timeout_ms": 42000, "headers": map[string]string{"Authorization": "preserve-secret"}, "oauth": map[string]string{"client_secret": "preserve-client-secret"}}}
	raw, err := json.Marshal(source)
	if err != nil {
		t.Fatal(err)
	}
	view, err = services.Config.CompareExchange(ctx, view.Revision, raw)
	if err != nil {
		t.Fatal(err)
	}
	item := pluginpkg.Plugin{Manifest: pluginpkg.Manifest{ID: "connector-fixture", MCPServers: map[string]config.MCPServerConfig{"docs": {URL: "https://example.test/mcp", Connector: &config.MCPConnectorDescriptor{Title: "Documents"}}}}, Source: "user"}
	srv.rt.Plugins = []pluginpkg.Plugin{item}
	srv.rt.ActivePlugins = []pluginpkg.Plugin{item}
	// The selected provider deliberately reports a durable commit that cannot
	// activate. The handler must report that result without rollback or replay.
	provider.outcome = "committed"
	priorWrites := provider.writes
	callPluginPackageRPC(t, srv, "save", MethodMCPConfigure, map[string]any{"servers": map[string]any{name: map[string]any{"enabled": true}}, "revision": view.Revision})
	response := responseByID(t, parseOutput(t, out.String()), "save")
	if response["error"] == nil || !strings.Contains(fmt.Sprint(response["error"]), "saved") || provider.writes != priorWrites+1 {
		t.Fatalf("commit outcome = %+v writes=%d", response, provider.writes)
	}
	saved, err := services.Config.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var document struct {
		Servers map[string]map[string]any `json:"mcp_servers"`
	}
	if err := json.Unmarshal(saved.Source, &document); err != nil {
		t.Fatal(err)
	}
	got := document.Servers[name]
	if got["enabled"] != true || got["tool_timeout_ms"] != float64(42000) || !reflect.DeepEqual(got["oauth"], map[string]any{"client_secret": "preserve-client-secret"}) || !reflect.DeepEqual(got["headers"], map[string]any{"Authorization": "preserve-secret"}) || len(got) != 4 {
		t.Fatalf("preference overwrote source: %+v", got)
	}
}

func TestMCPConnectorActionAdmissionPreventsSetupAndDisabledOAuth(t *testing.T) {
	var requests, exchanges atomic.Int32
	var endpoint string
	auth := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		switch r.URL.Path {
		case "/.well-known/oauth-protected-resource":
			_ = json.NewEncoder(w).Encode(map[string]any{"resource": endpoint, "authorization_servers": []string{endpoint}})
		case "/.well-known/oauth-authorization-server":
			_ = json.NewEncoder(w).Encode(map[string]any{"issuer": endpoint, "authorization_endpoint": endpoint + "/authorize", "token_endpoint": endpoint + "/token"})
		case "/token":
			exchanges.Add(1)
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "fixture-token"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer auth.Close()
	endpoint = auth.URL
	srv, _ := newSelectedConfigSourceServer(t)
	out := &lockedBuffer{}
	srv.out = out
	name := "plugin.connector-fixture.docs"
	descriptor := &config.MCPConnectorDescriptor{Title: "Documents", SetupRequired: "Finish administrator setup."}
	item := pluginpkg.Plugin{Manifest: pluginpkg.Manifest{ID: "connector-fixture", MCPServers: map[string]config.MCPServerConfig{"docs": {URL: endpoint, Connector: descriptor}}}, Source: "user"}
	srv.rt.Plugins = []pluginpkg.Plugin{item}
	srv.rt.ActivePlugins = []pluginpkg.Plugin{item}
	kit, err := tools.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	manager := mcp.NewManager()
	defer manager.Close()
	manager.Configure(map[string]mcp.ServerConfig{name: {Name: name, URL: endpoint, OAuth: &mcp.OAuthConfig{ClientID: "fixture", RedirectURI: "http://127.0.0.1/callback"}}})
	manager.SetOAuthManager(mcp.NewOAuthManager(credentialstore.NewFileStore(filepath.Join(t.TempDir(), "oauth.json")), auth.Client()))
	kit.SetMCPManager(manager)
	srv.rt.Toolkit = kit
	call := func(id, method string, params any) map[string]any {
		t.Helper()
		callPluginPackageRPC(t, srv, id, method, params)
		return responseByID(t, parseOutput(t, out.String()), id)
	}
	actions := []string{MethodMCPConnect, MethodMCPRefresh, MethodMCPAuthStart, MethodMCPAuthFinish}
	for i, method := range actions {
		params := map[string]any{"name": name}
		if method == MethodMCPAuthFinish {
			params["state"] = "state"
			params["code"] = "code"
		}
		response := call(fmt.Sprintf("setup-%d", i), method, params)
		if response["error"] == nil || !strings.Contains(fmt.Sprint(response["error"]), "requires setup") {
			t.Fatalf("setup action admitted: %s %+v", method, response)
		}
	}
	if requests.Load() != 0 {
		t.Fatal("setup connector contacted its server")
	}
	descriptor.SetupRequired = ""
	disabled := false
	declared := item.MCPServers["docs"]
	declared.Enabled = &disabled
	item.MCPServers["docs"] = declared
	for i, method := range actions {
		params := map[string]any{"name": name}
		if method == MethodMCPAuthFinish {
			params["state"] = "state"
			params["code"] = "code"
		}
		response := call(fmt.Sprintf("configuration-disabled-%d", i), method, params)
		if response["error"] == nil || !strings.Contains(fmt.Sprint(response["error"]), "disabled") {
			t.Fatalf("disabled connector admitted: %s %+v", method, response)
		}
	}
	if requests.Load() != 0 {
		t.Fatal("configuration-disabled connector contacted its server")
	}
	declared.Enabled = nil
	item.MCPServers["docs"] = declared
	startResponse := call("start", MethodMCPAuthStart, map[string]any{"name": name})
	if startResponse["error"] != nil {
		t.Fatal(startResponse)
	}
	start := remarshal[MCPAuthStartResult](t, startResponse["result"])
	if start.State == "" {
		t.Fatal("missing OAuth attempt")
	}
	srv.rt.ActivePlugins = nil
	for i, method := range actions {
		params := map[string]any{"name": name}
		if method == MethodMCPAuthFinish {
			params["state"] = start.State
			params["code"] = "callback"
		}
		response := call(fmt.Sprintf("owner-disabled-%d", i), method, params)
		if response["error"] == nil || !strings.Contains(fmt.Sprint(response["error"]), "active plugin") {
			t.Fatalf("disabled owner admitted: %s %+v", method, response)
		}
	}
	if exchanges.Load() != 0 {
		t.Fatal("disabled owner exchanged callback for credentials")
	}
	for _, method := range []string{MethodMCPAuthStatus, MethodMCPAuthCancel, MethodMCPAuthRemove} {
		params := map[string]any{"name": name}
		if method == MethodMCPAuthCancel {
			params["state"] = start.State
		}
		if response := call("cleanup-"+method, method, params); response["error"] != nil {
			t.Fatalf("disabled cleanup rejected: %s %+v", method, response)
		}
	}
	srv.rt.Plugins = nil
	if response := call("removed-finish", MethodMCPAuthFinish, map[string]any{"name": name, "state": start.State, "code": "callback"}); response["error"] == nil || !strings.Contains(fmt.Sprint(response["error"]), "active plugin") {
		t.Fatalf("removed owner admitted: %+v", response)
	}
}

func TestMCPConnectorExplicitEnableActivatesCanonicalPackageDefinition(t *testing.T) {
	var requests atomic.Int32
	contacted := make(chan struct{}, 1)
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		select {
		case contacted <- struct{}{}:
		default:
		}
		http.Error(w, "fixture requires authentication", http.StatusUnauthorized)
	}))
	defer endpoint.Close()
	prior := newTestRuntime(t, &fakeClient{})
	preparePluginRuntimePathsForTest(t, prior)
	manifest := filepath.Join(t.TempDir(), "plugin.json")
	writePluginPackageFile(t, manifest, fmt.Sprintf(`{"id":"connector-fixture","mcp_servers":{"docs":{"url":%q,"transport":"http","enabled":false,"connector":{"title":"Documents"}}}}`, endpoint.URL))
	item, err := pluginpkg.LoadManifest(manifest, "user")
	if err != nil {
		t.Fatal(err)
	}
	prior.Plugins = []pluginpkg.Plugin{item}
	cfg, _, err := config.LoadPath(prior.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Extensions = &extensions.Settings{}
	if err := cfg.Extensions.RecordGrant(extensions.Grant{SubjectID: item.SubjectID, Fingerprint: item.Fingerprint, Permissions: item.EffectivePermissions}); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(prior.ConfigPath, raw, 0600); err != nil {
		t.Fatal(err)
	}
	rt := newConfigRefreshRuntime(t, prior)
	out := &lockedBuffer{}
	// The fixture drives every generation transition explicitly, without watchers.
	srv := newOwnerHandlerFixture(rt, out)
	name := runtime.PluginMCPServerName(item.ID, "docs")
	callPluginPackageRPC(t, srv, "read-lazy", MethodMCPDefinitionRead, map[string]any{"name": name})
	read := responseByID(t, parseOutput(t, out.String()), "read-lazy")
	if read["error"] != nil {
		t.Fatal(read)
	}
	definition := remarshal[MCPDefinitionResult](t, read["result"])
	if requests.Load() != 0 {
		t.Fatal("disabled package contacted server before connect")
	}
	callPluginPackageRPC(t, srv, "direct-disabled", MethodMCPConnect, map[string]any{"name": name})
	if responseByID(t, parseOutput(t, out.String()), "direct-disabled")["error"] == nil || requests.Load() != 0 {
		t.Fatal("direct connect bypassed startup preference")
	}
	callPluginPackageRPC(t, srv, "enable", MethodMCPConfigure, map[string]any{"servers": map[string]any{name: map[string]any{"enabled": true}}, "revision": definition.Revision})
	response := responseByID(t, parseOutput(t, out.String()), "enable")
	if response["error"] != nil {
		t.Fatalf("explicit enable failed: %+v", response)
	}
	select {
	case <-contacted:
	case <-time.After(5 * time.Second):
		t.Fatal("explicit enable never activated package server")
	}
	saved, _, err := config.LoadPath(rt.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	preference := saved.MCPServers[name]
	if preference.Enabled == nil || !*preference.Enabled || preference.URL != "" || preference.Connector != nil {
		t.Fatalf("package definition copied into config: %+v", preference)
	}
	configured, ok := rt.Toolkit.MCPManager().Config(name)
	if !ok || configured.URL != endpoint.URL || !configured.IsEnabled() {
		t.Fatalf("canonical runtime definition not enabled: %+v", configured)
	}
	restarted := newConfigRefreshRuntime(t, prior)
	restored, ok := restarted.Toolkit.MCPManager().Config(name)
	if !ok || !restored.IsEnabled() || restored.URL != endpoint.URL {
		t.Fatalf("restart lost package preference: %+v", restored)
	}

}

// Exercise shipped inert declarations through real discovery, composition,
// runtime admission and the public protocol, never by assigning ActivePlugins.
func TestBundledLazyConnectorsHonorRuntimeComposition(t *testing.T) {
	var bundled pluginpkg.Plugin
	for _, item := range pluginpkg.Discover(t.TempDir(), t.TempDir()) {
		if item.ID == "account-connectors" {
			bundled = item
			break
		}
	}
	if bundled.ID == "" || !bundled.Official || bundled.Runtime != nil {
		t.Fatalf("missing bundled declarative connector package: %+v", bundled)
	}
	if len(bundled.MCPServers) == 0 {
		t.Fatal("bundled package has no connector declarations")
	}
	// Fail before constructing a runtime if a catalog change would make this
	// local-only fixture contact an external service during startup.
	for name, definition := range bundled.MCPServers {
		if definition.Enabled == nil || *definition.Enabled {
			t.Fatalf("bundled connector %s is not lazy", name)
		}
	}
	for _, test := range []struct {
		name                                    string
		explicit, selected, disabled, untrusted bool
		wantActive                              bool
	}{
		{name: "inferred", wantActive: true},
		{name: "desktop profile excludes addon", explicit: true},
		{name: "desktop profile selects addon", explicit: true, selected: true, wantActive: true},
		{name: "disabled owner", disabled: true},
		{name: "untrusted same manifest", untrusted: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			prior := newTestRuntime(t, &fakeClient{})
			preparePluginRuntimePathsForTest(t, prior)
			item := bundled
			if test.untrusted {
				var err error
				item, err = pluginpkg.LoadManifest(bundled.ManifestPath, "user")
				if err != nil {
					t.Fatal(err)
				}
			}
			prior.Plugins = []pluginpkg.Plugin{item}
			cfg, _, err := config.LoadPath(prior.ConfigPath)
			if err != nil {
				t.Fatal(err)
			}
			if test.explicit {
				cfg.Composition = &composition.Config{Version: 3, Profile: "desktop"}
				if test.selected {
					cfg.Composition.Patches = []composition.Patch{{Modules: map[string]bool{bundled.ID: true}}}
				}
			}
			if test.disabled {
				cfg.Extensions = &extensions.Settings{}
				cfg.Extensions.SetDisabled(item.SubjectID, true)
			}
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
			callPluginPackageRPC(t, srv, "bundled-list", MethodMCPList, nil)
			response := responseByID(t, parseOutput(t, out.String()), "bundled-list")
			if response["error"] != nil {
				t.Fatal(response)
			}
			listed := remarshal[MCPListResult](t, response["result"])
			if len(listed.Servers) != len(bundled.MCPServers) {
				t.Fatalf("lazy inventory omitted declarations: %+v", listed)
			}
			for _, status := range listed.Servers {
				if status.PluginActive == nil || *status.PluginActive != test.wantActive || status.Configurable != test.wantActive || status.Connector == nil || status.ConfigurationEnabled == nil || *status.ConfigurationEnabled || status.Connected {
					t.Fatalf("lazy connector admission: %+v", status)
				}
				callPluginPackageRPC(t, srv, "definition-"+status.Name, MethodMCPDefinitionRead, map[string]any{"name": status.Name})
				definition := responseByID(t, parseOutput(t, out.String()), "definition-"+status.Name)
				if (definition["error"] == nil) != test.wantActive {
					t.Fatalf("lazy preference admission: %+v", definition)
				}
				if test.wantActive {
					manager, err := srv.mcpManager()
					if err != nil {
						t.Fatal(err)
					}
					runtimeConfig, exists := manager.Config(status.Name)
					if !exists || runtimeConfig.IsEnabled() {
						t.Fatalf("disabled definition not registered lazily: %+v", runtimeConfig)
					}
					callPluginPackageRPC(t, srv, "auth-"+status.Name, MethodMCPAuthStatus, map[string]any{"name": status.Name})
					auth := responseByID(t, parseOutput(t, out.String()), "auth-"+status.Name)
					if auth["error"] != nil {
						t.Fatalf("lazy connector local auth status unavailable: %+v", auth)
					}
					if got := remarshal[MCPAuthStatusResult](t, auth["result"]); got.Authenticated || len(got.Scopes) != 0 {
						t.Fatalf("fresh connector invented authorization: %+v", got)
					}
				}
			}
		})
	}
}

func TestLazyConnectorSurvivesCatalogAndConfigReload(t *testing.T) {
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
	manifest := fmt.Sprintf(`{"id":"reload-connector","mcp_servers":{"docs":{"url":%q},"account":{"url":%q,"enabled":false,"connector":{"title":"Fixture"}}}}`, endpoint.URL, endpoint.URL)
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
			t.Fatalf("lazy connector lost its manager after reload %d: %+v", index, response)
		}
		if err := srv.refreshConfigIfChanged(); err != nil {
			t.Fatal(err)
		}
	}
	// Enabling a lazy connector must retain the local manager that owns its
	// upcoming OAuth flow, even though that server cannot be ready yet.
	callPluginPackageRPC(t, srv, "reload-read", MethodMCPDefinitionRead, map[string]any{"name": "plugin.reload-connector.account"})
	read := responseByID(t, parseOutput(t, out.String()), "reload-read")
	if read["error"] != nil {
		t.Fatal(read["error"])
	}
	definition := remarshal[MCPDefinitionResult](t, read["result"])
	callPluginPackageRPC(t, srv, "reload-enable", MethodMCPConfigure, map[string]any{"servers": map[string]any{"plugin.reload-connector.account": map[string]any{"enabled": true}}, "revision": definition.Revision})
	if response := responseByID(t, parseOutput(t, out.String()), "reload-enable"); response["error"] != nil {
		t.Fatal(response["error"])
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
