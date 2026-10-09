package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/blueberrycongee/wuu/internal/approvefor"
	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/providers"
)

func TestPluginManagerExecutionTrustBoundary(t *testing.T) {
	for _, tc := range []struct {
		name     string
		boundary WorkspaceBoundary
		review   bool
		outcome  string
		allowed  bool
	}{
		{"standard without reviewer", StandardBoundary(), false, "", false},
		{"reviewer missing", StandardBoundary(), true, "", false},
		{"reviewer fails", StandardBoundary(), true, approvefor.OutcomeFailed, false},
		{"reviewer denies", StandardBoundary(), true, approvefor.OutcomeDeny, false},
		{"reviewer allows", StandardBoundary(), true, approvefor.OutcomeAllow, true},
		{"read only", ReadOnlyBoundary(), true, approvefor.OutcomeAllow, false},
		{"unconfined", UnconfinedBoundary(), false, "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			t.Setenv("WUU_HOME", t.TempDir())
			if err := os.WriteFile(filepath.Join(root, "extension.ts"), []byte("export default {}"), 0600); err != nil {
				t.Fatal(err)
			}
			kit, err := New(root)
			if err != nil {
				t.Fatal(err)
			}
			kit.SetToolSearchEnabled(false)
			kit.SetBoundary(tc.boundary)
			kit.SetPermissionMode("standard")
			kit.SetApproveForMe(tc.review)
			if tc.outcome != "" {
				kit.SetReviewer(&recordingReviewer{decision: approvefor.Decision{Outcome: tc.outcome}})
			}
			calls := 0
			kit.SetPluginManager(func(_ context.Context, req PluginManagementRequest) (any, error) {
				calls++
				return map[string]any{"published": true}, nil
			})
			if _, err := NewToolSearchTool(kit).Execute(context.Background(), `{"query":"select:plugin_manager"}`); err != nil {
				t.Fatal(err)
			}
			_, err = kit.Execute(context.Background(), providers.ToolCall{Name: "plugin_manager", Arguments: `{"action":"apply","path":"extension.ts"}`})
			if (err == nil) != tc.allowed || (calls == 1) != tc.allowed {
				t.Fatalf("err=%v calls=%d allowed=%v", err, calls, tc.allowed)
			}
		})
	}
}

func TestPluginManagerStatusCloneAndSourceScope(t *testing.T) {
	root := t.TempDir()
	t.Setenv("WUU_HOME", t.TempDir())
	kit, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	kit.SetToolSearchEnabled(false)
	calls := 0
	kit.SetPluginManager(func(_ context.Context, req PluginManagementRequest) (any, error) {
		calls++
		return map[string]any{"action": req.Action}, nil
	})
	clone, err := kit.CloneForRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	clone.SetBoundary(ReadOnlyBoundary())
	if _, err := NewToolSearchTool(clone).Execute(context.Background(), `{"query":"select:plugin_manager"}`); err != nil {
		t.Fatal(err)
	}
	if _, err := clone.Execute(context.Background(), providers.ToolCall{Name: "plugin_manager", Arguments: `{"action":"status"}`}); err != nil {
		t.Fatal(err)
	}
	if _, err := NewToolSearchTool(kit).Execute(context.Background(), `{"query":"select:plugin_manager"}`); err != nil {
		t.Fatal(err)
	}
	kit.SetApproveForMe(true)
	kit.SetPermissionMode("standard")
	kit.SetReviewer(&recordingReviewer{decision: approvefor.Decision{Outcome: approvefor.OutcomeAllow}})
	outside := filepath.Join(t.TempDir(), "extension.ts")
	if err := os.WriteFile(outside, []byte(""), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "escape.ts")); err != nil {
		t.Fatal(err)
	}
	_, err = kit.Execute(context.Background(), providers.ToolCall{Name: "plugin_manager", Arguments: `{"action":"apply","path":"escape.ts"}`})
	if err == nil || !strings.Contains(err.Error(), "workspace") || calls != 1 {
		t.Fatalf("err=%v calls=%d", err, calls)
	}
}

func TestPluginManagerProfileDiscoveryAndCodeMode(t *testing.T) {
	kit := newCodeModeTestToolkit(t)
	kit.SetPluginManager(func(_ context.Context, req PluginManagementRequest) (any, error) {
		return map[string]any{"action": req.Action, "published_fingerprint": "new", "current_conversation_fingerprint": "old"}, nil
	})
	kit.ConfigureSurfaceForProviderModel("openai", "gpt-5.5", true)
	kit.SetToolSearchEnabled(true)
	kit.ConfigurePTC(kit.CodeModeService(), config.PTCConfig{Enabled: false})
	discovery, err := kit.Execute(context.Background(), providers.ToolCall{Name: "tool_search", Arguments: `{"query":"select:plugin_manager"}`})
	if err != nil || !strings.Contains(discovery, "plugin_manager") {
		t.Fatalf("discovery: %s %v", discovery, err)
	}
	kit.ConfigurePTC(kit.CodeModeService(), config.PTCConfig{Enabled: true})
	resultRich := runPTCProgram(t, kit, `const result = await tools.plugin_manager({action:"status"}); text(result);`, 4096)
	if resultRich.IsError || !strings.Contains(resultRich.TextProjection(), "published_fingerprint") {
		t.Fatalf("code mode: %s", resultRich.TextProjection())
	}
}

type pluginManagementReviewer struct{ pluginCalls int }

func (r *pluginManagementReviewer) Review(_ context.Context, req approvefor.Request) (approvefor.Decision, error) {
	if req.Tool.Name == "plugin_manager" {
		r.pluginCalls++
		return approvefor.Decision{Outcome: approvefor.OutcomeDeny, Reason: "host execution not authorized"}, nil
	}
	return approvefor.Decision{Outcome: approvefor.OutcomeAllow}, nil
}
func TestPluginManagerCodeModeCannotBypassExecutionTrust(t *testing.T) {
	kit := newCodeModeTestToolkit(t)
	// The interpreter fixture is unconfined; standard reviewer policy still
	// evaluates each nested host operation independently of its outer program.
	kit.SetPermissionMode("standard")
	kit.SetApproveForMe(true)
	reviewer := &pluginManagementReviewer{}
	kit.SetReviewer(reviewer)
	calls := 0
	kit.SetPluginManager(func(context.Context, PluginManagementRequest) (any, error) { calls++; return nil, nil })
	result := runPTCProgram(t, kit, `text(await tools.plugin_manager({action:"apply",path:"extension.ts"}));`)
	if calls != 0 || reviewer.pluginCalls != 1 || !strings.Contains(result.TextProjection(), "host execution not authorized") {
		t.Fatalf("calls=%d reviews=%d result=%s", calls, reviewer.pluginCalls, result.TextProjection())
	}
}

func TestPluginManagerUsesBoundWorktreeSourceAndInventory(t *testing.T) {
	kit, parent, worktree, ctx := newWorktreeExecFixture(t)
	kit.SetApproveForMe(true)
	kit.SetPermissionMode("standard")
	kit.SetReviewer(&recordingReviewer{decision: approvefor.Decision{Outcome: approvefor.OutcomeAllow}})
	if err := os.WriteFile(filepath.Join(parent, "extension.ts"), []byte("stale parent"), 0600); err != nil {
		t.Fatal(err)
	}
	calls := 0
	kit.SetPluginManager(func(_ context.Context, req PluginManagementRequest) (any, error) {
		calls++
		if req.CWD != worktree {
			t.Fatalf("inventory/command cwd=%q want checkout %q", req.CWD, worktree)
		}
		if req.Action == "apply" {
			if filepath.Dir(req.Path) != worktree {
				t.Fatalf("source=%q want checkout", req.Path)
			}
			content, err := os.ReadFile(req.Path)
			if err != nil || string(content) != "checkout source" {
				t.Fatalf("source content=%q err=%v", content, err)
			}
		}
		return map[string]any{"action": req.Action}, nil
	})
	if _, err := NewToolSearchTool(kit).Execute(ctx, `{"query":"select:plugin_manager"}`); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"extension.ts", "new-extension.ts"} {
		executeToolForWorktreeTest(t, kit, ctx, "write_file", `{"path":"`+name+`","content":"checkout source"}`)
		if _, err := kit.Execute(ctx, providers.ToolCall{Name: "plugin_manager", Arguments: `{"action":"apply","path":"` + name + `"}`}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := kit.Execute(ctx, providers.ToolCall{Name: "plugin_manager", Arguments: `{"action":"status"}`}); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(filepath.Join(parent, "extension.ts"))
	if err != nil || string(content) != "stale parent" {
		t.Fatalf("parent changed: %q %v", content, err)
	}
	if _, err := os.Stat(filepath.Join(parent, "new-extension.ts")); !os.IsNotExist(err) {
		t.Fatalf("new source unexpectedly exists in parent: %v", err)
	}
	outside := filepath.Join(t.TempDir(), "outside.ts")
	if err := os.WriteFile(outside, []byte("outside"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(worktree, "escape.ts")); err != nil {
		t.Fatal(err)
	}
	if _, err := kit.Execute(ctx, providers.ToolCall{Name: "plugin_manager", Arguments: `{"action":"apply","path":"escape.ts"}`}); err == nil {
		t.Fatal("accepted checkout symlink escaping allowed scope")
	}
	if calls != 3 {
		t.Fatalf("host callback calls=%d want 3", calls)
	}
}
