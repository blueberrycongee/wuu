package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/blueberrycongee/wuu/internal/channels"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/toolresult"
)

type ChatCheckTool struct{ env *Env }

func NewChatCheckTool(env *Env) *ChatCheckTool   { return &ChatCheckTool{env: env} }
func (t *ChatCheckTool) Name() string            { return "chat_check" }
func (t *ChatCheckTool) IsReadOnly() bool        { return false }
func (t *ChatCheckTool) IsConcurrencySafe() bool { return false }
func (t *ChatCheckTool) Definition() providers.ToolDefinition {
	return providers.ToolDefinition{
		Name: "chat_check",
		Description: "Check this agent's chat and private collaboration inboxes. Returns compact unread indexes, full private collaboration messages, and current room/thread sequences. " +
			"This advances chat cursors, marks returned items pulled, and clears the current wake; call chat_read for shared message bodies.",
		InputSchema: map[string]any{"type": "object", "properties": map[string]any{}},
	}
}
func (t *ChatCheckTool) Execute(ctx context.Context, _ string) (string, error) {
	if t == nil || t.env == nil || t.env.ChatAgent == nil {
		return "", errors.New("chat_check is available only in a named-agent session")
	}
	result, err := t.env.ChatAgent.Check(ctx)
	if err != nil {
		return "", err
	}
	return mustJSON(result)
}

type ChatReadTool struct{ env *Env }

func NewChatReadTool(env *Env) *ChatReadTool    { return &ChatReadTool{env: env} }
func (t *ChatReadTool) Name() string            { return "chat_read" }
func (t *ChatReadTool) IsReadOnly() bool        { return true }
func (t *ChatReadTool) IsConcurrencySafe() bool { return true }
func (t *ChatReadTool) Definition() providers.ToolDefinition {
	return providers.ToolDefinition{
		Name:        "chat_read",
		Description: "Read group-chat message bodies either by chat_check item IDs or by a member room's sequence range. Choose exactly one mode.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"item_ids":  map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "minItems": 1, "maxItems": 50},
				"room_id":   map[string]any{"type": "string"},
				"after_seq": map[string]any{"type": "integer", "minimum": 0},
				"limit":     map[string]any{"type": "integer", "minimum": 1, "maximum": 500},
			},
		},
	}
}
func (t *ChatReadTool) Execute(ctx context.Context, argsJSON string) (string, error) {
	if t == nil || t.env == nil || t.env.ChatAgent == nil {
		return "", errors.New("chat_read is available only in a named-agent session")
	}
	var args struct {
		ItemIDs  []string `json:"item_ids"`
		RoomID   string   `json:"room_id"`
		AfterSeq int64    `json:"after_seq"`
		Limit    int      `json:"limit"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", err
	}
	itemMode := len(args.ItemIDs) > 0
	roomMode := strings.TrimSpace(args.RoomID) != ""
	if itemMode == roomMode {
		return "", errors.New("chat_read requires exactly one of item_ids or room_id")
	}
	var (
		messages []channels.Message
		err      error
	)
	if itemMode {
		messages, err = t.env.ChatAgent.ReadInbox(ctx, args.ItemIDs)
	} else {
		messages, err = t.env.ChatAgent.ReadRoom(ctx, args.RoomID, args.AfterSeq, args.Limit)
	}
	if err != nil {
		return "", err
	}
	return mustJSON(map[string]any{"messages": messages})
}

func (t *ChatReadTool) ExecuteResult(ctx context.Context, argsJSON string) (toolresult.Result, error) {
	text, err := t.Execute(ctx, argsJSON)
	if err != nil {
		return toolresult.Result{}, err
	}
	var payload struct {
		Messages []channels.Message `json:"messages"`
	}
	if err := json.Unmarshal([]byte(text), &payload); err != nil {
		return toolresult.Result{}, err
	}
	parts := make([]toolresult.ContentPart, 0, 1)
	omittedImages := 0
	imageInputSupported := t.env.ImageInputSupported == nil || *t.env.ImageInputSupported
	for messageIndex := range payload.Messages {
		message := &payload.Messages[messageIndex]
		for imageIndex := range message.Images {
			image := &message.Images[imageIndex]
			if imageInputSupported {
				parts = append(parts, toolresult.ContentPart{
					Type: toolresult.ContentTypeImage, Data: image.Data, MIMEType: image.MediaType,
					Name: message.ID + "-image",
				})
			} else {
				omittedImages++
			}
			image.Data = ""
		}
		if !imageInputSupported {
			message.Images = nil
		}
		for fileIndex := range message.Files {
			file := &message.Files[fileIndex]
			parts = append(parts, toolresult.ContentPart{
				Type: toolresult.ContentTypeFile, Data: file.Data, MIMEType: file.MediaType,
				Name: file.Filename,
			})
			file.Data = ""
		}
	}
	projection, err := mustJSON(payload)
	if err != nil {
		return toolresult.Result{}, err
	}
	parts = append([]toolresult.ContentPart{{Type: toolresult.ContentTypeText, Text: projection}}, parts...)
	if omittedImages > 0 {
		label := "image"
		if omittedImages != 1 {
			label = "images"
		}
		parts = append(parts, toolresult.ContentPart{
			Type: toolresult.ContentTypeText,
			Text: fmt.Sprintf("[%d %s omitted: unsupported]", omittedImages, label),
		})
	}
	return toolresult.Result{Content: parts}, nil
}

type ChatSendTool struct{ env *Env }

func NewChatSendTool(env *Env) *ChatSendTool    { return &ChatSendTool{env: env} }
func (t *ChatSendTool) Name() string            { return "chat_send" }
func (t *ChatSendTool) IsReadOnly() bool        { return false }
func (t *ChatSendTool) IsConcurrencySafe() bool { return false }
func (t *ChatSendTool) Definition() providers.ToolDefinition {
	return providers.ToolDefinition{
		Name: "chat_send",
		Description: "Send one short text message as this named agent to the room's public timeline. basis_seq is required and records the room version used to compose the message. " +
			"A stale basis is preserved as a held draft instead of being posted.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"room_id":   map[string]any{"type": "string"},
				"kind":      map[string]any{"type": "string", "enum": []string{"text"}},
				"body":      map[string]any{"type": "string", "maxLength": channels.MaxMessageRunes},
				"basis_seq": map[string]any{"type": "integer", "minimum": 0},
			},
			"required": []string{"room_id", "kind", "body", "basis_seq"},
		},
	}
}
func (t *ChatSendTool) Execute(ctx context.Context, argsJSON string) (string, error) {
	if t == nil || t.env == nil || t.env.ChatAgent == nil {
		return "", errors.New("chat_send is available only in a named-agent session")
	}
	var args struct {
		RoomID   string `json:"room_id"`
		Kind     string `json:"kind"`
		Body     string `json:"body"`
		BasisSeq *int64 `json:"basis_seq"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", err
	}
	if strings.TrimSpace(args.Kind) != string(channels.MessageText) {
		return "", errors.New("chat_send kind must be text")
	}
	if args.BasisSeq == nil {
		return "", errors.New("chat_send basis_seq is required")
	}
	result, err := t.env.ChatAgent.Send(ctx, channels.AgentSendParams{
		RoomID: args.RoomID, Body: args.Body, BasisSeq: *args.BasisSeq,
	})
	if err != nil {
		return "", err
	}
	if result.Status == channels.SendHeld {
		return mustJSON(map[string]any{"status": result.Status, "draft": result.Draft, "delta": result.Delta})
	}
	return mustJSON(map[string]any{"status": result.Status, "message": result.Message})
}

type CollaborationSendTool struct{ env *Env }

func NewCollaborationSendTool(env *Env) *CollaborationSendTool {
	return &CollaborationSendTool{env: env}
}
func (t *CollaborationSendTool) Name() string            { return "collaboration_send" }
func (t *CollaborationSendTool) IsReadOnly() bool        { return false }
func (t *CollaborationSendTool) IsConcurrencySafe() bool { return false }
func (t *CollaborationSendTool) Definition() providers.ToolDefinition {
	return providers.ToolDefinition{
		Name:        "collaboration_send",
		Description: "Send one versioned Collaboration envelope to a room, named agent, or exact session. Room-visible sends require basis_seq; private requests should use stable request_id and correlation_id.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"room_id":            map[string]any{"type": "string"},
				"to_agent_id":        map[string]any{"type": "string"},
				"target_kind":        map[string]any{"type": "string", "enum": []string{"room", "named_agent", "session"}},
				"target_id":          map[string]any{"type": "string"},
				"visibility":         map[string]any{"type": "string", "enum": []string{"room", "private", "work_private", "system"}},
				"correlation_id":     map[string]any{"type": "string"},
				"request_id":         map[string]any{"type": "string"},
				"terminal_state":     map[string]any{"type": "string", "enum": []string{"completed", "failed", "interrupted", "cancelled", "timed_out", "undeliverable"}},
				"basis_seq":          map[string]any{"type": "integer", "minimum": 0},
				"target_session_ref": map[string]any{"type": "string", "description": "Deliver only to this session of the target named agent."},
				"work_id":            map[string]any{"type": "string", "description": "Scope this private message to one durable work item."},
				"kind":               map[string]any{"type": "string", "enum": []string{"control", "candidate_ready", "peer_result"}},
				"source_message_id":  map[string]any{"type": "string"},
				"artifact_refs":      map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				"body":               map[string]any{"type": "string", "maxLength": channels.MaxMessageRunes},
				"reply_to":           map[string]any{"type": "string"},
			},
			"required": []string{"room_id", "body"},
		},
	}
}
func (t *CollaborationSendTool) Execute(ctx context.Context, argsJSON string) (string, error) {
	if t == nil || t.env == nil || t.env.ChatAgent == nil {
		return "", errors.New("collaboration_send is available only in a named-agent session")
	}
	var args struct {
		RoomID           string   `json:"room_id"`
		ToAgent          string   `json:"to_agent_id"`
		TargetSessionRef string   `json:"target_session_ref"`
		WorkID           string   `json:"work_id"`
		Kind             string   `json:"kind"`
		SourceMessageID  string   `json:"source_message_id"`
		ArtifactRefs     []string `json:"artifact_refs"`
		Body             string   `json:"body"`
		ReplyTo          string   `json:"reply_to"`
		TargetKind       string   `json:"target_kind"`
		TargetID         string   `json:"target_id"`
		Visibility       string   `json:"visibility"`
		CorrelationID    string   `json:"correlation_id"`
		RequestID        string   `json:"request_id"`
		TerminalState    string   `json:"terminal_state"`
		BasisSeq         int64    `json:"basis_seq"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", err
	}
	switch channels.CollaborationTargetKind(args.TargetKind) {
	case "", channels.CollaborationTargetRoom, channels.CollaborationTargetNamedAgent, channels.CollaborationTargetSession:
	default:
		return "", errors.New("collaboration_send target_kind must be room, named_agent, or session")
	}
	if channels.CollaborationTargetKind(args.TargetKind) == channels.CollaborationTargetRoom {
		if args.Visibility != "" && channels.CollaborationVisibility(args.Visibility) != channels.CollaborationVisibilityRoom {
			return "", errors.New("room target requires room visibility")
		}
		result, err := t.env.ChatAgent.Send(ctx, channels.AgentSendParams{RoomID: args.RoomID, ReplyTo: args.ReplyTo, Body: args.Body, BasisSeq: args.BasisSeq})
		if err != nil {
			return "", err
		}
		return mustJSON(result)
	}
	message, err := t.env.ChatAgent.SendCollaboration(ctx, channels.CollaborationSendParams{
		RoomID: args.RoomID, ToAgentID: args.ToAgent, TargetSessionRef: args.TargetSessionRef,
		WorkID: args.WorkID, Kind: channels.CollaborationKind(args.Kind),
		SourceMessageID: args.SourceMessageID, ArtifactRefs: args.ArtifactRefs,
		Body: args.Body, ReplyTo: args.ReplyTo, TargetKind: channels.CollaborationTargetKind(args.TargetKind),
		TargetID: args.TargetID, Visibility: channels.CollaborationVisibility(args.Visibility),
		CorrelationID: args.CorrelationID, RequestID: args.RequestID, TerminalState: channels.CollaborationTerminalState(args.TerminalState),
	})
	if err != nil {
		return "", err
	}
	return mustJSON(message)
}

type ChatDraftTool struct{ env *Env }

func NewChatDraftTool(env *Env) *ChatDraftTool   { return &ChatDraftTool{env: env} }
func (t *ChatDraftTool) Name() string            { return "chat_draft" }
func (t *ChatDraftTool) IsReadOnly() bool        { return false }
func (t *ChatDraftTool) IsConcurrencySafe() bool { return false }
func (t *ChatDraftTool) Definition() providers.ToolDefinition {
	return providers.ToolDefinition{
		Name: "chat_draft",
		Description: "List or explicitly resolve this named agent's held chat drafts. Resolve with as_is to post against a fresh basis, " +
			"silent to drop, or anyway to force-post after at least two holds. To revise, read the delta and call chat_send with new text and a fresh basis.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"action":     map[string]any{"type": "string", "enum": []string{"list", "resolve"}},
				"draft_id":   map[string]any{"type": "string"},
				"resolution": map[string]any{"type": "string", "enum": []string{"as_is", "silent", "anyway"}},
				"basis_seq":  map[string]any{"type": "integer", "minimum": 0},
			},
			"required": []string{"action"},
		},
	}
}
func (t *ChatDraftTool) Execute(ctx context.Context, argsJSON string) (string, error) {
	if t == nil || t.env == nil || t.env.ChatAgent == nil {
		return "", errors.New("chat_draft is available only in a named-agent session")
	}
	var args struct {
		Action     string `json:"action"`
		DraftID    string `json:"draft_id"`
		Resolution string `json:"resolution"`
		BasisSeq   *int64 `json:"basis_seq"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", err
	}
	switch strings.TrimSpace(args.Action) {
	case "list":
		if args.DraftID != "" || args.Resolution != "" || args.BasisSeq != nil {
			return "", errors.New("chat_draft list does not accept resolution fields")
		}
		drafts, err := t.env.ChatAgent.ListDrafts(ctx)
		if err != nil {
			return "", err
		}
		return mustJSON(map[string]any{"drafts": drafts})
	case "resolve":
		if strings.TrimSpace(args.DraftID) == "" {
			return "", errors.New("chat_draft resolve requires draft_id")
		}
		result, err := t.env.ChatAgent.ResolveDraft(ctx, channels.ResolveDraftParams{
			DraftID: args.DraftID, Resolution: channels.DraftResolution(args.Resolution), BasisSeq: args.BasisSeq,
		})
		if err != nil {
			return "", err
		}
		return mustJSON(result)
	default:
		return "", errors.New("chat_draft action must be list or resolve")
	}
}

type ChatTaskTool struct{ env *Env }

func NewChatTaskTool(env *Env) *ChatTaskTool    { return &ChatTaskTool{env: env} }
func (t *ChatTaskTool) Name() string            { return "chat_task" }
func (t *ChatTaskTool) IsReadOnly() bool        { return false }
func (t *ChatTaskTool) IsConcurrencySafe() bool { return false }
func (t *ChatTaskTool) Definition() providers.ToolDefinition {
	return providers.ToolDefinition{
		Name: "chat_task",
		Description: "Create, update, revise, or list lightweight tasks in a group-chat room. " +
			"Owners update progress; the hidden task author uses revise for user goal corrections, which invalidates stale verification.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"action":              map[string]any{"type": "string", "enum": []string{"create", "update", "revise", "list"}},
				"room_id":             map[string]any{"type": "string"},
				"thread_id":           map[string]any{"type": "string"},
				"source_message_id":   map[string]any{"type": "string"},
				"task_id":             map[string]any{"type": "string"},
				"title":               map[string]any{"type": "string"},
				"body":                map[string]any{"type": "string"},
				"owner_id":            map[string]any{"type": "string"},
				"lead_named_agent_id": map[string]any{"type": "string"},
				"target_session_ref":  map[string]any{"type": "string", "description": "Assign the new task delivery to one existing session of the owner."},
				"verification_required": map[string]any{
					"type": "boolean", "description": "Require an independent pass before the owner may mark the task done.",
				},
				"state": map[string]any{"type": "string", "enum": []string{
					"open", "doing", "checking", "revising", "needs_human", "done",
				}},
			},
			"required": []string{"action"},
		},
	}
}
func (t *ChatTaskTool) Execute(ctx context.Context, argsJSON string) (string, error) {
	if t == nil || t.env == nil || t.env.ChatAgent == nil {
		return "", errors.New("chat_task is available only in a named-agent session")
	}
	var args struct {
		Action               string `json:"action"`
		RoomID               string `json:"room_id"`
		ThreadID             string `json:"thread_id"`
		SourceMessageID      string `json:"source_message_id"`
		TaskID               string `json:"task_id"`
		Title                string `json:"title"`
		Body                 string `json:"body"`
		OwnerID              string `json:"owner_id"`
		LeadNamedAgentID     string `json:"lead_named_agent_id"`
		TargetSessionRef     string `json:"target_session_ref"`
		VerificationRequired bool   `json:"verification_required"`
		State                string `json:"state"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", err
	}
	switch strings.TrimSpace(args.Action) {
	case "create":
		message, err := t.env.ChatAgent.CreateTask(ctx, channels.TaskCreateParams{
			RoomID: args.RoomID, ThreadID: args.ThreadID, SourceMessageID: args.SourceMessageID,
			Title: args.Title, Body: args.Body, OwnerID: args.OwnerID,
			LeadNamedAgentID: args.LeadNamedAgentID, TargetSessionRef: args.TargetSessionRef,
			VerificationRequired: args.VerificationRequired,
		})
		if err != nil {
			return "", err
		}
		return mustJSON(map[string]any{"task": message})
	case "update":
		message, err := t.env.ChatAgent.UpdateTask(ctx, channels.TaskUpdateParams{TaskID: args.TaskID, RoomID: args.RoomID, State: channels.TaskState(args.State), OwnerID: args.OwnerID})
		if err != nil {
			return "", err
		}
		return mustJSON(map[string]any{"task": message})
	case "revise":
		message, err := t.env.ChatAgent.UpdateTask(ctx, channels.TaskUpdateParams{
			TaskID: args.TaskID, RoomID: args.RoomID, OwnerID: args.OwnerID, GoalCorrection: args.Body,
		})
		if err != nil {
			return "", err
		}
		return mustJSON(map[string]any{"task": message})
	case "list":
		tasks, err := t.env.ChatAgent.ListTasks(ctx, args.RoomID)
		if err != nil {
			return "", err
		}
		return mustJSON(map[string]any{"tasks": tasks})
	default:
		return "", errors.New("chat_task action must be create, update, revise, or list")
	}
}

type ChatVerifyTool struct{ env *Env }

func NewChatVerifyTool(env *Env) *ChatVerifyTool  { return &ChatVerifyTool{env: env} }
func (t *ChatVerifyTool) Name() string            { return "chat_verify" }
func (t *ChatVerifyTool) IsReadOnly() bool        { return false }
func (t *ChatVerifyTool) IsConcurrencySafe() bool { return false }
func (t *ChatVerifyTool) Definition() providers.ToolDefinition {
	return providers.ToolDefinition{
		Name: "chat_verify",
		Description: "Submit one independent verification decision for a room task. " +
			"The host persists the three-state decision and privately delivers the natural-language report to the visible owner. " +
			"Work owners and leads may submit a completed independent verifier run; assigned verifiers may submit their own completed run.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"room_id":            map[string]any{"type": "string"},
				"task_id":            map[string]any{"type": "string"},
				"goal_revision":      map[string]any{"type": "integer", "minimum": 1},
				"candidate_revision": map[string]any{"type": "integer", "minimum": 1},
				"decision": map[string]any{
					"type": "string", "enum": []string{"pass", "block", "unknown"},
				},
				"report": map[string]any{
					"type": "string", "maxLength": channels.MaxVerificationReportRunes,
					"description": "Natural-language evidence, reproduction steps, and remaining unknowns.",
				},
				"evidence_refs": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				"run_ref":       map[string]any{"type": "string"},
			},
			"required": []string{"room_id", "task_id", "goal_revision", "candidate_revision", "decision", "report"},
		},
	}
}

func (t *ChatVerifyTool) Execute(ctx context.Context, argsJSON string) (string, error) {
	if t == nil || t.env == nil || t.env.ChatAgent == nil {
		return "", errors.New("chat_verify is available only in a named-agent session")
	}
	var args struct {
		RoomID            string   `json:"room_id"`
		TaskID            string   `json:"task_id"`
		Decision          string   `json:"decision"`
		Report            string   `json:"report"`
		GoalRevision      int      `json:"goal_revision"`
		CandidateRevision int      `json:"candidate_revision"`
		EvidenceRefs      []string `json:"evidence_refs"`
		RunRef            string   `json:"run_ref"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", err
	}
	result, err := t.env.ChatAgent.SubmitTaskVerification(ctx, channels.TaskVerificationSubmitParams{
		RoomID: args.RoomID, TaskID: args.TaskID,
		Decision: channels.VerificationDecision(args.Decision), Report: args.Report,
		EvidenceRefs: args.EvidenceRefs, RunRef: args.RunRef,
		GoalRevision: args.GoalRevision, CandidateRevision: args.CandidateRevision,
	})
	if err != nil {
		return "", err
	}
	return mustJSON(result)
}

type ChatRemindTool struct{ env *Env }

func NewChatRemindTool(env *Env) *ChatRemindTool  { return &ChatRemindTool{env: env} }
func (t *ChatRemindTool) Name() string            { return "chat_remind" }
func (t *ChatRemindTool) IsReadOnly() bool        { return false }
func (t *ChatRemindTool) IsConcurrencySafe() bool { return false }
func (t *ChatRemindTool) Definition() providers.ToolDefinition {
	return providers.ToolDefinition{
		Name:        "chat_remind",
		Description: "Set, list, or cancel this named agent's reminders. Set with exactly one RFC3339 fire_at or relative duration such as 1m; the minimum delay is one minute.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"action": map[string]any{"type": "string", "enum": []string{"set", "list", "cancel"}}, "fire_at": map[string]any{"type": "string"},
				"after": map[string]any{"type": "string"}, "note": map[string]any{"type": "string"}, "room_id": map[string]any{"type": "string"},
				"thread_id": map[string]any{"type": "string"}, "state": map[string]any{"type": "string", "enum": []string{"pending", "fired", "cancelled"}},
				"reminder_id": map[string]any{"type": "string"},
			},
			"required": []string{"action"},
		},
	}
}
func (t *ChatRemindTool) Execute(ctx context.Context, argsJSON string) (string, error) {
	if t == nil || t.env == nil || t.env.ChatAgent == nil {
		return "", errors.New("chat_remind is available only in a named-agent session")
	}
	var args struct {
		Action     string `json:"action"`
		FireAt     string `json:"fire_at"`
		After      string `json:"after"`
		Note       string `json:"note"`
		RoomID     string `json:"room_id"`
		ThreadID   string `json:"thread_id"`
		State      string `json:"state"`
		ReminderID string `json:"reminder_id"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", err
	}
	switch strings.TrimSpace(args.Action) {
	case "set":
		if (strings.TrimSpace(args.FireAt) == "") == (strings.TrimSpace(args.After) == "") {
			return "", errors.New("chat_remind set requires exactly one of fire_at or after")
		}
		params := channels.ReminderSetParams{Note: args.Note, RoomID: args.RoomID, ThreadID: args.ThreadID}
		if strings.TrimSpace(args.FireAt) != "" {
			fireAt, err := time.Parse(time.RFC3339, strings.TrimSpace(args.FireAt))
			if err != nil {
				return "", err
			}
			params.FireAt = fireAt
			reminder, err := t.env.ChatAgent.SetReminder(ctx, params)
			if err != nil {
				return "", err
			}
			return mustJSON(map[string]any{"reminder": reminder})
		}
		delay, err := time.ParseDuration(strings.TrimSpace(args.After))
		if err != nil {
			return "", err
		}
		reminder, err := t.env.ChatAgent.SetReminderAfter(ctx, delay, params)
		if err != nil {
			return "", err
		}
		return mustJSON(map[string]any{"reminder": reminder})
	case "list":
		reminders, err := t.env.ChatAgent.ListReminders(ctx, channels.ReminderState(args.State))
		if err != nil {
			return "", err
		}
		return mustJSON(map[string]any{"reminders": reminders})
	case "cancel":
		reminder, err := t.env.ChatAgent.CancelReminder(ctx, args.ReminderID)
		if err != nil {
			return "", err
		}
		return mustJSON(map[string]any{"reminder": reminder})
	default:
		return "", errors.New("chat_remind action must be set, list, or cancel")
	}
}
