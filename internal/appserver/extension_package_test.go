package appserver

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/extensions"
	pluginpkg "github.com/blueberrycongee/wuu/internal/plugin"
)

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
	rt.Plugins = []pluginpkg.Plugin{{
		Manifest: pluginpkg.Manifest{
			ID:      "prompt-kit",
			Desktop: &pluginpkg.DesktopSpec{Entry: "dist/desktop.mjs"},
			Commands: []pluginpkg.ResolvedCommand{{
				CommandSpec: pluginpkg.CommandSpec{ID: "draft", Title: "Draft", Kind: pluginpkg.CommandKindPromptTemplate, Prompt: "Draft ${input}"},
				PublicID:    "prompt-kit.draft",
			}},
			Themes: []pluginpkg.ThemeSpec{{ID: "focused", Name: "Focused", Base: "dark", Tokens: map[string]string{"--wuu-paper": "#101214"}}},
			Settings: map[string]pluginpkg.SettingDefinition{
				"prompt-kit.enabled": {Type: pluginpkg.SettingTypeBoolean, Title: "Enabled", Default: true, Scope: pluginpkg.SettingScopeUser, Apply: pluginpkg.SettingApplyLive},
			},
			Slots:      []pluginpkg.SlotContributionSpec{{ID: "toolbar", Target: "composer.toolbar", Order: 4}},
			Surfaces:   []pluginpkg.SurfaceContributionSpec{{ID: "main", Target: "app.main", Mode: pluginpkg.ContributionModeWrap}},
			Presenters: []pluginpkg.PresenterContributionSpec{{ID: "message", Target: "conversation.item", Mode: pluginpkg.ContributionModeReplace, Priority: 8}},
			WorkspaceTools: []pluginpkg.ViewEntryContributionSpec{{
				ID: "inspector", View: "inspector", Title: "Inspector", Description: "Inspect workspace",
			}},
			SettingsPages: []pluginpkg.ViewEntryContributionSpec{{ID: "advanced", View: "advanced", Title: "Advanced"}},
		},
		Source: "project", SubjectID: "plugin:project:prompt-kit", Fingerprint: "sha256:prompt-kit",
		EffectivePermissions: []string{extensions.PermCommandsExecute},
	}}

	out := &lockedBuffer{}
	srv := New(rt, out)
	grant := `{"id":"grant","method":"extension/package/update","params":{"id":"plugin:project:prompt-kit","fingerprint":"sha256:prompt-kit","action":"grant"}}`
	if err := srv.handleLine(context.Background(), []byte(grant)); err != nil {
		t.Fatalf("grant package: %v", err)
	}
	messages := parseOutput(t, out.String())
	result := remarshal[ExtensionPackageUpdateResult](t, responseByID(t, messages, "grant")["result"])
	record := findExtensionRecord(t, result.ExtensionInventory, "plugin:project:prompt-kit")
	if record.ApprovalState != ExtensionApprovalGranted || record.RuntimeState != ExtensionRuntimeActive || record.Enabled == nil || !*record.Enabled {
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
	if len(record.Contributions.Slots) != 1 || record.Contributions.Slots[0].Target != "composer.toolbar" ||
		len(record.Contributions.Surfaces) != 1 || record.Contributions.Surfaces[0].Mode != "wrap" ||
		len(record.Contributions.Presenters) != 1 || record.Contributions.Presenters[0].Priority != 8 ||
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
	projectData, err := os.ReadFile(rt.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(projectData) != string(baseConfig) {
		t.Fatalf("user-owned grant mutated project config:\n%s", projectData)
	}

	disable := `{"id":"disable","method":"extension/package/update","params":{"id":"plugin:project:prompt-kit","fingerprint":"sha256:prompt-kit","action":"disable"}}`
	if err := srv.handleLine(context.Background(), []byte(disable)); err != nil {
		t.Fatalf("disable package: %v", err)
	}
	messages = parseOutput(t, out.String())
	result = remarshal[ExtensionPackageUpdateResult](t, responseByID(t, messages, "disable")["result"])
	record = findExtensionRecord(t, result.ExtensionInventory, "plugin:project:prompt-kit")
	if record.Enabled == nil || *record.Enabled || record.RuntimeState != ExtensionRuntimeInactive {
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
