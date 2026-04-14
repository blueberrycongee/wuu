package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/blueberrycongee/wuu/internal/providers"
)

func setupGitRepo(t *testing.T) (*Toolkit, string) {
	t.Helper()
	root := t.TempDir()
	for _, c := range []string{
		"git init -q",
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
	if p["exit_code"].(float64) != 0 {
		t.Fatalf("stash list: %v", p)
	}
	p = gitCall(t, kit, "stash", "list")
	if p["exit_code"].(float64) != 0 {
		t.Fatalf("stash+list: %v", p)
	}
	p = gitCall(t, kit, "config", "--get", "user.name")
	if p["exit_code"].(float64) != 0 {
		t.Fatalf("config --get: %v", p)
	}
	if !strings.Contains(p["output"].(string), "tester") {
		t.Errorf("user.name: got %q", p["output"])
	}
	p = gitCall(t, kit, "worktree list")
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
	log := runBash(t, root, "git log -1 --format=%s")
	if strings.TrimSpace(log) != "Add staged file" {
		t.Fatalf("unexpected commit message: %q", log)
	}
}

func TestToolkit_Git_CommitWithoutStagedChangesFailsCleanly(t *testing.T) {
	kit, _ := setupGitRepo(t)
	p := gitCall(t, kit, "commit", "-m", "Nothing to commit")
	if p["exit_code"].(float64) == 0 {
		t.Fatalf("expected non-zero exit for empty commit: %v", p)
	}
}

func TestToolkit_Git_CommitRejectedFlags(t *testing.T) {
	kit, _ := setupGitRepo(t)
	for _, args := range [][]string{
		{"--amend", "-m", "x"},
		{"--no-verify", "-m", "x"},
		{"--allow-empty", "-m", "x"},
		{"-F", "msg.txt"},
	} {
		msg := gitErr(t, kit, "commit", args...)
		if !strings.Contains(msg, "not allowed") {
			t.Errorf("commit args %v: want restricted error, got %s", args, msg)
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

func TestToolkit_Git_NonInteractiveEnv(t *testing.T) {
	kit, _ := setupGitRepo(t)
	resp, err := kit.Execute(context.Background(), providers.ToolCall{
		Name:      "run_shell",
		Arguments: `{"command":"printf '%s' \"$GIT_TERMINAL_PROMPT\""}`,
	})
	if err != nil {
		t.Fatalf("run_shell: %v", err)
	}
	var p map[string]any
	if err := json.Unmarshal([]byte(resp), &p); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if p["output"].(string) != "0" {
		t.Errorf("GIT_TERMINAL_PROMPT got %q", p["output"])
	}
}

func TestToolkit_Git_InDefinitions(t *testing.T) {
	kit, _ := setupGitRepo(t)
	for _, d := range kit.Definitions() {
		if d.Name == "git" {
			if !strings.Contains(strings.ToLower(d.Description), "commit") || !strings.Contains(strings.ToLower(d.Description), "push") {
				t.Errorf("desc: %q", d.Description)
			}
			return
		}
	}
	t.Fatal("git not in Definitions()")
}

func TestToolkit_Git_NotDisabledWithShellDisabled(t *testing.T) {
	kit, root := setupGitRepo(t)
	kit.DisableTools("write_file", "edit_file", "run_shell")
	found := false
	for _, d := range kit.Definitions() {
		if d.Name == "git" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("git should remain after disabling shell")
	}
	runBash(t, root, "printf 'more\n' >> hello.txt && git add hello.txt")
	p := gitCall(t, kit, "commit", "--message", "Update hello")
	if p["exit_code"].(float64) != 0 {
		t.Fatalf("git commit after disable: %v", p)
	}
}
