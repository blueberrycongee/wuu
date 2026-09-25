package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/toolctx"
)

// newWorktreeExecFixture builds a toolkit rooted at a parent workspace plus a
// separate directory standing in for the isolated worktree checkout. This
// mirrors the fork-to-worktree safety net: the runtime is (wrongly or
// historically) rooted at the parent repo while the session metadata still
// binds the worktree, so the ctx binding must relocate execution.
func newWorktreeExecFixture(t *testing.T) (kit *Toolkit, parent, worktree string, ctx context.Context) {
	t.Helper()
	base := t.TempDir()
	parent = filepath.Join(base, "parent")
	worktree = filepath.Join(base, "state", "worktrees", "session", "fork")
	for _, dir := range []string{parent, worktree} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}
	var err error
	kit, err = New(parent)
	if err != nil {
		t.Fatalf("tools.New: %v", err)
	}
	kit.SetStateDir(filepath.Join(base, "state"))
	return kit, parent, worktree, toolctx.WithWorktreePath(context.Background(), worktree)
}

func executeToolForWorktreeTest(t *testing.T, kit *Toolkit, ctx context.Context, name, args string) string {
	t.Helper()
	out, err := executeEnvelope(kit, ctx, providers.ToolCall{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return out
}

func TestWorktreeBoundWriteLandsInWorktreeNotParent(t *testing.T) {
	kit, parent, worktree, ctx := newWorktreeExecFixture(t)

	out := executeToolForWorktreeTest(t, kit, ctx, "write_file", `{"path":"isolated.txt","content":"worktree only\n"}`)
	var result struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("unmarshal write result: %v", err)
	}
	if result.Path != "isolated.txt" {
		t.Fatalf("display path should stay workspace-relative, got %q", result.Path)
	}
	if _, err := os.Stat(filepath.Join(worktree, "isolated.txt")); err != nil {
		t.Fatalf("expected file in worktree: %v", err)
	}
	if _, err := os.Stat(filepath.Join(parent, "isolated.txt")); !os.IsNotExist(err) {
		t.Fatalf("parent repo must stay untouched, stat err=%v", err)
	}
}

func TestUnboundWriteKeepsParentBehavior(t *testing.T) {
	kit, parent, worktree, _ := newWorktreeExecFixture(t)

	executeToolForWorktreeTest(t, kit, context.Background(), "write_file", `{"path":"plain.txt","content":"parent\n"}`)
	if _, err := os.Stat(filepath.Join(parent, "plain.txt")); err != nil {
		t.Fatalf("unbound thread should keep writing to the workspace root: %v", err)
	}
	if _, err := os.Stat(filepath.Join(worktree, "plain.txt")); !os.IsNotExist(err) {
		t.Fatalf("worktree must stay untouched without ctx binding, stat err=%v", err)
	}
}

func TestWorktreeBoundReadAndEditUseWorktreeCopy(t *testing.T) {
	kit, parent, worktree, ctx := newWorktreeExecFixture(t)
	if err := os.WriteFile(filepath.Join(parent, "note.txt"), []byte("parent version\n"), 0o644); err != nil {
		t.Fatalf("seed parent: %v", err)
	}
	if err := os.WriteFile(filepath.Join(worktree, "note.txt"), []byte("worktree version\n"), 0o644); err != nil {
		t.Fatalf("seed worktree: %v", err)
	}

	out := executeToolForWorktreeTest(t, kit, ctx, "read_file", `{"path":"note.txt"}`)
	if !strings.Contains(out, "worktree version") || strings.Contains(out, "parent version") {
		t.Fatalf("read_file should see the worktree copy, got %s", out)
	}

	executeToolForWorktreeTest(t, kit, ctx, "edit_file", `{"path":"note.txt","old_text":"worktree version","new_text":"edited in worktree"}`)
	edited, err := os.ReadFile(filepath.Join(worktree, "note.txt"))
	if err != nil || !strings.Contains(string(edited), "edited in worktree") {
		t.Fatalf("edit should land in worktree, content=%q err=%v", edited, err)
	}
	parentContent, err := os.ReadFile(filepath.Join(parent, "note.txt"))
	if err != nil || string(parentContent) != "parent version\n" {
		t.Fatalf("parent copy must stay untouched, content=%q err=%v", parentContent, err)
	}
}

func TestWorktreeBoundBashRunExecutesInWorktree(t *testing.T) {
	kit, parent, worktree, ctx := newWorktreeExecFixture(t)
	enableShellExecutionForTest(kit.env)

	// Regression: BashTool.executeRun used to resolve the shell working dir
	// twice (once in executeRun, again inside executeShellCommandWithCWD). With a
	// worktree binding and an empty cwd the second resolve fed the already
	// worktree-relocated absolute path back through the sandbox rel-check and
	// falsely rejected it as escaping the workspace. Drive the full bash action
	// dispatch (kit.Execute -> executeRun) with an empty cwd and assert it runs
	// inside the worktree instead of erroring.
	out := executeToolForWorktreeTest(t, kit, ctx, "bash", `{"command":"pwd"}`)
	var result struct {
		ExitCode int    `json:"exit_code"`
		Stdout   string `json:"stdout_tail"`
	}
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("unmarshal bash result: %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("bash run should succeed inside the worktree, got exit=%d out=%s", result.ExitCode, out)
	}
	resolvedWorktree, err := filepath.EvalSymlinks(worktree)
	if err != nil {
		resolvedWorktree = worktree
	}
	if !strings.Contains(result.Stdout, resolvedWorktree) {
		t.Fatalf("bash run should execute inside the worktree %q, got %s", resolvedWorktree, result.Stdout)
	}
	resolvedParent, err := filepath.EvalSymlinks(parent)
	if err != nil {
		resolvedParent = parent
	}
	if strings.Contains(result.Stdout, resolvedParent) {
		t.Fatalf("bash run should not execute in the parent root %q, got %s", resolvedParent, result.Stdout)
	}
}

func TestWorktreeBoundSearchRootsSwitch(t *testing.T) {
	kit, parent, worktree, ctx := newWorktreeExecFixture(t)
	if err := os.WriteFile(filepath.Join(parent, "haystack.txt"), []byte("needle-parent\n"), 0o644); err != nil {
		t.Fatalf("seed parent: %v", err)
	}
	if err := os.WriteFile(filepath.Join(worktree, "haystack.txt"), []byte("needle-worktree\n"), 0o644); err != nil {
		t.Fatalf("seed worktree: %v", err)
	}

	out := executeToolForWorktreeTest(t, kit, ctx, "grep", `{"pattern":"needle-"}`)
	if !strings.Contains(out, "needle-worktree") || strings.Contains(out, "needle-parent") {
		t.Fatalf("grep should search the worktree, got %s", out)
	}

	globOut := executeToolForWorktreeTest(t, kit, ctx, "glob", `{"pattern":"*.txt"}`)
	if !strings.Contains(globOut, "haystack.txt") {
		t.Fatalf("glob should list worktree files, got %s", globOut)
	}

	listOut := executeToolForWorktreeTest(t, kit, ctx, "list_files", `{}`)
	if !strings.Contains(listOut, "haystack.txt") {
		t.Fatalf("list_files should list the worktree, got %s", listOut)
	}
}

func TestWorktreeBindingDoesNotLoosenWhitelist(t *testing.T) {
	kit, parent, worktree, ctx := newWorktreeExecFixture(t)

	// Escaping the workspace stays rejected exactly as before.
	if _, err := kit.Execute(ctx, providers.ToolCall{
		Name:      "write_file",
		Arguments: `{"path":"../escape.txt","content":"nope\n"}`,
	}); err == nil || !strings.Contains(err.Error(), "escapes workspace") {
		t.Fatalf("workspace escape must stay rejected, err=%v", err)
	}

	// Sensitive paths stay rejected: the check runs BEFORE the worktree
	// rebase, on the workspace-relative path.
	if _, err := kit.Execute(ctx, providers.ToolCall{
		Name:      "write_file",
		Arguments: `{"path":".env","content":"SECRET=1\n"}`,
	}); err == nil || !strings.Contains(err.Error(), "sensitive path") {
		t.Fatalf(".env write must stay rejected, err=%v", err)
	}

	// Directly addressing the checkout from outside the whitelist stays
	// rejected too — the binding relocates approved paths, it does not
	// whitelist the checkout.
	directArgs, err := json.Marshal(map[string]string{
		"path":    filepath.Join(worktree, "direct.txt"),
		"content": "nope\n",
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if _, err := kit.Execute(ctx, providers.ToolCall{
		Name:      "write_file",
		Arguments: string(directArgs),
	}); err == nil || !strings.Contains(err.Error(), "escapes workspace") {
		t.Fatalf("absolute worktree path must stay outside the whitelist, err=%v", err)
	}
	_ = parent
}

func TestWorktreeBoundFileScopeRootsPassThrough(t *testing.T) {
	kit, _, worktree, ctx := newWorktreeExecFixture(t)
	notebook := filepath.Join(t.TempDir(), "notebook")
	if err := os.MkdirAll(notebook, 0o755); err != nil {
		t.Fatalf("mkdir notebook: %v", err)
	}
	kit.SetFileScopeRoots([]string{kit.RootDir(), notebook})

	notePath := filepath.Join(notebook, "topic.md")
	args, err := json.Marshal(map[string]string{"path": notePath, "content": "memory\n"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	executeToolForWorktreeTest(t, kit, ctx, "write_file", string(args))
	if _, err := os.Stat(notePath); err != nil {
		t.Fatalf("whitelisted notebook write must NOT be rebased into the worktree: %v", err)
	}
	if _, err := os.Stat(filepath.Join(worktree, "topic.md")); !os.IsNotExist(err) {
		t.Fatalf("notebook write must not leak into the worktree, stat err=%v", err)
	}
}

func TestWorktreeNotReadyFailsInsteadOfParentFallback(t *testing.T) {
	kit, parent, _, _ := newWorktreeExecFixture(t)
	missing := filepath.Join(t.TempDir(), "gone", "checkout")
	ctx := toolctx.WithWorktreePath(context.Background(), missing)

	if _, err := kit.Execute(ctx, providers.ToolCall{
		Name:      "write_file",
		Arguments: `{"path":"fallback.txt","content":"nope\n"}`,
	}); err == nil || !strings.Contains(err.Error(), "not ready") {
		t.Fatalf("missing checkout must fail loudly, err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(parent, "fallback.txt")); !os.IsNotExist(err) {
		t.Fatalf("missing checkout must never fall back to the parent repo, stat err=%v", err)
	}
}

func TestWorktreeBindingEqualToRootIsNoop(t *testing.T) {
	kit, parent, _, _ := newWorktreeExecFixture(t)
	ctx := toolctx.WithWorktreePath(context.Background(), parent)

	executeToolForWorktreeTest(t, kit, ctx, "write_file", `{"path":"same-root.txt","content":"ok\n"}`)
	if _, err := os.Stat(filepath.Join(parent, "same-root.txt")); err != nil {
		t.Fatalf("binding equal to the root must behave exactly like today: %v", err)
	}
}
