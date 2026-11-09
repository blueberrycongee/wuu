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
func (t *GitTool) IsReadOnly() bool        { return false } // commit, push are writes
func (t *GitTool) IsConcurrencySafe() bool { return false }

func (t *GitTool) Classify(argsJSON string) ToolClassification {
	invocation, err := parseGitInvocation(argsJSON, t.env.BypassToolHardProtections())
	if err != nil {
		return ToolClassification{
			ReadOnly:        false,
			ConcurrencySafe: false,
			Destructive:     false,
			Risk:            ToolRiskHigh,
			Reason:          "invalid restricted git invocation",
		}
	}
	switch invocation.Subcommand {
	case "add":
		return ToolClassification{
			ReadOnly:        false,
			ConcurrencySafe: false,
			Destructive:     false,
			Risk:            ToolRiskMedium,
			Reason:          "git add writes the repository index",
		}
	case "restore --staged":
		return ToolClassification{
			ReadOnly:        false,
			ConcurrencySafe: false,
			Destructive:     false,
			Risk:            ToolRiskMedium,
			Reason:          "git restore --staged writes the repository index",
		}
	case "commit":
		return ToolClassification{
			ReadOnly:        false,
			ConcurrencySafe: false,
			Destructive:     false,
			Risk:            ToolRiskMedium,
			Reason:          "git commit writes local repository history",
		}
	case "push":
		return ToolClassification{
			ReadOnly:        false,
			ConcurrencySafe: false,
			Destructive:     false,
			Risk:            ToolRiskMedium,
			Reason:          "git push writes remote branch state",
		}
	case "ls-remote":
		return ToolClassification{
			ReadOnly:        true,
			ConcurrencySafe: true,
			Destructive:     false,
			Risk:            ToolRiskMedium,
			Reason:          "git ls-remote is read-only but may contact a remote",
		}
	default:
		return ToolClassification{
			ReadOnly:        true,
			ConcurrencySafe: true,
			Destructive:     false,
			Risk:            ToolRiskLow,
			Reason:          "restricted git read-only command",
		}
	}
}

func (t *GitTool) ValidateInput(argsJSON string) error {
	_, err := parseGitInvocation(argsJSON, t.env.BypassToolHardProtections())
	return err
}

func (t *GitTool) Definition() providers.ToolDefinition {
	return providers.ToolDefinition{
		Name: "git",
		Description: "Run structured restricted git commands in the workspace. Normal non-interactive git workflows may also use bash; use this tool when structured status output, explicit restricted helpers, or sensitive-path protections are more useful than plain shell output.\n\n" +
			"Supported read-only commands: log, show, diff, status, blame, branch (list only), " +
			"tag (list only), reflog, stash list/show, ls-files, ls-remote, remote -v, " +
			"remote show, config --get/--get-all/--list, rev-parse, rev-list, describe, " +
			"cat-file metadata modes, for-each-ref, grep, worktree list, merge-base, shortlog.\n\n" +
			"Supported write commands: add (stage explicit paths only), restore --staged (unstage explicit paths only), " +
			"commit (non-interactive explicit message via -m/--message or -F/--file, no staged sensitive paths), push (plain or -u origin <branch>).\n\n" +
			"Git workflow: use status to inspect {staged, unstaged, untracked}; use diff and diff --cached before deciding; " +
			"use add with explicit paths from status to stage intended files; use restore --staged with explicit paths to remove accidental staged files; " +
			"review staged changes with diff --cached before committing.\n\n" +
			"git status returns structured {staged, unstaged, untracked} output.\n\n" +
			"add and restore --staged reject root/current-directory/pathspec magic/wildcard forms; pass explicit workspace-relative paths. " +
			"Content output for sensitive credential paths is rejected or redacted.\n\n" +
			"NOT supported (delegate to a worker): rebase, merge, cherry-pick, clean, reset --hard, " +
			"stash pop/apply/drop/clear, force push, branch create/delete, tag create/delete.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"subcommand": map[string]any{
					"type":        "string",
					"description": "Git subcommand. Supported read/query commands: log, show, diff, status, blame, branch (list-only flags), tag (list-only flags), reflog, stash list, stash show, ls-files, ls-remote, remote (-v only), remote show, config --get, config --get-all, config --list, rev-parse, rev-list, describe, cat-file, for-each-ref, grep, worktree list, merge-base, shortlog. Supported restricted write commands: add, restore --staged, commit, push.",
				},
				"args": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "Arguments to pass to the git subcommand. add and restore --staged require explicit workspace-relative paths from git status and do not accept '.', wildcards, pathspec magic, or flags. commit supports non-interactive explicit messages with one or more -m/--message flags, a workspace-relative -F/--file message file, and --amend when explicitly requested; push only supports plain push or -u/--set-upstream origin <current-branch>.",
				},
			},
			"required": []string{"subcommand"},
		},
	}
}

func (t *GitTool) Execute(ctx context.Context, argsJSON string) (string, error) {
	return gitExecute(t.env, ctx, argsJSON)
}
