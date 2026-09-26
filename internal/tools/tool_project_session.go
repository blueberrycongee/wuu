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
	Action    string               `json:"action"`
	SessionID string               `json:"session_id,omitempty"`
	Title     string               `json:"title,omitempty"`
	Prompt    string               `json:"prompt,omitempty"`
	Workspace string               `json:"workspace,omitempty"`
	Candidate *ProjectCandidateRef `json:"candidate,omitempty"`
	Query     string               `json:"query,omitempty"`
	Limit     int                  `json:"limit,omitempty"`
	Before    int                  `json:"before,omitempty"`
}

// ProjectCandidateRef names one frozen candidate: a managed session's turn.
type ProjectCandidateRef struct {
	SessionID string `json:"session_id"`
	TurnID    string `json:"turn_id"`
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
	return ToolClassification{ReadOnly: request.Action == "list" || request.Action == "inspect", ConcurrencySafe: true}
}

func (t *ProjectSessionTool) Definition() providers.ToolDefinition {
	str := func(description string) map[string]any {
		return map[string]any{"type": "string", "description": description}
	}
	return providers.ToolDefinition{
		Name: projectSessionToolName,
		Description: "Start and manage the sessions that do this project's work. " +
			"list shows your sessions with their state and review candidates. " +
			"create starts a session from a self-contained brief: the goal, constraints, acceptance checks and what to leave alone; the session does not see this conversation. " +
			"It works in its own Git worktree unless workspace is shared; give it candidate to review a frozen change in a copy of that change. " +
			"send gives an existing session its next instruction or a correction, steering a running turn. " +
			"stop interrupts a running turn. inspect reads recent history without waiting. " +
			"You are told in this conversation when a session's turn ends, so end your turn instead of polling.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"action":     map[string]any{"type": "string", "enum": []string{"list", "create", "send", "stop", "inspect"}},
				"session_id": str("Managed session for send, stop and inspect."),
				"title":      str("Short name for a new session."),
				"prompt":     str("The brief for create, or the next instruction for send."),
				"workspace":  map[string]any{"type": "string", "enum": []string{"worktree", "shared"}, "description": "worktree (default in a Git workspace) isolates changes for review; shared edits the workspace directly and suits a single writer."},
				"candidate": map[string]any{"type": "object", "description": "Start the new session from this frozen candidate, for independent review.", "properties": map[string]any{
					"session_id": str("Session that produced the candidate."),
					"turn_id":    str("Turn that produced the candidate."),
				}, "required": []string{"session_id", "turn_id"}},
				"query":  str("Search text for inspect."),
				"limit":  map[string]any{"type": "integer", "minimum": 1, "maximum": 30},
				"before": map[string]any{"type": "integer", "description": "inspect records before this sequence."},
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
