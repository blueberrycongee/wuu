package tui

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/blueberrycongee/wuu/internal/compact"
	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/cron"
	"github.com/blueberrycongee/wuu/internal/insight"
	processruntime "github.com/blueberrycongee/wuu/internal/process"
	"github.com/blueberrycongee/wuu/internal/providers"
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
	Group       string // one of commandGroupOrder; "Other" falls to the tail
	Aliases     []string
	Description string
	ArgHint     string
	InlineArgs  bool
	Hidden      bool
	Type        commandType
	Execute     func(args string, m *Model) string
}

// commandGroupOrder dictates /help's section ordering. Commands that
// don't match any entry here land in an "Other" bucket rendered last,
// so adding a command without updating this list still produces a
// sensible help screen.
var commandGroupOrder = []string{
	"Session",
	"Context",
	"Info",
	"Config",
	"Output",
	"Worktree",
	"Processes",
	"Scheduling",
	"App",
}

func (c command) completionEnterBehavior() slashCompletionEnterBehavior {
	if c.ArgHint != "" || c.InlineArgs {
		return slashCompletionInsertOnly
	}
	switch c.Name {
	case "help", "clear", "status", "context", "compact", "fork", "new", "diff", "copy", "skills", "memory", "workers", "processes", "cleanup-worktrees", "insight", "exit":
		return slashCompletionExecute
	default:
		return slashCompletionInsertOnly
	}
}

var commandRegistry []command

func init() {
	commandRegistry = []command{
		// ── App ────────────────────────────────────────────────────
		{Name: "help", Group: "App", Description: "Show available commands", Type: cmdTypeLocal, Execute: cmdHelp},
		{Name: "exit", Group: "App", Aliases: []string{"quit"}, Description: "Exit wuu", Type: cmdTypeLocal, Execute: cmdExit},

		// ── Session ────────────────────────────────────────────────
		{Name: "clear", Group: "Session", Description: "Clear screen", Type: cmdTypeLocal, Execute: cmdClear},
		{Name: "new", Group: "Session", Description: "Start new conversation", Type: cmdTypeLocal, Execute: cmdNew},
		{Name: "resume", Group: "Session", Description: "Resume previous session", ArgHint: "[session-id]", InlineArgs: true, Type: cmdTypeLocal, Execute: cmdResume},
		{Name: "fork", Group: "Session", Description: "Fork current session", Type: cmdTypeLocal, Execute: cmdFork},
		{Name: "queue", Group: "Session", Description: "List or manage queued messages", ArgHint: "[rm <n> | clear]", InlineArgs: true, Type: cmdTypeLocal, Execute: cmdQueue},

		// ── Context ────────────────────────────────────────────────
		{Name: "compact", Group: "Context", Description: "Compress conversation context", Type: cmdTypeLocal, Execute: cmdCompact},
		{Name: "context", Group: "Context", Description: "Show context window usage breakdown", Type: cmdTypeLocal, Execute: cmdContext},

		// ── Info ───────────────────────────────────────────────────
		{Name: "status", Group: "Info", Description: "Show session config and token usage", Type: cmdTypeLocal, Execute: cmdStatus},
		{Name: "skills", Group: "Info", Description: "List available skills", Type: cmdTypeLocal, Execute: cmdSkills},
		{Name: "memory", Group: "Info", Description: "Show loaded memory files (CLAUDE.md / AGENTS.md)", Type: cmdTypeLocal, Execute: cmdMemory},
		{Name: "insight", Group: "Info", Description: "Session stats and diagnostics", Type: cmdTypeLocal, Execute: cmdInsight},

		// ── Config ─────────────────────────────────────────────────
		{Name: "model", Group: "Config", Description: "Switch model/provider", ArgHint: "<model-name>", InlineArgs: true, Type: cmdTypeLocal, Execute: cmdModelSwitch},
		{Name: "effort", Group: "Config", Description: "Set reasoning effort level", ArgHint: "[low|medium|high|max]", InlineArgs: true, Type: cmdTypeLocal, Execute: cmdEffort},

		// ── Output ─────────────────────────────────────────────────
		{Name: "diff", Group: "Output", Description: "Show git diff", Type: cmdTypeLocal, Execute: cmdDiff},
		{Name: "copy", Group: "Output", Description: "Copy last output to clipboard", Type: cmdTypeLocal, Execute: cmdCopy},

		// ── Worktree ───────────────────────────────────────────────
		{Name: "worktree", Group: "Worktree", Description: "Create/switch git worktree", ArgHint: "<name>", InlineArgs: true, Type: cmdTypeLocal, Execute: cmdWorktree},
		{Name: "cleanup-worktrees", Group: "Worktree", Description: "Remove all sub-agent worktrees for this session", Type: cmdTypeLocal, Execute: cmdCleanupWorktrees},

		// ── Processes ──────────────────────────────────────────────
		{Name: "workers", Group: "Processes", Description: "List active and recent sub-agents", Type: cmdTypeLocal, Execute: cmdWorkers},
		{Name: "processes", Group: "Processes", Description: "List managed background processes", Type: cmdTypeLocal, Execute: cmdProcesses},
		{Name: "stop-process", Group: "Processes", Description: "Stop a managed background process", ArgHint: "<id-or-substring>", InlineArgs: true, Type: cmdTypeLocal, Execute: cmdStopProcess},
		{Name: "logs", Group: "Processes", Description: "Show recent output from a managed background process", ArgHint: "<id-or-substring>", InlineArgs: true, Type: cmdTypeLocal, Execute: cmdLogs},

		// ── Scheduling ─────────────────────────────────────────────
		{Name: "loop", Group: "Scheduling", Description: "Create a session-only recurring task", ArgHint: "<interval> <prompt>", InlineArgs: true, Type: cmdTypeLocal, Execute: cmdLoop},
		{Name: "unloop", Group: "Scheduling", Description: "Cancel a scheduled task by id", ArgHint: "<task-id>", InlineArgs: true, Type: cmdTypeLocal, Execute: cmdUnloop},
		{Name: "tasks", Group: "Scheduling", Description: "List scheduled tasks", Type: cmdTypeLocal, Execute: cmdTasks},
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
	// Bucket commands by their declared group. Commands that forgot to
	// set one land in "Other" so the screen degrades gracefully instead
	// of silently hiding them.
	byGroup := make(map[string][]command)
	for _, cmd := range commandRegistry {
		if cmd.Hidden {
			continue
		}
		g := cmd.Group
		if g == "" {
			g = "Other"
		}
		byGroup[g] = append(byGroup[g], cmd)
	}

	// Stable left-column width across the whole help screen: find the
	// longest "/name <arg-hint>" so colons line up regardless of group.
	maxLead := 0
	for _, cmds := range byGroup {
		for _, cmd := range cmds {
			if w := len(formatCommandLead(cmd)); w > maxLead {
				maxLead = w
			}
		}
	}

	emit := func(b *strings.Builder, group string) {
		cmds, ok := byGroup[group]
		if !ok || len(cmds) == 0 {
			return
		}
		fmt.Fprintf(b, "\n%s:\n", group)
		for _, cmd := range cmds {
			lead := formatCommandLead(cmd)
			pad := strings.Repeat(" ", maxLead-len(lead))
			fmt.Fprintf(b, "  %s%s  %s\n", lead, pad, cmd.Description)
		}
		delete(byGroup, group)
	}

	var b strings.Builder
	b.WriteString("Available commands:\n")
	for _, g := range commandGroupOrder {
		emit(&b, g)
	}
	// Any groups not mentioned in commandGroupOrder (including "Other")
	// come last in insertion-order-stable alphabetical form.
	remaining := make([]string, 0, len(byGroup))
	for g := range byGroup {
		remaining = append(remaining, g)
	}
	sort.Strings(remaining)
	for _, g := range remaining {
		emit(&b, g)
	}
	return strings.TrimRight(b.String(), "\n")
}

// formatCommandLead renders the "/name [hint]" prefix for a help row.
// Extracted so the column alignment pass computes widths from the same
// string that the render pass prints.
func formatCommandLead(cmd command) string {
	if cmd.ArgHint != "" {
		return "/" + cmd.Name + " " + cmd.ArgHint
	}
	return "/" + cmd.Name
}

func cmdClear(_ string, m *Model) string {
	m.entries = nil
	m.refreshViewport(true)
	return "screen cleared"
}

// cmdQueue inspects and mutates the two pending message buffers the
// header advertises as "queue:N". The two buffers are intentionally
// treated as a single numbered list from the user's perspective:
//
//   - pendingSteers first (0-indexed), because these will be flushed
//     into the stream immediately once the current turn yields,
//   - messageQueue second, which fires one-by-one on subsequent turns.
//
// Commands:
//
//	/queue              → list
//	/queue clear        → drop both buffers
//	/queue rm <n>       → drop the nth pending item (0-indexed)
func cmdQueue(args string, m *Model) string {
	raw := strings.TrimSpace(args)
	if raw == "" {
		return renderQueueList(m.pendingSteers, m.messageQueue)
	}

	fields := strings.Fields(raw)
	switch strings.ToLower(fields[0]) {
	case "clear":
		total := len(m.pendingSteers) + len(m.messageQueue)
		if total == 0 {
			return "queue is already empty"
		}
		m.pendingSteers = nil
		m.messageQueue = nil
		return fmt.Sprintf("cleared %d queued item(s)", total)

	case "rm", "remove", "delete":
		if len(fields) < 2 {
			return "usage: /queue rm <n> (see /queue for indices)"
		}
		idx, err := strconv.Atoi(fields[1])
		if err != nil || idx < 0 {
			return fmt.Sprintf("not a valid index: %q", fields[1])
		}
		removed, ok := removeQueuedAt(m, idx)
		if !ok {
			return fmt.Sprintf("index %d is out of range", idx)
		}
		return fmt.Sprintf("removed #%d: %s", idx, summarizeQueuedMessage(removed))

	default:
		return fmt.Sprintf("unknown /queue subcommand: %q (try /queue, /queue rm <n>, /queue clear)", fields[0])
	}
}

func renderQueueList(steers, queue []queuedMessage) string {
	if len(steers)+len(queue) == 0 {
		return "queue is empty"
	}
	var b strings.Builder
	b.WriteString("Queued messages (rm with /queue rm <n>):\n")
	idx := 0
	for _, item := range steers {
		fmt.Fprintf(&b, "  %d  [steer]  %s\n", idx, summarizeQueuedMessage(item))
		idx++
	}
	for _, item := range queue {
		fmt.Fprintf(&b, "  %d  [queue]  %s\n", idx, summarizeQueuedMessage(item))
		idx++
	}
	return strings.TrimRight(b.String(), "\n")
}

// removeQueuedAt deletes the idx-th item across the two logical queues
// (pendingSteers first, then messageQueue) and returns it. Returns a
// zero queuedMessage and false when idx is out of range.
func removeQueuedAt(m *Model, idx int) (queuedMessage, bool) {
	if idx < 0 {
		return queuedMessage{}, false
	}
	if idx < len(m.pendingSteers) {
		out := m.pendingSteers[idx]
		m.pendingSteers = append(m.pendingSteers[:idx], m.pendingSteers[idx+1:]...)
		return out, true
	}
	idx -= len(m.pendingSteers)
	if idx < len(m.messageQueue) {
		out := m.messageQueue[idx]
		m.messageQueue = append(m.messageQueue[:idx], m.messageQueue[idx+1:]...)
		return out, true
	}
	return queuedMessage{}, false
}

func cmdStatus(_ string, m *Model) string {
	return fmt.Sprintf("provider: %s\nmodel: %s\nconfig: %s\nentries: %d\nworkspace: %s",
		m.provider, m.modelName, m.configPath, len(m.entries), m.workspaceRoot)
}

func cmdContext(_ string, m *Model) string {
	if m.streamRunner == nil {
		return "context: no stream runner configured"
	}

	model := m.modelName
	window := providers.ContextWindowFor(model)
	if m.streamRunner.ContextWindowOverride > 0 {
		window = m.streamRunner.ContextWindowOverride
	}

	// Category breakdown.
	var sysTokens, toolTokens, msgTokens int
	for _, msg := range m.chatHistory {
		est := compact.EstimateMessagesTokens([]providers.ChatMessage{msg})
		switch msg.Role {
		case "system":
			sysTokens += est
		default:
			msgTokens += est
		}
	}
	if m.streamRunner.Tools != nil {
		for _, def := range m.streamRunner.Tools.Definitions() {
			toolTokens += compact.EstimateTokens(def.Name)
			toolTokens += compact.EstimateTokens(def.Description)
			toolTokens += 20 // schema overhead per tool
		}
		toolTokens += 500 // tool definition preamble (aligned with CC)
	}

	// Memory files.
	var memTokens int
	for _, mf := range m.memoryFiles {
		memTokens += compact.EstimateTokens(mf.Content)
	}
	// Skills.
	var skillTokens int
	for _, sk := range m.skills {
		skillTokens += compact.EstimateTokens(sk.Content)
	}

	totalUsed := sysTokens + toolTokens + memTokens + skillTokens + msgTokens
	free := window - totalUsed
	if free < 0 {
		free = 0
	}
	compactBuffer := int(float64(window) * 0.1) // 10% reserved for compact

	pct := func(n int) string {
		if window == 0 {
			return "0.0%"
		}
		return fmt.Sprintf("%.1f%%", float64(n)/float64(window)*100)
	}
	fmtK := func(n int) string {
		if n < 1000 {
			return fmt.Sprintf("%d", n)
		}
		return fmt.Sprintf("%.1fk", float64(n)/1000)
	}

	// Visual bar (20 chars wide).
	barWidth := 20
	usedSlots := 0
	if window > 0 {
		usedSlots = totalUsed * barWidth / window
		if usedSlots > barWidth {
			usedSlots = barWidth
		}
	}
	bar := strings.Repeat("█", usedSlots) + strings.Repeat("░", barWidth-usedSlots)

	var b strings.Builder
	fmt.Fprintf(&b, "Context Usage  %s/%s  %s  %s\n", model, m.provider, fmtK(totalUsed)+"/"+fmtK(window)+" tokens", pct(totalUsed))
	fmt.Fprintf(&b, "%s\n\n", bar)
	fmt.Fprintf(&b, "  System prompt:  %6s tokens (%s)\n", fmtK(sysTokens), pct(sysTokens))
	fmt.Fprintf(&b, "  Tool defs:      %6s tokens (%s)\n", fmtK(toolTokens), pct(toolTokens))
	fmt.Fprintf(&b, "  Memory files:   %6s tokens (%s)\n", fmtK(memTokens), pct(memTokens))
	fmt.Fprintf(&b, "  Skills:         %6s tokens (%s)\n", fmtK(skillTokens), pct(skillTokens))
	fmt.Fprintf(&b, "  Messages:       %6s tokens (%s)\n", fmtK(msgTokens), pct(msgTokens))
	fmt.Fprintf(&b, "  Free:           %6s tokens (%s)\n", fmtK(free), pct(free))
	fmt.Fprintf(&b, "  Compact buffer: %6s tokens (%s)\n", fmtK(compactBuffer), pct(compactBuffer))

	// Message breakdown.
	userMsgs, assistMsgs, toolMsgs := 0, 0, 0
	for _, msg := range m.chatHistory {
		switch msg.Role {
		case "user":
			userMsgs++
		case "assistant":
			assistMsgs++
		case "tool":
			toolMsgs++
		}
	}
	fmt.Fprintf(&b, "\n  Messages: %d total (%d user, %d assistant, %d tool)\n", len(m.chatHistory), userMsgs, assistMsgs, toolMsgs)

	// Memory files list.
	if len(m.memoryFiles) > 0 {
		b.WriteString("\nMemory files:\n")
		for _, mf := range m.memoryFiles {
			fmt.Fprintf(&b, "  %s: %s tokens\n", mf.Path, fmtK(compact.EstimateTokens(mf.Content)))
		}
	}

	// Skills list.
	if len(m.skills) > 0 {
		b.WriteString("\nSkills:\n")
		for _, sk := range m.skills {
			fmt.Fprintf(&b, "  %s: %s tokens\n", sk.Name, fmtK(compact.EstimateTokens(sk.Content)))
		}
	}

	return b.String()
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

	// Model downshift detection: if the new model has a smaller context
	// window and the current history exceeds ~80% of it, proactively
	// compact to avoid an immediate overflow on the next turn.
	// Aligned with Codex CLI's CompactionReason::ModelDownshift.
	msg := fmt.Sprintf("model switched: %s -> %s (saved)", old, name)
	if m.streamRunner != nil && len(m.chatHistory) > 2 {
		newWindow := providers.ContextWindowFor(name)
		estimated := compact.EstimateMessagesTokens(m.chatHistory)
		threshold := int(float64(newWindow) * 0.8)
		if estimated > threshold {
			before := len(m.chatHistory)
			compacted, err := compact.Compact(context.Background(), m.chatHistory, m.streamRunner.Client, name)
			if err == nil && len(compacted) < before {
				m.chatHistory = compacted
				msg += fmt.Sprintf("\n⚡ Auto-compacted: history exceeded new model's context window (%d → %d messages)",
					before, len(compacted))
			}
		}
	}
	return msg
}

var validEffortLevels = map[string]bool{
	"low": true, "medium": true, "high": true, "max": true,
}

func cmdEffort(args string, m *Model) string {
	level := strings.TrimSpace(strings.ToLower(args))
	if level == "" {
		current := "default (API decides)"
		if m.streamRunner != nil && m.streamRunner.Effort != "" {
			current = m.streamRunner.Effort
		}
		return fmt.Sprintf("current effort: %s (use /effort low|medium|high|max)", current)
	}

	if !validEffortLevels[level] {
		return fmt.Sprintf("invalid effort level %q — valid: low, medium, high, max", level)
	}

	if m.streamRunner != nil {
		m.streamRunner.Effort = level
	}
	return fmt.Sprintf("effort set to %s", level)
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

func cmdLoop(args string, m *Model) string {
	args = strings.TrimSpace(args)
	if args == "" {
		return "usage: /loop <interval> <prompt>\n  interval: 5m, 2h, 1d (default 10m)\n  example: /loop 5m check the deploy\n  note: session-only, stops when this wuu process exits"
	}

	interval, prompt := parseLoopArgs(args)
	if prompt == "" {
		return "usage: /loop <interval> <prompt>"
	}

	cronStr, err := cron.IntervalToCron(interval)
	if err != nil {
		return fmt.Sprintf("loop: invalid interval %q", interval)
	}

	fileStore := cron.NewTaskStore(filepath.Join(m.workspaceRoot, ".wuu", "scheduled_tasks.json"))
	sessionStore := cron.NewSessionTaskStore(m.workspaceRoot)
	fileTasks, _ := fileStore.List()
	sessionTasks, _ := sessionStore.List()
	if len(fileTasks)+len(sessionTasks) >= cron.MaxJobs {
		return fmt.Sprintf("loop: maximum number of scheduled tasks reached (%d)", cron.MaxJobs)
	}

	task := cron.Task{
		ID:        cron.GenerateTaskID(),
		Cron:      cronStr,
		Prompt:    prompt,
		CreatedAt: time.Now().UnixMilli(),
		Recurring: true,
	}
	if err := sessionStore.Add(task); err != nil {
		return fmt.Sprintf("loop: failed to save task: %v", err)
	}

	// Queue the prompt for immediate execution (UX: don't wait for first cron fire).
	m.messageQueue = append(m.messageQueue, queuedMessage{
		Text:            prompt,
		ScheduledTaskID: task.ID,
	})

	return fmt.Sprintf("loop: scheduling '%s' every %s (%s) in this session only", prompt, interval, cronStr)
}

func cmdUnloop(args string, m *Model) string {
	id := strings.TrimSpace(args)
	if id == "" {
		return "usage: /unloop <task-id>"
	}

	fileStore := cron.NewTaskStore(filepath.Join(m.workspaceRoot, ".wuu", "scheduled_tasks.json"))
	sessionStore := cron.NewSessionTaskStore(m.workspaceRoot)
	fileTasks, err := fileStore.List()
	if err != nil {
		return fmt.Sprintf("unloop: %v", err)
	}
	sessionTasks, err := sessionStore.List()
	if err != nil {
		return fmt.Sprintf("unloop: %v", err)
	}

	foundInFile := false
	for _, task := range fileTasks {
		if task.ID == id {
			foundInFile = true
			break
		}
	}
	foundInSession := false
	for _, task := range sessionTasks {
		if task.ID == id {
			foundInSession = true
			break
		}
	}
	if !foundInFile && !foundInSession {
		return fmt.Sprintf("unloop: no scheduled task with id %q", id)
	}

	if foundInFile {
		if err := fileStore.Remove(id); err != nil {
			return fmt.Sprintf("unloop: %v", err)
		}
	}
	if foundInSession {
		if err := sessionStore.Remove(id); err != nil {
			return fmt.Sprintf("unloop: %v", err)
		}
	}

	removedQueued := m.removeQueuedScheduledTaskMessages(id)
	if removedQueued > 0 {
		return fmt.Sprintf("unloop: cancelled %s and removed %d queued run(s)", id, removedQueued)
	}
	return fmt.Sprintf("unloop: cancelled %s", id)
}

func cmdTasks(_ string, m *Model) string {
	fileStore := cron.NewTaskStore(filepath.Join(m.workspaceRoot, ".wuu", "scheduled_tasks.json"))
	sessionStore := cron.NewSessionTaskStore(m.workspaceRoot)
	fileTasks, err := fileStore.List()
	if err != nil {
		return fmt.Sprintf("tasks: %v", err)
	}
	sessionTasks, err := sessionStore.List()
	if err != nil {
		return fmt.Sprintf("tasks: %v", err)
	}
	if len(fileTasks)+len(sessionTasks) == 0 {
		return "tasks: no scheduled tasks"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "scheduled tasks (%d):\n", len(fileTasks)+len(sessionTasks))
	now := time.Now().UnixMilli()
	appendTask := func(task cron.Task, sessionOnly bool) {
		typeLabel := "one-shot"
		if task.Recurring {
			typeLabel = "recurring"
		}
		if cron.IsExpired(task, now) {
			typeLabel += " [expired]"
		}
		if sessionOnly {
			typeLabel += " [session-only]"
		}
		fmt.Fprintf(&b, "  %s — %s — %s: %s\n", task.ID, task.Cron, typeLabel, stringutil.Truncate(task.Prompt, 40, "..."))
	}
	for _, task := range fileTasks {
		appendTask(task, false)
	}
	for _, task := range sessionTasks {
		appendTask(task, true)
	}
	return strings.TrimRight(b.String(), "\n")
}

// parseLoopArgs extracts interval and prompt from /loop arguments.
// Priority: leading interval token, then trailing "every" clause, then default 10m.
func parseLoopArgs(input string) (interval, prompt string) {
	fields := strings.Fields(input)
	if len(fields) == 0 {
		return "10m", ""
	}

	first := strings.ToLower(fields[0])
	if isIntervalToken(first) {
		return first, strings.TrimSpace(strings.TrimPrefix(input, fields[0]))
	}

	if len(fields) >= 3 {
		last := strings.ToLower(fields[len(fields)-1])
		secondLast := strings.ToLower(fields[len(fields)-2])
		if secondLast == "every" && isIntervalToken(last) {
			promptParts := fields[:len(fields)-2]
			return last, strings.Join(promptParts, " ")
		}
	}

	return "10m", input
}

func isIntervalToken(s string) bool {
	if len(s) < 2 {
		return false
	}
	unit := s[len(s)-1]
	if unit != 's' && unit != 'm' && unit != 'h' && unit != 'd' {
		return false
	}
	num := s[:len(s)-1]
	if num == "" {
		return false
	}
	for _, c := range num {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
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
