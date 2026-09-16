package tools

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/blueberrycongee/wuu/internal/channels"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/session"
	"github.com/blueberrycongee/wuu/internal/toolresult"
)

type HarnessSessionTool struct{ env *Env }

func NewHarnessSessionTool(env *Env) *HarnessSessionTool { return &HarnessSessionTool{env: env} }
func (t *HarnessSessionTool) Name() string               { return "session" }
func (t *HarnessSessionTool) IsReadOnly() bool           { return false }
func (t *HarnessSessionTool) IsConcurrencySafe() bool    { return true }
func (t *HarnessSessionTool) Classify(raw string) ToolClassification {
	var p channels.HarnessSessionParams
	_ = json.Unmarshal([]byte(raw), &p)
	return ToolClassification{ReadOnly: p.Action == "list" || p.Action == "inspect", ConcurrencySafe: true}
}
func (t *HarnessSessionTool) Definition() providers.ToolDefinition {
	return providers.ToolDefinition{Name: t.Name(), Description: "Manage ordinary Harness sessions the user can open and continue. One named identity can manage several execution sessions. Create for a distinct objective or fresh context; reuse for corrections and follow-ups toward the same result. Sharing a project does not require sharing a session. list finds existing work; create starts a visible session in an explicit project and follows its results; inspect reads status and bounded evidence without waiting; send continues the same context (queue by default, steer for a current correction); stop pauses execution and automatic follow-up; manage attaches, releases, or explicitly resumes follow-up without starting a turn. Sending alone does not attach management. Managed sessions notify you when a turn ends: assess the evidence and send the next useful instruction, or deliver the verified result. Release your turn while they work; do not poll or wait. Models inherit your selection unless specified.", InputSchema: map[string]any{"type": "object", "properties": map[string]any{
		"action":         map[string]any{"type": "string", "enum": []string{"list", "create", "inspect", "send", "stop", "manage"}},
		"work_id":        map[string]any{"type": "string", "description": "Existing chat_task/chat_work ID when executing a recorded responsibility. Include it on create/manage to bind its budget, goal revision and cancellation; omit for work without a task record."},
		"session_id":     map[string]any{"type": "string", "description": "Existing Harness session ID from list or create."},
		"workspace_root": map[string]any{"type": "string", "description": "Absolute project path from the user's task and registered workspaces. Required for create unless workspace_id is supplied. Never infer it from your identity home, the active UI, or a similar session title. Clarify if the project is ambiguous. Existing sessions keep their binding; inspect it before reusing them."},
		"workspace_id":   map[string]any{"type": "string", "description": "Registered project ID; may replace workspace_root. When both are supplied they must agree. The host executes with this project's runtime and configuration; listing a project does not authorize unrelated work in it."},
		"workspace":      map[string]any{"type": "string", "enum": []string{"shared", "worktree"}, "description": "Create in the project directly or an isolated Git worktree. Default shared; keep concurrent write scopes separate."},
		"title":          map[string]any{"type": "string"},
		"prompt":         map[string]any{"type": "string", "description": "A concise goal and relevant constraints for create; the next instruction or correction for send; the responsibility to track for manage. Carry authorization and expected evidence, then refine through follow-ups as facts arrive."},
		"mode":           map[string]any{"type": "string", "enum": []string{"queue", "steer", "attach", "release", "resume"}, "description": "send: queue or steer. manage: attach (default), release, or resume only after an explicit user request to return control/continue."},
		"query":          map[string]any{"type": "string", "description": "Optional title/workspace search for list, transcript search for inspect."},
		"limit":          map[string]any{"type": "integer", "minimum": 1, "maximum": 30},
		"before":         map[string]any{"type": "integer", "description": "Inspect earlier transcript records before this sequence."},
		"provider":       map[string]any{"type": "string"}, "model": map[string]any{"type": "string"}, "effort": map[string]any{"type": "string"},
	}, "required": []string{"action"}}}
}
func (t *HarnessSessionTool) Execute(ctx context.Context, raw string) (string, error) {
	r, err := t.ExecuteResultCall(ctx, providers.ToolCall{ID: session.NewID(), Name: t.Name(), Arguments: raw})
	return r.TextProjection(), err
}
func (t *HarnessSessionTool) ExecuteResultCall(ctx context.Context, call providers.ToolCall) (toolresult.Result, error) {
	if t == nil || t.env == nil || t.env.ChatAgent == nil {
		return toolresult.Result{}, errors.New("session requires a collaboration identity")
	}
	var p channels.HarnessSessionParams
	if err := json.Unmarshal([]byte(call.Arguments), &p); err != nil {
		return toolresult.Result{}, err
	}
	p.OperationID = call.ID
	result, err := t.env.ChatAgent.HarnessSession(ctx, p)
	if err != nil {
		return toolresult.Result{}, err
	}
	data, err := json.Marshal(result)
	return toolresult.FromText(string(data)), err
}
