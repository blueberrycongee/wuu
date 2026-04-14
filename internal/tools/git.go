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

// allowedGitSubcommands is the whitelist of read-only git subcommands.
// Multi-word commands use space separation (e.g. "stash list").
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
}

// blockedArgPrefixes are git flags that can lead to code execution or
// bypass the read-only intent. Any arg matching one of these is rejected.
var blockedArgPrefixes = []string{
	"-c",
	"--config-env",
	"--exec-path",
	"--no-index",
}

// shellMetacharacters are characters that should not appear in individual
// args. Each arg is a separate token passed to exec.Command so shell
// injection is not possible, but blocking these prevents the model from
// trying shell-like patterns.
var shellMetacharacters = ";&|$`><()"

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

	// Normalize: try matching multi-word subcommands first.
	// If the subcommand alone isn't in the whitelist and there are args,
	// try combining subcommand + " " + args[0].
	if !allowedGitSubcommands[subcmd] && len(remainingArgs) > 0 {
		combined := subcmd + " " + remainingArgs[0]
		if allowedGitSubcommands[combined] {
			subcmd = combined
			remainingArgs = remainingArgs[1:]
		}
	}

	if !allowedGitSubcommands[subcmd] {
		return "", fmt.Errorf("git subcommand %q is not allowed (read-only mode)", args.Subcommand)
	}

	// Security: validate individual args.
	if err := validateGitArgs(remainingArgs); err != nil {
		return "", err
	}

	// Build command: git --no-optional-locks <subcommand> [args...]
	// Split multi-word subcommand into separate tokens.
	subcmdParts := strings.Fields(subcmd)
	gitArgs := append([]string{"--no-optional-locks"}, subcmdParts...)
	gitArgs = append(gitArgs, remainingArgs...)

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

// validateGitArgs checks individual args for blocked flags and shell
// metacharacters.
func validateGitArgs(args []string) error {
	for i, arg := range args {
		// Check for blocked flag prefixes.
		for _, prefix := range blockedArgPrefixes {
			if arg == prefix || strings.HasPrefix(arg, prefix+"=") {
				return fmt.Errorf("git arg %q is not allowed", arg)
			}
		}
		// Special case: "-c" as a standalone flag with the value in the
		// next arg.
		if arg == "-c" && i+1 < len(args) {
			return fmt.Errorf("git arg \"-c\" is not allowed (can set arbitrary config)")
		}

		// Check for shell metacharacters.
		for _, ch := range shellMetacharacters {
			if strings.ContainsRune(arg, ch) {
				return fmt.Errorf("git arg %q contains blocked metacharacter %q", arg, string(ch))
			}
		}
	}
	return nil
}
