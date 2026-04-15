package skills

import (
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/blueberrycongee/wuu/internal/stringutil"
)

// ProcessOptions controls how a skill body is transformed before being
// shown to the model.
type ProcessOptions struct {
	// Arguments substituted into ${ARGUMENTS} placeholders.
	Arguments string
	// SkillDir substituted into ${CLAUDE_SKILL_DIR} and used as cwd
	// for inline shell commands.
	SkillDir string
	// SessionID substituted into ${CLAUDE_SESSION_ID}.
	SessionID string
	// Shell to use for inline command execution. Defaults to "sh".
	Shell string
	// PerCmdTimeout caps each individual inline command. Defaults to 10s.
	PerCmdTimeout time.Duration
	// MaxOutputBytes caps the captured stdout per command. Defaults to 32KB.
	MaxOutputBytes int
	// AllowInlineShell controls whether `!cmd` and ```! blocks are
	// executed. When false, they are passed through unchanged. Defaults
	// to true. Set false for skills loaded from untrusted sources.
	AllowInlineShell bool
	// disableShell is a private flag used by ProcessSkillBody to skip
	// shell execution entirely (used internally when AllowInlineShell
	// is explicitly set false via the public API).
	disableShell bool
}

// ProcessSkillBody applies all transformations a skill body needs before
// being delivered to the model:
//  1. Variable substitution (${ARGUMENTS}, ${CLAUDE_SKILL_DIR}, ${CLAUDE_SESSION_ID})
//  2. Inline shell command execution (`!cmd` and ```! ... ``` blocks)
//
// The function never returns an error: shell failures are inlined as
// "[error: ...]" markers so a single bad command doesn't break the whole
// skill load. Variable substitution happens BEFORE shell execution so
// commands can reference ${ARGUMENTS} etc.
func ProcessSkillBody(ctx context.Context, body string, opts ProcessOptions) string {
	// Variable substitution first.
	body = substituteVariables(body, opts)

	// Inline shell execution.
	if !opts.disableShell && opts.AllowInlineShell {
		body = executeInlineCommands(ctx, body, opts)
	}
	return body
}

// substituteVariables replaces the three CC-compatible placeholders.
func substituteVariables(body string, opts ProcessOptions) string {
	r := strings.NewReplacer(
		"${ARGUMENTS}", opts.Arguments,
		"${CLAUDE_SKILL_DIR}", opts.SkillDir,
		"${CLAUDE_SESSION_ID}", opts.SessionID,
	)
	return r.Replace(body)
}

// inlineBacktickRe matches `!cmd` (single-line, command between backticks
// starting with !). The command body cannot contain backticks.
var inlineBacktickRe = regexp.MustCompile("`!([^`\n]+)`")

// codeBlockRe matches ```!\n...\n``` (multi-line, optional language tag
// after !).
var codeBlockRe = regexp.MustCompile("(?s)```!\\s*\n(.*?)\n```")

// executeInlineCommands runs all inline shell commands in body and
// substitutes them with their stdout (or an error marker on failure).
func executeInlineCommands(ctx context.Context, body string, opts ProcessOptions) string {
	if opts.PerCmdTimeout <= 0 {
		opts.PerCmdTimeout = 10 * time.Second
	}
	if opts.MaxOutputBytes <= 0 {
		opts.MaxOutputBytes = 32 * 1024
	}
	if opts.Shell == "" {
		opts.Shell = "sh"
	}

	run := func(cmd string) string {
		cmd = strings.TrimSpace(cmd)
		if cmd == "" {
			return ""
		}
		cctx, cancel := context.WithTimeout(ctx, opts.PerCmdTimeout)
		defer cancel()
		c := exec.CommandContext(cctx, opts.Shell, "-c", cmd)
		if opts.SkillDir != "" {
			c.Dir = opts.SkillDir
		}
		out, err := c.CombinedOutput()
		if cctx.Err() == context.DeadlineExceeded {
			return fmt.Sprintf("[error: command timed out after %s: %s]", opts.PerCmdTimeout, cmd)
		}
		if err != nil {
			return fmt.Sprintf("[error: %v]\n%s", err, truncateOutput(string(out), opts.MaxOutputBytes))
		}
		return truncateOutput(string(out), opts.MaxOutputBytes)
	}

	// Replace code blocks first (they're greedier and would otherwise
	// confuse the inline backtick regex if they share content).
	body = codeBlockRe.ReplaceAllStringFunc(body, func(match string) string {
		groups := codeBlockRe.FindStringSubmatch(match)
		if len(groups) < 2 {
			return match
		}
		return run(groups[1])
	})

	// Then inline backticks.
	body = inlineBacktickRe.ReplaceAllStringFunc(body, func(match string) string {
		groups := inlineBacktickRe.FindStringSubmatch(match)
		if len(groups) < 2 {
			return match
		}
		out := run(groups[1])
		// Strip trailing newline so inline substitution stays on one line.
		out = strings.TrimRight(out, "\n")
		return out
	})

	return body
}

// truncateOutput caps a command's output to maxBytes and appends a marker.
func truncateOutput(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	return stringutil.Truncate(s, maxBytes, fmt.Sprintf("\n... [truncated, %d more bytes]", len(s)-maxBytes))
}
