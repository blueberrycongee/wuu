package harness

import (
	"encoding/json"
	"time"
)

// TaskStatus describes a durable task node in the harness graph.
type TaskStatus string

const (
	TaskStatusPending   TaskStatus = "pending"
	TaskStatusQueued    TaskStatus = "queued"
	TaskStatusRunning   TaskStatus = "running"
	TaskStatusCompleted TaskStatus = "completed"
	TaskStatusFailed    TaskStatus = "failed"
	TaskStatusCancelled TaskStatus = "cancelled"
	// TaskStatusInterrupted is the terminal status stamped by startup
	// reconciliation on a task that was still pending/queued/running in the
	// durable record while no live executor existed for it (the recording
	// process died mid-run). It preserves the original timestamps and any
	// recorded error; the run can be revived through a follow-up when a
	// resumable worker snapshot exists.
	TaskStatusInterrupted TaskStatus = "interrupted"
)

// legacyTaskStatusAwaitingReport is the removed lifecycle status that older
// builds persisted for a worker that finished without filing agent_report.
// Lifecycle is now owned by runtime facts (a loop that ended without error is
// completed, unconditionally), so this value only survives as a read-time
// migration alias.
const legacyTaskStatusAwaitingReport TaskStatus = "awaiting_report"

// NormalizeTaskStatus maps any legacy persisted status onto the converged
// lifecycle enum. It is applied at every deserialization point so no consumer
// ever observes a removed status.
func NormalizeTaskStatus(status TaskStatus) TaskStatus {
	if status == legacyTaskStatusAwaitingReport {
		return TaskStatusCompleted
	}
	return status
}

// WorkspaceMode records where a task is allowed to execute.
type WorkspaceMode string

const (
	WorkspaceShared   WorkspaceMode = "shared"
	WorkspaceWorktree WorkspaceMode = "worktree"
)

// ArtifactKind classifies durable outputs that later agents can inspect.
type ArtifactKind string

const (
	ArtifactReport       ArtifactKind = "report"
	ArtifactPatch        ArtifactKind = "patch"
	ArtifactArchive      ArtifactKind = "archive"
	ArtifactCommand      ArtifactKind = "command_output"
	ArtifactLog          ArtifactKind = "log"
	ArtifactManifest     ArtifactKind = "manifest"
	ArtifactEvidence     ArtifactKind = "evidence"
	ArtifactResult       ArtifactKind = "agent_result"
	ArtifactConversation ArtifactKind = "conversation"
)

// EventType describes append-only harness lifecycle facts.
type EventType string

const (
	EventTaskCreated       EventType = "task_created"
	EventTaskStatusChanged EventType = "task_status_changed"
	EventWorkspaceAssigned EventType = "workspace_assigned"
	EventRunStarted        EventType = "run_started"
	EventRunCompleted      EventType = "run_completed"
	EventMessageSent       EventType = "message_sent"
	EventReportSubmitted   EventType = "report_submitted"
	EventArtifactRecorded  EventType = "artifact_recorded"
)

// WorkspaceLease is the execution location assigned to a task.
type WorkspaceLease struct {
	Mode      WorkspaceMode `json:"mode"`
	Root      string        `json:"root,omitempty"`
	BaseRepo  string        `json:"base_repo,omitempty"`
	CreatedAt time.Time     `json:"created_at,omitempty"`
}

// Task is the durable source of truth for one harness task node.
type Task struct {
	ID            string         `json:"id"`
	SessionID     string         `json:"session_id,omitempty"`
	ParentID      string         `json:"parent_id,omitempty"`
	ParentPath    string         `json:"parent_path,omitempty"`
	Path          string         `json:"path,omitempty"`
	Name          string         `json:"name,omitempty"`
	Role          string         `json:"role,omitempty"`
	Intent        string         `json:"intent,omitempty"`
	Constraints   []string       `json:"constraints,omitempty"`
	Workspace     WorkspaceLease `json:"workspace,omitempty"`
	Status        TaskStatus     `json:"status"`
	LastRunID     string         `json:"last_run_id,omitempty"`
	CardItemID    string         `json:"card_item_id,omitempty"`
	CreatedAt     time.Time      `json:"created_at"`
	UpdatedAt     time.Time      `json:"updated_at"`
	StartedAt     time.Time      `json:"started_at,omitempty"`
	CompletedAt   time.Time      `json:"completed_at,omitempty"`
	InputTokens   int            `json:"input_tokens,omitempty"`
	OutputTokens  int            `json:"output_tokens,omitempty"`
	Error         string         `json:"error,omitempty"`
	ReportPath    string         `json:"report_path,omitempty"`
	ArtifactPaths []string       `json:"artifact_paths,omitempty"`
}

// AgentRun records one execution attempt for a task.
type AgentRun struct {
	ID           string     `json:"id"`
	TaskID       string     `json:"task_id"`
	AgentID      string     `json:"agent_id,omitempty"`
	Role         string     `json:"role,omitempty"`
	Model        string     `json:"model,omitempty"`
	Status       TaskStatus `json:"status"`
	StartedAt    time.Time  `json:"started_at"`
	CompletedAt  time.Time  `json:"completed_at,omitempty"`
	InputTokens  int        `json:"input_tokens,omitempty"`
	OutputTokens int        `json:"output_tokens,omitempty"`
	Error        string     `json:"error,omitempty"`
}

// Artifact points to durable evidence, reports, patches, or logs.
type Artifact struct {
	ID        string       `json:"id"`
	TaskID    string       `json:"task_id"`
	RunID     string       `json:"run_id,omitempty"`
	Kind      ArtifactKind `json:"kind"`
	Path      string       `json:"path,omitempty"`
	Summary   string       `json:"summary,omitempty"`
	CreatedAt time.Time    `json:"created_at"`
}

// EvidenceRef keeps a report tied to source material instead of only prose.
type EvidenceRef struct {
	Type    string `json:"type,omitempty"`
	Path    string `json:"path,omitempty"`
	Line    int    `json:"line,omitempty"`
	Command string `json:"command,omitempty"`
	Output  string `json:"output,omitempty"`
	Note    string `json:"note,omitempty"`
}

// ReportKind classifies how a run's report was produced.
type ReportKind string

const (
	// ReportKindStructured is a report the agent submitted via agent_report.
	ReportKindStructured ReportKind = "structured"
	// ReportKindFinalText is a report the runtime synthesized from observed
	// facts (final text, changed files, artifacts) when the agent finished
	// without submitting a structured handoff.
	ReportKindFinalText ReportKind = "final_text"
)

// NormalizeReportKind resolves the report kind, defaulting a legacy report
// with no recorded kind to structured (all pre-migration reports came from
// agent_report).
func NormalizeReportKind(kind ReportKind) ReportKind {
	if kind == ReportKindFinalText {
		return ReportKindFinalText
	}
	return ReportKindStructured
}

// Report is the handoff packet for a run. It is either a structured packet the
// agent submitted via agent_report, or a final_text packet the runtime
// synthesized from observed facts. Kind records which.
type Report struct {
	ID           string        `json:"id"`
	TaskID       string        `json:"task_id"`
	RunID        string        `json:"run_id,omitempty"`
	AgentID      string        `json:"agent_id,omitempty"`
	AgentPath    string        `json:"agent_path,omitempty"`
	Kind         ReportKind    `json:"kind,omitempty"`
	Outcome      string        `json:"outcome"`
	Summary      string        `json:"summary,omitempty"`
	ChangedFiles []string      `json:"changed_files,omitempty"`
	WorkDone     []string      `json:"work_done,omitempty"`
	Blockers     []string      `json:"blockers,omitempty"`
	Risks        []string      `json:"risks,omitempty"`
	Verification []string      `json:"verification,omitempty"`
	NextSteps    []string      `json:"next_steps,omitempty"`
	Evidence     []EvidenceRef `json:"evidence,omitempty"`
	Artifacts    []string      `json:"artifacts,omitempty"`
	RawResult    string        `json:"raw_result,omitempty"`
	ReportPath   string        `json:"report_path,omitempty"`
	SubmittedAt  time.Time     `json:"submitted_at"`
}

// Event is one append-only fact in the task graph.
type Event struct {
	Type      EventType `json:"type"`
	TaskID    string    `json:"task_id,omitempty"`
	RunID     string    `json:"run_id,omitempty"`
	AgentID   string    `json:"agent_id,omitempty"`
	Path      string    `json:"path,omitempty"`
	Status    string    `json:"status,omitempty"`
	Message   string    `json:"message,omitempty"`
	Artifact  string    `json:"artifact,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

type QueueItemState string

const (
	// QueueItemStateQueued is also represented by the empty value for backward
	// compatibility with queue records written before state was explicit.
	QueueItemStateQueued     QueueItemState = "queued"
	QueueItemStateCancelling QueueItemState = "cancelling"
	QueueItemStateFailing    QueueItemState = "failing"
)

// QueueItem stores the durable payload needed to start a queued task. A
// cancelling item is a tombstone: recovery must compensate and settle it,
// never launch it.
type QueueItem struct {
	ID        string          `json:"id"`
	TaskID    string          `json:"task_id"`
	Kind      string          `json:"kind"`
	State     QueueItemState  `json:"state,omitempty"`
	Error     string          `json:"error,omitempty"`
	Payload   json.RawMessage `json:"payload"`
	CreatedAt time.Time       `json:"created_at"`
	UpdatedAt time.Time       `json:"updated_at"`
}
