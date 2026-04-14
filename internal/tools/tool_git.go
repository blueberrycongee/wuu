package tools

import (
	"context"

	"github.com/blueberrycongee/wuu/internal/providers"
)

// GitTool wraps the git command validation and execution. The heavy
// lifting (flag-level policies, subcommand whitelisting) stays in
// git.go; this file only provides the Tool interface adapter.
type GitTool struct{ env *Env }

func NewGitTool(env *Env) *GitTool { return &GitTool{env: env} }

func (t *GitTool) Name() string            { return "git" }
func (t *GitTool) IsReadOnly() bool         { return false } // commit, push are writes
func (t *GitTool) IsConcurrencySafe() bool  { return false }

func (t *GitTool) Definition() providers.ToolDefinition {
	return providers.ToolDefinition{
		Name:        "git",
		Description: "Run restricted git commands from the main agent: read-only queries (log, status, diff, show, blame, branch (list only), tag (list only), remote -v, remote show, config --get/--get-all/--list, etc.) plus simple git operations like commit and push. branch/tag/remote/config are restricted to read-only operations with flag-level enforcement. git status returns structured {staged, unstaged, untracked} output. Complex or destructive operations (rebase, merge, cherry-pick, clean, reset --hard, stash pop/apply/drop/clear, force push, branch create/delete, tag create/delete, etc.) are not supported and should be delegated to a worker.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"subcommand": map[string]any{
					"type":        "string",
					"description": "Git subcommand. Supported read/query commands: log, show, diff, status, blame, branch (list-only flags), tag (list-only flags), reflog, stash list, stash show, ls-files, ls-remote, remote (-v only), remote show, config --get, config --get-all, config --list, rev-parse, rev-list, describe, cat-file, for-each-ref, grep, worktree list, merge-base, shortlog. Supported restricted write commands: commit, push.",
				},
				"args": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "Arguments to pass to the git subcommand. commit only supports explicit -m/--message forms on staged changes; push only supports plain push or -u/--set-upstream origin <current-branch>.",
				},
			},
			"required": []string{"subcommand"},
		},
	}
}

func (t *GitTool) Execute(ctx context.Context, argsJSON string) (string, error) {
	return gitExecute(t.env, ctx, argsJSON)
}
