package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/gitattribution"
	proc "github.com/blueberrycongee/wuu/internal/process"
	"github.com/blueberrycongee/wuu/internal/providers"
)

var sharedGitWrapperBuild struct {
	once   sync.Once
	dir    string
	binary string
	err    error
}

func TestMain(m *testing.M) {
	code := m.Run()
	if sharedGitWrapperBuild.dir != "" {
		_ = os.RemoveAll(sharedGitWrapperBuild.dir)
	}
	os.Exit(code)
}

func setupGitRepo(t *testing.T) (*Toolkit, string) {
	t.Helper()
	// Strip inherited wuu git-wrapper shim dirs from PATH. Agent shells (and
	// any process spawned from one, like `go test` run inside wuu) carry the
	// attribution wrapper first on PATH, and the structured git tool resolves
	// "git" through this process's PATH. The shim appends the co-author
	// trailer unconditionally, which breaks the attribution-disabled
	// assertions below.
	pathEntries := strings.Split(os.Getenv("PATH"), string(os.PathListSeparator))
	kept := pathEntries[:0]
	for _, entry := range pathEntries {
		if strings.Contains(entry, "git-wrapper") {
			continue
		}
		kept = append(kept, entry)
	}
	t.Setenv("PATH", strings.Join(kept, string(os.PathListSeparator)))
	root := t.TempDir()
	for _, c := range []string{
		"git init -q",
		"git config core.hooksPath .git/hooks",
		"git config user.email test@test.com",
		"git config user.name tester",
		"printf 'hello\n' > hello.txt",
		"git add hello.txt",
		"git commit -qm initial",
	} {
		cmd := exec.Command("bash", "-lc", c)
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("setup %q: %v\n%s", c, err, out)
		}
	}
	kit, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return kit, root
}

func setupGitRemoteRepo(t *testing.T) (*Toolkit, string, string) {
	t.Helper()
	kit, root := setupGitRepo(t)
	remote := filepath.Join(t.TempDir(), "remote.git")
	for _, c := range []string{
		fmt.Sprintf("git init -q --bare %q", remote),
		fmt.Sprintf("git remote add origin %q", remote),
	} {
		cmd := exec.Command("bash", "-lc", c)
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("remote setup %q: %v\n%s", c, err, out)
		}
	}
	return kit, root, remote
}

func TestGitCommitAttributionPreservesIdentityAndExistingCoauthors(t *testing.T) {
	kit, root := setupGitRepo(t)
	runBash(t, root, "printf 'updated\n' > hello.txt")
	gitCall(t, kit, "add", "hello.txt")
	gitCall(
		t,
		kit,
		"commit",
		"-m",
		"Update hello",
		"-m",
		"Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>",
	)

	identity := strings.TrimSpace(runBash(t, root, "git log -1 --format='%an <%ae>|%cn <%ce>'"))
	if identity != "tester <test@test.com>|tester <test@test.com>" {
		t.Fatalf("commit identity changed: %q", identity)
	}
	message := runBash(t, root, "git log -1 --format=%B")
	if !strings.Contains(message, "Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>") {
		t.Fatalf("existing Claude co-author missing:\n%s", message)
	}
	if strings.Count(message, gitattribution.Email) != 1 {
		t.Fatalf("WUU co-author count = %d, want 1:\n%s", strings.Count(message, gitattribution.Email), message)
	}
}

func TestBashCommitAttributionPreservesMessageFileTrailersAndDeduplicates(t *testing.T) {
	kit, root := setupGitRepo(t)
	enableShellExecutionForTest(kit.env)
	kit.env.GitWrapperExecutable = buildWuuForGitWrapper(t)
	kit.SetSessionDir(t.TempDir())
	runBash(t, root, "printf 'updated\n' > hello.txt")
	runBash(t, root, "printf 'Update hello\n\nCo-Authored-By: WUU Agent <305930189+wuu-agent[bot]@users.noreply.github.com>\nCo-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>\n' > commit-message.txt")

	args, _ := json.Marshal(map[string]any{
		"command": "git add hello.txt && git -C . commit -F commit-message.txt -- hello.txt 2>&1",
	})
	if _, err := kit.Execute(context.Background(), providers.ToolCall{
		Name:      "bash",
		Arguments: string(args),
	}); err != nil {
		t.Fatalf("bash commit: %v", err)
	}

	message := runBash(t, root, "git log -1 --format=%B")
	if !strings.Contains(message, "Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>") {
		t.Fatalf("existing Claude co-author missing:\n%s", message)
	}
	if strings.Count(message, gitattribution.Email) != 1 {
		t.Fatalf("WUU co-author count = %d, want 1:\n%s", strings.Count(message, gitattribution.Email), message)
	}
	if _, err := os.Stat(filepath.Join(root, "--trailer")); !os.IsNotExist(err) {
		t.Fatalf("redirection created a --trailer file: %v", err)
	}
}

func TestBashGitAttributionDoesNotRewriteHeredocAndCoversNestedShell(t *testing.T) {
	kit, root := setupGitRepo(t)
	enableShellExecutionForTest(kit.env)
	kit.env.GitWrapperExecutable = buildWuuForGitWrapper(t)
	kit.SetSessionDir(t.TempDir())
	script := "#!/bin/sh\nprintf 'nested\\n' > hello.txt\ngit add hello.txt\ngit commit -m 'Nested commit'\n"
	command := "cat > deploy.sh <<'EOF'\n" + script + "EOF\nsh -c 'sh deploy.sh'"
	args, _ := json.Marshal(map[string]any{"command": command})
	if _, err := kit.Execute(context.Background(), providers.ToolCall{Name: "bash", Arguments: string(args)}); err != nil {
		t.Fatalf("bash nested commit: %v", err)
	}
	written, err := os.ReadFile(filepath.Join(root, "deploy.sh"))
	if err != nil {
		t.Fatalf("read heredoc output: %v", err)
	}
	if string(written) != script {
		t.Fatalf("heredoc body was rewritten:\n%s", written)
	}
	message := runBash(t, root, "git log -1 --format=%B")
	if strings.Count(message, gitattribution.Email) != 1 {
		t.Fatalf("nested shell WUU co-author count = %d, want 1:\n%s", strings.Count(message, gitattribution.Email), message)
	}
}

func TestBashGitAttributionRejectsWrapperSelfResolution(t *testing.T) {
	kit, _ := setupGitRepo(t)
	enableShellExecutionForTest(kit.env)
	kit.env.GitWrapperExecutable = buildWuuForGitWrapper(t)
	kit.SetSessionDir(t.TempDir())
	args, _ := json.Marshal(map[string]any{
		"command":         `env -i PATH="$PATH" git status`,
		"timeout_seconds": 10,
	})
	response, err := executeEnvelope(kit, context.Background(), providers.ToolCall{Name: "bash", Arguments: string(args)})
	if err != nil {
		t.Fatalf("bash self-resolution check: %v", err)
	}
	var result shellExecutionResult
	if err := json.Unmarshal([]byte(response), &result); err != nil {
		t.Fatalf("parse bash response: %v\n%s", err, response)
	}
	if result.ExitCode != 127 || !strings.Contains(result.StderrTail, "resolved to the WUU git wrapper itself") {
		t.Fatalf("self-resolving wrapper result = %+v", result)
	}
}

func TestBashBackgroundGitAttributionUsesWrapper(t *testing.T) {
	kit, root := setupGitRepo(t)
	enableShellExecutionForTest(kit.env)
	kit.env.GitWrapperExecutable = buildWuuForGitWrapper(t)
	kit.SetSessionDir(t.TempDir())
	manager, err := proc.NewManager(root, filepath.Join(t.TempDir(), "runtime"))
	if err != nil {
		t.Fatalf("create process manager: %v", err)
	}
	defer func() { _ = manager.CleanupSession() }()
	kit.SetProcessManager(manager)
	kit.SetSessionID("thread-background-git-attribution")
	runBash(t, root, "printf 'background\\n' > hello.txt")
	args, _ := json.Marshal(map[string]any{
		"command":           "git add hello.txt && git commit -m 'Background commit'",
		"run_in_background": true,
	})
	response, err := executeEnvelope(kit, context.Background(), providers.ToolCall{Name: "bash", Arguments: string(args)})
	if err != nil {
		t.Fatalf("start background commit: %v", err)
	}
	var started proc.Process
	if err := json.Unmarshal([]byte(response), &started); err != nil {
		t.Fatalf("parse background commit response: %v\n%s", err, response)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		process, processErr := manager.Get(started.ID)
		if processErr != nil {
			t.Fatalf("check background commit status: %v", processErr)
		}
		if process.Status == proc.StatusStopped {
			break
		}
		if process.Status == proc.StatusFailed {
			t.Fatalf("background commit failed: %+v", process)
		}
		if time.Now().After(deadline) {
			t.Fatal("background commit did not complete")
		}
		time.Sleep(10 * time.Millisecond)
	}
	message := runBash(t, root, "git log -1 --format=%B")
	if strings.Count(message, gitattribution.Email) != 1 {
		t.Fatalf("background WUU co-author count = %d, want 1:\n%s", strings.Count(message, gitattribution.Email), message)
	}
}

func TestGitAttributionSkipsAmend(t *testing.T) {
	kit, root := setupGitRepo(t)
	enableShellExecutionForTest(kit.env)
	kit.env.GitWrapperExecutable = buildWuuForGitWrapper(t)
	kit.SetSessionDir(t.TempDir())
	runBash(t, root, "printf 'human\\n' > hello.txt && git add hello.txt && git commit -qm human")

	args, _ := json.Marshal(map[string]any{"command": "git commit --amend -m 'amended through bash'"})
	if _, err := kit.Execute(context.Background(), providers.ToolCall{Name: "bash", Arguments: string(args)}); err != nil {
		t.Fatalf("bash amend: %v", err)
	}
	message := runBash(t, root, "git log -1 --format=%B")
	if strings.Contains(message, gitattribution.Email) {
		t.Fatalf("bash amend added WUU attribution:\n%s", message)
	}

	gitCall(t, kit, "commit", "--amend", "-m", "amended through structured git")
	message = runBash(t, root, "git log -1 --format=%B")
	if strings.Contains(message, gitattribution.Email) {
		t.Fatalf("structured amend added WUU attribution:\n%s", message)
	}
}

func TestGitCommitRejectsCallerSuppliedTrailer(t *testing.T) {
	kit, _ := setupGitRepo(t)
	arguments, _ := json.Marshal(map[string]any{
		"subcommand": "commit",
		"args":       []string{"-m", "message", "--trailer", "Signed-off-by: Some Human <human@example.com>"},
	})
	_, err := kit.Execute(context.Background(), providers.ToolCall{Name: "git", Arguments: string(arguments)})
	if err == nil {
		t.Fatalf("expected caller-supplied trailer rejection, got %v", err)
	}
}

func TestGitCommitAttributionCanBeDisabled(t *testing.T) {
	kit, root := setupGitRepo(t)
	enableShellExecutionForTest(kit.env)
	kit.SetGitAttributionEnabled(false)
	runBash(t, root, "printf 'updated\n' > hello.txt")
	gitCall(t, kit, "add", "hello.txt")
	gitCall(t, kit, "commit", "-m", "Update hello", "-m", "Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>")

	message := runBash(t, root, "git log -1 --format=%B")
	if !strings.Contains(message, "Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>") {
		t.Fatalf("existing Claude co-author missing:\n%s", message)
	}
	if strings.Contains(message, gitattribution.Trailer) {
		t.Fatalf("disabled WUU co-author was added:\n%s", message)
	}

	runBash(t, root, "printf 'disabled bash\\n' > hello.txt")
	args, _ := json.Marshal(map[string]any{"command": "git add hello.txt && git commit -m 'Disabled bash attribution'"})
	if _, err := kit.Execute(context.Background(), providers.ToolCall{Name: "bash", Arguments: string(args)}); err != nil {
		t.Fatalf("disabled bash commit: %v", err)
	}
	message = runBash(t, root, "git log -1 --format=%B")
	if strings.Contains(message, gitattribution.Email) {
		t.Fatalf("disabled bash attribution was added:\n%s", message)
	}
}

func buildWuuForGitWrapper(t *testing.T) string {
	t.Helper()
	sharedGitWrapperBuild.once.Do(func() {
		repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
		if err != nil {
			sharedGitWrapperBuild.err = fmt.Errorf("resolve repository root: %w", err)
			return
		}
		sharedGitWrapperBuild.dir, err = os.MkdirTemp("", "wuu-git-wrapper-test-")
		if err != nil {
			sharedGitWrapperBuild.err = fmt.Errorf("create git wrapper build directory: %w", err)
			return
		}
		binaryName := "wuu"
		if runtime.GOOS == "windows" {
			binaryName += ".exe"
		}
		sharedGitWrapperBuild.binary = filepath.Join(sharedGitWrapperBuild.dir, binaryName)
		cmd := exec.Command("go", "build", "-o", sharedGitWrapperBuild.binary, "./cmd/wuu")
		cmd.Dir = repoRoot
		if output, err := cmd.CombinedOutput(); err != nil {
			sharedGitWrapperBuild.err = fmt.Errorf("build WUU git wrapper: %w\n%s", err, output)
		}
	})
	if sharedGitWrapperBuild.err != nil {
		t.Fatal(sharedGitWrapperBuild.err)
	}
	return sharedGitWrapperBuild.binary
}

func gitCall(t *testing.T, kit *Toolkit, subcmd string, args ...string) map[string]any {
	t.Helper()
	aj, _ := json.Marshal(map[string]any{"subcommand": subcmd, "args": args})
	resp, err := kit.Execute(context.Background(), providers.ToolCall{Name: "git", Arguments: string(aj)})
	if err != nil {
		t.Fatalf("git %s %v: %v", subcmd, args, err)
	}
	var p map[string]any
	if err := json.Unmarshal([]byte(resp), &p); err != nil {
		t.Fatalf("parse: %v\nraw: %s", err, resp)
	}
	return p
}

func requireGitWorkspaceRevision(t *testing.T, p map[string]any) string {
	t.Helper()
	rev, ok := p["workspace_revision"].(string)
	if !ok || rev == "" {
		t.Fatalf("git response missing workspace_revision: %+v", p)
	}
	if !strings.HasPrefix(rev, "git:") {
		t.Fatalf("workspace_revision = %q, want git: prefix", rev)
	}
	return rev
}

func requireGitAction(t *testing.T, p map[string]any, want string) {
	t.Helper()
	if got, _ := p["action"].(string); got != want {
		t.Fatalf("git action = %q, want %q in %+v", got, want, p)
	}
}

func gitErr(t *testing.T, kit *Toolkit, subcmd string, args ...string) string {
	t.Helper()
	aj, _ := json.Marshal(map[string]any{"subcommand": subcmd, "args": args})
	_, err := kit.Execute(context.Background(), providers.ToolCall{Name: "git", Arguments: string(aj)})
	if err == nil {
		t.Fatalf("expected error for git %s %v", subcmd, args)
	}
	return err.Error()
}

func runBash(t *testing.T, dir, cmdline string) string {
	t.Helper()
	cmd := exec.Command("bash", "-lc", cmdline)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("run %q: %v\n%s", cmdline, err, out)
	}
	return string(out)
}

func TestToolkit_Git_ReadOnlySubcommands(t *testing.T) {
	kit, _ := setupGitRepo(t)
	for _, sub := range []string{"log", "status", "diff", "show"} {
		p := gitCall(t, kit, sub)
		if p["exit_code"].(float64) != 0 {
			t.Errorf("git %s: exit_code=%v output=%v", sub, p["exit_code"], p["output"])
		}
		if suggestions, ok := p["next_suggestions"].([]any); !ok || len(suggestions) == 0 {
			t.Errorf("git %s missing next_suggestions: %+v", sub, p)
		}
		requireGitAction(t, p, sub)
		requireGitWorkspaceRevision(t, p)
	}
}

func TestToolkit_GitTelemetryRecordsResultActions(t *testing.T) {
	kit, _ := setupGitRepo(t)
	gitCall(t, kit, "status")
	gitCall(t, kit, "diff")
	records := kit.ToolTelemetry()
	got := make([]string, 0, len(records))
	for _, record := range records {
		got = append(got, record.Name+":"+record.ResultAction)
	}
	if strings.Join(got, ",") != "git:status,git:diff" {
		t.Fatalf("git telemetry actions = %+v", got)
	}
}

func TestToolkit_Git_BlockedSubcommands(t *testing.T) {
	kit, _ := setupGitRepo(t)
	for _, sub := range []string{"rebase", "merge", "clean", "cherry-pick", "stash pop", "stash apply", "stash drop", "stash clear"} {
		msg := gitErr(t, kit, sub)
		if !strings.Contains(msg, "not allowed") {
			t.Errorf("git %s: want 'not allowed', got: %s", sub, msg)
		}
	}
	_, err := kit.Execute(context.Background(), providers.ToolCall{Name: "git", Arguments: `{"subcommand":""}`})
	if err == nil {
		t.Fatal("empty subcommand should error")
	}
}

func TestToolkit_Git_MultiWordSubcommands(t *testing.T) {
	kit, _ := setupGitRepo(t)
	p := gitCall(t, kit, "stash list")
	requireGitAction(t, p, "stash_list")
	if p["exit_code"].(float64) != 0 {
		t.Fatalf("stash list: %v", p)
	}
	p = gitCall(t, kit, "stash", "list")
	requireGitAction(t, p, "stash_list")
	if p["exit_code"].(float64) != 0 {
		t.Fatalf("stash+list: %v", p)
	}
	p = gitCall(t, kit, "config", "--get", "user.name")
	requireGitAction(t, p, "config_get")
	if p["exit_code"].(float64) != 0 {
		t.Fatalf("config --get: %v", p)
	}
	if !strings.Contains(p["output"].(string), "tester") {
		t.Errorf("user.name: got %q", p["output"])
	}
	p = gitCall(t, kit, "worktree list")
	requireGitAction(t, p, "worktree_list")
	if p["exit_code"].(float64) != 0 {
		t.Fatalf("worktree list: %v", p)
	}
}

func TestToolkit_Git_CommitAllowedOnStagedChanges(t *testing.T) {
	kit, root := setupGitRepo(t)
	runBash(t, root, "printf 'next\n' > staged.txt && git add staged.txt")
	p := gitCall(t, kit, "commit", "-m", "Add staged file")
	if p["exit_code"].(float64) != 0 {
		t.Fatalf("commit: %v", p)
	}
	if sha, _ := p["commit_sha"].(string); len(sha) != 40 {
		t.Fatalf("commit response missing full commit sha: %+v", p)
	}
	if subject, _ := p["commit_subject"].(string); subject != "Add staged file" {
		t.Fatalf("commit response subject = %q, want Add staged file", subject)
	}
	requireGitWorkspaceRevision(t, p)
	log := runBash(t, root, "git log -1 --format=%s")
	if strings.TrimSpace(log) != "Add staged file" {
		t.Fatalf("unexpected commit message: %q", log)
	}
}

func TestToolkit_Git_AddStagesExplicitPaths(t *testing.T) {
	kit, root := setupGitRepo(t)
	runBash(t, root, "printf 'dirty\n' >> hello.txt")
	runBash(t, root, "printf 'new\n' > new.txt")

	p := gitCall(t, kit, "add", "hello.txt", "new.txt")
	requireGitAction(t, p, "add")
	if p["exit_code"].(float64) != 0 {
		t.Fatalf("git add: %+v", p)
	}
	requireGitWorkspaceRevision(t, p)
	staged, ok := p["staged"].([]any)
	if !ok || len(staged) != 2 {
		t.Fatalf("git add response should include staged snapshot, got: %+v", p)
	}
	if got := strings.Fields(runBash(t, root, "git diff --cached --name-only")); strings.Join(got, ",") != "hello.txt,new.txt" {
		t.Fatalf("staged files = %+v, want hello.txt,new.txt", got)
	}
}

func TestToolkit_Git_AddAcceptsLiteralPathCharacters(t *testing.T) {
	kit, root := setupGitRepo(t)
	runBash(t, root, "printf 'new\n' > 'hello (copy).txt'")

	p := gitCall(t, kit, "add", "hello (copy).txt")
	if p["exit_code"].(float64) != 0 {
		t.Fatalf("git add literal path: %+v", p)
	}
	if got := strings.TrimSpace(runBash(t, root, "git diff --cached --name-only")); got != "hello (copy).txt" {
		t.Fatalf("staged file = %q, want literal path", got)
	}
}

func TestToolkit_Git_AddRejectsBroadOrMagicPathspecs(t *testing.T) {
	kit, _ := setupGitRepo(t)
	for _, args := range [][]string{
		{"."},
		{"./"},
		{"../outside.txt"},
		{"*.go"},
		{":(glob)*.go"},
		{"-A"},
	} {
		msg := gitErr(t, kit, "add", args...)
		if !strings.Contains(msg, "explicit") &&
			!strings.Contains(msg, "literal") &&
			!strings.Contains(msg, "workspace") &&
			!strings.Contains(msg, "metacharacter") {
			t.Fatalf("git add %v should reject broad/magic pathspec, got: %s", args, msg)
		}
	}
}

func TestToolkit_Git_AddRejectsSensitivePathsBeforeStaging(t *testing.T) {
	kit, root := setupGitRepo(t)
	runBash(t, root, "mkdir -p config && printf 'API_KEY=secret-value\n' > config/.env")

	msg := gitErr(t, kit, "add", "config")
	if !strings.Contains(msg, "sensitive path") || !strings.Contains(msg, "explicit secret handling") {
		t.Fatalf("expected sensitive path staging guidance, got: %q", msg)
	}
	if strings.Contains(msg, "secret-value") {
		t.Fatalf("git add sensitive path error leaked file content: %q", msg)
	}
	if got := strings.TrimSpace(runBash(t, root, "git diff --cached --name-only")); got != "" {
		t.Fatalf("sensitive file should not be staged, staged: %q", got)
	}
}

func TestToolkit_Git_RestoreStagedUnstagesExplicitPaths(t *testing.T) {
	kit, root := setupGitRepo(t)
	runBash(t, root, "printf 'dirty\n' >> hello.txt && git add hello.txt")

	p := gitCall(t, kit, "restore --staged", "hello.txt")
	requireGitAction(t, p, "restore_staged")
	if p["exit_code"].(float64) != 0 {
		t.Fatalf("git restore --staged: %+v", p)
	}
	staged, ok := p["staged"].([]any)
	if !ok || len(staged) != 0 {
		t.Fatalf("restore --staged response should have no staged files: %+v", p)
	}
	unstaged, ok := p["unstaged"].([]any)
	if !ok || len(unstaged) != 1 {
		t.Fatalf("restore --staged response should expose unstaged file: %+v", p)
	}
	if got := strings.TrimSpace(runBash(t, root, "git diff --cached --name-only")); got != "" {
		t.Fatalf("file should be unstaged, staged: %q", got)
	}
}

func TestToolkit_Git_CommitRejectsSensitiveStagedPaths(t *testing.T) {
	kit, root := setupGitRepo(t)
	runBash(t, root, "printf 'API_KEY=staged-secret-value\n' > .env && git add .env")

	msg := gitErr(t, kit, "commit", "-m", "Add env")
	if !strings.Contains(msg, "staged sensitive path") || !strings.Contains(msg, "explicit secret handling") {
		t.Fatalf("expected sensitive staged path guidance, got: %q", msg)
	}
	if strings.Contains(msg, "staged-secret-value") {
		t.Fatalf("commit sensitive path error leaked file content: %q", msg)
	}
	log := runBash(t, root, "git log --format=%s")
	if strings.Contains(log, "Add env") {
		t.Fatalf("sensitive staged path was committed")
	}
}

func TestToolkit_Git_UnconfinedStillRejectsSensitiveStageAndCommit(t *testing.T) {
	kit, root := setupGitRepo(t)
	kit.SetBoundary(UnconfinedBoundary())
	runBash(t, root, "printf 'API_KEY=full-access-secret\n' > .env")

	// Unconfined lifts the path boundary but not secret staging guards.
	msg := gitErr(t, kit, "add", ".env")
	if !strings.Contains(msg, "sensitive path") || !strings.Contains(msg, "explicit secret handling") {
		t.Fatalf("expected sensitive path staging guidance in unconfined mode, got: %q", msg)
	}
	if got := strings.TrimSpace(runBash(t, root, "git diff --cached --name-only")); got != "" {
		t.Fatalf("sensitive file should not be staged in unconfined mode, staged: %q", got)
	}

	// Even when the sensitive file was staged outside the tool, commit
	// must refuse it in unconfined mode too.
	runBash(t, root, "git add .env")
	msg = gitErr(t, kit, "commit", "-m", "Add env")
	if !strings.Contains(msg, "staged sensitive path") {
		t.Fatalf("expected staged sensitive path refusal in unconfined mode, got: %q", msg)
	}
	if strings.Contains(msg, "full-access-secret") {
		t.Fatalf("unconfined commit refusal leaked file content: %q", msg)
	}
}

func TestToolkit_Git_CommitRejectedFlags(t *testing.T) {
	kit, _ := setupGitRepo(t)
	for _, args := range [][]string{
		{"--no-verify", "-m", "x"},
		{"--allow-empty", "-m", "x"},
		{"-e", "-m", "x"},
	} {
		msg := gitErr(t, kit, "commit", args...)
		if !strings.Contains(msg, "not allowed") {
			t.Errorf("commit args %v: want restricted error, got %s", args, msg)
		}
	}
}

func TestToolkit_Git_CommitAllowsNormalMessageForms(t *testing.T) {
	kit, root := setupGitRepo(t)
	runBash(t, root, "printf 'next\n' > staged.txt && git add staged.txt")

	p := gitCall(t, kit, "commit",
		"-m", "Subject with spaces",
		"-m", "Body mentions \"quoted text\" and shell-looking text $(ignored); still a message.",
	)
	if p["exit_code"].(float64) != 0 {
		t.Fatalf("commit with repeated -m failed: %+v", p)
	}
	if log := runBash(t, root, "git log -1 --format=%B"); !strings.Contains(log, "Subject with spaces") || !strings.Contains(log, "shell-looking text") {
		t.Fatalf("unexpected commit message:\n%s", log)
	}
}

func TestToolkit_Git_CommitAllowsMessageFileAndAmend(t *testing.T) {
	kit, root := setupGitRepo(t)
	runBash(t, root, "printf 'From message file\n\nBody\n' > msg.txt")
	runBash(t, root, "printf 'next\n' > staged.txt && git add staged.txt")

	p := gitCall(t, kit, "commit", "-F", "msg.txt")
	if p["exit_code"].(float64) != 0 {
		t.Fatalf("commit with -F failed: %+v", p)
	}
	if log := runBash(t, root, "git log -1 --format=%B"); !strings.Contains(log, "From message file") {
		t.Fatalf("unexpected commit message from file:\n%s", log)
	}

	p = gitCall(t, kit, "commit", "--amend", "-m", "Amended subject")
	if p["exit_code"].(float64) != 0 {
		t.Fatalf("commit --amend -m failed: %+v", p)
	}
	if log := runBash(t, root, "git log -1 --format=%s"); strings.TrimSpace(log) != "Amended subject" {
		t.Fatalf("unexpected amended subject: %q", log)
	}
}

func TestToolkit_Git_CommitRejectsUnsafeMessageFile(t *testing.T) {
	kit, _ := setupGitRepo(t)
	for _, args := range [][]string{
		{"-F", "-"},
		{"-F", "../msg.txt"},
		{"--file=.env"},
	} {
		msg := gitErr(t, kit, "commit", args...)
		if !strings.Contains(msg, "message") && !strings.Contains(msg, "secret") {
			t.Errorf("commit args %v: want message-file restriction, got %s", args, msg)
		}
	}
}

func TestToolkit_Git_PushValidation(t *testing.T) {
	kit, root, _ := setupGitRemoteRepo(t)

	p := gitCall(t, kit, "push")
	if p["exit_code"].(float64) == 0 {
		t.Fatalf("plain push without upstream should fail at runtime in fresh repo: %v", p)
	}

	branch := strings.TrimSpace(runBash(t, root, "git rev-parse --abbrev-ref HEAD"))
	p = gitCall(t, kit, "push", "-u", "origin", branch)
	if p["exit_code"].(float64) != 0 {
		t.Fatalf("push -u origin branch should be allowed and succeed: %v", p)
	}

	msg := gitErr(t, kit, "push", "--force")
	if !strings.Contains(msg, "not allowed") {
		t.Fatalf("push --force: got %s", msg)
	}
	msg = gitErr(t, kit, "push", "origin", "otherbranch")
	if !strings.Contains(msg, "only supports") && !strings.Contains(msg, "only allows") {
		t.Fatalf("push origin otherbranch: got %s", msg)
	}
}

func TestToolkit_Git_BlockedArgs(t *testing.T) {
	kit, _ := setupGitRepo(t)
	for _, a := range [][]string{{"--config-env", "x=y"}, {"--exec-path"}, {"--no-index"}} {
		msg := gitErr(t, kit, "log", a...)
		if msg == "" {
			t.Errorf("expected blocked arg error for %v", a)
		}
	}
	if msg := gitErr(t, kit, "log", "echo;rm"); msg == "" {
		t.Error("expected metachar error")
	}
}

func TestToolkit_Git_RedactsSensitiveDiffContent(t *testing.T) {
	kit, root := setupGitRepo(t)
	runBash(t, root, "printf 'API_KEY=old-secret-value\n' > .env && git add .env && git commit -qm env")
	runBash(t, root, "printf 'API_KEY=new-secret-value\n' > .env")

	p := gitCall(t, kit, "diff")
	output := p["output"].(string)
	if strings.Contains(output, "old-secret-value") || strings.Contains(output, "new-secret-value") {
		t.Fatalf("git diff leaked sensitive file content: %q", output)
	}
	if !strings.Contains(output, "REDACTED git diff") || p["redacted"] != true {
		t.Fatalf("git diff should report redacted sensitive content: %+v", p)
	}
}

func TestToolkit_Git_UnconfinedStillRedactsSensitiveDiffContent(t *testing.T) {
	kit, root := setupGitRepo(t)
	kit.SetBoundary(UnconfinedBoundary())
	runBash(t, root, "printf 'API_KEY=old-secret-value\n' > .env && git add .env && git commit -qm env")
	runBash(t, root, "printf 'API_KEY=new-secret-value\n' > .env")

	// Unconfined lifts the path boundary but not diff redaction.
	p := gitCall(t, kit, "diff")
	output := p["output"].(string)
	if strings.Contains(output, "old-secret-value") || strings.Contains(output, "new-secret-value") {
		t.Fatalf("unconfined git diff leaked sensitive file content: %q", output)
	}
	if !strings.Contains(output, "REDACTED git diff") || p["redacted"] != true {
		t.Fatalf("unconfined git diff should report redacted sensitive content: %+v", p)
	}
}

func TestToolkit_Git_RejectsSensitiveObjectPath(t *testing.T) {
	kit, root := setupGitRepo(t)
	runBash(t, root, "printf 'API_KEY=old-secret-value\n' > .env && git add .env && git commit -qm env")

	msg := gitErr(t, kit, "show", "HEAD:.env")
	if !strings.Contains(msg, "sensitive path") || strings.Contains(msg, "old-secret-value") {
		t.Fatalf("git show sensitive object path guidance/leak mismatch: %q", msg)
	}
	msg = gitErr(t, kit, "cat-file", "-p", "HEAD:.env")
	if !strings.Contains(msg, "cat-file") || !strings.Contains(msg, "metadata modes") {
		t.Fatalf("git cat-file content mode should be blocked, got: %q", msg)
	}
}

func TestToolkit_Git_RedactsSensitiveGrepLines(t *testing.T) {
	kit, root := setupGitRepo(t)
	runBash(t, root, "printf 'API_KEY=old-secret-value\n' > .env && git add .env && git commit -qm env")

	p := gitCall(t, kit, "grep", "API_KEY")
	output := p["output"].(string)
	if strings.Contains(output, "old-secret-value") {
		t.Fatalf("git grep leaked sensitive file content: %q", output)
	}
	if !strings.Contains(output, "REDACTED git grep") || p["redacted"] != true {
		t.Fatalf("git grep should report redacted sensitive content: %+v", p)
	}
}

func TestToolkit_Git_RedactsCredentialsInOutput(t *testing.T) {
	kit, root, _ := setupGitRemoteRepo(t)
	runBash(t, root, "git remote set-url origin https://user:ghp_secret_token@example.com/repo.git")
	runBash(t, root, "git config http.extraHeader 'Authorization: Bearer real-bearer-token'")

	p := gitCall(t, kit, "remote", "-v")
	output := p["output"].(string)
	if strings.Contains(output, "ghp_secret_token") {
		t.Fatalf("git remote leaked credential: %q", output)
	}
	if !strings.Contains(output, "[REDACTED]") || p["redacted"] != true {
		t.Fatalf("git remote should redact credential-bearing URL: %+v", p)
	}

	p = gitCall(t, kit, "config", "--get", "remote.origin.url")
	output = p["output"].(string)
	if strings.Contains(output, "ghp_secret_token") || !strings.Contains(output, "[REDACTED]") {
		t.Fatalf("git config did not redact credential-bearing URL: %+v", p)
	}

	p = gitCall(t, kit, "config", "--get", "http.extraHeader")
	output = p["output"].(string)
	if strings.Contains(output, "real-bearer-token") || !strings.Contains(output, "[REDACTED]") {
		t.Fatalf("git config did not redact bearer header: %+v", p)
	}
}

// ── branch policy tests ──────────────────────────────────────────

func TestToolkit_Git_BranchPolicyAllowed(t *testing.T) {
	kit, _ := setupGitRepo(t)
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"bare list", nil},
		{"-a", []string{"-a"}},
		{"--list", []string{"--list"}},
		{"-v", []string{"-v"}},
		{"--show-current", []string{"--show-current"}},
		{"--contains HEAD", []string{"--contains", "HEAD"}},
		{"--sort=-refname", []string{"--sort=-refname"}},
		{"-l pattern", []string{"-l", "ma*"}},
		{"combined -avl", []string{"-avl"}},
		{"--merged", []string{"--merged"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := gitCall(t, kit, "branch", tc.args...)
			if p["exit_code"].(float64) != 0 {
				t.Errorf("git branch %v: exit_code=%v output=%v", tc.args, p["exit_code"], p["output"])
			}
		})
	}
}

func TestToolkit_Git_BranchPolicyBlocked(t *testing.T) {
	kit, _ := setupGitRepo(t)
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"create branch", []string{"newbranch"}},
		{"-D main", []string{"-D", "main"}},
		{"-d feature", []string{"-d", "feature"}},
		{"-m newname", []string{"-m", "newname"}},
		{"-- -l (create)", []string{"--", "-l"}},
		{"-f main HEAD~1", []string{"-f", "main", "HEAD~1"}},
		{"--set-upstream-to", []string{"--set-upstream-to=origin/main"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			msg := gitErr(t, kit, "branch", tc.args...)
			if !strings.Contains(msg, "not allowed") {
				t.Errorf("git branch %v: want 'not allowed', got: %s", tc.args, msg)
			}
		})
	}
}

// ── tag policy tests ─────────────────────────────────────────────

func TestToolkit_Git_TagPolicyAllowed(t *testing.T) {
	kit, _ := setupGitRepo(t)
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"bare list", nil},
		{"-l", []string{"-l"}},
		{"-l pattern", []string{"-l", "v*"}},
		{"--contains HEAD", []string{"--contains", "HEAD"}},
		{"--sort=-version:refname", []string{"--sort=-version:refname"}},
		{"-li (bundle)", []string{"-li"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := gitCall(t, kit, "tag", tc.args...)
			if p["exit_code"].(float64) != 0 {
				t.Errorf("git tag %v: exit_code=%v output=%v", tc.args, p["exit_code"], p["output"])
			}
		})
	}
}

func TestToolkit_Git_TagPolicyBlocked(t *testing.T) {
	kit, _ := setupGitRepo(t)
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"create tag", []string{"v1.0"}},
		{"-d v1.0", []string{"-d", "v1.0"}},
		{"-a v1.0 -m release", []string{"-a", "v1.0", "-m", "release"}},
		{"-- -l (create)", []string{"--", "-l"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			msg := gitErr(t, kit, "tag", tc.args...)
			if !strings.Contains(msg, "not allowed") {
				t.Errorf("git tag %v: want 'not allowed', got: %s", tc.args, msg)
			}
		})
	}
}

// ── remote policy tests ──────────────────────────────────────────

func TestToolkit_Git_RemotePolicyAllowed(t *testing.T) {
	kit, _, _ := setupGitRemoteRepo(t)
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"bare list", nil},
		{"-v", []string{"-v"}},
		{"--verbose", []string{"--verbose"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := gitCall(t, kit, "remote", tc.args...)
			if p["exit_code"].(float64) != 0 {
				t.Errorf("git remote %v: exit_code=%v output=%v", tc.args, p["exit_code"], p["output"])
			}
		})
	}
}

func TestToolkit_Git_RemotePolicyBlocked(t *testing.T) {
	kit, _, _ := setupGitRemoteRepo(t)
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"add evil", []string{"add", "evil", "http://evil.com"}},
		{"rename origin", []string{"rename", "origin", "upstream"}},
		{"remove origin", []string{"remove", "origin"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			msg := gitErr(t, kit, "remote", tc.args...)
			if !strings.Contains(msg, "not allowed") {
				t.Errorf("git remote %v: want 'not allowed', got: %s", tc.args, msg)
			}
		})
	}
}

func TestToolkit_Git_RemoteShowAllowed(t *testing.T) {
	kit, _, _ := setupGitRemoteRepo(t)
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"show origin", []string{"origin"}},
		{"show -n origin", []string{"-n", "origin"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := gitCall(t, kit, "remote show", tc.args...)
			if p["exit_code"].(float64) != 0 {
				t.Errorf("git remote show %v: exit_code=%v output=%v", tc.args, p["exit_code"], p["output"])
			}
		})
	}
}

func TestToolkit_Git_RemoteShowBlocked(t *testing.T) {
	kit, _, _ := setupGitRemoteRepo(t)
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"no remote name", nil},
		{"two positional", []string{"origin", "extra"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			msg := gitErr(t, kit, "remote show", tc.args...)
			if !strings.Contains(msg, "not allowed") {
				t.Errorf("git remote show %v: want 'not allowed', got: %s", tc.args, msg)
			}
		})
	}
}

// ── config policy tests ──────────────────────────────────────────

func TestToolkit_Git_ConfigReadOnly(t *testing.T) {
	kit, _ := setupGitRepo(t)

	// config --get user.name (existing behavior, now via multi-word match)
	p := gitCall(t, kit, "config", "--get", "user.name")
	if p["exit_code"].(float64) != 0 {
		t.Fatalf("config --get: %v", p)
	}
	if !strings.Contains(p["output"].(string), "tester") {
		t.Errorf("user.name: got %q", p["output"])
	}

	// config --list
	p = gitCall(t, kit, "config", "--list")
	if p["exit_code"].(float64) != 0 {
		t.Fatalf("config --list: %v", p)
	}

	// config --get-all user.name
	p = gitCall(t, kit, "config", "--get-all", "user.name")
	if p["exit_code"].(float64) != 0 {
		t.Fatalf("config --get-all: %v", p)
	}
}

func TestToolkit_Git_ConfigWriteBlocked(t *testing.T) {
	kit, _ := setupGitRepo(t)

	// bare config user.name "hacker" — config alone is not in either map
	msg := gitErr(t, kit, "config", "user.name", "hacker")
	if !strings.Contains(msg, "not allowed") {
		t.Errorf("bare config write: want 'not allowed', got: %s", msg)
	}

	// config --unset user.name — config --unset is not a known multi-word sub
	msg = gitErr(t, kit, "config", "--unset", "user.name")
	if !strings.Contains(msg, "not allowed") {
		t.Errorf("config --unset: want 'not allowed', got: %s", msg)
	}
}

// ── structured status tests ──────────────────────────────────────

func setupGitStatusRepo(t *testing.T) (*Toolkit, string) {
	t.Helper()
	// Timed-out telemetry status probes must not leave optional index locks.
	t.Setenv("GIT_OPTIONAL_LOCKS", "0")
	kit, root := setupGitRepo(t)
	runGitFixture(t, root, "config", "status.renames", "true")
	return kit, root
}

func runGitFixture(t *testing.T, root string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %q: %v\n%s", args, err, out)
	}
	return string(out)
}

func requireGitStatusPaths(t *testing.T, result map[string]any, staged, unstaged map[string]string, untracked []string) {
	t.Helper()
	if result["exit_code"] != float64(0) {
		t.Fatalf("git command failed: %+v", result)
	}
	for key, want := range map[string]map[string]string{"staged": staged, "unstaged": unstaged} {
		entries, ok := result[key].([]any)
		if !ok {
			t.Fatalf("missing %s snapshot: %+v", key, result)
		}
		got := make(map[string]string)
		for _, raw := range entries {
			entry := raw.(map[string]any)
			got[entry["file"].(string)] = entry["status"].(string)
		}
		if len(entries) != len(want) || (len(want) > 0 && !reflect.DeepEqual(got, want)) {
			t.Errorf("%s = %q, want %q", key, got, want)
		}
	}
	entries, ok := result["untracked"].([]any)
	if !ok {
		t.Fatalf("missing untracked snapshot: %+v", result)
	}
	got := make([]string, 0, len(entries))
	for _, entry := range entries {
		got = append(got, entry.(string))
	}
	slices.Sort(got)
	want := slices.Clone(untracked)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("untracked = %q, want %q", got, want)
	}
}

func TestToolkit_Git_StatusPathsRoundTrip(t *testing.T) {
	for _, quotePath := range []string{"true", "false"} {
		t.Run("quotePath="+quotePath, func(t *testing.T) {
			for _, name := range []string{
				"中文.txt", "hello world.txt", " leading.txt", "trailing.txt ",
				"double\"quote.txt", "single'quote.txt", "back\\slash.txt", "line\nbreak.txt", "tab\tname.txt",
			} {
				t.Run(name, func(t *testing.T) {
					if runtime.GOOS == "windows" && (strings.ContainsAny(name, "\"\\\n\t") || strings.HasSuffix(name, " ")) {
						t.Skip("filename is not representable on Windows")
					}
					kit, root := setupGitStatusRepo(t)
					runGitFixture(t, root, "config", "core.quotePath", quotePath)
					runBash(t, root, "printf 'unrelated change\\n' >> hello.txt")
					if err := os.WriteFile(filepath.Join(root, name), []byte("new\n"), 0600); err != nil {
						t.Fatal(err)
					}
					status := gitCall(t, kit, "status")
					requireGitStatusPaths(t, status, nil, map[string]string{"hello.txt": "modified"}, []string{name})
					returned := status["untracked"].([]any)[0].(string)
					added := gitCall(t, kit, "add", returned)
					requireGitStatusPaths(t, added, map[string]string{name: "added"}, map[string]string{"hello.txt": "modified"}, nil)
					if err := os.WriteFile(filepath.Join(root, name), []byte("updated\n"), 0600); err != nil {
						t.Fatal(err)
					}
					status = gitCall(t, kit, "status")
					requireGitStatusPaths(t, status, map[string]string{name: "added"}, map[string]string{"hello.txt": "modified", name: "modified"}, nil)
					returned = added["staged"].([]any)[0].(map[string]any)["file"].(string)
					restored := gitCall(t, kit, "restore --staged", returned)
					requireGitStatusPaths(t, restored, nil, map[string]string{"hello.txt": "modified"}, []string{name})
					if got := runGitFixture(t, root, "diff", "--cached", "--name-only", "-z"); got != "" {
						t.Errorf("unexpected staged paths: %q", got)
					}
					if got, err := os.ReadFile(filepath.Join(root, name)); err != nil || string(got) != "updated\n" {
						t.Errorf("working file changed: %q, %v", got, err)
					}
				})
			}
		})
	}
}

func TestToolkit_Git_StatusRenamePathsRoundTrip(t *testing.T) {
	kit, root := setupGitStatusRepo(t)
	const source = "old 中文 name.txt"
	const destination = "new 中文 name.txt"
	runGitFixture(t, root, "config", "core.quotePath", "true")
	runGitFixture(t, root, "mv", "hello.txt", source)
	runGitFixture(t, root, "commit", "-qm", "Prepare rename source")
	runGitFixture(t, root, "mv", source, destination)
	if err := os.WriteFile(filepath.Join(root, destination), []byte("modified after rename\n"), 0600); err != nil {
		t.Fatal(err)
	}
	status := gitCall(t, kit, "status")
	requireGitStatusPaths(t, status, map[string]string{destination: "renamed"}, map[string]string{destination: "modified"}, nil)
	returned := status["unstaged"].([]any)[0].(map[string]any)["file"].(string)
	added := gitCall(t, kit, "add", returned)
	requireGitStatusPaths(t, added, map[string]string{source: "deleted", destination: "added"}, nil, nil)
	// Restore the original content so Git still recognizes the rename.
	if err := os.WriteFile(filepath.Join(root, destination), []byte("hello\n"), 0600); err != nil {
		t.Fatal(err)
	}
	added = gitCall(t, kit, "add", returned)
	requireGitStatusPaths(t, added, map[string]string{destination: "renamed"}, nil, nil)
	returned = added["staged"].([]any)[0].(map[string]any)["file"].(string)
	restored := gitCall(t, kit, "restore --staged", returned)
	requireGitStatusPaths(t, restored, map[string]string{source: "deleted"}, nil, []string{destination})
	if got := runGitFixture(t, root, "diff", "--cached", "--name-only", "-z"); got != source+"\x00" {
		t.Errorf("unstaging the destination changed another path: %q", got)
	}
}

func TestToolkit_Git_BackslashPathsStayLiteral(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("backslash is a directory separator on Windows")
	}
	kit, root := setupGitStatusRepo(t)
	const name = "back\\slash.txt"
	const other = "backslash.txt"
	for _, file := range []string{name, other} {
		if err := os.WriteFile(filepath.Join(root, file), []byte("new\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	added := gitCall(t, kit, "add", name)
	requireGitStatusPaths(t, added, map[string]string{name: "added"}, nil, []string{other})
	runGitFixture(t, root, "add", "--", other)
	restored := gitCall(t, kit, "restore --staged", name)
	requireGitStatusPaths(t, restored, map[string]string{other: "added"}, nil, []string{name})
	if got := runGitFixture(t, root, "diff", "--cached", "--name-only", "-z"); got != other+"\x00" {
		t.Errorf("unstaging a backslash path changed another path: %q", got)
	}
}

func TestToolkit_Git_StatusPathsKeepSensitiveGuards(t *testing.T) {
	for _, unconfined := range []bool{false, true} {
		t.Run(fmt.Sprintf("unconfined=%t", unconfined), func(t *testing.T) {
			kit, root := setupGitStatusRepo(t)
			if unconfined {
				kit.SetBoundary(UnconfinedBoundary())
			}
			const directory = "配置 files"
			const sensitive = directory + "/.env"
			if err := os.Mkdir(filepath.Join(root, directory), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, sensitive), []byte("API_KEY=fixture-value\n"), 0600); err != nil {
				t.Fatal(err)
			}
			status := gitCall(t, kit, "status")
			requireGitStatusPaths(t, status, nil, nil, []string{directory + "/"})
			returned := status["untracked"].([]any)[0].(string)
			if msg := gitErr(t, kit, "add", returned); !strings.Contains(msg, "sensitive path") {
				t.Fatalf("sensitive directory accepted: %s", msg)
			}
			if got := runGitFixture(t, root, "diff", "--cached", "--name-only", "-z"); got != "" {
				t.Fatalf("rejected add changed index: %q", got)
			}
			runGitFixture(t, root, "add", "--", sensitive)
			status = gitCall(t, kit, "status")
			requireGitStatusPaths(t, status, map[string]string{sensitive: "added"}, nil, nil)
			if msg := gitErr(t, kit, "commit", "-m", "Must be rejected"); !strings.Contains(msg, "staged sensitive path") {
				t.Fatalf("sensitive commit accepted: %s", msg)
			}
			returned = status["staged"].([]any)[0].(map[string]any)["file"].(string)
			restored := gitCall(t, kit, "restore --staged", returned)
			requireGitStatusPaths(t, restored, nil, nil, []string{directory + "/"})
			// The source of a staged rename must still participate in the guard.
			runGitFixture(t, root, "add", "--", sensitive)
			runGitFixture(t, root, "commit", "-qm", "Prepare sensitive rename fixture")
			runGitFixture(t, root, "mv", sensitive, directory+"/public.txt")
			before := runGitFixture(t, root, "diff", "--cached", "--raw", "-z")
			if msg := gitErr(t, kit, "add", directory); !strings.Contains(msg, "sensitive path") {
				t.Fatalf("sensitive rename source accepted: %s", msg)
			}
			if after := runGitFixture(t, root, "diff", "--cached", "--raw", "-z"); after != before {
				t.Fatalf("rejected add changed staged rename: %q", after)
			}
		})
	}
}

func TestToolkit_Git_StatusStructured(t *testing.T) {
	kit, root := setupGitRepo(t)

	// Create a mix of staged, unstaged, and untracked changes.
	runBash(t, root, "printf 'modified\n' > hello.txt && git add hello.txt")
	runBash(t, root, "printf 'more\n' >> hello.txt") // now both staged and unstaged
	runBash(t, root, "printf 'new\n' > untracked.txt")
	runBash(t, root, "printf 'added\n' > added.txt && git add added.txt")

	p := gitCall(t, kit, "status")
	if p["exit_code"].(float64) != 0 {
		t.Fatalf("status: %v", p)
	}

	// Check staged
	staged, ok := p["staged"].([]any)
	if !ok {
		t.Fatalf("staged not an array: %T", p["staged"])
	}
	stagedFiles := make(map[string]string)
	for _, e := range staged {
		entry := e.(map[string]any)
		stagedFiles[entry["file"].(string)] = entry["status"].(string)
	}
	if stagedFiles["hello.txt"] != "modified" {
		t.Errorf("staged hello.txt: want modified, got %q", stagedFiles["hello.txt"])
	}
	if stagedFiles["added.txt"] != "added" {
		t.Errorf("staged added.txt: want added, got %q", stagedFiles["added.txt"])
	}

	// Check unstaged
	unstaged, ok := p["unstaged"].([]any)
	if !ok {
		t.Fatalf("unstaged not an array: %T", p["unstaged"])
	}
	unstagedFiles := make(map[string]string)
	for _, e := range unstaged {
		entry := e.(map[string]any)
		unstagedFiles[entry["file"].(string)] = entry["status"].(string)
	}
	if unstagedFiles["hello.txt"] != "modified" {
		t.Errorf("unstaged hello.txt: want modified, got %q", unstagedFiles["hello.txt"])
	}

	// Check untracked
	untracked, ok := p["untracked"].([]any)
	if !ok {
		t.Fatalf("untracked not an array: %T", p["untracked"])
	}
	found := false
	for _, u := range untracked {
		if u.(string) == "untracked.txt" {
			found = true
		}
	}
	if !found {
		t.Errorf("untracked should contain untracked.txt, got %v", untracked)
	}

	// Raw output should be present
	if p["output"].(string) == "" {
		t.Error("output should not be empty")
	}
}

func TestToolkit_Git_StatusCleanRepo(t *testing.T) {
	kit, _ := setupGitRepo(t)
	p := gitCall(t, kit, "status")
	if p["exit_code"].(float64) != 0 {
		t.Fatalf("status: %v", p)
	}
	staged := p["staged"].([]any)
	unstaged := p["unstaged"].([]any)
	untracked := p["untracked"].([]any)
	if len(staged) != 0 || len(unstaged) != 0 || len(untracked) != 0 {
		t.Errorf("clean repo should have empty arrays, got staged=%d unstaged=%d untracked=%d",
			len(staged), len(unstaged), len(untracked))
	}
}

func TestParseGitPorcelain(t *testing.T) {
	for _, tc := range []struct {
		name      string
		input     string
		staged    []fileEntry
		unstaged  []fileEntry
		untracked []string
	}{
		{
			name:      "empty",
			input:     "",
			staged:    []fileEntry{},
			unstaged:  []fileEntry{},
			untracked: []string{},
		},
		{
			name:      "staged modified",
			input:     "M  foo.go\x00",
			staged:    []fileEntry{{File: "foo.go", Status: "modified"}},
			unstaged:  []fileEntry{},
			untracked: []string{},
		},
		{
			name:      "unstaged modified",
			input:     " M bar.go\x00",
			staged:    []fileEntry{},
			unstaged:  []fileEntry{{File: "bar.go", Status: "modified"}},
			untracked: []string{},
		},
		{
			name:      "both staged and unstaged",
			input:     "MM baz.go\x00",
			staged:    []fileEntry{{File: "baz.go", Status: "modified"}},
			unstaged:  []fileEntry{{File: "baz.go", Status: "modified"}},
			untracked: []string{},
		},
		{
			name:      "staged added",
			input:     "A  new.go\x00",
			staged:    []fileEntry{{File: "new.go", Status: "added"}},
			unstaged:  []fileEntry{},
			untracked: []string{},
		},
		{
			name:      "untracked",
			input:     "?? unk.go\x00",
			staged:    []fileEntry{},
			unstaged:  []fileEntry{},
			untracked: []string{"unk.go"},
		},
		{
			name:      "staged deleted",
			input:     "D  del.go\x00",
			staged:    []fileEntry{{File: "del.go", Status: "deleted"}},
			unstaged:  []fileEntry{},
			untracked: []string{},
		},
		{
			name:      "renamed",
			input:     "R  new.go\x00old.go\x00",
			staged:    []fileEntry{{File: "new.go", Status: "renamed"}},
			unstaged:  []fileEntry{},
			untracked: []string{},
		},
		{
			name:      "renamed and modified with status-like source",
			input:     "RM new\nname.go\x00?? old.go\x00?? next.go\x00",
			staged:    []fileEntry{{File: "new\nname.go", Status: "renamed"}},
			unstaged:  []fileEntry{{File: "new\nname.go", Status: "modified"}},
			untracked: []string{"next.go"},
		},
		{
			name:      "copied",
			input:     "C  copy.go\x00original.go\x00?? next.go\x00",
			staged:    []fileEntry{{File: "copy.go", Status: "copied"}},
			unstaged:  []fileEntry{},
			untracked: []string{"next.go"},
		},
		{
			name:      "worktree rename",
			input:     " R new.go\x00old.go\x00",
			staged:    []fileEntry{},
			unstaged:  []fileEntry{{File: "new.go", Status: "renamed"}},
			untracked: []string{},
		},
		{
			name:      "unmerged and ignored",
			input:     "UU conflict.go\x00!! ignored.go\x00",
			staged:    []fileEntry{{File: "conflict.go", Status: "unmerged"}},
			unstaged:  []fileEntry{{File: "conflict.go", Status: "unmerged"}},
			untracked: []string{},
		},
		{
			name:  "mixed",
			input: "M  staged.go\x00 M unstaged.go\x00?? new.go\x00A  added.go\x00D  removed.go\x00",
			staged: []fileEntry{
				{File: "staged.go", Status: "modified"},
				{File: "added.go", Status: "added"},
				{File: "removed.go", Status: "deleted"},
			},
			unstaged:  []fileEntry{{File: "unstaged.go", Status: "modified"}},
			untracked: []string{"new.go"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			staged, unstaged, untracked := parseGitPorcelain(tc.input)
			if len(staged) != len(tc.staged) {
				t.Fatalf("staged: want %d, got %d (%v)", len(tc.staged), len(staged), staged)
			}
			for i, want := range tc.staged {
				if staged[i] != want {
					t.Errorf("staged[%d]: want %v, got %v", i, want, staged[i])
				}
			}
			if len(unstaged) != len(tc.unstaged) {
				t.Fatalf("unstaged: want %d, got %d (%v)", len(tc.unstaged), len(unstaged), unstaged)
			}
			for i, want := range tc.unstaged {
				if unstaged[i] != want {
					t.Errorf("unstaged[%d]: want %v, got %v", i, want, unstaged[i])
				}
			}
			if len(untracked) != len(tc.untracked) {
				t.Fatalf("untracked: want %d, got %d (%v)", len(tc.untracked), len(untracked), untracked)
			}
			for i, want := range tc.untracked {
				if untracked[i] != want {
					t.Errorf("untracked[%d]: want %q, got %q", i, want, untracked[i])
				}
			}
		})
	}
}

func TestValidateSubcommandFlags(t *testing.T) {
	policy := &subcommandPolicy{
		safeFlags: map[string]flagArgType{
			"-v": flagNone, "--verbose": flagNone,
			"-n":     flagNumber,
			"--sort": flagString,
			"-l":     flagNone,
			"-a":     flagNone,
		},
	}

	for _, tc := range []struct {
		name    string
		args    []string
		wantErr bool
	}{
		{"no args", nil, false},
		{"known flag", []string{"-v"}, false},
		{"unknown flag", []string{"--unknown"}, true},
		{"number flag valid", []string{"-n", "5"}, false},
		{"number flag invalid", []string{"-n", "abc"}, true},
		{"string flag", []string{"--sort", "-refname"}, false},
		{"no-arg with =value", []string{"--verbose=true"}, true},
		{"combined short -avl", []string{"-avl"}, false},
		{"combined with unknown", []string{"-avx"}, true},
		{"positional passthrough", []string{"somearg"}, false},
		{"-- stops validation", []string{"--", "--unknown"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := validateSubcommandFlags("test", policy, tc.args)
			if (err != nil) != tc.wantErr {
				t.Errorf("want err=%v, got %v", tc.wantErr, err)
			}
		})
	}
}
