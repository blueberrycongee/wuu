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
	Work       *ProjectWorkRequest `json:"work,omitempty"`
	Role       string              `json:"role,omitempty"`
	ModelAlias string              `json:"model_alias,omitempty"`
	Wake       bool                `json:"wake,omitempty"`
	Action     string              `json:"action"`
	SessionID  string              `json:"session_id,omitempty"`
	Title      string              `json:"title,omitempty"`
	Prompt     string              `json:"prompt,omitempty"`
	Workspace  string              `json:"workspace,omitempty"`
	Query      string              `json:"query,omitempty"`
	Limit      int                 `json:"limit,omitempty"`
	Before     int                 `json:"before,omitempty"`
	Block      *bool               `json:"block,omitempty"`
	TurnID     string              `json:"turn_id,omitempty"`
	TimeoutMS  int                 `json:"timeout_ms,omitempty"`
}

// ProjectWorkRequest addresses a durable outcome and its requirement revision.
// Only the coordinator changes the user contract; the technical lead owns
// execution and review. Evidence and delivery are recorded separately.
type ProjectWorkRequest struct {
	Operation  string `json:"operation"`
	WorkID     string `json:"work_id,omitempty"`
	Revision   int    `json:"revision,omitempty"`
	Title      string `json:"title,omitempty"`
	Brief      string `json:"brief,omitempty"`
	Acceptance string `json:"acceptance,omitempty"`
	Authority  string `json:"authority,omitempty"`
	SourceRefs string `json:"source_refs,omitempty"`
	ModelAlias string `json:"model_alias,omitempty"`
	Summary    string `json:"summary,omitempty"`
	Evidence   string `json:"evidence,omitempty"`
	CodeRef    string `json:"code_ref,omitempty"`
	Delivery   string `json:"delivery,omitempty"`
}

func projectWorkSchema() map[string]any {
	p := map[string]any{}
	for name, description := range map[string]string{
		"work_id": "Persistent work identity returned by create/list.",
		"title":   "Short outcome name.", "brief": "create/update: full current user requirements. execute: one coherent technical phase, scope, interfaces, checks, reusable results/processes and escalation conditions.",
		"acceptance": "User-visible acceptance criteria; required for create/update.", "authority": "Actual user authorization and limits; delegation cannot expand it. Required for create/update.",
		"source_refs": "Original user message references and relevant dependencies/decisions.", "model_alias": "Optional configured model override for a new technical lead or executor.",
		"summary": "Concise outcome or actionable blocker.", "evidence": "Exact verification/review commands, outcomes and accessible artifact references; required for submit/review.",
		"code_ref": "Exact code/content version reviewed; review must match the submitted version.", "delivery": "Actual destination and what was delivered; only record after authorized delivery occurred.",
	} {
		p[name] = map[string]any{"type": "string", "description": description}
	}
	p["operation"] = map[string]any{"type": "string", "enum": []string{"list", "get", "create", "update", "execute", "submit", "review", "dispatch_delivery", "complete_delivery", "deliver", "block", "stop", "resume"}}
	p["revision"] = map[string]any{"type": "integer", "minimum": 1, "description": "Required current requirement revision for every mutation after create. Read the work again after a conflict."}
	return map[string]any{"type": "object", "properties": p, "required": []string{"operation"}}
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
	readOnly := request.Action == "list" || request.Action == "inspect" || request.Action == "wait"
	if request.Action == "work" && request.Work != nil {
		readOnly = request.Work.Operation == "list" || request.Work.Operation == "get"
	}
	return ToolClassification{ReadOnly: readOnly, ConcurrencySafe: true}
}

func (t *ProjectSessionTool) Definition() providers.ToolDefinition {
	str := func(description string) map[string]any {
		return map[string]any{"type": "string", "description": description}
	}
	return providers.ToolDefinition{
		DirectOnly: true,
		Name:       projectSessionToolName,
		Description: "Start and manage the sessions that do this project's work. " +
			"Use action work for durable outcomes: the coordinator creates or updates the complete user contract, then ends its turn. Each work reuses a technical lead and an optional persistent executor in one isolated workspace. The technical lead uses execute for a bounded phase, the executor submits evidence, and only the technical lead reviews the exact result. After acceptance the coordinator can dispatch_delivery to the technical lead, which records complete_delivery after the authorized action succeeds; deliver directly records an already-delivered local result. get/list expose shared facts, dispatch receipts, blockers and token usage. stop interrupts the entire work; resume requires explicit new user intent. Coordinator dispatch is always asynchronous and cannot wait. Work results go to the technical lead first; only acceptance and actionable blockers wake the coordinator. " +
			"list shows your sessions with their state. " +
			"create starts a session from a self-contained brief: the goal, constraints, acceptance checks and what to leave alone; the session does not see this conversation. " +
			"Workers default to their own Git worktree when available. side creates or resumes the persistent Side Agent in the shared workspace, steering it when running. " +
			"send gives an existing session its next instruction or a correction, steering a running turn. " +
			"Legacy session dispatches are durable and replay-safe. Coordinator dispatches always return immediately; background callers can set block true. wait returns the selected turn status and final output; timeout or caller cancellation leaves the child running. stop interrupts and revokes earlier queued dispatches. inspect reads recent history without waiting. " +
			"For legacy sessions only the coordinator uses send/stop. Work-bound sessions use versioned work operations instead. Work participants message their technical lead; use work review/block for coordinator reports. Preserve user authorization boundaries. Messages are durable and attributed to the sender. " +
			"Legacy sessions report to the coordinator; work executors report only to their technical lead. End your turn instead of polling or sending acknowledgements that add no information.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"action":      map[string]any{"type": "string", "enum": []string{"work", "list", "create", "side", "send", "wait", "message", "stop", "inspect"}},
				"work":        projectWorkSchema(),
				"block":       map[string]any{"type": "boolean", "description": "Wait for this dispatch. Defaults false. Ignored for the coordinator, which cannot block."},
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
