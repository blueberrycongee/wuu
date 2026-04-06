package tui

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// ---------------------------------------------------------------------------
// Command registry types
// ---------------------------------------------------------------------------

type commandType string

const (
	cmdTypeLocal  commandType = "local"
	cmdTypePrompt commandType = "prompt"
)

type command struct {
	Name        string
	Aliases     []string
	Description string
	ArgHint     string
	InlineArgs  bool
	Hidden      bool
	Type        commandType
	Execute     func(args string, m *Model) string
}

var commandRegistry []command

func init() {
	commandRegistry = []command{
		{Name: "help", Description: "Show available commands", Type: cmdTypeLocal, Execute: cmdHelp},
		{Name: "clear", Description: "Clear screen", Type: cmdTypeLocal, Execute: cmdClear},
		{Name: "status", Description: "Show session config and token usage", Type: cmdTypeLocal, Execute: cmdStatus},
		{Name: "compact", Description: "Compress conversation context", Type: cmdTypeLocal, Execute: cmdCompact},
		{Name: "model", Description: "Switch model/provider", ArgHint: "<model-name>", InlineArgs: true, Type: cmdTypeLocal, Execute: cmdModelSwitch},
		{Name: "resume", Description: "Resume previous session", Type: cmdTypeLocal, Execute: cmdResume},
		{Name: "fork", Description: "Fork current session", Type: cmdTypeLocal, Execute: cmdFork},
		{Name: "new", Description: "Start new conversation", Type: cmdTypeLocal, Execute: cmdNew},
		{Name: "diff", Description: "Show git diff", Type: cmdTypeLocal, Execute: cmdDiff},
		{Name: "copy", Description: "Copy last output to clipboard", Type: cmdTypeLocal, Execute: cmdCopy},
		{Name: "worktree", Description: "Create/switch git worktree", ArgHint: "<name>", InlineArgs: true, Type: cmdTypeLocal, Execute: cmdWorktree},
		{Name: "skills", Description: "List available skills", Type: cmdTypeLocal, Execute: cmdSkills},
		{Name: "insight", Description: "Session stats and diagnostics", Type: cmdTypeLocal, Execute: cmdInsight},
		{Name: "exit", Aliases: []string{"quit"}, Description: "Exit wuu", Type: cmdTypeLocal, Execute: cmdExit},
	}
}

// ---------------------------------------------------------------------------
// Slash-command parsing (kept for backward compat / tests)
// ---------------------------------------------------------------------------

type slashCommand struct {
	Name string
	Args string
}

func parseSlashCommand(input string) (slashCommand, bool) {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" || !strings.HasPrefix(trimmed, "/") {
		return slashCommand{}, false
	}
	fields := strings.Fields(trimmed[1:])
	if len(fields) == 0 {
		return slashCommand{}, false
	}

	name := strings.ToLower(strings.TrimSpace(fields[0]))
	args := ""
	if len(fields) > 1 {
		args = strings.TrimSpace(strings.Join(fields[1:], " "))
	}
	return slashCommand{Name: name, Args: args}, true
}

// ---------------------------------------------------------------------------
// handleSlash dispatches through the registry (signature unchanged)
// ---------------------------------------------------------------------------

func (m *Model) handleSlash(input string) (string, bool) {
	trimmed := strings.TrimSpace(input)
	if !strings.HasPrefix(trimmed, "/") {
		return "", false
	}

	parts := strings.SplitN(trimmed[1:], " ", 2)
	name := strings.ToLower(parts[0])
	if name == "" {
		return "", false
	}
	args := ""
	if len(parts) > 1 {
		args = parts[1]
	}

	for _, cmd := range commandRegistry {
		if cmd.Name == name || containsAlias(cmd.Aliases, name) {
			return cmd.Execute(args, m), true
		}
	}

	return fmt.Sprintf("unknown command: /%s (type /help for available commands)", name), true
}

func containsAlias(aliases []string, name string) bool {
	for _, a := range aliases {
		if a == name {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// Command implementations
// ---------------------------------------------------------------------------

func cmdHelp(_ string, _ *Model) string {
	var b strings.Builder
	b.WriteString("Available commands:\n")
	for _, cmd := range commandRegistry {
		if cmd.Hidden {
			continue
		}
		hint := ""
		if cmd.ArgHint != "" {
			hint = " " + cmd.ArgHint
		}
		b.WriteString(fmt.Sprintf("  /%s%s - %s\n", cmd.Name, hint, cmd.Description))
	}
	return strings.TrimRight(b.String(), "\n")
}

func cmdClear(_ string, m *Model) string {
	m.entries = nil
	m.refreshViewport(true)
	return "screen cleared"
}

func cmdStatus(_ string, m *Model) string {
	return fmt.Sprintf("provider: %s\nmodel: %s\nconfig: %s\nentries: %d\nworkspace: %s",
		m.provider, m.modelName, m.configPath, len(m.entries), m.workspaceRoot)
}

func cmdCompact(_ string, _ *Model) string {
	return "compact: not yet implemented"
}

func cmdModelSwitch(args string, m *Model) string {
	name := strings.TrimSpace(args)
	if name == "" {
		return fmt.Sprintf("current model: %s (use /model <name> to switch)", m.modelName)
	}
	old := m.modelName
	m.modelName = name
	if m.streamRunner != nil {
		m.streamRunner.Model = name
	}
	return fmt.Sprintf("model switched: %s -> %s", old, name)
}

func cmdResume(_ string, m *Model) string {
	if strings.TrimSpace(m.memoryPath) == "" {
		return "resume: memory file is disabled for this session."
	}
	entries, err := loadMemoryEntries(m.memoryPath)
	if err != nil {
		return fmt.Sprintf("resume: failed to read memory: %v", err)
	}
	if len(entries) == 0 {
		return "resume: no saved entries found."
	}
	m.entries = entries
	m.refreshViewport(true)
	return fmt.Sprintf("resume: loaded %d entries from %s", len(entries), m.memoryPath)
}

func cmdFork(_ string, m *Model) string {
	if strings.TrimSpace(m.memoryPath) == "" {
		return "fork: memory file is disabled for this session."
	}
	target := strings.TrimSuffix(m.memoryPath, filepath.Ext(m.memoryPath))
	target = fmt.Sprintf("%s.fork-%s.jsonl", target, time.Now().Format("20060102-150405"))
	if err := copyFile(m.memoryPath, target); err != nil {
		return fmt.Sprintf("fork: failed to create snapshot: %v", err)
	}
	return fmt.Sprintf("fork: session snapshot created at %s", target)
}

func cmdNew(_ string, m *Model) string {
	m.entries = nil
	m.streamTarget = -1
	m.streaming = false
	m.pendingRequest = false
	m.refreshViewport(true)
	return "new conversation started"
}

func cmdDiff(_ string, m *Model) string {
	out, err := exec.Command("git", "-C", m.workspaceRoot, "diff", "--stat").CombinedOutput()
	if err != nil {
		return fmt.Sprintf("git diff failed: %v", err)
	}
	result := strings.TrimSpace(string(out))
	if result == "" {
		return "no changes"
	}
	return result
}

func cmdCopy(_ string, m *Model) string {
	if len(m.entries) == 0 {
		return "nothing to copy"
	}
	last := m.entries[len(m.entries)-1]
	cmd := exec.Command("pbcopy")
	cmd.Stdin = strings.NewReader(last.Content)
	if err := cmd.Run(); err != nil {
		cmd = exec.Command("xclip", "-selection", "clipboard")
		cmd.Stdin = strings.NewReader(last.Content)
		if err := cmd.Run(); err != nil {
			return "clipboard copy failed (install pbcopy or xclip)"
		}
	}
	return "copied to clipboard"
}

func cmdWorktree(args string, m *Model) string {
	name := strings.TrimSpace(args)
	if name == "" {
		out, err := exec.Command("git", "-C", m.workspaceRoot, "worktree", "list").CombinedOutput()
		if err != nil {
			return fmt.Sprintf("git worktree list failed: %v", err)
		}
		return strings.TrimSpace(string(out))
	}
	out, err := exec.Command("git", "-C", m.workspaceRoot, "worktree", "add", name).CombinedOutput()
	if err != nil {
		return fmt.Sprintf("git worktree add failed: %v\n%s", err, string(out))
	}
	return fmt.Sprintf("worktree created: %s", name)
}

func cmdSkills(_ string, m *Model) string {
	skills := discoverLocalSkills(filepath.Join(m.workspaceRoot, ".claude", "skills"))
	if len(skills) == 0 {
		return "skills: no local skills found under .claude/skills"
	}
	return fmt.Sprintf("skills: %s", strings.Join(skills, ", "))
}

func cmdInsight(_ string, m *Model) string {
	userCount := 0
	assistantCount := 0
	for _, e := range m.entries {
		switch e.Role {
		case "USER":
			userCount++
		case "ASSISTANT":
			assistantCount++
		}
	}
	return fmt.Sprintf("session insight:\n  total entries: %d\n  user messages: %d\n  assistant messages: %d\n  time: %s",
		len(m.entries), userCount, assistantCount, time.Now().Format("15:04:05"))
}

func cmdExit(_ string, _ *Model) string {
	return "goodbye"
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func currentBranch(root string) string {
	cmd := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		return "(unknown)"
	}
	branch := strings.TrimSpace(string(out))
	if branch == "" {
		return "(unknown)"
	}
	return branch
}

func discoverLocalSkills(baseDir string) []string {
	entries, err := os.ReadDir(baseDir)
	if err != nil {
		return nil
	}
	skills := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		skillPath := filepath.Join(baseDir, entry.Name(), "SKILL.md")
		if _, statErr := os.Stat(skillPath); statErr == nil {
			skills = append(skills, entry.Name())
		}
	}
	sort.Strings(skills)
	return skills
}

func copyFile(src, dst string) error {
	input, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("open source file: %w", err)
	}
	defer input.Close()

	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return fmt.Errorf("create destination directory: %w", err)
	}
	output, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return fmt.Errorf("open destination file: %w", err)
	}
	defer output.Close()

	if _, err := io.Copy(output, input); err != nil {
		return fmt.Errorf("copy data: %w", err)
	}
	return nil
}
