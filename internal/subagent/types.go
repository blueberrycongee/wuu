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

	// Internal state — read-only from outside.
	prompt       string
	systemPrompt string
	model        string
	toolkit      agent.ToolExecutor
	historyPath  string

	// LLM client for the sub-agent's runner.
	client providers.Client

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
}

// Notification is sent to listeners when a sub-agent's status changes
// (started, completed, failed, cancelled).
type Notification struct {
	AgentID  string
	Status   Status
	Snapshot SubAgentSnapshot
}
