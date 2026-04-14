package tools

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

const gitTimeout = 30 * time.Second

// allowedGitSubcommands is the whitelist of git subcommands available to the
// main agent. Multi-word commands use space separation (e.g. "stash list").
var allowedGitSubcommands = map[string]bool{
	"log":           true,
	"show":          true,
	"diff":          true,
	"status":        true,
	"blame":         true,
	"branch":        true,
	"tag":           true,
	"reflog":        true,
	"stash list":    true,
	"stash show":    true,
	"ls-files":      true,
	"ls-remote":     true,
	"remote":        true,
	"config":        true,
	"rev-parse":     true,
	"rev-list":      true,
	"describe":      true,
	"cat-file":      true,
	"for-each-ref":  true,
	"grep":          true,
	"worktree list": true,
	"merge-base":    true,
	"shortlog":      true,
	"commit":        true,
	"push":          true,
}

// blockedGlobalArgPrefixes are git flags that can lead to code execution or
// bypass the restricted-git intent. These are blocked before any subcommand-
// specific validation. Do NOT include subcommand-local flags here.
var blockedGlobalArgPrefixes = []string{
	"--config-env",
	"--exec-path",
}

// shellMetacharacters are characters that should not appear in non-message
// individual args. Each arg is a separate token passed to exec.Command so shell
// injection is not possible, but blocking these prevents the model from trying
// shell-like patterns where the git CLI would interpret them specially.
var shellMetacharacters = ";&|$`><()"

var blockedCommitFlags = map[string]bool{
	"--amend":               true,
	"--no-verify":           true,
	"--gpg-sign":            true,
	"--no-gpg-sign":         true,
	"-S":                    true,
	"--signoff":             true,
	"--fixup":               true,
	"--squash":              true,
	"-C":                    true,
	"-c":                    true,
	"-F":                    true,
	"--file":                true,
	"--author":              true,
	"--date":                true,
	"--allow-empty":         true,
	"--allow-empty-message": true,
	"-e":                    true,
	"--edit":                true,
}

func (t *Toolkit) git(ctx context.Context, argsJSON string) (string, error) {
	var args struct {
		Subcommand string   `json:"subcommand"`
		Args       []string `json:"args"`
	}
	if err := decodeArgs(argsJSON, &args); err != nil {
		return "", err
	}
	if strings.TrimSpace(args.Subcommand) == "" {
		return "", errors.New("git requires subcommand")
	}

	subcmd := strings.TrimSpace(args.Subcommand)
	remainingArgs := args.Args

	if !allowedGitSubcommands[subcmd] && len(remainingArgs) > 0 {
		combined := subcmd + " " + remainingArgs[0]
		if allowedGitSubcommands[combined] {
			subcmd = combined
			remainingArgs = remainingArgs[1:]
		}
	}

	if !allowedGitSubcommands[subcmd] {
		return "", fmt.Errorf("git subcommand %q is not allowed in restricted mode", args.Subcommand)
	}

	if err := validateGitArgs(subcmd, remainingArgs); err != nil {
		return "", err
	}

	subcmdParts := strings.Fields(subcmd)
	gitArgs := append([]string{"--no-optional-locks"}, subcmdParts...)
	gitArgs = append(gitArgs, remainingArgs...)
	if subcmd == "push" {
		normalized, err := t.normalizePushArgs(ctx, remainingArgs)
		if err != nil {
			return "", err
		}
		gitArgs = append([]string{"--no-optional-locks", "push"}, normalized...)
	}

	runCtx, cancel := context.WithTimeout(ctx, gitTimeout)
	defer cancel()

	cmd := exec.CommandContext(runCtx, "git", gitArgs...)
	cmd.Dir = t.rootDir
	cmd.Env = mergeEnv(os.Environ(), nonInteractiveShellEnv())

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	exitCode := 0
	timedOut := false
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			exitCode = exitErr.ExitCode()
		} else if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
			exitCode = 124
			timedOut = true
		} else {
			return "", fmt.Errorf("git %s: %w", subcmd, err)
		}
	}

	output := stdout.String() + stderr.String()
	trimmed, truncated := truncate(output, maxShellOutputBytes)

	result := map[string]any{
		"subcommand": subcmd,
		"exit_code":  exitCode,
		"output":     trimmed,
		"timed_out":  timedOut,
	}
	if truncated {
		result["truncated"] = true
	}
	return mustJSON(result)
}

func validateGitArgs(subcmd string, args []string) error {
	for i, arg := range args {
		for _, prefix := range blockedGlobalArgPrefixes {
			if arg == prefix || strings.HasPrefix(arg, prefix+"=") {
				return fmt.Errorf("git arg %q is not allowed", arg)
			}
		}
		for _, ch := range shellMetacharacters {
			if strings.ContainsRune(arg, ch) && !isCommitMessageArg(subcmd, args, i) {
				return fmt.Errorf("git arg %q contains blocked metacharacter %q", arg, string(ch))
			}
		}
	}

	switch subcmd {
	case "commit":
		return validateCommitArgs(args)
	case "push":
		return validatePushArgs(args)
	default:
		if hasDangerousGlobalConfigArgs(args) {
			return errors.New("git global config overrides are not allowed")
		}
	}
	return nil
}

func isCommitMessageArg(subcmd string, args []string, idx int) bool {
	if subcmd != "commit" || idx <= 0 || idx >= len(args) {
		return false
	}
	prev := args[idx-1]
	return prev == "-m" || prev == "--message"
}

func hasDangerousGlobalConfigArgs(args []string) bool {
	for i, arg := range args {
		if arg == "-c" {
			return true
		}
		if strings.HasPrefix(arg, "-c") && len(arg) > 2 {
			return true
		}
		if arg == "--config-env" || strings.HasPrefix(arg, "--config-env=") {
			return true
		}
		if arg == "--exec-path" || strings.HasPrefix(arg, "--exec-path=") {
			return true
		}
		if arg == "--no-index" {
			return true
		}
		if i == 0 && arg == "-c" {
			return true
		}
	}
	return false
}

func validateCommitArgs(args []string) error {
	if len(args) == 0 {
		return errors.New("git commit requires an explicit message via -m or --message")
	}
	messageSeen := false
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if blockedCommitFlags[arg] {
			return fmt.Errorf("git commit flag %q is not allowed in restricted mode", arg)
		}
		for blocked := range blockedCommitFlags {
			if strings.HasPrefix(arg, blocked+"=") {
				return fmt.Errorf("git commit flag %q is not allowed in restricted mode", blocked)
			}
		}
		switch arg {
		case "-m", "--message":
			if messageSeen {
				return errors.New("git commit accepts exactly one explicit message")
			}
			if i+1 >= len(args) {
				return fmt.Errorf("git commit flag %q requires a message", arg)
			}
			messageSeen = true
			i++
			continue
		}
		if strings.HasPrefix(arg, "-m") && arg != "-m" {
			messageSeen = true
			continue
		}
		if strings.HasPrefix(arg, "--message=") {
			if messageSeen {
				return errors.New("git commit accepts exactly one explicit message")
			}
			messageSeen = true
			continue
		}
		if strings.HasPrefix(arg, "-") {
			return fmt.Errorf("git commit flag %q is not allowed in restricted mode", arg)
		}
		return fmt.Errorf("git commit only supports -m/--message; unexpected arg %q", arg)
	}
	if !messageSeen {
		return errors.New("git commit requires an explicit message via -m or --message")
	}
	return nil
}

func validatePushArgs(args []string) error {
	if len(args) == 0 {
		return nil
	}
	if len(args) == 3 && (args[0] == "-u" || args[0] == "--set-upstream") {
		return nil
	}
	for _, arg := range args {
		if arg == "--force" || arg == "--force-with-lease" || arg == "-f" {
			return fmt.Errorf("git push flag %q is not allowed in restricted mode", arg)
		}
		if strings.HasPrefix(arg, "--force=") || strings.HasPrefix(arg, "--delete") || arg == "--tags" {
			return fmt.Errorf("git push flag %q is not allowed in restricted mode", arg)
		}
		if strings.HasPrefix(arg, ":") {
			return fmt.Errorf("git push refspec %q is not allowed in restricted mode", arg)
		}
	}
	return errors.New("git push only supports: no args, -u origin <current-branch>, or --set-upstream origin <current-branch>")
}

func (t *Toolkit) normalizePushArgs(ctx context.Context, args []string) ([]string, error) {
	if len(args) == 0 {
		return nil, nil
	}
	branch, err := t.currentBranch(ctx)
	if err != nil {
		return nil, err
	}
	if len(args) == 3 && (args[0] == "-u" || args[0] == "--set-upstream") {
		if args[1] != "origin" {
			return nil, fmt.Errorf("git push only allows remote %q in restricted mode", "origin")
		}
		if args[2] != branch {
			return nil, fmt.Errorf("git push only allows current branch %q, got %q", branch, args[2])
		}
		return args, nil
	}
	return nil, errors.New("git push only supports: no args, -u origin <current-branch>, or --set-upstream origin <current-branch>")
}

func (t *Toolkit) currentBranch(ctx context.Context) (string, error) {
	runCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(runCtx, "git", "--no-optional-locks", "rev-parse", "--abbrev-ref", "HEAD")
	cmd.Dir = t.rootDir
	cmd.Env = mergeEnv(os.Environ(), nonInteractiveShellEnv())
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("resolve current branch: %w", err)
	}
	branch := strings.TrimSpace(string(out))
	if branch == "" || branch == "HEAD" {
		return "", errors.New("git push requires a checked-out branch")
	}
	return branch, nil
}
