package sdk

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/appserver"
	"github.com/blueberrycongee/wuu/internal/clientauthority"
	"github.com/blueberrycongee/wuu/internal/loopdriver"
	"github.com/blueberrycongee/wuu/internal/principal"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/userspace"
	"github.com/blueberrycongee/wuu/internal/version"
)

// Exercise the real SDK host, product override, generated plugin process,
// initialization inventory with an existing v4 config.
func TestPersonalGenUIDefaultSDKHost(t *testing.T) {
	previousVersion := version.Version
	version.Version = "2026.10.19"
	t.Cleanup(func() { version.Version = previousVersion })
	t.Setenv("WUU_HOME", t.TempDir())
	configPath := filepath.Join(t.TempDir(), "config.json")
	config := `{"composition":{"version":4,"profile":"desktop"},"ptc":{"enabled":true},"default_provider":"fixture","providers":{"fixture":{"type":"openai-compatible","base_url":"https://model.invalid/v1","model":"fixture"}}}`
	if err := os.WriteFile(configPath, []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	rt, err := New(Options{CompositionProfile: "personal", UserSpaceID: "local", ProductDomain: "personal", WorkDir: t.TempDir(), HomeDir: t.TempDir(), ConfigPath: configPath})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rt.Close() })
	cfg, _, err := rt.core.LoadEffectiveConfig()
	if err != nil || cfg.Composition == nil || cfg.Composition.Version != 4 || cfg.Composition.Profile != "personal" {
		t.Fatalf("effective composition = %+v, %v", cfg.Composition, err)
	}
	tool, ok := rt.core.PluginHost.DeclaredTool("generative-ui", "render_ui")
	if !ok || tool.PluginID != "generative-ui" {
		t.Fatalf("GenUI tool unavailable: %+v", tool)
	}
	thread, err := rt.core.NewThreadRuntime("genui-ptc-check")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = thread.CloseComposition() })
	name := ""
	codeMode := false
	for _, definition := range thread.StreamRunner.Tools.Definitions() {
		if definition.Name == "run_code" {
			codeMode = true
		}
		if strings.Contains(definition.Name, "generative_ui_render_ui") {
			if !definition.DirectOnly {
				t.Fatal("render_ui must be direct in PTC mode")
			}
			name = definition.Name
		}
	}
	if !codeMode {
		t.Fatal("Personal PTC model surface does not expose run_code")
	}
	if name == "" {
		t.Fatal("Personal PTC model surface does not expose render_ui")
	}
	execution := loopdriver.WithExecutionContext(context.Background(), loopdriver.ExecutionContext{SessionID: "genui-ptc-check", ExecutionID: "turn"})
	arguments := `{"spec":{"version":1,"title":"Example","fallback":"Example text","blocks":[{"id":"intro","type":"text","text":"Example text"}]}}`
	result, err := thread.StreamRunner.Tools.(agent.RichToolExecutor).ExecuteResult(execution, providers.ToolCall{ID: "call", Name: name, Arguments: arguments})
	if err != nil || result.IsError || len(result.Content) != 2 {
		t.Fatalf("Personal PTC render_ui: %+v, %v", result, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	binding := userspace.Binding{UserSpaceID: "local", ProductDomain: "personal"}
	owner, err := clientauthority.AdmitDesktop(ctx, binding, strings.NewReader(`{"version":1,"user_space_id":"local","product_domain":"personal"}`))
	if err != nil {
		t.Fatal(err)
	}
	client, err := rt.Connect(owner, ClientOptions{Name: "genui-personal-default"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close(context.Background()) })
	var init struct {
		Inventory []appserver.ExtensionInventoryRecord `json:"extension_inventory"`
	}
	if err := json.Unmarshal(client.Initialization().Raw, &init); err != nil {
		t.Fatal(err)
	}
	var found *appserver.ExtensionInventoryRecord
	for i := range init.Inventory {
		if init.Inventory[i].Provenance.PluginID == "generative-ui" {
			found = &init.Inventory[i]
			break
		}
	}
	if found == nil || found.Enabled == nil || !*found.Enabled || found.Desktop == nil || found.RuntimeState != appserver.ExtensionRuntimeActive {
		t.Fatalf("GenUI missing from active desktop inventory: %+v", found)
	}
	if len(found.Desktop.ContentRenderers) != 1 || found.Desktop.ContentRenderers[0].Category != "tool-result" || found.Desktop.ContentRenderers[0].Match != "application/vnd.wuu.ui+json" {
		t.Fatalf("GenUI desktop renderer capability missing: %+v", found.Desktop.ContentRenderers)
	}
	params := map[string]any{"id": found.ID, "fingerprint": found.Fingerprint, "graph_generation": found.GraphGeneration}
	var module appserver.PluginDesktopModuleReadResult
	if err := client.rpc.call(ctx, appserver.MethodPluginDesktopModuleRead, params, &module); err != nil || !strings.Contains(module.Source, "registerRenderer") {
		t.Fatalf("desktop module read: %v", err)
	}
	params["graph_generation"] = "foreign-graph"
	if err := client.rpc.call(ctx, appserver.MethodPluginDesktopModuleRead, params, &module); err == nil {
		t.Fatal("foreign graph loaded the Personal desktop module")
	}
	params["graph_generation"] = found.GraphGeneration
	params["fingerprint"] = "wrong-fingerprint"
	if err := client.rpc.call(ctx, appserver.MethodPluginDesktopModuleRead, params, &module); err == nil {
		t.Fatal("wrong fingerprint loaded the Personal desktop module")
	}
	params["fingerprint"] = found.Fingerprint
	delete(params, "graph_generation")
	if err := client.rpc.call(ctx, appserver.MethodPluginDesktopModuleRead, params, &module); err == nil {
		t.Fatal("unbound module read was accepted")
	}
	var ack appserver.OKResult
	activation := map[string]any{"id": found.ID, "fingerprint": found.Fingerprint, "graph_generation": found.GraphGeneration, "activation": 1, "phase": "registration", "status": "active"}
	if err := client.rpc.call(ctx, appserver.MethodPluginDesktopActivation, activation, &ack); err != nil {
		t.Fatalf("desktop activation: %v", err)
	}
	other, err := rt.Connect(owner, ClientOptions{Name: "genui-second-window"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = other.Close(context.Background()) })
	if err := other.rpc.call(ctx, appserver.MethodPluginDesktopActivation, activation, &ack); err != nil {
		t.Fatalf("second desktop activation: %v", err)
	}
	clients := func() int {
		for _, item := range rt.core.PluginHost.ServiceRegistry().Snapshot(0).Composition.Modules {
			if item.ID == "generative-ui" && item.Renderer != nil {
				return len(item.Renderer.Clients)
			}
		}
		return 0
	}
	if clients() != 2 {
		t.Fatalf("separate windows share renderer authority: %d", clients())
	}
	if err := other.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if clients() != 1 {
		t.Fatalf("disconnect removed another window's renderer: %d", clients())
	}
	nonOwner, err := principal.Authenticated(principal.Identity{Kind: "user", Issuer: "fixture", Tenant: "fixture", Subject: "other"})
	if err != nil {
		t.Fatal(err)
	}
	guest, err := rt.Connect(principal.WithContext(ctx, nonOwner), ClientOptions{Name: "genui-guest"})
	if err == nil {
		defer guest.Close(context.Background())
		params["graph_generation"] = found.GraphGeneration
		if err := guest.rpc.call(ctx, appserver.MethodPluginDesktopModuleRead, params, &module); err == nil {
			t.Fatal("non-owner read a Personal desktop module")
		}
		if err := guest.rpc.call(ctx, appserver.MethodPluginDesktopActivation, activation, &ack); err == nil {
			t.Fatal("non-owner registered a Personal renderer")
		}
	}
	activation["graph_generation"] = "foreign-graph"
	if err := client.rpc.call(ctx, appserver.MethodPluginDesktopActivation, activation, &ack); err == nil {
		t.Fatal("foreign graph accepted a Personal activation")
	}
}
