package runtime

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

	"github.com/blueberrycongee/wuu/internal/composition"
	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/credentialstore"
	"github.com/blueberrycongee/wuu/internal/loopdriver"
	"github.com/blueberrycongee/wuu/internal/mcp"
	pluginpkg "github.com/blueberrycongee/wuu/internal/plugin"
	"github.com/blueberrycongee/wuu/internal/providers"
)

func TestSharedGlobalMCPUserHostAdmissionAndDisable(t *testing.T) {
	var calls atomic.Int32
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		var req struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch req.Method {
		case "initialize":
			fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"result":{"protocolVersion":"2025-06-18","capabilities":{"tools":{}},"serverInfo":{"name":"fixture","version":"1"}}}`, req.ID)
		case "tools/list":
			fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"result":{"tools":[{"name":"observe","inputSchema":{"type":"object"}}]}}`, req.ID)
		case "tools/call":
			calls.Add(1)
			fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"result":{"content":[{"type":"text","text":"fixture-result"}]}}`, req.ID)
		default:
			w.WriteHeader(http.StatusAccepted)
		}
	}))
	defer peer.Close()
	home := t.TempDir()
	enabled, disabled := true, false
	item := pluginpkg.Plugin{Manifest: pluginpkg.Manifest{ID: "plugin-fixture", MCPServers: map[string]config.MCPServerConfig{
		"docs": {URL: peer.URL, Transport: "http"},
	}}, Source: "bundled", Official: true, SubjectID: "fixture-plugin", Fingerprint: "fixture-v1"}
	name := "docs"
	cfg := config.Default()
	cfg.PTC.Enabled = false
	cfg.DefaultProvider = "fixture"
	cfg.Providers = map[string]config.ProviderConfig{"fixture": {Type: "openai-compatible", BaseURL: "http://127.0.0.1:9/v1", Model: "fixture", APIKey: "synthetic"}}
	cfg.MCPServers = map[string]config.MCPServerConfig{name: {URL: peer.URL, Transport: "http", Enabled: &enabled}}
	cfg.Composition = composition.DefaultConfig("headless")
	cfg.Composition.Patches = []composition.Patch{{Modules: map[string]bool{credentialstore.OSModule: false, credentialstore.FileModule: true, "plugin-fixture": true}}}
	catalog := []pluginpkg.Plugin{item}
	configPath := filepath.Join(home, "selected-config.json")
	source, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, source, 0600); err != nil {
		t.Fatal(err)
	}
	open := func(shared bool) *Session {
		t.Helper()
		rt, err := NewSession(Options{RootDir: filepath.Join(home, "project"), HomeDir: home, WuuHome: home, Config: cfg, ConfigPath: configPath, ConfigLoadMode: ConfigLoadFile, UserSpaceID: "owner", ProductDomain: "personal", SharedMCPConnectors: shared, DiscoveredPlugins: &catalog, NonInteractive: true})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _, _ = rt.Cleanup() })
		return rt
	}
	isolated := open(false)
	if isolated.Toolkit.MCPManager() != nil {
		t.Fatal("unadmitted host inherited MCP manager")
	}
	rt := open(true)
	mgr := rt.Toolkit.MCPManager()
	if mgr == nil {
		t.Fatalf("shared host has no MCP manager; snapshot=%+v toolkit=%v", rt.ExtensionSnapshot(), rt.Toolkit != nil)
	}
	if err := mgr.Connect(context.Background(), name); err != nil {
		t.Fatal(err)
	}
	if _, ok := mgr.Config(PluginMCPServerName(item.ID, "docs")); ok {
		t.Fatal("plugin MCP crossed host boundary")
	}
	native := mgr.NativeTools()
	if len(native) != 1 {
		t.Fatalf("MCP tool discovery: %d", len(native))
	}

	tool := mcp.NewMCPTool(native[0].Client, native[0].Definition)
	rt.Toolkit.SetToolSearchEnabled(false)
	defs := rt.Toolkit.Definitions()
	discovered := false
	for _, def := range defs {
		if def.Name == tool.Name() {
			discovered = true
		}
	}
	if !discovered {
		t.Fatal("shared server tool not offered to the main session")
	}
	call := providers.ToolCall{ID: "shared-call", Name: tool.Name(), Arguments: "{}"}
	if _, err := rt.Toolkit.ExecuteResult(context.Background(), call); err != nil {
		t.Fatalf("main call: %v; surface=%+v", err, rt.Toolkit.ActiveSurface())
	}
	worker, err := rt.Toolkit.CloneForRoot(rt.RootDir)
	if err != nil {
		t.Fatal(err)
	}
	worker.SetToolSearchEnabled(false)
	if _, err := worker.ExecuteResult(context.Background(), call); err != nil {
		t.Fatalf("worker call: %v", err)
	}
	if calls.Load() != 2 {
		t.Fatalf("remote calls = %d", calls.Load())
	}
	if err := rt.PinExecutionEnvironment("fresh-worker", ""); err != nil {
		t.Fatal(err)
	}
	thread, err := rt.NewThreadCapabilityRuntime("fresh-worker", rt.RootDir, ThreadModelSelection{PermissionMode: "standard"})
	if err != nil {
		t.Fatal(err)
	}
	defer rt.releasePluginGeneration(thread.PluginGeneration)
	foundBackground := false
	for _, def := range thread.Toolkit.ExternalCapabilityDefinitions() {
		if def.Name == call.Name {
			foundBackground = true
		}
	}
	if !foundBackground {
		t.Fatal("fresh Personal external-engine task cannot discover the shared server")
	}
	if _, err := thread.CapabilityTools.Execute(loopdriver.WithExecutionContext(context.Background(), loopdriver.ExecutionContext{SessionID: "fresh-worker", ExecutionID: "turn-one"}), call); err != nil {
		t.Fatalf("fresh Personal external-engine task: %v", err)
	}
	if err := rt.PinExecutionEnvironment("read-only-worker", ""); err != nil {
		t.Fatal(err)
	}
	readOnly, err := rt.NewThreadCapabilityRuntime("read-only-worker", rt.RootDir, ThreadModelSelection{PermissionMode: "read_only"})
	if err != nil {
		t.Fatal(err)
	}
	defer rt.releasePluginGeneration(readOnly.PluginGeneration)
	deniedCall := call
	deniedCall.ID = "denied-call"
	if _, err := readOnly.CapabilityTools.Execute(loopdriver.WithExecutionContext(context.Background(), loopdriver.ExecutionContext{SessionID: "read-only-worker", ExecutionID: "turn-two"}), deniedCall); err == nil {
		t.Fatal("read-only external task invoked a write-capable server tool")
	}
	external, err := rt.Toolkit.CloneForRoot(rt.RootDir)
	if err != nil {
		t.Fatal(err)
	}
	external.ConfigureExternalCapabilities()
	for _, def := range external.ExternalCapabilityDefinitions() {
		if def.Name == call.Name {
			t.Fatal("external engine got unselected server")
		}
	}
	if _, err := external.ExecuteResult(context.Background(), call); err == nil {
		t.Fatal("external engine invoked unselected server")
	}
	external.SetConversationTools([]string{name}, nil)
	found := false
	for _, def := range external.ExternalCapabilityDefinitions() {
		if def.Name == call.Name {
			found = true
		}
	}
	if !found {
		t.Fatal("selected server missing from external capability catalog")
	}
	if _, err := external.ExecuteResult(context.Background(), call); err != nil {
		t.Fatalf("selected external call: %v", err)
	}
	ConfigureToolkitPermissions(worker, config.ResolvedPermissions{Mode: "read_only"})
	if _, err := worker.ExecuteResult(context.Background(), call); err == nil {
		t.Fatal("read-only worker invoked a write-capable tool")
	}
	if calls.Load() != 4 {
		t.Fatalf("unexpected remote calls before reload: %d", calls.Load())
	}
	old := rt.RetainPluginGeneration()
	defer rt.releasePluginGeneration(old)
	candidate, err := rt.PreflightDiscoveredExtensions(cfg, catalog)
	if err != nil {
		t.Fatalf("prepare plugin generation: %v", err)
	}
	if err := rt.ActivatePluginGeneration(candidate, nil); err != nil {
		t.Fatalf("activate plugin generation: %v", err)
	}
	replacement := rt.Toolkit.MCPManager()
	if replacement == nil || replacement == mgr {
		t.Fatal("reload did not create replacement server manager")
	}
	if err := replacement.Connect(context.Background(), name); err != nil {
		t.Fatalf("connect after reload: %v", err)
	}
	_ = rt.Toolkit.Definitions()
	query, _ := json.Marshal(map[string]string{"query": "select:" + call.Name})
	if _, err := rt.Toolkit.ExecuteResult(context.Background(), providers.ToolCall{Name: "tool_search", Arguments: string(query)}); err != nil {
		t.Fatalf("discover after reload: %v", err)
	}
	if _, err := rt.Toolkit.ExecuteResult(context.Background(), call); err != nil {
		t.Fatalf("call after reload: %v", err)
	}
	if calls.Load() != 5 {
		t.Fatalf("remote calls after reload = %d", calls.Load())
	}
	coding, err := NewSession(Options{RootDir: filepath.Join(home, "coding"), HomeDir: home, WuuHome: home, Config: cfg, ConfigPath: configPath, ConfigLoadMode: ConfigLoadFile, DiscoveredPlugins: &catalog, NonInteractive: true})
	if err != nil {
		t.Fatal(err)
	}
	defer coding.Cleanup()
	codingManager := coding.Toolkit.MCPManager()
	if err := codingManager.Connect(context.Background(), name); err != nil {
		t.Fatal(err)
	}
	codingNative := codingManager.NativeTools()
	var codingTool *mcp.MCPTool
	for _, native := range codingNative {
		if native.Client.Name() == name {
			codingTool = mcp.NewMCPTool(native.Client, native.Definition)
		}
	}
	if codingTool == nil {
		t.Fatal("Coding server tool missing")
	}
	if _, err := codingTool.ExecuteResult(context.Background(), `{}`); err != nil {
		t.Fatalf("Coding tool: %v", err)
	}
	if calls.Load() != 6 {
		t.Fatalf("Coding remote calls: %d", calls.Load())
	}
	services, release, err := rt.RetainStateServices()
	if err != nil {
		t.Fatal(err)
	}
	view, err := services.Config.Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	view.Config.MCPServers[name] = config.MCPServerConfig{URL: peer.URL, Transport: "http", Enabled: &disabled}
	raw, err := json.Marshal(view.Config)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = services.Config.CompareExchange(context.Background(), view.Revision, raw); err != nil {
		t.Fatal(err)
	}
	release()
	if _, err := tool.ExecuteResult(context.Background(), `{}`); err == nil {
		t.Fatal("captured tool dispatched after disable")
	}
	if calls.Load() != 6 {
		t.Fatal("disabled tool reached remote")
	}
}
