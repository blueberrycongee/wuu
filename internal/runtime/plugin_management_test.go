package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/blueberrycongee/wuu/internal/extensions"
	pluginpkg "github.com/blueberrycongee/wuu/internal/plugin"
	"github.com/blueberrycongee/wuu/internal/session"
	"github.com/blueberrycongee/wuu/internal/tools"
)

func TestPluginManagementKeepsConversationGenerationPinned(t *testing.T) {
	home := t.TempDir()
	root := t.TempDir()
	dir := filepath.Join(home, "plugins", "example")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "plugin.json"), []byte(`{"schema_version":1,"id":"example","name":"Example"}`), 0600); err != nil {
		t.Fatal(err)
	}
	found := pluginpkg.Discover(root, home)
	var item pluginpkg.Plugin
	for _, p := range found {
		if p.ID == "example" {
			item = p
		}
	}
	if item.ID == "" {
		t.Fatal("fixture not discovered")
	}
	settings := extensions.Settings{}
	if err := settings.RecordGrant(extensions.Grant{SubjectID: item.SubjectID, Fingerprint: item.Fingerprint, Scope: extensions.GrantScopeUser, Permissions: item.EffectivePermissions}); err != nil {
		t.Fatal(err)
	}
	settings.SetEnabled(item.SubjectID, true)
	policy, err := json.Marshal(map[string]any{"extensions": settings})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "config.json"), policy, 0600); err != nil {
		t.Fatal(err)
	}
	old := item
	old.Fingerprint = "previous-generation"
	s := &Session{RootDir: root, WuuHome: home}
	generation := &PluginGeneration{active: []pluginpkg.Plugin{old}}
	manager := s.pluginManager(generation, func(context.Context, string, string, []string) (string, error) {
		t.Fatal("status executed command")
		return "", nil
	})
	value, err := manager(context.Background(), tools.PluginManagementRequest{Action: "status", ID: "example", CWD: root})
	if err != nil {
		t.Fatal(err)
	}
	result := value.(pluginManagementResult)
	if len(result.Plugins) != 1 || result.Plugins[0].CurrentFingerprint != "previous-generation" || result.Plugins[0].PublishedFingerprint != item.Fingerprint {
		t.Fatalf("status=%+v", result)
	}
	// Re-enabling unchanged bytes must not reuse a revoked conversation host.
	generation.active = []pluginpkg.Plugin{item}
	generation.revokedPlugins = map[string]bool{item.ID: true}
	value, err = manager(context.Background(), tools.PluginManagementRequest{Action: "status", ID: "example", CWD: root})
	if err != nil {
		t.Fatal(err)
	}
	result = value.(pluginManagementResult)
	if !result.Plugins[0].AdoptionPending || result.Plugins[0].RuntimeState != "stopped" {
		t.Fatalf("reenabled revoked generation=%+v", result)
	}
	s.SafeMode = true
	value, err = manager(context.Background(), tools.PluginManagementRequest{Action: "status", ID: "example", CWD: root})
	if err != nil {
		t.Fatal(err)
	}
	if value.(pluginManagementResult).Plugins[0].AdoptionPending {
		t.Fatal("safe mode promised adoption")
	}
}

func TestPluginManagementFixedCommand(t *testing.T) {
	home := t.TempDir()
	root := t.TempDir()
	source := filepath.Join(root, "extension.ts")
	var got []string
	s := &Session{RootDir: root, WuuHome: home}
	manager := s.pluginManager(nil, func(_ context.Context, gotHome, gotCWD string, args []string) (string, error) {
		if gotHome != home || gotCWD != root {
			t.Fatalf("home=%s cwd=%s", gotHome, gotCWD)
		}
		got = args
		return "published", nil
	})
	_, err := manager(context.Background(), tools.PluginManagementRequest{Action: "apply", Path: source, CWD: root})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 4 || got[0] != "plugin" || got[1] != "dev" || got[2] != "--watch=false" || got[3] != source {
		t.Fatalf("args=%q", got)
	}
	expected := ""
	for _, tc := range []struct {
		req  tools.PluginManagementRequest
		args []string
	}{
		{tools.PluginManagementRequest{Action: "capture", Path: source, CWD: root}, []string{"plugin", "experiment", "capture", "--json", "--workdir", root, "--", source}},
		{tools.PluginManagementRequest{Action: "candidates", ID: "example", CWD: root}, []string{"plugin", "experiment", "list", "--json", "--workdir", root, "--", "example"}},
		{tools.PluginManagementRequest{Action: "select", ID: "example", CandidateID: "candidate", ExpectedCurrent: &expected, CWD: root}, []string{"plugin", "experiment", "select", "--json", "--workdir", root, "--expect-current=", "--", "example", "candidate"}},
		{tools.PluginManagementRequest{Action: "rollback", ID: "example", CandidateID: "candidate", ExpectedCurrent: &expected, CWD: root}, []string{"plugin", "experiment", "rollback", "--json", "--workdir", root, "--expect-current=", "--", "example", "candidate"}},
		{tools.PluginManagementRequest{Action: "eval", ID: "example", CandidateIDs: []string{"first", "second"}, Command: []string{"echo", "literal; not a shell"}, Concurrency: 2, TimeoutSeconds: 60, CWD: root}, []string{"plugin", "experiment", "eval", "--json", "--workspace", root, "--concurrency", "2", "--timeout", "60s", "--", "example", "first", "second", "--", "echo", "literal; not a shell"}},
	} {
		t.Run(tc.req.Action, func(t *testing.T) {
			manager := s.pluginManager(nil, func(_ context.Context, gotHome, gotCWD string, args []string) (string, error) {
				if gotHome != home || gotCWD != root || !reflect.DeepEqual(args, tc.args) {
					t.Fatalf("home=%s cwd=%s args=%q", gotHome, gotCWD, args)
				}
				return `{"receipt":"saved"}`, nil
			})
			value, err := manager(context.Background(), tc.req)
			if err != nil || string(value.(pluginManagementResult).Experiment) != `{"receipt":"saved"}` {
				t.Fatalf("value=%+v err=%v", value, err)
			}
		})
	}
}

func TestPluginManagementCatalogBusyAndCommandFailure(t *testing.T) {
	home := t.TempDir()
	root := t.TempDir()
	s := &Session{RootDir: root, WuuHome: home}
	calls := 0
	manager := s.pluginManager(nil, func(context.Context, string, string, []string) (string, error) {
		calls++
		return "candidate rejected", errors.New("command failed")
	})
	lease, acquired, err := session.TryAcquirePluginCatalogMutationLease(home)
	if err != nil || !acquired {
		t.Fatalf("lease=%v %v", acquired, err)
	}
	if _, err := manager(context.Background(), tools.PluginManagementRequest{Action: "status", CWD: root}); err == nil {
		t.Fatal("status read a mutating catalog")
	}
	if err := lease.Release(); err != nil {
		t.Fatal(err)
	}
	result, err := manager(context.Background(), tools.PluginManagementRequest{Action: "apply", Path: filepath.Join(root, "extension.ts"), CWD: root})
	if err == nil || result != nil || calls != 1 {
		t.Fatalf("result=%v calls=%d err=%v", result, calls, err)
	}
}
