// Package subagent runs isolated sub-agents that the coordinator can
// spawn to perform focused tasks. Each sub-agent has its own chat
// history, its own tool subset, runs against the same LLM client, and
// returns its final assistant message back to the orchestrator.
//
// This package is provider-agnostic — it operates over the providers
// abstraction so any backend wuu supports also supports sub-agents.
package subagent

import (
	"context"
	"sync"
	"time"

	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/providers"
)

// Status describes a sub-agent's lifecycle state.
type Status string

const (
	StatusPending   Status = "pending"
	StatusRunning   Status = "running"
	StatusCompleted Status = "completed"
	StatusFailed    Status = "failed"
	StatusCancelled Status = "cancelled"

	// DefaultMaxLifetime is the maximum wall-clock time a worker can run
	// before being forcibly cancelled. Prevents runaway workers from
	// consuming resources indefinitely.
	DefaultMaxLifetime = 2 * time.Hour
)

// SpawnOptions describes a new sub-agent to launch.
type SpawnOptions struct {
	// Type is a label like "explorer", "worker", "verifier" that the
	// caller uses to track agent roles. The subagent package itself
	// doesn't interpret it — type-specific tool whitelists and prompts
	// are decided by the caller.
	Type string

	// Description is a short human-readable summary of the task,
	// shown in status displays.
	Description string

	// Prompt is the initial user-role message sent to the sub-agent.
	// It must be self-contained: the sub-agent does not see the
	// orchestrator's history.
	Prompt string

	// SystemPrompt is the system-role message that establishes the
	// sub-agent's role and constraints.
	SystemPrompt string

	// Toolkit is the tool executor the sub-agent will use. It should
	// already be configured with the correct rootDir (e.g. a worktree
	// path) and any tool restrictions.
	Toolkit agent.ToolExecutor

	// Model is the model identifier to use. Empty inherits whatever
	// the runner's default is.
	Model string

	// MaxSteps caps how many tool-use rounds the sub-agent can run
	// before being forced to wrap up. Zero uses the runner default.
	MaxSteps int

	// HistoryPath, if set, is the absolute path where this sub-agent's
	// JSONL history should be persisted.
	HistoryPath string

	// MaxLifetime caps how long a worker can run before being forcibly
	// cancelled. Zero defaults to DefaultMaxLifetime (2h).
	MaxLifetime time.Duration

	// InitialHistory, when non-nil, seeds the worker's conversation
	// with this exact message slice instead of starting from
	// [system, user_prompt]. Used by fork_agent so the worker
	// inherits the parent's full history verbatim — which is what
	// makes prompt-cache hit work across the fork boundary.
	//
	// When InitialHistory is set:
	//   - SystemPrompt on this struct is IGNORED. The system message
	//     is whatever the caller put at history[0] (typically the
	//     parent's system prompt verbatim, for cache friendliness).
	//   - Prompt becomes the FINAL user message appended to the
	//     history, not the only user message. It is the place to
	//     inject role-override instructions (e.g. wrapped in a
	//     <system-reminder> block).
	//   - The history MUST end with a complete turn — no dangling
	//     tool_use without a matching tool_result, or the provider
	//     API will reject the worker's first request. The caller
	//     (fork_agent) is responsible for stripping any in-flight
	//     tool_use blocks before passing the history through.
	InitialHistory []providers.ChatMessage
}

// SubAgent is an isolated agent instance managed by Manager.
type SubAgent struct {
	ID           string
	Type         string
	Description  string
	Status       Status
	StartedAt    time.Time
	CompletedAt  time.Time
	Result       string // final assistant message text
	Error        error  // populated when Status == failed
	InputTokens  int    // cumulative input tokens used so far
	OutputTokens int    // cumulative output tokens used so far

	// Activity is a short, human-readable phrase describing what the
	// sub-agent is currently doing ("→ read_file", "thinking",
	// "responding"). Mutated by the event callback in Manager.run and
	// read via Snapshot. Only changes when the phase changes, so the
	// observer sees transitions rather than a stream of deltas.
	Activity   string
	ActivityAt time.Time

	// Internal state — read-only from outside.
	prompt         string
	systemPrompt   string
	model          string
	toolkit        agent.ToolExecutor
	historyPath    string
	initialHistory []providers.ChatMessage
	// Follow-up messages queued by the coordinator while this worker
	// is already running. Manager.run drains this queue between model
	// turns and appends each entry as a new user message.
	pendingMessages []string

	// LLM client for the sub-agent's runner. Workers run through the
	// streaming runner so they share the same transport semantics as
	// the interactive main agent — most importantly, they avoid the
	// short idle timeouts that proxies tend to apply to non-stream
	// HTTP requests during long worker runs.
	client providers.StreamClient

	// Lifecycle plumbing.
	cancelFunc context.CancelFunc
	doneCh     chan struct{}

	// Synchronizes Status / Result / Error access between the runner
	// goroutine and observers.
	mu sync.Mutex
}

// Snapshot returns a read-only copy of the sub-agent's public fields.
// Safe to call from any goroutine.
func (s *SubAgent) Snapshot() SubAgentSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	return SubAgentSnapshot{
		ID:           s.ID,
		Type:         s.Type,
		Description:  s.Description,
		Status:       s.Status,
		StartedAt:    s.StartedAt,
		CompletedAt:  s.CompletedAt,
		Result:       s.Result,
		Error:        s.Error,
		InputTokens:  s.InputTokens,
		OutputTokens: s.OutputTokens,
		Activity:     s.Activity,
		ActivityAt:   s.ActivityAt,
	}
}

// SubAgentSnapshot is an immutable view of a SubAgent's state at a
// point in time.
type SubAgentSnapshot struct {
	ID           string
	Type         string
	Description  string
	Status       Status
	StartedAt    time.Time
	CompletedAt  time.Time
	Result       string
	Error        error
	InputTokens  int
	OutputTokens int
	Activity     string
	ActivityAt   time.Time
}

// Notification is sent to listeners when a sub-agent's status changes
// (started, completed, failed, cancelled).
type Notification struct {
	AgentID  string
	Status   Status
	Snapshot SubAgentSnapshot
}
