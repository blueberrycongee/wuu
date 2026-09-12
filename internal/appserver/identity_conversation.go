package appserver

import (
	"context"
	"errors"

	"github.com/blueberrycongee/wuu/internal/channels"
	"github.com/blueberrycongee/wuu/internal/runtime"
	"github.com/blueberrycongee/wuu/internal/session"
)

func (s *Server) dispatchIdentityConversationLocked(ctx context.Context, agent channels.AgentRuntime, force bool) error {
	// Admission must never move the room scope while a local or remote host is
	// still using it. Legacy executions drain before this identity switches over.
	active, _, err := s.namedAgentActivity(ctx, agent)
	if err != nil {
		return err
	}
	if active {
		return s.channelService.MarkWakePending(ctx, agent.ID)
	}
	ref := channels.NamedAgentConversationRef(agent)
	if !force && !agent.Autostart {
		if _, found, err := session.Find(s.rt.SessionDir, ref); err != nil || !found {
			return err
		}
	}
	selection, err := s.namedAgentPinnedSelection(agent, ref, "")
	if err != nil {
		return err
	}
	binding, ready, err := s.channelService.PrepareIdentityConversation(ctx, agent, channels.CollaborationSessionBindParams{
		Provider: selection.Provider, Model: selection.Model, Effort: firstNonEmpty(selection.Effort, selection.Variant), RuntimeVersion: runtime.CollaborationRuntimeVersion,
	})
	if err != nil {
		return err
	}
	if !ready {
		if binding.State == channels.CollaborationSessionQueued {
			client, err := s.bindCollaborationPrincipal(ctx, agent, "")
			if err != nil {
				return err
			}
			return s.startNamedAgentDispatchTargetLocked(ctx, agent, client, namedAgentDispatchTarget{binding: binding, workID: binding.WorkID, roomIDs: []string{binding.RoomID}}, force)
		}
		_, err := s.channelService.FinishWakeAttempt(ctx, agent.ID)
		return err
	}
	client, err := s.bindCollaborationPrincipal(ctx, agent, "")
	if err != nil {
		return err
	}
	return s.startNamedAgentDispatchTargetLocked(ctx, agent, client, namedAgentDispatchTarget{binding: binding, workID: binding.WorkID, roomIDs: []string{binding.RoomID}}, force)
}

func (s *Server) recoverIdentityConversationLocked(ctx context.Context, agent channels.AgentRuntime) error {
	refs, bindings, err := s.namedAgentSessionRefs(ctx, agent)
	if err != nil {
		return err
	}
	for _, ref := range refs {
		binding, exists := bindings[ref]
		if !exists || (binding.State != channels.CollaborationSessionRunning && binding.State != channels.CollaborationSessionStarting && binding.State != channels.CollaborationSessionWaiting) {
			continue
		}
		if active, err := session.ThreadExecutionActive(s.rt.SessionDir, ref); err != nil {
			return err
		} else if active {
			continue
		}
		if th := s.thread(ref); th != nil && threadIsRunning(th) {
			continue
		}
		if binding.Primary && binding.State == channels.CollaborationSessionWaiting {
			continue
		}
		if binding.Primary && binding.TurnID != "" {
			if err := s.settleCollaborationTurn(ctx, ref, binding.TurnID); err == nil {
				continue
			}
		}
		client, err := s.bindCollaborationPrincipal(ctx, agent, "")
		if err != nil {
			return err
		}
		if _, err = client.UpdateCollaborationSessionState(ctx, channels.CollaborationSessionStateParams{SessionRef: ref, State: channels.CollaborationSessionIdle}); err != nil && !errors.Is(err, channels.ErrConflict) {
			return err
		}
		if binding.RoomID == "" {
			continue
		}
		_, err = s.channelService.EnqueueSessionInput(ctx, channels.CollaborationSessionSendParams{SessionRef: ref,
			Body:      "Continue unfinished responsibilities after restart in your single identity conversation. Read the existing task and history before repeating effects. Previous session: " + ref,
			RequestID: "identity-recovery:" + ref + ":" + binding.UpdatedAt.String(),
		})
		if err != nil {
			return err
		}
	}
	return s.dispatchIdentityConversationLocked(ctx, agent, false)
}

func (s *Server) identityIsRoomMember(ctx context.Context, agentID, roomID string) bool {
	room, err := s.channelService.GetRoom(ctx, roomID)
	return err == nil && roomContainsAgent(room, agentID)
}
