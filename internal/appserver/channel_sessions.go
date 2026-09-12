package appserver

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/blueberrycongee/wuu/internal/channels"
	"github.com/blueberrycongee/wuu/internal/runtime"
	"github.com/blueberrycongee/wuu/internal/session"
)

const (
	MethodChannelSessionList   = "channel/session/list"
	MethodChannelSessionCreate = "channel/session/create"
	MethodChannelSessionRead   = "channel/session/read"
	MethodChannelSessionSend   = "channel/session/send"
	MethodChannelSessionStop   = "channel/session/stop"
	MethodChannelSessionResume = "channel/session/resume"
)

type ChannelSessionListParams struct {
	AgentID string `json:"agentId"`
	RoomID  string `json:"roomId"`
}
type ChannelSessionListResult struct {
	Sessions []channels.CollaborationSessionBinding `json:"sessions"`
}
type ChannelSessionCreateParams struct {
	AgentID   string `json:"agentId"`
	RoomID    string `json:"roomId"`
	Title     string `json:"title"`
	Prompt    string `json:"prompt"`
	Provider  string `json:"provider"`
	Model     string `json:"model"`
	Effort    string `json:"effort"`
	RequestID string `json:"requestId"`
}
type ChannelSessionRefParams struct {
	SessionRef string `json:"sessionRef"`
	Prompt     string `json:"prompt"`
	RequestID  string `json:"requestId"`
}
type ChannelSessionResult struct {
	Session channels.CollaborationSessionBinding `json:"session"`
}
type ChannelSessionReadResult struct {
	Session channels.CollaborationSessionBinding `json:"session"`
	Thread  Thread                               `json:"thread"`
}

func (s *Server) handleChannelSession(ctx context.Context, req Request) error {
	if s.channelService == nil || s.rt == nil {
		return s.writeResponse(req.ID, nil, errors.New("collaboration is unavailable"))
	}
	if req.Method == MethodChannelSessionList {
		var params ChannelSessionListParams
		if err := decodeParams(req.Params, &params); err != nil {
			return s.writeResponse(req.ID, nil, err)
		}
		all, err := s.channelService.ListAllCollaborationSessions(ctx)
		matches := make([]channels.CollaborationSessionBinding, 0)
		for _, binding := range all {
			if binding.NamedAgentID == "" {
				continue
			}
			if (params.AgentID == "" || binding.NamedAgentID == params.AgentID) && (params.RoomID == "" || binding.RoomID == params.RoomID) {
				matches = append(matches, binding)
			}
		}
		return s.writeResponse(req.ID, ChannelSessionListResult{Sessions: matches}, err)
	}
	if req.Method == MethodChannelSessionCreate {
		var params ChannelSessionCreateParams
		if err := decodeParams(req.Params, &params); err != nil {
			return s.writeResponse(req.ID, nil, err)
		}
		binding, err := s.channelService.CreateSession(ctx, channels.CollaborationSessionCreateParams{NamedAgentID: params.AgentID, RoomID: params.RoomID, Title: params.Title, Objective: params.Prompt, Provider: params.Provider, Model: params.Model, Effort: params.Effort, RequestID: params.RequestID})
		return s.writeResponse(req.ID, ChannelSessionResult{binding}, err)
	}
	var params ChannelSessionRefParams
	if err := decodeParams(req.Params, &params); err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	var binding channels.CollaborationSessionBinding
	var err error
	switch req.Method {
	case MethodChannelSessionRead:
		binding, err = s.channelService.LookupCollaborationSession(ctx, params.SessionRef)
		if err != nil {
			return s.writeResponse(req.ID, nil, err)
		}
		th, loadErr := s.ensureThreadLoaded(params.SessionRef)
		if loadErr != nil {
			return s.writeResponse(req.ID, nil, loadErr)
		}
		th.mu.Lock()
		snapshot := th.snapshotLocked()
		th.mu.Unlock()
		snapshot.ReadOnly = true
		return s.writeResponse(req.ID, ChannelSessionReadResult{binding, snapshot}, nil)
	case MethodChannelSessionSend:
		binding, err = s.channelService.SendSession(ctx, channels.CollaborationSessionSendParams{SessionRef: params.SessionRef, Body: params.Prompt, RequestID: params.RequestID})
	case MethodChannelSessionStop:
		binding, err = s.channelService.StopSession(ctx, channels.CollaborationSessionControlParams{SessionRef: params.SessionRef})
	case MethodChannelSessionResume:
		binding, err = s.channelService.ResumeSession(ctx, channels.CollaborationSessionControlParams{SessionRef: params.SessionRef})
	default:
		err = errors.New("unknown collaboration session method")
	}
	return s.writeResponse(req.ID, ChannelSessionResult{binding}, err)
}

// The channels service authenticates and reserves a stable handle before these
// controller methods run. The admission mutex protects local launch decisions,
// never model execution, so independent sessions execute concurrently.
func (s *Server) CreateSession(ctx context.Context, params channels.CollaborationSessionCreateParams) (channels.CollaborationSessionBinding, error) {
	s.namedAgentMu.Lock()
	defer s.namedAgentMu.Unlock()
	agent, err := s.channelService.GetAgentRuntime(ctx, params.NamedAgentID)
	if err != nil {
		return channels.CollaborationSessionBinding{}, err
	}
	client, err := s.channelService.BindAgent(ctx, agent.ID)
	if err != nil {
		return channels.CollaborationSessionBinding{}, err
	}
	binding, lookupErr := s.channelService.LookupCollaborationSession(ctx, params.SessionRef)
	if errors.Is(lookupErr, channels.ErrNotFound) {
		selection := s.currentSessionRuntimeSelection()
		selection = agentRuntimeSelection(selection, agent)
		if params.Provider != "" {
			selection.Provider = params.Provider
		}
		if params.Model != "" {
			selection.Model = params.Model
			selection.Variant = ""
		}
		if params.Effort != "" {
			selection.Effort = params.Effort
		}
		binding, err = client.BindCollaborationSession(ctx, channels.CollaborationSessionBindParams{SessionRef: params.SessionRef, RoomID: params.RoomID, Title: params.Title, Objective: params.Objective, ParentSessionRef: params.ParentSessionRef, Provider: selection.Provider, Model: selection.Model, Effort: firstNonEmpty(selection.Effort, selection.Variant), RuntimeVersion: runtime.CollaborationRuntimeVersion, Purpose: channels.CollaborationSessionWork, State: channels.CollaborationSessionIdle})
	} else {
		err = lookupErr
	}
	if err != nil {
		return binding, err
	}
	th, err := s.ensureAgentRuntimeSessionThreadLocked(agent, binding.SessionRef)
	if err != nil {
		s.failCollaborationSession(ctx, binding, err)
		return binding, err
	}
	if binding.Title != "" {
		th.mu.Lock()
		th.Title = binding.Title
		th.mu.Unlock()
		if _, err = session.UpdateTitle(s.rt.SessionDir, th.ID, binding.Title); err != nil {
			return binding, err
		}
	}
	_, err = s.channelService.EnqueueSessionInput(ctx, channels.CollaborationSessionSendParams{SessionRef: binding.SessionRef, Body: binding.Objective, RequestID: "session-initial:" + binding.SessionRef, ActorID: params.ActorID, SourceSessionRef: params.SourceSessionRef})
	if err != nil {
		return binding, err
	}
	if err = s.dispatchNamedAgentWakeLocked(ctx, agent, true); err != nil {
		return binding, err
	}
	return s.channelService.LookupCollaborationSession(ctx, binding.SessionRef)
}

func (s *Server) SendSession(ctx context.Context, params channels.CollaborationSessionSendParams) (channels.CollaborationSessionBinding, error) {
	s.namedAgentMu.Lock()
	defer s.namedAgentMu.Unlock()
	binding, err := s.channelService.LookupCollaborationSession(ctx, params.SessionRef)
	if err != nil {
		return binding, err
	}
	if binding.State == channels.CollaborationSessionCancelled || binding.State == channels.CollaborationSessionInterrupted || binding.State == channels.CollaborationSessionFailed {
		return binding, errors.New("session is stopped; resume it before sending more work")
	}
	if _, err = s.channelService.EnqueueSessionInput(ctx, params); err != nil {
		return binding, err
	}
	agent, err := s.channelService.GetAgentRuntime(ctx, binding.PrincipalID)
	if err != nil {
		return binding, err
	}
	if err = s.dispatchNamedAgentWakeLocked(ctx, agent, true); err != nil {
		return binding, err
	}
	return s.channelService.LookupCollaborationSession(ctx, binding.SessionRef)
}

func (s *Server) StopSession(ctx context.Context, params channels.CollaborationSessionControlParams) (channels.CollaborationSessionBinding, error) {
	s.namedAgentMu.Lock()
	defer s.namedAgentMu.Unlock()
	binding, err := s.channelService.LookupCollaborationSession(ctx, params.SessionRef)
	if err != nil {
		return binding, err
	}
	stopped, err := s.channelService.CancelCollaborationSessions(ctx, binding.SessionRef)
	if err != nil {
		return binding, err
	}
	// Persist cancellation and parent notifications before interrupting execution.
	// Retry every target even when one lease reset fails; the durable fence keeps
	// late callbacks from reviving any member of the cancelled subtree.
	var interruptErr error
	for _, target := range stopped {
		if th := s.thread(target.SessionRef); th != nil {
			_, err = s.interruptThreadExecution(th.ID, "", "")
		} else {
			_, err = session.RequestThreadExecutionReset(s.rt.SessionDir, target.SessionRef)
		}
		interruptErr = errors.Join(interruptErr, err)
	}
	s.drainCollaborationSessionsLocked(ctx)
	updated, lookupErr := s.channelService.LookupCollaborationSession(ctx, binding.SessionRef)
	return updated, errors.Join(interruptErr, lookupErr)
}

func (s *Server) ResumeSession(ctx context.Context, params channels.CollaborationSessionControlParams) (channels.CollaborationSessionBinding, error) {
	s.namedAgentMu.Lock()
	defer s.namedAgentMu.Unlock()
	binding, err := s.channelService.LookupCollaborationSession(ctx, params.SessionRef)
	if err != nil {
		return binding, err
	}
	if th := s.thread(binding.SessionRef); threadIsRunning(th) {
		return binding, nil
	}
	if binding.WorkID != "" {
		return binding, errors.New("resume the associated work item to preserve its goal and verification state")
	}
	if err = s.setCollaborationSessionState(ctx, binding, channels.CollaborationSessionIdle, ""); err != nil {
		return binding, err
	}
	_, err = s.channelService.EnqueueSessionInput(ctx, channels.CollaborationSessionSendParams{SessionRef: binding.SessionRef, Body: "Resume the current objective using the persisted context and current messages. Check existing results before repeating work.", RequestID: "resume:" + session.NewID(), ActorID: params.ActorID, SourceSessionRef: params.SourceSessionRef})
	if err != nil {
		return binding, err
	}
	agent, err := s.channelService.GetAgentRuntime(ctx, binding.PrincipalID)
	if err != nil {
		return binding, err
	}
	if err = s.dispatchNamedAgentWakeLocked(ctx, agent, true); err != nil {
		return binding, err
	}
	return s.channelService.LookupCollaborationSession(ctx, binding.SessionRef)
}

func (s *Server) setCollaborationSessionState(ctx context.Context, binding channels.CollaborationSessionBinding, state channels.CollaborationSessionState, reason string) error {
	var client *channels.AgentClient
	var err error
	agent, err := s.channelService.GetAgentRuntime(ctx, binding.PrincipalID)
	if err != nil {
		return err
	}
	client, err = s.channelService.BindAgent(ctx, agent.ID)
	if err != nil {
		return err
	}
	_, err = client.UpdateCollaborationSessionState(ctx, channels.CollaborationSessionStateParams{SessionRef: binding.SessionRef, State: state, FailureReason: reason})
	return err
}
func (s *Server) failCollaborationSession(ctx context.Context, binding channels.CollaborationSessionBinding, cause error) {
	if binding.WorkID != "" {
		_ = s.setCollaborationSessionState(ctx, binding, channels.CollaborationSessionFailed, cause.Error())
		return
	}
	current, err := s.channelService.LookupCollaborationSession(ctx, binding.SessionRef)
	if err != nil || current.State == channels.CollaborationSessionCancelled || current.State == channels.CollaborationSessionInterrupted {
		return
	}
	if th := s.thread(binding.SessionRef); threadIsRunning(th) {
		return
	}
	if active, _ := session.ThreadExecutionActive(s.rt.SessionDir, binding.SessionRef); active {
		return
	}
	agent, err := s.channelService.GetAgentRuntime(ctx, binding.PrincipalID)
	if err != nil {
		return
	}
	client, err := s.bindCollaborationPrincipal(ctx, agent, "")
	if err != nil {
		return
	}
	turnID := "admission:" + session.NewID()
	if _, err = client.UpdateCollaborationSessionState(ctx, channels.CollaborationSessionStateParams{SessionRef: binding.SessionRef, State: channels.CollaborationSessionRunning, TurnID: turnID}); err != nil {
		return
	}
	_, _ = s.channelService.SettleCollaborationSession(ctx, channels.CollaborationSessionSettleParams{SessionRef: binding.SessionRef, State: channels.CollaborationSessionFailed, TurnID: turnID, Result: cause.Error(), FailureReason: cause.Error()})
}
func (s *Server) drainCollaborationSessionsLocked(ctx context.Context) {
	if err := s.channelService.AdmitQueuedWorkRuns(ctx); err != nil {
		return
	}
	queued, err := s.channelService.ListQueuedCollaborationSessions(ctx)
	if err != nil {
		return
	}
	for _, binding := range queued {
		agent, err := s.channelService.GetAgentRuntime(ctx, binding.PrincipalID)
		if err != nil {
			continue
		}
		client, bindErr := s.bindCollaborationPrincipal(ctx, agent, "")
		if bindErr != nil {
			continue
		}
		if err = s.startNamedAgentDispatchTargetLocked(ctx, agent, client, namedAgentDispatchTarget{binding: binding, workID: binding.WorkID, roomIDs: []string{binding.RoomID}}, true); err != nil {
			s.failCollaborationSession(ctx, binding, fmt.Errorf("start queued session: %w", err))
		}
	}
}

func (s *Server) bindCollaborationPrincipal(ctx context.Context, agent channels.AgentRuntime, ref string) (*channels.AgentClient, error) {
	if agent.IsRoomRuntime() {
		if ref != "" {
			return s.channelService.BindRuntimeSession(ctx, agent.ID, ref)
		}
		return s.channelService.BindRuntime(ctx, agent.ID)
	}
	if ref != "" {
		return s.channelService.BindAgentSession(ctx, agent.ID, ref)
	}
	return s.channelService.BindAgent(ctx, agent.ID)
}

// Settlement is one channels transaction: state, result and parent wake commit
// together. A replay of an older turn cannot overwrite a newer admitted turn.
func (s *Server) settleCollaborationTurn(ctx context.Context, ref, turnID string) error {
	binding, err := s.channelService.LookupCollaborationSession(ctx, ref)
	if err != nil {
		return err
	}
	th := s.thread(ref)
	if th == nil {
		th, err = s.ensureThreadLoaded(ref)
		if err != nil {
			return err
		}
	}
	th.mu.Lock()
	var outcome *Turn
	for i := range th.Turns {
		if th.Turns[i].ID == turnID {
			copy := th.Turns[i]
			outcome = &copy
			break
		}
	}
	var result string
	if outcome != nil {
		for _, item := range outcome.Items {
			if item.Type == ThreadItemAgentMessage {
				result += item.Text + "\n"
			}
		}
	}
	th.mu.Unlock()
	if outcome == nil || outcome.Status == TurnStatusInProgress {
		return errors.New("session turn has no terminal result")
	}
	next := channels.CollaborationSessionCompleted
	if binding.Purpose == channels.CollaborationSessionConversation || binding.Purpose == channels.CollaborationSessionCoordination {
		next = channels.CollaborationSessionIdle
	}
	failure := ""
	if outcome.Status == TurnStatusInterrupted {
		next = channels.CollaborationSessionInterrupted
		// Reset interrupts one room turn while preserving the member's inbox
		// entrypoint. Explicit session stop is fenced by the cancelled state in
		// settlement and cannot be undone by this completion callback.
		if (binding.Purpose == channels.CollaborationSessionConversation || binding.Purpose == channels.CollaborationSessionCoordination) && binding.ParentSessionRef == "" {
			next = channels.CollaborationSessionIdle
		}
	}
	if outcome.Status == TurnStatusFailed {
		next = channels.CollaborationSessionFailed
		// A failed request must not disable a room member. The failed turn and
		// its reason stay visible; new input can start a later attempt.
		if (binding.Purpose == channels.CollaborationSessionConversation || binding.Purpose == channels.CollaborationSessionCoordination) && binding.ParentSessionRef == "" {
			next = channels.CollaborationSessionIdle
		}
	}
	if outcome.Error != nil {
		failure = outcome.Error.Message
	}
	publicReply := ""
	if outcome.Status == TurnStatusCompleted {
		agent, agentErr := s.channelService.GetAgentRuntime(ctx, binding.PrincipalID)
		if agentErr == nil && isRoomConversation(binding, agent) && !roomReplySent(*outcome, binding.RoomID) {
			publicReply = roomReplyText(*outcome, false)
		}
	}
	result = strings.TrimSpace(result)
	if result == "" {
		result = fmt.Sprintf("Session %s ended with status %s. %s", ref, outcome.Status, failure)
	}
	runes := []rune(result)
	if len(runes) > 12000 {
		result = string(runes[:12000]) + "\n[Result excerpt; inspect the session and shared artifacts for the remainder.]"
	}
	_, err = s.channelService.SettleCollaborationSession(ctx, channels.CollaborationSessionSettleParams{SessionRef: ref, TurnID: turnID, State: next, Result: result, FailureReason: failure, PublicReply: publicReply})
	return err
}
