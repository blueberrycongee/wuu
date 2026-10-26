package appserver

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/extensions"
	pluginpkg "github.com/blueberrycongee/wuu/internal/plugin"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/session"
)

func TestExtensionPackageBatchProcess(t *testing.T) {
	role := os.Getenv("WUU_BATCH_POLICY_ROLE")
	if role == "" {
		return
	}
	scanner, encoder := bufio.NewScanner(os.Stdin), json.NewEncoder(os.Stdout)
	for scanner.Scan() {
		var req struct{ ID, Method string }
		if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
			os.Exit(2)
		}
		var result any = map[string]any{}
		switch req.Method {
		case "initialize":
			definition := pluginhost.CapabilityInitializeResult{
				ProtocolVersion: pluginhost.CapabilityProtocolVersion, LifecycleVersion: pluginhost.RuntimeLifecycleVersion,
				InitializeResult: pluginhost.InitializeResult{Tools: []pluginhost.ToolRegistration{{ID: "probe", Description: "Probe selected fixture", InputSchema: map[string]any{"type": "object"}}}},
			}
			if role == "provider" {
				definition.ProvidedServices = []pluginhost.ServiceDescriptor{{Name: "fixture.scheduler", Version: "1.0.0", Methods: []pluginhost.ServiceMethodDescriptor{{Name: "probe", InputSchema: "fixture.probe.input.v1", OutputSchema: "fixture.probe.output.v1"}}}}
			} else if role == "consumer" {
				definition.RequiredServices = []pluginhost.ServiceRequirement{{Name: "fixture.scheduler", MajorVersion: 1, Required: true}}
			}
			result = definition
		case "tool.execute":
			result = map[string]any{"result": map[string]any{"content": []map[string]string{{"type": "text", "text": role}}}}
		case "activate":
			if err := os.WriteFile(os.Getenv("WUU_BATCH_POLICY_SHUTDOWN")+".active", []byte(fmt.Sprint(os.Getpid())), 0600); err != nil {
				os.Exit(2)
			}
		case "shutdown":
			if err := os.WriteFile(os.Getenv("WUU_BATCH_POLICY_SHUTDOWN"), []byte(fmt.Sprint(os.Getpid())), 0600); err != nil {
				os.Exit(2)
			}
		}
		if err := encoder.Encode(map[string]any{"id": req.ID, "result": result}); err != nil {
			os.Exit(2)
		}
		if req.Method == "shutdown" {
			os.Exit(0)
		}
	}
	os.Exit(0)
}

// Failure cases: a selection publishes an invalid intermediate graph, a later
// invalid entry partly writes policy, or disabled providers survive a retained
// generation. The real process contract keeps the graph checks in this test.
func TestExtensionPackageBatchPublishesDependencyCompletePolicy(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	rt.WuuHome = retryingTempDir(t)
	preparePluginRuntimePathsForTest(t, rt)
	cfg, _, err := config.LoadPath(rt.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Extensions = &extensions.Settings{}
	cfg.Extensions.SetEnabled("plugin:user:unrelated", false)
	control := t.TempDir()
	items := make(map[string]pluginpkg.Plugin)
	for _, role := range []string{"provider", "consumer", "independent"} {
		manifest := pluginpkg.Manifest{SchemaVersion: 1, ID: "batch-" + role, Runtime: &pluginpkg.RuntimeSpec{
			Protocol: pluginhost.ProtocolName, Command: os.Args[0], Args: []string{"-test.run=^TestExtensionPackageBatchProcess$"},
			Env: map[string]string{"WUU_BATCH_POLICY_ROLE": role, "WUU_BATCH_POLICY_SHUTDOWN": filepath.Join(control, role)},
		}}
		if role == "consumer" {
			manifest.Desktop = &pluginpkg.DesktopSpec{Entry: "desktop.js", RequiredServices: []pluginhost.ServiceRequirement{{Name: "fixture.scheduler", MajorVersion: 1, Required: true}}}
		}
		raw, err := json.Marshal(manifest)
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(rt.WuuHome, "plugins", manifest.ID, "plugin.json")
		writePluginPackageFile(t, path, string(raw))
		if role == "consumer" {
			writePluginPackageFile(t, filepath.Join(filepath.Dir(path), "desktop.js"), "export function activate() {}")
		}
		item, err := pluginpkg.LoadManifest(path, "user")
		if err != nil {
			t.Fatal(err)
		}
		items[role] = item
		if err := cfg.Extensions.RecordGrant(extensions.Grant{SubjectID: item.SubjectID, Fingerprint: item.Fingerprint, Permissions: item.EffectivePermissions}); err != nil {
			t.Fatal(err)
		}
	}
	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	writePluginPackageFile(t, rt.ConfigPath, string(raw))
	rt = newConfigRefreshRuntime(t, rt)
	raw, err = os.ReadFile(rt.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	out := &lockedBuffer{}
	srv := New(rt, out)
	t.Cleanup(srv.Close)
	oldHost := rt.PluginHost
	pinned := rt.RetainPluginGeneration()
	defer rt.ReleasePluginGeneration(pinned)
	activePIDs := make(map[string]string)
	for _, role := range []string{"provider", "consumer"} {
		pid, err := os.ReadFile(filepath.Join(control, role+".active"))
		if err != nil {
			t.Fatal(err)
		}
		activePIDs[role] = string(pid)
	}
	call := func(id string, params any) map[string]any {
		t.Helper()
		callPluginPackageRPC(t, srv, id, MethodExtensionPackageUpdate, params)
		return responseByID(t, parseOutput(t, out.String()), id)
	}
	change := func(role, action string) map[string]any {
		item := items[role]
		return map[string]any{"id": item.SubjectID, "fingerprint": item.Fingerprint, "action": action}
	}
	assertUnchanged := func() {
		t.Helper()
		current, err := os.ReadFile(rt.ConfigPath)
		if err != nil || string(current) != string(raw) || rt.PluginHost != oldHost {
			t.Fatalf("rejected choice changed config or generation: %s, %v", current, err)
		}
	}
	response := call("single-provider", change("provider", "disable"))
	if response["error"] == nil || !strings.Contains(fmt.Sprint(response["error"]), "fixture.scheduler") {
		t.Fatalf("single provider disable did not reject incomplete graph: %+v", response)
	}
	assertUnchanged()
	for _, test := range []struct {
		name   string
		params any
	}{
		{"empty", map[string]any{"updates": []any{}}},
		{"null", map[string]any{"updates": nil}},
		{"mixed", map[string]any{"id": items["provider"].SubjectID, "action": "disable", "updates": []any{change("consumer", "disable")}}},
		{"mixed-empty-field", map[string]any{"id": "", "updates": []any{change("consumer", "disable")}}},
		{"missing-id", map[string]any{"updates": []any{change("provider", "disable"), map[string]any{"action": "disable"}}}},
		{"unknown", map[string]any{"updates": []any{change("provider", "disable"), map[string]any{"id": "plugin:user:missing", "action": "disable"}}}},
		{"stale", map[string]any{"updates": []any{change("provider", "disable"), map[string]any{"id": items["consumer"].SubjectID, "fingerprint": "stale", "action": "disable"}}}},
		{"duplicate", map[string]any{"updates": []any{change("provider", "disable"), map[string]any{"id": " " + items["provider"].SubjectID + " ", "action": "disable"}}}},
		{"trust-action", map[string]any{"updates": []any{change("provider", "disable"), change("consumer", "grant")}}},
		{"incomplete", map[string]any{"updates": []any{change("provider", "disable")}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if response := call(test.name, test.params); response["error"] == nil {
				t.Fatalf("invalid batch succeeded: %+v", response)
			}
			assertUnchanged()
		})
	}
	initialEpoch, err := session.ReadPluginGenerationEpoch(rt.WuuHome)
	if err != nil {
		t.Fatal(err)
	}
	response = call("disable-together", map[string]any{"updates": []any{change("provider", "disable"), change("consumer", "disable")}})
	if response["error"] != nil {
		t.Fatalf("complete selection failed: %+v", response)
	}
	result := remarshal[ExtensionPackageUpdateResult](t, response["result"])
	for _, role := range []string{"provider", "consumer"} {
		record := findExtensionRecord(t, result.ExtensionInventory, items[role].SubjectID)
		if record.Enabled == nil || *record.Enabled || record.EnabledOverride == nil || *record.EnabledOverride || record.RuntimeState != ExtensionRuntimeInactive {
			t.Fatalf("disabled %s: %+v", role, record)
		}
		if data, err := os.ReadFile(filepath.Join(control, role)); err != nil || string(data) != activePIDs[role] {
			t.Fatalf("retained %s process did not stop: %s, %v", role, data, err)
		}
	}
	if oldHost.ServiceRegistry().HasProvider("fixture.scheduler", 1) || rt.PluginHost.ServiceRegistry().HasProvider("fixture.scheduler", 1) {
		t.Fatal("disabled scheduler remained available")
	}
	if independent := findExtensionRecord(t, result.ExtensionInventory, items["independent"].SubjectID); independent.RuntimeState != ExtensionRuntimeActive {
		t.Fatalf("unrelated plugin stopped: %+v", independent)
	}
	epoch, err := session.ReadPluginGenerationEpoch(rt.WuuHome)
	if err != nil || epoch != initialEpoch+1 || strings.Count(out.String(), NotificationPluginInventoryChanged) != 1 {
		t.Fatalf("batch did not publish once: epoch=%d, %v", epoch, err)
	}
	disabledConfig, err := os.ReadFile(rt.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	disabledHost := rt.PluginHost
	for index, params := range []any{map[string]any{"updates": []any{change("consumer", "enable")}}, change("consumer", "enable")} {
		response = call(fmt.Sprintf("enable-without-provider-%d", index), params)
		if response["error"] == nil || !strings.Contains(fmt.Sprint(response["error"]), "fixture.scheduler") {
			t.Fatalf("explicit enable without its provider did not fail preparation: %+v", response)
		}
		current, err := os.ReadFile(rt.ConfigPath)
		if err != nil || string(current) != string(disabledConfig) || rt.PluginHost != disabledHost {
			t.Fatalf("failed enable changed config or generation: %s, %v", current, err)
		}
	}
	response = call("enable-together", map[string]any{"updates": []any{change("consumer", "enable"), change("provider", "enable")}})
	if response["error"] != nil {
		t.Fatalf("complete enable selection failed: %+v", response)
	}
	result = remarshal[ExtensionPackageUpdateResult](t, response["result"])
	for _, role := range []string{"provider", "consumer", "independent"} {
		if record := findExtensionRecord(t, result.ExtensionInventory, items[role].SubjectID); record.RuntimeState != ExtensionRuntimeActive {
			t.Fatalf("enabled %s: %+v", role, record)
		}
	}
	persisted, _, err := config.LoadPath(rt.ConfigPath)
	if err != nil || persisted.Extensions == nil || !reflect.DeepEqual(persisted.Extensions.Grants, cfg.Extensions.Grants) || persisted.Extensions.IsEnabled("plugin:user:unrelated", true) {
		t.Fatalf("batch changed unrelated policy or grants: %+v, %v", persisted.Extensions, err)
	}
}

// Package planning can exclude a consumer before it has a runtime status.
// Explicit enable must reject that selection without publishing its policy.
func TestExtensionPackageExplicitEnableRejectsUnavailablePackageDependency(t *testing.T) {
	for _, versioned := range []bool{false, true} {
		t.Run(fmt.Sprintf("versioned-%t", versioned), func(t *testing.T) {
			rt := newTestRuntime(t, &fakeClient{})
			rt.WuuHome = retryingTempDir(t)
			preparePluginRuntimePathsForTest(t, rt)
			cfg, _, err := config.LoadPath(rt.ConfigPath)
			if err != nil {
				t.Fatal(err)
			}
			cfg.Extensions = &extensions.Settings{}
			consumer := pluginpkg.Manifest{SchemaVersion: 1, ID: "package-consumer", Requires: []string{"package-provider"}}
			if versioned {
				consumer.Requires = nil
				consumer.Dependencies = []pluginpkg.Dependency{{ID: "package-provider", Version: ">=2.0.0"}}
			}
			var consumerPackage, providerPackage pluginpkg.Plugin
			for _, manifest := range []pluginpkg.Manifest{{SchemaVersion: 1, ID: "package-provider", Version: "1.0.0"}, consumer} {
				raw, err := json.Marshal(manifest)
				if err != nil {
					t.Fatal(err)
				}
				path := filepath.Join(rt.WuuHome, "plugins", manifest.ID, "plugin.json")
				writePluginPackageFile(t, path, string(raw))
				item, err := pluginpkg.LoadManifest(path, "user")
				if err != nil {
					t.Fatal(err)
				}
				if err := cfg.Extensions.RecordGrant(extensions.Grant{SubjectID: item.SubjectID, Fingerprint: item.Fingerprint, Permissions: item.EffectivePermissions}); err != nil {
					t.Fatal(err)
				}
				cfg.Extensions.SetEnabled(item.SubjectID, versioned && manifest.ID == "package-provider")
				if manifest.ID == consumer.ID {
					consumerPackage = item
				} else {
					providerPackage = item
				}
			}
			raw, err := json.Marshal(cfg)
			if err != nil {
				t.Fatal(err)
			}
			writePluginPackageFile(t, rt.ConfigPath, string(raw))
			rt = newConfigRefreshRuntime(t, rt)
			raw, err = os.ReadFile(rt.ConfigPath)
			if err != nil {
				t.Fatal(err)
			}
			out := &lockedBuffer{}
			srv := New(rt, out)
			t.Cleanup(srv.Close)
			oldHost := rt.PluginHost
			enable := map[string]any{"id": consumerPackage.SubjectID, "fingerprint": consumerPackage.Fingerprint, "action": "enable"}
			for index, params := range []any{map[string]any{"updates": []any{enable}}, enable} {
				id := fmt.Sprintf("enable-consumer-%d", index)
				callPluginPackageRPC(t, srv, id, MethodExtensionPackageUpdate, params)
				response := responseByID(t, parseOutput(t, out.String()), id)
				if response["error"] == nil || !strings.Contains(fmt.Sprint(response["error"]), providerPackage.ID) {
					t.Fatalf("explicit enable accepted unavailable package dependency: error=%v", response["error"])
				}
				current, err := os.ReadFile(rt.ConfigPath)
				if err != nil || string(current) != string(raw) || rt.PluginHost != oldHost {
					t.Fatalf("failed enable changed config or generation: %v", err)
				}
			}
			if !versioned {
				callPluginPackageRPC(t, srv, "enable-both", MethodExtensionPackageUpdate, map[string]any{"updates": []any{enable, map[string]any{"id": providerPackage.SubjectID, "action": "enable"}}})
				if response := responseByID(t, parseOutput(t, out.String()), "enable-both"); response["error"] != nil {
					t.Fatalf("complete package dependency selection failed: %v", response["error"])
				}
				active := false
				for _, item := range rt.ActivePlugins {
					active = active || item.ID == consumer.ID
				}
				if !active {
					t.Fatal("complete selection did not activate the consumer")
				}
			}
		})
	}
}

func TestExtensionPackageGrantEnablesDefaultDisabledPackage(t *testing.T) {
	disabled := false
	plugin := pluginpkg.Plugin{
		Manifest:             pluginpkg.Manifest{ID: "experimental", DefaultEnabled: &disabled},
		SubjectID:            "plugin:bundled:experimental",
		Fingerprint:          "sha256:experimental",
		EffectivePermissions: []string{extensions.PermProcessSpawn},
	}
	settings := &extensions.Settings{}
	if err := applyExtensionPackageAction(settings, ExtensionPackageGrant, plugin, time.Now().UTC()); err != nil {
		t.Fatalf("grant default-disabled package: %v", err)
	}
	if !settings.IsEnabled(plugin.SubjectID, plugin.EnabledByDefault()) {
		t.Fatalf("grant did not enable default-disabled package: %+v", settings)
	}
	if _, ok := settings.FindGrant(plugin.SubjectID, plugin.Fingerprint); !ok {
		t.Fatalf("grant was not recorded: %+v", settings)
	}
}

func TestExtensionPackageUpdateGrantsExactFingerprintAndDisablesImmediately(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	home := retryingTempDir(t)
	wuuHome := filepath.Join(home, ".wuu")
	t.Setenv("WUU_HOME", wuuHome)
	rt.HomeDir = home
	rt.WuuHome = wuuHome
	baseConfig := []byte(`{
  "default_provider": "fake-provider",
  "providers": {"fake-provider": {"type": "openai-compatible", "base_url": "https://example.test/v1", "model": "fake-model"}}
}
`)
	if err := os.MkdirAll(wuuHome, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wuuHome, "config.json"), baseConfig, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(rt.ConfigPath, baseConfig, 0o600); err != nil {
		t.Fatal(err)
	}
	packageRoot := filepath.Join(rt.RootDir, ".wuu", "plugins", "prompt-kit")
	manifestPath := filepath.Join(packageRoot, "plugin.json")
	writePluginPackageFile(t, filepath.Join(packageRoot, "dist", "desktop.mjs"), "export function activate() {}")
	writePluginPackageFile(t, filepath.Join(packageRoot, "draft.md"), "Draft ${input}")
	writePluginPackageFile(t, manifestPath, `{
  "schemaVersion": 1, "id": "prompt-kit", "desktop": {"entry": "dist/desktop.mjs"},
  "contributes": {
    "commands": [{"id": "draft", "title": "Draft", "kind": "prompt_template", "prompt": "draft.md"}],
    "themes": [{"id": "focused", "name": "Focused", "base": "dark", "tokens": {"--wuu-paper": "#101214"}}],
    "settings": {"enabled": {"type": "boolean", "title": "Enabled", "default": true, "scope": "user", "apply": "live"}},
    "presenters": [{"id": "message", "target": "conversation.item", "mode": "replace", "priority": 8}],
    "navigation": [{"id": "dashboard", "view": "dashboard", "title": "Dashboard"}],
    "workspaceTools": [{"id": "inspector", "view": "inspector", "title": "Inspector", "description": "Inspect workspace"}],
    "settingsPages": [{"id": "advanced", "view": "advanced", "title": "Advanced"}]
  }
}`)
	item, err := pluginpkg.LoadManifest(manifestPath, "project")
	if err != nil {
		t.Fatal(err)
	}
	rt.Plugins = []pluginpkg.Plugin{item}

	preparePluginRuntimePathsForTest(t, rt)
	projectConfigPath := rt.ConfigPath
	rt.ConfigPath = filepath.Join(wuuHome, "config.json")
	rt = newConfigRefreshRuntime(t, rt)
	out := &lockedBuffer{}
	srv := New(rt, out)
	t.Cleanup(srv.Close)
	grant := fmt.Sprintf(`{"id":"grant","method":"extension/package/update","params":{"id":"plugin:project:prompt-kit","fingerprint":%q,"action":"grant"}}`, item.Fingerprint)
	if err := srv.handleLine(context.Background(), []byte(grant)); err != nil {
		t.Fatalf("grant package: %v", err)
	}
	messages := parseOutput(t, out.String())
	response := responseByID(t, messages, "grant")
	if response["error"] != nil {
		t.Fatalf("grant package response: %+v", response)
	}
	result := remarshal[ExtensionPackageUpdateResult](t, response["result"])
	record := findExtensionRecord(t, result.ExtensionInventory, "plugin:project:prompt-kit")
	if record.ApprovalState != ExtensionApprovalGranted || record.RuntimeState != ExtensionRuntimeActive || record.Enabled == nil || !*record.Enabled || record.EnabledOverride == nil || !*record.EnabledOverride {
		t.Fatalf("granted package record = %+v", record)
	}
	if len(record.Contributions.Commands) != 1 || record.Contributions.Commands[0].ID != "draft" || record.Contributions.Commands[0].Template != "Draft ${input}" {
		t.Fatalf("command contributions = %+v", record.Contributions)
	}
	if record.Desktop == nil || record.Desktop.Entry != "dist/desktop.mjs" {
		t.Fatalf("desktop contribution = %+v", record.Desktop)
	}
	if len(record.Contributions.Themes) != 1 || record.Contributions.Themes[0].Tokens["--wuu-paper"] != "#101214" {
		t.Fatalf("theme contributions = %+v", record.Contributions)
	}
	if len(record.Contributions.Settings) != 1 || record.Contributions.Settings[0].ID != "prompt-kit.enabled" || record.Contributions.Settings[0].Default != true {
		t.Fatalf("setting contributions = %+v", record.Contributions)
	}
	if len(record.Contributions.Presenters) != 1 || record.Contributions.Presenters[0].Priority != 8 ||
		len(record.Contributions.Navigation) != 1 || record.Contributions.Navigation[0].View != "dashboard" ||
		len(record.Contributions.WorkspaceTools) != 1 || record.Contributions.WorkspaceTools[0].Description != "Inspect workspace" ||
		len(record.Contributions.SettingsPages) != 1 || record.Contributions.SettingsPages[0].View != "advanced" {
		t.Fatalf("UI contributions = %+v", record.Contributions)
	}
	data, err := os.ReadFile(filepath.Join(wuuHome, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	var persisted map[string]any
	if err := json.Unmarshal(data, &persisted); err != nil {
		t.Fatal(err)
	}
	if persisted["extensions"] == nil {
		t.Fatalf("grant was not persisted in user-owned config: %s", data)
	}
	projectData, err := os.ReadFile(projectConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(projectData) != string(baseConfig) {
		t.Fatalf("user-owned grant mutated project config:\n%s", projectData)
	}

	disable := fmt.Sprintf(`{"id":"disable","method":"extension/package/update","params":{"id":"plugin:project:prompt-kit","fingerprint":%q,"action":"disable"}}`, item.Fingerprint)
	if err := srv.handleLine(context.Background(), []byte(disable)); err != nil {
		t.Fatalf("disable package: %v", err)
	}
	messages = parseOutput(t, out.String())
	result = remarshal[ExtensionPackageUpdateResult](t, responseByID(t, messages, "disable")["result"])
	record = findExtensionRecord(t, result.ExtensionInventory, "plugin:project:prompt-kit")
	if record.Enabled == nil || *record.Enabled || record.RuntimeState != ExtensionRuntimeInactive || record.EnabledOverride == nil || *record.EnabledOverride {
		t.Fatalf("disabled package record = %+v", record)
	}
}

func TestExtensionPackageUpdateRejectsStaleFingerprint(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	rt.Plugins = []pluginpkg.Plugin{{
		Manifest: pluginpkg.Manifest{ID: "changed"}, Source: "project",
		SubjectID: "plugin:project:changed", Fingerprint: "sha256:new",
	}}
	out := &lockedBuffer{}
	srv := New(rt, out)
	req := `{"id":"stale","method":"extension/package/update","params":{"id":"plugin:project:changed","fingerprint":"sha256:old","action":"grant"}}`
	if err := srv.handleLine(context.Background(), []byte(req)); err != nil {
		t.Fatalf("stale update dispatch: %v", err)
	}
	response := responseByID(t, parseOutput(t, out.String()), "stale")
	if response["error"] == nil {
		t.Fatalf("stale fingerprint unexpectedly succeeded: %+v", response)
	}
}

func TestExtensionCatalogRefreshRediscoversPackages(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	root := t.TempDir()
	rt.RootDir = root
	rt.WuuHome = filepath.Join(root, "wuu-home")
	manifestPath := filepath.Join(root, ".wuu", "plugins", "fresh", pluginpkg.ManifestFilename)
	if err := os.MkdirAll(filepath.Dir(manifestPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifestPath, []byte(`{"id":"fresh","name":"Fresh plugin"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	preparePluginRuntimePathsForTest(t, rt)
	rt = newConfigRefreshRuntime(t, rt)
	bindConfigSourceForTest(t, rt)
	out := &lockedBuffer{}
	srv := New(rt, out)
	defer srv.Close()
	if err := srv.handleLine(context.Background(), []byte(`{"id":"refresh","method":"extension/catalog/refresh"}`)); err != nil {
		t.Fatalf("refresh extension catalog: %v", err)
	}
	result := remarshal[ExtensionCatalogRefreshResult](t, responseByID(t, parseOutput(t, out.String()), "refresh")["result"])
	record := findPluginExtensionRecord(t, result.ExtensionInventory, "fresh")
	if record.Name != "Fresh plugin" || record.ApprovalState != ExtensionApprovalPending {
		t.Fatalf("refreshed package record = %+v", record)
	}
}

// People see a plugin by the name and words its manifest gives them; the id
// is only the fallback when a manifest names nothing.
func TestExtensionInventoryUsesManifestPresentation(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	rt.Plugins = []pluginpkg.Plugin{
		{
			Manifest: pluginpkg.Manifest{
				ID: "cua-mac", Name: "cua-mac", Description: "Control macOS apps through Accessibility.",
				Interface: json.RawMessage(`{"displayName":"Computer Use for Mac","shortDescription":"Operate Mac apps with native input.","longDescription":"Uses Accessibility for semantic control and native input as a fallback.","developerName":"Wuu"}`),
			},
			Source: "bundled", Official: true, SubjectID: "plugin:bundled:cua-mac", Fingerprint: "sha256:cua",
		},
		{
			Manifest: pluginpkg.Manifest{ID: "todo", Name: "TODO", Description: "Visible task tracking."},
			Source:   "bundled", Official: true, SubjectID: "plugin:bundled:todo", Fingerprint: "sha256:todo",
		},
		{
			Manifest: pluginpkg.Manifest{ID: "bare"},
			Source:   "user", SubjectID: "plugin:user:bare", Fingerprint: "sha256:bare",
		},
	}
	srv := New(rt, &lockedBuffer{})
	inventory := srv.currentExtensionInventory()

	cua := findPluginExtensionRecord(t, inventory, "cua-mac")
	if cua.Name != "Computer Use for Mac" || cua.Description != "Operate Mac apps with native input." ||
		cua.LongDescription != "Uses Accessibility for semantic control and native input as a fallback." || cua.Developer != "Wuu" {
		t.Fatalf("interface presentation not used: %+v", cua)
	}
	if todo := findPluginExtensionRecord(t, inventory, "todo"); todo.Name != "TODO" || todo.Description != "Visible task tracking." || todo.LongDescription != "" {
		t.Fatalf("manifest name and description not used: %+v", todo)
	}
	if bare := findPluginExtensionRecord(t, inventory, "bare"); bare.Name != "bare" || bare.Description != "" {
		t.Fatalf("an unnamed plugin lost its id: %+v", bare)
	}
}

func findPluginExtensionRecord(t *testing.T, records []ExtensionInventoryRecord, pluginID string) ExtensionInventoryRecord {
	t.Helper()
	for _, record := range records {
		if record.Provenance.PluginID == pluginID {
			return record
		}
	}
	t.Fatalf("plugin extension record %q not found in %+v", pluginID, records)
	return ExtensionInventoryRecord{}
}

func findExtensionRecord(t *testing.T, records []ExtensionInventoryRecord, id string) ExtensionInventoryRecord {
	t.Helper()
	for _, record := range records {
		if record.ID == id {
			return record
		}
	}
	t.Fatalf("extension record %q not found in %+v", id, records)
	return ExtensionInventoryRecord{}
}
