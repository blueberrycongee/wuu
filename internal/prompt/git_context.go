package prompt

import (
	"fmt"
	"os/exec"
	"strings"
	"sync"
)

// GitContext caches git status information for the session.
// Call Invalidate() after git-mutating tools to refresh on next read.
type GitContext struct {
	rootDir string
	mu      sync.Mutex
	cached  string
	valid   bool
}

// NewGitContext creates a git context collector for rootDir.
func NewGitContext(rootDir string) *GitContext {
	return &GitContext{rootDir: rootDir}
}

// Collect returns the git context string, caching it for the session.
func (g *GitContext) Collect() string {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.valid {
		return g.cached
	}
	g.cached = collectGitContext(g.rootDir)
	g.valid = true
	return g.cached
}

// Invalidate clears the cache so the next Collect() runs fresh.
func (g *GitContext) Invalidate() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.valid = false
}

func collectGitContext(rootDir string) string {
	var parts []string

	if branch := gitCmd(rootDir, "rev-parse", "--abbrev-ref", "HEAD"); branch != "" {
		parts = append(parts, fmt.Sprintf("Branch: %s", branch))
	}

	if status := gitCmd(rootDir, "status", "--short"); status != "" {
		// Cap status at 2000 chars to avoid explosion on dirty repos.
		if len(status) > 2000 {
			status = status[:2000] + "\n..."
		}
		parts = append(parts, fmt.Sprintf("Status:\n%s", status))
	} else {
		parts = append(parts, "Status: clean")
	}

	if log := gitCmd(rootDir, "log", "--oneline", "-5"); log != "" {
		parts = append(parts, fmt.Sprintf("Recent commits:\n%s", log))
	}

	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, "\n\n")
}

func gitCmd(dir string, args ...string) string {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
