package tools

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/blueberrycongee/wuu/internal/gitattribution"
)

const gitTimeout = 30 * time.Second

// ── flag-level policy types ─────────────────────────────────────────

// flagArgType describes what kind of argument a flag consumes.
type flagArgType int

const (
	flagNone   flagArgType = iota // flag takes no argument
	flagString                    // flag takes a string argument
	flagNumber                    // flag takes a numeric argument
)

// subcommandPolicy defines the allowed flags and an optional semantic
// check for a git subcommand that needs flag-level enforcement.
type subcommandPolicy struct {
	safeFlags   map[string]flagArgType
	isDangerous func(args []string) bool // nil = no extra check
}

// fileEntry represents a single file in structured git status output.
type fileEntry struct {
	File   string `json:"file"`
	Status string `json:"status"`
}

type gitInvocation struct {
	Subcommand string
	Args       []string
}

// ── subcommand whitelists ───────────────────────────────────────────

// allowedGitSubcommands is the whitelist of git subcommands that need
// no per-flag validation (inherently read-only or handled by their own
// switch-case in validateGitArgs).
var allowedGitSubcommands = map[string]bool{
	"log":              true,
	"show":             true,
	"diff":             true,
	"blame":            true,
	"reflog":           true,
	"stash list":       true,
	"stash show":       true,
	"ls-files":         true,
	"ls-remote":        true,
	"rev-parse":        true,
	"rev-list":         true,
	"describe":         true,
	"cat-file":         true,
	"for-each-ref":     true,
	"grep":             true,
	"worktree list":    true,
	"merge-base":       true,
	"shortlog":         true,
	"add":              true,
	"restore --staged": true,
	"commit":           true,
	"push":             true,
}

// policiedSubcommands require flag-level validation via subcommandPolicy.
var policiedSubcommands = map[string]*subcommandPolicy{
	"branch":           branchPolicy,
	"tag":              tagPolicy,
	"remote":           remotePolicy,
	"remote show":      remoteShowPolicy,
	"config --get":     configGetPolicy,
	"config --get-all": configGetPolicy,
	"config --list":    configListPolicy,
	"status":           statusPolicy,
}

// ── policy definitions for read-only git subcommands ─

var branchPolicy = &subcommandPolicy{
	safeFlags: map[string]flagArgType{
		"-l": flagNone, "--list": flagNone,
		"-a": flagNone, "--all": flagNone,
		"-r": flagNone, "--remotes": flagNone,
		"-v": flagNone, "-vv": flagNone, "--verbose": flagNone,
		"--color": flagNone, "--no-color": flagNone,
		"--column": flagNone, "--no-column": flagNone,
		"--abbrev": flagNumber, "--no-abbrev": flagNone,
		"--contains": flagString, "--no-contains": flagString,
		"--merged": flagNone, "--no-merged": flagNone,
		"--points-at": flagString, "--sort": flagString,
		"--show-current": flagNone,
		"-i":             flagNone, "--ignore-case": flagNone,
	},
	isDangerous: branchIsDangerous,
}

var tagPolicy = &subcommandPolicy{
	safeFlags: map[string]flagArgType{
		"-l": flagNone, "--list": flagNone,
		"-n":         flagNumber,
		"--contains": flagString, "--no-contains": flagString,
		"--merged": flagString, "--no-merged": flagString,
		"--sort": flagString, "--format": flagString,
		"--points-at": flagString,
		"--column":    flagNone, "--no-column": flagNone,
		"-i": flagNone, "--ignore-case": flagNone,
	},
	isDangerous: tagIsDangerous,
}

var remotePolicy = &subcommandPolicy{
	safeFlags: map[string]flagArgType{
		"-v": flagNone, "--verbose": flagNone,
	},
	isDangerous: func(args []string) bool {
		for _, a := range args {
			if a != "-v" && a != "--verbose" {
				return true
			}
		}
		return false
	},
}

var remoteShowPolicy = &subcommandPolicy{
	safeFlags: map[string]flagArgType{
		"-n": flagNone,
	},
	isDangerous: func(args []string) bool {
		var positional []string
		for _, a := range args {
			if a != "-n" {
				positional = append(positional, a)
			}
		}
		if len(positional) != 1 {
			return true
		}
		matched, _ := regexp.MatchString(`^[a-zA-Z0-9_-]+$`, positional[0])
		return !matched
	},
}

var configGetPolicy = &subcommandPolicy{
	safeFlags: map[string]flagArgType{
		"--local": flagNone, "--global": flagNone,
		"--system": flagNone, "--worktree": flagNone,
		"--default": flagString, "--type": flagString,
		"--bool": flagNone, "--int": flagNone,
		"--bool-or-int": flagNone, "--path": flagNone,
		"--expiry-date": flagNone,
		"-z":            flagNone, "--null": flagNone,
		"--name-only": flagNone, "--show-origin": flagNone,
		"--show-scope": flagNone,
	},
	isDangerous: nil, // positional args are config key names, harmless
}

var configListPolicy = &subcommandPolicy{
	safeFlags: map[string]flagArgType{
		"--local": flagNone, "--global": flagNone,
		"--system": flagNone, "--worktree": flagNone,
		"--type": flagString,
		"--bool": flagNone, "--int": flagNone,
		"--bool-or-int": flagNone, "--path": flagNone,
		"--expiry-date": flagNone,
		"-z":            flagNone, "--null": flagNone,
		"--name-only": flagNone, "--show-origin": flagNone,
		"--show-scope": flagNone,
	},
	isDangerous: func(args []string) bool {
		// --list takes no key argument; block any positional args
		for _, a := range args {
			if !strings.HasPrefix(a, "-") {
				return true
			}
		}
		return false
	},
}

var statusPolicy = &subcommandPolicy{
	safeFlags: map[string]flagArgType{
		"--short": flagNone, "-s": flagNone,
		"--branch": flagNone, "-b": flagNone,
		"--porcelain": flagNone,
		"--long":      flagNone,
		"--verbose":   flagNone, "-v": flagNone,
		"--untracked-files": flagString, "-u": flagString,
		"--ignored":           flagNone,
		"--ignore-submodules": flagString,
		"--column":            flagNone, "--no-column": flagNone,
		"--ahead-behind": flagNone, "--no-ahead-behind": flagNone,
		"--renames": flagNone, "--no-renames": flagNone,
		"--find-renames": flagString, "-M": flagString,
	},
	isDangerous: nil,
}

// ── isDangerous callbacks ───────────────────────────────────────────

// branchIsDangerous blocks positional args (branch creation/deletion)
// unless -l/--list is present. Handles -- end-of-options.
func branchIsDangerous(args []string) bool {
	flagsWithArgs := map[string]bool{
		"--contains": true, "--no-contains": true,
		"--points-at": true, "--sort": true,
	}
	flagsWithOptionalArgs := map[string]bool{
		"--merged": true, "--no-merged": true,
	}

	var (
		i            int
		lastFlag     string
		seenListFlag bool
		seenDashDash bool
	)
	for i < len(args) {
		token := args[i]
		if token == "--" && !seenDashDash {
			seenDashDash = true
			lastFlag = ""
			i++
			continue
		}
		if !seenDashDash && strings.HasPrefix(token, "-") {
			if token == "--list" || token == "-l" {
				seenListFlag = true
			} else if len(token) > 2 && token[0] == '-' && token[1] != '-' && !strings.Contains(token, "=") && strings.ContainsRune(token[1:], 'l') {
				seenListFlag = true
			}
			if strings.Contains(token, "=") {
				lastFlag = strings.SplitN(token, "=", 2)[0]
				i++
			} else if flagsWithArgs[token] {
				lastFlag = token
				i += 2
			} else {
				lastFlag = token
				i++
			}
		} else {
			// Positional arg — dangerous unless listing or after optional-arg flag
			if !seenListFlag && !flagsWithOptionalArgs[lastFlag] {
				return true
			}
			i++
		}
	}
	return false
}

// tagIsDangerous blocks positional args (tag creation) unless -l/--list
// is present. Handles -- end-of-options.
func tagIsDangerous(args []string) bool {
	flagsWithArgs := map[string]bool{
		"--contains": true, "--no-contains": true,
		"--merged": true, "--no-merged": true,
		"--points-at": true, "--sort": true,
		"--format": true, "-n": true,
	}

	var (
		i            int
		seenListFlag bool
		seenDashDash bool
	)
	for i < len(args) {
		token := args[i]
		if token == "--" && !seenDashDash {
			seenDashDash = true
			i++
			continue
		}
		if !seenDashDash && strings.HasPrefix(token, "-") {
			if token == "--list" || token == "-l" {
				seenListFlag = true
			} else if len(token) > 2 && token[0] == '-' && token[1] != '-' && !strings.Contains(token, "=") && strings.ContainsRune(token[1:], 'l') {
				seenListFlag = true
			}
			if strings.Contains(token, "=") {
				i++
			} else if flagsWithArgs[token] {
				i += 2
			} else {
				i++
			}
		} else {
			if !seenListFlag {
				return true
			}
			i++
		}
	}
	return false
}

// ── generic flag validation ─────────────────────────────────────────

// validateSubcommandFlags checks every flag in args against the policy's
// safeFlags whitelist, then runs the isDangerous callback if present.
func validateSubcommandFlags(subcmd string, policy *subcommandPolicy, args []string) error {
	for i := 0; i < len(args); i++ {
		arg := args[i]

		// -- ends flag parsing; rest is positional (left to isDangerous)
		if arg == "--" {
			break
		}

		if !strings.HasPrefix(arg, "-") {
			continue // positional arg, handled by isDangerous
		}

		// Split --flag=value
		flagName := arg
		hasEquals := false
		if idx := strings.Index(arg, "="); idx >= 0 {
			flagName = arg[:idx]
			hasEquals = true
		}

		argType, known := policy.safeFlags[flagName]
		if !known {
			// Try combined short flags like -avl
			if len(flagName) > 2 && flagName[0] == '-' && flagName[1] != '-' && !hasEquals {
				if err := validateCombinedShortFlags(subcmd, policy, flagName[1:]); err != nil {
					return err
				}
				continue
			}
			return fmt.Errorf("git %s flag %q is not allowed in restricted mode", subcmd, flagName)
		}

		switch argType {
		case flagNone:
			// no-arg flag; reject --flag=value form
			if hasEquals {
				return fmt.Errorf("git %s flag %q does not accept a value", subcmd, flagName)
			}
		case flagString:
			if !hasEquals {
				i++ // consume next token as value
			}
		case flagNumber:
			var val string
			if hasEquals {
				val = arg[strings.Index(arg, "=")+1:]
			} else {
				i++
				if i < len(args) {
					val = args[i]
				}
			}
			if val != "" {
				if _, err := strconv.Atoi(val); err != nil {
					return fmt.Errorf("git %s flag %q requires a numeric value, got %q", subcmd, flagName, val)
				}
			}
		}
	}

	if policy.isDangerous != nil && policy.isDangerous(args) {
		return fmt.Errorf("git %s: operation not allowed in restricted mode", subcmd)
	}
	return nil
}

// validateCombinedShortFlags checks that every character in a short-flag
// bundle (e.g. "avl" from "-avl") is a known flagNone flag.
func validateCombinedShortFlags(subcmd string, policy *subcommandPolicy, chars string) error {
	for _, ch := range chars {
		flag := "-" + string(ch)
		argType, known := policy.safeFlags[flag]
		if !known || argType != flagNone {
			return fmt.Errorf("git %s flag %q is not allowed in restricted mode", subcmd, flag)
		}
	}
	return nil
}

// ── global arg checks ───────────────────────────────────────────────

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

var gitSecretValuePatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\bauthorization\s*[:=]\s*bearer\s+[A-Za-z0-9._~+/=-]+`),
	regexp.MustCompile(`(?i)\bbearer\s+[A-Za-z0-9._~+/=-]+`),
	regexp.MustCompile(`(?i)\b(api[_-]?key|access[_-]?token|refresh[_-]?token|authorization|password|secret)\s*([:=])\s*["']?[^"'\s,;]+`),
	regexp.MustCompile(`(?i)(https?|ssh|git)://[^/\s:@]+:[^@\s/]+@`),
}

var blockedCommitFlags = map[string]bool{
	"--no-verify":           true,
	"--gpg-sign":            true,
	"--no-gpg-sign":         true,
	"-S":                    true,
	"--signoff":             true,
	"--fixup":               true,
	"--squash":              true,
	"-C":                    true,
	"-c":                    true,
	"--author":              true,
	"--date":                true,
	"--allow-empty":         true,
	"--allow-empty-message": true,
	"-e":                    true,
	"--edit":                true,
}

func gitExecute(env *Env, ctx context.Context, argsJSON string) (string, error) {
	// Unconfined lifts the read gate for content-emitting subcommands
	// (diff/show/grep of sensitive paths), but write-side sensitive guards
	// and output redaction stay on in every mode.
	allowSensitiveReads := env.BypassToolHardProtections()
	invocation, err := parseGitInvocation(argsJSON, allowSensitiveReads)
	if err != nil {
		return "", err
	}
	// Structured output for git status.
	if invocation.Subcommand == "status" {
		return gitStatus(env, ctx, invocation.Args)
	}

	subcmdParts := strings.Fields(invocation.Subcommand)
	gitArgs := append([]string{"--no-optional-locks"}, subcmdParts...)
	gitArgs = append(gitArgs, invocation.Args...)
	if invocation.Subcommand == "add" {
		pathspecs, err := normalizeExplicitGitPathspecs(invocation.Subcommand, invocation.Args, false)
		if err != nil {
			return "", err
		}
		if err := rejectSensitiveStagePathspecs(env, ctx, pathspecs); err != nil {
			return "", err
		}
		gitArgs = append([]string{"--no-optional-locks", "--literal-pathspecs", "add", "--"}, pathspecs...)
	} else if invocation.Subcommand == "restore --staged" {
		pathspecs, err := normalizeExplicitGitPathspecs(invocation.Subcommand, invocation.Args, true)
		if err != nil {
			return "", err
		}
		gitArgs = append([]string{"--no-optional-locks", "--literal-pathspecs", "restore", "--staged", "--"}, pathspecs...)
	} else if invocation.Subcommand == "push" {
		normalized, err := normalizePushArgs(env, ctx, invocation.Args)
		if err != nil {
			return "", err
		}
		gitArgs = append([]string{"--no-optional-locks", "push"}, normalized...)
	} else if invocation.Subcommand == "commit" {
		if err := rejectSensitiveStagedCommitPaths(env, ctx); err != nil {
			return "", err
		}
		if env.gitAttributionEnabled() {
			gitArgs, _ = gitattribution.AddToCommitArgs(gitArgs)
		}
	}

	return runGit(env, ctx, invocation.Subcommand, gitArgs)
}

// parseGitInvocation validates a git tool call. allowSensitiveReads lifts
// the read-side gate for content-emitting subcommands (unconfined mode);
// write-side sensitive guards (pathspec staging, commit message files) are
// always enforced regardless of mode.
func parseGitInvocation(argsJSON string, allowSensitiveReads bool) (gitInvocation, error) {
	var args struct {
		Subcommand string   `json:"subcommand"`
		Args       []string `json:"args"`
	}
	if err := decodeArgs(argsJSON, &args); err != nil {
		return gitInvocation{}, err
	}
	if strings.TrimSpace(args.Subcommand) == "" {
		return gitInvocation{}, errors.New("git requires subcommand")
	}

	subcmd := strings.TrimSpace(args.Subcommand)
	remainingArgs := args.Args

	// Try multi-word subcommand matching (check both maps).
	if !allowedGitSubcommands[subcmd] && policiedSubcommands[subcmd] == nil && len(remainingArgs) > 0 {
		combined := subcmd + " " + remainingArgs[0]
		if allowedGitSubcommands[combined] || policiedSubcommands[combined] != nil {
			subcmd = combined
			remainingArgs = remainingArgs[1:]
		}
	}

	// Run global arg checks (blocked prefixes + shell metacharacters)
	// regardless of which path we take.
	if err := validateGlobalGitArgs(subcmd, remainingArgs); err != nil {
		return gitInvocation{}, err
	}

	// Dispatch: policied → flag-level validation, allowed → legacy validation.
	if policy := policiedSubcommands[subcmd]; policy != nil {
		if err := validateSubcommandFlags(subcmd, policy, remainingArgs); err != nil {
			return gitInvocation{}, err
		}
	} else if allowedGitSubcommands[subcmd] {
		if err := validateGitArgs(subcmd, remainingArgs, false); err != nil {
			return gitInvocation{}, err
		}
	} else {
		return gitInvocation{}, fmt.Errorf("git subcommand %q is not allowed in restricted mode", args.Subcommand)
	}
	if err := validateSensitiveGitContentArgs(subcmd, remainingArgs, allowSensitiveReads); err != nil {
		return gitInvocation{}, err
	}

	return gitInvocation{
		Subcommand: subcmd,
		Args:       remainingArgs,
	}, nil
}

// runGit executes a git command and returns the standard JSON envelope.
func runGit(env *Env, ctx context.Context, subcmd string, gitArgs []string) (string, error) {
	// Worktree-bound threads run git inside the isolated checkout; the
	// command policy checks above already ran against the ordinary rules.
	workDir, err := env.ExecRootDir(ctx)
	if err != nil {
		return "", err
	}
	runCtx, cancel := context.WithTimeout(ctx, gitTimeout)
	defer cancel()

	cmd := exec.CommandContext(runCtx, "git", gitArgs...)
	cmd.Dir = workDir
	cmd.Env = mergeEnv(os.Environ(), nonInteractiveShellEnv())

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err = cmd.Run()
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

	output, redacted := sanitizeGitOutput(subcmd, stdout.String()+stderr.String(), false)
	trimmed, truncated := truncate(output, maxShellOutputBytes)

	result := map[string]any{
		"action":             gitResultAction(subcmd),
		"subcommand":         subcmd,
		"exit_code":          exitCode,
		"output":             trimmed,
		"timed_out":          timedOut,
		"workspace_revision": workspaceRevision(ctx, env.RevisionRoot(ctx)),
		"next_suggestions":   gitNextSuggestions(subcmd, exitCode, timedOut),
	}
	if subcmd == "commit" && exitCode == 0 && !timedOut {
		if commit, err := latestCommitMetadata(env, ctx); err == nil {
			result["commit_sha"] = commit.SHA
			result["commit_subject"] = commit.Subject
		}
	}
	if (subcmd == "add" || subcmd == "restore --staged") && exitCode == 0 && !timedOut {
		if staged, unstaged, untracked, err := gitStatusSnapshot(env, ctx); err == nil {
			result["staged"] = staged
			result["unstaged"] = unstaged
			result["untracked"] = untracked
		}
	}
	if truncated {
		result["truncated"] = true
	}
	if redacted {
		result["redacted"] = true
	}
	return mustJSON(result)
}

func gitResultAction(subcmd string) string {
	parts := strings.Fields(strings.ToLower(strings.TrimSpace(subcmd)))
	cleaned := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimLeft(part, "-")
		var b strings.Builder
		for _, r := range part {
			switch {
			case r >= 'a' && r <= 'z':
				b.WriteRune(r)
			case r >= '0' && r <= '9':
				b.WriteRune(r)
			default:
				b.WriteByte('_')
			}
		}
		if value := strings.Trim(b.String(), "_"); value != "" {
			cleaned = append(cleaned, value)
		}
	}
	if len(cleaned) == 0 {
		return "git"
	}
	return strings.Join(cleaned, "_")
}

type gitCommitMetadata struct {
	SHA     string
	Subject string
}

func latestCommitMetadata(env *Env, ctx context.Context) (gitCommitMetadata, error) {
	workDir, err := env.ExecRootDir(ctx)
	if err != nil {
		return gitCommitMetadata{}, err
	}
	runCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(runCtx, "git", "--no-optional-locks", "log", "-1", "--format=%H%x00%s")
	cmd.Dir = workDir
	cmd.Env = mergeEnv(os.Environ(), nonInteractiveShellEnv())
	out, err := cmd.Output()
	if err != nil {
		return gitCommitMetadata{}, fmt.Errorf("read latest commit: %w", err)
	}
	parts := strings.SplitN(strings.TrimRight(string(out), "\n"), "\x00", 2)
	if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" {
		return gitCommitMetadata{}, errors.New("latest commit metadata missing")
	}
	return gitCommitMetadata{SHA: strings.TrimSpace(parts[0]), Subject: strings.TrimSpace(parts[1])}, nil
}

// ── structured git status ───────────────────────────────────────────

// gitStatus runs git status --porcelain=v1 -z and returns structured output
// with staged, unstaged, and untracked file lists.
func gitStatus(env *Env, ctx context.Context, userArgs []string) (string, error) {
	// NUL-delimited paths preserve filenames independently of core.quotePath.
	gitArgs := []string{"--no-optional-locks", "status", "--porcelain=v1", "-z"}
	for i := 0; i < len(userArgs); i++ {
		switch userArgs[i] {
		case "-u", "--untracked-files":
			if i+1 < len(userArgs) {
				gitArgs = append(gitArgs, userArgs[i], userArgs[i+1])
				i++
			}
		case "--ignore-submodules":
			if i+1 < len(userArgs) {
				gitArgs = append(gitArgs, userArgs[i], userArgs[i+1])
				i++
			}
		case "--find-renames", "-M":
			if i+1 < len(userArgs) {
				gitArgs = append(gitArgs, userArgs[i], userArgs[i+1])
				i++
			}
		case "--renames", "--no-renames", "--ignored":
			gitArgs = append(gitArgs, userArgs[i])
		default:
			// Handle --flag=value forms for behavior flags.
			for _, prefix := range []string{"--untracked-files=", "--ignore-submodules=", "--find-renames=", "-M"} {
				if strings.HasPrefix(userArgs[i], prefix) {
					gitArgs = append(gitArgs, userArgs[i])
					break
				}
			}
		}
	}

	workDir, err := env.ExecRootDir(ctx)
	if err != nil {
		return "", err
	}
	runCtx, cancel := context.WithTimeout(ctx, gitTimeout)
	defer cancel()

	cmd := exec.CommandContext(runCtx, "git", gitArgs...)
	cmd.Dir = workDir
	cmd.Env = mergeEnv(os.Environ(), nonInteractiveShellEnv())

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err = cmd.Run()
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
			return "", fmt.Errorf("git status: %w", err)
		}
	}

	staged, unstaged, untracked := parseGitPorcelain(stdout.String())

	rawOutput, redacted := sanitizeGitOutput("status", stdout.String()+stderr.String(), false)
	trimmed, truncated := truncate(rawOutput, maxShellOutputBytes)

	result := map[string]any{
		"action":             "status",
		"subcommand":         "status",
		"exit_code":          exitCode,
		"staged":             staged,
		"unstaged":           unstaged,
		"untracked":          untracked,
		"output":             trimmed,
		"timed_out":          timedOut,
		"workspace_revision": workspaceRevision(ctx, env.RevisionRoot(ctx)),
		"next_suggestions":   gitStatusNextSuggestions(staged, unstaged, untracked, exitCode, timedOut),
	}
	if truncated {
		result["truncated"] = true
	}
	if redacted {
		result["redacted"] = true
	}
	return mustJSON(result)
}

func gitNextSuggestions(subcmd string, exitCode int, timedOut bool) []string {
	if timedOut {
		return []string{"retry with a narrower git command or inspect repository state with git status"}
	}
	if exitCode != 0 {
		return []string{"inspect the git output, then correct the command or run git status before retrying"}
	}
	switch subcmd {
	case "diff", "show", "log", "blame", "grep":
		return []string{"use this git output as evidence, or read_file relevant paths before editing"}
	case "add":
		return []string{"review staged changes with git diff --cached before committing"}
	case "restore --staged":
		return []string{"run git status to confirm the index now matches the intended staging set"}
	case "commit":
		return []string{"confirm git status is clean or contains only intentional remaining changes"}
	case "push":
		return []string{"confirm the remote update succeeded and report the pushed branch"}
	default:
		return []string{"use this git output as evidence for the next action"}
	}
}

func gitStatusNextSuggestions(staged, unstaged []fileEntry, untracked []string, exitCode int, timedOut bool) []string {
	if timedOut {
		return []string{"retry git status with narrower flags or inspect repository state with git diff"}
	}
	if exitCode != 0 {
		return []string{"inspect git status output and resolve repository errors before continuing"}
	}
	if len(staged) == 0 && len(unstaged) == 0 && len(untracked) == 0 {
		return []string{"working tree is clean; use prior validation evidence before finishing"}
	}
	if len(unstaged) > 0 || len(untracked) > 0 {
		return []string{"run git diff or read_file relevant paths before deciding what to stage, commit, or report"}
	}
	return []string{"review staged changes with git diff --cached before committing"}
}

type gitPorcelainRecord struct {
	x, y         byte
	path         string
	originalPath string
}

// parseGitPorcelainRecords reads porcelain v1 -z records. Renames and copies
// put the destination first and the source in a second NUL-delimited field.
func parseGitPorcelainRecords(output string) []gitPorcelainRecord {
	var records []gitPorcelainRecord
	for output != "" {
		var record string
		record, output, _ = strings.Cut(output, "\x00")
		if len(record) < 4 {
			continue
		}
		entry := gitPorcelainRecord{x: record[0], y: record[1], path: record[3:]}
		if strings.ContainsAny(record[:2], "RC") {
			entry.originalPath, output, _ = strings.Cut(output, "\x00")
		}
		records = append(records, entry)
	}
	return records
}

// parseGitPorcelain parses `git status --porcelain=v1 -z` output into
// structured staged, unstaged, and untracked file lists.
func parseGitPorcelain(output string) (staged, unstaged []fileEntry, untracked []string) {
	staged = []fileEntry{}
	unstaged = []fileEntry{}
	untracked = []string{}

	for _, record := range parseGitPorcelainRecords(output) {
		x, y := record.x, record.y
		filename := record.path

		if x == '?' && y == '?' {
			untracked = append(untracked, filename)
			continue
		}
		if x == '!' && y == '!' {
			continue // ignored
		}
		if x != ' ' && x != '?' {
			staged = append(staged, fileEntry{
				File:   filename,
				Status: statusDescription(x),
			})
		}
		if y != ' ' && y != '?' {
			unstaged = append(unstaged, fileEntry{
				File:   filename,
				Status: statusDescription(y),
			})
		}
	}
	return
}

// statusDescription maps a porcelain status character to a human-readable string.
func statusDescription(code byte) string {
	switch code {
	case 'A':
		return "added"
	case 'M':
		return "modified"
	case 'D':
		return "deleted"
	case 'R':
		return "renamed"
	case 'C':
		return "copied"
	case 'T':
		return "type_changed"
	case 'U':
		return "unmerged"
	default:
		return "unknown"
	}
}

// validateGlobalGitArgs checks blocked global arg prefixes and shell
// metacharacters. Called for all subcommands before specific validation.
func validateGlobalGitArgs(subcmd string, args []string) error {
	for i, arg := range args {
		for _, prefix := range blockedGlobalArgPrefixes {
			if arg == prefix || strings.HasPrefix(arg, prefix+"=") {
				return fmt.Errorf("git arg %q is not allowed", arg)
			}
		}
		for _, ch := range shellMetacharacters {
			if strings.ContainsRune(arg, ch) && !isCommitMessageArg(subcmd, args, i) && !isExplicitGitPathArg(subcmd, arg) {
				return fmt.Errorf("git arg %q contains blocked metacharacter %q", arg, string(ch))
			}
		}
	}
	return nil
}

// validateGitArgs runs subcommand-specific validation for non-policied
// subcommands (commit, push, and everything else in allowedGitSubcommands).
func validateGitArgs(subcmd string, args []string, allowSensitive bool) error {
	switch subcmd {
	case "add":
		_, err := normalizeExplicitGitPathspecs(subcmd, args, allowSensitive)
		return err
	case "restore --staged":
		_, err := normalizeExplicitGitPathspecs(subcmd, args, true)
		return err
	case "commit":
		return validateCommitArgs(args, allowSensitive)
	case "push":
		return validatePushArgs(args)
	case "cat-file":
		return validateCatFileArgs(args)
	default:
		if hasDangerousGlobalConfigArgs(args) {
			return errors.New("git global config overrides are not allowed")
		}
	}
	return nil
}

func normalizeExplicitGitPathspecs(subcmd string, args []string, allowSensitive bool) ([]string, error) {
	var pathspecs []string
	seenDashDash := false
	for _, raw := range args {
		if raw == "--" {
			if seenDashDash {
				return nil, fmt.Errorf("git %s accepts at most one -- path separator", subcmd)
			}
			seenDashDash = true
			continue
		}
		arg := strings.TrimSpace(raw)
		if arg == "" {
			return nil, fmt.Errorf("git %s requires non-empty explicit path arguments", subcmd)
		}
		if strings.HasPrefix(arg, "-") {
			return nil, fmt.Errorf("git %s only accepts explicit file or directory paths; flag %q is not allowed", subcmd, arg)
		}
		if filepath.IsAbs(arg) {
			return nil, fmt.Errorf("git %s requires workspace-relative paths, got absolute path %q", subcmd, arg)
		}
		if strings.ContainsAny(arg, "*?[") || strings.HasPrefix(arg, ":") || strings.Contains(arg, ":(") {
			return nil, fmt.Errorf("git %s only accepts literal paths from git status; wildcard or pathspec magic %q is not allowed", subcmd, arg)
		}
		cleaned := path.Clean(filepath.ToSlash(arg))
		if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
			return nil, fmt.Errorf("git %s requires explicit workspace paths; root, current-directory, and parent traversal pathspecs are not allowed", subcmd)
		}
		if !allowSensitive {
			if reason, ok := sensitivePathReason(cleaned); ok {
				return nil, fmt.Errorf("git %s refuses sensitive path %q (%s). Ask the user for explicit secret handling before staging", subcmd, cleaned, reason)
			}
		}
		// Validation may trim whitespace conservatively, but execution must use
		// the exact filename returned by status, including leading/trailing space.
		pathspecs = append(pathspecs, raw)
	}
	if len(pathspecs) == 0 {
		return nil, fmt.Errorf("git %s requires at least one explicit file or directory path from git status", subcmd)
	}
	return pathspecs, nil
}

func validateCatFileArgs(args []string) error {
	if len(args) == 0 {
		return errors.New("git cat-file requires a metadata mode")
	}
	modeSeen := false
	for _, arg := range args {
		switch arg {
		case "-t", "-s", "-e":
			if modeSeen {
				return errors.New("git cat-file accepts exactly one metadata mode")
			}
			modeSeen = true
		default:
			if strings.HasPrefix(arg, "-") {
				return fmt.Errorf("git cat-file flag %q is not allowed; only -t, -s, and -e metadata modes are allowed", arg)
			}
		}
	}
	if !modeSeen {
		return errors.New("git cat-file only allows -t, -s, and -e metadata modes")
	}
	return nil
}

func isCommitMessageArg(subcmd string, args []string, idx int) bool {
	if subcmd != "commit" || idx <= 0 || idx >= len(args) {
		if subcmd == "commit" && idx >= 0 && idx < len(args) {
			arg := args[idx]
			return strings.HasPrefix(arg, "-m") && arg != "-m" ||
				strings.HasPrefix(arg, "--message=")
		}
		return false
	}
	prev := args[idx-1]
	if prev == "-m" || prev == "--message" {
		return true
	}
	arg := args[idx]
	return strings.HasPrefix(arg, "-m") && arg != "-m" ||
		strings.HasPrefix(arg, "--message=")
}

func isExplicitGitPathArg(subcmd, arg string) bool {
	switch subcmd {
	case "add", "restore --staged":
		return arg != "--" && !strings.HasPrefix(strings.TrimSpace(arg), "-")
	default:
		return false
	}
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

func validateCommitArgs(args []string, allowSensitive bool) error {
	if len(args) == 0 {
		return errors.New("git commit requires an explicit message via -m/--message or -F/--file")
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
			if i+1 >= len(args) {
				return fmt.Errorf("git commit flag %q requires a message", arg)
			}
			messageSeen = true
			i++
			continue
		case "-F", "--file":
			if i+1 >= len(args) {
				return fmt.Errorf("git commit flag %q requires a message file", arg)
			}
			if err := validateCommitMessageFileArg(arg, args[i+1], allowSensitive); err != nil {
				return err
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
			messageSeen = true
			continue
		}
		if strings.HasPrefix(arg, "--file=") {
			if err := validateCommitMessageFileArg("--file", strings.TrimPrefix(arg, "--file="), allowSensitive); err != nil {
				return err
			}
			messageSeen = true
			continue
		}
		if arg == "--amend" {
			continue
		}
		if strings.HasPrefix(arg, "-") {
			return fmt.Errorf("git commit flag %q is not allowed in restricted mode", arg)
		}
		return fmt.Errorf("git commit only supports explicit non-interactive message flags; unexpected arg %q", arg)
	}
	if !messageSeen {
		return errors.New("git commit requires an explicit message via -m/--message or -F/--file")
	}
	return nil
}

func validateCommitMessageFileArg(flag, raw string, allowSensitive bool) error {
	value := strings.TrimSpace(raw)
	if value == "" {
		return fmt.Errorf("git commit flag %q requires a non-empty message file", flag)
	}
	if value == "-" {
		return fmt.Errorf("git commit flag %q cannot read the message from stdin in restricted mode", flag)
	}
	if filepath.IsAbs(value) {
		return fmt.Errorf("git commit flag %q requires a workspace-relative message file, got absolute path %q", flag, value)
	}
	cleaned := path.Clean(filepath.ToSlash(value))
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return fmt.Errorf("git commit flag %q requires a workspace-relative message file, got %q", flag, value)
	}
	if strings.ContainsAny(cleaned, "*?[") || strings.HasPrefix(cleaned, ":") || strings.Contains(cleaned, ":(") {
		return fmt.Errorf("git commit flag %q requires a literal message file path, got %q", flag, value)
	}
	if reason, ok := sensitivePathReason(cleaned); ok && !allowSensitive {
		return fmt.Errorf("git commit refuses message file %q (%s). Ask the user for explicit secret handling before committing", cleaned, reason)
	}
	return nil
}

// rejectSensitiveStagePathspecs refuses to stage sensitive paths in every
// permission mode, including unconfined: lifting the path boundary does not
// lift secret staging guards.
func rejectSensitiveStagePathspecs(env *Env, ctx context.Context, pathspecs []string) error {
	paths, err := changedPathsForPathspecs(env, ctx, pathspecs)
	if err != nil {
		return err
	}
	for _, path := range paths {
		if reason, ok := sensitivePathReason(path); ok {
			return fmt.Errorf("git add refuses sensitive path %q (%s). Ask the user for explicit secret handling before staging", path, reason)
		}
	}
	return nil
}

func changedPathsForPathspecs(env *Env, ctx context.Context, pathspecs []string) ([]string, error) {
	workDir, err := env.ExecRootDir(ctx)
	if err != nil {
		return nil, err
	}
	runCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	args := append([]string{"--no-optional-locks", "--literal-pathspecs", "status", "--porcelain=v1", "-z", "--untracked-files=all", "--"}, pathspecs...)
	cmd := exec.CommandContext(runCtx, "git", args...)
	cmd.Dir = workDir
	cmd.Env = mergeEnv(os.Environ(), nonInteractiveShellEnv())
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("inspect paths before git add: %w", err)
	}
	return parseGitPorcelainZPaths(string(out)), nil
}

func parseGitPorcelainZPaths(output string) []string {
	var paths []string
	for _, record := range parseGitPorcelainRecords(output) {
		paths = append(paths, record.path)
		if record.originalPath != "" {
			paths = append(paths, record.originalPath)
		}
	}
	return paths
}

func gitStatusSnapshot(env *Env, ctx context.Context) (staged, unstaged []fileEntry, untracked []string, err error) {
	workDir, err := env.ExecRootDir(ctx)
	if err != nil {
		return nil, nil, nil, err
	}
	runCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	cmd := exec.CommandContext(runCtx, "git", "--no-optional-locks", "status", "--porcelain=v1", "-z")
	cmd.Dir = workDir
	cmd.Env = mergeEnv(os.Environ(), nonInteractiveShellEnv())
	out, err := cmd.Output()
	if err != nil {
		return nil, nil, nil, fmt.Errorf("read git status snapshot: %w", err)
	}
	staged, unstaged, untracked = parseGitPorcelain(string(out))
	return staged, unstaged, untracked, nil
}

// rejectSensitiveStagedCommitPaths refuses to commit staged sensitive paths
// in every permission mode, including unconfined: lifting the path boundary
// does not lift secret commit guards.
func rejectSensitiveStagedCommitPaths(env *Env, ctx context.Context) error {
	workDir, err := env.ExecRootDir(ctx)
	if err != nil {
		return err
	}
	runCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	cmd := exec.CommandContext(runCtx, "git", "--no-optional-locks", "diff", "--cached", "--name-only", "-z")
	cmd.Dir = workDir
	cmd.Env = mergeEnv(os.Environ(), nonInteractiveShellEnv())
	out, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("inspect staged paths before commit: %w", err)
	}
	for _, raw := range strings.Split(string(out), "\x00") {
		path := strings.TrimSpace(raw)
		if path == "" {
			continue
		}
		if reason, ok := sensitivePathReason(path); ok {
			return fmt.Errorf("git commit refuses staged sensitive path %q (%s). Ask the user for explicit secret handling before committing", path, reason)
		}
	}
	return nil
}

func validateSensitiveGitContentArgs(subcmd string, args []string, allowSensitive bool) error {
	if allowSensitive {
		return nil
	}
	if !gitSubcommandCanEmitFileContent(subcmd) {
		return nil
	}
	for _, arg := range args {
		for _, candidate := range sensitiveGitPathCandidates(arg) {
			if reason, ok := sensitivePathReason(candidate); ok {
				return fmt.Errorf("git %s refuses to inspect sensitive path %q (%s). Use metadata-safe git commands or ask the user for explicit secret handling", subcmd, candidate, reason)
			}
		}
	}
	return nil
}

func gitSubcommandCanEmitFileContent(subcmd string) bool {
	switch subcmd {
	case "diff", "show", "blame", "grep", "cat-file", "stash show", "log":
		return true
	default:
		return false
	}
}

func sensitiveGitPathCandidates(arg string) []string {
	token := strings.Trim(strings.TrimSpace(arg), `"'`)
	if token == "" || token == "--" {
		return nil
	}
	if strings.HasPrefix(token, "--") && !strings.Contains(token, "=") {
		return nil
	}
	var candidates []string
	if idx := strings.LastIndex(token, ":"); idx >= 0 && idx+1 < len(token) {
		candidates = append(candidates, token[idx+1:])
	}
	if strings.HasPrefix(token, ":(top)") {
		candidates = append(candidates, strings.TrimPrefix(token, ":(top)"))
	} else if strings.HasPrefix(token, ":(") {
		if idx := strings.Index(token, ")"); idx >= 0 && idx+1 < len(token) {
			candidates = append(candidates, token[idx+1:])
		}
	}
	if !strings.HasPrefix(token, "-") {
		candidates = append(candidates, token)
	}
	return candidates
}

func sanitizeGitOutput(subcmd, output string, allowSensitive bool) (string, bool) {
	if allowSensitive {
		return output, false
	}
	redacted := false
	if gitSubcommandMayReturnDiff(subcmd) {
		var changed bool
		output, changed = redactSensitiveDiffBlocks(output)
		redacted = redacted || changed
	}
	if subcmd == "grep" {
		var changed bool
		output, changed = redactSensitiveGitGrepLines(output)
		redacted = redacted || changed
	}
	for _, pattern := range gitSecretValuePatterns {
		next := pattern.ReplaceAllStringFunc(output, func(match string) string {
			redacted = true
			lower := strings.ToLower(match)
			switch {
			case strings.HasPrefix(lower, "bearer "):
				return "Bearer [REDACTED]"
			case strings.Contains(match, "://") && strings.Contains(match, "@"):
				if idx := strings.Index(match, "://"); idx >= 0 {
					return match[:idx+3] + "[REDACTED]@"
				}
			}
			return redactKeyValueMatch(match)
		})
		output = next
	}
	return output, redacted
}

func redactKeyValueMatch(match string) string {
	for _, sep := range []string{":", "="} {
		if idx := strings.Index(match, sep); idx >= 0 {
			return strings.TrimSpace(match[:idx]) + sep + "[REDACTED]"
		}
	}
	return "[REDACTED]"
}

func gitSubcommandMayReturnDiff(subcmd string) bool {
	switch subcmd {
	case "diff", "show", "log", "stash show":
		return true
	default:
		return false
	}
}

func redactSensitiveDiffBlocks(output string) (string, bool) {
	if !strings.Contains(output, "diff --git ") {
		return output, false
	}
	lines := strings.SplitAfter(output, "\n")
	var b strings.Builder
	redacted := false
	for i := 0; i < len(lines); {
		if !strings.HasPrefix(lines[i], "diff --git ") {
			b.WriteString(lines[i])
			i++
			continue
		}
		start := i
		i++
		for i < len(lines) && !strings.HasPrefix(lines[i], "diff --git ") {
			i++
		}
		block := strings.Join(lines[start:i], "")
		if path, reason, ok := sensitiveDiffBlockPath(block); ok {
			b.WriteString("diff --git [REDACTED sensitive path]\n")
			b.WriteString(fmt.Sprintf("[REDACTED git diff for sensitive path %q (%s)]\n", path, reason))
			redacted = true
			continue
		}
		b.WriteString(block)
	}
	return b.String(), redacted
}

func sensitiveDiffBlockPath(block string) (string, string, bool) {
	for _, line := range strings.Split(block, "\n") {
		for _, candidate := range gitDiffPathCandidates(line) {
			if reason, ok := sensitivePathReason(candidate); ok {
				return candidate, reason, true
			}
		}
	}
	return "", "", false
}

func gitDiffPathCandidates(line string) []string {
	line = strings.TrimSpace(line)
	switch {
	case strings.HasPrefix(line, "diff --git "):
		fields := strings.Fields(line)
		if len(fields) >= 4 {
			return []string{cleanGitDiffPathToken(fields[2]), cleanGitDiffPathToken(fields[3])}
		}
	case strings.HasPrefix(line, "--- ") || strings.HasPrefix(line, "+++ "):
		fields := strings.Fields(line)
		if len(fields) >= 2 {
			return []string{cleanGitDiffPathToken(fields[1])}
		}
	case strings.HasPrefix(line, "rename from "):
		return []string{cleanGitDiffPathToken(strings.TrimPrefix(line, "rename from "))}
	case strings.HasPrefix(line, "rename to "):
		return []string{cleanGitDiffPathToken(strings.TrimPrefix(line, "rename to "))}
	}
	return nil
}

func cleanGitDiffPathToken(token string) string {
	token = strings.Trim(strings.TrimSpace(token), `"'`)
	if token == "/dev/null" {
		return ""
	}
	token = strings.TrimPrefix(token, "a/")
	token = strings.TrimPrefix(token, "b/")
	return token
}

func redactSensitiveGitGrepLines(output string) (string, bool) {
	lines := strings.SplitAfter(output, "\n")
	redacted := false
	for i, line := range lines {
		body := strings.TrimRight(line, "\r\n")
		newline := strings.TrimPrefix(line, body)
		if idx := strings.Index(body, ":"); idx > 0 {
			path := body[:idx]
			if reason, ok := sensitivePathReason(path); ok {
				lines[i] = fmt.Sprintf("%s:[REDACTED git grep output for sensitive path (%s)]%s", path, reason, newline)
				redacted = true
			}
		}
	}
	return strings.Join(lines, ""), redacted
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

func normalizePushArgs(env *Env, ctx context.Context, args []string) ([]string, error) {
	if len(args) == 0 {
		return nil, nil
	}
	branch, err := currentBranch(env, ctx)
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

func currentBranch(env *Env, ctx context.Context) (string, error) {
	workDir, err := env.ExecRootDir(ctx)
	if err != nil {
		return "", err
	}
	runCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(runCtx, "git", "--no-optional-locks", "rev-parse", "--abbrev-ref", "HEAD")
	cmd.Dir = workDir
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
