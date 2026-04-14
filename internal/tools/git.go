package tools

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
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

// ── subcommand whitelists ───────────────────────────────────────────

// allowedGitSubcommands is the whitelist of git subcommands that need
// no per-flag validation (inherently read-only or handled by their own
// switch-case in validateGitArgs).
var allowedGitSubcommands = map[string]bool{
	"log":           true,
	"show":          true,
	"diff":          true,
	"blame":         true,
	"reflog":        true,
	"stash list":    true,
	"stash show":    true,
	"ls-files":      true,
	"ls-remote":     true,
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

// policiedSubcommands require flag-level validation via subcommandPolicy.
var policiedSubcommands = map[string]*subcommandPolicy{
	"branch":        branchPolicy,
	"tag":           tagPolicy,
	"remote":        remotePolicy,
	"remote show":   remoteShowPolicy,
	"config --get":     configGetPolicy,
	"config --get-all": configGetPolicy,
	"config --list":    configListPolicy,
	"status":        statusPolicy,
}

// ── policy definitions (ported from CC readOnlyCommandValidation.ts) ─

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
		"-i": flagNone, "--ignore-case": flagNone,
	},
	isDangerous: branchIsDangerous,
}

var tagPolicy = &subcommandPolicy{
	safeFlags: map[string]flagArgType{
		"-l": flagNone, "--list": flagNone,
		"-n":            flagNumber,
		"--contains":    flagString, "--no-contains": flagString,
		"--merged":      flagString, "--no-merged": flagString,
		"--sort":        flagString, "--format": flagString,
		"--points-at":   flagString,
		"--column":      flagNone, "--no-column": flagNone,
		"-i":            flagNone, "--ignore-case": flagNone,
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
		"-z": flagNone, "--null": flagNone,
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
		"-z": flagNone, "--null": flagNone,
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
		"--long": flagNone,
		"--verbose": flagNone, "-v": flagNone,
		"--untracked-files": flagString, "-u": flagString,
		"--ignored":           flagNone,
		"--ignore-submodules": flagString,
		"--column": flagNone, "--no-column": flagNone,
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

func gitExecute(env *Env, ctx context.Context, argsJSON string) (string, error) {
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
		return "", err
	}

	// Dispatch: policied → flag-level validation, allowed → legacy validation.
	if policy := policiedSubcommands[subcmd]; policy != nil {
		if err := validateSubcommandFlags(subcmd, policy, remainingArgs); err != nil {
			return "", err
		}
	} else if allowedGitSubcommands[subcmd] {
		if err := validateGitArgs(subcmd, remainingArgs); err != nil {
			return "", err
		}
	} else {
		return "", fmt.Errorf("git subcommand %q is not allowed in restricted mode", args.Subcommand)
	}

	// Structured output for git status.
	if subcmd == "status" {
		return gitStatus(env, ctx, remainingArgs)
	}

	subcmdParts := strings.Fields(subcmd)
	gitArgs := append([]string{"--no-optional-locks"}, subcmdParts...)
	gitArgs = append(gitArgs, remainingArgs...)
	if subcmd == "push" {
		normalized, err := normalizePushArgs(env, ctx, remainingArgs)
		if err != nil {
			return "", err
		}
		gitArgs = append([]string{"--no-optional-locks", "push"}, normalized...)
	}

	return runGit(env, ctx, subcmd, gitArgs)
}

// runGit executes a git command and returns the standard JSON envelope.
func runGit(env *Env, ctx context.Context, subcmd string, gitArgs []string) (string, error) {
	runCtx, cancel := context.WithTimeout(ctx, gitTimeout)
	defer cancel()

	cmd := exec.CommandContext(runCtx, "git", gitArgs...)
	cmd.Dir = env.RootDir
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

// ── structured git status ───────────────────────────────────────────

// gitStatus runs git status --porcelain and returns structured output
// with staged, unstaged, and untracked file lists.
func gitStatus(env *Env, ctx context.Context, userArgs []string) (string, error) {
	// Build args: always use --porcelain, forward behavior-relevant flags.
	gitArgs := []string{"--no-optional-locks", "status", "--porcelain"}
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

	runCtx, cancel := context.WithTimeout(ctx, gitTimeout)
	defer cancel()

	cmd := exec.CommandContext(runCtx, "git", gitArgs...)
	cmd.Dir = env.RootDir
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
			return "", fmt.Errorf("git status: %w", err)
		}
	}

	staged, unstaged, untracked := parseGitPorcelain(stdout.String())

	rawOutput := stdout.String() + stderr.String()
	trimmed, truncated := truncate(rawOutput, maxShellOutputBytes)

	result := map[string]any{
		"subcommand": "status",
		"exit_code":  exitCode,
		"staged":     staged,
		"unstaged":   unstaged,
		"untracked":  untracked,
		"output":     trimmed,
		"timed_out":  timedOut,
	}
	if truncated {
		result["truncated"] = true
	}
	return mustJSON(result)
}

// parseGitPorcelain parses `git status --porcelain` output into
// structured staged, unstaged, and untracked file lists.
func parseGitPorcelain(output string) (staged, unstaged []fileEntry, untracked []string) {
	staged = []fileEntry{}
	unstaged = []fileEntry{}
	untracked = []string{}

	for _, line := range strings.Split(output, "\n") {
		if len(line) < 3 {
			continue
		}
		x := line[0] // index status
		y := line[1] // worktree status
		filename := strings.TrimLeftFunc(line[2:], unicode.IsSpace)

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
			if strings.ContainsRune(arg, ch) && !isCommitMessageArg(subcmd, args, i) {
				return fmt.Errorf("git arg %q contains blocked metacharacter %q", arg, string(ch))
			}
		}
	}
	return nil
}

// validateGitArgs runs subcommand-specific validation for non-policied
// subcommands (commit, push, and everything else in allowedGitSubcommands).
func validateGitArgs(subcmd string, args []string) error {
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
	runCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(runCtx, "git", "--no-optional-locks", "rev-parse", "--abbrev-ref", "HEAD")
	cmd.Dir = env.RootDir
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
