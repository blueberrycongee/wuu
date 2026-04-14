package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/coordinator"
	proc "github.com/blueberrycongee/wuu/internal/process"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/skills"
)

const (
	defaultShellTimeoutSeconds = 300
	maxShellTimeoutSeconds     = 3600
	defaultMaxFileBytes        = 256 * 1024
	defaultMaxEntries          = 1000
	// Per-tool output size limits (in bytes). Aligned with Claude Code's
	// per-tool maxResultSizeChars: shell/grep produce verbose, low-density
	// output and get a tighter cap; other tools use a generous default.
	maxShellOutputBytes = 30 * 1024  // 30 KB — matches Claude Code BashTool
	maxGrepOutputBytes  = 20 * 1024  // 20 KB — matches Claude Code GrepTool
	maxToolOutputBytes  = 100 * 1024 // 100 KB — general cap for other tools
)

// Toolkit executes local coding tools for the agent. It satisfies
// agent.ToolExecutor via Definitions() + Execute().
//
// Internally it holds an Env (shared runtime state) and a Registry
// (all registered tools). The old switch-case dispatch is replaced by
// registry lookup.
type Toolkit struct {
	env           *Env
	registry      *Registry
	disabledTools map[string]struct{}
}

// New creates a tool executor rooted in a workspace.
func New(rootDir string) (*Toolkit, error) {
	if strings.TrimSpace(rootDir) == "" {
		return nil, errors.New("root directory is required")
	}
	abs, err := filepath.Abs(rootDir)
	if err != nil {
		return nil, fmt.Errorf("resolve root directory: %w", err)
	}
	if ev, err := filepath.EvalSymlinks(abs); err == nil {
		abs = ev
	}

	env := &Env{RootDir: abs}
	t := &Toolkit{env: env}
	t.rebuildRegistry()
	return t, nil
}

// rebuildRegistry constructs the tool registry from the current Env.
// Called at construction and whenever dependencies change.
func (t *Toolkit) rebuildRegistry() {
	e := t.env
	t.registry = NewRegistry(
		// File operations
		NewReadFileTool(e),
		NewWriteFileTool(e),
		NewListFilesTool(e),
		NewEditFileTool(e),
		// Search
		NewGrepTool(e),
		NewGlobTool(e),
		// Shell
		NewShellTool(e),
		// Git
		NewGitTool(e),
		// Web
		NewWebSearchTool(e),
		NewWebFetchTool(e),
		// Skills
		NewLoadSkillTool(e),
		// User interaction
		NewAskUserTool(e),
		// Agent orchestration
		NewSpawnAgentTool(e),
		NewForkAgentTool(e),
		NewSendMessageTool(e),
		NewStopAgentTool(e),
		NewListAgentsTool(e),
		// Process management
		NewStartProcessTool(e),
		NewListProcessesTool(e),
		NewStopProcessTool(e),
		NewReadProcessOutputTool(e),
	)
}

// ── Dependency setters ─────────────────────────────────────────────

// SetCoordinator attaches the orchestration runtime.
func (t *Toolkit) SetCoordinator(c *coordinator.Coordinator) {
	t.env.Coordinator = c
}

// SetAskUserBridge attaches the bridge used by ask_user.
func (t *Toolkit) SetAskUserBridge(b AskUserBridge) {
	t.env.AskBridge = b
}

// SetProcessManager attaches the process manager.
func (t *Toolkit) SetProcessManager(m *proc.Manager) {
	t.env.ProcessMgr = m
}

// SetSkills attaches the discovered skills.
func (t *Toolkit) SetSkills(s []skills.Skill) {
	t.env.Skills = s
}

// Skills returns the currently registered skills (read-only).
func (t *Toolkit) Skills() []skills.Skill {
	return t.env.Skills
}

// SetSessionID sets the current session ID.
func (t *Toolkit) SetSessionID(id string) {
	t.env.SessionID = id
}

// SetSessionDir sets the session directory for result budgeting.
func (t *Toolkit) SetSessionDir(dir string) {
	t.env.SessionDir = dir
}

// SetOnFileChanged sets the callback fired after write_file/edit_file
// successfully modifies a file. Used to dispatch FileChanged hooks.
func (t *Toolkit) SetOnFileChanged(fn func(absPath string)) {
	t.env.OnFileChanged = fn
}

// Coordinator returns the attached orchestration runtime, or nil.
func (t *Toolkit) Coordinator() *coordinator.Coordinator {
	return t.env.Coordinator
}

// ── Tool disabling ─────────────────────────────────────────────────

// DisableTools removes specific tools from this toolkit instance.
func (t *Toolkit) DisableTools(names ...string) {
	if t.disabledTools == nil {
		t.disabledTools = make(map[string]struct{}, len(names))
	}
	for _, n := range names {
		n = strings.TrimSpace(n)
		if n == "" {
			continue
		}
		t.disabledTools[n] = struct{}{}
	}
}

func (t *Toolkit) isToolDisabled(name string) bool {
	if len(t.disabledTools) == 0 {
		return false
	}
	_, ok := t.disabledTools[name]
	return ok
}

// ── ToolExecutor interface ─────────────────────────────────────────

// Definitions returns JSON-schema tool definitions for every enabled
// tool the agent can call.
func (t *Toolkit) Definitions() []providers.ToolDefinition {
	all := t.registry.Definitions()
	if len(t.disabledTools) == 0 {
		return all
	}
	out := make([]providers.ToolDefinition, 0, len(all))
	for _, d := range all {
		if !t.isToolDisabled(d.Name) {
			out = append(out, d)
		}
	}
	return out
}

// Execute runs one tool call and returns JSON result. This is the
// registry-based dispatch that replaces the old switch-case.
//
// Large results are automatically persisted to disk when a SessionDir
// is configured, so the model receives a compact reference instead of
// a truncated blob. Aligned with Claude Code's tool result budgeting.
func (t *Toolkit) Execute(ctx context.Context, call providers.ToolCall) (string, error) {
	if t.isToolDisabled(call.Name) {
		return "", fmt.Errorf("tool %q is disabled in this session", call.Name)
	}
	tool := t.registry.Lookup(call.Name)
	if tool == nil {
		return "", fmt.Errorf("unknown tool %q", call.Name)
	}
	result, err := tool.Execute(ctx, call.Arguments)
	if err != nil {
		return result, err
	}
	return MaybePersistResult(t.env.SessionDir, call.Name, call.ID, result, defaultResultBudget), nil
}

// LookupTool returns the Tool with the given name, or nil. This
// allows callers (e.g. the agent loop) to inspect tool metadata
// like IsReadOnly() and IsConcurrencySafe() for scheduling.
func (t *Toolkit) LookupTool(name string) Tool {
	return t.registry.Lookup(name)
}

// ToolMetadata implements agent.ToolMetadataProvider so the loop can
// partition tool calls into concurrent (read-only) and serial (write)
// batches without importing the tools package.
func (t *Toolkit) ToolMetadata(name string) (agent.ToolMetadata, bool) {
	tool := t.registry.Lookup(name)
	if tool == nil {
		return agent.ToolMetadata{}, false
	}
	return agent.ToolMetadata{
		ReadOnly:        tool.IsReadOnly(),
		ConcurrencySafe: tool.IsConcurrencySafe(),
	}, true
}

// ── Shared utilities (used by individual tool files) ───────────────

func nonInteractiveShellEnv() map[string]string {
	return map[string]string{
		"EDITOR":              "true",
		"GIT_EDITOR":          "true",
		"GIT_PAGER":           "cat",
		"GIT_SEQUENCE_EDITOR": "true",
		"GIT_TERMINAL_PROMPT": "0",
		"GH_PAGER":            "cat",
		"NO_COLOR":            "1",
		"PAGER":               "cat",
		"VISUAL":              "true",
	}
}

func mergeEnv(base []string, overrides map[string]string) []string {
	if len(overrides) == 0 {
		return base
	}
	merged := make(map[string]string, len(base)+len(overrides))
	order := make([]string, 0, len(base)+len(overrides))
	for _, entry := range base {
		key, value, ok := strings.Cut(entry, "=")
		if !ok {
			continue
		}
		if _, exists := merged[key]; !exists {
			order = append(order, key)
		}
		merged[key] = value
	}
	for key, value := range overrides {
		if _, exists := merged[key]; !exists {
			order = append(order, key)
		}
		merged[key] = value
	}
	out := make([]string, 0, len(order))
	for _, key := range order {
		out = append(out, key+"="+merged[key])
	}
	return out
}

func decodeArgs(raw string, target any) error {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		trimmed = "{}"
	}
	if err := json.Unmarshal([]byte(trimmed), target); err != nil {
		return fmt.Errorf("invalid tool arguments: %w", err)
	}
	return nil
}

func mustJSON(v any) (string, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func truncate(value string, maxBytes int) (string, bool) {
	if len(value) <= maxBytes {
		return value, false
	}
	return value[:maxBytes], true
}

func normalizeDisplayPath(rootDir, absPath string) string {
	rel, err := filepath.Rel(rootDir, absPath)
	if err != nil {
		return absPath
	}
	if rel == "." {
		return "."
	}
	return rel
}

func isSkippedDir(name string) bool {
	switch name {
	case ".git", ".wuu", ".hg", ".svn", "node_modules", "vendor", "__pycache__", ".tox", ".venv":
		return true
	}
	return false
}

func isBinaryFile(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()

	const binaryCheckSize = 8192
	buf := make([]byte, binaryCheckSize)
	n, _ := f.Read(buf)
	checkBuf := buf[:n]

	nonPrintable := 0
	for _, b := range checkBuf {
		if b == 0 {
			return true
		}
		if b < 32 && b != 9 && b != 10 && b != 13 {
			nonPrintable++
		}
	}

	if len(checkBuf) > 0 && float64(nonPrintable)/float64(len(checkBuf)) > 0.1 {
		return true
	}
	return false
}

func matchGlob(pattern, path string) bool {
	pattern = filepath.ToSlash(strings.TrimSpace(pattern))
	path = filepath.ToSlash(path)
	if pattern == "" {
		return false
	}
	if !strings.Contains(pattern, "/") {
		matched, _ := filepath.Match(pattern, filepath.Base(path))
		return matched
	}
	re, err := regexp.Compile(globToRegexp(pattern))
	if err != nil {
		return false
	}
	return re.MatchString(path)
}

func globToRegexp(pattern string) string {
	var b strings.Builder
	b.WriteString("^")
	for i := 0; i < len(pattern); i++ {
		switch pattern[i] {
		case '*':
			if i+1 < len(pattern) && pattern[i+1] == '*' {
				if i+2 < len(pattern) && pattern[i+2] == '/' {
					b.WriteString("(?:.*/)?")
					i += 2
					continue
				}
				if i+2 >= len(pattern) && i > 0 && pattern[i-1] == '/' {
					b.WriteString(".*")
					i++
					continue
				}
				b.WriteString("[^/]*")
				i++
				continue
			}
			b.WriteString("[^/]*")
		case '?':
			b.WriteString("[^/]")
		case '.', '+', '(', ')', '|', '^', '$', '{', '}', '[', ']', '\\':
			b.WriteByte('\\')
			b.WriteByte(pattern[i])
		default:
			b.WriteByte(pattern[i])
		}
	}
	b.WriteString("$")
	return b.String()
}

// ── Ripgrep helpers ────────────────────────────────────────────────

type grepMatch struct {
	File    string `json:"file"`
	Line    int    `json:"line"`
	Content string `json:"content"`
}

type rgJSONEvent struct {
	Type string `json:"type"`
	Data struct {
		Path struct {
			Text string `json:"text"`
		} `json:"path"`
		Lines struct {
			Text string `json:"text"`
		} `json:"lines"`
		LineNumber int `json:"line_number"`
	} `json:"data"`
}

var (
	rgLookupPath = exec.LookPath
	rgCommand    = exec.CommandContext
	rgPathOnce   sync.Once
	rgPath       string
)

func lookupRG() string {
	rgPathOnce.Do(func() {
		path, err := rgLookupPath("rg")
		if err == nil {
			rgPath = path
		}
	})
	return rgPath
}

func resetRGForTests() {
	rgPathOnce = sync.Once{}
	rgPath = ""
}

func buildRGFilesCommand(ctx context.Context, pattern string) *exec.Cmd {
	name := lookupRG()
	if name == "" {
		return nil
	}
	args := []string{"--files", "--hidden", "-0", "--glob", pattern}
	return rgCommand(ctx, name, args...)
}

func buildRGGrepCommand(ctx context.Context, pattern, searchRoot, include string) *exec.Cmd {
	name := lookupRG()
	if name == "" {
		return nil
	}
	args := []string{"--json", "--hidden", "-H", "-n", pattern}
	if include != "" {
		args = append(args, "--glob", include)
	}
	if strings.TrimSpace(searchRoot) != "" {
		args = append(args, searchRoot)
	}
	return rgCommand(ctx, name, args...)
}
