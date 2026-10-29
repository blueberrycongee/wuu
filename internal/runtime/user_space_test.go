package runtime

import (
	"context"
	"errors"
	"github.com/blueberrycongee/wuu/internal/tools"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/blueberrycongee/wuu/internal/composition"
	"github.com/blueberrycongee/wuu/internal/config"
	pluginpkg "github.com/blueberrycongee/wuu/internal/plugin"
	"github.com/blueberrycongee/wuu/internal/userspace"
)

// A user-host boundary must be installed before any state exists, while legacy
// Coding tool behavior stays unchanged. These one-way toolkit restrictions
// remain defense in depth and do not prove strong two-way host isolation.
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

// Activation must distinguish logical memory boundaries from separately verified
// host isolation. Invalid modes/bindings and unavailable strong verification fail
// before creating user data; legacy Coding needs no user-host contract.
func TestUserHostActivationIsolationModes(t *testing.T) {
	for _, tc := range []struct {
		name, space, domain string
		mode                UserHostIsolationMode
		verifier            *testUserHostIsolationVerifier
		want                UserHostIsolationMode
		wantError           string
	}{
		{name: "logical default", space: "local", domain: "personal", want: UserHostIsolationLogicalMemory},
		{name: "logical explicit", space: "local", domain: "personal", mode: UserHostIsolationLogicalMemory, want: UserHostIsolationLogicalMemory},
		{name: "logical does not claim verifier", space: "local", domain: "personal", verifier: &testUserHostIsolationVerifier{err: errors.New("not isolated")}, want: UserHostIsolationLogicalMemory},
		{name: "verified missing verifier", space: "local", domain: "personal", mode: UserHostIsolationVerifiedHost, wantError: UserHostIsolationRequiredCode},
		{name: "verified denied", space: "local", domain: "personal", mode: UserHostIsolationVerifiedHost, verifier: &testUserHostIsolationVerifier{err: errors.New("identity shared")}, wantError: "identity shared"},
		{name: "verified success", space: "local", domain: "personal", mode: UserHostIsolationVerifiedHost, verifier: &testUserHostIsolationVerifier{}, want: UserHostIsolationVerifiedHost},
		{name: "unknown mode", space: "local", domain: "personal", mode: "invented", wantError: "unsupported user host isolation mode"},
		{name: "missing space", domain: "personal", wantError: "user_space_id"},
		{name: "missing domain", space: "local", wantError: "product_domain"},
		{name: "invalid space", space: "../escape", domain: "personal", wantError: "user_space_id"},
		{name: "mode without binding", mode: UserHostIsolationVerifiedHost, wantError: "requires an explicit user binding"},
		{name: "legacy Coding"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home, root := t.TempDir(), t.TempDir()
			state := filepath.Join(home, "state")
			t.Setenv("WUU_HOME", state)
			t.Setenv("TEST_WUU_KEY", "synthetic")
			opts := Options{RootDir: root, HomeDir: home, UserSpaceID: tc.space, ProductDomain: tc.domain, UserHostIsolationMode: tc.mode, SafeMode: true, NoTools: true,
				Config: config.Config{DefaultProvider: "test", Providers: map[string]config.ProviderConfig{"test": {Type: "openai-compatible", BaseURL: "https://example.test/v1", APIKeyEnv: "TEST_WUU_KEY", Model: "test"}}}}
			if tc.verifier != nil {
				opts.UserHostIsolationVerifier = tc.verifier
			}
			rt, err := NewSession(opts)
			if tc.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantError) {
					t.Fatalf("activation error = %v, want %q", err, tc.wantError)
				}
				if _, err := os.Stat(filepath.Join(state, "user-spaces")); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("rejected activation created user data: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _, _ = rt.Cleanup() })
			if rt.UserHostIsolationMode != tc.want {
				t.Fatalf("isolation mode = %q, want %q", rt.UserHostIsolationMode, tc.want)
			}
			if tc.verifier != nil {
				wantCalls := 0
				if tc.mode == UserHostIsolationVerifiedHost {
					wantCalls = 1
				}
				if tc.verifier.calls != wantCalls {
					t.Fatalf("verifier calls = %d, want %d", tc.verifier.calls, wantCalls)
				}
			}
			if tc.space != "" {
				binding := userspace.Binding{UserSpaceID: tc.space, ProductDomain: tc.domain}
				if rt.RootDir != binding.Root(state) || rt.ProductDataRoot() != binding.State(state) || rt.SessionDir != filepath.Join(binding.State(state), "sessions") {
					t.Fatalf("unscoped user runtime: root=%s state=%s sessions=%s", rt.RootDir, rt.ProductDataRoot(), rt.SessionDir)
				}
			} else if rt.RootDir != root || rt.ProductDataRoot() != state {
				t.Fatal("legacy Coding roots changed")
			}
		})
	}
}

type testUserHostIsolationVerifier struct {
	calls int
	err   error
}

func (v *testUserHostIsolationVerifier) VerifyUserHostIsolation(context.Context, userspace.Binding, string) error {
	v.calls++
	return v.err
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

// The selected native session and document providers must use the product's
// data root. Matching IDs are valid in different domains and survive restart.
func TestUserHostSessionStorageIsolatedFromCodingAcrossRestart(t *testing.T) {
	home, root := t.TempDir(), t.TempDir()
	cfg := config.Default()
	cfg.DefaultProvider = "fixture"
	cfg.Providers = map[string]config.ProviderConfig{"fixture": {Type: "openai-compatible", BaseURL: "http://127.0.0.1:9/v1", APIKey: "synthetic", Model: "fixture"}}
	cfg.Composition = &composition.Config{Version: composition.Version, Profile: "headless"}
	plugins := []pluginpkg.Plugin{}
	open := func(personal bool) *Session {
		t.Helper()
		opts := Options{RootDir: root, WuuHome: home, HomeDir: home, Config: cfg, DiscoveredPlugins: &plugins, NonInteractive: true}
		if personal {
			opts.UserSpaceID, opts.ProductDomain = "alice", "personal"
		}
		rt, err := NewSession(opts)
		if err != nil {
			t.Fatal(err)
		}
		return rt
	}
	closeSession := func(rt *Session) {
		t.Helper()
		if _, err := rt.Cleanup(); err != nil {
			t.Fatal(err)
		}
	}
	coding := open(false)
	if _, err := coding.Sessions.CreateWithMetadata("shared-id", "/coding-only"); err != nil {
		t.Fatal(err)
	}
	closeSession(coding)

	personal := open(true)
	if _, found, err := personal.Sessions.Find("shared-id"); err != nil || found {
		t.Fatalf("Personal observed Coding session: found=%v err=%v", found, err)
	}
	if _, err := personal.Sessions.CreateWithMetadata("shared-id", "/personal-only"); err != nil {
		t.Fatal(err)
	}
	closeSession(personal)

	for _, tc := range []struct {
		personal bool
		want     string
	}{{false, "/coding-only"}, {true, "/personal-only"}} {
		rt := open(tc.personal)
		got, found, err := rt.Sessions.Find("shared-id")
		if err != nil || !found || got.CWD != tc.want {
			t.Fatalf("personal=%v session=%+v found=%v err=%v", tc.personal, got, found, err)
		}
		closeSession(rt)
	}
}
