package appserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/channels"
	wuucontext "github.com/blueberrycongee/wuu/internal/context"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/runtime"
	"github.com/blueberrycongee/wuu/internal/session"
	"github.com/blueberrycongee/wuu/internal/statepath"
	"github.com/blueberrycongee/wuu/internal/worktree"
)

type pluginTurnReference struct {
	PluginID  string
	RequestID string
	QueueID   string
}

const maxPluginSessionInspectTimeout = 15 * time.Minute

func clonePluginTurnReference(reference *pluginTurnReference) *pluginTurnReference {
	if reference == nil {
		return nil
	}
	cloned := *reference
	return &cloned
}

func (s *Server) notifyPluginTurnDiscarded(threadID string, entry queuedTurn, reason string) {
	reference := entry.snapshot.PluginTurn
	if reference == nil {
		return
	}
	s.notifyPluginTurnLifecycleAsync(reference.PluginID, pluginhost.AgentTurnLifecycleInput{
		RequestID: reference.RequestID, State: pluginhost.TurnLifecycleDiscarded,
		ThreadID: strings.TrimSpace(threadID), QueueID: reference.QueueID,
		Error: strings.TrimSpace(reason),
	})
}

func (s *Server) createPluginSession(ctx context.Context, pluginID string, params pluginhost.SessionCreateParams) (pluginhost.SessionCreateResult, error) {
	if s == nil || s.closed.Load() {
		return pluginhost.SessionCreateResult{}, errServerClosed
	}
	pluginID = strings.TrimSpace(pluginID)
	params.RequestID = strings.TrimSpace(params.RequestID)
	params.Name = strings.TrimSpace(params.Name)
	params.Visibility = strings.TrimSpace(params.Visibility)
	params.ParentSessionID = strings.TrimSpace(params.ParentSessionID)
	params.ContextSource = strings.TrimSpace(params.ContextSource)
	params.Workspace = strings.TrimSpace(params.Workspace)
	params.WorkspaceID = strings.TrimSpace(params.WorkspaceID)
	params.WorkspaceRoot = strings.TrimSpace(params.WorkspaceRoot)
	params.ModelAlias = strings.TrimSpace(params.ModelAlias)
	params.Instructions = strings.TrimSpace(params.Instructions)
	if pluginID == "" {
		return pluginhost.SessionCreateResult{}, errors.New("plugin owner is required")
	}
	if params.RequestID == "" {
		return pluginhost.SessionCreateResult{}, errors.New("request_id is required")
	}
	if len([]byte(params.RequestID)) > pluginhost.MaxSessionSendRequestIDBytes {
		return pluginhost.SessionCreateResult{}, fmt.Errorf("request_id exceeds %d bytes", pluginhost.MaxSessionSendRequestIDBytes)
	}
	if len([]byte(params.Instructions)) > pluginhost.MaxSessionSendContextBlockBytes {
		return pluginhost.SessionCreateResult{}, fmt.Errorf("instructions exceeds %d bytes", pluginhost.MaxSessionSendContextBlockBytes)
	}
	if params.Visibility != pluginhost.SessionVisibilityUser && params.Visibility != pluginhost.SessionVisibilityPlugin {
		return pluginhost.SessionCreateResult{}, errors.New("visibility must be user or plugin")
	}
	if params.ContextSource != pluginhost.SessionContextFresh && params.ContextSource != pluginhost.SessionContextFork && params.ContextSource != pluginhost.SessionContextSourceSeed {
		return pluginhost.SessionCreateResult{}, errors.New("context_source must be fresh, fork, or seed")
	}
	if params.ContextSource == pluginhost.SessionContextFork && params.ParentSessionID == "" {
		return pluginhost.SessionCreateResult{}, errors.New("parent_session_id is required for fork context")
	}
	if params.ContextSource == pluginhost.SessionContextSourceSeed {
		if params.Seed == nil {
			return pluginhost.SessionCreateResult{}, errors.New("seed is required for seed context")
		}
		if params.Instructions != "" {
			return pluginhost.SessionCreateResult{}, errors.New("seed initialization cannot use instructions")
		}
		if params.Provider == "" || params.Model == "" {
			return pluginhost.SessionCreateResult{}, errors.New("seed initialization requires provider and model")
		}
	} else if params.Seed != nil {
		return pluginhost.SessionCreateResult{}, errors.New("seed is only valid when context_source is seed")
	}
	if params.Workspace == "" {
		params.Workspace = "shared"
	}
	if params.Workspace != "shared" && params.Workspace != "worktree" {
		return pluginhost.SessionCreateResult{}, errors.New("workspace must be shared or worktree")
	}
	if params.ToolPolicy != nil {
		normalized, err := normalizeSessionToolPolicy(*params.ToolPolicy)
		if err != nil {
			return pluginhost.SessionCreateResult{}, err
		}
		params.ToolPolicy = &normalized
	}
	// A worktree session runs in its own git worktree based on the fork
	// parent's directory, or on the project root for fresh context.
	if params.WorkspaceID != "" && params.WorkspaceID != strings.TrimSpace(s.rt.WorkspaceID) {
		return pluginhost.SessionCreateResult{}, errors.New("target workspace is not served by this app-server")
	}
	if params.WorkspaceRoot != "" && filepath.Clean(params.WorkspaceRoot) != filepath.Clean(s.rt.RootDir) {
		return pluginhost.SessionCreateResult{}, errors.New("target workspace root is not served by this app-server")
	}
	owner := pluginSessionOwner(pluginID, params)
	if existing, ok, err := session.FindManagedByRequest(s.rt.SessionDir, owner, params.RequestID); err != nil {
		return pluginhost.SessionCreateResult{}, err
	} else if ok {
		if th, loadErr := s.ensureThreadLoaded(existing.ID); loadErr == nil && params.ContextSource == pluginhost.SessionContextSourceSeed {
			if err := s.dispatchHandoffLaunchTurn(th, params); err != nil {
				providers.DebugLogf("dispatch existing handoff first turn for %q: %v", existing.ID, err)
			}
		}
		return pluginhost.SessionCreateResult{SessionID: existing.ID, Created: false, WorkspaceRoot: existing.CWD}, nil
	}
	if launch, ok, err := session.LoadSessionLaunch(s.rt.SessionDir, params.RequestID); err != nil {
		return pluginhost.SessionCreateResult{}, err
	} else if ok && launch.TargetSession != "" {
		workspaceRoot := ""
		if th, loadErr := s.ensureThreadLoaded(launch.TargetSession); loadErr == nil {
			workspaceRoot = th.CWD
			if params.ContextSource == pluginhost.SessionContextSourceSeed {
				if err := s.dispatchHandoffLaunchTurn(th, params); err != nil {
					providers.DebugLogf("dispatch existing handoff first turn for %q: %v", launch.TargetSession, err)
				}
			}
		}
		return pluginhost.SessionCreateResult{SessionID: launch.TargetSession, Created: false, WorkspaceRoot: workspaceRoot}, nil
	}
	prepared, err := s.prepareHandoffSeed(ctx, params)
	if err != nil {
		return pluginhost.SessionCreateResult{}, err
	}
	params = prepared
	th, err := s.createPluginSessionThread(owner, params)
	if err != nil {
		if existing, ok, findErr := session.FindManagedByRequest(s.rt.SessionDir, owner, params.RequestID); findErr == nil && ok {
			return pluginhost.SessionCreateResult{SessionID: existing.ID, Created: false, WorkspaceRoot: existing.CWD}, nil
		}
		return pluginhost.SessionCreateResult{}, err
	}
	return pluginhost.SessionCreateResult{SessionID: th.ID, Created: true, WorkspaceRoot: th.CWD}, nil
}

func (s *Server) listPluginSessions(_ context.Context, pluginID string, params pluginhost.SessionListParams) (pluginhost.SessionListResult, error) {
	pluginID = strings.TrimSpace(pluginID)
	parentID := strings.TrimSpace(params.ParentSessionID)
	scope := strings.TrimSpace(params.Scope)
	if scope == "" {
		scope = pluginhost.SessionListScopeOwned
	}
	if pluginID == "" {
		return pluginhost.SessionListResult{}, errors.New("plugin owner is required")
	}
	if scope != pluginhost.SessionListScopeOwned && scope != pluginhost.SessionListScopeShared {
		return pluginhost.SessionListResult{}, errors.New("session list scope must be owned or shared")
	}
	items, err := session.List(s.rt.SessionDir, 0)
	if err != nil {
		return pluginhost.SessionListResult{}, err
	}
	owner := "plugin:" + pluginID
	result := pluginhost.SessionListResult{Sessions: make([]pluginhost.SessionSummary, 0)}
	for _, item := range items {
		if parentID != "" && item.ParentID != parentID {
			continue
		}
		if scope == pluginhost.SessionListScopeOwned && item.Owner != owner {
			continue
		}
		if scope == pluginhost.SessionListScopeShared && (item.Visibility == pluginhost.SessionVisibilityPlugin || item.ArchivedAt != nil) {
			continue
		}
		state := pluginhost.TurnLifecycleCompleted
		if th := s.thread(item.ID); th != nil {
			th.mu.Lock()
			if th.running {
				state = pluginhost.TurnLifecycleRunning
			}
			th.mu.Unlock()
		}
		s.queuedTurnMu.Lock()
		if len(s.pendingQueuedTurns[item.ID]) != 0 {
			state = pluginhost.TurnLifecycleQueued
		}
		s.queuedTurnMu.Unlock()
		result.Sessions = append(result.Sessions, pluginhost.SessionSummary{SessionID: item.ID, Name: item.Title, ParentSessionID: item.ParentID, Visibility: item.Visibility, State: state, WorkspaceID: item.WorkspaceID, CreatedAt: item.CreatedAt.Format(time.RFC3339Nano), UpdatedAt: item.UpdatedAt.Format(time.RFC3339Nano)})
	}
	return result, nil
}

func (s *Server) inspectPluginSession(ctx context.Context, pluginID string, params pluginhost.SessionInspectParams) (pluginhost.SessionInspectResult, error) {
	params.SessionID = strings.TrimSpace(params.SessionID)
	params.TurnID = strings.TrimSpace(params.TurnID)
	params.RequestID = strings.TrimSpace(params.RequestID)
	params.Wait = strings.TrimSpace(params.Wait)
	if params.Wait == "" {
		params.Wait = pluginhost.SessionInspectWaitNone
	}
	if params.SessionID == "" || strings.TrimSpace(pluginID) == "" {
		return pluginhost.SessionInspectResult{}, errors.New("plugin owner and session_id are required")
	}
	if params.Wait != pluginhost.SessionInspectWaitNone && params.Wait != pluginhost.SessionInspectWaitTerminal {
		return pluginhost.SessionInspectResult{}, errors.New("wait must be none or terminal")
	}
	maxTimeoutMS := int(maxPluginSessionInspectTimeout / time.Millisecond)
	if params.TimeoutMS < 0 || params.TimeoutMS > maxTimeoutMS {
		return pluginhost.SessionInspectResult{}, fmt.Errorf("timeout_ms must be between 0 and %d", maxTimeoutMS)
	}
	inspect := func() (pluginhost.SessionInspectResult, error) {
		return s.inspectPluginSessionOnce(pluginID, params)
	}
	result, err := inspect()
	if err != nil || params.Wait == pluginhost.SessionInspectWaitNone || result.Turn == nil || terminalPluginTurnState(result.Turn.State) {
		return result, err
	}
	timeout := time.Duration(params.TimeoutMS) * time.Millisecond
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	requestID := params.RequestID
	if requestID == "" {
		requestID = strings.TrimSpace(result.Turn.RequestID)
	}
	if requestID == "" {
		return pluginhost.SessionInspectResult{}, errors.New("terminal wait requires a resolvable request_id")
	}
	params.RequestID = requestID
	ready, unsubscribe := s.pluginTurnWaiters.subscribe(pluginID, requestID)
	defer unsubscribe()

	// Re-inspect after registering to close the race where terminal state is
	// persisted between the initial read and waiter registration.
	result, err = inspect()
	if err != nil || result.Turn == nil || terminalPluginTurnState(result.Turn.State) {
		return result, err
	}

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return pluginhost.SessionInspectResult{}, ctx.Err()
	case <-ready:
		return inspect()
	case <-timer.C:
		// Prefer a terminal result that landed on the timeout boundary. Otherwise
		// return the freshest non-terminal snapshot with TimedOut set.
		result, err = inspect()
		if err != nil || result.Turn == nil || terminalPluginTurnState(result.Turn.State) {
			return result, err
		}
		result.TimedOut = true
		return result, nil
	}
}

func (s *Server) inspectPluginSessionOnce(pluginID string, params pluginhost.SessionInspectParams) (pluginhost.SessionInspectResult, error) {
	metadata, ok, err := session.Find(s.rt.SessionDir, params.SessionID)
	if err != nil {
		return pluginhost.SessionInspectResult{}, err
	}
	if !ok {
		return pluginhost.SessionInspectResult{}, session.ErrSessionNotFound
	}
	if metadata.Owner != "plugin:"+strings.TrimSpace(pluginID) {
		// Shared-session senders may inspect their own correlated request. Goal
		// continuations and automation heartbeats both need recovery here.
		if metadata.Visibility == pluginhost.SessionVisibilityPlugin || params.RequestID == "" {
			return pluginhost.SessionInspectResult{}, errors.New("shared session inspection requires a plugin request_id")
		}
	}
	th, err := s.ensureThreadLoaded(params.SessionID)
	if err != nil {
		return pluginhost.SessionInspectResult{}, err
	}
	requestID := params.RequestID
	if requestID == "" {
		requestID = latestPluginRequestID(th, pluginID, params.TurnID)
	}
	state := pluginhost.TurnLifecycleCompleted
	if th != nil {
		th.mu.Lock()
		if th.running {
			state = pluginhost.TurnLifecycleRunning
		}
		th.mu.Unlock()
	}
	s.queuedTurnMu.Lock()
	if len(s.pendingQueuedTurns[params.SessionID]) > 0 {
		state = pluginhost.TurnLifecycleQueued
	}
	s.queuedTurnMu.Unlock()
	result := pluginhost.SessionInspectResult{Session: pluginhost.SessionSummary{
		SessionID: metadata.ID, Name: metadata.Title, ParentSessionID: metadata.ParentID,
		Visibility: metadata.Visibility, State: state, WorkspaceID: metadata.WorkspaceID,
		CreatedAt: metadata.CreatedAt.Format(time.RFC3339Nano), UpdatedAt: metadata.UpdatedAt.Format(time.RFC3339Nano),
	}}
	if metadata.WorktreePath != "" {
		summary := &pluginhost.SessionWorkspaceSummary{Kind: "worktree"}
		if manager, managerErr := s.worktreeManager(metadata.WorktreeBaseRepo); managerErr == nil {
			if status, statusErr := manager.Status(metadata.WorktreePath); statusErr == nil {
				summary.Dirty = status.Dirty
				summary.ChangedFiles = status.ChangedFiles
			}
		}
		result.Workspace = summary
	} else {
		result.Workspace = &pluginhost.SessionWorkspaceSummary{Kind: "shared"}
	}
	entry, found, err := session.FindPluginTurnLifecycle(s.rt.SessionDir, pluginID, requestID, params.SessionID, params.TurnID)
	if err != nil {
		return pluginhost.SessionInspectResult{}, err
	}
	if found {
		var lifecycle pluginhost.AgentTurnLifecycleInput
		if err := json.Unmarshal(entry.Payload, &lifecycle); err != nil {
			return pluginhost.SessionInspectResult{}, fmt.Errorf("decode plugin lifecycle state: %w", err)
		}
		result.Turn = &pluginhost.SessionTurnInspection{
			RequestID: lifecycle.RequestID, State: lifecycle.State, TurnID: lifecycle.TurnID, QueueID: lifecycle.QueueID,
			Error: lifecycle.Error, StartedAt: lifecycle.StartedAt, CompletedAt: lifecycle.CompletedAt,
			InputTokens: lifecycle.InputTokens, OutputTokens: lifecycle.OutputTokens, FinalOutput: lifecycle.FinalOutput,
		}
		result.Session.State = lifecycle.State
		return result, nil
	}
	if requestID != "" {
		if live, ok := s.findPluginSessionRequest(th, pluginSessionRequestClientID(pluginID, requestID)); ok {
			if params.TurnID == "" || live.TurnID == params.TurnID {
				result.Turn = &pluginhost.SessionTurnInspection{RequestID: requestID, State: live.State, TurnID: live.TurnID, QueueID: live.QueueID}
				result.Session.State = live.State
			}
		}
	}
	return result, nil
}

func latestPluginRequestID(th *threadState, pluginID, turnID string) string {
	if th == nil {
		return ""
	}
	th.mu.Lock()
	defer th.mu.Unlock()
	for turnIndex := len(th.Turns) - 1; turnIndex >= 0; turnIndex-- {
		turn := th.Turns[turnIndex]
		if turnID != "" && turn.ID != turnID {
			continue
		}
		for itemIndex := len(turn.Items) - 1; itemIndex >= 0; itemIndex-- {
			owner, requestID, ok := pluginSessionRequestFromClientID(turn.Items[itemIndex].SourceID)
			if ok && owner == pluginID {
				return requestID
			}
		}
	}
	if turnID != "" {
		return ""
	}
	for index := len(th.History) - 1; index >= 0; index-- {
		owner, requestID, ok := pluginSessionRequestFromClientID(th.History[index].ClientID)
		if ok && owner == pluginID {
			return requestID
		}
	}
	return ""
}

func terminalPluginTurnState(state string) bool {
	switch strings.TrimSpace(state) {
	case pluginhost.TurnLifecycleCompleted, pluginhost.TurnLifecycleFailed, pluginhost.TurnLifecycleInterrupted, pluginhost.TurnLifecycleDiscarded:
		return true
	default:
		return false
	}
}

func (s *Server) statusPluginWorkspace(_ context.Context, pluginID string, params pluginhost.WorkspaceStatusParams) (pluginhost.WorkspaceStatusResult, error) {
	metadata, manager, target, err := s.pluginWorkspaceTarget(pluginID, params.SessionID)
	if err != nil {
		return pluginhost.WorkspaceStatusResult{}, err
	}
	status, err := manager.Status(target)
	if err != nil {
		return pluginhost.WorkspaceStatusResult{}, err
	}
	diff, err := manager.Diff(target)
	if err != nil {
		return pluginhost.WorkspaceStatusResult{}, err
	}
	preview := manager.MergePreview(target, metadata.WorktreeBaseRepo)
	return pluginhost.WorkspaceStatusResult{
		SessionID: metadata.ID, Dirty: status.Dirty, ChangedFiles: status.ChangedFiles,
		Porcelain: status.Porcelain, Diff: diff, CanApply: preview.CanApply,
		ConflictFiles: preview.ConflictFiles, Error: preview.Error,
	}, nil
}

func (s *Server) applyPluginWorkspace(_ context.Context, pluginID string, params pluginhost.WorkspaceApplyParams) (pluginhost.WorkspaceApplyResult, error) {
	metadata, manager, target, err := s.pluginWorkspaceTarget(pluginID, params.SessionID)
	if err != nil {
		return pluginhost.WorkspaceApplyResult{}, err
	}
	if err := s.requirePluginWorkspaceIdle(metadata.ID); err != nil {
		return pluginhost.WorkspaceApplyResult{}, err
	}
	applied, err := manager.ApplyToTarget(target, metadata.WorktreeBaseRepo)
	if err != nil {
		return pluginhost.WorkspaceApplyResult{}, err
	}
	if err := manager.Cleanup(target); err != nil {
		return pluginhost.WorkspaceApplyResult{}, err
	}
	if err := s.rebindThreadWorkspace(metadata.ID, metadata.WorktreeBaseRepo); err != nil {
		return pluginhost.WorkspaceApplyResult{}, err
	}
	return pluginhost.WorkspaceApplyResult{SessionID: metadata.ID, Applied: applied.Applied, ChangedFiles: applied.ChangedFiles, Discarded: true}, nil
}

func (s *Server) discardPluginWorkspace(_ context.Context, pluginID string, params pluginhost.WorkspaceDiscardParams) (pluginhost.WorkspaceDiscardResult, error) {
	metadata, manager, target, err := s.pluginWorkspaceTarget(pluginID, params.SessionID)
	if err != nil {
		return pluginhost.WorkspaceDiscardResult{}, err
	}
	if err := s.requirePluginWorkspaceIdle(metadata.ID); err != nil {
		return pluginhost.WorkspaceDiscardResult{}, err
	}
	if err := manager.Cleanup(target); err != nil {
		return pluginhost.WorkspaceDiscardResult{}, err
	}
	if err := s.rebindThreadWorkspace(metadata.ID, metadata.WorktreeBaseRepo); err != nil {
		return pluginhost.WorkspaceDiscardResult{}, err
	}
	return pluginhost.WorkspaceDiscardResult{SessionID: metadata.ID, Discarded: true}, nil
}

func (s *Server) pluginWorkspaceTarget(pluginID, sessionID string) (session.Session, *worktree.Manager, *worktree.Worktree, error) {
	pluginID = strings.TrimSpace(pluginID)
	sessionID = strings.TrimSpace(sessionID)
	if pluginID == "" || sessionID == "" {
		return session.Session{}, nil, nil, errors.New("plugin owner and session_id are required")
	}
	metadata, ok, err := session.Find(s.rt.SessionDir, sessionID)
	if err != nil {
		return session.Session{}, nil, nil, err
	}
	if !ok {
		return session.Session{}, nil, nil, session.ErrSessionNotFound
	}
	if metadata.Owner != "plugin:"+pluginID {
		return session.Session{}, nil, nil, errors.New("plugin does not own the session")
	}
	if strings.TrimSpace(metadata.WorktreePath) == "" || strings.TrimSpace(metadata.WorktreeBaseRepo) == "" {
		return session.Session{}, nil, nil, errors.New("session does not own an isolated workspace")
	}
	manager, err := s.worktreeManager(metadata.WorktreeBaseRepo)
	if err != nil {
		return session.Session{}, nil, nil, err
	}
	target := &worktree.Worktree{
		Path: metadata.WorktreePath, SessionID: metadata.ID, WorkerID: "plugin",
		HEAD: metadata.WorktreeBaseHEAD, BaseRepo: metadata.WorktreeBaseRepo,
	}
	return metadata, manager, target, nil
}

func (s *Server) requirePluginWorkspaceIdle(sessionID string) error {
	th := s.thread(sessionID)
	if th == nil {
		return nil
	}
	th.mu.Lock()
	defer th.mu.Unlock()
	if th.running || th.admissionReserved {
		return errors.New("session workspace cannot be changed while a turn is running")
	}
	return nil
}

func (s *Server) cancelPluginSession(_ context.Context, pluginID string, params pluginhost.SessionCancelParams) (pluginhost.SessionCancelResult, error) {
	pluginID = strings.TrimSpace(pluginID)
	sessionID := strings.TrimSpace(params.SessionID)
	turnID := strings.TrimSpace(params.TurnID)
	queueID := strings.TrimSpace(params.QueueID)
	if pluginID == "" || sessionID == "" {
		return pluginhost.SessionCancelResult{}, errors.New("plugin owner and session_id are required")
	}
	if turnID != "" && queueID != "" {
		return pluginhost.SessionCancelResult{}, errors.New("turn_id and queue_id are mutually exclusive")
	}
	metadata, ok, err := session.Find(s.rt.SessionDir, sessionID)
	if err != nil {
		return pluginhost.SessionCancelResult{}, err
	}
	if !ok {
		return pluginhost.SessionCancelResult{}, session.ErrSessionNotFound
	}
	if metadata.Owner != "plugin:"+pluginID {
		if metadata.Visibility == pluginhost.SessionVisibilityPlugin || (turnID == "" && queueID == "") {
			return pluginhost.SessionCancelResult{}, errors.New("shared session cancellation requires an owned turn_id or queue_id")
		}
		if turnID != "" {
			th, err := s.ensureThreadLoaded(sessionID)
			if err != nil {
				return pluginhost.SessionCancelResult{}, err
			}
			requestID := latestPluginRequestID(th, pluginID, turnID)
			sent, found := s.findPluginSessionRequest(th, pluginSessionRequestClientID(pluginID, requestID))
			if requestID == "" || !found || sent.TurnID != turnID || sent.Steered {
				return pluginhost.SessionCancelResult{}, errors.New("plugin does not own the turn")
			}
		}
	}
	if queueID != "" {
		entry, ok := s.removePluginQueuedTurn(sessionID, pluginID, queueID)
		if !ok {
			return pluginhost.SessionCancelResult{SessionID: sessionID, QueueID: queueID, Cancelled: false}, nil
		}
		s.notifyPluginTurnDiscarded(sessionID, entry, "queued plugin turn was cancelled")
		s.notifyQueuedTurnsDequeued(sessionID, []string{queueID})
		return pluginhost.SessionCancelResult{SessionID: sessionID, QueueID: queueID, Cancelled: true}, nil
	}
	if _, err := s.ensureThreadLoaded(sessionID); err != nil {
		return pluginhost.SessionCancelResult{}, err
	}
	cancelled, err := s.interruptThreadExecution(sessionID, "", turnID)
	if err != nil {
		return pluginhost.SessionCancelResult{}, err
	}
	return pluginhost.SessionCancelResult{SessionID: sessionID, TurnID: turnID, Cancelled: cancelled}, nil
}

func (s *Server) removePluginQueuedTurn(threadID, pluginID, queueID string) (queuedTurn, bool) {
	if s == nil {
		return queuedTurn{}, false
	}
	s.queuedTurnMu.Lock()
	defer s.queuedTurnMu.Unlock()
	pending := s.pendingQueuedTurns[threadID]
	for index, entry := range pending {
		if entry.id != queueID || entry.snapshot.PluginTurn == nil || entry.snapshot.PluginTurn.PluginID != pluginID {
			continue
		}
		remaining := append(append([]queuedTurn(nil), pending[:index]...), pending[index+1:]...)
		if len(remaining) == 0 {
			delete(s.pendingQueuedTurns, threadID)
		} else {
			s.pendingQueuedTurns[threadID] = remaining
		}
		return entry, true
	}
	return queuedTurn{}, false
}

func (s *Server) sendPluginSession(ctx context.Context, pluginID string, params pluginhost.SessionSendParams) (pluginhost.SessionSendResult, error) {
	if s == nil || s.closed.Load() {
		return pluginhost.SessionSendResult{}, errServerClosed
	}
	pluginID = strings.TrimSpace(pluginID)
	params.RequestID = strings.TrimSpace(params.RequestID)
	params.SessionID = strings.TrimSpace(params.SessionID)
	params.Input.Prompt = strings.TrimSpace(params.Input.Prompt)
	params.Cause = strings.TrimSpace(params.Cause)
	params.IfRunning = strings.TrimSpace(params.IfRunning)
	if pluginID == "" || params.RequestID == "" || params.SessionID == "" || params.Input.Prompt == "" {
		return pluginhost.SessionSendResult{}, errors.New("plugin owner, request_id, session_id, and input.prompt are required")
	}
	if params.IfRunning == "" {
		params.IfRunning = pluginhost.SessionIfRunningQueue
	}
	if params.IfRunning != pluginhost.SessionIfRunningQueue && params.IfRunning != pluginhost.SessionIfRunningSteer {
		return pluginhost.SessionSendResult{}, errors.New("if_running must be queue or steer")
	}
	if params.IfRunning == pluginhost.SessionIfRunningSteer && len(params.Input.ContextBlocks) > 0 {
		return pluginhost.SessionSendResult{}, errors.New("context_blocks cannot be used when if_running is steer")
	}
	if len([]byte(params.RequestID)) > pluginhost.MaxSessionSendRequestIDBytes {
		return pluginhost.SessionSendResult{}, fmt.Errorf("request_id exceeds %d bytes", pluginhost.MaxSessionSendRequestIDBytes)
	}
	metadata, ok, err := session.Find(s.rt.SessionDir, params.SessionID)
	if err != nil {
		return pluginhost.SessionSendResult{}, err
	}
	if !ok {
		return pluginhost.SessionSendResult{}, session.ErrSessionNotFound
	}
	owner := "plugin:" + pluginID
	if metadata.Visibility == pluginhost.SessionVisibilityPlugin && metadata.Owner != owner {
		return pluginhost.SessionSendResult{}, errors.New("plugin does not own the private session")
	}
	requestContext, err := pluginTurnRequestContext(params.Input.ContextBlocks)
	if err != nil {
		return pluginhost.SessionSendResult{}, err
	}
	th, err := s.ensureThreadLoaded(params.SessionID)
	if err != nil {
		return pluginhost.SessionSendResult{}, err
	}
	clientID := pluginSessionRequestClientID(pluginID, params.RequestID)
	if existing, ok := s.findPluginSessionRequest(th, clientID); ok {
		return existing, nil
	}
	msg, err := literalUserMessageFromPrompt(params.Input.Prompt, nil, nil)
	if err != nil {
		return pluginhost.SessionSendResult{}, err
	}
	msg.ClientID = clientID
	msg.Origin = pluginhost.SessionInputPlugin
	msg.OriginID = pluginID
	msg.Cause = params.Cause
	msg.PresentationKind = pluginhost.SessionPresentationQueryBubble
	msg.ReadOnly = true
	msg.DisplayContent = "插件已唤醒 Agent"
	if params.Presentation != nil {
		if kind := strings.TrimSpace(params.Presentation.Kind); kind != "" && kind != pluginhost.SessionPresentationQueryBubble {
			return pluginhost.SessionSendResult{}, errors.New("presentation.kind must be query_bubble")
		}
		if text := strings.TrimSpace(params.Presentation.Text); text != "" {
			msg.DisplayContent = text
		}
		msg.Name = strings.TrimSpace(params.Presentation.Name)
		msg.RelatedSessionID = strings.TrimSpace(params.Presentation.RelatedSessionID)
		if msg.RelatedSessionID != "" {
			related, exists, findErr := session.Find(s.rt.SessionDir, msg.RelatedSessionID)
			if findErr != nil {
				return pluginhost.SessionSendResult{}, findErr
			}
			if !exists || related.Owner != owner {
				return pluginhost.SessionSendResult{}, errors.New("related_session_id must name a session owned by the plugin")
			}
		}
	}
	if s.channelService != nil {
		binding, err := s.channelService.LookupCollaborationSession(ctx, params.SessionID)
		if err == nil && binding.Primary {
			if strings.TrimSpace(params.ReplyToTurnID) == "" || msg.RelatedSessionID == "" {
				return pluginhost.SessionSendResult{}, errors.New("a collaboration result requires reply_to_turn_id and its related child session")
			}
			child, found, err := session.Find(s.rt.SessionDir, msg.RelatedSessionID)
			if err != nil {
				return pluginhost.SessionSendResult{}, err
			}
			if !found || child.Owner != owner || child.ParentID != params.SessionID {
				return pluginhost.SessionSendResult{}, errors.New("result source must be the plugin's child of this conversation")
			}
			body := params.Input.Prompt
			for _, block := range params.Input.ContextBlocks {
				body += "\n\n" + block.Content
			}
			delivery, err := s.channelService.EnqueueSessionResult(ctx, channels.SessionResultEnqueueParams{
				ParentSessionRef: params.SessionID, ParentTurnID: params.ReplyToTurnID, SourceSessionRef: child.ID,
				RequestID: clientID, Body: body,
			})
			if err != nil {
				return pluginhost.SessionSendResult{}, err
			}
			if delivery.Discarded {
				return pluginhost.SessionSendResult{State: pluginhost.TurnLifecycleDiscarded, SessionID: params.SessionID}, nil
			}
			return pluginhost.SessionSendResult{State: pluginhost.TurnLifecycleQueued, SessionID: params.SessionID, QueueID: delivery.Message.ID}, nil
		}
		if err != nil && !errors.Is(err, channels.ErrNotFound) {
			return pluginhost.SessionSendResult{}, err
		}
	}
	if params.IfRunning == pluginhost.SessionIfRunningSteer {
		if turnID, steered := s.steerPluginSession(th, msg); steered {
			return pluginhost.SessionSendResult{
				State: pluginhost.TurnLifecycleRunning, SessionID: th.ID, TurnID: turnID, Steered: true,
			}, nil
		}
	}
	permissions, err := s.resolveThreadTurnPermissions(th, nil)
	if err != nil {
		return pluginhost.SessionSendResult{}, err
	}
	snapshot := turnRuntimeSnapshot{}.withPermissions(permissions)
	snapshot.RequestContext = requestContext
	snapshot.PluginTurn = &pluginTurnReference{PluginID: pluginID, RequestID: params.RequestID}

	started, ok, err := s.startPluginSubmittedTurn(ctx, th, msg, snapshot)
	if err != nil {
		return pluginhost.SessionSendResult{}, err
	}
	if ok {
		return pluginhost.SessionSendResult{State: pluginhost.TurnLifecycleRunning, SessionID: th.ID, TurnID: started.turnID}, nil
	}

	queueID := session.NewID()
	snapshot.PluginTurn.QueueID = queueID
	entry := queuedTurn{id: queueID, msg: msg, snapshot: snapshot}
	s.enqueueQueuedUserTurn(th.ID, entry)
	queued := queuedTurnSummary(th.ID, entry)
	_ = s.writeNotification(NotificationTurnQueued, TurnQueuedNotification{Queued: queued})
	s.kickQueuedTurnDrain(th.ID)
	return pluginhost.SessionSendResult{State: pluginhost.TurnLifecycleQueued, SessionID: th.ID, QueueID: queueID}, nil
}

func pluginSessionRequestClientID(pluginID, requestID string) string {
	return "plugin:" + strings.TrimSpace(pluginID) + ":" + strings.TrimSpace(requestID)
}

func pluginSessionRequestFromClientID(clientID string) (string, string, bool) {
	trimmed := strings.TrimSpace(clientID)
	rest := strings.TrimPrefix(trimmed, "plugin:")
	if rest == trimmed {
		return "", "", false
	}
	pluginID, requestID, ok := strings.Cut(rest, ":")
	pluginID = strings.TrimSpace(pluginID)
	requestID = strings.TrimSpace(requestID)
	return pluginID, requestID, ok && pluginID != "" && requestID != ""
}

func (s *Server) steerPluginSession(th *threadState, msg providers.ChatMessage) (string, bool) {
	if th == nil {
		return "", false
	}
	th.mu.Lock()
	defer th.mu.Unlock()
	if !th.running || th.currentTurn == "" || th.currentTurnKind == TurnKindCompact || th.interrupting {
		return "", false
	}
	for _, existing := range th.pendingSteers {
		if existing.ClientID == msg.ClientID {
			return th.currentTurn, true
		}
	}
	msg.Steered = true
	th.pendingSteers = append(th.pendingSteers, msg)
	th.signalSteerWakeLocked()
	return th.currentTurn, true
}

func (s *Server) findPluginSessionRequest(th *threadState, clientID string) (pluginhost.SessionSendResult, bool) {
	if th == nil || strings.TrimSpace(clientID) == "" {
		return pluginhost.SessionSendResult{}, false
	}
	th.mu.Lock()
	for _, pending := range th.pendingSteers {
		if pending.ClientID == clientID {
			result := pluginhost.SessionSendResult{
				State: pluginhost.TurnLifecycleRunning, SessionID: th.ID, TurnID: th.currentTurn, Steered: true,
			}
			th.mu.Unlock()
			return result, true
		}
	}
	steered := false
	for _, message := range th.History {
		if message.ClientID == clientID && message.Steered {
			steered = true
			break
		}
	}
	for _, turn := range th.Turns {
		for _, item := range turn.Items {
			if item.Type != ThreadItemUserMessage || item.SourceID != clientID {
				continue
			}
			state := pluginhost.TurnLifecycleCompleted
			if turn.Status == TurnStatusInProgress {
				state = pluginhost.TurnLifecycleRunning
			}
			result := pluginhost.SessionSendResult{State: state, SessionID: th.ID, TurnID: turn.ID, Steered: steered}
			th.mu.Unlock()
			return result, true
		}
	}
	th.mu.Unlock()

	s.queuedTurnMu.Lock()
	defer s.queuedTurnMu.Unlock()
	for _, entry := range s.pendingQueuedTurns[th.ID] {
		if entry.msg.ClientID == clientID {
			return pluginhost.SessionSendResult{State: pluginhost.TurnLifecycleQueued, SessionID: th.ID, QueueID: entry.id}, true
		}
	}
	return pluginhost.SessionSendResult{}, false
}

func (s *Server) createPluginSessionThread(owner string, params pluginhost.SessionCreateParams) (*threadState, error) {
	if s.rt == nil || s.rt.StreamRunner == nil {
		return nil, errors.New("runtime session is required")
	}
	id := session.NewID()
	threadCWD := s.rt.RootDir
	managed := session.ManagedMetadata{Owner: owner, Visibility: params.Visibility, ParentID: params.ParentSessionID, ContextSource: params.ContextSource, CreationRequestID: params.RequestID}
	var history []providers.ChatMessage
	var createdWorktreePath string
	cleanupWorktree := false
	fork := session.ForkMetadata{}
	if params.ContextSource == pluginhost.SessionContextFork {
		parent, loadErr := s.loadPersistedThreadSnapshot(params.ParentSessionID)
		if loadErr != nil {
			return nil, loadErr
		}
		threadCWD = firstNonEmpty(parent.metadata.CWD, threadCWD)
		history = cloneForkHistory(parent.history)
		fork = session.ForkMetadata{ForkedFromID: params.ParentSessionID}
	}
	if params.ContextSource == pluginhost.SessionContextSourceSeed && params.ParentSessionID != "" {
		parent, loadErr := s.loadPersistedThreadSnapshot(params.ParentSessionID)
		if loadErr != nil {
			return nil, loadErr
		}
		threadCWD = firstNonEmpty(parent.metadata.CWD, threadCWD)
	}
	selection := s.currentSessionRuntimeSelection()
	if params.ParentSessionID != "" {
		parent, found, err := session.Find(s.rt.SessionDir, params.ParentSessionID)
		if err != nil {
			return nil, err
		}
		if !found {
			return nil, session.ErrSessionNotFound
		}
		selection = runtimeSelectionFromSession(parent)
		threadCWD = firstNonEmpty(parent.CWD, threadCWD)
	}
	if params.Provider != "" || params.Model != "" {
		selection.Provider = params.Provider
		selection.Model = params.Model
		selection.Variant = params.Variant
		selection.Effort = params.Effort
		if params.PermissionMode != "" {
			selection.PermissionMode = params.PermissionMode
		}
	}
	if params.ModelAlias != "" {
		resolved := s.resolveSubagentModelAlias(params.ModelAlias)
		if resolved.Err != nil {
			return nil, resolved.Err
		}
		if !resolved.Found {
			return nil, fmt.Errorf("unknown model alias %q (available: %s)", params.ModelAlias, strings.Join(resolved.ValidAliases, ", "))
		}
		selection.Provider = resolved.Runtime.Provider
		selection.Model = resolved.Runtime.Model
		selection.Variant = resolved.Runtime.Variant
		selection.Effort = resolved.Runtime.Effort
	}
	source := pluginSessionSource(owner, params)
	workspaceID := strings.TrimSpace(s.rt.WorkspaceID)
	if len(history) == 0 {
		history = make([]providers.ChatMessage, 0, 1)
	}
	if params.ContextSource != pluginhost.SessionContextSourceSeed {
		if prompt := strings.TrimSpace(s.rt.StreamRunner.SystemPrompt); prompt != "" && len(history) == 0 {
			history = append(history, providers.ChatMessage{Role: "system", Content: prompt})
		}
		if params.Instructions != "" {
			history = applyPluginSessionInstructions(history, params.Instructions)
		}
	}
	toolPolicyJSON := ""
	if params.ToolPolicy != nil {
		encoded, err := json.Marshal(params.ToolPolicy)
		if err != nil {
			return nil, fmt.Errorf("encode tool_policy: %w", err)
		}
		toolPolicyJSON = string(encoded)
	}

	worktree := session.WorktreeInfo{}
	if params.Workspace == "worktree" {
		baseRepo := threadCWD
		manager, err := s.worktreeManager(baseRepo)
		if err != nil {
			return nil, err
		}
		createdWorktree, err := manager.Create(id, "plugin", "")
		if err != nil {
			return nil, err
		}
		createdWorktreePath = createdWorktree.Path
		cleanupWorktree = true
		threadCWD = createdWorktree.Path
		worktree = session.WorktreeInfo{Path: createdWorktree.Path, BaseHEAD: createdWorktree.HEAD, BaseRepo: baseRepo}
		defer func() {
			if cleanupWorktree {
				_ = manager.Cleanup(createdWorktree)
			}
		}()
	}
	initial := session.Session{
		ID: id, Title: params.Name, CWD: threadCWD,
		WorkspaceID: workspaceID, Source: source,
		Owner: managed.Owner, Visibility: managed.Visibility, ParentID: managed.ParentID,
		ContextSource: managed.ContextSource, CreationRequestID: managed.CreationRequestID,
		ForkedFromID: fork.ForkedFromID,
		WorktreePath: worktree.Path, WorktreeBaseHEAD: worktree.BaseHEAD, WorktreeBaseRepo: worktree.BaseRepo,
		Provider: selection.Provider, Model: selection.Model, Variant: selection.Variant,
		Effort: selection.Effort, PermissionMode: selection.PermissionMode,
		Instructions: params.Instructions, ToolPolicyJSON: toolPolicyJSON,
	}
	var records []session.HistoryRecord
	artifactStateDir := ""
	if params.ContextSource == pluginhost.SessionContextFork {
		stateDir, stateErr := s.workspaceStateDir()
		if stateErr != nil {
			return nil, stateErr
		}
		artifactStateDir = stateDir
		if err := preserveForkArtifacts(artifactStateDir, params.ParentSessionID, id, history); err != nil {
			_ = os.RemoveAll(statepath.SessionArtifactDir(artifactStateDir, id))
			return nil, err
		}
		records = historyRecordsFromChatMessages(history)
	}
	var seed session.ContextSeed
	var launch session.SessionLaunchRecord
	if params.ContextSource == pluginhost.SessionContextSourceSeed {
		seed = contextSeedFromParams(*params.Seed)
		launch = session.SessionLaunchRecord{
			RequestID: params.RequestID, Revision: 1, Kind: session.SessionLaunchKindHandoff,
			SourceSession: seed.Source.SessionID, SourceCutoff: seed.Source.ThroughSeq,
			Owner: owner, Producer: seed.Provenance.Producer,
			Runtime: session.SessionRuntimeSelection{Provider: selection.Provider, Model: selection.Model, Variant: selection.Variant, Effort: selection.Effort, PermissionMode: selection.PermissionMode},
		}
		if params.Launch != nil {
			if params.Launch.Revision > 0 {
				launch.Revision = params.Launch.Revision
			}
			if params.Launch.Kind != "" {
				launch.Kind = params.Launch.Kind
			}
			launch.Input.Intent = params.Launch.Intent
			launch.Input.Prompt = strings.TrimSpace(params.Launch.Prompt)
		}
		if launch.Input.Prompt == "" {
			launch.Input.Prompt = strings.TrimSpace(launch.Input.Intent)
		}
		if launch.Input.Prompt != "" {
			records = append(records, historyRecordFromPersistedMessage(persistedMessageFromChatMessage(handoffLaunchUserMessage(params.RequestID, launch.Input.Prompt))))
		}
	}
	created, err := session.CreateInitializedWithLaunch(s.rt.SessionDir, initial, records, seed, launch)
	if err != nil {
		if artifactStateDir != "" {
			_ = os.RemoveAll(statepath.SessionArtifactDir(artifactStateDir, id))
		}
		return nil, err
	}
	cleanupWorktree = false
	if params.ContextSource == pluginhost.SessionContextSourceSeed {
		if loaded, err := loadChatMessages(s.rt.SessionDir, id); err == nil {
			history = loaded
		}
	}
	th := newThreadState(id, history, s.rt.ProviderName, s.rt.Model, threadCWD, true, time.Now().UTC())
	applyThreadRuntimeSelection(th, selection)
	th.Source = source
	th.Title = params.Name
	th.Owner = owner
	th.Visibility = params.Visibility
	th.ParentID = params.ParentSessionID
	th.WorktreePath = createdWorktreePath
	if created != nil {
		th.WorktreeBaseHEAD = created.WorktreeBaseHEAD
		th.WorktreeBaseRepo = created.WorktreeBaseRepo
	}
	th.WorkspaceKind = workspaceKindForCWD(s.rt.WuuHome, threadCWD)
	if workspaceID != "" {
		th.WorkspaceKind = WorkspaceKindProject
	}
	s.mu.Lock()
	s.threads[id] = th
	s.mu.Unlock()
	if params.ContextSource == pluginhost.SessionContextSourceSeed {
		if err := s.dispatchHandoffLaunchTurn(th, params); err != nil {
			return nil, err
		}
	}
	th.mu.Lock()
	thread := th.snapshotLocked()
	th.mu.Unlock()
	if params.Visibility == pluginhost.SessionVisibilityUser && params.ContextSource != pluginhost.SessionContextSourceSeed {
		if err := s.notifyThreadStarted(thread); err != nil {
			providers.DebugLogf("notify plugin-created thread %q: %v", id, err)
		}
	}
	s.pruneCachedThreads(id)
	return th, nil
}

func pluginSessionOwner(pluginID string, params pluginhost.SessionCreateParams) string {
	if params.ContextSource == pluginhost.SessionContextSourceSeed && params.Visibility == pluginhost.SessionVisibilityUser {
		return "user"
	}
	return "plugin:" + pluginID
}

func pluginSessionSource(owner string, params pluginhost.SessionCreateParams) string {
	if params.ContextSource == pluginhost.SessionContextSourceSeed && params.Seed != nil {
		if producer := strings.TrimSpace(params.Seed.Provenance.Producer); producer != "" {
			return producer
		}
	}
	return owner
}

func handoffLaunchUserMessage(requestID, prompt string) providers.ChatMessage {
	return providers.ChatMessage{
		Role:     "user",
		Content:  strings.TrimSpace(prompt),
		ClientID: pluginSessionRequestClientID("handoff", requestID),
		Origin:   "user",
		Cause:    session.SessionLaunchKindHandoff,
	}
}

func handoffLaunchTurnAlreadyStarted(th *threadState, clientID string) bool {
	if th == nil {
		return false
	}
	clientID = strings.TrimSpace(clientID)
	if clientID == "" {
		return false
	}
	th.mu.Lock()
	defer th.mu.Unlock()
	if th.running {
		return true
	}
	sawUser := false
	for _, existing := range th.History {
		if strings.TrimSpace(existing.ClientID) == clientID {
			sawUser = true
			continue
		}
		if sawUser && strings.EqualFold(strings.TrimSpace(existing.Role), "assistant") && !existing.Hidden {
			return true
		}
	}
	return false
}

func (s *Server) dispatchHandoffLaunchTurn(th *threadState, params pluginhost.SessionCreateParams) error {
	if th == nil || params.Launch == nil {
		return nil
	}
	prompt := strings.TrimSpace(params.Launch.Prompt)
	if prompt == "" {
		prompt = strings.TrimSpace(params.Launch.Intent)
	}
	if prompt == "" {
		return nil
	}
	msg := handoffLaunchUserMessage(params.RequestID, prompt)
	if handoffLaunchTurnAlreadyStarted(th, msg.ClientID) {
		return nil
	}
	permissions, err := s.resolveThreadTurnPermissions(th, nil)
	if err != nil {
		return err
	}
	_, _, err = s.startPluginSubmittedTurn(context.Background(), th, msg, turnRuntimeSnapshot{}.withPermissions(permissions))
	return err
}

func (s *Server) startPluginSubmittedTurn(ctx context.Context, th *threadState, msg providers.ChatMessage, snapshot turnRuntimeSnapshot) (startedThreadTurn, bool, error) {
	// The host call only admits the turn. Once accepted, the turn belongs to the
	// target session and must outlive the plugin invocation that submitted it.
	ctx = context.WithoutCancel(ctx)
	var threadRuntime *runtime.ThreadRuntime
	started, ok, err := s.startThreadUserTurnWithAdmission(
		ctx, th, msg, snapshot, false, turnReadOnlyFail,
		turnAdmissionHooks{afterLease: func(admitted *threadState, _ *providers.ChatMessage) error {
			var runtimeErr error
			threadRuntime, runtimeErr = s.ensureThreadRuntimeAfterAdmission(admitted)
			if runtimeErr == nil {
				s.foldFrozenWorkerTree(admitted, threadRuntime)
			}
			return runtimeErr
		}},
	)
	if err != nil {
		if errors.Is(err, errThreadExecutionBusy) {
			return startedThreadTurn{}, false, nil
		}
		return startedThreadTurn{}, false, err
	}
	if !ok {
		return startedThreadTurn{}, false, nil
	}
	launch, accepted := s.reserveBackground(func() {
		s.runTurn(started.ctx, th, threadRuntime, started.turnID, started.runtime, started.history)
	})
	if !accepted {
		return startedThreadTurn{}, false, errors.Join(errServerClosed, s.abortStartedThreadTurnDurably(th, started, errServerClosed))
	}
	defer launch.Cancel()
	if err := s.writeNotification(NotificationTurnStarted, TurnStartedNotification{ThreadID: th.ID, Turn: started.turn}); err != nil {
		return startedThreadTurn{}, false, errors.Join(err, s.abortStartedThreadTurnDurably(th, started, err))
	}
	launch.Commit()
	return started, true, nil
}

func normalizeSessionToolPolicy(policy pluginhost.SessionToolPolicy) (pluginhost.SessionToolPolicy, error) {
	normalize := func(kind string, names []string) ([]string, error) {
		seen := make(map[string]struct{}, len(names))
		out := make([]string, 0, len(names))
		for _, name := range names {
			name = strings.TrimSpace(name)
			if name == "" {
				return nil, fmt.Errorf("tool_policy.%s contains an empty tool name", kind)
			}
			if len([]byte(name)) > 256 {
				return nil, fmt.Errorf("tool_policy.%s tool name exceeds 256 bytes", kind)
			}
			if _, ok := seen[name]; ok {
				continue
			}
			seen[name] = struct{}{}
			out = append(out, name)
		}
		return out, nil
	}
	allow, err := normalize("allow", policy.Allow)
	if err != nil {
		return pluginhost.SessionToolPolicy{}, err
	}
	deny, err := normalize("deny", policy.Deny)
	if err != nil {
		return pluginhost.SessionToolPolicy{}, err
	}
	return pluginhost.SessionToolPolicy{Allow: allow, Deny: deny}, nil
}

func applyPluginSessionInstructions(history []providers.ChatMessage, instructions string) []providers.ChatMessage {
	instructions = strings.TrimSpace(instructions)
	if instructions == "" {
		return history
	}
	for index := range history {
		if strings.EqualFold(strings.TrimSpace(history[index].Role), "system") {
			history[index].Content = strings.TrimSpace(history[index].Content) + "\n\n# Session instructions\n\n" + instructions
			return history
		}
	}
	return append([]providers.ChatMessage{{Role: "system", Content: "# Session instructions\n\n" + instructions}}, history...)
}

func pluginTurnRequestContext(input []pluginhost.SessionContextBlock) ([]agent.ContextSegment, error) {
	if len(input) > pluginhost.MaxSessionSendContextBlocks {
		return nil, fmt.Errorf("context_blocks exceeds %d entries", pluginhost.MaxSessionSendContextBlocks)
	}
	blocks := make([]wuucontext.Block, 0, len(input))
	total := 0
	for _, block := range input {
		content := strings.TrimSpace(block.Content)
		if content == "" {
			return nil, errors.New("context block content is required")
		}
		size := len([]byte(content))
		if size > pluginhost.MaxSessionSendContextBlockBytes {
			return nil, fmt.Errorf("context block exceeds %d bytes", pluginhost.MaxSessionSendContextBlockBytes)
		}
		total += size
		if total > pluginhost.MaxSessionSendContextTotalBytes {
			return nil, fmt.Errorf("context_blocks exceeds %d total bytes", pluginhost.MaxSessionSendContextTotalBytes)
		}
		kind := strings.TrimSpace(block.Kind)
		if kind == "" {
			kind = string(wuucontext.BlockAdditionalContext)
		}
		blocks = append(blocks, wuucontext.Block{
			Kind: wuucontext.BlockKind(kind), Title: strings.TrimSpace(block.Title),
			Source: strings.TrimSpace(block.Source), Content: content,
		})
	}
	return agent.RequestOnlyContextBlocks(blocks), nil
}

func contextSeedFromParams(params pluginhost.SessionContextSeed) session.ContextSeed {
	seed := session.ContextSeed{
		Version: params.Version,
		ID:      strings.TrimSpace(params.ID),
		Body:    params.Body,
		Source:  session.HistorySnapshot{SessionID: strings.TrimSpace(params.Source.SessionID), ThroughSeq: params.Source.ThroughSeq},
		Provenance: session.ContextSeedProvenance{
			Producer: strings.TrimSpace(params.Provenance.Producer), SourceModel: strings.TrimSpace(params.Provenance.SourceModel), CreatedAt: strings.TrimSpace(params.Provenance.CreatedAt),
		},
	}
	for _, reference := range params.References {
		seed.References = append(seed.References, session.ContextSeedReference{
			ID:    strings.TrimSpace(reference.ID),
			Label: strings.TrimSpace(reference.Label),
			History: session.HistoryRef{
				Snapshot: session.HistorySnapshot{SessionID: strings.TrimSpace(reference.History.Snapshot.SessionID), ThroughSeq: reference.History.Snapshot.ThroughSeq},
				StartSeq: reference.History.StartSeq,
				EndSeq:   reference.History.EndSeq,
			},
		})
	}
	for _, artifact := range params.Artifacts {
		seed.Artifacts = append(seed.Artifacts, session.ContextSeedArtifact{ID: strings.TrimSpace(artifact.ID), Label: strings.TrimSpace(artifact.Label)})
	}
	return seed
}
