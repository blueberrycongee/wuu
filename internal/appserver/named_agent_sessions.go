package appserver

import (
	"context"
	"errors"
	"fmt"
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
	client, err := s.bindCollaborationPrincipal(ctx, agent, "")
	if err != nil {
		return err
	}
	bindings, err := client.ListCollaborationSessions(ctx, channels.CollaborationSessionListParams{PrincipalID: agent.ID})
	if err != nil {
		return err
	}
	bySession := make(map[string]channels.CollaborationSessionBinding, len(bindings))
	byWork := make(map[string]channels.CollaborationSessionBinding, len(bindings))
	hasWorkBinding := make(map[string]bool, len(bindings))
	for _, binding := range bindings {
		bySession[binding.SessionRef] = binding
		if binding.WorkID != "" {
			hasWorkBinding[binding.WorkID] = true
		}
		if binding.WorkID != "" && (binding.State == channels.CollaborationSessionIdle || binding.State == channels.CollaborationSessionRunning) {
			if _, exists := byWork[binding.WorkID]; !exists {
				byWork[binding.WorkID] = binding
			}
		}
	}
	dispatches, err := s.channelService.PendingCollaborationDispatches(ctx, agent.ID)
	if err != nil {
		return err
	}
	targets := make(map[string]namedAgentDispatchTarget)
	conversationRooms := make(map[string]struct{})
	var dispatchErr error
	for _, dispatch := range dispatches {
		if dispatch.TargetSessionRef == "" && (agent.IsRoomRuntime() || dispatch.WorkID == "" || collaborationDispatchIsResult(dispatch.Kind)) {
			conversationRooms[dispatch.RoomID] = struct{}{}
			continue
		}
		binding, found := bySession[dispatch.TargetSessionRef]
		if dispatch.TargetSessionRef == "" {
			binding, found = byWork[dispatch.WorkID]
			if !found {
				sessionRef := namedAgentWorkSessionID(agent, dispatch.WorkID)
				if hasWorkBinding[dispatch.WorkID] {
					sessionRef = session.NewID()
				}
				binding, err = client.BindCollaborationSession(ctx, channels.CollaborationSessionBindParams{
					SessionRef: sessionRef, PrincipalID: agent.ID, RoomID: dispatch.RoomID,
					WorkID: dispatch.WorkID, Purpose: channels.CollaborationSessionWork,
					State: channels.CollaborationSessionIdle,
				})
				if err != nil {
					dispatchErr = errors.Join(dispatchErr, fmt.Errorf("bind work %q: %w", dispatch.WorkID, err))
					continue
				}
				bySession[binding.SessionRef] = binding
				byWork[binding.WorkID] = binding
				hasWorkBinding[binding.WorkID] = true
			}
		} else if !found {
			dispatchErr = errors.Join(dispatchErr, fmt.Errorf("target collaboration session %q is unavailable", dispatch.TargetSessionRef))
			continue
		}
		if binding.PrincipalID != agent.ID || binding.RoomID != dispatch.RoomID || (dispatch.TargetSessionRef == "" && binding.WorkID != dispatch.WorkID) {
			dispatchErr = errors.Join(dispatchErr, fmt.Errorf("target collaboration session %q has a different owner or scope", binding.SessionRef))
			continue
		}
		if dispatch.TargetSessionRef == "" && dispatch.WorkID != "" {
			if err := s.channelService.RoutePendingCollaborationToSession(ctx, agent.ID, dispatch.WorkID, binding.SessionRef); err != nil {
				dispatchErr = errors.Join(dispatchErr, fmt.Errorf("route work %q: %w", dispatch.WorkID, err))
				continue
			}
		}
		target := targets[binding.SessionRef]
		target.binding = binding
		target.workID = binding.WorkID
		target.roomIDs = appendDistinctStrings(target.roomIDs, binding.RoomID)
		targets[binding.SessionRef] = target
	}

	inbox, err := s.channelService.ListInbox(ctx, agent.ID, true)
	if err != nil {
		return errors.Join(dispatchErr, err)
	}
	for _, item := range inbox {
		// Public messages without an addressed delivery are passive context.
		// Processing another session's result must not turn them into new work.
		if item.Kind != channels.InboxTask && item.Kind != channels.InboxReminder {
			continue
		}
		if item.Kind == channels.InboxTask {
			if binding, found := byWork[item.MessageID]; found {
				target := targets[binding.SessionRef]
				target.binding = binding
				target.workID = binding.WorkID
				target.roomIDs = appendDistinctStrings(target.roomIDs, item.RoomID)
				targets[binding.SessionRef] = target
				continue
			}
		}
		conversationRooms[item.RoomID] = struct{}{}
	}
	for _, target := range targets {
		if err := s.startNamedAgentDispatchTargetLocked(ctx, agent, client, target, force); err != nil {
			dispatchErr = errors.Join(dispatchErr, err)
		}
	}
	for roomID := range conversationRooms {
		if err := s.startNamedAgentConversationLocked(ctx, agent, client, roomID, force); err != nil {
			dispatchErr = errors.Join(dispatchErr, err)
		}
	}
	if dispatchErr == nil && len(targets) == 0 && len(conversationRooms) == 0 {
		_, err := s.channelService.FinishWakeAttempt(ctx, agent.ID)
		dispatchErr = errors.Join(dispatchErr, err)
	}
	if dispatchErr != nil {
		_ = s.channelService.MarkWakePending(ctx, agent.ID)
	}
	return dispatchErr
}

func collaborationDispatchIsResult(kind channels.CollaborationKind) bool {
	switch kind {
	case channels.CollaborationPeerResult, channels.CollaborationCandidateReady,
		channels.CollaborationVerificationFeedback, channels.CollaborationCompletion, channels.CollaborationWorkRunTerminal:
		return true
	default:
		return false
	}
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
	return principalSessionID(agent.ID+"\x00room:"+roomID, agent.CreatedAt)
}

func (s *Server) startNamedAgentDispatchTargetLocked(ctx context.Context, agent channels.AgentRuntime, client *channels.AgentClient, target namedAgentDispatchTarget, force bool) (dispatchErr error) {
	defer func() {
		if dispatchErr != nil && target.workID == "" {
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
	if target.workID == "" {
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
	client, err := s.bindCollaborationPrincipal(ctx, agent, "")
	if err != nil {
		return err
	}
	bindings, err := client.ListCollaborationSessions(ctx, channels.CollaborationSessionListParams{PrincipalID: agent.ID})
	if err != nil {
		return err
	}
	var resumeErr error
	for _, binding := range bindings {
		if binding.State != channels.CollaborationSessionRunning && binding.State != channels.CollaborationSessionStarting {
			continue
		}
		if binding.RunID == "" || binding.WorkID == "" {
			if active, _ := session.ThreadExecutionActive(s.rt.SessionDir, binding.SessionRef); active {
				continue
			}
			if binding.TurnID != "" {
				if settleErr := s.settleCollaborationTurn(ctx, binding.SessionRef, binding.TurnID); settleErr == nil {
					continue
				}
			}
			_, stateErr := client.UpdateCollaborationSessionState(ctx, channels.CollaborationSessionStateParams{SessionRef: binding.SessionRef, State: channels.CollaborationSessionIdle})
			if stateErr == nil {
				_, stateErr = s.channelService.EnqueueSessionInput(ctx, channels.CollaborationSessionSendParams{SessionRef: binding.SessionRef, Body: "Execution was interrupted by host restart. Continue the existing objective from durable history; inspect completed effects before repeating actions.", RequestID: "recover:" + binding.SessionRef + ":" + binding.UpdatedAt.String()})
			}
			resumeErr = errors.Join(resumeErr, stateErr)
			continue
		}
		work, workErr := client.GetWork(ctx, binding.WorkID)
		if workErr != nil {
			_, stateErr := client.UpdateCollaborationSessionState(ctx, channels.CollaborationSessionStateParams{
				SessionRef: binding.SessionRef, State: channels.CollaborationSessionMissing,
			})
			resumeErr = errors.Join(resumeErr, workErr, stateErr)
			continue
		}
		var runState channels.WorkRunState
		for _, run := range work.Runs {
			if run.ID == binding.RunID {
				runState = run.State
				break
			}
		}
		if runState != channels.WorkRunRunning && runState != channels.WorkRunQueued {
			nextState := channels.CollaborationSessionIdle
			if runState == "" {
				nextState = channels.CollaborationSessionMissing
			} else if runState == channels.WorkRunInterrupted {
				nextState = channels.CollaborationSessionInterrupted
			}
			_, stateErr := client.UpdateCollaborationSessionState(ctx, channels.CollaborationSessionStateParams{
				SessionRef: binding.SessionRef, State: nextState,
			})
			resumeErr = errors.Join(resumeErr, stateErr)
			continue
		}
		th, ensureErr := s.ensureAgentRuntimeSessionThreadLocked(agent, binding.SessionRef)
		if ensureErr == nil && !threadIsRunning(th) {
			ensureErr = s.startAgentRuntimeSessionWakeLocked(agent, th, []string{binding.RoomID}, binding.WorkID, binding.RunID)
		}
		if ensureErr != nil {
			resumeErr = errors.Join(resumeErr, fmt.Errorf("resume session %q: %w", binding.SessionRef, ensureErr))
		}
	}
	return resumeErr
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
