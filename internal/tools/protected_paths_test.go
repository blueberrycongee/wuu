package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/blueberrycongee/wuu/internal/codemode"
	"github.com/blueberrycongee/wuu/internal/executionenv"
	"github.com/blueberrycongee/wuu/internal/processsandbox"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/toolctx"
	"github.com/blueberrycongee/wuu/internal/toolresult"
)

// Failure cases: unconfined and whitelist widening, symlinked existing and
// not-yet-created targets, session artifact aliases, ancestor search, worktree
// rebasing, and cloned/nested execution must not cross a protected domain.
func TestProtectedPathsSurviveFileToolEscapeRoutes(t *testing.T) {
	root := t.TempDir()
	protected := filepath.Join(root, "private-domain")
	mustWriteFile(t, filepath.Join(protected, "memory.txt"), "DOMAIN_SECRET")
	public := filepath.Join(root, "public")
	mustWriteFile(t, filepath.Join(public, "ordinary.txt"), "ordinary")
	alias := filepath.Join(public, "alias")
	if err := os.Symlink(protected, alias); err != nil {
		t.Fatal(err)
	}
	kit, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	roots := []string{protected}
	kit.SetProtectedPaths(roots)
	roots[0] = public // caller-owned configuration must not remain mutable
	kit.SetBoundary(UnconfinedBoundary())
	kit.SetFileScopeRoots([]string{root})
	kit.SetSessionDir(root)
	kit.SetProtectedPaths(nil) // removing ordinary limits cannot clear exclusions
	for _, tc := range []struct{ name, args string }{
		{"read_file", fmt.Sprintf(`{"path":%q}`, filepath.Join(protected, "memory.txt"))},
		{"read_file", fmt.Sprintf(`{"path":%q}`, filepath.Join(alias, "memory.txt"))},
		{"read_file", `{"path":"$SESSION_DIR/private-domain/memory.txt"}`},
		{"write_file", fmt.Sprintf(`{"path":%q,"content":"overwrite"}`, filepath.Join(protected, "memory.txt"))},
		{"write_file", fmt.Sprintf(`{"path":%q,"content":"created"}`, filepath.Join(alias, "new", "memory.txt"))},
		{"edit_file", fmt.Sprintf(`{"path":%q,"old_text":"DOMAIN_SECRET","new_text":"overwrite"}`, filepath.Join(protected, "memory.txt"))},
		{"grep", `{"pattern":"DOMAIN_SECRET"}`},
		{"grep", fmt.Sprintf(`{"path":%q,"pattern":"DOMAIN_SECRET","output_mode":"count"}`, root)},
		{"glob", `{"pattern":"**/*"}`},
		{"list_files", fmt.Sprintf(`{"path":%q}`, protected)},
	} {
		t.Run(tc.name+tc.args, func(t *testing.T) {
			output, err := kit.Execute(context.Background(), providers.ToolCall{Name: tc.name, Arguments: tc.args})
			if err == nil || !strings.Contains(err.Error(), "protected") || strings.Contains(output, "DOMAIN_SECRET") {
				t.Fatalf("protected operation output=%q error=%v", output, err)
			}
		})
	}
	if out, err := kit.Execute(context.Background(), providers.ToolCall{Name: "read_file", Arguments: fmt.Sprintf(`{"path":%q}`, filepath.Join(public, "ordinary.txt"))}); err != nil || !strings.Contains(out, "ordinary") {
		t.Fatalf("ordinary read: %s %v", out, err)
	}
	if out, err := kit.Execute(context.Background(), providers.ToolCall{Name: "list_files", Arguments: fmt.Sprintf(`{"path":%q}`, public)}); err != nil || strings.Contains(out, `"name":"alias"`) {
		t.Fatalf("directory leaked protected alias: %s %v", out, err)
	}
	clone, err := kit.CloneForRoot(public)
	if err != nil {
		t.Fatal(err)
	}
	clone.SetBoundary(UnconfinedBoundary())
	clone.SetProtectedPaths(nil)
	if _, err := clone.ExecuteEnvironmentResult(context.Background(), providers.ToolCall{Name: "read_file", Arguments: fmt.Sprintf(`{"path":%q}`, filepath.Join(alias, "memory.txt"))}); err == nil {
		t.Fatal("child environment crossed protected boundary")
	}
	data, err := os.ReadFile(filepath.Join(protected, "memory.txt"))
	if err != nil || string(data) != "DOMAIN_SECRET" {
		t.Fatalf("protected data modified: %q %v", data, err)
	}
	if _, err := os.Stat(filepath.Join(protected, "new", "memory.txt")); !os.IsNotExist(err) {
		t.Fatalf("created protected descendant: %v", err)
	}
}

func TestProtectedPathsCheckWorktreeDestination(t *testing.T) {
	root, protected := t.TempDir(), t.TempDir()
	mustWriteFile(t, filepath.Join(root, "memory.txt"), "ordinary")
	mustWriteFile(t, filepath.Join(protected, "memory.txt"), "DOMAIN_SECRET")
	kit, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	kit.SetProtectedPaths([]string{protected})
	ctx := toolctx.WithWorktreeBinding(context.Background(), root, protected)
	if out, err := kit.Execute(ctx, providers.ToolCall{Name: "read_file", Arguments: `{"path":"memory.txt"}`}); err == nil || strings.Contains(out, "DOMAIN_SECRET") {
		t.Fatalf("worktree rebasing bypass: %s %v", out, err)
	}
}

func TestProtectedPathsKeepProcessPolicyInUnconfinedMode(t *testing.T) {
	env := &Env{RootDir: t.TempDir(), Unconfined: true, ProtectedPaths: []string{t.TempDir()}}
	policy, _, err := env.processSandboxPolicyWithBuiltIn(context.Background(), true)
	if err != nil || policy == nil || len(policy.ProtectedPaths) != 1 {
		t.Fatalf("unconfined discarded protected roots: %#v %v", policy, err)
	}
	if _, _, err := env.processSandboxPolicyWithBuiltIn(context.Background(), false); err == nil {
		t.Fatal("missing sandbox allowed protected process")
	}
}

func TestProtectedPathsGuardSearchAndRevisionContent(t *testing.T) {
	root := t.TempDir()
	private := filepath.Join(root, "private")
	public := filepath.Join(root, "public")
	mustWriteFile(t, filepath.Join(private, "memory.txt"), "DOMAIN_SECRET")
	mustWriteFile(t, filepath.Join(public, "ordinary.txt"), "ordinary")
	kit, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	kit.SetProtectedPaths([]string{private})
	before := kit.env.workspaceRevision(context.Background(), root)
	mustWriteFile(t, filepath.Join(private, "memory.txt"), "CHANGED_PRIVATE_DATA")
	if after := kit.env.workspaceRevision(context.Background(), root); before != after {
		t.Fatalf("protected content affected exposed revision: %q -> %q", before, after)
	}
	for _, call := range []providers.ToolCall{
		{Name: "grep", Arguments: fmt.Sprintf(`{"path":%q,"pattern":"ordinary"}`, public)},
		{Name: "grep", Arguments: fmt.Sprintf(`{"path":%q,"pattern":"ordinary","output_mode":"files_with_matches"}`, public)},
		{Name: "grep", Arguments: fmt.Sprintf(`{"path":%q,"pattern":"ordinary","output_mode":"count"}`, public)},
		{Name: "glob", Arguments: fmt.Sprintf(`{"path":%q,"pattern":"*.txt"}`, public)},
	} {
		if out, err := kit.Execute(context.Background(), call); err != nil || !strings.Contains(out, "ordinary.txt") {
			t.Fatalf("safe search unavailable: %s %v", out, err)
		}
	}
	// A dangling alias must not make a future protected file writable.
	if err := os.Symlink(filepath.Join(private, "future"), filepath.Join(public, "dangling")); err != nil {
		t.Fatal(err)
	}
	if _, err := kit.env.ResolvePath(filepath.Join(public, "dangling", "new.txt")); err == nil {
		t.Fatal("dangling protected alias accepted")
	}
}

func TestProtectedPathsDenyUnconfinedShellWithoutSupportingBackend(t *testing.T) {
	if processsandbox.Supported() {
		t.Skip("this failure contract requires an unavailable built-in backend")
	}
	root := t.TempDir()
	private := filepath.Join(root, "private")
	secret := filepath.Join(private, "memory.txt")
	mustWriteFile(t, secret, "DOMAIN_SECRET")
	kit, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	kit.SetBoundary(UnconfinedBoundary())
	kit.SetProtectedPaths([]string{private})
	kit.SetSessionID("protected-process-test")
	kit.SetSessionDir(t.TempDir())
	for _, args := range []string{
		fmt.Sprintf(`{"command":%q}`, "cat "+secret),
		fmt.Sprintf(`{"command":%q}`, "printf overwritten > "+secret),
		fmt.Sprintf(`{"command":%q,"run_in_background":true}`, "cat "+secret),
	} {
		out, err := kit.Execute(context.Background(), providers.ToolCall{Name: "bash", Arguments: args})
		if err == nil || strings.Contains(out, "DOMAIN_SECRET") {
			t.Fatalf("shell escaped protected boundary: %s %v", out, err)
		}
	}
	if _, err := kit.Execute(context.Background(), providers.ToolCall{Name: "process", Arguments: `{"action":"list"}`}); err == nil {
		t.Fatal("unverified process policy accepted")
	}
	codeKit := newCodeModeTestToolkit(t)
	codeKit.SetProtectedPaths([]string{private})
	result := runPTCProgram(t, codeKit, fmt.Sprintf(`const result = await tools.read_file({path:%q}); text(result);`, secret))
	if !result.IsError || strings.Contains(result.TextProjection(), "DOMAIN_SECRET") {
		t.Fatalf("code mode escaped missing sandbox: %+v", result)
	}
	if data, err := os.ReadFile(secret); err != nil || string(data) != "DOMAIN_SECRET" {
		t.Fatalf("shell changed protected memory: %q %v", data, err)
	}
}

type protectedRemoteExecutor struct{ called bool }

func (e *protectedRemoteExecutor) Execute(context.Context, executionenv.ToolRequest) (toolresult.Result, error) {
	e.called = true
	return toolresult.FromText("DOMAIN_SECRET"), nil
}
func (e *protectedRemoteExecutor) RunCode(context.Context, executionenv.CodeRequest, toolctx.NestedExecutor) (codemode.RunResult, error) {
	e.called = true
	return codemode.RunResult{}, nil
}

func TestProtectedPathsRejectRemoteBypass(t *testing.T) {
	kit := newCodeModeTestToolkit(t)
	kit.SetProtectedPaths([]string{t.TempDir()})
	remote := &protectedRemoteExecutor{}
	kit.SetExecutionEnvironment(remote)
	if out, err := kit.Execute(context.Background(), providers.ToolCall{Name: "read_file", Arguments: `{"path":"ordinary.txt"}`}); err == nil || remote.called || strings.Contains(out, "DOMAIN_SECRET") {
		t.Fatalf("remote escaped protected paths: %s %v called=%t", out, err, remote.called)
	}
	result := runPTCProgram(t, kit, `const r = await tools.read_file({path:"ordinary.txt"}); text(r);`)
	if !result.IsError || remote.called {
		t.Fatalf("remote code escaped protected paths: %+v called=%t", result, remote.called)
	}
}

func TestProtectedPathsRejectBrowserBridgeBypass(t *testing.T) {
	kit, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	kit.SetProtectedPaths([]string{t.TempDir()})
	bridge := &fakeBrowserBridge{}
	kit.SetBrowserBridge(bridge)
	for _, args := range []string{`{"action":"navigate","url":"file:///private/memory"}`, `{"action":"observe","tab_id":"existing"}`, `{"action":"tabs"}`} {
		if _, err := kit.Execute(context.Background(), providers.ToolCall{Name: browserToolName, Arguments: args}); err == nil || !strings.Contains(err.Error(), "protected") {
			t.Fatalf("browser bridge bypass accepted: %v", err)
		}
		if _, err := NewBrowserTool(kit.env).ExecuteResult(context.Background(), args); err == nil || !strings.Contains(err.Error(), "protected") {
			t.Fatalf("direct browser bypass accepted: %v", err)
		}
	}
	if len(bridge.openCalls)+len(bridge.cdpCalls)+bridge.listCalls+bridge.shotCalls != 0 {
		t.Fatal("blocked browser reached external bridge")
	}
}

func TestProtectedPathsKeepProcessScratchOutsideAuthorityState(t *testing.T) {
	protected := t.TempDir()
	env := &Env{RootDir: t.TempDir(), SessionDir: filepath.Join(protected, "session"), ProtectedPaths: []string{protected}, SessionID: "protected-scratch"}
	temp, err := env.ensureProcessSandboxTempDir()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(temp) })
	if _, inside := relInsideRoot(protected, temp); inside {
		t.Fatalf("process scratch exposed protected state: %s", temp)
	}
	if info, err := os.Stat(temp); err != nil || !info.IsDir() {
		t.Fatalf("private scratch missing: %v", err)
	}
}
