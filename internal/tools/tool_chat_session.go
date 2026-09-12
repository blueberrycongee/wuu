package tools

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/blueberrycongee/wuu/internal/channels"
	"github.com/blueberrycongee/wuu/internal/providers"
)

// ChatSessionTool exposes the same durable session lifecycle used by the UI.
// Models choose the collaboration graph; the host owns identity and admission.
type ChatSessionTool struct{ env *Env }

func NewChatSessionTool(env *Env) *ChatSessionTool { return &ChatSessionTool{env} }
func (t *ChatSessionTool) Name() string            { return "chat_session" }
func (t *ChatSessionTool) IsReadOnly() bool        { return false }
func (t *ChatSessionTool) IsConcurrencySafe() bool { return true }
func (t *ChatSessionTool) Definition() providers.ToolDefinition {
	return providers.ToolDefinition{Name: t.Name(), Description: "Discover named agents' continuing conversations and read prior execution results. Each name has one continuing session. Use send for asynchronous peer messages with room_id and request_id; use collaboration_send or chat_task to delegate to another identity. Work directly in your own session. List/get include archived execution metadata; prior sessions must not be restarted as parallel copies. Stop pauses a conversation and resume continues it. Private transcripts remain private.", InputSchema: map[string]any{
		"type": "object", "properties": map[string]any{
			"action":    map[string]any{"type": "string", "enum": []string{"list", "peers", "get", "results", "send", "stop", "resume"}},
			"agent_id":  map[string]any{"type": "string", "description": "Durable named identity to discover."},
			"result_id": map[string]any{"type": "integer"}, "offset": map[string]any{"type": "integer", "minimum": 0},
			"after": map[string]any{"type": "integer", "minimum": 0}, "limit": map[string]any{"type": "integer", "minimum": 1, "maximum": 50},
			"room_id": map[string]any{"type": "string"}, "session_ref": map[string]any{"type": "string"}, "title": map[string]any{"type": "string"},
			"prompt":   map[string]any{"type": "string", "description": "Objective and relevant evidence for create, or follow-up message for send. Include expected result and where artifacts can be read."},
			"provider": map[string]any{"type": "string"}, "model": map[string]any{"type": "string"}, "effort": map[string]any{"type": "string"}, "request_id": map[string]any{"type": "string", "description": "Reuse only for retries of the same creation or message."},
		}, "required": []string{"action"}}}
}
func (t *ChatSessionTool) Execute(ctx context.Context, argsJSON string) (string, error) {
	if t == nil || t.env == nil || t.env.ChatAgent == nil {
		return "", errors.New("chat_session requires a collaboration identity")
	}
	var args struct {
		ResultID   int64  `json:"result_id"`
		Offset     int    `json:"offset"`
		After      int64  `json:"after"`
		Limit      int    `json:"limit"`
		Action     string `json:"action"`
		AgentID    string `json:"agent_id"`
		RoomID     string `json:"room_id"`
		SessionRef string `json:"session_ref"`
		Title      string `json:"title"`
		Prompt     string `json:"prompt"`
		Provider   string `json:"provider"`
		Model      string `json:"model"`
		Effort     string `json:"effort"`
		RequestID  string `json:"request_id"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", err
	}
	c := t.env.ChatAgent
	switch strings.TrimSpace(args.Action) {
	case "peers":
		result, err := c.RoomPeers(ctx, args.RoomID)
		if err != nil {
			return "", err
		}
		return mustJSON(map[string]any{"peers": result})
	case "list":
		result, err := c.ListCollaborationSessions(ctx, channels.CollaborationSessionListParams{PrincipalID: args.AgentID, RoomID: args.RoomID})
		if err != nil {
			return "", err
		}
		return mustJSON(map[string]any{"sessions": result})
	case "results":
		if args.ResultID > 0 {
			result, err := c.ReadSessionResultPage(ctx, args.SessionRef, args.ResultID, args.Offset)
			if err != nil {
				return "", err
			}
			return mustJSON(result)
		}
		result, err := c.ReadSessionResults(ctx, args.SessionRef, args.After, args.Limit)
		if err != nil {
			return "", err
		}
		return mustJSON(result)
	case "get":
		result, err := c.GetCollaborationSession(ctx, args.SessionRef)
		if err != nil {
			return "", err
		}
		return mustJSON(map[string]any{"session": result})
	case "create":
		if strings.TrimSpace(args.RequestID) == "" {
			return "", errors.New("create requires a stable request_id")
		}
		result, err := c.CreateSession(ctx, channels.CollaborationSessionCreateParams{NamedAgentID: args.AgentID, RoomID: args.RoomID, Title: args.Title, Objective: args.Prompt, Provider: args.Provider, Model: args.Model, Effort: args.Effort, RequestID: args.RequestID})
		if err != nil {
			return "", err
		}
		return mustJSON(map[string]any{"session": result})
	case "send":
		if strings.TrimSpace(args.RequestID) == "" {
			return "", errors.New("send requires a stable request_id")
		}
		result, err := c.SendSession(ctx, channels.CollaborationSessionSendParams{SessionRef: args.SessionRef, RoomID: args.RoomID, Body: args.Prompt, RequestID: args.RequestID})
		if err != nil {
			return "", err
		}
		return mustJSON(map[string]any{"session": result})
	case "stop":
		result, err := c.StopSession(ctx, channels.CollaborationSessionControlParams{SessionRef: args.SessionRef})
		if err != nil {
			return "", err
		}
		return mustJSON(map[string]any{"session": result})
	case "resume":
		result, err := c.ResumeSession(ctx, channels.CollaborationSessionControlParams{SessionRef: args.SessionRef})
		if err != nil {
			return "", err
		}
		return mustJSON(map[string]any{"session": result})
	default:
		return "", errors.New("unsupported chat_session action")
	}
}
