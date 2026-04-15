package tui

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/blueberrycongee/wuu/internal/compact"
	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/insight"
	processruntime "github.com/blueberrycongee/wuu/internal/process"
	"github.com/blueberrycongee/wuu/internal/session"
	"github.com/blueberrycongee/wuu/internal/skills"
	"github.com/blueberrycongee/wuu/internal/stringutil"
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

func (c command) completionEnterBehavior() slashCompletionEnterBehavior {
	if c.ArgHint != "" || c.InlineArgs {
		return slashCompletionInsertOnly
	}
	switch c.Name {
	case "help", "clear", "status", "compact", "fork", "new", "diff", "copy", "skills", "memory", "workers", "processes", "cleanup-worktrees", "insight", "exit":
		return slashCompletionExecute
	default:
		return slashCompletionInsertOnly
	}
}

var commandRegistry []command

func init() {
	commandRegistry = []command{
		{Name: "help", Description: "Show available commands", Type: cmdTypeLocal, Execute: cmdHelp},
		{Name: "clear", Description: "Clear screen", Type: cmdTypeLocal, Execute: cmdClear},
		{Name: "status", Description: "Show session config and token usage", Type: cmdTypeLocal, Execute: cmdStatus},
		{Name: "compact", Description: "Compress conversation context", Type: cmdTypeLocal, Execute: cmdCompact},
		{Name: "model", Description: "Switch model/provider", ArgHint: "<model-name>", InlineArgs: true, Type: cmdTypeLocal, Execute: cmdModelSwitch},
		{Name: "resume", Description: "Resume previous session", ArgHint: "[session-id]", InlineArgs: true, Type: cmdTypeLocal, Execute: cmdResume},
		{Name: "fork", Description: "Fork current session", Type: cmdTypeLocal, Execute: cmdFork},
		{Name: "new", Description: "Start new conversation", Type: cmdTypeLocal, Execute: cmdNew},
		{Name: "diff", Description: "Show git diff", Type: cmdTypeLocal, Execute: cmdDiff},
		{Name: "copy", Description: "Copy last output to clipboard", Type: cmdTypeLocal, Execute: cmdCopy},
		{Name: "worktree", Description: "Create/switch git worktree", ArgHint: "<name>", InlineArgs: true, Type: cmdTypeLocal, Execute: cmdWorktree},
		{Name: "skills", Description: "List available skills", Type: cmdTypeLocal, Execute: cmdSkills},
		{Name: "memory", Description: "Show loaded memory files (CLAUDE.md / AGENTS.md)", Type: cmdTypeLocal, Execute: cmdMemory},
		{Name: "workers", Description: "List active and recent sub-agents", Type: cmdTypeLocal, Execute: cmdWorkers},
		{Name: "processes", Description: "List managed background processes", Type: cmdTypeLocal, Execute: cmdProcesses},
		{Name: "stop-process", Description: "Stop a managed background process", ArgHint: "<id-or-substring>", InlineArgs: true, Type: cmdTypeLocal, Execute: cmdStopProcess},
		{Name: "logs", Description: "Show recent output from a managed background process", ArgHint: "<id-or-substring>", InlineArgs: true, Type: cmdTypeLocal, Execute: cmdLogs},
		{Name: "cleanup-worktrees", Description: "Remove all sub-agent worktrees for this session", Type: cmdTypeLocal, Execute: cmdCleanupWorktrees},
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

// expandSkillShorthand checks whether the input is a /<skill-name> shorthand
// for a discovered skill. If it is, it returns the fully processed skill
// body (with variable substitution and inline shell execution applied) so
// submit() can dispatch it as a regular user message. Built-in commands
// always take precedence over skills with the same name.
func (m *Model) expandSkillShorthand(input string) (string, bool) {
	trimmed := strings.TrimSpace(input)
	if !strings.HasPrefix(trimmed, "/") {
		return "", false
	}
	parts := strings.SplitN(trimmed[1:], " ", 2)
	name := strings.ToLower(parts[0])
	if name == "" {
		return "", false
	}
	for _, cmd := range commandRegistry {
		if cmd.Name == name || containsAlias(cmd.Aliases, name) {
			return "", false
		}
	}
	skill, ok := skills.Find(m.skills, name)
	if !ok {
		return "", false
	}
	if !skill.UserInvocable {
		return "", false
	}
	args := ""
	if len(parts) > 1 {
		args = strings.TrimSpace(parts[1])
	}
	// Use a per-skill timeout context so a hanging inline command can't
	// freeze the TUI for more than 60 seconds total.
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	body := skills.ProcessSkillBody(ctx, skill.Content, skills.ProcessOptions{
		Arguments:        args,
		SkillDir:         skill.Dir,
		SessionID:        m.sessionID,
		Shell:            skill.Shell,
		AllowInlineShell: true,
	})
	return body, true
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

func cmdCompact(_ string, m *Model) string {
	if m.streamRunner == nil {
		return "compact: no stream runner configured"
	}
	if len(m.chatHistory) <= 2 {
		return "compact: conversation too short to compact"
	}

	before := len(m.chatHistory)
	tokensBefore := compact.EstimateMessagesTokens(m.chatHistory)

	compacted, err := compact.Compact(context.Background(), m.chatHistory, m.streamRunner.Client, m.streamRunner.Model)
	if err != nil {
		return fmt.Sprintf("compact: %v", err)
	}

	m.chatHistory = compacted
	after := len(m.chatHistory)
	tokensAfter := compact.EstimateMessagesTokens(m.chatHistory)

	return fmt.Sprintf("Compacted: %d → %d messages (~%d → ~%d tokens)", before, after, tokensBefore, tokensAfter)
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
	if err := config.UpdateProviderModel(m.configPath, m.provider, name); err != nil {
		return fmt.Sprintf("model switched: %s -> %s (save failed: %v)", old, name, err)
	}
	return fmt.Sprintf("model switched: %s -> %s (saved)", old, name)
}

func cmdResume(args string, m *Model) string {
	id := strings.TrimSpace(args)

	// Session-based resume.
	if m.sessionDir != "" {
		if id == "" {
			// Open the interactive picker with preview pane.
			picker, err := newResumePicker(m.sessionDir, 50, m.width, m.height)
			if err != nil {
				return fmt.Sprintf("resume: failed to list sessions: %v", err)
			}
			if len(picker.entries) == 0 {
				return "resume: no previous sessions found"
			}
			m.resumePicker = picker
			return "resume: opening picker..."
		}

		// Resume specific session.
		path, err := session.Load(m.sessionDir, id)
		if err != nil {
			return fmt.Sprintf("resume: %v", err)
		}
		entries, err := loadMemoryEntries(path)
		if err != nil {
			return fmt.Sprintf("resume: failed to load session: %v", err)
		}
		m.sessionID = id
		m.memoryPath = path
		m.entries = entries
		m.refreshViewport(true)
		return fmt.Sprintf("resume: loaded session %s (%d entries)", id, len(entries))
	}

	// Legacy memory-file resume.
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
	if m.sessionDir != "" {
		// Session-based fork: copy current session file to new session.
		newSess, err := session.Create(m.sessionDir)
		if err != nil {
			return fmt.Sprintf("fork: failed to create new session: %v", err)
		}
		srcPath := session.FilePath(m.sessionDir, m.sessionID)
		dstPath := session.FilePath(m.sessionDir, newSess.ID)
		if err := copyFile(srcPath, dstPath); err != nil {
			return fmt.Sprintf("fork: failed to copy session data: %v", err)
		}
		// Update old session index.
		summary := firstUserSummary(m.entries)
		session.UpdateIndex(m.sessionDir, m.sessionID, len(m.entries), summary)
		// Switch to new session.
		m.sessionID = newSess.ID
		m.memoryPath = dstPath
		return fmt.Sprintf("fork: session forked to %s (%d entries)", newSess.ID, len(m.entries))
	}

	// Legacy fork.
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
	// Update index for current session before switching.
	if m.sessionDir != "" && m.sessionID != "" {
		summary := firstUserSummary(m.entries)
		session.UpdateIndex(m.sessionDir, m.sessionID, len(m.entries), summary)
	}

	m.entries = nil
	m.resetChatHistory()
	m.streamTarget = -1
	m.streaming = false
	m.pendingRequest = false
	m.messageQueue = nil
	m.pendingSteers = nil
	m.pendingImages = nil
	m.imageBarFocused = false
	m.selectedImageIdx = 0

	// Create new session if session isolation is active.
	if m.sessionDir != "" {
		sess, err := session.Create(m.sessionDir)
		if err == nil {
			m.sessionID = sess.ID
			m.memoryPath = session.FilePath(m.sessionDir, sess.ID)
		}
	}

	m.refreshViewport(true)
	return fmt.Sprintf("new conversation started (session: %s)", m.sessionID)
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
	method, err := writeClipboard(last.Content)
	if err != nil {
		return "clipboard copy failed (install pbcopy / xclip / wl-copy)"
	}
	if method == "osc52" {
		return "copied via OSC 52 (terminal-dependent)"
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
	projectDir := filepath.Join(m.workspaceRoot, ".claude", "skills")
	userDir := ""
	if home := os.Getenv("HOME"); home != "" {
		userDir = filepath.Join(home, ".claude", "skills")
	}
	discovered := skills.Discover(projectDir, userDir)
	if len(discovered) == 0 {
		return "skills: no skills found in .claude/skills/ (project) or ~/.claude/skills/ (user)"
	}
	var b strings.Builder
	b.WriteString(fmt.Sprintf("skills (%d available):\n", len(discovered)))
	for _, s := range discovered {
		desc := s.Description
		if desc == "" {
			desc = "(no description)"
		}
		fmt.Fprintf(&b, "  • %s [%s] — %s\n", s.Name, s.Source, desc)
	}
	b.WriteString("\nThe model can invoke any of these via the load_skill tool.")
	return b.String()
}

func cmdMemory(_ string, m *Model) string {
	if len(m.memoryFiles) == 0 {
		return "memory: no CLAUDE.md or AGENTS.md found in project hierarchy or ~/.claude/"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "memory (%d file%s loaded):\n", len(m.memoryFiles), pluralS(len(m.memoryFiles)))
	for _, f := range m.memoryFiles {
		fmt.Fprintf(&b, "  • [%s] %s  (%d bytes)\n", f.Source, f.Path, len(f.Content))
	}
	b.WriteString("\nThese files are injected into the system prompt at session start.")
	return b.String()
}

func pluralS(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func cmdWorkers(_ string, m *Model) string {
	if m.coordinator == nil {
		return "workers: coordinator not available (not a git repository?)"
	}
	list := m.coordinator.List()
	if len(list) == 0 {
		return "workers: none spawned in this session yet"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "workers (%d total):\n", len(list))
	for _, s := range list {
		dur := ""
		if !s.CompletedAt.IsZero() && !s.StartedAt.IsZero() {
			dur = fmt.Sprintf("  (%s)", s.CompletedAt.Sub(s.StartedAt).Truncate(time.Millisecond))
		}
		desc := s.Description
		if desc == "" {
			desc = "(no description)"
		}
		fmt.Fprintf(&b, "  %s [%s] %s — %s%s\n", s.ID, s.Type, s.Status, desc, dur)
	}
	return b.String()
}

func cmdStopProcess(args string, m *Model) string {
	if m.processManager == nil {
		return "stop-process: process manager not available"
	}
	query := strings.TrimSpace(args)
	if query == "" {
		return "stop-process: requires <id-or-substring>"
	}
	p, err := resolveProcessQuery(m.processManager, query)
	if err != nil {
		return fmt.Sprintf("stop-process: %v", err)
	}
	stopped, err := m.processManager.Stop(p.ID)
	if err != nil {
		return fmt.Sprintf("stop-process: %v", err)
	}
	if stopped == nil {
		return fmt.Sprintf("stop-process: process %s stopped", p.ID)
	}
	return fmt.Sprintf("stop-process: stopped %s (%s) — status:%s", stopped.ID, processDisplayName(*stopped), stopped.Status)
}

func cmdLogs(args string, m *Model) string {
	if m.processManager == nil {
		return "logs: process manager not available"
	}
	query := strings.TrimSpace(args)
	if query == "" {
		return "logs: requires <id-or-substring>"
	}
	p, err := resolveProcessQuery(m.processManager, query)
	if err != nil {
		return fmt.Sprintf("logs: %v", err)
	}
	output, truncated, err := m.processManager.ReadOutput(p.ID, defaultProcessLogTailBytes)
	if err != nil {
		return fmt.Sprintf("logs: %v", err)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "process: %s\n", p.ID)
	fmt.Fprintf(&b, "command: %s\n", processDisplayName(*p))
	fmt.Fprintf(&b, "truncated: %t", truncated)
	trimmed := strings.TrimRight(output, "\n")
	if trimmed == "" {
		b.WriteString("\n\n(no recent output)")
	} else {
		b.WriteString("\n\n")
		b.WriteString(trimmed)
	}
	return b.String()
}

func cmdProcesses(_ string, m *Model) string {
	if m.processManager == nil {
		return "processes: process manager not available"
	}
	list, err := m.processManager.List()
	if err != nil {
		return fmt.Sprintf("processes: %v", err)
	}
	if len(list) == 0 {
		return "processes: no workspace managed processes found"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "workspace managed processes (%d total):\n", len(list))
	for _, p := range list {
		fmt.Fprintf(&b, "  %s — %s — owner:%s — lifecycle:%s — status:%s\n",
			p.ID,
			processDisplayName(p),
			strings.TrimPrefix(processOwnerLabel(p), "owner:"),
			p.Lifecycle,
			p.Status,
		)
	}
	return strings.TrimRight(b.String(), "\n")
}

const defaultProcessLogTailBytes = 8 * 1024

type processMatch struct {
	Process processruntime.Process
	Field   string
}

func resolveProcessQuery(manager *processruntime.Manager, query string) (*processruntime.Process, error) {
	list, err := manager.List()
	if err != nil {
		return nil, err
	}
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, fmt.Errorf("requires <id-or-substring>")
	}
	for _, p := range list {
		if p.ID == query {
			cp := p
			return &cp, nil
		}
	}
	matches := make([]processMatch, 0, len(list))
	for _, p := range list {
		if strings.Contains(p.Command, query) {
			matches = append(matches, processMatch{Process: p, Field: "command"})
			continue
		}
		name := processDisplayName(p)
		if name != p.Command && strings.Contains(name, query) {
			matches = append(matches, processMatch{Process: p, Field: "display"})
		}
	}
	switch len(matches) {
	case 0:
		return nil, fmt.Errorf("no process matched %q", query)
	case 1:
		cp := matches[0].Process
		return &cp, nil
	default:
		sort.Slice(matches, func(i, j int) bool { return matches[i].Process.StartedAt.Before(matches[j].Process.StartedAt) })
		lines := make([]string, 0, len(matches))
		for _, match := range matches {
			lines = append(lines, fmt.Sprintf("%s (%s via %s)", match.Process.ID, processDisplayName(match.Process), match.Field))
		}
		return nil, fmt.Errorf("ambiguous process match %q:\n  %s", query, strings.Join(lines, "\n  "))
	}
}

func cmdCleanupWorktrees(_ string, m *Model) string {
	if m.coordinator == nil {
		return "cleanup-worktrees: coordinator not available"
	}
	if err := m.coordinator.CleanupSession(); err != nil {
		return fmt.Sprintf("cleanup-worktrees: %v", err)
	}
	return "cleanup-worktrees: removed all worktrees for this session"
}

func cmdInsight(_ string, m *Model) string {
	if m.insightRunning {
		return "insight: already running"
	}
	// Insight should run in a fresh session to avoid polluting conversation
	// context with the lengthy report output.
	if len(m.entries) > 0 {
		return "insight: please start a new session first (/new), then run /insight.\n  The report is large and would pollute your current conversation context."
	}
	if m.streaming || m.pendingRequest {
		return "insight: please wait for the current response to finish"
	}
	if m.sessionDir == "" {
		return "insight: no session directory configured"
	}
	if m.streamRunner == nil {
		return "insight: requires a streaming provider (no LLM client available)"
	}

	ch := make(chan insight.ProgressEvent, 16)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)

	m.insightRunning = true
	m.insightCh = ch
	m.cancelInsight = cancel

	go insight.Run(ctx, insight.RunConfig{
		SessionDir:    m.sessionDir,
		WorkspaceRoot: m.workspaceRoot,
		Client:        m.streamRunner.Client,
		Model:         m.streamRunner.Model,
		MaxSessions:   50,
	}, ch)

	return "insight: scanning sessions..."
}

func cmdExit(_ string, _ *Model) string {
	return "__exit__"
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

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

// firstUserSummary returns the first 60 chars of the first user message.
func firstUserSummary(entries []transcriptEntry) string {
	for _, e := range entries {
		if e.Role == "USER" {
			s := strings.TrimSpace(e.Content)
			return stringutil.Truncate(s, 60, "...")
		}
	}
	return ""
}
