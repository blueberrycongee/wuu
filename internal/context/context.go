// Package context provides per-turn dynamic context injection for the
// agent loop. It generates environment information (CWD, date, git
// status) that gets injected as <system-reminder> blocks in user
// messages, keeping the system prompt stable for prompt caching.
//
// Design aligned with Claude Code's getSystemContext() +
// getUserContext() dual-path injection architecture:
//   - System prompt = static role, rules, instructions (cacheable)
//   - User context = dynamic environment info, memory, skills (per-turn)
package context

import (
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// EnvInfo holds the dynamic environment snapshot for one turn.
type EnvInfo struct {
	CWD       string
	Date      string
	GitBranch string
	GitStatus string // short summary, not full porcelain
}

// Snapshot captures the current environment state. Safe to call from
// any goroutine; all data comes from the OS / git CLI.
func Snapshot(cwd string) EnvInfo {
	info := EnvInfo{
		CWD:  cwd,
		Date: time.Now().Format("2006-01-02"),
	}
	if branch, err := gitBranch(cwd); err == nil {
		info.GitBranch = branch
	}
	if status, err := gitStatusSummary(cwd); err == nil {
		info.GitStatus = status
	}
	return info
}

// FormatSystemReminder formats environment info and optional extra
// context sections (memory, skills) into a <system-reminder> block
// suitable for injection into a user message.
func FormatSystemReminder(env EnvInfo, sections ...string) string {
	var b strings.Builder

	// Environment section
	b.WriteString("# Environment\n")
	b.WriteString(fmt.Sprintf("- CWD: %s\n", env.CWD))
	b.WriteString(fmt.Sprintf("- Date: %s\n", env.Date))
	if env.GitBranch != "" {
		b.WriteString(fmt.Sprintf("- Git branch: %s\n", env.GitBranch))
	}
	if env.GitStatus != "" {
		b.WriteString(fmt.Sprintf("- Git status: %s\n", env.GitStatus))
	}

	// Append extra sections (memory, skills, etc.)
	for _, sec := range sections {
		sec = strings.TrimSpace(sec)
		if sec != "" {
			b.WriteString("\n")
			b.WriteString(sec)
			b.WriteString("\n")
		}
	}

	return "<system-reminder>\n" + strings.TrimRight(b.String(), "\n") + "\n</system-reminder>"
}

// ── git helpers ────────────────────────────────────────────────────

func gitBranch(cwd string) (string, error) {
	cmd := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD")
	cmd.Dir = cwd
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	branch := strings.TrimSpace(string(out))
	if branch == "" || branch == "HEAD" {
		return "(detached)", nil
	}
	return branch, nil
}

func gitStatusSummary(cwd string) (string, error) {
	cmd := exec.Command("git", "status", "--porcelain", "--short")
	cmd.Dir = cwd
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) == 1 && lines[0] == "" {
		return "clean", nil
	}
	if len(lines) > 10 {
		return fmt.Sprintf("%d changed files", len(lines)), nil
	}
	// For small diffs, show the summary
	modified, added, deleted, other := 0, 0, 0, 0
	for _, line := range lines {
		if len(line) < 2 {
			continue
		}
		switch {
		case line[0] == '?' || line[1] == '?':
			added++
		case line[0] == 'M' || line[1] == 'M':
			modified++
		case line[0] == 'D' || line[1] == 'D':
			deleted++
		case line[0] == 'A' || line[1] == 'A':
			added++
		default:
			other++
		}
	}
	var parts []string
	if modified > 0 {
		parts = append(parts, fmt.Sprintf("%d modified", modified))
	}
	if added > 0 {
		parts = append(parts, fmt.Sprintf("%d added", added))
	}
	if deleted > 0 {
		parts = append(parts, fmt.Sprintf("%d deleted", deleted))
	}
	if other > 0 {
		parts = append(parts, fmt.Sprintf("%d other", other))
	}
	if len(parts) == 0 {
		return "clean", nil
	}
	return strings.Join(parts, ", "), nil
}
