package tools

import (
	"context"
	"encoding/json"
	"os/exec"
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
		"printf 'hello\\n' > hello.txt",
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
	if err != nil { t.Fatalf("New: %v", err) }
	return kit, root
}

func gitCall(t *testing.T, kit *Toolkit, subcmd string, args ...string) map[string]any {
	t.Helper()
	aj, _ := json.Marshal(map[string]any{"subcommand": subcmd, "args": args})
	resp, err := kit.Execute(context.Background(), providers.ToolCall{Name: "git", Arguments: string(aj)})
	if err != nil { t.Fatalf("git %s %v: %v", subcmd, args, err) }
	var p map[string]any
	if err := json.Unmarshal([]byte(resp), &p); err != nil { t.Fatalf("parse: %v", err) }
	return p
}

func gitErr(t *testing.T, kit *Toolkit, subcmd string, args ...string) string {
	t.Helper()
	aj, _ := json.Marshal(map[string]any{"subcommand": subcmd, "args": args})
	_, err := kit.Execute(context.Background(), providers.ToolCall{Name: "git", Arguments: string(aj)})
	if err == nil { t.Fatalf("expected error for git %s %v", subcmd, args) }
	return err.Error()
}

func TestToolkit_Git_ReadOnlySubcommands(t *testing.T) {
	kit, _ := setupGitRepo(t)
	for _, sub := range []string{"log", "status", "diff", "show"} {
		p := gitCall(t, kit, sub)
		if p["exit_code"].(float64) != 0 { t.Errorf("git %s: exit_code=%v", sub, p["exit_code"]) }
	}
}

func TestToolkit_Git_BlockedSubcommands(t *testing.T) {
	kit, _ := setupGitRepo(t)
	for _, sub := range []string{"commit", "push", "rebase", "merge", "clean"} {
		msg := gitErr(t, kit, sub)
		if !strings.Contains(msg, "not allowed") { t.Errorf("git %s: want 'not allowed', got: %s", sub, msg) }
	}
	_, err := kit.Execute(context.Background(), providers.ToolCall{Name: "git", Arguments: `{"subcommand":""}`})
	if err == nil { t.Fatal("empty subcommand should error") }
}

func TestToolkit_Git_MultiWordSubcommands(t *testing.T) {
	kit, _ := setupGitRepo(t)
	p := gitCall(t, kit, "stash list")
	if p["exit_code"].(float64) != 0 { t.Fatalf("stash list: %v", p) }
	p = gitCall(t, kit, "stash", "list")
	if p["exit_code"].(float64) != 0 { t.Fatalf("stash+list: %v", p) }
	p = gitCall(t, kit, "config", "--get", "user.name")
	if p["exit_code"].(float64) != 0 { t.Fatalf("config --get: %v", p) }
	if !strings.Contains(p["output"].(string), "tester") { t.Errorf("user.name: got %q", p["output"]) }
	p = gitCall(t, kit, "worktree list")
	if p["exit_code"].(float64) != 0 { t.Fatalf("worktree list: %v", p) }
}

func TestToolkit_Git_BlockedArgs(t *testing.T) {
	kit, _ := setupGitRepo(t)
	for _, a := range [][]string{{"-c", "foo=true"}, {"--config-env", "x=y"}, {"--exec-path"}, {"--no-index"}} {
		msg := gitErr(t, kit, "log", a...)
		if msg == "" { t.Errorf("expected blocked arg error for %v", a) }
	}
	if msg := gitErr(t, kit, "log", "echo;rm"); msg == "" { t.Error("expected metachar error") }
}

func TestToolkit_Git_NonInteractiveEnv(t *testing.T) {
	kit, _ := setupGitRepo(t)
	// Verify non-interactive env is set by running a shell command that
	// prints the GIT_TERMINAL_PROMPT env var. The git tool sets the same
	// env as run_shell, but git itself cannot introspect env vars via
	// "git config --get" (that reads config keys, not env vars).
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
			if !strings.Contains(strings.ToLower(d.Description), "read-only") { t.Errorf("desc: %q", d.Description) }
			return
		}
	}
	t.Fatal("git not in Definitions()")
}

func TestToolkit_Git_NotDisabledWithShellDisabled(t *testing.T) {
	kit, _ := setupGitRepo(t)
	kit.DisableTools("write_file", "edit_file", "run_shell")
	found := false
	for _, d := range kit.Definitions() {
		if d.Name == "git" { found = true; break }
	}
	if !found { t.Fatal("git should remain after disabling shell") }
	p := gitCall(t, kit, "log", "--oneline")
	if p["exit_code"].(float64) != 0 { t.Fatalf("git log: %v", p) }
}
