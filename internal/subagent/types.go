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
	"github.com/blueberrycongee/wuu/internal/loopdriver"
	"github.com/blueberrycongee/wuu/internal/persistence"
	"github.com/blueberrycongee/wuu/internal/provideroptions"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/toolledger"
)

// WorkerRuntime is a complete immutable worker inference runtime resolved from
// a configured model alias. It carries every value the runner needs so that
// once a sub-agent starts, its runtime stays fixed across follow-up turns and
// cross-restart resume regardless of later settings changes.
//
// The Client field is live state and is intentionally not persisted; resume
// rebuilds it from the persisted Provider via an installed provider-client
// resolver.
type WorkerRuntime struct {
	Provider                string
	Model                   string
	APIModel                string
	Effort                  string
	Variant                 string
	ProviderOptions         map[string]any
	Temperature             float64
	ContextWindow           int
	MaxInputTokens          int
	OutputReserveTokens     int
	CompactThresholdTokens  int
	CompactThresholdPct     float64
	CompactKeepRecentTokens int
	DisableAutoCompact      bool
	Client                  providers.StreamClient
}

// Clone returns a deep copy of the runtime with the Client pointer shared (it
// is not persisted and is replaced by resume anyway).
func (r WorkerRuntime) Clone() WorkerRuntime {
	out := r
	out.ProviderOptions = provideroptions.Clone(r.ProviderOptions)
	return out
}

// Status describes a sub-agent's lifecycle state.
type Status string

const (
	StatusPending   Status = "pending"
	StatusQueued    Status = "queued"
	StatusRunning   Status = "running"
	StatusCompleted Status = "completed"
	StatusFailed    Status = "failed"
	StatusCancelled Status = "cancelled"
	// StatusInterrupted is the terminal state stamped by startup
	// reconciliation when an owner exited before the run or its final outcome
	// was durably settled. A candidate result may exist, but its outcome is
	// unconfirmed and tool effects may already have occurred. Interrupted runs
	// can be explicitly resumed when a usable worker snapshot exists.
	StatusInterrupted Status = "interrupted"
	// StatusWaitingChildren parks a run that produced its final message
	// while direct children were still live. The result is held (not yet
	// delivered to the parent), no goroutine runs and no execution slot is
	// consumed; a child delivery resumes the run through Followup so it can
	// integrate the results before one final parent delivery. Not terminal:
	// the run becomes terminal only once no direct child remains live.
	StatusWaitingChildren Status = "waiting_children"
)

// IsTerminal reports whether the status is a terminal lifecycle state — the
// run has no live goroutine and can accept a Followup to start a new turn.
func IsTerminal(status Status) bool {
	switch status {
	case StatusCompleted, StatusFailed, StatusCancelled, StatusInterrupted:
		return true
	default:
		return false
	}
}

// SpawnOptions describes a new sub-agent to launch.
type SpawnOptions struct {
	// ID, when set, is used as the sub-agent ID instead of generating
	// a new one. The coordinator uses this to keep worktree IDs,
	// thread metadata, and visible agent IDs aligned.
	ID string

	// ParticipantID is the persisted participant identity assigned to this run.
	// The subagent package carries it through runtime snapshots and history.
	ParticipantID string

	// Type is a label like "explorer", "worker", "verifier" that the
	// caller uses to track agent roles. The subagent package itself
	// doesn't interpret it — type-specific tool whitelists and prompts
	// are decided by the caller.
	Type string

	// TaskName is a stable human-provided task identifier. AgentPath is
	// the canonical path in the root thread's agent tree.
	TaskName     string
	AgentProfile string
	AgentPath    string
	ParentID     string

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

	// ModelAlias is the configured alias requested by the caller (e.g.
	// "cheap" or "frontend"). It is recorded for provenance and diagnostics.
	// The resolved runtime is supplied via WorkerRuntime. AgentControl also
	// snapshots the current worker default into WorkerRuntime for omitted or
	// unknown aliases so that runtime remains fixed after the first run.
	ModelAlias string

	// ModelAliasFallback is true when ModelAlias was non-empty but could not
	// be resolved to a configured alias. The worker falls back to the
	// current default runtime and the fallback is surfaced in diagnostics.
	ModelAliasFallback bool

	// WorkerRuntime, when non-nil, overrides the manager's default runtime
	// for this spawn. It carries the complete resolved alias runtime
	// (provider, model, effort, provider options, budgets, temperature).
	// Once set, the runtime is snapshotted and persisted so follow-ups and
	// resume reuse it exactly.
	WorkerRuntime *WorkerRuntime

	// WorkerRoot is the sub-agent's working directory: the shared parent
	// repo for inplace workers or the worktree path for isolated ones. It
	// is persisted with the run so a follow-up after a restart can rebuild
	// the toolkit rooted at the same place and confirm the directory still
	// exists before resuming.
	WorkerRoot string

	// ModelPin is the raw per-participant model pin (e.g. "provider:model"
	// or "model"). It is persisted so a resumed run can rebuild a
	// cross-provider client the same way queued-spawn restore does. Empty
	// means the run honors only Model.
	ModelPin string

	// Client, when non-nil, replaces the stream client the runner
	// would otherwise inherit from the manager defaults. Callers use
	// it to honor a per-participant model pin that points at a
	// different provider — the coordinator builds a dedicated stream
	// client for that provider and passes it through here. Runtime
	// fields on the agent control keep the override scoped to this
	// single spawn; the manager defaults are not modified.
	Client providers.StreamClient

	// ProviderName names the provider Client belongs to, so worker-produced
	// native state (Responses item ids, reasoning signatures) is persisted
	// with the provider that minted it. Ignored when Client is nil — the
	// spawn then inherits the manager defaults' client and provider name as
	// a pair.
	ProviderName string

	// MaxSteps caps how many tool-use rounds the sub-agent can run
	// before being forced to wrap up. Zero uses the runner default.
	MaxSteps int

	// HistoryPath, if set, is the absolute path where this sub-agent's
	// JSONL history should be persisted.
	HistoryPath      string
	HistoryDocuments persistence.Documents
	HistoryScope     string

	// MaxLifetime caps how long a worker can run before being forcibly
	// cancelled. Zero means no hard runtime lifetime; callers can still
	// cancel the worker through the context or Stop.
	MaxLifetime time.Duration

	// InitialHistory, when non-nil, seeds the worker's conversation
	// with this exact message slice instead of starting from
	// [system, user_prompt]. Used by spawn_agent fork spawns so the
	// worker inherits the parent's history verbatim, which is what
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
	//     API will reject the worker's first request. The caller is
	//     responsible for stripping any in-flight tool_use blocks before
	//     passing the history through.
	InitialHistory []providers.ChatMessage
}

// terminalWaitState retains an unconfirmed outcome for waiters of one turn.
// It remains valid when an explicit follow-up starts another turn.
type terminalWaitState struct {
	done     chan struct{}
	err      error
	snapshot SubAgentSnapshot
}

// SubAgent is an isolated agent instance managed by Manager.
type SubAgent struct {
	ID                  string
	ParticipantID       string
	Type                string
	TaskName            string
	AgentProfile        string
	AgentPath           string
	ParentID            string
	Description         string
	Status              Status
	StartedAt           time.Time
	CompletedAt         time.Time
	Result              string // final assistant message text
	Error               error  // populated when Status == failed
	InputTokens         int    // cumulative input tokens used so far
	OutputTokens        int    // cumulative output tokens used so far
	CacheCreationTokens int    // cumulative prompt-cache write tokens
	CacheReadTokens     int    // cumulative prompt-cache read tokens

	// Activity is a short, human-readable phrase describing what the
	// sub-agent is currently doing ("→ read_file", "thinking",
	// "responding"). Mutated by the event callback in Manager.run and
	// read via Snapshot. Only changes when the phase changes, so the
	// observer sees transitions rather than a stream of deltas.
	Activity   string
	ActivityAt time.Time

	// Internal state — read-only from outside.
	prompt             string
	systemPrompt       string
	model              string
	modelPin           string         // raw participant pin, persisted for cross-restart resume
	modelAlias         string         // configured alias requested at spawn time
	modelAliasFallback bool           // true when modelAlias was unknown and the default runtime was used
	runtime            *WorkerRuntime // complete resolved runtime; overrides manager defaults when non-nil
	driverExecutionID  string
	driverBinding      *loopdriver.Binding
	driverCheckpoint   *loopdriver.Checkpoint
	workerRoot         string // working directory, persisted so a resume can validate + reroot
	toolkit            agent.ToolExecutor
	historyPath        string
	historyDocuments   persistence.Documents
	historyScope       string
	initialHistory     []providers.ChatMessage
	history            []providers.ChatMessage
	maxSteps           int
	maxLifetime        time.Duration
	runtimeDefaults    managerDefaults
	// Follow-up messages queued by the coordinator while this worker
	// is already running. Manager.run drains this queue between model
	// turns and appends each entry as a new user message.
	pendingMessages []string
	// forceToolNextTurn, when non-empty, pins the first request of the
	// next turn to the named tool (forced tool_choice). Set by
	// FollowupForcingTool for mechanical closing turns; consumed and
	// cleared by runTurn.
	forceToolNextTurn string

	// LLM client for the sub-agent's runner. Workers run through the
	// streaming runner so they share the same transport semantics as
	// the interactive main agent — most importantly, they avoid the
	// short idle timeouts that proxies tend to apply to non-stream
	// HTTP requests during long worker runs.
	client providers.StreamClient
	// providerName names the provider client belongs to; it is stamped on
	// the runner so persisted native state carries its provider of origin.
	// Immutable after construction, like client.
	providerName string
	toolLedger   *toolledger.Ledger

	// Lifecycle plumbing.
	cancelFunc       context.CancelFunc
	doneCh           chan struct{}
	waitState        *terminalWaitState
	initialWaitState *terminalWaitState
	terminalPending  bool

	// Synchronizes Status / Result / Error access between the runner
	// goroutine and observers.
	mu sync.Mutex
}

// Snapshot returns a read-only copy of the sub-agent's public fields.
// Safe to call from any goroutine.
func (s *SubAgent) Snapshot() SubAgentSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	snap := snapshotLocked(s)
	if s.terminalPending && IsTerminal(snap.Status) {
		snap.Status = StatusRunning
		snap.CompletedAt = time.Time{}
		snap.Result = ""
		snap.Error = nil
	}
	return snap
}

// CoordinatorSnapshot exposes the terminal candidate only to the owner that
// serializes worker execution and confirmation. User-facing paths use Snapshot.
func (s *SubAgent) CoordinatorSnapshot() SubAgentSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	return snapshotLocked(s)
}

// HistorySnapshot returns a copy of the worker's current conversation.
func (s *SubAgent) HistorySnapshot() []providers.ChatMessage {
	s.mu.Lock()
	defer s.mu.Unlock()
	return providers.CloneChatMessages(s.history)
}

// SubAgentSnapshot is an immutable view of a SubAgent's state at a
// point in time.
type SubAgentSnapshot struct {
	ID                  string
	ParticipantID       string
	Type                string
	TaskName            string
	AgentProfile        string
	AgentPath           string
	ParentID            string
	Description         string
	WorkerRoot          string
	Model               string
	ModelPin            string
	ModelAlias          string
	ModelAliasFallback  bool
	ResolvedProvider    string
	ResolvedModel       string
	ResolvedAPIModel    string
	ResolvedEffort      string
	ResolvedVariant     string
	Status              Status
	StartedAt           time.Time
	CompletedAt         time.Time
	Result              string
	Error               error
	InputTokens         int
	OutputTokens        int
	CacheCreationTokens int
	CacheReadTokens     int
	PendingMessageCount int
	Activity            string
	ActivityAt          time.Time
}

// Notification is sent to listeners when a sub-agent's status changes
// (started, completed, failed, cancelled).
type Notification struct {
	TerminalGeneration  string
	TerminalPrepared    bool
	TerminalUnconfirmed bool
	SnapshotOnly        bool
	AgentID             string
	Status              Status
	Snapshot            SubAgentSnapshot
}

// StreamNotification is sent to listeners for every model/tool stream event
// emitted by a sub-agent turn.
type StreamNotification struct {
	AgentID  string
	Snapshot SubAgentSnapshot
	Event    providers.StreamEvent
}
