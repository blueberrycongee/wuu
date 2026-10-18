package runtime

import (
	"context"
	"github.com/blueberrycongee/wuu/internal/tools"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/userspace"
)

// A user-host boundary must be installed before any state exists, while legacy
// Coding tool behavior stays unchanged. The separate two-way host-isolation
// prerequisite cannot be replaced by this one-way toolkit restriction.
func TestUserHostProtectedPathsAndFileScope(t *testing.T) {
	home := t.TempDir()
	if roots := memoryProtectedPaths(home, userspace.Binding{}); len(roots) != 0 {
		t.Fatalf("Coding behavior changed: %v", roots)
	}
	b := userspace.Binding{UserSpaceID: "local", ProductDomain: "personal"}
	roots := memoryProtectedPaths(home, b)
	for _, want := range []string{b.State(home), filepath.Join(home, "sessions"), filepath.Join(home, "plugin-storage")} {
		found := false
		for _, root := range roots {
			if root == want {
				found = true
			}
		}
		if !found {
			t.Errorf("missing future protected root %s", want)
		}
	}
	scopes := runtimeFileScopeRoots(b.Root(home), home, b, "/untrusted/project")
	if len(scopes) != 1 || scopes[0] != b.Root(home) {
		t.Fatalf("user host inherited project scopes: %v", scopes)
	}
}

func TestUserHostActivationRequiresTrustedIsolationVerifier(t *testing.T) {
	_, err := NewSession(Options{RootDir: t.TempDir(), HomeDir: t.TempDir(), UserSpaceID: "local", ProductDomain: "personal"})
	if err == nil || !strings.Contains(err.Error(), "verified host isolation") {
		t.Fatalf("unverified user host was not rejected first: %v", err)
	}
}

func TestUserHostConfigDoesNotInheritExecutableCodingExtensions(t *testing.T) {
	cfg := config.Config{Hooks: map[string][]config.HookEntry{"x": {}}, MCPServers: map[string]config.MCPServerConfig{"coding": {}}, MCPJson: &config.MCPJsonTrust{}}
	legacy := userHostConfig(cfg, userspace.Binding{})
	if len(legacy.Hooks) != 1 || len(legacy.MCPServers) != 1 {
		t.Fatal("Coding config changed")
	}
	personal := userHostConfig(cfg, userspace.Binding{UserSpaceID: "local", ProductDomain: "personal"})
	if personal.Hooks != nil || personal.MCPServers != nil || personal.MCPJson != nil {
		t.Fatal("Personal inherited Coding executable extensions")
	}
	if len(cfg.Hooks) != 1 || len(cfg.MCPServers) != 1 {
		t.Fatal("shared config was mutated")
	}
}

func TestUserHostSkillRefreshCannotReintroduceGlobalSkills(t *testing.T) {
	home := t.TempDir()
	root := t.TempDir()
	path := filepath.Join(home, ".agents", "skills", "coding-secret")
	if err := os.MkdirAll(path, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "SKILL.md"), []byte("---\nname: coding-secret\ndescription: Coding only\n---\nPrivate Coding context"), 0600); err != nil {
		t.Fatal(err)
	}
	kit, err := tools.New(root)
	if err != nil {
		t.Fatal(err)
	}
	kit.SetSkills(nil)
	rt := &Session{UserSpaceID: "local", ProductDomain: "personal", HomeDir: home, WuuHome: home, RootDir: root}
	rt.bindLocalSkillRefresh(kit, root, nil)
	skills, err := kit.CurrentSkills(context.Background(), true)
	if err != nil {
		t.Fatal(err)
	}
	if len(skills) != 0 {
		t.Fatalf("user host discovered Coding/global skills: %v", skills)
	}
}
