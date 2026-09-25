package plugin

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestLoadManifestNormalizesSupportedFormats(t *testing.T) {
	tests := []struct {
		name       string
		path       string
		wantID     string
		wantSkills []string
		wantMCP    string
	}{
		{
			name:       "wuu",
			path:       filepath.Join("testdata", "wuu", "plugin.json"),
			wantID:     "wuu-demo",
			wantSkills: []string{"skills"},
			wantMCP:    "wuu-docs",
		},
		{
			name:       "codex camel case",
			path:       filepath.Join("testdata", "codex", ".codex-plugin", "plugin.json"),
			wantID:     "codex-demo",
			wantSkills: []string{"skills"},
			wantMCP:    "codex-docs",
		},
		{
			name:       "claude defaults and aliases",
			path:       filepath.Join("testdata", "claude", ".claude-plugin", "plugin.json"),
			wantID:     "claude-demo",
			wantSkills: nil,
			wantMCP:    "claude-docs",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := LoadManifest(tt.path, "project")
			if err != nil {
				t.Fatalf("LoadManifest: %v", err)
			}
			if got.ID != tt.wantID {
				t.Fatalf("ID = %q, want %q", got.ID, tt.wantID)
			}
			if !reflect.DeepEqual(got.Skills, tt.wantSkills) {
				t.Fatalf("Skills = %#v, want %#v", got.Skills, tt.wantSkills)
			}
			if got.MCPServers["docs"].Command != tt.wantMCP {
				t.Fatalf("MCPServers = %+v", got.MCPServers)
			}
			if got.ManifestPath != tt.path {
				t.Fatalf("ManifestPath = %q, want %q", got.ManifestPath, tt.path)
			}
		})
	}
}

func TestLoadManifestNormalizesExecutableRuntime(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, ManifestFilename)
	writeFile(t, path, `{
  "id": "runtime-demo",
  "runtime": {
    "protocol": "wuu-plugin-v1",
    "command": "bin/plugin",
    "args": ["--stdio"],
    "env": {"PLUGIN_MODE": "test"},
    "timeout": 12
  }
}`)
	plugin, err := LoadManifest(path, "project")
	if err != nil {
		t.Fatal(err)
	}
	if plugin.Runtime == nil {
		t.Fatal("runtime missing")
	}
	if plugin.Runtime.Command != filepath.Join(root, "bin", "plugin") || plugin.Runtime.Protocol != "wuu-plugin-v1" {
		t.Fatalf("runtime = %+v", plugin.Runtime)
	}
	if plugin.Runtime.Timeout != 12 || !reflect.DeepEqual(plugin.Runtime.Args, []string{"--stdio"}) || plugin.Runtime.Env["PLUGIN_MODE"] != "test" {
		t.Fatalf("runtime = %+v", plugin.Runtime)
	}
}

func TestLoadManifestRejectsInvalidRuntime(t *testing.T) {
	for _, test := range []struct {
		name    string
		runtime string
	}{
		{name: "unknown protocol", runtime: `{"protocol":"other","command":"node"}`},
		{name: "missing command", runtime: `{"protocol":"wuu-plugin-v1"}`},
		{name: "escaping command", runtime: `{"protocol":"wuu-plugin-v1","command":"../plugin"}`},
		{name: "invalid env", runtime: `{"protocol":"wuu-plugin-v1","command":"node","env":{"BAD=NAME":"x"}}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), ManifestFilename)
			writeFile(t, path, `{"id":"bad","runtime":`+test.runtime+`}`)
			if _, err := LoadManifest(path, "project"); err == nil {
				t.Fatal("expected runtime validation error")
			}
		})
	}
}

func TestLoadManifestReportsUnsupportedFields(t *testing.T) {
	got, err := LoadManifest(filepath.Join("testdata", "codex", ".codex-plugin", "plugin.json"), "project")
	if err != nil {
		t.Fatalf("LoadManifest: %v", err)
	}
	want := []string{"bundledContentVariant", "commands"}
	if !reflect.DeepEqual(got.UnsupportedFields, want) {
		t.Fatalf("UnsupportedFields = %#v, want %#v", got.UnsupportedFields, want)
	}
}

func TestLoadManifestRejectsPathsOutsidePluginRoot(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".codex-plugin", "plugin.json")
	writeFile(t, path, `{"name":"escape","skills":"../skills"}`)

	_, err := LoadManifest(path, "project")
	if err == nil || !strings.Contains(err.Error(), "plugin root") {
		t.Fatalf("LoadManifest error = %v, want plugin root rejection", err)
	}
}

func TestLoadManifestRejectsCommunityOfficialNativeHelper(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "plugin.json")
	writeFile(t, path, `{"id":"native","official_native_helper":{"path":"helper"}}`)

	if _, err := LoadManifest(path, "user"); err == nil || !strings.Contains(err.Error(), "official_native_helper") {
		t.Fatalf("LoadManifest error = %v, want official helper rejection", err)
	}

	got, err := LoadManifestWithOptions(path, LoadOptions{Source: "bundled", Official: true})
	if err != nil {
		t.Fatalf("official LoadManifestWithOptions: %v", err)
	}
	if !got.Official || len(got.OfficialNativeHelper) == 0 {
		t.Fatalf("official provenance/helper not preserved: %+v", got)
	}
}

func TestLoadManifestNormalizesDesktopThemeAndSettingsContributions(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "dist", "desktop.mjs"), "export function activate() {}")
	path := filepath.Join(root, ManifestFilename)
	writeFile(t, path, `{
  "schemaVersion": 1,
  "id": "productivity",
  "desktop": {"entry": "dist/desktop.mjs"},
  "contributes": {
    "themes": [{
      "id": "focused",
      "name": "Focused",
      "base": "dark",
      "tokens": {"--wuu-paper": "#101214", "--wuu-ink": "#f4f5f6", "--wuu-font-family-ui": "system-ui"},
      "syntax": {"--hljs-keyword": "#ff8bd1", "--wuu-syntax-string": "#86efac"}
    }],
    "settings": {
      "enabled": {"type":"boolean","title":"Enabled","default":true,"scope":"user","apply":"live"},
      "mode": {"type":"enum","title":"Mode","default":"quiet","enum":["quiet","active"],"scope":"workspace","apply":"restart"},
      "label": {"type":"string","title":"Label","default":"work","scope":"user","apply":"live"},
      "limit": {"type":"number","title":"Limit","default":3,"scope":"workspace","apply":"live"}
    }
  }
}`)

	got, err := LoadManifest(path, "user")
	if err != nil {
		t.Fatal(err)
	}
	if got.SchemaVersion != 1 || got.Desktop == nil || got.Desktop.Entry != "dist/desktop.mjs" {
		t.Fatalf("manifest contribution metadata = %+v", got.Manifest)
	}
	if len(got.Themes) != 1 || got.Themes[0].Tokens["--wuu-paper"] != "#101214" {
		t.Fatalf("themes = %+v", got.Themes)
	}
	if got.Themes[0].Tokens["--wuu-font-family-ui"] != "system-ui" || got.Themes[0].Syntax["--wuu-syntax-string"] != "#86efac" {
		t.Fatalf("modern theme contract = %+v", got.Themes[0])
	}
	if len(got.Settings) != 4 || got.Settings["productivity.mode"].Scope != SettingScopeWorkspace {
		t.Fatalf("settings = %+v", got.Settings)
	}
}

func TestLoadManifestRejectsAmbiguousContributionJSON(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{name: "duplicate top-level field", body: `{"id":"one","id":"two"}`, want: "duplicate"},
		{name: "both schema aliases", body: `{"schemaVersion":1,"schema_version":1,"id":"demo"}`, want: "both schemaVersion"},
		{name: "duplicate contribution field", body: `{"id":"demo","contributes":{"themes":[],"themes":[]}}`, want: "duplicate"},
		{name: "unknown contribution", body: `{"id":"demo","contributes":{"styles":[]}}`, want: "unknown field"},
		{name: "trailing value", body: `{"id":"demo"} {"id":"other"}`, want: "invalid character"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), ManifestFilename)
			writeFile(t, path, test.body)
			if _, err := LoadManifest(path, "user"); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("LoadManifest error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestLoadManifestNormalizesDeclarativeUIContributions(t *testing.T) {
	path := filepath.Join(t.TempDir(), ManifestFilename)
	writeFile(t, path, `{
  "id":"ui-demo",
  "contributes":{
    "slots":[{"id":"toolbar","target":"composer.toolbar","order":4,"title":"Toolbar"}],
    "surfaces":[{"id":"timeline-frame","target":"conversation.timeline","mode":"wrap","order":2}],
    "presenters":[{"id":"message","target":"conversation.item","mode":"replace","priority":8}],
    "navigation":[{"id":"dashboard-nav","view":"dashboard","title":"Dashboard","order":3}],
    "workspaceTools":[{"id":"inspector-tool","view":"inspector","title":"Inspector","description":"Workspace inspector"}],
    "settingsPages":[{"id":"advanced-page","view":"advanced","title":"Advanced","icon":"sliders"}]
  }
}`)
	manifest, err := LoadManifest(path, "user")
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest.Slots) != 1 || manifest.Slots[0].Target != "composer.toolbar" || manifest.Slots[0].Order != 4 {
		t.Fatalf("slots = %+v", manifest.Slots)
	}
	if len(manifest.Surfaces) != 1 || manifest.Surfaces[0].Mode != ContributionModeWrap {
		t.Fatalf("surfaces = %+v", manifest.Surfaces)
	}
	if len(manifest.Presenters) != 1 || manifest.Presenters[0].Priority != 8 {
		t.Fatalf("presenters = %+v", manifest.Presenters)
	}
	if len(manifest.Navigation) != 1 || manifest.Navigation[0].View != "dashboard" || manifest.Navigation[0].Order != 3 {
		t.Fatalf("navigation = %+v", manifest.Navigation)
	}
	if len(manifest.WorkspaceTools) != 1 || manifest.WorkspaceTools[0].Description != "Workspace inspector" {
		t.Fatalf("workspace tools = %+v", manifest.WorkspaceTools)
	}
	if len(manifest.SettingsPages) != 1 || manifest.SettingsPages[0].Icon == nil || manifest.SettingsPages[0].Icon.Name != "sliders" {
		t.Fatalf("settings pages = %+v", manifest.SettingsPages)
	}
}

func TestLoadManifestRejectsInvalidDeclarativeUIContributions(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{name: "unknown slot target", body: `{"id":"demo","contributes":{"slots":[{"id":"one","target":"unknown"}]}}`, want: "unknown target"},
		{name: "invalid surface mode", body: `{"id":"demo","contributes":{"surfaces":[{"id":"one","target":"conversation.timeline","mode":"append"}]}}`, want: "replace or wrap"},
		{name: "unknown presenter target", body: `{"id":"demo","contributes":{"presenters":[{"id":"one","target":"custom","mode":"wrap"}]}}`, want: "unknown target"},
		{name: "duplicate id across kinds", body: `{"id":"demo","contributes":{"slots":[{"id":"same","target":"composer.above"}],"surfaces":[{"id":"same","target":"conversation.timeline","mode":"wrap"}]}}`, want: "duplicate plugin-local id"},
		{name: "unknown item field", body: `{"id":"demo","contributes":{"slots":[{"id":"one","target":"composer.above","priority":1}]}}`, want: "unknown field"},
		{name: "missing View entry title", body: `{"id":"demo","contributes":{"navigation":[{"id":"one","view":"dashboard"}]}}`, want: "requires title"},
		{name: "duplicate entry id across locations", body: `{"id":"demo","contributes":{"navigation":[{"id":"same","view":"one","title":"One"}],"workspaceTools":[{"id":"same","view":"two","title":"Two"}]}}`, want: "duplicate plugin-local id"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), ManifestFilename)
			writeFile(t, path, test.body)
			if _, err := LoadManifest(path, "user"); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("LoadManifest error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestLoadManifestRejectsUnsafeDesktopAndThemeContributions(t *testing.T) {
	t.Run("desktop symlink", func(t *testing.T) {
		root := t.TempDir()
		target := filepath.Join(root, "target.mjs")
		writeFile(t, target, "export {}")
		link := filepath.Join(root, "desktop.mjs")
		if err := os.Symlink(target, link); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(root, ManifestFilename)
		writeFile(t, path, `{"id":"demo","desktop":{"entry":"desktop.mjs"}}`)
		if _, err := LoadManifest(path, "user"); err == nil || !strings.Contains(err.Error(), "symbolic link") {
			t.Fatalf("LoadManifest error = %v, want symlink rejection", err)
		}
	})

	t.Run("unpublished theme token", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), ManifestFilename)
		writeFile(t, path, `{"id":"demo","contributes":{"themes":[{"id":"bad","name":"Bad","base":"dark","tokens":{"--arbitrary-global":"red"}}]}}`)
		if _, err := LoadManifest(path, "user"); err == nil || !strings.Contains(err.Error(), "unsupported semantic token") {
			t.Fatalf("LoadManifest error = %v, want token rejection", err)
		}
	})
}

func TestDeepUIExampleManifestLoads(t *testing.T) {
	if _, err := LoadManifest(filepath.Join("..", "..", "examples", "plugins", "deep-ui", ManifestFilename), "user"); err != nil {
		t.Fatal(err)
	}
}

func TestMangaStudioExampleManifestLoads(t *testing.T) {
	if _, err := LoadManifest(filepath.Join("..", "..", "examples", "plugins", "manga-studio", ManifestFilename), "user"); err != nil {
		t.Fatal(err)
	}
}
