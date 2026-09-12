package channels

import "time"

const (
	MaxRoomMembers  = 32
	MaxRoomAgents   = 6
	MaxMessageRunes = 4000
	// Verification reports leave room for the host-owned decision and next-action
	// wrapper while keeping the delivered internal message within MaxMessageRunes.
	MaxVerificationReportRunes = 3200
	DraftExpiry                = 24 * time.Hour
	MinReminderDur             = time.Minute
	ThreadStreakCap            = 6
	// NamedAgentIDEnv marks subprocesses launched by a named-agent tool
	// runtime. Human-only CLI entrypoints use it to avoid attributing an
	// agent-authored channel message to the local user.
	NamedAgentIDEnv = "WUU_NAMED_AGENT_ID"
)

type RoomKind string

const (
	RoomChannel RoomKind = "channel"
	RoomDM      RoomKind = "dm"
)

type MemberType string

const (
	MemberHuman MemberType = "human"
	MemberAgent MemberType = "agent"
)

type PrincipalKind string

const (
	PrincipalNamedAgent  PrincipalKind = "named_agent"
	PrincipalRoomRuntime PrincipalKind = "room_runtime"
)

type MessageKind string

const (
	MessageText   MessageKind = "text"
	MessageTask   MessageKind = "task"
	MessageSystem MessageKind = "system"
)

type TaskState string

const (
	TaskStateOpen       TaskState = "open"
	TaskStateDoing      TaskState = "doing"
	TaskStateChecking   TaskState = "checking"
	TaskStateRevising   TaskState = "revising"
	TaskStateNeedsHuman TaskState = "needs_human"
	TaskStateDone       TaskState = "done"
)

type InboxKind string

const (
	InboxMention      InboxKind = "mention"
	InboxReply        InboxKind = "reply"
	InboxThreadUpdate InboxKind = "thread_update"
	InboxTask         InboxKind = "task"
	InboxReminder     InboxKind = "reminder"
)

type CollaborationKind string

const (
	CollaborationControl              CollaborationKind = "control"
	CollaborationAssignment           CollaborationKind = "assignment"
	CollaborationPeerResult           CollaborationKind = "peer_result"
	CollaborationCandidateReady       CollaborationKind = "candidate_ready"
	CollaborationVerificationFeedback CollaborationKind = "verification_feedback"
	CollaborationCompletion           CollaborationKind = "completion"
	CollaborationWorkRunTerminal      CollaborationKind = "work_run_terminal"
)

type CollaborationTargetKind string

const (
	CollaborationTargetRoom        CollaborationTargetKind = "room"
	CollaborationTargetNamedAgent  CollaborationTargetKind = "named_agent"
	CollaborationTargetSession     CollaborationTargetKind = "session"
	CollaborationTargetRoomRuntime CollaborationTargetKind = "room_runtime"
)

type CollaborationVisibility string

const (
	CollaborationVisibilityRoom        CollaborationVisibility = "room"
	CollaborationVisibilityPrivate     CollaborationVisibility = "private"
	CollaborationVisibilityWorkPrivate CollaborationVisibility = "work_private"
	CollaborationVisibilitySystem      CollaborationVisibility = "system"
)

type CollaborationTerminalState string

const (
	CollaborationTerminalCompleted     CollaborationTerminalState = "completed"
	CollaborationTerminalFailed        CollaborationTerminalState = "failed"
	CollaborationTerminalInterrupted   CollaborationTerminalState = "interrupted"
	CollaborationTerminalCancelled     CollaborationTerminalState = "cancelled"
	CollaborationTerminalTimedOut      CollaborationTerminalState = "timed_out"
	CollaborationTerminalUndeliverable CollaborationTerminalState = "undeliverable"
)

type VerificationDecision string

const (
	VerificationPass    VerificationDecision = "pass"
	VerificationBlock   VerificationDecision = "block"
	VerificationUnknown VerificationDecision = "unknown"
)

type ReminderState string

const (
	ReminderPending   ReminderState = "pending"
	ReminderFired     ReminderState = "fired"
	ReminderCancelled ReminderState = "cancelled"
)

type NamedAgent struct {
	ID               string       `json:"id"`
	Name             string       `json:"name"`
	Role             string       `json:"role,omitempty"`
	MemoryDir        string       `json:"memory_dir"`
	AvatarKey        string       `json:"avatar_key"`
	AvatarImage      string       `json:"avatar_image,omitempty"`
	EngineOverride   string       `json:"engine_override,omitempty"`
	ProviderOverride string       `json:"provider_override,omitempty"`
	ModelOverride    string       `json:"model_override,omitempty"`
	EffortOverride   string       `json:"effort_override,omitempty"`
	Autostart        bool         `json:"autostart"`
	CreatedAt        time.Time    `json:"created_at"`
	ActivityStatus   string       `json:"activity_status,omitempty"`
	ActivityRoomIDs  []string     `json:"activity_room_ids,omitempty"`
	SessionCapacity  WorkCapacity `json:"session_capacity"`
}

// AgentRuntime is the internal execution identity used by the wake/session
// host. Room runtimes deliberately do not use NamedAgent or participant data.
type AgentRuntime struct {
	ID               string
	Kind             PrincipalKind
	RoomID           string
	Name             string
	Role             string
	MemoryDir        string
	EngineOverride   string
	ProviderOverride string
	ModelOverride    string
	EffortOverride   string
	Autostart        bool
	CreatedAt        time.Time
}

func (r AgentRuntime) IsRoomRuntime() bool { return r.Kind == PrincipalRoomRuntime }

type AgentCredential struct {
	Agent NamedAgent `json:"agent"`
	Token string     `json:"token"`
}

type CreateNamedAgentParams struct {
	// RequestID makes retries of the same creation return the existing identity.
	RequestID        string
	Name             string
	Role             string
	AvatarKey        string
	AvatarImage      string
	EngineOverride   string
	ProviderOverride string
	ModelOverride    string
	EffortOverride   string
	Autostart        bool
}

type UpdateNamedAgentParams struct {
	ID               string
	Name             string
	Role             string
	AvatarKey        string
	AvatarImage      *string
	EngineOverride   string
	ProviderOverride string
	ModelOverride    string
	EffortOverride   string
}

type BootstrapResult struct {
	Agents []NamedAgent `json:"agents"`
	Rooms  []Room       `json:"rooms"`
}

type RoomMember struct {
	RoomID     string     `json:"room_id"`
	MemberType MemberType `json:"member_type"`
	MemberID   string     `json:"member_id"`
	JoinedAt   time.Time  `json:"joined_at"`
}

type Room struct {
	ID   string   `json:"id"`
	Kind RoomKind `json:"kind"`
	Name string   `json:"name"`
	// RuntimeID is internal routing state, not a participant identity.
	RuntimeID string `json:"-"`
	// AgentID is a source-compatible internal alias for migrations and tests.
	// It is never serialized and is not a Named Agent identity.
	AgentID            string       `json:"-"`
	AvatarKey          string       `json:"avatar_key,omitempty"`
	AvatarImage        string       `json:"avatar_image,omitempty"`
	CreatedBy          string       `json:"created_by"`
	CreatedAt          time.Time    `json:"created_at"`
	MembershipRevision int64        `json:"membership_revision"`
	Members            []RoomMember `json:"members"`
	UnreadCount        int          `json:"unread_count"`
	ActivityStatus     string       `json:"activity_status,omitempty"`
}

type CreateRoomParams struct {
	Kind        RoomKind
	Name        string
	AvatarImage string
	CreatedBy   string
	Members     []RoomMember
}

type UpdateRoomParams struct {
	RoomID  string
	Name    *string
	Members *[]RoomMember
}

type RoomRoster struct {
	RoomID             string                   `json:"room_id"`
	RoomName           string                   `json:"room_name"`
	MembershipRevision int64                    `json:"membership_revision"`
	Members            []AgentCapabilitySummary `json:"members"`
	AvailableAgents    []AgentCapabilitySummary `json:"available_agents"`
}

type AgentCapabilitySummary struct {
	ID               string                `json:"id"`
	Name             string                `json:"name"`
	Role             string                `json:"role,omitempty"`
	MemoryIndex      string                `json:"memory_index,omitempty"`
	EngineOverride   string                `json:"engine_override,omitempty"`
	ProviderOverride string                `json:"provider_override,omitempty"`
	ModelOverride    string                `json:"model_override,omitempty"`
	EffortOverride   string                `json:"effort_override,omitempty"`
	Sessions         []AgentSessionSummary `json:"sessions"`
}

type AgentSessionSummary struct {
	Title            string                      `json:"title,omitempty"`
	Objective        string                      `json:"objective,omitempty"`
	ParentSessionRef string                      `json:"parent_session_ref,omitempty"`
	Provider         string                      `json:"provider,omitempty"`
	Model            string                      `json:"model,omitempty"`
	Effort           string                      `json:"effort,omitempty"`
	RuntimeVersion   string                      `json:"runtime_version,omitempty"`
	SessionRef       string                      `json:"session_ref"`
	RoomID           string                      `json:"room_id,omitempty"`
	WorkID           string                      `json:"work_id,omitempty"`
	RunID            string                      `json:"run_id,omitempty"`
	Purpose          CollaborationSessionPurpose `json:"purpose"`
	State            CollaborationSessionState   `json:"state"`
	UpdatedAt        time.Time                   `json:"updated_at"`
}

type AgentCreationProposalState string

const (
	AgentCreationPending    AgentCreationProposalState = "pending"
	AgentCreationProcessing AgentCreationProposalState = "processing"
	AgentCreationApproved   AgentCreationProposalState = "approved"
	AgentCreationCancelled  AgentCreationProposalState = "cancelled"
)

type AgentCreationProposal struct {
	ID             string                     `json:"id"`
	MessageID      string                     `json:"message_id"`
	RoomID         string                     `json:"room_id"`
	Name           string                     `json:"name"`
	Role           string                     `json:"role"`
	State          AgentCreationProposalState `json:"state"`
	Provider       string                     `json:"provider,omitempty"`
	Model          string                     `json:"model,omitempty"`
	CreatedAgentID string                     `json:"created_agent_id,omitempty"`
	CreatedAt      time.Time                  `json:"created_at"`
	ResolvedAt     *time.Time                 `json:"resolved_at,omitempty"`
}

type ResolveAgentCreationProposalParams struct {
	ProposalID string
	HumanID    string
	Approve    bool
	Provider   string
	Model      string
}

type Message struct {
	ID                       string                 `json:"id"`
	RoomID                   string                 `json:"room_id"`
	Seq                      int64                  `json:"seq"`
	ThreadID                 string                 `json:"thread_id,omitempty"`
	AuthorType               MemberType             `json:"author_type"`
	AuthorID                 string                 `json:"author_id"`
	Kind                     MessageKind            `json:"kind"`
	Body                     string                 `json:"body"`
	Images                   []MessageImage         `json:"images,omitempty"`
	Files                    []MessageFile          `json:"files,omitempty"`
	Mentions                 []string               `json:"mentions"`
	ReplyTo                  string                 `json:"reply_to,omitempty"`
	TaskTitle                string                 `json:"task_title,omitempty"`
	TaskState                string                 `json:"task_state,omitempty"`
	TaskOwner                string                 `json:"task_owner,omitempty"`
	TaskVerificationRequired bool                   `json:"task_verification_required,omitempty"`
	TaskGoalRevision         int                    `json:"task_goal_revision,omitempty"`
	TaskCandidateRevision    int                    `json:"task_candidate_revision,omitempty"`
	AgentCreationProposal    *AgentCreationProposal `json:"agent_creation_proposal,omitempty"`
	Work                     *Work                  `json:"work,omitempty"`
	CreatedAt                time.Time              `json:"created_at"`
}

type MessageImage struct {
	MediaType string `json:"media_type"`
	Data      string `json:"data"`
	Width     uint32 `json:"width,omitempty"`
	Height    uint32 `json:"height,omitempty"`
}

type MessageFile struct {
	MediaType string `json:"media_type"`
	Data      string `json:"data"`
	Filename  string `json:"filename,omitempty"`
}

type HumanSendParams struct {
	RoomID   string
	HumanID  string
	ThreadID string
	ReplyTo  string
	Body     string
	Images   []MessageImage
	Files    []MessageFile
}

type AgentSendParams struct {
	RoomID     string
	AgentID    string
	Token      string
	SessionRef string
	ThreadID   string
	ReplyTo    string
	Body       string
	BasisSeq   int64
}

type SendResult struct {
	Status       SendStatus  `json:"status"`
	Message      Message     `json:"message"`
	Draft        *Draft      `json:"draft,omitempty"`
	Delta        *DraftDelta `json:"delta,omitempty"`
	WakeAgentIDs []string    `json:"wake_agent_ids,omitempty"`
}

type SendStatus string

const (
	SendCommitted SendStatus = "committed"
	SendHeld      SendStatus = "held"
)

type DraftState string

const (
	DraftHeld      DraftState = "held"
	DraftDropped   DraftState = "dropped"
	DraftCommitted DraftState = "committed"
	DraftExpired   DraftState = "expired"
)

type DraftResolution string

const (
	DraftAsIs   DraftResolution = "as_is"
	DraftSilent DraftResolution = "silent"
	DraftAnyway DraftResolution = "anyway"
)

type Draft struct {
	ID         string     `json:"id"`
	AgentID    string     `json:"agent_id"`
	SessionRef string     `json:"session_ref,omitempty"`
	RoomID     string     `json:"room_id"`
	ThreadID   string     `json:"thread_id,omitempty"`
	Body       string     `json:"body"`
	BasisSeq   int64      `json:"basis_seq"`
	HoldCount  int        `json:"hold_count"`
	State      DraftState `json:"state"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
}

type DraftDeltaItem struct {
	MessageID  string     `json:"message_id"`
	Seq        int64      `json:"seq"`
	AuthorType MemberType `json:"author_type"`
	AuthorID   string     `json:"author_id"`
	Preview    string     `json:"preview"`
}

type DraftDelta struct {
	Count int              `json:"count"`
	Items []DraftDeltaItem `json:"items"`
}

type ResolveDraftParams struct {
	AgentID    string
	Token      string
	SessionRef string
	DraftID    string
	Resolution DraftResolution
	BasisSeq   *int64
}

type DraftResult struct {
	Status  SendStatus  `json:"status,omitempty"`
	Draft   Draft       `json:"draft"`
	Message *Message    `json:"message,omitempty"`
	Delta   *DraftDelta `json:"delta,omitempty"`
}

type InboxItem struct {
	ID        string    `json:"id"`
	AgentID   string    `json:"agent_id"`
	RoomID    string    `json:"room_id"`
	MessageID string    `json:"message_id"`
	Kind      InboxKind `json:"kind"`
	CreatedAt time.Time `json:"created_at"`
	PulledAt  time.Time `json:"pulled_at,omitempty"`
}

type CheckItem struct {
	ID         string     `json:"id"`
	RoomID     string     `json:"room_id"`
	MessageID  string     `json:"message_id"`
	ThreadID   string     `json:"thread_id,omitempty"`
	AuthorType MemberType `json:"author_type"`
	AuthorID   string     `json:"author_id"`
	Kind       InboxKind  `json:"kind"`
	Preview    string     `json:"preview"`
	Seq        int64      `json:"seq"`
	CreatedAt  time.Time  `json:"created_at"`
}

type ScopeSequence struct {
	RoomID   string `json:"room_id"`
	ThreadID string `json:"thread_id,omitempty"`
	Seq      int64  `json:"seq"`
}

type CheckResult struct {
	Items         []CheckItem            `json:"items"`
	Collaboration []CollaborationMessage `json:"collaboration,omitempty"`
	Reminders     []Reminder             `json:"reminders"`
	Scopes        []ScopeSequence        `json:"scopes"`
	HasMore       bool                   `json:"has_more"`
	CheckedAt     time.Time              `json:"checked_at"`
}

type CollaborationMessage struct {
	ID                    string                     `json:"id"`
	RoomID                string                     `json:"room_id"`
	FromType              MemberType                 `json:"from_type,omitempty"`
	FromID                string                     `json:"from_id,omitempty"`
	FromSessionRef        string                     `json:"from_session_ref,omitempty"`
	ToAgentID             string                     `json:"to_agent_id,omitempty"`
	TargetSessionRef      string                     `json:"target_session_ref,omitempty"`
	WorkID                string                     `json:"work_id,omitempty"`
	RecipientNamedAgentID string                     `json:"recipient_named_agent_id,omitempty"`
	Kind                  CollaborationKind          `json:"kind,omitempty"`
	Body                  string                     `json:"body"`
	ArtifactRefs          []string                   `json:"artifact_refs,omitempty"`
	SourceMessageID       string                     `json:"source_message_id,omitempty"`
	GoalRevision          int                        `json:"goal_revision,omitempty"`
	CandidateRevision     int                        `json:"candidate_revision,omitempty"`
	ReplyTo               string                     `json:"reply_to,omitempty"`
	TargetKind            CollaborationTargetKind    `json:"target_kind"`
	TargetID              string                     `json:"target_id,omitempty"`
	Visibility            CollaborationVisibility    `json:"visibility"`
	CorrelationID         string                     `json:"correlation_id,omitempty"`
	RequestID             string                     `json:"request_id,omitempty"`
	TerminalState         CollaborationTerminalState `json:"terminal_state,omitempty"`
	CreatedAt             time.Time                  `json:"created_at"`
	ConsumedAt            time.Time                  `json:"consumed_at,omitempty"`
	InvalidatedAt         time.Time                  `json:"invalidated_at,omitempty"`
}

// CollaborationDispatch is the minimal unconsumed-delivery envelope used by
// the host scheduler. Message bodies remain private to the session that claims
// them through Check or CheckSession.
type CollaborationDispatch struct {
	ID               string                  `json:"id"`
	RoomID           string                  `json:"room_id"`
	TargetSessionRef string                  `json:"target_session_ref,omitempty"`
	WorkID           string                  `json:"work_id,omitempty"`
	Kind             CollaborationKind       `json:"kind,omitempty"`
	TargetKind       CollaborationTargetKind `json:"target_kind"`
	Visibility       CollaborationVisibility `json:"visibility"`
	CorrelationID    string                  `json:"correlation_id,omitempty"`
}

type CollaborationSendParams struct {
	AgentID          string
	Token            string
	FromSessionRef   string
	RoomID           string
	ToAgentID        string
	TargetSessionRef string
	Kind             CollaborationKind
	Body             string
	ArtifactRefs     []string
	SourceMessageID  string
	WorkID           string
	ReplyTo          string
	TargetKind       CollaborationTargetKind
	TargetID         string
	Visibility       CollaborationVisibility
	CorrelationID    string
	RequestID        string
	TerminalState    CollaborationTerminalState
}

type TaskVerification struct {
	TaskID            string               `json:"task_id"`
	RoomID            string               `json:"room_id"`
	OwnerID           string               `json:"owner_id"`
	Decision          VerificationDecision `json:"decision"`
	Report            string               `json:"report"`
	EvidenceRefs      []string             `json:"evidence_refs,omitempty"`
	RunRef            string               `json:"run_ref,omitempty"`
	Attempt           int                  `json:"attempt"`
	GoalRevision      int                  `json:"goal_revision"`
	CandidateRevision int                  `json:"candidate_revision"`
	UpdatedAt         time.Time            `json:"updated_at"`
}

type TaskVerificationSubmitParams struct {
	TaskID            string
	RoomID            string
	Decision          VerificationDecision
	Report            string
	EvidenceRefs      []string
	RunRef            string
	GoalRevision      int
	CandidateRevision int
	AgentID           string
	Token             string
}

type TaskVerificationSubmitResult struct {
	Verification TaskVerification     `json:"verification"`
	Delivery     CollaborationMessage `json:"delivery"`
}

type WakeState struct {
	AgentID     string    `json:"agent_id"`
	Outstanding bool      `json:"outstanding"`
	Pending     bool      `json:"pending"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type WakeSink interface {
	Deliver(agentID string)
}

type WakeInterruptSink interface {
	WakeSink
	Interrupt(agentID string)
}

type WakeSessionInterruptSink interface {
	WakeSink
	InterruptSession(agentID, sessionRef string)
}

// WakeRunSessionInterruptSink interrupts a hidden Work run that has a durable
// session ref but no Named Agent owner, such as an independent verifier.
type WakeRunSessionInterruptSink interface {
	WakeSink
	InterruptRunSession(sessionRef string)
}

type TelemetryEvent struct {
	Name       string     `json:"name"`
	MemberType MemberType `json:"member_type,omitempty"`
	MemberID   string     `json:"member_id,omitempty"`
	RoomID     string     `json:"room_id,omitempty"`
	ThreadID   string     `json:"thread_id,omitempty"`
	WakeCount  int        `json:"wake_count,omitempty"`
	HoldCount  int        `json:"hold_count,omitempty"`
}

type TelemetrySink interface {
	RecordChannelEvent(event TelemetryEvent)
}

type Reminder struct {
	ID        string        `json:"id"`
	AgentID   string        `json:"agent_id"`
	FireAt    time.Time     `json:"fire_at"`
	Note      string        `json:"note"`
	RoomID    string        `json:"room_id,omitempty"`
	ThreadID  string        `json:"thread_id,omitempty"`
	State     ReminderState `json:"state"`
	CreatedAt time.Time     `json:"created_at"`
}

type TaskCreateParams struct {
	SourceSessionRef     string
	RoomID               string
	ThreadID             string
	SourceMessageID      string
	Title                string
	Body                 string
	OwnerID              string
	LeadNamedAgentID     string
	TargetSessionRef     string
	VerificationRequired bool
	AgentID              string
	Token                string
	HumanID              string
}

type TaskUpdateParams struct {
	TaskID         string
	RoomID         string
	State          TaskState
	OwnerID        string
	GoalCorrection string
	SessionRef     string
	AgentID        string
	Token          string
	HumanID        string
}

type TaskListParams struct {
	RoomID  string
	OwnerID string
	AgentID string
	Token   string
	HumanID string
}

type ReminderSetParams struct {
	AgentID  string
	Token    string
	FireAt   time.Time
	Note     string
	RoomID   string
	ThreadID string
}

type ReminderListParams struct {
	AgentID string
	Token   string
	State   ReminderState
}

type ReminderCancelParams struct {
	AgentID    string
	Token      string
	ReminderID string
}

type HumanMentionItem struct {
	ID         string     `json:"id"`
	HumanID    string     `json:"human_id"`
	RoomID     string     `json:"room_id"`
	MessageID  string     `json:"message_id"`
	AuthorID   string     `json:"author_id"`
	AuthorType MemberType `json:"author_type"`
	Preview    string     `json:"preview"`
	CreatedAt  time.Time  `json:"created_at"`
}

type HumanMentionCount struct {
	RoomID      string `json:"room_id"`
	UnreadCount int    `json:"unread_count"`
}

type HumanRoomUnreadCount struct {
	RoomID      string `json:"room_id"`
	UnreadCount int    `json:"unread_count"`
}
