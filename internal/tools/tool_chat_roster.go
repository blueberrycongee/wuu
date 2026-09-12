package tools

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/blueberrycongee/wuu/internal/providers"
)

type ChatRosterTool struct{ env *Env }

func NewChatRosterTool(env *Env) *ChatRosterTool  { return &ChatRosterTool{env: env} }
func (t *ChatRosterTool) Name() string            { return "chat_roster" }
func (t *ChatRosterTool) IsReadOnly() bool        { return false }
func (t *ChatRosterTool) IsConcurrencySafe() bool { return false }
func (t *ChatRosterTool) Definition() providers.ToolDefinition {
	return providers.ToolDefinition{
		Name:        "chat_roster",
		Description: "List this room's named agents with their roles and current room-scoped sessions; invite an existing named agent; or propose a persistent named agent for user approval. Creating a new role always requires the user's model choice in the room. Use create only for a durable distinct role; never create anonymous or disposable workers.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"action":   map[string]any{"type": "string", "enum": []string{"list", "invite", "create"}},
				"room_id":  map[string]any{"type": "string"},
				"agent_id": map[string]any{"type": "string"},
				"name":     map[string]any{"type": "string", "maxLength": 64},
				"role":     map[string]any{"type": "string", "maxLength": 280},
			},
			"required": []string{"action", "room_id"},
		},
	}
}

func (t *ChatRosterTool) Execute(ctx context.Context, argsJSON string) (string, error) {
	if t == nil || t.env == nil || t.env.ChatAgent == nil {
		return "", errors.New("chat_roster is available only in a named-agent session")
	}
	var args struct {
		Action  string `json:"action"`
		RoomID  string `json:"room_id"`
		AgentID string `json:"agent_id"`
		Name    string `json:"name"`
		Role    string `json:"role"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", err
	}
	switch strings.TrimSpace(args.Action) {
	case "list":
		result, err := t.env.ChatAgent.RoomRoster(ctx, args.RoomID)
		if err != nil {
			return "", err
		}
		return mustJSON(result)
	case "invite":
		if strings.TrimSpace(args.AgentID) == "" {
			return "", errors.New("chat_roster invite requires agent_id")
		}
		room, err := t.env.ChatAgent.InviteRoomAgent(ctx, args.RoomID, args.AgentID)
		if err != nil {
			return "", err
		}
		return mustJSON(map[string]any{"room": room})
	case "create":
		if strings.TrimSpace(args.Name) == "" {
			return "", errors.New("chat_roster create requires name")
		}
		result, err := t.env.ChatAgent.ProposeRoomAgent(ctx, args.RoomID, args.Name, args.Role)
		if err != nil {
			return "", err
		}
		return mustJSON(result)
	default:
		return "", errors.New("chat_roster action must be list, invite, or create")
	}
}
