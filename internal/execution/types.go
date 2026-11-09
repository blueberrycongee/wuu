package execution

import "time"

type Status string

const (
	StatusAccepted    Status = "accepted"
	StatusRunning     Status = "running"
	StatusCompleted   Status = "completed"
	StatusFailed      Status = "failed"
	StatusInterrupted Status = "interrupted"
	StatusTimedOut    Status = "timed_out"
	StatusCancelled   Status = "cancelled"
)

func (s Status) Terminal() bool {
	switch s {
	case StatusCompleted, StatusFailed, StatusInterrupted, StatusTimedOut, StatusCancelled:
		return true
	default:
		return false
	}
}

type Mode string

const (
	ModeStart  Mode = "start"
	ModeResume Mode = "resume"
	ModeFork   Mode = "fork"
	ModeReview Mode = "review"
)

type WorkspaceRef struct {
	ID   string `json:"id,omitempty"`
	Root string `json:"root"`
}

type Selection struct {
	Provider       string `json:"provider,omitempty"`
	Model          string `json:"model,omitempty"`
	Variant        string `json:"variant,omitempty"`
	Effort         string `json:"effort,omitempty"`
	PermissionMode string `json:"permission_mode,omitempty"`
}

// Request is the canonical Wuu-owned invocation manifest. User content and
// attachments live on the referenced Turn; environment and Git state remain
// external execution inputs and are intentionally not copied here.
type Request struct {
	Mode             Mode      `json:"mode"`
	SourceThreadID   string    `json:"source_thread_id,omitempty"`
	Requested        Selection `json:"requested,omitempty"`
	AgentProfile     string    `json:"agent_profile,omitempty"`
	MaxTurns         int       `json:"max_turns,omitempty"`
	TimeoutMS        int64     `json:"timeout_ms,omitempty"`
	NoTools          bool      `json:"no_tools,omitempty"`
	HasPrompt        bool      `json:"has_prompt"`
	ImageCount       int       `json:"image_count,omitempty"`
	FileCount        int       `json:"file_count,omitempty"`
	StructuredOutput bool      `json:"structured_output,omitempty"`
}

type RuntimeManifest struct {
	Resolved        Selection `json:"resolved"`
	ProtocolVersion string    `json:"protocol_version"`
	CoreVersion     string    `json:"core_version,omitempty"`
	CoreCommit      string    `json:"core_commit,omitempty"`
	MaxParallel     int       `json:"max_parallel"`
}

type Error struct {
	Code       string `json:"code,omitempty"`
	Category   string `json:"category,omitempty"`
	Message    string `json:"message"`
	Provider   string `json:"provider,omitempty"`
	StatusCode int    `json:"status_code,omitempty"`
}

type Result struct {
	FinalTurnID string `json:"final_turn_id,omitempty"`
	FinalItemID string `json:"final_item_id,omitempty"`
	TracePath   string `json:"trace_path,omitempty"`
	ExitCode    int    `json:"exit_code,omitempty"`
}

type TurnRef struct {
	TurnID     string    `json:"turn_id"`
	ThreadID   string    `json:"thread_id"`
	Ordinal    int       `json:"ordinal"`
	TracePath  string    `json:"trace_path,omitempty"`
	AttachedAt time.Time `json:"attached_at"`
}

// Run is the execution record of one invocation. Higher-level orchestration
// (such as automation schedules) keeps its own dispatch record and
// references the execution Run; it does not redefine execution state here.
type Run struct {
	ID          string          `json:"id"`
	RuntimeID   string          `json:"runtime_id,omitempty"`
	Status      Status          `json:"status"`
	Request     Request         `json:"request"`
	Runtime     RuntimeManifest `json:"runtime"`
	Workspace   WorkspaceRef    `json:"workspace"`
	ThreadID    string          `json:"thread_id"`
	Turns       []TurnRef       `json:"turns"`
	Result      *Result         `json:"result,omitempty"`
	Error       *Error          `json:"error,omitempty"`
	Ephemeral   bool            `json:"ephemeral,omitempty"`
	CreatedAt   time.Time       `json:"created_at"`
	StartedAt   *time.Time      `json:"started_at,omitempty"`
	UpdatedAt   time.Time       `json:"updated_at"`
	CompletedAt *time.Time      `json:"completed_at,omitempty"`
}

type CreateParams struct {
	RuntimeID string
	Request   Request
	Runtime   RuntimeManifest
	Workspace WorkspaceRef
	ThreadID  string
	Ephemeral bool
}

type TurnTerminal struct {
	TracePath string
	At        time.Time
}

type ListOptions struct {
	WorkspaceID   string
	WorkspaceRoot string
	ThreadID      string
	Status        Status
	Limit         int
}
