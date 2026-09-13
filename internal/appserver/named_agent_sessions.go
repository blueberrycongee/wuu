package appserver

import (
	"context"
	"errors"
	"strings"

	"github.com/blueberrycongee/wuu/internal/channels"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/runtime"
	"github.com/blueberrycongee/wuu/internal/session"
)

func (s *Server) namedAgentSessionRefs(ctx context.Context, agent channels.AgentRuntime) ([]string, map[string]channels.CollaborationSessionBinding, error) {
	refs := []string{agentRuntimeSessionID(agent)}
	seen := map[string]struct{}{refs[0]: {}}
	bySession := make(map[string]channels.CollaborationSessionBinding)
	client, err := s.bindCollaborationPrincipal(ctx, agent, "")
	if err != nil {
		return nil, nil, err
	}
	bindings, err := client.ListCollaborationSessions(ctx, channels.CollaborationSessionListParams{PrincipalID: agent.ID})
	if err != nil {
		return nil, nil, err
	}
	for _, binding := range bindings {
		bySession[binding.SessionRef] = binding
		if _, ok := seen[binding.SessionRef]; !ok {
			seen[binding.SessionRef] = struct{}{}
			refs = append(refs, binding.SessionRef)
		}
	}
	if s.rt != nil {
		stored, listErr := session.List(s.rt.SessionDir, 0)
		if listErr != nil {
			return nil, nil, listErr
		}
		for _, metadata := range stored {
			if metadata.Source != namedAgentSessionSource+agent.ID {
				continue
			}
			if _, ok := seen[metadata.ID]; ok {
				continue
			}
			seen[metadata.ID] = struct{}{}
			refs = append(refs, metadata.ID)
		}
	}
	return refs, bySession, nil
}

func (s *Server) namedAgentActivity(ctx context.Context, agent channels.AgentRuntime) (bool, []string, error) {
	refs, bindings, err := s.namedAgentSessionRefs(ctx, agent)
	if err != nil {
		return false, nil, err
	}
	thinking := false
	roomIDs := make([]string, 0)
	for _, ref := range refs {
		if th := s.thread(ref); th != nil && threadIsRunning(th) {
			thinking = true
			roomIDs = appendDistinctStrings(roomIDs, namedAgentActivityRoomIDs(th)...)
			if binding, ok := bindings[ref]; ok {
				roomIDs = appendDistinctStrings(roomIDs, binding.RoomID)
			}
			continue
		}
		if s.rt == nil {
			continue
		}
		active, activeErr := session.ThreadExecutionActive(s.rt.SessionDir, ref)
		if activeErr != nil {
			return false, nil, activeErr
		}
		if active {
			thinking = true
			if binding, ok := bindings[ref]; ok {
				roomIDs = appendDistinctStrings(roomIDs, binding.RoomID)
			}
		}
	}
	return thinking, roomIDs, nil
}

type namedAgentDispatchTarget struct {
	binding channels.CollaborationSessionBinding
	workID  string
	roomIDs []string
}

// dispatchNamedAgentWakeLocked fans one principal-level wake out to every
// durable session that currently owns a delivery. The caller holds
// namedAgentMu only while sessions are selected and admitted; inference runs
// independently after admission.
func (s *Server) dispatchNamedAgentWakeLocked(ctx context.Context, agent channels.AgentRuntime, force bool) error {
	if agent.IsRoomRuntime() {
		return nil
	}
	return s.dispatchIdentityConversationLocked(ctx, agent, force)
}

// Each identity has a separate room conversation. It uses the same durable
// admission and parent-result path as independent sessions, so direct room
// traffic cannot bypass BYOK capacity or lose the origin of delegated work.
func (s *Server) startNamedAgentConversationLocked(ctx context.Context, agent channels.AgentRuntime, client *channels.AgentClient, roomID string, force bool) error {
	sessionRef := namedAgentRoomSessionID(agent, roomID)
	binding, err := client.GetCollaborationSession(ctx, sessionRef)
	if errors.Is(err, channels.ErrNotFound) {
		selection, selectErr := s.namedAgentPinnedSelection(agent, sessionRef, "")
		if selectErr != nil {
			return selectErr
		}
		binding, err = client.BindCollaborationSession(ctx, channels.CollaborationSessionBindParams{
			SessionRef: sessionRef, RoomID: roomID, Purpose: roomConversationPurpose(agent),
			State: channels.CollaborationSessionIdle, RuntimeVersion: runtime.CollaborationRuntimeVersion,
			Title: agent.Name, Provider: selection.Provider, Model: selection.Model, Effort: firstNonEmpty(selection.Effort, selection.Variant),
		})
	}
	if err != nil {
		return err
	}
	return s.startNamedAgentDispatchTargetLocked(ctx, agent, client, namedAgentDispatchTarget{
		binding: binding, roomIDs: []string{roomID},
	}, force)
}

func namedAgentRoomSessionID(agent channels.AgentRuntime, roomID string) string {
	if !agent.IsRoomRuntime() {
		return channels.NamedAgentConversationRef(agent)
	}
	return principalSessionID(agent.ID+"\x00room:"+roomID, agent.CreatedAt)
}

func (s *Server) startNamedAgentDispatchTargetLocked(ctx context.Context, agent channels.AgentRuntime, client *channels.AgentClient, target namedAgentDispatchTarget, force bool) (dispatchErr error) {
	defer func() {
		if dispatchErr != nil && (target.workID == "" || target.binding.Primary) {
			s.failCollaborationSession(ctx, target.binding, dispatchErr)
		}
	}()
	sessionRef := target.binding.SessionRef
	if !force && !agent.Autostart {
		if _, found, err := session.Find(s.rt.SessionDir, sessionRef); err != nil || !found {
			return err
		}
	}
	if target.binding.State == channels.CollaborationSessionCancelled || target.binding.State == channels.CollaborationSessionInterrupted || target.binding.State == channels.CollaborationSessionFailed || target.binding.State == channels.CollaborationSessionMissing {
		return nil
	}
	if target.workID == "" || target.binding.Primary && target.binding.RunID == "" {
		admitted, err := s.channelService.AdmitCollaborationSession(ctx, sessionRef)
		if err != nil {
			return err
		}
		if admitted.State == channels.CollaborationSessionQueued {
			return nil
		}
		target.binding = admitted
	}

	th, err := s.ensureAgentRuntimeSessionThreadLocked(agent, sessionRef)
	if err != nil {
		return err
	}
	runID := target.binding.RunID
	if target.workID != "" && runID == "" {
		runID, err = s.ensureNamedAgentProducerRun(ctx, client, target.binding)
		if err != nil {
			return err
		}
	} else if target.workID == "" && target.binding.State != channels.CollaborationSessionRunning {
		if _, err := client.UpdateCollaborationSessionState(ctx, channels.CollaborationSessionStateParams{
			SessionRef: sessionRef, State: channels.CollaborationSessionRunning,
		}); err != nil {
			return err
		}
	}
	if target.workID != "" && runID != "" {
		work, err := client.GetWork(ctx, target.workID)
		if err != nil {
			return err
		}
		for _, run := range work.Runs {
			if run.ID == runID && run.State == channels.WorkRunQueued {
				// The durable admission queue owns this wake. A terminal event will
				// promote the run and send a fresh control delivery when capacity opens.
				return nil
			}
		}
	}
	if threadIsRunning(th) {
		if err := s.channelService.MarkWakePending(ctx, agent.ID); err != nil {
			return err
		}
		return nil
	}
	return s.startAgentRuntimeSessionWakeLocked(agent, th, target.roomIDs, target.workID, runID)
}

func (s *Server) resumeNamedAgentBoundSessionsLocked(ctx context.Context, agent channels.AgentRuntime) error {
	if agent.IsRoomRuntime() {
		return nil
	}
	return s.recoverIdentityConversationLocked(ctx, agent)
}

func (s *Server) ensureNamedAgentProducerRun(ctx context.Context, client *channels.AgentClient, binding channels.CollaborationSessionBinding) (string, error) {
	work, err := client.GetWork(ctx, binding.WorkID)
	if err != nil {
		return "", err
	}
	for _, run := range work.Runs {
		if run.State != channels.WorkRunRunning && run.State != channels.WorkRunQueued {
			continue
		}
		if run.SessionRef != binding.SessionRef || run.NamedAgentID != binding.NamedAgentID {
			continue
		}
		return run.ID, nil
	}
	sessionClient, err := s.channelService.BindAgentSession(ctx, binding.PrincipalID, binding.SessionRef)
	if err != nil {
		return "", err
	}
	if binding.Primary {
		// Results retain their task scope, but receiving one does not request
		// another producer. Only an assignment or explicit continuation does.
		messages, err := sessionClient.ReceiveCollaboration(ctx, 32)
		if err != nil {
			return "", err
		}
		requested := false
		for _, message := range messages {
			if message.WorkID == binding.WorkID && (message.Kind == channels.CollaborationAssignment || message.Kind == channels.CollaborationControl) {
				requested = true
				break
			}
		}
		if !requested {
			return "", nil
		}
	}
	run, err := sessionClient.StartWorkRun(ctx, channels.WorkRunStartParams{
		WorkID: binding.WorkID, Kind: channels.WorkRunProducer, Profile: binding.NamedAgentID,
	})
	if err != nil {
		return "", err
	}
	return run.ID, nil
}

func (s *Server) finishNamedAgentWorkRun(ctx context.Context, agentID, sessionRef, workID, runID, turnID string) {
	client, err := s.channelService.BindAgentSession(ctx, agentID, sessionRef)
	if err != nil {
		providers.DebugLogf("bind completed work session %q: %v", sessionRef, err)
		return
	}
	work, err := client.GetWork(ctx, workID)
	if err != nil {
		providers.DebugLogf("load completed work %q: %v", workID, err)
		return
	}
	var current channels.WorkRun
	for _, run := range work.Runs {
		if run.ID == runID {
			current = run
			break
		}
	}
	if current.ID == "" || current.State != channels.WorkRunRunning {
		return
	}
	state := channels.WorkRunFailed
	outcome := "turn_failed"
	var provider, model string
	var inputTokens, outputTokens int64
	qualified := false
	if th := s.thread(sessionRef); th != nil {
		th.mu.Lock()
		for _, turn := range th.Turns {
			if turn.ID != turnID {
				continue
			}
			provider, model = turn.ModelProvider, turn.Model
			inputTokens, outputTokens = int64(turn.InputTokens), int64(turn.OutputTokens)
			switch turn.Status {
			case TurnStatusCompleted:
				state, outcome = channels.WorkRunCompleted, "turn_completed"
				var messages []string
				for _, item := range turn.Items {
					if item.Type == ThreadItemAgentMessage && strings.TrimSpace(item.Text) != "" {
						messages = append(messages, item.Text)
					}
				}
				if len(messages) > 0 {
					outcome = strings.Join(messages, "\n")
				}
			case TurnStatusInterrupted:
				state, outcome = channels.WorkRunInterrupted, "turn_interrupted"
			}
			break
		}
		th.mu.Unlock()
	}
	if state == channels.WorkRunCompleted && current.Kind == channels.WorkRunProducer {
		for _, artifact := range work.Artifacts {
			if artifact.RunID == current.ID && artifact.Kind == channels.WorkArtifactCandidate {
				qualified = true
				break
			}
		}
	}
	if _, err := client.FinishWorkRun(ctx, channels.WorkRunFinishParams{
		WorkID: workID, RunID: runID, State: state, Outcome: outcome,
		Provider: provider, Model: model, InputTokens: inputTokens, OutputTokens: outputTokens,
		Qualified: qualified,
	}); err != nil && !errors.Is(err, channels.ErrConflict) {
		providers.DebugLogf("finish named agent work run %q: %v", runID, err)
	}
}

func namedAgentWorkSessionID(agent channels.AgentRuntime, workID string) string {
	return principalSessionID(agent.ID+"\x00work\x00"+strings.TrimSpace(workID), agent.CreatedAt)
}

func roomConversationPurpose(agent channels.AgentRuntime) channels.CollaborationSessionPurpose {
	if agent.IsRoomRuntime() {
		return channels.CollaborationSessionCoordination
	}
	return channels.CollaborationSessionConversation
}
