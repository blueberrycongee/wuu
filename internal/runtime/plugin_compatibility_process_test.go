package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/composition"
	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/credentialstore"
	pluginpkg "github.com/blueberrycongee/wuu/internal/plugin"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/pluginsettings"
)

// This is an old-artifact/new-host process gate, not an author build against
// today's SDK. Failure cases: changed retained bytes, pre-activation effects,
// incompatible preflight, lost settings on reload, and revived retired handles.
func TestHistoricalPluginArtifactOnCurrentHost(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node.js is unavailable; the dedicated compatibility gate rejects skips")
	}
	fixture, err := filepath.Abs("../../scripts/fixtures/plugin-compatibility")
	if err != nil {
		t.Fatal(err)
	}
	baseline := filepath.Join(fixture, "2026-10-22")
	provenanceBytes, err := os.ReadFile(filepath.Join(baseline, "provenance.json"))
	if err != nil {
		t.Fatal(err)
	}
	var provenance struct {
		Revision string            `json:"revision"`
		Files    map[string]string `json:"files"`
	}
	if err := json.Unmarshal(provenanceBytes, &provenance); err != nil {
		t.Fatal(err)
	}
	if len(provenance.Files) == 0 {
		t.Fatal("historical artifact inventory is empty")
	}
	for name, want := range provenance.Files {
		bytes, err := os.ReadFile(filepath.Join(baseline, name))
		if err != nil || fmt.Sprintf("%x", sha256.Sum256(bytes)) != want {
			t.Fatalf("retained artifact %s changed: %v; add a baseline, do not rebuild this one", name, err)
		}
	}
	t.Logf("verified retained SDK package and shipped TODO bytes from %s", provenance.Revision)

	ctx := context.Background()
	workspace := t.TempDir()
	home, state := testWorkspaceState(t, workspace)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("WUU_HOME", home)
	probe := testRuntimePlugin("historical-probe")
	probe.Root, probe.Version = fixture, "0.1.0"
	probe.SubjectID, probe.Fingerprint = "plugin:user:historical-probe", "new-generation-fingerprint"
	probe.Runtime.Command, probe.Runtime.Args = node, []string{"probe.mjs"}
	probe.Settings = map[string]pluginpkg.SettingDefinition{
		"historical-probe.label": {Type: pluginpkg.SettingTypeString, Title: "Label", Default: "default", Scope: pluginpkg.SettingScopeUser, Apply: pluginpkg.SettingApplyRestart},
	}
	todo := testRuntimePlugin("historical-todo")
	todo.Root, todo.Runtime.Command, todo.Runtime.Args = baseline, node, []string{"todo.mjs"}
	modules := []string{probe.ID, todo.ID, kernelHostModule, pluginsettings.Module, config.Module, credentialstore.FileModule, ArtifactModule}
	selection := composition.Config{Version: 3, Modules: &modules}
	configure := func(value string) pluginsettings.Document {
		t.Helper()
		document, err := pluginsettings.Update(home, workspace, probe.SubjectID, pluginsettings.ScopeUser, "old-package-fingerprint", func(values map[string]json.RawMessage) error {
			values["label"], _ = json.Marshal(value)
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		return document
	}
	before := configure("configured-before-upgrade")
	build := func(items []pluginpkg.Plugin, selected *composition.Config) (*pluginhost.Host, error) {
		host, _, err := buildPluginHost(items, workspace, "compatibility", home, state, nil, startPluginClient, nil, nil, selected, "")
		if host != nil {
			t.Cleanup(func() { _ = host.Close(ctx) })
		}
		return host, err
	}
	host, err := build([]pluginpkg.Plugin{probe, todo}, &selection)
	if err != nil {
		t.Fatal(err)
	}
	capabilities := host.Capabilities(pluginhost.CapabilityPluginClientRequest)
	if len(capabilities) != 1 {
		t.Fatalf("historical capability was not registered: %+v", capabilities)
	}
	var response map[string]any
	if err := host.InvokeCapability(ctx, capabilities[0], map[string]any{}, &response); err == nil {
		t.Fatal("prepared process was callable before activation")
	}
	if err := host.Activate(ctx); err != nil {
		t.Fatal(err)
	}
	invoke := func(h *pluginhost.Host, want string) map[string]any {
		t.Helper()
		var result map[string]any
		// Additive input fields must not make the historical SDK reject a call.
		err := h.InvokeCapability(ctx, h.Capabilities(pluginhost.CapabilityPluginClientRequest)[0], map[string]any{"method": "settings", "optional_from_new_host": true}, &result)
		if err != nil || result["prepared"] != want || result["value"] != want || !reflect.DeepEqual(result["entries"], map[string]any{"label": want}) {
			t.Fatalf("old settings get/list failed: result=%+v err=%v", result, err)
		}
		return result
	}
	first := invoke(host, "configured-before-upgrade")
	tools := host.ToolDefinitions()
	if len(tools) != 1 {
		t.Fatalf("shipped historical TODO registration = %+v", tools)
	}
	todoResult, err := host.ExecuteTool(ctx, tools[0].Name, pluginhost.ToolExecuteInput{SessionID: "compat-thread", ThreadID: "compat-thread", TurnID: "compat-turn", CWD: workspace, CallID: "historical-todo", Arguments: json.RawMessage(`{"todos":[{"content":"Keep history","status":"in_progress"}]}`)})
	if err != nil || !strings.Contains(fmt.Sprint(todoResult), "Keep history") {
		t.Fatalf("unmodified shipped TODO cannot execute: %+v %v", todoResult, err)
	}

	// A missing selected dependency cannot be silently supplied by a default.
	missingModules := []string{probe.ID, todo.ID}
	missing := composition.Config{Version: 3, Modules: &missingModules}
	if rejected, err := build([]pluginpkg.Plugin{probe, todo}, &missing); err == nil || rejected != nil || !strings.Contains(err.Error(), "host.settings") {
		t.Fatalf("missing settings selection did not reject preparation: host=%v err=%v", rejected, err)
	}
	invoke(host, "configured-before-upgrade")
	// A different major cannot satisfy the old consumer, even while a compatible
	// native provider exists. An explicit selection must not become a fallback.
	future := testRuntimePlugin("future-settings")
	future.Root = fixture
	future.Runtime = &pluginpkg.RuntimeSpec{Protocol: pluginhost.ProtocolName, Command: node, Args: []string{"negotiation.mjs"}, Env: map[string]string{"FIXTURE_PROTOCOL": "3", "FIXTURE_SETTINGS_MAJOR": "2"}}
	futureModules := append(append([]string(nil), modules...), future.ID)
	futureSelection := composition.Config{Version: 3, Modules: &futureModules, Services: []composition.Binding{{Service: "host.settings.get", Major: 1, Provider: future.ID}}}
	if rejected, err := build([]pluginpkg.Plugin{probe, todo, future}, &futureSelection); err == nil || rejected != nil || !strings.Contains(err.Error(), "host.settings.get") || !strings.Contains(err.Error(), future.ID) {
		t.Fatalf("incompatible selected major fell back to native settings: host=%v err=%v", rejected, err)
	}
	invoke(host, "configured-before-upgrade")
	// A new executable cannot accept a future capability protocol by guessing.
	bad := probe
	bad.Runtime = &pluginpkg.RuntimeSpec{Protocol: pluginhost.ProtocolName, Command: node, Args: []string{"negotiation.mjs"}, Env: map[string]string{"FIXTURE_PROTOCOL": "99"}}
	if rejected, err := build([]pluginpkg.Plugin{bad, todo}, &selection); err == nil || rejected != nil || !strings.Contains(err.Error(), "unsupported negotiated protocol version 99") {
		t.Fatalf("future version did not reject preparation: host=%v err=%v", rejected, err)
	}
	invoke(host, "configured-before-upgrade")
	after, err := pluginsettings.Read(home, workspace, probe.SubjectID, pluginsettings.ScopeUser)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("preflight or reads rewrote persisted settings: before=%+v after=%+v err=%v", before, after, err)
	}

	configure("configured-after-reload")
	reloaded, err := build([]pluginpkg.Plugin{probe, todo}, &selection)
	if err != nil {
		t.Fatal(err)
	}
	if err := reloaded.Activate(ctx); err != nil {
		t.Fatal(err)
	}
	second := invoke(reloaded, "configured-after-reload")
	if first["pid"] == second["pid"] {
		t.Fatal("reload did not create a new process")
	}
	if err := host.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if err := host.InvokeCapability(ctx, capabilities[0], nil, &response); !errors.Is(err, pluginhost.ErrCapabilityRetired) {
		t.Fatalf("retired old-generation handle did not fail: %v", err)
	}
	registration := reloaded.Capabilities(pluginhost.CapabilityPluginClientRequest)[0]
	if outcome, found := reloaded.RetirePlugin(ctx, probe.ID, errors.New("explicit removal")); !found || outcome.Err != nil {
		t.Fatalf("revoke historical process: %+v found=%v", outcome, found)
	}
	if err := reloaded.InvokeCapability(ctx, registration, nil, &response); !errors.Is(err, pluginhost.ErrCapabilityRetired) {
		t.Fatalf("removed historical handle was revived: %v", err)
	}
	if len(reloaded.ToolDefinitions()) != 1 {
		t.Fatal("revocation removed an independent historical plugin")
	}
	t.Log("configure, prepare, activate, invoke, failed upgrade, reload, retained data, and revoke passed without current-SDK compilation")
}

func TestPluginCompatibilityProtocolBoundaries(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node.js is unavailable; the dedicated compatibility gate rejects skips")
	}
	fixture, _ := filepath.Abs("../../scripts/fixtures/plugin-compatibility")
	for _, test := range []struct{ version, lifecycle, diagnostic string }{
		{"missing", "2", ""}, {"0", "2", ""}, {"1", "2", ""}, {"2", "2", ""}, {"3", "2", ""},
		{"-1", "2", "unsupported negotiated protocol version -1"}, {"99", "2", "unsupported negotiated protocol version 99"},
		{"3", "0", "runtime lifecycle version 2 is required for generation preflight (received 0)"},
		{"3", "1", "runtime lifecycle version 2 is required for generation preflight (received 1)"},
		{"3", "99", "runtime lifecycle version 2 is required for generation preflight (received 99)"},
	} {
		t.Run(test.version+"-lifecycle-"+test.lifecycle, func(t *testing.T) {
			process, err := pluginhost.Start(context.Background(), pluginhost.ProcessConfig{ID: "compatibility-wire", Command: node, Args: []string{"negotiation.mjs"}, PluginRoot: fixture,
				WuuHome: t.TempDir(), ProjectRoot: t.TempDir(), PrepareOnly: true, Timeout: 5 * time.Second,
				Env: map[string]string{"HOME": t.TempDir(), "FIXTURE_PROTOCOL": test.version, "FIXTURE_LIFECYCLE": test.lifecycle}})
			if test.diagnostic != "" {
				if err == nil || !strings.Contains(err.Error(), test.diagnostic) {
					if process != nil {
						_ = process.Close(context.Background())
					}
					t.Fatalf("negotiation diagnostic = %v, want %s", err, test.diagnostic)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			defer process.Close(context.Background())
			if err := process.Prepare(context.Background()); err != nil {
				t.Fatal(err)
			}
			if err := process.Activate(context.Background()); err != nil {
				t.Fatal(err)
			}
			if (test.version == "missing" || test.version == "0") && process.ProtocolVersion() != 1 {
				t.Fatal("omitted capability version no longer means protocol 1")
			}
		})
	}
}
