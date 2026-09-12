package appserver

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/blueberrycongee/wuu/internal/channels"
)

// ChannelResponse is the visible room conversation's current reply. Private
// worker sessions, reasoning and tool results never enter this projection.
type ChannelResponse struct {
	ID         string    `json:"id"`
	RoomID     string    `json:"room_id"`
	AgentID    string    `json:"agent_id"`
	SessionRef string    `json:"session_ref"`
	TurnID     string    `json:"turn_id"`
	State      string    `json:"state"`
	Body       string    `json:"body"`
	Error      string    `json:"error,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
}

func isRoomConversation(binding channels.CollaborationSessionBinding, agent channels.AgentRuntime) bool {
	return binding.Purpose == channels.CollaborationSessionConversation && binding.WorkID == "" &&
		binding.ParentSessionRef == "" && binding.SessionRef == namedAgentRoomSessionID(agent, binding.RoomID)
}

func (s *Server) channelResponses(ctx context.Context, roomID string) ([]ChannelResponse, error) {
	room, err := s.channelService.GetRoom(ctx, roomID)
	if err != nil {
		return nil, err
	}
	responses := make([]ChannelResponse, 0)
	for _, member := range room.Members {
		if member.MemberType != channels.MemberAgent {
			continue
		}
		agent, err := s.channelService.GetAgentRuntime(ctx, member.MemberID)
		if errors.Is(err, channels.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		binding, err := s.channelService.LookupCollaborationSession(ctx, namedAgentRoomSessionID(agent, roomID))
		if errors.Is(err, channels.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if !isRoomConversation(binding, agent) {
			continue
		}
		response := ChannelResponse{
			ID: channels.ConversationReplyID(binding.SessionRef, binding.TurnID), RoomID: roomID,
			AgentID: agent.ID, SessionRef: binding.SessionRef, TurnID: binding.TurnID,
			CreatedAt: binding.UpdatedAt, Error: binding.FailureReason,
		}
		switch binding.State {
		case channels.CollaborationSessionQueued:
			response.State = "queued"
			response.TurnID = ""
			response.ID = channels.ConversationReplyID(binding.SessionRef, "queued")
		case channels.CollaborationSessionStarting, channels.CollaborationSessionRunning:
			response.State = "thinking"
		case channels.CollaborationSessionWaiting:
			response.State = "waiting"
			response.ID = channels.ConversationReplyID(binding.SessionRef, "waiting:"+binding.TurnID)
		case channels.CollaborationSessionFailed, channels.CollaborationSessionMissing:
			response.State = "failed"
		case channels.CollaborationSessionInterrupted, channels.CollaborationSessionCancelled:
			response.State = "interrupted"
		default:
			if binding.FailureReason == "" {
				continue
			}
			response.State = "failed"
		}
		if binding.TurnID != "" && response.State != "queued" && response.State != "waiting" {
			turn := s.roomResponseTurn(binding.SessionRef, binding.TurnID)
			if turn != nil {
				if turn.StartedAt != nil {
					response.CreatedAt = *turn.StartedAt
				}
				if !roomReplySent(*turn, roomID) {
					response.Body = roomReplyText(*turn, true)
				}
				if turn.Error != nil {
					response.Error = turn.Error.Message
				}
				if turn.Status == TurnStatusFailed {
					response.State = "failed"
				} else if turn.Status == TurnStatusInterrupted {
					response.State = "interrupted"
				} else if response.Body != "" && response.State == "thinking" {
					response.State = "responding"
				}
			}
		}
		responses = append(responses, response)
	}
	return responses, nil
}

func (s *Server) roomResponseTurn(ref, turnID string) *Turn {
	th := s.thread(ref)
	if !threadIsRunning(th) {
		// Another project window may own this identity's execution. Read its
		// durable display state without acquiring a lease or starting a runtime.
		if s.rt == nil {
			return nil
		}
		var err error
		th, err = s.loadPersistedThreadState(ref, time.Now().UTC())
		if err != nil {
			return nil
		}
	}
	th.mu.Lock()
	defer th.mu.Unlock()
	for i := len(th.Turns) - 1; i >= 0; i-- {
		if th.Turns[i].ID == turnID {
			turn := th.Turns[i]
			turn.Items = append([]ThreadItem(nil), turn.Items...)
			return &turn
		}
	}
	return nil
}

func roomReplyText(turn Turn, preview bool) string {
	for i := len(turn.Items) - 1; i >= 0; i-- {
		item := turn.Items[i]
		if item.Type == ThreadItemAgentMessage && (item.Terminal || preview) {
			if strings.TrimSpace(item.Text) == "" {
				return ""
			}
			return item.Text
		}
	}
	return ""
}

// Suppress only an answer already delivered to this room. Earlier progress or
// targeted requests must not swallow a later answer; held drafts are not delivery.
func roomReplySent(turn Turn, roomID string) bool {
	body := strings.TrimSpace(roomReplyText(turn, true))
	if body == "" {
		return false
	}
	for _, item := range turn.Items {
		if item.Type != ThreadItemToolCall || item.Status != ThreadItemStatusCompleted ||
			(item.Name != "chat_send" && item.Name != "collaboration_send" && item.Name != "chat_draft") {
			continue
		}
		var result struct {
			Status  string `json:"status"`
			Message struct {
				RoomID string `json:"room_id"`
				Body   string `json:"body"`
			} `json:"message"`
		}
		if json.Unmarshal([]byte(item.Result), &result) == nil && result.Status == string(channels.SendCommitted) && result.Message.RoomID == roomID && strings.TrimSpace(result.Message.Body) == body {
			return true
		}
	}
	return false
}
