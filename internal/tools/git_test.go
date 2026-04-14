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
			input:     "M  foo.go\n",
			staged:    []fileEntry{{File: "foo.go", Status: "modified"}},
			unstaged:  []fileEntry{},
			untracked: []string{},
		},
		{
			name:      "unstaged modified",
			input:     " M bar.go\n",
			staged:    []fileEntry{},
			unstaged:  []fileEntry{{File: "bar.go", Status: "modified"}},
			untracked: []string{},
		},
		{
			name:      "both staged and unstaged",
			input:     "MM baz.go\n",
			staged:    []fileEntry{{File: "baz.go", Status: "modified"}},
			unstaged:  []fileEntry{{File: "baz.go", Status: "modified"}},
			untracked: []string{},
		},
		{
			name:      "staged added",
			input:     "A  new.go\n",
			staged:    []fileEntry{{File: "new.go", Status: "added"}},
			unstaged:  []fileEntry{},
			untracked: []string{},
		},
		{
			name:      "untracked",
			input:     "?? unk.go\n",
			staged:    []fileEntry{},
			unstaged:  []fileEntry{},
			untracked: []string{"unk.go"},
		},
		{
			name:      "staged deleted",
			input:     "D  del.go\n",
			staged:    []fileEntry{{File: "del.go", Status: "deleted"}},
			unstaged:  []fileEntry{},
			untracked: []string{},
		},
		{
			name:      "renamed",
			input:     "R  old.go -> new.go\n",
			staged:    []fileEntry{{File: "old.go -> new.go", Status: "renamed"}},
			unstaged:  []fileEntry{},
			untracked: []string{},
		},
		{
			name:  "mixed",
			input: "M  staged.go\n M unstaged.go\n?? new.go\nA  added.go\nD  removed.go\n",
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
