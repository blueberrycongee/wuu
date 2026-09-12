package appserver

import (
	"context"
	"errors"
	"fmt"

	"github.com/blueberrycongee/wuu/internal/channels"
)

func roomCoordinatorOrientation(roomID string) string {
	return collaborationEnvironmentOrientation + fmt.Sprintf(`# Room coordination
You manage shared goals in room %s. You are a hidden coordinator, not a named member. Never impersonate a member or write a public answer. Your final text stays private. You can read project evidence but cannot edit files, execute shell commands, or run implementation tools.

Interpret incoming messages in the ongoing room context. Use chat_read, chat_roster, chat_session and task records as needed to choose who can advance the user's goal. A direct assignment made while you were inactive is already part of the collaboration; help it continue rather than assuming you must dispatch it again. Start with one accountable member unless independent work warrants parallel sessions. Members own their expertise and private memory; use room messages, objectives, shared evidence and results, not another identity's private history. Goals, assignments and progress belong to this room and survive a change of members.

For a simple question, use collaboration_send to the selected member with the original source_message_id and an instruction to answer the user in the room. This uses the member's room conversation. For substantial work, create a chat_task with a visible owner and source_message_id, or create a chat_session under a room member with a concrete objective and stable request_id. Independent session results return privately to you. Tell a responsible member to publish meaningful progress and the final result. Do not start a second job merely because an existing one is queued or waiting.

Follow task and session terminal events. Decide whether the goal is satisfied, more evidence is needed, a dependent step can start, or a member needs help. Use chat_session send/stop/resume to steer existing work; reread current state before acting. Reuse relevant sessions for follow-ups. Respect explicit user recipients and keep independent work separate. Verification is optional and should answer a concrete uncertainty; ordinary conversation needs no task or verification ceremony.

Delegate asynchronously. After assigning work, yield_turn to release execution capacity; never poll or wait inside a model turn. New user messages and completed or failed sessions wake you again. A completion is a fact, not a new request to acknowledge: do not create acknowledgement loops. If the requested result is already visible, stop. If no suitable member exists, do not invent a recipient; wait for a membership change. Never fall back to waking every member.
`, roomID)
}

// ChannelCoordinatorStatus exposes operational state without adding a hidden
// identity or its private transcript to the room member/response namespaces.
type ChannelCoordinatorStatus struct {
	State      string   `json:"state"`
	SessionRef string   `json:"session_ref,omitempty"`
	AgentIDs   []string `json:"agent_ids,omitempty"`
	Error      string   `json:"error,omitempty"`
}

func (s *Server) channelCoordinatorStatus(ctx context.Context, roomID string) (*ChannelCoordinatorStatus, error) {
	room, err := s.channelService.GetRoom(ctx, roomID)
	if err != nil {
		return nil, err
	}
	if room.RuntimeID == "" {
		return nil, nil
	}
	agent, err := s.channelService.GetAgentRuntime(ctx, room.RuntimeID)
	if err != nil {
		return nil, err
	}
	result := &ChannelCoordinatorStatus{State: "idle"}
	members := 0
	for _, member := range room.Members {
		if member.MemberType == channels.MemberAgent {
			members++
		}
	}
	if members == 0 {
		result.State = "needs_members"
	}
	binding, err := s.channelService.LookupCollaborationSession(ctx, namedAgentRoomSessionID(agent, roomID))
	if errors.Is(err, channels.ErrNotFound) {
		wake, wakeErr := s.channelService.WakeState(ctx, agent.ID)
		if wakeErr != nil {
			return nil, wakeErr
		}
		if wake.Outstanding {
			result.State = "queued"
		}
		return result, nil
	}
	if err != nil {
		return nil, err
	}
	result.SessionRef = binding.SessionRef
	switch binding.State {
	case channels.CollaborationSessionStarting, channels.CollaborationSessionRunning:
		result.State = "working"
	case channels.CollaborationSessionQueued:
		result.State = "queued"
	case channels.CollaborationSessionFailed, channels.CollaborationSessionInterrupted, channels.CollaborationSessionCancelled, channels.CollaborationSessionMissing:
		result.State = "failed"
	case channels.CollaborationSessionWaiting:
		result.State = "waiting"
	default:
		if binding.FailureReason != "" {
			result.State = "failed"
		}
	}
	result.Error = binding.FailureReason
	if result.State == "working" || result.State == "queued" {
		result.Error = ""
	}
	client, err := s.channelService.BindRuntime(ctx, agent.ID)
	if err != nil {
		return nil, err
	}
	sessions, err := client.ListCollaborationSessions(ctx, channels.CollaborationSessionListParams{RoomID: roomID})
	if err != nil {
		return nil, err
	}
	for _, child := range sessions {
		if child.NamedAgentID == "" {
			continue
		}
		switch child.State {
		case channels.CollaborationSessionStarting, channels.CollaborationSessionRunning, channels.CollaborationSessionQueued, channels.CollaborationSessionWaiting:
			result.AgentIDs = appendDistinctStrings(result.AgentIDs, child.NamedAgentID)
		}
	}
	// Independent Work sessions also represent ongoing room work, even when
	// their result path is an assignment rather than a parent binding.
	if result.State == "idle" && len(result.AgentIDs) > 0 {
		result.State = "waiting"
	}
	if members == 0 && result.State != "working" && result.State != "queued" {
		result.State = "needs_members"
		result.Error = ""
	}
	return result, nil
}
