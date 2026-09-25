package tools

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/blueberrycongee/wuu/internal/channels"
	"github.com/blueberrycongee/wuu/internal/providers"
)

type ChatWorkTool struct{ env *Env }

func NewChatWorkTool(env *Env) *ChatWorkTool    { return &ChatWorkTool{env} }
func (t *ChatWorkTool) Name() string            { return "chat_work" }
func (t *ChatWorkTool) IsReadOnly() bool        { return false }
func (t *ChatWorkTool) IsConcurrencySafe() bool { return false }
func (t *ChatWorkTool) Definition() providers.ToolDefinition {
	str := map[string]any{"type": "string"}
	return providers.ToolDefinition{Name: t.Name(), Description: "Inspect Work, record check evidence or cancel it. The host owns execution runs, candidate snapshots and independent verification. Use session create with work_id to delegate implementation.", InputSchema: map[string]any{"type": "object", "properties": map[string]any{
		"action": map[string]any{"type": "string", "enum": []string{"get", "list", "evidence", "cancel"}}, "work_id": str, "room_id": str, "checks_summary": str, "unresolved_items": str, "changed_files_count": map[string]any{"type": "integer", "minimum": 0}, "reason": str,
	}, "required": []string{"action"}}}
}
func (t *ChatWorkTool) Execute(ctx context.Context, raw string) (string, error) {
	if t.env == nil || t.env.ChatAgent == nil {
		return "", errors.New("chat_work requires a collaboration identity")
	}
	var p struct {
		Action            string `json:"action"`
		WorkID            string `json:"work_id"`
		RoomID            string `json:"room_id"`
		ChecksSummary     string `json:"checks_summary"`
		UnresolvedItems   string `json:"unresolved_items"`
		ChangedFilesCount int    `json:"changed_files_count"`
		Reason            string `json:"reason"`
	}
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		return "", err
	}
	c := t.env.ChatAgent
	var work channels.Work
	var err error
	switch p.Action {
	case "get":
		work, err = c.GetWork(ctx, p.WorkID)
	case "list":
		works, err := c.ListWorks(ctx, p.RoomID)
		if err != nil {
			return "", err
		}
		return mustJSON(map[string]any{"works": works})
	case "evidence":
		work, err = c.UpdateWorkEvidence(ctx, channels.WorkEvidenceUpdateParams{WorkID: p.WorkID, ChecksSummary: p.ChecksSummary, UnresolvedItems: p.UnresolvedItems, ChangedFilesCount: p.ChangedFilesCount})
	case "cancel":
		work, err = c.CancelWork(ctx, p.WorkID, p.Reason)
	default:
		return "", errors.New("chat_work action must be get, list, evidence or cancel")
	}
	if err != nil {
		return "", err
	}
	return mustJSON(map[string]any{"work": work})
}

// WorkGetTool exposes the delegated work without granting lifecycle mutations.
type WorkGetTool struct{ env *Env }

func NewWorkGetTool(env *Env) *WorkGetTool     { return &WorkGetTool{env} }
func (t *WorkGetTool) Name() string            { return "work_get" }
func (t *WorkGetTool) IsReadOnly() bool        { return true }
func (t *WorkGetTool) IsConcurrencySafe() bool { return true }
func (t *WorkGetTool) Definition() providers.ToolDefinition {
	return providers.ToolDefinition{Name: t.Name(), Description: "Read the current delegated Work, its goal revision, candidate and evidence.", InputSchema: map[string]any{"type": "object", "properties": map[string]any{}, "additionalProperties": false}}
}
func (t *WorkGetTool) Execute(ctx context.Context, raw string) (string, error) {
	if t.env.ChatAgent == nil || t.env.CollaborationWorkID == "" {
		return "", errors.New("this session has no delegated Work")
	}
	work, err := t.env.ChatAgent.GetWork(ctx, t.env.CollaborationWorkID)
	if err != nil {
		return "", err
	}
	// Private coordinator deliveries and held drafts are not verifier evidence.
	work.Deliveries = nil
	work.PendingDeliveryRefs = nil
	return mustJSON(map[string]any{"work": work})
}
