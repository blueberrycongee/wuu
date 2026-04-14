package tools

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

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

// Toolkit executes local coding tools for the agent.
type Toolkit struct {
	rootDir       string
	skills        []skills.Skill
	sessionID     string
	coordinator   *coordinator.Coordinator
	askBridge     AskUserBridge
	processMgr    *proc.Manager
	disabledTools map[string]struct{}
}

// SetCoordinator attaches the orchestration runtime so the spawn_agent
// and related tools become available. When unset, those tools error
// at execute time. The coordinator is created by main.go after the
// session is set up.
func (t *Toolkit) SetCoordinator(c *coordinator.Coordinator) {
	t.coordinator = c
}

// SetAskUserBridge attaches the bridge used by the ask_user tool to
// render a modal dialog in the TUI and receive the user's answer.
// When unset, ask_user fails at execute time with a clear error —
// this is how sub-agent workers are kept from interrupting the human
// (their toolkit is constructed without a bridge).
func (t *Toolkit) SetAskUserBridge(b AskUserBridge) {
	t.askBridge = b
}

func (t *Toolkit) SetProcessManager(m *proc.Manager) {
	t.processMgr = m
}

// DisableTools removes specific tools from this toolkit instance.
// Disabled tools are hidden from Definitions() and rejected at Execute().
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
	// Evaluate symlinks on the root at construction time so that
	// resolvePath's symlink-aware escape check compares apples to
	// apples. Without this, a temp dir like /var/folders/... (which
	// is really /private/var/folders/...) would cause every path to
	// appear outside the workspace after EvalSymlinks.
	if ev, err := filepath.EvalSymlinks(abs); err == nil {
		abs = ev
	}
	return &Toolkit{rootDir: abs}, nil
}

// Definitions returns JSON-schema tool definitions for every tool the
// agent can call. There is no role-based filtering: every agent — the
// one talking to the user and any sub-agent it spawns — sees the same
// tool surface. Differentiation is the model's job, expressed through
// the system prompt and the agent's choice of which tool to call.
func (t *Toolkit) Definitions() []providers.ToolDefinition {
	defs := t.allDefinitions()
	if len(t.disabledTools) == 0 {
		return defs
	}
	out := make([]providers.ToolDefinition, 0, len(defs))
	for _, d := range defs {
		if t.isToolDisabled(d.Name) {
			continue
		}
		out = append(out, d)
	}
	return out
}

func (t *Toolkit) allDefinitions() []providers.ToolDefinition {
	return []providers.ToolDefinition{
		{
			Name:        "run_shell",
			Description: "Run a non-interactive shell command in the workspace and return output.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"command": map[string]any{
						"type":        "string",
						"description": "Shell command to execute. Must be non-interactive; never rely on editors, pagers, or terminal prompts.",
					},
					"timeout_seconds": map[string]any{
						"type":        "integer",
						"description": "Max runtime in seconds (1-3600).",
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
			Name:        "git",
			Description: "Run restricted git commands from the main agent: read-only queries (log, status, diff, show, blame, branch --list, etc.) plus simple git operations like commit and push. Complex or destructive operations (rebase, merge, cherry-pick, clean, reset --hard, stash pop/apply/drop/clear, force push, etc.) are not supported and should be delegated to a worker.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"subcommand": map[string]any{
						"type":        "string",
						"description": "Git subcommand. Supported read/query commands include: log, show, diff, status, blame, branch, tag, reflog, stash list, stash show, ls-files, ls-remote, remote, config, rev-parse, rev-list, describe, cat-file, for-each-ref, grep, worktree list, merge-base, shortlog. Supported restricted write commands: commit, push.",
					},
					"args": map[string]any{
						"type":        "array",
						"items":       map[string]any{"type": "string"},
						"description": "Arguments to pass to the git subcommand. commit only supports explicit -m/--message forms on staged changes; push only supports plain push or -u/--set-upstream origin <current-branch>.",
					},
				},
				"required": []string{"subcommand"},
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
			Name: "ask_user",
			Description: "Pause your turn and ask the user a multiple-choice clarifying question. " +
				"Use this BEFORE acting whenever the user's intent is unclear and the answer lives in " +
				"their head (Path A tasks: they have a specific answer you just don't have yet), or " +
				"to offer 2-4 concrete options WITH tradeoffs when the task is genuinely a choice " +
				"(Path B tasks, only AFTER you've done the research that makes the options concrete). " +
				"Send 1-4 questions per call, each with 2-4 options; an \"Other\" escape hatch is " +
				"appended automatically so the user can type a free-text answer if none of your " +
				"options fit. NEVER use this to ask something you could find by reading the code or " +
				"running a command — questions are for things only the user can answer: requirements, " +
				"preferences, tradeoffs, edge-case priorities. If you recommend a specific option, " +
				"put it first in the options list and add \"(recommended)\" at the end of its label.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"questions": map[string]any{
						"type":        "array",
						"minItems":    1,
						"maxItems":    4,
						"description": "Questions to ask the user (1-4 per call, batched into one dialog).",
						"items": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"question": map[string]any{
									"type":        "string",
									"description": "Full question text the user will read. End with a question mark. Must be unique across questions in this call (used as the answer-map key).",
								},
								"header": map[string]any{
									"type":        "string",
									"description": "Very short chip label (<= 12 chars) shown as a tag on the question nav bar. Examples: \"Auth method\", \"DB driver\", \"Approach\".",
								},
								"options": map[string]any{
									"type":        "array",
									"minItems":    2,
									"maxItems":    4,
									"description": "Available choices (2-4). Each option label must be unique. Do NOT include an \"Other\" option — it is appended automatically.",
									"items": map[string]any{
										"type": "object",
										"properties": map[string]any{
											"label": map[string]any{
												"type":        "string",
												"description": "Short display label (1-5 words). Add \"(recommended)\" to the label if this is your recommendation.",
											},
											"description": map[string]any{
												"type":        "string",
												"description": "Explanation of what this option means or what its tradeoffs are. Shown under the label so the user can make an informed choice.",
											},
											"preview": map[string]any{
												"type":        "string",
												"description": "Optional markdown preview (code snippet, ASCII mockup, diagram) rendered side-by-side with the option list when any option in this question has one. Use it when the user needs to visually compare concrete artifacts, not for simple preference questions.",
											},
										},
										"required": []string{"label", "description"},
									},
								},
								"multi_select": map[string]any{
									"type":        "boolean",
									"description": "Set true when the options are NOT mutually exclusive (user may pick several). Default false.",
								},
							},
							"required": []string{"question", "header", "options"},
						},
					},
				},
				"required": []string{"questions"},
			},
		},
		{
			Name: "spawn_agent",
			Description: "Spawn a sub-agent to perform a focused task. The sub-agent has its own " +
				"context and its own tools. There is exactly one worker type, 'worker', with the " +
				"full tool set; specialized roles (verification, read-only research) are injected " +
				"by pasting the appropriate preset block at the start of the prompt — see the " +
				"coordinator system prompt for the verbatim preset text. " +
				"By default the spawn runs INPLACE in the user's repo, so any files the worker " +
				"creates or edits land directly in the working tree. Set isolation='worktree' ONLY " +
				"when the work might break the build, when concurrent writers would collide, or " +
				"when the user explicitly asked for a sandbox. Do NOT use a worktree just because " +
				"the task involves writing files — additive writes are not a reason for isolation. " +
				"Use this for tasks that are context-independent — where a self-contained prompt " +
				"can fully specify what to do. When the task depends on context you've built up " +
				"through exploration (files read, user discussions, dead ends ruled out), consider " +
				"fork_agent instead to avoid losing information to prompt compression. " +
				"By default the spawn is asynchronous: this returns " +
				"immediately with an agent_id, and the worker's result will be delivered to you as " +
				"a <worker-result> message once it completes. Set synchronous=true to block until " +
				"the worker finishes. Spawn multiple workers in parallel by calling spawn_agent " +
				"multiple times in the same response — they run concurrently.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"type": map[string]any{
						"type":        "string",
						"description": "Worker type. Only 'worker' is supported; omit to use the default.",
					},
					"description": map[string]any{
						"type":        "string",
						"description": "Short 3-7 word task summary shown in status displays.",
					},
					"prompt": map[string]any{
						"type":        "string",
						"description": "Self-contained task description. The worker cannot see your conversation, so include all needed context: file paths, line numbers, requirements, acceptance criteria.",
					},
					"isolation": map[string]any{
						"type":        "string",
						"enum":        []string{"inplace", "worktree"},
						"description": "Optional. 'inplace' (default) shares the user's repo so writes land in the working tree. 'worktree' creates a fresh git worktree for sandboxed edits — only use this when the work might break the build, when concurrent writers would collide, or when the user explicitly asked for a sandbox.",
					},
					"base_repo": map[string]any{
						"type":        "string",
						"description": "Optional: path to another worker's worktree to chain off. Only valid with isolation=worktree.",
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
			Name: "fork_agent",
			Description: "Spawn a sub-agent that INHERITS your full conversation history — every " +
				"tool call, every observation, every piece of reasoning you've done so far. " +
				"The worker gets zero-loss context: everything you read, explored, or discussed " +
				"with the user is available without you needing to compress it into a prompt. " +
				"Use fork when the task is context-sensitive — the right execution depends on " +
				"details you learned during exploration that are hard to fully capture in a " +
				"summary. Use spawn_agent instead when the task is self-contained and a short " +
				"prompt can fully specify what to do. " +
				"The forked worker uses your system prompt verbatim (so prompt-cache hits across " +
				"the fork boundary) and runs INPLACE in the parent repo — there is no worktree " +
				"isolation option, because fork is for continuing your work, not for sandboxing. " +
				"The forked worker CANNOT use spawn_agent, fork_agent, send_message_to_agent, " +
				"stop_agent, list_agents, or ask_user (those tools are blocked at the worker " +
				"toolkit level). Your inherited history may reference those tools — the worker " +
				"sees them as read-only context, not patterns to reproduce. " +
				"Like spawn_agent, fork_agent is asynchronous by default: returns immediately " +
				"with an agent_id, the result arrives later as a <worker-result> message. " +
				"Set synchronous=true to block until the worker finishes.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"description": map[string]any{
						"type":        "string",
						"description": "Short 3-7 word task summary shown in status displays.",
					},
					"prompt": map[string]any{
						"type":        "string",
						"description": "The specific task for the forked worker to perform. The worker will see your full conversation history as context, so this prompt only needs to describe the NEW work — do not recap what's already in the history.",
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
			Description: "Send a follow-up instruction to an existing sub-agent. " +
				"If the worker is still running, the message is queued and injected as a " +
				"user turn before its next model round. Sending to completed / failed / " +
				"cancelled workers returns an error.",
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
			Name: "start_process", Description: "Start a managed background OS process in the workspace.",
			InputSchema: map[string]any{"type": "object", "properties": map[string]any{"command": map[string]any{"type": "string"}, "cwd": map[string]any{"type": "string"}, "owner_kind": map[string]any{"type": "string", "enum": []string{"main_agent", "subagent"}}, "owner_id": map[string]any{"type": "string"}, "lifecycle": map[string]any{"type": "string", "enum": []string{"session", "managed"}}}, "required": []string{"command", "owner_kind"}},
		},
		{Name: "list_processes", Description: "List wuu-managed background OS processes.", InputSchema: map[string]any{"type": "object", "properties": map[string]any{}}},
		{Name: "stop_process", Description: "Stop a background process by process group, graceful then kill.", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"process_id": map[string]any{"type": "string"}}, "required": []string{"process_id"}}},
		{Name: "read_process_output", Description: "Read recent output from a background process log.", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"process_id": map[string]any{"type": "string"}, "max_bytes": map[string]any{"type": "integer"}}, "required": []string{"process_id"}}},
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
	if t.isToolDisabled(call.Name) {
		return "", fmt.Errorf("tool %q is disabled in this session", call.Name)
	}
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
	case "git":
		return t.git(ctx, call.Arguments)
	case "web_search":
		return t.webSearch(ctx, call.Arguments)
	case "web_fetch":
		return t.webFetch(ctx, call.Arguments)
	case "load_skill":
		return t.loadSkill(ctx, call.Arguments)
	case "ask_user":
		return t.askUser(ctx, call.Arguments)
	case "spawn_agent":
		return t.spawnAgent(ctx, call.Arguments)
	case "fork_agent":
		return t.forkAgent(ctx, call.Arguments)
	case "send_message_to_agent":
		return t.sendMessageToAgent(call.Arguments)
	case "stop_agent":
		return t.stopAgent(call.Arguments)
	case "list_agents":
		return t.listAgents()
	case "start_process":
		return t.startProcess(ctx, call.Arguments)
	case "list_processes":
		return t.listProcesses()
	case "stop_process":
		return t.stopProcess(call.Arguments)
	case "read_process_output":
		return t.readProcessOutput(call.Arguments)
	default:
		return "", fmt.Errorf("unknown tool %q", call.Name)
	}
}

// askUser decodes an ask_user tool call, forwards it to the bridge,
// and returns the user's answers as a JSON payload. Workers — whose
// toolkit is built without a bridge — hit the "bridge not configured"
// branch, which is the intended isolation: only the main agent
// running inside a live TUI is allowed to interrupt the human.
func (t *Toolkit) askUser(ctx context.Context, argsJSON string) (string, error) {
	if t.askBridge == nil {
		return "", errors.New("ask_user is only available to the main agent in an interactive TUI session (sub-agents cannot interrupt the human)")
	}
	var req AskUserRequest
	if err := decodeArgs(argsJSON, &req); err != nil {
		return "", fmt.Errorf("ask_user: decode arguments: %w", err)
	}
	if err := req.Validate(); err != nil {
		return "", err
	}
	resp, err := t.askBridge.AskUser(ctx, req)
	if err != nil {
		return "", fmt.Errorf("ask_user: %w", err)
	}
	if resp.Cancelled {
		return "", errors.New("ask_user: user dismissed the dialog without answering; reconsider the plan before trying again")
	}
	payload, err := json.Marshal(resp)
	if err != nil {
		return "", fmt.Errorf("ask_user: marshal response: %w", err)
	}
	return string(payload), nil
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
		Isolation   string `json:"isolation"`
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
		Isolation:   args.Isolation,
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

// forkAgent dispatches a fork_agent tool call. Unlike spawnAgent it
// reads the parent's current message history out of ctx (RunToolLoop
// attaches it via withHistory), strips the in-flight assistant turn
// that contains this very fork_agent tool_use (so the worker's first
// API request doesn't have a dangling tool_use), wraps the model-
// supplied prompt in a <system-reminder> role override, and hands
// it all to coordinator.Fork. The whole point is that the worker's
// initial API request shares a byte-identical prefix with the
// parent's most recent request — that's what makes prompt-cache
// hits work across the fork boundary.
func (t *Toolkit) forkAgent(ctx context.Context, argsJSON string) (string, error) {
	if t.coordinator == nil {
		return "", errors.New("fork_agent: coordinator not configured (this build does not support sub-agents)")
	}
	parentHistory := agent.HistoryFromContext(ctx)
	if len(parentHistory) == 0 {
		return "", errors.New("fork_agent: no parent history available — only the main agent in an interactive session can fork (workers cannot fork)")
	}

	var args struct {
		Description string `json:"description"`
		Prompt      string `json:"prompt"`
		Synchronous bool   `json:"synchronous"`
	}
	if err := decodeArgs(argsJSON, &args); err != nil {
		return "", fmt.Errorf("fork_agent: %w", err)
	}
	if strings.TrimSpace(args.Description) == "" {
		return "", errors.New("fork_agent: description is required")
	}
	if strings.TrimSpace(args.Prompt) == "" {
		return "", errors.New("fork_agent: prompt is required")
	}

	// Strip the in-flight assistant turn that contains THIS
	// fork_agent tool_use. The Anthropic Messages API requires
	// every tool_use to be followed by a matching tool_result in
	// the next user/tool turn — leaving our own dangling tool_use
	// in the worker's history would make the worker's first
	// request fail with an invalid_request_error.
	cleaned := stripDanglingToolUses(parentHistory)
	if len(cleaned) == 0 {
		return "", errors.New("fork_agent: history is empty after stripping the in-flight tool_use (nothing to inherit)")
	}

	wrapped := wrapForkPrompt(args.Prompt)

	result, err := t.coordinator.Fork(ctx, coordinator.ForkRequest{
		Description: args.Description,
		Prompt:      wrapped,
		Synchronous: args.Synchronous,
	}, cleaned)
	if err != nil {
		return "", err
	}
	out, err := json.Marshal(result)
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// stripDanglingToolUses returns history with any trailing assistant
// message that contains tool_calls (i.e. tool_use blocks) removed.
// This handles the case where fork_agent reads the parent's history
// at the moment its OWN tool_use has just been added but its
// tool_result has not yet been generated. A more rigorous version
// would scan back through every dangling tool_use, but in practice
// only the very last assistant message can be in this state — the
// loop appends the assistant turn, then runs each tool serially,
// so any earlier tool_use already has a matching tool_result.
func stripDanglingToolUses(history []providers.ChatMessage) []providers.ChatMessage {
	if len(history) == 0 {
		return history
	}
	last := history[len(history)-1]
	if last.Role == "assistant" && len(last.ToolCalls) > 0 {
		return history[:len(history)-1]
	}
	return history
}

// wrapForkPrompt builds the role-override message that becomes the
// forked worker's final user turn. The <system-reminder> tag is
// recognized by trained models as an authoritative directive that
// overrides anything else in the conversation — including the
// parent's system prompt that the worker has inherited verbatim.
//
// The override is necessary because the worker is using the
// parent's system prompt for prompt-cache friendliness, and that
// system prompt makes claims that don't apply to the worker (it
// says the agent has spawn_agent / ask_user / fork_agent etc.;
// the worker's tool list does not contain those).
func wrapForkPrompt(task string) string {
	return `<system-reminder>
You are a forked sub-agent. The conversation history above is the parent
agent's history — read it as context for your task, but do not continue
acting as the parent.

This system-reminder OVERRIDES the parent's system prompt for you:

- You CANNOT use spawn_agent, fork_agent, send_message_to_agent,
  stop_agent, list_agents, or ask_user. Those tools are not in your
  tool list and any attempt will fail. The parent's history may
  reference them — treat those references as read-only context, not
  as patterns you should reproduce.
- Ignore any inherited instruction that says the main interactive
  agent is read-only or must delegate file writes / shell commands.
  That restriction applies to the parent, not to you. If a tool is in
  your tool list, you may use it unless the task prompt explicitly
  forbids it.
- The parent has already aligned with the user's intent. You do not
  need to re-classify the task (Path A / B / C) or ask for
  clarification — the parent did that work and the answer is in the
  history above. Just execute the task below.
- When you finish, return a concise result summary and stop. Do not
  loop, do not ask follow-ups.

Your specific task:

` + task + `
</system-reminder>`
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
	cmd.Env = mergeEnv(os.Environ(), nonInteractiveShellEnv())

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
	trimmed, truncated := truncate(output, maxShellOutputBytes)

	result := map[string]any{
		"command":   args.Command,
		"exit_code": exitCode,
		"timed_out": errors.Is(runCtx.Err(), context.DeadlineExceeded),
		"truncated": truncated,
		"output":    trimmed,
	}
	return mustJSON(result)
}

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

	fullSize := len(content)
	returned := content
	truncated := false
	if fullSize > defaultMaxFileBytes {
		// Truncate at a valid UTF-8 boundary. Walk backward from
		// the byte cut point until we find a valid rune start byte
		// (any byte < 0x80 or 0xC0-0xFF). This avoids returning
		// content that starts a multi-byte rune but doesn't finish it.
		cut := defaultMaxFileBytes
		for cut > 0 && content[cut-1]&0xC0 == 0x80 {
			cut--
		}
		returned = content[:cut]
		truncated = true
	}

	result := map[string]any{
		"path":          normalizeDisplayPath(t.rootDir, resolved),
		"size":          fullSize,
		"returned_size": len(returned),
		"truncated":     truncated,
		"content":       string(returned),
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

	if _, err := regexp.Compile(args.Pattern); err != nil {
		return "", fmt.Errorf("invalid regex: %w", err)
	}

	searchRoot := t.rootDir
	if strings.TrimSpace(args.Path) != "" {
		resolved, err := t.resolvePath(args.Path)
		if err != nil {
			return "", err
		}
		searchRoot = resolved
	}

	const limit = 250
	matches, err := t.grepWithRipgrep(context.Background(), args.Pattern, searchRoot, args.Include, limit)
	if err != nil {
		matches, err = t.grepWithFallback(args.Pattern, searchRoot, args.Include, limit)
		if err != nil {
			return "", err
		}
	}

	result := map[string]any{
		"pattern":   args.Pattern,
		"total":     len(matches),
		"truncated": len(matches) >= limit,
		"matches":   matches,
	}
	out, err := mustJSON(result)
	if err != nil {
		return "", err
	}
	if len(out) > maxGrepOutputBytes {
		out = out[:maxGrepOutputBytes]
	}
	return out, nil
}

func (t *Toolkit) grepWithRipgrep(ctx context.Context, pattern, searchRoot, include string, limit int) ([]grepMatch, error) {
	relSearchRoot, err := filepath.Rel(t.rootDir, searchRoot)
	if err != nil {
		return nil, err
	}
	if relSearchRoot == "." {
		relSearchRoot = ""
	}
	cmd := buildRGGrepCommand(ctx, pattern, relSearchRoot, include)
	if cmd != nil {
		cmd.Dir = t.rootDir
	}
	if cmd == nil {
		return nil, errors.New("ripgrep not available")
	}

	output, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
			return []grepMatch{}, nil
		}
		return nil, err
	}

	matches := make([]grepMatch, 0, min(limit, 16))
	for _, line := range bytes.Split(bytes.TrimSpace(output), []byte{'\n'}) {
		if len(line) == 0 {
			continue
		}
		var event rgJSONEvent
		if err := json.Unmarshal(line, &event); err != nil {
			return nil, fmt.Errorf("parse ripgrep output: %w", err)
		}
		if event.Type != "match" {
			continue
		}
		matchPath := event.Data.Path.Text
		if !filepath.IsAbs(matchPath) {
			matchPath = filepath.Join(t.rootDir, matchPath)
		}
		rel, err := filepath.Rel(t.rootDir, matchPath)
		if err != nil {
			continue
		}
		matches = append(matches, grepMatch{
			File:    filepath.ToSlash(rel),
			Line:    event.Data.LineNumber,
			Content: strings.TrimRight(event.Data.Lines.Text, "\r\n"),
		})
		if len(matches) >= limit {
			break
		}
	}
	sort.SliceStable(matches, func(i, j int) bool {
		if matches[i].File == matches[j].File {
			return matches[i].Line < matches[j].Line
		}
		return matches[i].File < matches[j].File
	})
	return matches, nil
}

func (t *Toolkit) grepWithFallback(pattern, searchRoot, include string, limit int) ([]grepMatch, error) {
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, fmt.Errorf("invalid regex: %w", err)
	}

	matches := make([]grepMatch, 0, min(limit, 16))
	walkErr := filepath.Walk(searchRoot, func(path string, info os.FileInfo, err error) error {
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

		rel, err := filepath.Rel(t.rootDir, path)
		if err != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if include != "" && !matchGlob(include, rel) {
			return nil
		}
		if isBinaryFile(path) {
			return nil
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}

		scanner := bufio.NewScanner(bytes.NewReader(data))
		lineNum := 0
		for scanner.Scan() {
			lineNum++
			line := scanner.Text()
			if re.MatchString(line) {
				matches = append(matches, grepMatch{
					File:    rel,
					Line:    lineNum,
					Content: line,
				})
				if len(matches) >= limit {
					break
				}
			}
		}
		if err := scanner.Err(); err != nil {
			return fmt.Errorf("scan %s: %w", rel, err)
		}
		return nil
	})
	if walkErr != nil {
		return nil, walkErr
	}
	sort.SliceStable(matches, func(i, j int) bool {
		if matches[i].File == matches[j].File {
			return matches[i].Line < matches[j].Line
		}
		return matches[i].File < matches[j].File
	})
	return matches, nil
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

	const limit = 500
	matches, err := t.globWithRipgrep(context.Background(), args.Pattern, limit)
	if err != nil {
		matches, err = t.globWithFallback(args.Pattern, limit)
		if err != nil {
			return "", err
		}
	}

	result := map[string]any{
		"pattern":   args.Pattern,
		"total":     len(matches),
		"truncated": len(matches) >= limit,
		"files":     matches,
	}
	return mustJSON(result)
}

func (t *Toolkit) globWithRipgrep(ctx context.Context, pattern string, limit int) ([]string, error) {
	cmd := buildRGFilesCommand(ctx, pattern)
	if cmd == nil {
		return nil, errors.New("ripgrep not available")
	}
	cmd.Dir = t.rootDir

	output, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
			return []string{}, nil
		}
		return nil, err
	}

	matches := make([]string, 0, min(limit, 16))
	for _, entry := range bytes.Split(output, []byte{0}) {
		if len(entry) == 0 {
			continue
		}
		matches = append(matches, filepath.ToSlash(string(entry)))
		if len(matches) >= limit {
			break
		}
	}
	sort.Strings(matches)
	return matches, nil
}

func (t *Toolkit) globWithFallback(pattern string, limit int) ([]string, error) {
	matches := make([]string, 0, min(limit, 16))
	_ = filepath.Walk(t.rootDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			if isSkippedDir(info.Name()) {
				return filepath.SkipDir
			}
			return nil
		}

		rel, err := filepath.Rel(t.rootDir, path)
		if err != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if matchGlob(pattern, rel) {
			matches = append(matches, rel)
		}
		if len(matches) >= limit {
			return filepath.SkipAll
		}
		return nil
	})
	sort.Strings(matches)
	return matches, nil
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

	// EvalSymlinks resolves any symlink components in both the
	// workspace root and the target path. If either path doesn't
	// exist yet (e.g. write_file creating a new file), the non-
	// evaluated absolute path is used as fallback.
	evalRoot := t.rootDir
	if ev, err := filepath.EvalSymlinks(t.rootDir); err == nil {
		evalRoot = ev
	}
	evalResolved := resolved
	if ev, err := filepath.EvalSymlinks(resolved); err == nil {
		evalResolved = ev
	}

	rel, err := filepath.Rel(evalRoot, evalResolved)
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

	// Read up to 8192 bytes for binary detection. Aligned with
	// Claude Code's BINARY_CHECK_SIZE — 512 bytes was too small
	// and missed binary files whose headers lack null bytes
	// (e.g. PDF starts with "%PDF-1.4\n").
	const binaryCheckSize = 8192
	buf := make([]byte, binaryCheckSize)
	n, _ := f.Read(buf)
	checkBuf := buf[:n]

	nonPrintable := 0
	for _, b := range checkBuf {
		if b == 0 {
			return true
		}
		// Count non-printable, non-whitespace bytes.
		// Printable ASCII is 32-126, plus common whitespace (9=tab, 10=newline, 13=CR).
		if b < 32 && b != 9 && b != 10 && b != 13 {
			nonPrintable++
		}
	}

	// If more than 10% non-printable, likely binary.
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

func (t *Toolkit) processManager() (*proc.Manager, error) {
	if t.processMgr != nil {
		return t.processMgr, nil
	}
	return proc.NewManager(t.rootDir)
}

func (t *Toolkit) startProcess(ctx context.Context, argsJSON string) (string, error) {
	var args struct{ Command, CWD, OwnerKind, OwnerID, Lifecycle string }
	if err := decodeArgs(argsJSON, &args); err != nil {
		return "", err
	}
	m, err := t.processManager()
	if err != nil {
		return "", err
	}
	p, err := m.Start(context.WithoutCancel(ctx), proc.StartOptions{Command: args.Command, CWD: args.CWD, OwnerKind: proc.OwnerKind(args.OwnerKind), OwnerID: args.OwnerID, Lifecycle: proc.Lifecycle(args.Lifecycle)})
	out, _ := mustJSON(p)
	if err != nil {
		return out, err
	}
	return out, nil
}
func (t *Toolkit) listProcesses() (string, error) {
	m, err := t.processManager()
	if err != nil {
		return "", err
	}
	ps, err := m.List()
	if err != nil {
		return "", err
	}
	return mustJSON(ps)
}
func (t *Toolkit) stopProcess(argsJSON string) (string, error) {
	var args struct {
		ProcessID string `json:"process_id"`
	}
	if err := decodeArgs(argsJSON, &args); err != nil {
		return "", err
	}
	m, err := t.processManager()
	if err != nil {
		return "", err
	}
	p, err := m.Stop(args.ProcessID)
	if err != nil {
		return "", err
	}
	return mustJSON(p)
}
func (t *Toolkit) readProcessOutput(argsJSON string) (string, error) {
	var args struct {
		ProcessID string `json:"process_id"`
		MaxBytes  int    `json:"max_bytes"`
	}
	if err := decodeArgs(argsJSON, &args); err != nil {
		return "", err
	}
	m, err := t.processManager()
	if err != nil {
		return "", err
	}
	out, tr, err := m.ReadOutput(args.ProcessID, args.MaxBytes)
	if err != nil {
		return "", err
	}
	return mustJSON(map[string]any{"process_id": args.ProcessID, "output": out, "truncated": tr})
}
