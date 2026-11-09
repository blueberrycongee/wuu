package activity

import "time"

type Kind string

const (
	KindBrowser Kind = "browser"
	KindCUA     Kind = "cua"
)

type State string

const (
	StateStarting             State = "starting"
	StateActive               State = "active"
	StateBackgroundControlled State = "background_controlled"
	StateForegroundControlled State = "foreground_controlled"
	StateUserControlled       State = "user_controlled"
	StateWaitingConfirmation  State = "waiting_confirmation"
	StateStopped              State = "stopped"
	StateError                State = "error"
)

type Controller string

const (
	ControllerAgent Controller = "agent"
	ControllerUser  Controller = "user"
	ControllerNone  Controller = "none"
)

type EventType string

const (
	EventStarted        EventType = "started"
	EventUpdated        EventType = "updated"
	EventControlChanged EventType = "control_changed"
	EventStopped        EventType = "stopped"
)

type Event struct {
	Type     EventType
	Activity Session
}

type Session struct {
	ID string `json:"id"`
	// ExecutionID identifies the admitted turn; absent for standalone callers.
	ExecutionID  string       `json:"execution_id,omitempty"`
	GenerationID string       `json:"-"`
	ConnectionID string       `json:"-"`
	Kind         Kind         `json:"kind"`
	ThreadID     string       `json:"thread_id"`
	Workdir      string       `json:"workdir"`
	PluginID     string       `json:"plugin_id,omitempty"`
	Target       string       `json:"target,omitempty"`
	ProcessID    int          `json:"process_id,omitempty"`
	WindowID     uint32       `json:"window_id,omitempty"`
	State        State        `json:"state"`
	Controller   Controller   `json:"controller"`
	Preview      string       `json:"preview,omitempty"`
	Error        string       `json:"error,omitempty"`
	Interaction  *Interaction `json:"interaction,omitempty"`
	CreatedAt    time.Time    `json:"created_at"`
	UpdatedAt    time.Time    `json:"updated_at"`
}

type Interaction struct {
	Kind      string   `json:"kind"`
	X         float64  `json:"x"`
	Y         float64  `json:"y"`
	ToX       *float64 `json:"to_x,omitempty"`
	ToY       *float64 `json:"to_y,omitempty"`
	Direction string   `json:"direction,omitempty"`
	Revision  int64    `json:"revision"`
}

type Lease struct {
	ActivityID string `json:"activity_id"`
	ThreadID   string `json:"thread_id"`
	Token      string `json:"token"`
}

type StartOptions struct {
	ID           string
	Kind         Kind
	ThreadID     string
	Workdir      string
	PluginID     string
	GenerationID string
	ConnectionID string
	Target       string
	ProcessID    int
	WindowID     uint32
	Preview      string
}

type UpdateOptions struct {
	State               State
	Target              string
	ProcessID           int
	WindowID            uint32
	ClearWindowIdentity bool
	Preview             string
	Error               string
	ClearError          bool
	Interaction         *Interaction
}
