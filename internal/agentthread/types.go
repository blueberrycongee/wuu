package agentthread

import "time"

type Status string

const (
	StatusPending   Status = "pending"
	StatusRunning   Status = "running"
	StatusCompleted Status = "completed"
	StatusFailed    Status = "failed"
	StatusCancelled Status = "cancelled"
	// StatusWaitingChildren mirrors subagent.StatusWaitingChildren: the
	// thread's run finished its final message but holds delivery while
	// direct children are still live.
	StatusWaitingChildren Status = "waiting_children"
)

type SourceKind string

const (
	SourceRoot        SourceKind = "root"
	SourceThreadSpawn SourceKind = "thread_spawn"
)

type EdgeStatus string

const (
	EdgeOpen   EdgeStatus = "open"
	EdgeClosed EdgeStatus = "closed"
)

type Source struct {
	Kind           SourceKind `json:"kind"`
	ParentThreadID string     `json:"parent_thread_id,omitempty"`
	ParentPath     string     `json:"parent_path,omitempty"`
	Depth          int        `json:"depth,omitempty"`
	ForkMode       string     `json:"fork_mode,omitempty"`
	EdgeStatus     EdgeStatus `json:"edge_status,omitempty"`
}

type Metadata struct {
	ID              string    `json:"id"`
	SessionID       string    `json:"session_id,omitempty"`
	ParentID        string    `json:"parent_id,omitempty"`
	Path            string    `json:"path"`
	TaskName        string    `json:"task_name,omitempty"`
	AgentProfile    string    `json:"agent_profile,omitempty"`
	Role            string    `json:"role,omitempty"`
	LastTaskMessage string    `json:"last_task_message,omitempty"`
	CWD             string    `json:"cwd,omitempty"`
	Model           string    `json:"model,omitempty"`
	Source          Source    `json:"source"`
	Status          Status    `json:"status"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

type SpawnSpec struct {
	ID              string
	SessionID       string
	ParentID        string
	ParentPath      string
	TaskName        string
	AgentProfile    string
	Role            string
	LastTaskMessage string
	CWD             string
	Model           string
	SourceKind      SourceKind
	ForkMode        string
	Status          Status
	Now             time.Time
}

type EventType string

const (
	EventThreadUpsert  EventType = "thread_upsert"
	EventStatusChange  EventType = "status_change"
	EventEdgeChange    EventType = "edge_change"
	EventMessage       EventType = "message"
	EventResultReady   EventType = "agent_result_ready"
	EventResultClaim   EventType = "agent_result_claim"
	EventResultRelease EventType = "agent_result_release"
)

type Event struct {
	Type          EventType  `json:"type"`
	ThreadID      string     `json:"thread_id"`
	AgentID       string     `json:"agent_id,omitempty"`
	ParentID      string     `json:"parent_id,omitempty"`
	ResultID      string     `json:"result_id,omitempty"`
	Consumer      string     `json:"consumer,omitempty"`
	Path          string     `json:"path,omitempty"`
	Status        Status     `json:"status,omitempty"`
	EdgeStatus    EdgeStatus `json:"edge_status,omitempty"`
	AuthorPath    string     `json:"author_path,omitempty"`
	RecipientPath string     `json:"recipient_path,omitempty"`
	Message       string     `json:"message,omitempty"`
	TriggerTurn   bool       `json:"trigger_turn,omitempty"`
	Metadata      *Metadata  `json:"metadata,omitempty"`
	CompletedAt   time.Time  `json:"completed_at,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
}
