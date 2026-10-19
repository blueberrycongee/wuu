package plugin

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestDiscoverProjectOverridesUserAndResolvesAssetDirs(t *testing.T) {
	root := t.TempDir()
	wuuHome := filepath.Join(t.TempDir(), "wuu-home")
	writePlugin(t, filepath.Join(wuuHome, "plugins", "compose"), `{
  "id": "compose",
  "description": "user compose",
  "skills": ["skills"]
}`)
	writePlugin(t, filepath.Join(root, ".wuu", "plugins", "compose"), `{
  "id": "compose",
  "description": "project compose",
  "skills": ["skills"]
}`)

	plugins := Discover(root, wuuHome)
	got, ok := findPlugin(plugins, "compose")
	if !ok {
		t.Fatalf("compose plugin missing: %+v", plugins)
	}
	if got.ID != "compose" || got.Source != "project" || got.Description != "project compose" {
		t.Fatalf("project plugin should override user plugin: %+v", got)
	}
	if got.SourceLabel() != "plugin:compose" {
		t.Fatalf("SourceLabel = %q", got.SourceLabel())
	}
	skillDirs := got.SkillDirs()
	if len(skillDirs) != 1 || skillDirs[0] != filepath.Join(got.Root, "skills") {
		t.Fatalf("SkillDirs = %+v", skillDirs)
	}
}

func TestDiscoverLoadsNestedCodexAndClaudeManifestLocations(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, ".wuu", "plugins", "codex-kit", ".codex-plugin", "plugin.json"), `{"name":"codex-kit"}`)
	writeFile(t, filepath.Join(root, ".wuu", "plugins", "claude-kit", ".claude-plugin", "plugin.json"), `{"name":"claude-kit"}`)

	plugins := Discover(root, "")
	_, hasClaude := findPlugin(plugins, "claude-kit")
	_, hasCodex := findPlugin(plugins, "codex-kit")
	if !hasClaude || !hasCodex {
		t.Fatalf("plugins = %+v", plugins)
	}
}

func TestDiscoverScopesProjectPluginPolicyByWorkspace(t *testing.T) {
	firstRoot := t.TempDir()
	secondRoot := t.TempDir()
	writePlugin(t, filepath.Join(firstRoot, ".wuu", "plugins", "same-id"), `{"id":"same-id"}`)
	writePlugin(t, filepath.Join(secondRoot, ".wuu", "plugins", "same-id"), `{"id":"same-id"}`)

	first, ok := findPlugin(Discover(firstRoot, ""), "same-id")
	if !ok {
		t.Fatal("first workspace plugin missing")
	}
	second, ok := findPlugin(Discover(secondRoot, ""), "same-id")
	if !ok {
		t.Fatal("second workspace plugin missing")
	}
	if first.WorkspaceID == "" || second.WorkspaceID == "" {
		t.Fatalf("workspace policy ids are required: %q %q", first.WorkspaceID, second.WorkspaceID)
	}
	if first.WorkspaceID == second.WorkspaceID || first.SubjectID == second.SubjectID {
		t.Fatalf("project plugin policy leaked across workspaces: %q %q", first.SubjectID, second.SubjectID)
	}
	firstAgain, ok := findPlugin(Discover(firstRoot, ""), "same-id")
	if !ok || firstAgain.SubjectID != first.SubjectID {
		t.Fatalf("workspace policy identity is not stable: %q %q", first.SubjectID, firstAgain.SubjectID)
	}
}

func TestDiscoverUsesDefaultAssetDirsWhenManifestOmitsThem(t *testing.T) {
	root := t.TempDir()
	pluginDir := filepath.Join(root, ".wuu", "plugins", "default-assets")
	writePlugin(t, pluginDir, `{"id":"default-assets"}`)

	plugins := Discover(root, "")
	got, ok := findPlugin(plugins, "default-assets")
	if !ok {
		t.Fatalf("default-assets plugin missing: %+v", plugins)
	}
	if len(got.SkillDirs()) != 1 {
		t.Fatalf("default skill dirs not discovered: skills=%+v", got.SkillDirs())
	}
}

func TestLoadManifestRequiresID(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ManifestFilename)
	if err := os.WriteFile(path, []byte(`{"description":"missing id"}`), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	if _, err := LoadManifest(path, "project"); err == nil {
		t.Fatal("expected missing id error")
	}
}

func TestDiscoverWithOptionsIncludesOfficialBundledCUAWhenEnabled(t *testing.T) {
	for _, target := range []struct{ platform, pluginID, enableEnv, helperEnv string }{
		{"darwin", "cua-mac", "WUU_ENABLE_CUA_MAC", "WUU_CUA_MAC_HELPER"},
		{"linux", "cua-linux", "WUU_ENABLE_CUA_LINUX", "WUU_CUA_LINUX_HELPER"},
	} {
		t.Run(target.pluginID, func(t *testing.T) {
			helper := filepath.Join(t.TempDir(), "wuu-"+target.pluginID)
			if err := os.WriteFile(helper, []byte("#!/bin/sh\n"), 0o755); err != nil {
				t.Fatal(err)
			}

			plugins := DiscoverWithOptions("", t.TempDir(), DiscoverOptions{
				GOOS: target.platform,
				LookupEnv: func(key string) (string, bool) {
					switch key {
					case target.enableEnv:
						return "1", true
					case target.helperEnv:
						return helper, true
					}
					return "", false
				},
			})

			var cua *Plugin
			for index := range plugins {
				if plugins[index].ID == target.pluginID {
					cua = &plugins[index]
					break
				}
			}
			if cua == nil {
				t.Fatalf("bundled %s plugin missing: %+v", target.pluginID, plugins)
			}
			if !cua.Official || cua.Source != "bundled" {
				t.Fatalf("%s provenance = official:%v source:%q", target.pluginID, cua.Official, cua.Source)
			}
			if got := cua.MCPServers["computer"].Command; got != helper {
				t.Fatalf("%s helper command = %q, want %q", target.pluginID, got, helper)
			}
			if len(cua.SkillDirs()) != 1 {
				t.Fatalf("%s skill dirs = %+v", target.pluginID, cua.SkillDirs())
			}
		})
	}
}

func TestDiscoverWithOptionsHidesBundledCUAByDefault(t *testing.T) {
	for _, target := range []struct {
		platform string
		enabled  bool
	}{
		{"darwin", false}, {"linux", false}, {"windows", true},
	} {
		t.Run(target.platform, func(t *testing.T) {
			plugins := DiscoverWithOptions("", t.TempDir(), DiscoverOptions{
				GOOS:     target.platform,
				LookPath: func(command string) (string, error) { return command, nil },
				LookupEnv: func(key string) (string, bool) {
					return "1", target.enabled && (key == "WUU_ENABLE_CUA_MAC" || key == "WUU_ENABLE_CUA_LINUX")
				},
			})
			for _, item := range plugins {
				if item.ID == "cua-mac" || item.ID == "cua-linux" {
					t.Fatalf("%s must stay hidden without opt-in on a supported platform: %+v", item.ID, item)
				}
			}
		})
	}
}

func TestDiscoverWithOptionsIncludesBundledPeers(t *testing.T) {
	plugins := DiscoverWithOptions("", t.TempDir(), DiscoverOptions{
		GOOS:      "darwin",
		LookPath:  func(command string) (string, error) { return command, nil },
		LookupEnv: func(string) (string, bool) { return "", false },
	})

	var peers *Plugin
	for index := range plugins {
		if plugins[index].ID == "peers" {
			peers = &plugins[index]
			break
		}
	}
	if peers == nil {
		t.Fatalf("bundled peers plugin missing: %+v", plugins)
	}
	if !peers.Official || peers.Source != "bundled" {
		t.Fatalf("peers provenance = official:%v source:%q", peers.Official, peers.Source)
	}
}

func TestBundledOptionalPluginsAreInstalledButDisabledByDefault(t *testing.T) {
	plugins := DiscoverWithOptions("", t.TempDir(), DiscoverOptions{
		WuuVersion: "2026.10.12",
		GOOS:       runtime.GOOS,
		LookPath:   func(command string) (string, error) { return command, nil },
		LookupEnv:  func(string) (string, bool) { return "", false },
	})
	want := map[string]bool{"ask-user": false, "subagent": false, "dream": false, "memory": false, "next-message": false}
	for _, item := range plugins {
		if _, ok := want[item.ID]; ok {
			want[item.ID] = !item.EnabledByDefault()
		}
	}
	for id, foundDisabled := range want {
		if !foundDisabled {
			t.Errorf("bundled plugin %q was missing or enabled by default", id)
		}
	}
	var side *Plugin
	for i := range plugins {
		if plugins[i].ID == "side" {
			side = &plugins[i]
			break
		}
	}
	if side == nil || !side.EnabledByDefault() || !side.Official || len(side.Commands) != 1 || side.Commands[0].Kind != CommandKindSideConversation || side.Commands[0].ResolvedPrompt == nil {
		t.Fatalf("bundled side command missing or not enabled by default: %+v", side)
	}

}

func writePlugin(t *testing.T, dir, manifest string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, "skills"), 0o755); err != nil {
		t.Fatalf("mkdir skills: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, ManifestFilename), []byte(manifest), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
}

func writeFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func findPlugin(plugins []Plugin, id string) (Plugin, bool) {
	for _, item := range plugins {
		if item.ID == id {
			return item, true
		}
	}
	return Plugin{}, false
}
