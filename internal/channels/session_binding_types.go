package channels

import "time"

type CollaborationSessionPurpose string

const (
	CollaborationSessionConversation CollaborationSessionPurpose = "conversation"
	CollaborationSessionCoordination CollaborationSessionPurpose = "coordination"
	CollaborationSessionWork         CollaborationSessionPurpose = "work"
	CollaborationSessionVerification CollaborationSessionPurpose = "verification"
)

type CollaborationSessionState string

const (
	CollaborationSessionWaiting     CollaborationSessionState = "waiting"
	CollaborationSessionQueued      CollaborationSessionState = "queued"
	CollaborationSessionIdle        CollaborationSessionState = "idle"
	CollaborationSessionStarting    CollaborationSessionState = "starting"
	CollaborationSessionRunning     CollaborationSessionState = "running"
	CollaborationSessionInterrupted CollaborationSessionState = "interrupted"
	CollaborationSessionMissing     CollaborationSessionState = "missing"
	CollaborationSessionCompleted   CollaborationSessionState = "completed"
	CollaborationSessionCancelled   CollaborationSessionState = "cancelled"
	CollaborationSessionFailed      CollaborationSessionState = "failed"
)

// CollaborationSessionBinding is the durable execution identity behind a
// collaboration principal. Each named identity has one primary conversation; older bindings remain history.
type CollaborationSessionBinding struct {
	Primary          bool                        `json:"primary,omitempty"`
	TurnID           string                      `json:"turn_id,omitempty"`
	SessionRef       string                      `json:"session_ref"`
	Title            string                      `json:"title,omitempty"`
	Objective        string                      `json:"objective,omitempty"`
	ParentSessionRef string                      `json:"parent_session_ref,omitempty"`
	Provider         string                      `json:"provider,omitempty"`
	Model            string                      `json:"model,omitempty"`
	Effort           string                      `json:"effort,omitempty"`
	RuntimeVersion   string                      `json:"runtime_version,omitempty"`
	FailureReason    string                      `json:"failure_reason,omitempty"`
	PrincipalID      string                      `json:"principal_id"`
	NamedAgentID     string                      `json:"named_agent_id,omitempty"`
	RoomID           string                      `json:"room_id,omitempty"`
	WorkID           string                      `json:"work_id,omitempty"`
	RunID            string                      `json:"run_id,omitempty"`
	Purpose          CollaborationSessionPurpose `json:"purpose"`
	State            CollaborationSessionState   `json:"state"`
	CreatedAt        time.Time                   `json:"created_at"`
	UpdatedAt        time.Time                   `json:"updated_at"`
}

type CollaborationSessionBindParams struct {
	Title            string
	Objective        string
	ParentSessionRef string
	Provider         string
	Model            string
	Effort           string
	RuntimeVersion   string
	FailureReason    string
	SessionRef       string
	PrincipalID      string
	RoomID           string
	WorkID           string
	RunID            string
	Purpose          CollaborationSessionPurpose
	State            CollaborationSessionState
	AgentID          string
	Token            string
}

type CollaborationSessionListParams struct {
	PrincipalID string
	RoomID      string
	AgentID     string
	Token       string
}

type CollaborationSessionStateParams struct {
	TurnID        string
	FailureReason string
	SessionRef    string
	State         CollaborationSessionState
	AgentID       string
	Token         string
}
