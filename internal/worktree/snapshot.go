package worktree

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Snapshot freezes tracked and untracked, non-ignored files in a Git commit
// without changing the user's index, branch, worktree files or commit history.
// The private ref keeps the candidate reachable until explicitly discarded.
func Snapshot(ctx context.Context, root, base, key string) (string, string, error) {
	if key == "" || strings.ContainsAny(key, "/\\. \t\n") {
		return "", "", fmt.Errorf("invalid snapshot key")
	}
	directory, err := os.MkdirTemp("", "wuu-candidate-index-")
	if err != nil {
		return "", "", err
	}
	defer os.RemoveAll(directory)
	run := func(input string, args ...string) (string, error) {
		command := exec.CommandContext(ctx, "git", append([]string{"--no-pager", "--literal-pathspecs", "-C", root, "-c", "core.fsmonitor=false", "-c", "core.hooksPath=/dev/null"}, args...)...)
		command.Env = append(os.Environ(), "GIT_INDEX_FILE="+filepath.Join(directory, "index"), "GIT_TERMINAL_PROMPT=0", "GIT_AUTHOR_NAME=Wuu", "GIT_AUTHOR_EMAIL=wuu@localhost", "GIT_COMMITTER_NAME=Wuu", "GIT_COMMITTER_EMAIL=wuu@localhost")
		command.Stdin = strings.NewReader(input)
		var out, stderr bytes.Buffer
		command.Stdout, command.Stderr = &out, &stderr
		if err := command.Run(); err != nil {
			return "", fmt.Errorf("snapshot git %s: %s: %w", args[0], strings.TrimSpace(stderr.String()), err)
		}
		return out.String(), nil
	}
	ref := "refs/wuu/candidates/" + key
	revision, err := run("", "rev-parse", "--verify", "--quiet", ref+"^{commit}")
	if err != nil {
		if _, err = run("", "read-tree", "HEAD"); err != nil {
			return "", "", err
		}
		if _, err = run("", "add", "--all", "--", "."); err != nil {
			return "", "", err
		}
		tree, err := run("", "write-tree")
		if err != nil {
			return "", "", err
		}
		revision, err = run("Wuu review candidate\n", "commit-tree", strings.TrimSpace(tree), "-p", base)
		if err != nil {
			return "", "", err
		}
		if _, err = run("", "update-ref", ref, strings.TrimSpace(revision)); err != nil {
			return "", "", err
		}
	}
	revision = strings.TrimSpace(revision)
	diff, err := run("", "diff", "--no-ext-diff", "--no-textconv", "--binary", base, revision, "--")
	return revision, diff, err
}
