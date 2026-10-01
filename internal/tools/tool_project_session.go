package tools

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/session"
	"github.com/blueberrycongee/wuu/internal/toolresult"
)

const projectSessionToolName = "session"

// ProjectSessionRequest is one project coordinator operation on its managed
// sessions. The host resolves the project from the calling conversation.
type ProjectSessionRequest struct {
	Role       string `json:"role,omitempty"`
	ModelAlias string `json:"model_alias,omitempty"`
	Wake       bool   `json:"wake,omitempty"`
	Action     string `json:"action"`
	SessionID  string `json:"session_id,omitempty"`
	Title      string `json:"title,omitempty"`
	Prompt     string `json:"prompt,omitempty"`
	Workspace  string `json:"workspace,omitempty"`
	Query      string `json:"query,omitempty"`
	Limit      int    `json:"limit,omitempty"`
	Before     int    `json:"before,omitempty"`
	Block      *bool  `json:"block,omitempty"`
	TurnID     string `json:"turn_id,omitempty"`
	TimeoutMS  int    `json:"timeout_ms,omitempty"`
}

// ProjectSessionHandler serves a coordinator's session tool calls. callID is
// stable across replays of the same tool call and keys idempotent creation.
type ProjectSessionHandler func(ctx context.Context, callID string, request ProjectSessionRequest) (any, error)

type ProjectSessionTool struct{ env *Env }

func NewProjectSessionTool(env *Env) *ProjectSessionTool { return &ProjectSessionTool{env: env} }
func (t *ProjectSessionTool) Name() string               { return projectSessionToolName }
func (t *ProjectSessionTool) IsReadOnly() bool           { return false }
func (t *ProjectSessionTool) IsConcurrencySafe() bool    { return true }

func (t *ProjectSessionTool) Classify(raw string) ToolClassification {
	var request ProjectSessionRequest
	_ = json.Unmarshal([]byte(raw), &request)
	return ToolClassification{ReadOnly: request.Action == "list" || request.Action == "inspect" || request.Action == "wait", ConcurrencySafe: true}
}

func (t *ProjectSessionTool) Definition() providers.ToolDefinition {
	str := func(description string) map[string]any {
		return map[string]any{"type": "string", "description": description}
	}
	return providers.ToolDefinition{
		DirectOnly: true,
		Name:       projectSessionToolName,
		Description: "Start and manage the sessions that do this project's work. " +
			"list shows your sessions with their state. " +
			"create starts a session from a self-contained brief: the goal, constraints, acceptance checks and what to leave alone; the session does not see this conversation. " +
			"Workers default to their own Git worktree when available. side creates or resumes the persistent Side Agent in the shared workspace, steering it when running. " +
			"send gives an existing session its next instruction or a correction, steering a running turn. " +
			"Dispatches are durable and replay-safe. side waits by default; create and send return immediately unless block is true. wait returns the selected turn status and final output; timeout or caller cancellation leaves the child running. stop interrupts and revokes earlier queued dispatches. inspect reads recent history without waiting. " +
			"Only the lead uses send/stop. Any active team member may message another member or the lead directly; preserve user authorization boundaries and copy consequential decisions to the lead. Messages are durable and attributed to the sender. " +
			"The lead receives final reports. End your turn instead of polling or sending acknowledgements that add no information.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"action":      map[string]any{"type": "string", "enum": []string{"list", "create", "side", "send", "wait", "message", "stop", "inspect"}},
				"block":       map[string]any{"type": "boolean", "description": "Wait for this dispatch. Defaults true for side, false for create/send."},
				"turn_id":     str("wait: exact turn to observe; omit for the current or latest turn."),
				"timeout_ms":  map[string]any{"type": "integer", "minimum": 1, "description": "Maximum time to wait; the host bounds it. Expiry does not stop the session."},
				"role":        map[string]any{"type": "string", "enum": []string{"side", "worker"}, "description": "create: worker by default. Only the lead can create the project's persistent side; repeated side creation returns the existing session without sending the prompt. Lead and side can create workers; workers cannot create sessions."},
				"model_alias": str("create/new side: optional configured model alias overriding the user's role default. Without a role default, inherit the lead model. Existing sessions keep their saved model."),
				"wake":        map[string]any{"type": "boolean", "description": "message: true only when a reply or action is needed now. Default false stores information for the recipient's next turn."},
				"session_id":  str("Target session for send, wait, message, stop and inspect; message can also address the project lead."),
				"title":       str("Short name for a new session."),
				"prompt":      str("The brief for create/side, or the next instruction for send/message."),
				"workspace":   map[string]any{"type": "string", "enum": []string{"worktree", "shared"}, "description": "New workers default to worktree in a Git workspace; new sides default to shared. Worktree isolates files on the session's own branch; shared requires coordinating a single writer per scope. Existing sessions keep their workspace."},
				"query":       str("Search text for inspect."),
				"limit":       map[string]any{"type": "integer", "minimum": 1, "maximum": 30},
				"before":      map[string]any{"type": "integer", "description": "inspect records before this sequence."},
			},
			"required": []string{"action"},
		},
	}
}

func (t *ProjectSessionTool) Execute(ctx context.Context, raw string) (string, error) {
	result, err := t.ExecuteResultCall(ctx, providers.ToolCall{ID: session.NewID(), Name: t.Name(), Arguments: raw})
	return result.TextProjection(), err
}

func (t *ProjectSessionTool) ExecuteResultCall(ctx context.Context, call providers.ToolCall) (toolresult.Result, error) {
	if t.env == nil || t.env.ProjectSessions == nil {
		return toolresult.Result{}, errors.New("session is available only in a project conversation")
	}
	var request ProjectSessionRequest
	if err := json.Unmarshal([]byte(call.Arguments), &request); err != nil {
		return toolresult.Result{}, err
	}
	result, err := t.env.ProjectSessions(ctx, call.ID, request)
	if err != nil {
		return toolresult.Result{}, err
	}
	data, err := json.Marshal(result)
	return toolresult.FromText(string(data)), err
}
