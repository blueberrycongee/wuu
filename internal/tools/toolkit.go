package tools

import (
	"bytes"
	"context"
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/blueberrycongee/wuu/internal/coordinator"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/skills"
)

const (
	defaultShellTimeoutSeconds = 120
	maxShellTimeoutSeconds     = 600
	defaultMaxFileBytes        = 256 * 1024
	defaultMaxEntries          = 1000
	maxToolOutputBytes         = 256 * 1024
)

// Toolkit executes local coding tools for the agent.
type Toolkit struct {
	rootDir     string
	skills      []skills.Skill
	sessionID   string
	coordinator *coordinator.Coordinator
}

// SetCoordinator attaches the orchestration runtime so the spawn_agent
// and related tools become available. When unset, those tools error
// at execute time. The coordinator is created by main.go after the
// session is set up.
func (t *Toolkit) SetCoordinator(c *coordinator.Coordinator) {
	t.coordinator = c
}

// Coordinator returns the attached orchestration runtime, or nil.
func (t *Toolkit) Coordinator() *coordinator.Coordinator {
	return t.coordinator
}

// SetSkills attaches the discovered skills so the load_skill tool can find them.
func (t *Toolkit) SetSkills(s []skills.Skill) {
	t.skills = s
}

// Skills returns the currently registered skills (read-only).
func (t *Toolkit) Skills() []skills.Skill {
	return t.skills
}

// SetSessionID sets the current session ID, used for ${CLAUDE_SESSION_ID}
// substitution in skill bodies.
func (t *Toolkit) SetSessionID(id string) {
	t.sessionID = id
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
	return &Toolkit{rootDir: abs}, nil
}

// Definitions returns JSON-schema tool definitions.
func (t *Toolkit) Definitions() []providers.ToolDefinition {
	return []providers.ToolDefinition{
		{
			Name:        "run_shell",
			Description: "Run a shell command in the workspace and return output.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"command": map[string]any{
						"type":        "string",
						"description": "Shell command to execute.",
					},
					"timeout_seconds": map[string]any{
						"type":        "integer",
						"description": "Max runtime in seconds (1-300).",
					},
				},
				"required": []string{"command"},
			},
		},
		{
			Name:        "read_file",
			Description: "Read a file from workspace.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path": map[string]any{
						"type":        "string",
						"description": "Relative file path in workspace.",
					},
				},
				"required": []string{"path"},
			},
		},
		{
			Name:        "write_file",
			Description: "Write full file content in workspace.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path": map[string]any{
						"type":        "string",
						"description": "Relative file path in workspace.",
					},
					"content": map[string]any{
						"type":        "string",
						"description": "File content.",
					},
				},
				"required": []string{"path", "content"},
			},
		},
		{
			Name:        "list_files",
			Description: "List entries under a directory in workspace.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path": map[string]any{
						"type":        "string",
						"description": "Relative directory path, default is current workspace root.",
					},
				},
			},
		},
		{
			Name:        "edit_file",
			Description: "Replace exact text in a file. Provide old_text (must match exactly) and new_text. Use for precise edits without rewriting the whole file.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path": map[string]any{
						"type":        "string",
						"description": "Relative file path in workspace.",
					},
					"old_text": map[string]any{
						"type":        "string",
						"description": "Exact text to find and replace. Must match exactly once in the file.",
					},
					"new_text": map[string]any{
						"type":        "string",
						"description": "Text to replace old_text with. Use empty string to delete.",
					},
				},
				"required": []string{"path", "old_text", "new_text"},
			},
		},
		{
			Name:        "grep",
			Description: "Search file contents using a regex pattern. Returns matching lines with file paths and line numbers.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"pattern": map[string]any{
						"type":        "string",
						"description": "Regex pattern to search for.",
					},
					"path": map[string]any{
						"type":        "string",
						"description": "Directory or file to search in. Default is workspace root.",
					},
					"include": map[string]any{
						"type":        "string",
						"description": "Glob pattern to filter files (e.g. '*.go', '*.ts').",
					},
				},
				"required": []string{"pattern"},
			},
		},
		{
			Name:        "glob",
			Description: "Find files matching a glob pattern in the workspace. Supports ** for recursive matching.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"pattern": map[string]any{
						"type":        "string",
						"description": "Glob pattern (e.g. '**/*.go', 'src/**/*.ts', '*.json').",
					},
				},
				"required": []string{"pattern"},
			},
		},
		{
			Name:        "web_search",
			Description: "Search the web using DuckDuckGo. Returns titles, URLs, and snippets.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"query": map[string]any{
						"type":        "string",
						"description": "Search query.",
					},
				},
				"required": []string{"query"},
			},
		},
		{
			Name:        "web_fetch",
			Description: "Fetch a URL and return its content as text. HTML is converted to readable text.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"url": map[string]any{
						"type":        "string",
						"description": "URL to fetch.",
					},
				},
				"required": []string{"url"},
			},
		},
		{
			Name: "spawn_agent",
			Description: "Spawn a sub-agent to perform a focused task in an isolated git worktree. " +
				"The sub-agent has its own context, its own tools, and runs in its own working " +
				"directory based on the current HEAD. Use this for any task that requires reading " +
				"file contents or making changes — your own context stays clean. " +
				"By default the spawn is asynchronous: this returns immediately with an agent_id, " +
				"and the worker's result will be delivered to you as a <worker-result> message " +
				"once it completes. Set synchronous=true to block until the worker finishes. " +
				"Spawn multiple workers in parallel by calling spawn_agent multiple times in " +
				"the same response — they run concurrently.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"type": map[string]any{
						"type":        "string",
						"description": "Worker type. Choose 'worker' for general tasks, 'explorer' for read-only investigation. (More types added in Phase 4.)",
					},
					"description": map[string]any{
						"type":        "string",
						"description": "Short 3-7 word task summary shown in status displays.",
					},
					"prompt": map[string]any{
						"type":        "string",
						"description": "Self-contained task description. The worker cannot see your conversation, so include all needed context: file paths, line numbers, requirements, acceptance criteria.",
					},
					"base_repo": map[string]any{
						"type":        "string",
						"description": "Optional: path to another worker's worktree. The new worker is then based on that worktree's HEAD, enabling chained workflows.",
					},
					"synchronous": map[string]any{
						"type":        "boolean",
						"description": "If true, block until the worker completes and return its result inline. If false (default), return immediately and receive the result later via a <worker-result> message.",
					},
				},
				"required": []string{"description", "prompt"},
			},
		},
		{
			Name: "send_message_to_agent",
			Description: "Send a follow-up message to an existing sub-agent that is still running " +
				"or has completed. The agent will resume from its current state and process the " +
				"new instruction.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"agent_id": map[string]any{
						"type":        "string",
						"description": "The agent_id returned by spawn_agent.",
					},
					"message": map[string]any{
						"type":        "string",
						"description": "Follow-up instruction to send.",
					},
				},
				"required": []string{"agent_id", "message"},
			},
		},
		{
			Name: "stop_agent",
			Description: "Halt a running sub-agent. Use this to abort work that's no longer needed " +
				"or that's taking too long.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"agent_id": map[string]any{
						"type":        "string",
						"description": "The agent_id to stop.",
					},
				},
				"required": []string{"agent_id"},
			},
		},
		{
			Name: "list_agents",
			Description: "List all sub-agents in the current session with their status (running, " +
				"completed, failed, cancelled), type, description, and timing info.",
			InputSchema: map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			},
		},
		{
			Name: "load_skill",
			Description: "Load the full body of a named skill from the project's .claude/skills/ or " +
				"the user's ~/.claude/skills/ directory. Skills are reusable instructions that you " +
				"can invoke when their description matches the user's request. The returned body " +
				"may contain ${ARGUMENTS} (replaced by the arguments parameter), ${CLAUDE_SKILL_DIR} " +
				"(skill's directory path), and ${CLAUDE_SESSION_ID} (current session). Use the " +
				"/skills command to see what's available.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"name": map[string]any{
						"type":        "string",
						"description": "Skill name (e.g. \"commit\" or \"review-pr\"). Leading slash is optional.",
					},
					"arguments": map[string]any{
						"type":        "string",
						"description": "Optional arguments string substituted into ${ARGUMENTS} placeholders in the skill body.",
					},
				},
				"required": []string{"name"},
			},
		},
	}
}

// Execute runs one tool call and returns JSON result.
func (t *Toolkit) Execute(ctx context.Context, call providers.ToolCall) (string, error) {
	switch call.Name {
	case "run_shell":
		return t.runShell(ctx, call.Arguments)
	case "read_file":
		return t.readFile(call.Arguments)
	case "write_file":
		return t.writeFile(call.Arguments)
	case "list_files":
		return t.listFiles(call.Arguments)
	case "edit_file":
		return t.editFile(call.Arguments)
	case "grep":
		return t.grep(call.Arguments)
	case "glob":
		return t.glob(call.Arguments)
	case "web_search":
		return t.webSearch(ctx, call.Arguments)
	case "web_fetch":
		return t.webFetch(ctx, call.Arguments)
	case "load_skill":
		return t.loadSkill(ctx, call.Arguments)
	case "spawn_agent":
		return t.spawnAgent(ctx, call.Arguments)
	case "send_message_to_agent":
		return t.sendMessageToAgent(call.Arguments)
	case "stop_agent":
		return t.stopAgent(call.Arguments)
	case "list_agents":
		return t.listAgents()
	default:
		return "", fmt.Errorf("unknown tool %q", call.Name)
	}
}

func (t *Toolkit) sendMessageToAgent(argsJSON string) (string, error) {
	if t.coordinator == nil {
		return "", errors.New("send_message_to_agent: coordinator not configured")
	}
	var args struct {
		AgentID string `json:"agent_id"`
		Message string `json:"message"`
	}
	if err := decodeArgs(argsJSON, &args); err != nil {
		return "", err
	}
	if err := t.coordinator.SendMessage(args.AgentID, args.Message); err != nil {
		return "", err
	}
	return `{"status":"sent"}`, nil
}

func (t *Toolkit) stopAgent(argsJSON string) (string, error) {
	if t.coordinator == nil {
		return "", errors.New("stop_agent: coordinator not configured")
	}
	var args struct {
		AgentID string `json:"agent_id"`
	}
	if err := decodeArgs(argsJSON, &args); err != nil {
		return "", err
	}
	if !t.coordinator.Stop(args.AgentID) {
		return "", fmt.Errorf("agent %q not found", args.AgentID)
	}
	return `{"status":"stopped"}`, nil
}

func (t *Toolkit) listAgents() (string, error) {
	if t.coordinator == nil {
		return "", errors.New("list_agents: coordinator not configured")
	}
	list := t.coordinator.List()
	out, err := json.Marshal(list)
	if err != nil {
		return "", err
	}
	return string(out), nil
}

func (t *Toolkit) spawnAgent(ctx context.Context, argsJSON string) (string, error) {
	if t.coordinator == nil {
		return "", errors.New("spawn_agent: coordinator not configured (this build does not support sub-agents)")
	}
	var args struct {
		Type        string `json:"type"`
		Description string `json:"description"`
		Prompt      string `json:"prompt"`
		BaseRepo    string `json:"base_repo"`
		Synchronous bool   `json:"synchronous"`
	}
	if err := decodeArgs(argsJSON, &args); err != nil {
		return "", err
	}
	result, err := t.coordinator.Spawn(ctx, coordinator.SpawnRequest{
		Type:        args.Type,
		Description: args.Description,
		Prompt:      args.Prompt,
		BaseRepo:    args.BaseRepo,
		Synchronous: args.Synchronous,
	})
	if err != nil {
		return "", err
	}
	out, err := json.Marshal(result)
	if err != nil {
		return "", err
	}
	return string(out), nil
}

func (t *Toolkit) loadSkill(ctx context.Context, argsJSON string) (string, error) {
	var args struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	}
	if err := decodeArgs(argsJSON, &args); err != nil {
		return "", err
	}
	if strings.TrimSpace(args.Name) == "" {
		return "", errors.New("load_skill requires name")
	}
	skill, ok := skills.Find(t.skills, args.Name)
	if !ok {
		available := make([]string, 0, len(t.skills))
		for _, s := range t.skills {
			available = append(available, s.Name)
		}
		return "", fmt.Errorf("skill %q not found. available: %s", args.Name, strings.Join(available, ", "))
	}

	body := skills.ProcessSkillBody(ctx, skill.Content, skills.ProcessOptions{
		Arguments:        args.Arguments,
		SkillDir:         skill.Dir,
		SessionID:        t.sessionID,
		Shell:            skill.Shell,
		AllowInlineShell: true,
	})

	result := map[string]any{
		"name":        skill.Name,
		"description": skill.Description,
		"source":      skill.Source,
		"content":     body,
	}
	if skill.WhenToUse != "" {
		result["when_to_use"] = skill.WhenToUse
	}
	if len(skill.AllowedTools) > 0 {
		result["allowed_tools"] = skill.AllowedTools
	}
	out, err := json.Marshal(result)
	if err != nil {
		return "", err
	}
	return string(out), nil
}

func (t *Toolkit) runShell(ctx context.Context, argsJSON string) (string, error) {
	var args struct {
		Command        string `json:"command"`
		TimeoutSeconds int    `json:"timeout_seconds"`
	}
	if err := decodeArgs(argsJSON, &args); err != nil {
		return "", err
	}
	if strings.TrimSpace(args.Command) == "" {
		return "", errors.New("run_shell requires command")
	}

	timeout := args.TimeoutSeconds
	if timeout <= 0 {
		timeout = defaultShellTimeoutSeconds
	}
	if timeout > maxShellTimeoutSeconds {
		timeout = maxShellTimeoutSeconds
	}

	runCtx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
	defer cancel()

	cmd := exec.CommandContext(runCtx, "bash", "-lc", args.Command)
	cmd.Dir = t.rootDir

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	exitCode := 0
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			exitCode = exitErr.ExitCode()
		} else if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
			exitCode = 124
		} else {
			return "", fmt.Errorf("run command: %w", err)
		}
	}

	output := stdout.String() + stderr.String()
	trimmed, truncated := truncate(output, maxToolOutputBytes)

	result := map[string]any{
		"command":   args.Command,
		"exit_code": exitCode,
		"timed_out": errors.Is(runCtx.Err(), context.DeadlineExceeded),
		"truncated": truncated,
		"output":    trimmed,
	}
	return mustJSON(result)
}

func (t *Toolkit) readFile(argsJSON string) (string, error) {
	var args struct {
		Path string `json:"path"`
	}
	if err := decodeArgs(argsJSON, &args); err != nil {
		return "", err
	}
	if strings.TrimSpace(args.Path) == "" {
		return "", errors.New("read_file requires path")
	}

	resolved, err := t.resolvePath(args.Path)
	if err != nil {
		return "", err
	}

	content, err := os.ReadFile(resolved)
	if err != nil {
		return "", fmt.Errorf("read file: %w", err)
	}

	truncated := false
	if len(content) > defaultMaxFileBytes {
		content = content[:defaultMaxFileBytes]
		truncated = true
	}

	result := map[string]any{
		"path":      normalizeDisplayPath(t.rootDir, resolved),
		"size":      len(content),
		"truncated": truncated,
		"content":   string(content),
	}
	return mustJSON(result)
}

func (t *Toolkit) writeFile(argsJSON string) (string, error) {
	var args struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if err := decodeArgs(argsJSON, &args); err != nil {
		return "", err
	}
	if strings.TrimSpace(args.Path) == "" {
		return "", errors.New("write_file requires path")
	}

	resolved, err := t.resolvePath(args.Path)
	if err != nil {
		return "", err
	}

	// Read old content for diff (if file exists).
	oldContent, _ := os.ReadFile(resolved)

	if err := os.MkdirAll(filepath.Dir(resolved), 0o755); err != nil {
		return "", fmt.Errorf("create parent directory: %w", err)
	}
	if err := os.WriteFile(resolved, []byte(args.Content), 0o644); err != nil {
		return "", fmt.Errorf("write file: %w", err)
	}

	result := map[string]any{
		"path":          normalizeDisplayPath(t.rootDir, resolved),
		"written_bytes": len(args.Content),
	}

	if len(oldContent) > 0 {
		// Existing file — compute diff.
		result["diff"] = computeDiff(string(oldContent), args.Content, 3)
	} else {
		// New file.
		lineCount := strings.Count(args.Content, "\n")
		if len(args.Content) > 0 && !strings.HasSuffix(args.Content, "\n") {
			lineCount++
		}
		result["diff"] = DiffResult{NewFile: true, Lines: lineCount}
	}
	return mustJSON(result)
}

func (t *Toolkit) listFiles(argsJSON string) (string, error) {
	var args struct {
		Path string `json:"path"`
	}
	if err := decodeArgs(argsJSON, &args); err != nil {
		return "", err
	}
	if strings.TrimSpace(args.Path) == "" {
		args.Path = "."
	}

	resolved, err := t.resolvePath(args.Path)
	if err != nil {
		return "", err
	}

	entries, err := os.ReadDir(resolved)
	if err != nil {
		return "", fmt.Errorf("list directory: %w", err)
	}

	limit := defaultMaxEntries

	resultEntries := make([]map[string]any, 0, min(limit, len(entries)))
	for i, entry := range entries {
		if i >= limit {
			break
		}

		item := map[string]any{
			"name":   entry.Name(),
			"is_dir": entry.IsDir(),
		}
		if !entry.IsDir() {
			info, statErr := entry.Info()
			if statErr == nil {
				item["size"] = info.Size()
			}
		}
		resultEntries = append(resultEntries, item)
	}

	result := map[string]any{
		"path":      normalizeDisplayPath(t.rootDir, resolved),
		"total":     len(entries),
		"truncated": len(entries) > limit,
		"entries":   resultEntries,
	}
	return mustJSON(result)
}

func (t *Toolkit) editFile(argsJSON string) (string, error) {
	var args struct {
		Path    string `json:"path"`
		OldText string `json:"old_text"`
		NewText string `json:"new_text"`
	}
	if err := decodeArgs(argsJSON, &args); err != nil {
		return "", err
	}
	if strings.TrimSpace(args.Path) == "" {
		return "", errors.New("edit_file requires path")
	}
	if args.OldText == "" {
		return "", errors.New("edit_file requires old_text")
	}

	resolved, err := t.resolvePath(args.Path)
	if err != nil {
		return "", err
	}

	content, err := os.ReadFile(resolved)
	if err != nil {
		return "", fmt.Errorf("read file: %w", err)
	}

	text := string(content)
	count := strings.Count(text, args.OldText)
	if count == 0 {
		return "", errors.New("old_text not found in file")
	}
	if count > 1 {
		return "", fmt.Errorf("old_text matches %d times, must be unique", count)
	}

	newContent := strings.Replace(text, args.OldText, args.NewText, 1)
	if err := os.WriteFile(resolved, []byte(newContent), 0o644); err != nil {
		return "", fmt.Errorf("write file: %w", err)
	}

	diff := computeDiff(text, newContent, 3)
	result := map[string]any{
		"path": normalizeDisplayPath(t.rootDir, resolved),
		"diff": diff,
	}
	return mustJSON(result)
}

func (t *Toolkit) grep(argsJSON string) (string, error) {
	var args struct {
		Pattern string `json:"pattern"`
		Path    string `json:"path"`
		Include string `json:"include"`
	}
	if err := decodeArgs(argsJSON, &args); err != nil {
		return "", err
	}
	if strings.TrimSpace(args.Pattern) == "" {
		return "", errors.New("grep requires pattern")
	}

	re, err := regexp.Compile(args.Pattern)
	if err != nil {
		return "", fmt.Errorf("invalid regex: %w", err)
	}

	limit := 250

	searchRoot := t.rootDir
	if strings.TrimSpace(args.Path) != "" {
		resolved, err := t.resolvePath(args.Path)
		if err != nil {
			return "", err
		}
		searchRoot = resolved
	}

	type match struct {
		File    string `json:"file"`
		Line    int    `json:"line"`
		Content string `json:"content"`
	}
	var matches []match

	filepath.Walk(searchRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			if isSkippedDir(info.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if len(matches) >= limit {
			return filepath.SkipAll
		}
		if args.Include != "" {
			if matched, _ := filepath.Match(args.Include, info.Name()); !matched {
				return nil
			}
		}
		if isBinaryFile(path) {
			return nil
		}

		f, err := os.Open(path)
		if err != nil {
			return nil
		}
		defer f.Close()

		rel, _ := filepath.Rel(t.rootDir, path)
		scanner := bufio.NewScanner(f)
		lineNum := 0
		for scanner.Scan() {
			lineNum++
			line := scanner.Text()
			if re.MatchString(line) {
				matches = append(matches, match{
					File:    rel,
					Line:    lineNum,
					Content: line,
				})
				if len(matches) >= limit {
					break
				}
			}
		}
		return nil
	})

	result := map[string]any{
		"pattern":   args.Pattern,
		"total":     len(matches),
		"truncated": len(matches) >= limit,
		"matches":   matches,
	}
	return mustJSON(result)
}

func (t *Toolkit) glob(argsJSON string) (string, error) {
	var args struct {
		Pattern string `json:"pattern"`
	}
	if err := decodeArgs(argsJSON, &args); err != nil {
		return "", err
	}
	if strings.TrimSpace(args.Pattern) == "" {
		return "", errors.New("glob requires pattern")
	}

	limit := 100

	pattern := args.Pattern
	var matches []string

	filepath.Walk(t.rootDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			if isSkippedDir(info.Name()) {
				return filepath.SkipDir
			}
			return nil
		}

		rel, _ := filepath.Rel(t.rootDir, path)
		if matchGlob(pattern, rel) {
			matches = append(matches, rel)
		}
		if len(matches) >= limit {
			return filepath.SkipAll
		}
		return nil
	})

	result := map[string]any{
		"pattern":   pattern,
		"total":     len(matches),
		"truncated": len(matches) >= limit,
		"files":     matches,
	}
	return mustJSON(result)
}

func (t *Toolkit) resolvePath(input string) (string, error) {
	candidate := strings.TrimSpace(input)
	if candidate == "" {
		candidate = "."
	}

	var abs string
	if filepath.IsAbs(candidate) {
		abs = filepath.Clean(candidate)
	} else {
		abs = filepath.Join(t.rootDir, candidate)
	}

	resolved, err := filepath.Abs(abs)
	if err != nil {
		return "", fmt.Errorf("resolve path: %w", err)
	}

	rel, err := filepath.Rel(t.rootDir, resolved)
	if err != nil {
		return "", fmt.Errorf("path relation check: %w", err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path %q escapes workspace", input)
	}
	return resolved, nil
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
	return strings.HasPrefix(name, ".")
}

func isBinaryFile(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	buf := make([]byte, 512)
	n, _ := f.Read(buf)
	for _, b := range buf[:n] {
		if b == 0 {
			return true
		}
	}
	return false
}

func matchGlob(pattern, path string) bool {
	// Handle **/ prefix: match suffix against any file in the tree
	if strings.HasPrefix(pattern, "**/") {
		suffix := pattern[3:]
		// Match against just the filename
		if matched, _ := filepath.Match(suffix, filepath.Base(path)); matched {
			return true
		}
		// Match against each possible tail of the path
		parts := strings.Split(path, string(filepath.Separator))
		for i := range parts {
			tail := strings.Join(parts[i:], string(filepath.Separator))
			if matched, _ := filepath.Match(suffix, tail); matched {
				return true
			}
		}
		return false
	}

	// Handle patterns with ** in the middle (e.g. src/**/*.ts)
	if idx := strings.Index(pattern, "/**/"); idx >= 0 {
		prefix := pattern[:idx]
		suffix := pattern[idx+4:]
		parts := strings.Split(path, string(filepath.Separator))
		for i := range parts {
			dirPart := strings.Join(parts[:i], string(filepath.Separator))
			filePart := strings.Join(parts[i:], string(filepath.Separator))
			prefixMatch, _ := filepath.Match(prefix, dirPart)
			suffixMatch, _ := filepath.Match(suffix, filepath.Base(filePart))
			if prefixMatch && suffixMatch {
				return true
			}
		}
		return false
	}

	// Direct match
	matched, _ := filepath.Match(pattern, path)
	if matched {
		return true
	}
	// Also try matching just the filename for simple patterns
	matched, _ = filepath.Match(pattern, filepath.Base(path))
	return matched
}
