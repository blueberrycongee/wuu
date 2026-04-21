package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/coordinator"
	"github.com/blueberrycongee/wuu/internal/providers"
)

// ---------------------------------------------------------------------------
// ask_user
// ---------------------------------------------------------------------------

type AskUserTool struct{ env *Env }

func NewAskUserTool(env *Env) *AskUserTool { return &AskUserTool{env: env} }

func (t *AskUserTool) Name() string            { return "ask_user" }
func (t *AskUserTool) IsReadOnly() bool         { return true }
func (t *AskUserTool) IsConcurrencySafe() bool  { return false }

func (t *AskUserTool) Definition() providers.ToolDefinition {
	return providers.ToolDefinition{
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
											"description": "Explanation of what this option means or what its tradeoffs are. Shown under the label in the option list.",
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
	}
}

func (t *AskUserTool) Execute(ctx context.Context, argsJSON string) (string, error) {
	if t.env.AskBridge == nil {
		return "", errors.New("ask_user is only available to the main agent in an interactive TUI session (sub-agents cannot interrupt the human)")
	}
	var req AskUserRequest
	if err := decodeArgs(argsJSON, &req); err != nil {
		return "", fmt.Errorf("ask_user: decode arguments: %w", err)
	}
	if err := req.Validate(); err != nil {
		return "", err
	}
	resp, err := t.env.AskBridge.AskUser(ctx, req)
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

// ---------------------------------------------------------------------------
// spawn_agent
// ---------------------------------------------------------------------------

type SpawnAgentTool struct{ env *Env }

func NewSpawnAgentTool(env *Env) *SpawnAgentTool { return &SpawnAgentTool{env: env} }

func (t *SpawnAgentTool) Name() string            { return "spawn_agent" }
func (t *SpawnAgentTool) IsReadOnly() bool         { return false }
func (t *SpawnAgentTool) IsConcurrencySafe() bool  { return true }

func (t *SpawnAgentTool) Definition() providers.ToolDefinition {
	return providers.ToolDefinition{
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
			"immediately with an agent_id, and the worker's result will be delivered to you " +
			"automatically as a <worker-result> message once it completes — you will be " +
			"notified without any action on your part. After spawning async workers, END " +
			"YOUR TURN — do NOT generate waiting messages or loop checking status. The system handles notification and auto-resume. " +
			"Set synchronous=true to block until " +
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
	}
}

func (t *SpawnAgentTool) Execute(ctx context.Context, argsJSON string) (string, error) {
	if t.env.Coordinator == nil {
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
	result, err := t.env.Coordinator.Spawn(ctx, coordinator.SpawnRequest{
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

// ---------------------------------------------------------------------------
// fork_agent
// ---------------------------------------------------------------------------

type ForkAgentTool struct{ env *Env }

func NewForkAgentTool(env *Env) *ForkAgentTool { return &ForkAgentTool{env: env} }

func (t *ForkAgentTool) Name() string            { return "fork_agent" }
func (t *ForkAgentTool) IsReadOnly() bool         { return false }
func (t *ForkAgentTool) IsConcurrencySafe() bool  { return false }

func (t *ForkAgentTool) Definition() providers.ToolDefinition {
	return providers.ToolDefinition{
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
			"with an agent_id, the result arrives later automatically as a <worker-result> " +
			"message. After spawning, END YOUR TURN — do NOT generate waiting messages. The system handles notification and auto-resume. " +
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
	}
}

func (t *ForkAgentTool) Execute(ctx context.Context, argsJSON string) (string, error) {
	if t.env.Coordinator == nil {
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

	cleaned := stripDanglingToolUses(parentHistory)
	if len(cleaned) == 0 {
		return "", errors.New("fork_agent: history is empty after stripping the in-flight tool_use (nothing to inherit)")
	}

	wrapped := wrapForkPrompt(args.Prompt)

	result, err := t.env.Coordinator.Fork(ctx, coordinator.ForkRequest{
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
// message that contains tool_calls removed.
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

// wrapForkPrompt builds the role-override message for forked workers.
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
- The parent has already aligned with the user on both intent and context.
  The goal, success criteria, constraints, and relevant code areas are
  all captured in the history above. You do not need to re-classify
  the task or ask for clarification — just execute the task below.
- When you finish, return a concise result summary and stop. Do not
  loop, do not ask follow-ups.

Your specific task:

` + task + `
</system-reminder>`
}

// ---------------------------------------------------------------------------
// send_message_to_agent
// ---------------------------------------------------------------------------

type SendMessageTool struct{ env *Env }

func NewSendMessageTool(env *Env) *SendMessageTool { return &SendMessageTool{env: env} }

func (t *SendMessageTool) Name() string            { return "send_message_to_agent" }
func (t *SendMessageTool) IsReadOnly() bool         { return false }
func (t *SendMessageTool) IsConcurrencySafe() bool  { return true }

func (t *SendMessageTool) Definition() providers.ToolDefinition {
	return providers.ToolDefinition{
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
	}
}

func (t *SendMessageTool) Execute(_ context.Context, argsJSON string) (string, error) {
	if t.env.Coordinator == nil {
		return "", errors.New("send_message_to_agent: coordinator not configured")
	}
	var args struct {
		AgentID string `json:"agent_id"`
		Message string `json:"message"`
	}
	if err := decodeArgs(argsJSON, &args); err != nil {
		return "", err
	}
	if err := t.env.Coordinator.SendMessage(args.AgentID, args.Message); err != nil {
		return "", err
	}
	return `{"status":"sent"}`, nil
}

// ---------------------------------------------------------------------------
// stop_agent
// ---------------------------------------------------------------------------

type StopAgentTool struct{ env *Env }

func NewStopAgentTool(env *Env) *StopAgentTool { return &StopAgentTool{env: env} }

func (t *StopAgentTool) Name() string            { return "stop_agent" }
func (t *StopAgentTool) IsReadOnly() bool         { return false }
func (t *StopAgentTool) IsConcurrencySafe() bool  { return true }

func (t *StopAgentTool) Definition() providers.ToolDefinition {
	return providers.ToolDefinition{
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
	}
}

func (t *StopAgentTool) Execute(_ context.Context, argsJSON string) (string, error) {
	if t.env.Coordinator == nil {
		return "", errors.New("stop_agent: coordinator not configured")
	}
	var args struct {
		AgentID string `json:"agent_id"`
	}
	if err := decodeArgs(argsJSON, &args); err != nil {
		return "", err
	}
	if !t.env.Coordinator.Stop(args.AgentID) {
		return "", fmt.Errorf("agent %q not found", args.AgentID)
	}
	return `{"status":"stopped"}`, nil
}

// ---------------------------------------------------------------------------
// list_agents
// ---------------------------------------------------------------------------

type ListAgentsTool struct{ env *Env }

func NewListAgentsTool(env *Env) *ListAgentsTool { return &ListAgentsTool{env: env} }

func (t *ListAgentsTool) Name() string            { return "list_agents" }
func (t *ListAgentsTool) IsReadOnly() bool         { return true }
func (t *ListAgentsTool) IsConcurrencySafe() bool  { return true }

func (t *ListAgentsTool) Definition() providers.ToolDefinition {
	return providers.ToolDefinition{
		Name: "list_agents",
		Description: "List all sub-agents in the current session with their status (running, " +
			"completed, failed, cancelled), type, description, and timing info.",
		InputSchema: map[string]any{
			"type": "object",
		},
	}
}

func (t *ListAgentsTool) Execute(_ context.Context, _ string) (string, error) {
	if t.env.Coordinator == nil {
		return "", errors.New("list_agents: coordinator not configured")
	}
	list := t.env.Coordinator.List()
	out, err := json.Marshal(list)
	if err != nil {
		return "", err
	}
	return string(out), nil
}
