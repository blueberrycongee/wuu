package appserver

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/blueberrycongee/wuu/internal/channels"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/session"
	"github.com/blueberrycongee/wuu/internal/worktree"
)

type harnessSessionView struct {
	ID            string                       `json:"session_id"`
	Title         string                       `json:"title"`
	WorkspaceRoot string                       `json:"workspace_root"`
	WorkspaceID   string                       `json:"workspace_id,omitempty"`
	Provider      string                       `json:"provider"`
	Model         string                       `json:"model"`
	Effort        string                       `json:"effort,omitempty"`
	State         string                       `json:"state"`
	Control       *session.Control             `json:"control,omitempty"`
	Management    *channels.HarnessSessionLink `json:"management,omitempty"`
}

func (s *Server) harnessSessionView(ctx context.Context, metadata session.Session) (harnessSessionView, error) {
	v := harnessSessionView{ID: metadata.ID, Title: metadata.Title, WorkspaceRoot: metadata.CWD, WorkspaceID: metadata.WorkspaceID, Provider: metadata.Provider, Model: metadata.Model, Effort: firstNonEmpty(metadata.Effort, metadata.Variant), State: "idle"}
	if active, err := session.ThreadExecutionActive(s.rt.SessionDir, metadata.ID); err != nil {
		return v, err
	} else if active {
		v.State = "running"
	}
	if c, ok, err := session.ReadControl(s.rt.SessionDir, metadata.ID); err != nil {
		return v, err
	} else if ok {
		v.Control = &c
	}
	if link, err := s.channelService.HarnessLink(ctx, metadata.ID); err == nil {
		v.Management = &link
	} else if !errors.Is(err, channels.ErrNotFound) {
		return v, err
	}
	return v, nil
}

func (s *Server) sharedHarnessSession(id string) (session.Session, error) {
	m, ok, err := session.Find(s.rt.SessionDir, strings.TrimSpace(id))
	if err != nil {
		return m, err
	}
	if !ok {
		return m, session.ErrSessionNotFound
	}
	if m.Visibility == pluginhost.SessionVisibilityPlugin || isNamedAgentSessionSource(m.Source) || m.ArchivedAt != nil {
		return m, errors.New("session must be an active ordinary Harness session")
	}
	return m, nil
}

// HarnessSession provides short, non-blocking operations. Execution and result
// delivery are reconciled from the outbox, including after process restarts.
func (s *Server) HarnessSession(ctx context.Context, actor channels.HarnessSessionActor, p channels.HarnessSessionParams) (any, error) {
	if s == nil || s.rt == nil || s.channelService == nil {
		return nil, errors.New("session management unavailable")
	}
	if len(p.Media) > 0 && p.Action != "create" && p.Action != "send" {
		return nil, errors.New("media is supported only for session create/send")
	}
	if p.Action == "list" {
		return s.listHarnessSessions(ctx, actor, p)
	}
	if p.Action == "inspect" {
		return s.inspectHarnessSession(ctx, p)
	}
	if p.SessionID != "" && p.Action != "manage" {
		link, err := s.channelService.HarnessLink(ctx, p.SessionID)
		if err != nil && !errors.Is(err, channels.ErrNotFound) {
			return nil, err
		}
		if err == nil && link.Active {
			if link.AgentID != actor.AgentID || link.RoomID != actor.RoomID || p.WorkID != "" && p.WorkID != link.WorkID {
				return nil, channels.ErrUnauthorized
			}
			actor.WorkID, actor.GoalRevision = link.WorkID, link.GoalRevision
			if p.Action == "send" && link.WorkID != "" {
				work, err := s.channelService.GetWork(ctx, link.WorkID)
				if err != nil {
					return nil, err
				}
				actor.GoalRevision = work.GoalRevision
			}
		}
	}
	if p.WorkID != "" && (p.Action == "create" || p.Action == "manage") {
		work, err := s.channelService.GetWork(ctx, p.WorkID)
		if err != nil {
			return nil, err
		}
		if work.RoomID != actor.RoomID || work.OwnerNamedAgentID != actor.AgentID {
			return nil, channels.ErrUnauthorized
		}
		actor.WorkID, actor.GoalRevision = work.ID, work.GoalRevision
	}
	if (p.Purpose != "" || p.BaseRevision != "") && !p.HostVerification {
		return nil, errors.New("session purpose is assigned by the host")
	}
	if err := s.validateHarnessScope(ctx, actor, !p.HostVerification); err != nil {
		return nil, err
	}
	p.Prompt = strings.TrimSpace(p.Prompt)
	if p.Action == "create" {
		if p.WorkspaceRoot == "" && p.WorkspaceID == "" {
			room, err := s.channelService.GetRoom(ctx, actor.RoomID)
			if err != nil {
				return nil, err
			}
			p.WorkspaceRoot, p.WorkspaceID = room.WorkspaceRoot, room.WorkspaceID
			if p.WorkspaceRoot == "" && p.WorkspaceID == "" {
				return nil, errors.New("this conversation has no project; choose a workspace for execution")
			}
		}
		if p.Prompt == "" {
			return nil, errors.New("create requires prompt")
		}
		if p.Workspace != "" && p.Workspace != "shared" && p.Workspace != "worktree" {
			return nil, errors.New("workspace must be shared or worktree")
		}
		root, id, err := s.resolveSessionWorkspace(p.WorkspaceID, p.WorkspaceRoot)
		if err != nil {
			return nil, err
		}
		p.WorkspaceRoot, p.WorkspaceID = root, id
		if p.Workspace == "" && actor.WorkID != "" && worktree.IsGitRepo(root) {
			p.Workspace = "worktree"
		}
	} else {
		metadata, err := s.sharedHarnessSession(p.SessionID)
		if err != nil {
			return nil, err
		}
		root, id, err := s.sessionWorkspace(metadata)
		if err != nil {
			return nil, err
		}
		if p.WorkspaceRoot != "" || p.WorkspaceID != "" {
			requestedRoot, requestedID, err := s.resolveSessionWorkspace(p.WorkspaceID, p.WorkspaceRoot)
			if err != nil || root != requestedRoot || id != requestedID {
				return nil, errors.New("an existing session cannot be retargeted to another workspace")
			}
		}
		p.WorkspaceRoot, p.WorkspaceID = root, id
	}
	switch p.Action {
	case "create", "send", "stop", "manage":
	default:
		return nil, errors.New("unsupported session action")
	}
	if p.Action == "send" && (p.Prompt == "" || p.Mode != "" && p.Mode != "queue" && p.Mode != "steer") {
		return nil, errors.New("send requires prompt and mode queue or steer")
	}
	if p.Action == "manage" && p.Mode != "" && p.Mode != "attach" && p.Mode != "release" && p.Mode != "resume" {
		return nil, errors.New("manage mode must be attach, release, or resume")
	}
	if !s.ownsSessionWorkspace(p.WorkspaceRoot, p.WorkspaceID) && !s.supportsClientMethod(MethodWorkspaceHarnessDispatch) {
		return nil, errors.New("target workspace runtime is unavailable: this host does not support workspace dispatch; no operation was accepted")
	}
	id := p.SessionID
	if p.Action == "create" {
		id = session.NewID()
	}
	c, hasControl, err := session.ReadControl(s.rt.SessionDir, id)
	if err != nil {
		return nil, err
	}
	if hasControl && c.State != session.ControlReleased && c.ManagerID != actor.AgentID {
		return nil, errors.New("another manager controls this session")
	}
	actor.UserSeqStart, actor.UserSeqEnd, err = s.channelService.DelegationSourceRange(ctx, actor.RoomID, actor.WorkID)
	if err != nil {
		return nil, err
	}
	op, _, err := s.channelService.ReserveHarnessOperation(ctx, actor, p, id, c.Revision)
	if err != nil {
		return nil, err
	}
	if op.State == "pending" {
		if err := s.dispatchHarnessOperation(ctx, &op); err != nil {
			return nil, err
		}
	}
	if op.State == "failed" {
		return nil, fmt.Errorf("session operation %s failed: %s", op.ID, op.Error)
	}
	metadata, err := s.sharedHarnessSession(op.Params.SessionID)
	if err != nil {
		return nil, err
	}
	view, err := s.harnessSessionView(ctx, metadata)
	if err != nil {
		return nil, err
	}
	result := map[string]any{"session": view, "operation_id": op.ID, "state": op.State, "turn_id": op.TurnID, "error": op.Error}
	if view.Management != nil && view.Management.Active && view.Management.WorkID == "" {
		result["task_binding"] = map[string]any{
			"state": "unbound",
			"note":  "No recorded task is bound. Cancelling a chat_task will not stop this session, and task-specific limits do not apply. If this executes an existing responsibility, use manage with its work_id before continuing; chat_task list can recover the ID. Work without a task record may remain unbound.",
		}
	}
	return result, nil
}

func (s *Server) validateHarnessActor(ctx context.Context, actor channels.HarnessSessionActor) error {
	return s.validateHarnessScope(ctx, actor, true)
}

func (s *Server) validateHarnessScope(ctx context.Context, actor channels.HarnessSessionActor, current bool) error {
	b, err := s.channelService.LookupCollaborationSession(ctx, actor.SessionRef)
	if err != nil {
		return err
	}
	if b.PrincipalID != actor.AgentID || current && b.TurnID != actor.TurnID || b.State == channels.CollaborationSessionCancelled || b.State == channels.CollaborationSessionInterrupted {
		return session.ErrControlChanged
	}
	scope, err := s.channelService.LookupCollaborationTurnScope(ctx, actor.SessionRef, actor.TurnID)
	if err != nil {
		return err
	}
	if scope.RoomID != actor.RoomID {
		return channels.ErrUnauthorized
	}
	workID, goalRevision := scope.WorkID, scope.GoalRevision
	if actor.WorkID != "" {
		workID, goalRevision = actor.WorkID, actor.GoalRevision
	}
	if workID != "" {
		client, err := s.channelService.BindAgent(ctx, actor.AgentID)
		if err != nil {
			return err
		}
		work, err := client.GetWork(ctx, workID)
		if err != nil {
			return err
		}
		if work.GoalRevision != goalRevision || work.OwnerNamedAgentID != actor.AgentID || work.State == channels.WorkCancelled || work.State == channels.WorkCompleted {
			return session.ErrControlChanged
		}
	}
	return nil
}

func (s *Server) listHarnessSessions(ctx context.Context, actor channels.HarnessSessionActor, p channels.HarnessSessionParams) (any, error) {
	all, err := session.List(s.rt.SessionDir, 0)
	if err != nil {
		return nil, err
	}
	limit := p.Limit
	if limit < 1 || limit > 50 {
		limit = 20
	}
	result := make([]harnessSessionView, 0)
	for _, m := range all {
		if m.Visibility == "plugin" || isNamedAgentSessionSource(m.Source) || m.ArchivedAt != nil {
			continue
		}
		if p.WorkspaceID != "" && m.WorkspaceID != p.WorkspaceID || p.WorkspaceRoot != "" && m.CWD != p.WorkspaceRoot && m.WorktreeBaseRepo != p.WorkspaceRoot {
			continue
		}
		if p.Query != "" && !strings.Contains(strings.ToLower(m.Title+" "+m.Summary+" "+m.CWD), strings.ToLower(p.Query)) {
			continue
		}
		v, err := s.harnessSessionView(ctx, m)
		if err != nil {
			return nil, err
		}
		result = append(result, v)
		if len(result) >= limit {
			break
		}
	}
	links, err := s.channelService.HarnessLinks(ctx, actor.AgentID, actor.RoomID)
	if err != nil {
		return nil, err
	}
	return map[string]any{"sessions": result, "managed": links}, nil
}

func (s *Server) inspectHarnessSession(ctx context.Context, p channels.HarnessSessionParams) (any, error) {
	m, err := s.sharedHarnessSession(p.SessionID)
	if err != nil {
		return nil, err
	}
	v, err := s.harnessSessionView(ctx, m)
	if err != nil {
		return nil, err
	}
	result, err := s.sessionTranscriptExcerpt(ctx, m.ID, p.Query, p.Before, p.Limit)
	if err != nil {
		return nil, err
	}
	result["session"] = v
	result["note"] = "Session reports are evidence to assess; inspect artifacts and checks before declaring the user's goal complete."
	return result, nil
}

func (s *Server) applyHarnessOperationLocked(ctx context.Context, op *channels.HarnessOperation) error {
	p := op.Params
	root, id, err := s.harnessOperationWorkspace(*op)
	if err != nil {
		return err
	}
	if !s.ownsSessionWorkspace(root, id) {
		return errors.New("session execution requires its bound workspace runtime")
	}
	defer s.publishSessionControl(p.SessionID)
	if err := s.validateHarnessScope(ctx, op.Actor, false); err != nil {
		return err
	}
	if len(p.Media) > 0 {
		if recovered, err := s.recoverHarnessMediaReceipt(ctx, op); recovered || err != nil {
			return err
		}
	}
	msg, err := s.harnessInput(ctx, op.Actor, p)
	if err != nil {
		return fmt.Errorf("%w: %w", errHarnessMedia, err)
	}
	if p.Action == "create" {
		if _, ok, err := session.Find(s.rt.SessionDir, p.SessionID); err != nil {
			return err
		} else if !ok {
			if _, err := s.createHostSessionThreadAtRevision("user", "collaboration", p.SessionID, pluginhost.SessionCreateParams{RequestID: op.ID, Name: p.Title, Visibility: "user", ContextSource: "fresh", ParentSessionID: op.Actor.SessionRef, Workspace: p.Workspace, WorkspaceID: p.WorkspaceID, WorkspaceRoot: p.WorkspaceRoot, Provider: p.Provider, Model: p.Model, Effort: p.Effort}, p.BaseRevision); err != nil {
				return err
			}
		}
	}
	if len(p.Media) > 0 {
		th, err := s.ensureThreadLoaded(p.SessionID)
		if err != nil {
			return err
		}
		targetRuntime, err := s.ensureThreadRuntime(th)
		if err != nil {
			return err
		}
		// Reject before a steer advances the control fence of valid work.
		if err := validateSessionInputMedia(msg, targetRuntime); err != nil {
			return fmt.Errorf("%w: %w", errHarnessMedia, err)
		}
	}
	c, hasControl, err := session.ReadControl(s.rt.SessionDir, p.SessionID)
	if err != nil {
		return err
	}
	link, linkErr := s.channelService.HarnessLink(ctx, p.SessionID)
	if linkErr != nil && !errors.Is(linkErr, channels.ErrNotFound) {
		return linkErr
	}
	if !op.Prepared && (p.Action == "create" || p.Action == "manage") {
		state := session.ControlActive
		if p.Mode == "release" {
			state = session.ControlReleased
		}
		if p.Mode != "resume" && p.Mode != "release" && hasControl && (c.State == session.ControlPaused || c.State == session.ControlTakenOver) {
			return errors.New("session is paused or user-controlled; only an explicit user request permits manage resume")
		}
		// Replays may find the control committed before the relationship/outbox.
		if c.Revision == op.Revision {
			c, err = session.ChangeControl(s.rt.SessionDir, p.SessionID, op.Actor.AgentID, state, op.Revision)
			if err != nil {
				return err
			}
		} else if c.Revision != op.Revision+1 || c.ManagerID != op.Actor.AgentID || c.State != state {
			return session.ErrControlChanged
		}
		if linkErr != nil || !link.Active {
			metadata, err := s.sharedHarnessSession(p.SessionID)
			if err != nil {
				return err
			}
			link = channels.HarnessSessionLink{SessionID: p.SessionID, LastTurnID: metadata.LatestCompletedTurnID}
		}
		link.AgentID, link.SourceSessionRef, link.SourceTurnID = op.Actor.AgentID, op.Actor.SessionRef, op.Actor.TurnID
		link.RoomID, link.WorkID, link.GoalRevision = op.Actor.RoomID, op.Actor.WorkID, op.Actor.GoalRevision
		if p.Prompt != "" {
			link.Objective = p.Prompt
		}
		if p.Action == "create" {
			metadata, err := s.sharedHarnessSession(p.SessionID)
			if err != nil {
				return err
			}
			link.BaseRevision = metadata.WorktreeBaseHEAD
			if link.BaseRevision == "" && op.Actor.WorkID != "" && worktree.IsGitRepo(metadata.CWD) {
				head, err := workspaceGitList(ctx, metadata.CWD, "rev-parse", "HEAD")
				if err != nil {
					return err
				}
				link.BaseRevision, _, err = worktree.Snapshot(ctx, metadata.CWD, strings.TrimSpace(string(head)), "base-"+p.SessionID)
				if err != nil {
					return err
				}
			}
			link.Purpose = p.Purpose
			if link.Purpose == "" {
				link.Purpose = channels.CollaborationSessionWork
			}
		}
		metadata, err := s.sharedHarnessSession(p.SessionID)
		if err != nil {
			return err
		}
		link.ExecutionRoot = metadata.CWD
		link.Active, link.ControlRevision = state == session.ControlActive, c.Revision
		if p.Mode == "resume" {
			metadata, err := s.sharedHarnessSession(p.SessionID)
			if err != nil {
				return err
			}
			link.LastTurnID = metadata.LatestCompletedTurnID
			link.Failures, link.Turns = 0, 0
		}
		if err := s.channelService.PutHarnessLink(ctx, link); err != nil {
			return err
		}
		linkErr = nil
		if p.Action == "manage" {
			op.State = "completed"
			return s.channelService.PutHarnessOperation(ctx, *op)
		}
		op.Revision = c.Revision
		op.Prepared = true
		hasControl = true
		if err := s.channelService.PutHarnessOperation(ctx, *op); err != nil {
			return err
		}
	}
	if p.Action == "stop" {
		if !hasControl || c.ManagerID != op.Actor.AgentID {
			return errors.New("manage the session before stopping it")
		}
		if c.Revision == op.Revision {
			c, err = session.ChangeControl(s.rt.SessionDir, p.SessionID, op.Actor.AgentID, session.ControlPaused, c.Revision)
			if err != nil {
				return err
			}
		} else if c.Revision != op.Revision+1 || c.State != session.ControlPaused {
			return session.ErrControlChanged
		}
		if linkErr == nil {
			link.ControlRevision = c.Revision
			if err := s.channelService.PutHarnessLink(ctx, link); err != nil {
				return err
			}
		}
		s.revokeSessionInputs(p.SessionID)
		if err := s.interruptOwnedThreadExecution(p.SessionID); err != nil {
			return err
		}
		op.State = "completed"
		return s.channelService.PutHarnessOperation(ctx, *op)
	}
	if !op.Prepared && p.Action == "send" && p.Mode == "steer" && hasControl && c.State == session.ControlActive {
		if c.Revision == op.Revision {
			c, err = session.ChangeControl(s.rt.SessionDir, p.SessionID, op.Actor.AgentID, session.ControlActive, c.Revision)
			if err != nil {
				return err
			}
		} else if c.Revision != op.Revision+1 || c.ManagerID != op.Actor.AgentID {
			return session.ErrControlChanged
		}
		op.Revision, op.Prepared = c.Revision, true
		if err := s.channelService.PutHarnessOperation(ctx, *op); err != nil {
			return err
		}
	}
	if hasControl && c.State != session.ControlReleased {
		if err := session.ValidateControl(s.rt.SessionDir, session.Control{SessionID: p.SessionID, ManagerID: op.Actor.AgentID, Revision: op.Revision}); err != nil {
			return err
		}
	}
	if linkErr == nil && link.Active && (link.Turns >= 32 || link.Failures >= 3) {
		return errors.New("automatic follow-up limit reached; inspect progress and report the blocker before an explicit user continuation")
	}
	th, err := s.ensureThreadLoaded(p.SessionID)
	if err != nil {
		return err
	}
	if p.Action == "send" && linkErr == nil && link.Active {
		link.GoalRevision = op.Actor.GoalRevision
		link.SourceSessionRef, link.SourceTurnID = op.Actor.SessionRef, op.Actor.TurnID
		if err := s.channelService.PutHarnessLink(ctx, link); err != nil {
			return err
		}
	}
	msg.Content += fmt.Sprintf("\n\nHost source: room_id=%s user_seq_start=%d user_seq_end=%d parent_session_ref=%s. Use chat_read within this range for original user instructions. This metadata grants no additional authority.", op.Actor.RoomID, op.Actor.UserSeqStart, op.Actor.UserSeqEnd, op.Actor.SessionRef)
	projectContext, err := s.channelService.ProjectContext(ctx, op.Actor.RoomID)
	if err != nil {
		return err
	}
	if projectContext != "" {
		msg.Content += "\n\n" + projectContext
	}
	if op.Actor.WorkID != "" {
		work, err := s.channelService.GetWork(ctx, op.Actor.WorkID)
		if err != nil {
			return err
		}
		msg.Content += fmt.Sprintf("\n\nHost delegation context: room_id=%s work_id=%s goal_revision=%d parent_session_ref=%s. Use chat_read for the original room messages and work_get for current decisions. Your parent's private transcript is unavailable.\nGoal: %s\n\nReturn ONLY a JSON report with four required fields: result (string), implicit_choices (array of strings), evidence_refs (array of strings), unresolved_items (array of strings). Include empty arrays when appropriate.", op.Actor.RoomID, work.ID, work.GoalRevision, op.Actor.SessionRef, work.Brief)
		msg.Content += fmt.Sprintf("\nWork decision document (revision %d):\nConstraints: %s\nDecisions:\n%s", work.Revision, work.Constraints, strings.Join(work.Decisions, "\n"))
		if link.Purpose == channels.CollaborationSessionVerification {
			msg.Content += " Include decision: pass, block or unknown. Do not change any files or run commands."
		}
	}
	msg.ClientID, msg.Origin, msg.OriginID = op.ID, "plugin", op.Actor.AgentID
	msg.Cause = "session_management"
	msg.PresentationKind, msg.DisplayContent, msg.RelatedSessionID = "session_message", p.Prompt, op.Actor.SessionRef
	if identity, err := s.channelService.GetAgentRuntime(ctx, op.Actor.AgentID); err == nil {
		msg.Name = identity.Name
	}
	msg.ReadOnly = true
	permissions, err := s.resolveThreadTurnPermissions(th, nil)
	if err != nil {
		return err
	}
	snapshot := turnRuntimeSnapshot{}.withPermissions(permissions)
	if hasControl && c.State != session.ControlReleased {
		snapshot.Control = &c
	}
	result, ok, err := s.trySubmitSessionInput(ctx, th, msg, p.Mode, snapshot)
	if errors.Is(err, channels.ErrHarnessCapacity) {
		return s.channelService.PutHarnessOperation(ctx, *op)
	}
	if err != nil {
		return err
	}
	if ok {
		if linkErr == nil && link.Active && link.WorkID != "" && op.RunID == "" {
			run, err := s.channelService.StartHarnessWorkRun(ctx, link.SessionID, "harness-turn:"+link.SessionID+":"+result.TurnID)
			if err != nil {
				return err
			}
			op.RunID = run.ID
			if err := s.channelService.PutHarnessOperation(ctx, *op); err != nil {
				return err
			}
		}

		if linkErr == nil && link.Active && link.RoomID == op.Actor.RoomID && link.WorkID == op.Actor.WorkID {
			if p.Mode == "steer" {
				link.ControlRevision = op.Revision
				link.Objective = p.Prompt
			}
			if err := s.channelService.PutHarnessLink(ctx, link); err != nil {
				return err
			}
		}
		op.TurnID, op.State = result.TurnID, "submitted"
	}
	return s.channelService.PutHarnessOperation(ctx, *op)
}

// kickHarnessSessions is event-driven. The maintenance pass is only a crash or
// lost-notification recovery path; no model turn waits or polls for a worker.
func (s *Server) kickHarnessSessions() {
	if s == nil || s.channelService == nil {
		return
	}
	s.startBackground(func() {
		if err := s.reconcileHarnessSessions(context.Background()); err != nil {
			providers.DebugLogf("reconcile Harness sessions: %v", err)
		}
	})
}

func (s *Server) reconcileLocalHarnessSessions(ctx context.Context) error {
	s.controlMu.Lock()
	defer s.controlMu.Unlock()
	links, err := s.channelService.HarnessLinks(ctx, "", "")
	if err != nil {
		return err
	}
	for _, link := range links {
		deleted, err := s.channelService.AgentDeleted(ctx, link.AgentID)
		if err != nil {
			return err
		}
		if deleted {
			// Cleanup is global: the workspace may never be opened again.
			if err := s.archiveDeletedAgentSession(ctx, link); err != nil {
				return err
			}
			continue
		}
		metadata, err := s.sharedHarnessSession(link.SessionID)
		if err != nil {
			continue
		}
		root, id, err := s.sessionWorkspace(metadata)
		if err != nil || !s.ownsSessionWorkspace(root, id) {
			continue
		}
		if active, err := session.ThreadExecutionActive(s.rt.SessionDir, link.SessionID); err != nil {
			return err
		} else if !active {
			if err := s.channelService.ReleaseHarnessExecution(ctx, link.SessionID); err != nil {
				return err
			}
		}
		if link.Active {
			if err := s.reconcileHarnessLink(ctx, link); err != nil {
				return err
			}
		}
	}
	ops, err := s.channelService.PendingHarnessOperations(ctx)
	if err != nil {
		return err
	}
	for i := range ops {
		op := &ops[i]
		root, id, err := s.harnessOperationWorkspace(*op)
		if err != nil {
			if err := s.failHarnessOperation(ctx, op, err.Error()); err != nil {
				return err
			}
			continue
		}
		if !s.ownsSessionWorkspace(root, id) {
			continue
		}
		if op.State == "submitted" {
			active, err := session.ThreadExecutionActive(s.rt.SessionDir, op.Params.SessionID)
			if err != nil {
				return err
			}
			if active {
				continue
			}
			persisted, err := s.loadPersistedThreadState(op.Params.SessionID, time.Now().UTC())
			if err != nil {
				return err
			}
			if receipt, found := s.findSessionInput(persisted, op.ID); found {
				op.TurnID = receipt.TurnID
				if receipt.State == pluginhost.TurnLifecycleQueued {
					continue
				}
				if receipt.State == pluginhost.TurnLifecycleCompleted {
					op.State = "completed"
					if err := s.channelService.PutHarnessOperation(ctx, *op); err != nil {
						return err
					}
					continue
				}
				// Persisted input without a terminal is never blindly replayed.
				if err := s.failHarnessOperation(ctx, op, "Execution ended without a durable terminal result; inspect existing artifacts before retrying."); err != nil {
					return err
				}
				continue
			}
			// A steer accepted only in memory may be lost during a crash. Its
			// durable outbox input is still valid unless control was revoked.
			op.State = "pending"
		}
		// Pending commands retain their original source scope. A newer room turn
		// does not revoke accepted work, but a stopped task or control revision does.
		if err := s.applyHarnessOperationLocked(ctx, op); err != nil {
			if err := s.failHarnessOperation(ctx, op, err.Error()); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Server) archiveDeletedAgentSession(ctx context.Context, link channels.HarnessSessionLink) error {
	c, ok, err := session.ReadControl(s.rt.SessionDir, link.SessionID)
	if err != nil {
		return err
	}
	if !ok || c.ManagerID != link.AgentID || c.State == session.ControlTakenOver {
		return nil
	}
	archivedNow := c.State == session.ControlActive || c.State == session.ControlPaused
	if archivedNow {
		if err := session.ArchiveControlled(s.rt.SessionDir, c, "agent_deleted"); err != nil {
			if errors.Is(err, session.ErrControlChanged) || errors.Is(err, session.ErrSessionNotFound) {
				return nil
			}
			return err
		}
	}
	metadata, found, err := session.Find(s.rt.SessionDir, link.SessionID)
	if err != nil {
		return err
	}
	if !found || metadata.ArchivedAt == nil || metadata.ArchiveReason != "agent_deleted" {
		return nil
	}
	// Retry interruption after a crash between the archive commit and cleanup.
	s.revokeSessionInputs(link.SessionID)
	cleanupErr := s.interruptOwnedThreadExecution(link.SessionID)
	if active, err := session.ThreadExecutionActive(s.rt.SessionDir, link.SessionID); err != nil {
		cleanupErr = errors.Join(cleanupErr, err)
	} else if !active {
		cleanupErr = errors.Join(cleanupErr, s.channelService.ReleaseHarnessExecution(ctx, link.SessionID))
	}
	if archivedNow {
		thread, err := s.threadAfterMetadataUpdate(metadata)
		if err != nil {
			return errors.Join(cleanupErr, err)
		}
		cleanupErr = errors.Join(cleanupErr, s.notifyThreadUpdated(thread))
	}
	return cleanupErr
}

func (s *Server) reconcileHarnessLink(ctx context.Context, link channels.HarnessSessionLink) error {
	c, ok, err := session.ReadControl(s.rt.SessionDir, link.SessionID)
	if err != nil {
		return err
	}
	if !ok || c.ManagerID != link.AgentID || c.State != session.ControlActive {
		return nil
	}
	if err := s.validateHarnessScope(ctx, channels.HarnessSessionActor{AgentID: link.AgentID, SessionRef: link.SourceSessionRef, TurnID: link.SourceTurnID, RoomID: link.RoomID, WorkID: link.WorkID, GoalRevision: link.GoalRevision}, false); err != nil {
		if link.WorkID != "" {
			if work, workErr := s.channelService.GetWork(ctx, link.WorkID); workErr == nil && work.GoalRevision != link.GoalRevision && work.State != channels.WorkCancelled && work.State != channels.WorkCompleted {
				s.revokeSessionInputs(link.SessionID)
				return s.interruptOwnedThreadExecution(link.SessionID)
			}
		}
		if errors.Is(err, session.ErrControlChanged) || errors.Is(err, channels.ErrNotFound) {
			if _, err := session.ChangeControl(s.rt.SessionDir, link.SessionID, link.AgentID, session.ControlPaused, c.Revision); err != nil {
				return err
			}
			s.revokeSessionInputs(link.SessionID)
			s.publishSessionControl(link.SessionID)
			return s.interruptOwnedThreadExecution(link.SessionID)
		}
		return err
	}
	if running, err := session.ThreadExecutionActive(s.rt.SessionDir, link.SessionID); err != nil {
		return err
	} else if running {
		return nil
	}
	th, err := s.loadPersistedThreadState(link.SessionID, time.Now().UTC())
	if err != nil {
		return err
	}
	if len(th.Turns) == 0 {
		return nil
	}
	start := 0
	for i, turn := range th.Turns {
		if turn.ID == link.LastTurnID {
			start = i + 1
			break
		}
	}
	for _, turn := range th.Turns[start:] {
		if turn.Status == TurnStatusInProgress {
			break
		}
		if err := s.deliverHarnessResult(ctx, &link, turn); err != nil {
			return err
		}
	}
	return nil
}

func (s *Server) deliverHarnessResult(ctx context.Context, link *channels.HarnessSessionLink, turn Turn) error {
	if err := s.channelService.RecordHarnessUsage(ctx, *link, turn.ID, turn.InputTokens, turn.OutputTokens); err != nil {
		return err
	}
	text := ""
	for _, item := range turn.Items {
		if item.Type == ThreadItemAgentMessage {
			text += item.Text + "\n"
		}
	}
	if turn.Error != nil {
		text += "\nError: " + turn.Error.Message
	}
	link.Turns++
	if turn.Status == TurnStatusFailed {
		link.Failures++
	} else {
		link.Failures = 0
	}
	// Read provenance from the accepted input, not the mutable current goal.
	// A queued follow-up must never relabel a late result from the previous goal.
	actor := channels.HarnessSessionActor{AgentID: link.AgentID, SessionRef: link.SourceSessionRef, TurnID: link.SourceTurnID, RoomID: link.RoomID, WorkID: link.WorkID, GoalRevision: link.GoalRevision}
	revision := link.ControlRevision
	var acceptedOperation channels.HarnessOperation
	for _, item := range turn.Items {
		if item.Type != ThreadItemUserMessage || item.SourceID == "" {
			continue
		}
		op, err := s.channelService.HarnessOperation(ctx, item.SourceID)
		if errors.Is(err, channels.ErrNotFound) {
			continue
		}
		if err != nil {
			return err
		}
		actor, revision = op.Actor, op.Revision
		acceptedOperation = op
	}
	if err := s.completeHarnessWork(ctx, *link, acceptedOperation, turn, text); err != nil {
		return err
	}
	if acceptedOperation.RunID != "" {
		work, err := s.channelService.GetWork(ctx, actor.WorkID)
		if err != nil {
			return err
		}
		for _, run := range work.Runs {
			if run.ID == acceptedOperation.RunID {
				text = fmt.Sprintf("Work run %s: %s. Session history: %s, turn %s.\n%s", run.ID, run.State, link.SessionID, turn.ID, run.Outcome)
				for _, artifact := range work.Artifacts {
					if artifact.RunID == run.ID {
						text += "\nCandidate artifact: " + artifact.ID + " (" + artifact.URI + ")"
					}
				}
				break
			}
		}
	}
	c, _, err := session.ReadControl(s.rt.SessionDir, link.SessionID)
	if err != nil {
		return err
	}
	if c.ManagerID == link.AgentID && c.State == session.ControlActive && actor.AgentID == link.AgentID && actor.RoomID == link.RoomID && s.validateHarnessScope(ctx, actor, false) == nil {
		// Keep the payload stable across retries. Current control can change after
		// delivery but before its cursor commits; provenance belongs to the input.
		body := fmt.Sprintf("Harness session %s finished turn %s (%s). This is execution evidence, not proof that the user's goal is complete. Accepted input belongs to room %s, task %q goal revision %d, control revision %d. Compare these with the current session: a later control or task change makes this prior evidence, not completion of the new goal. Inspect artifacts and pending instructions before continuing with session send or reporting completion.\n\n%s", link.SessionID, turn.ID, turn.Status, actor.RoomID, actor.WorkID, actor.GoalRevision, revision, excerpt(text, 2400))
		_, err = s.channelService.EnqueueSessionResult(ctx, channels.SessionResultEnqueueParams{ParentSessionRef: actor.SessionRef, ParentTurnID: actor.TurnID, SourceSessionRef: link.SessionID, RequestID: "harness-result:" + link.SessionID + ":" + turn.ID, Body: body, TerminalState: channels.CollaborationTerminalState(turn.Status)})
		if err != nil {
			return err
		}
	}
	link.LastTurnID = turn.ID
	if err := s.channelService.PutHarnessLink(ctx, *link); err != nil {
		return err
	}
	ops, err := s.channelService.PendingHarnessOperations(ctx)
	if err != nil {
		return err
	}
	accepted := make(map[string]bool)
	for _, item := range turn.Items {
		if item.Type == ThreadItemUserMessage {
			accepted[item.SourceID] = true
		}
	}
	for _, op := range ops {
		if op.Params.SessionID == link.SessionID && op.State == "submitted" && op.TurnID == turn.ID && accepted[op.ID] {
			op.State = "completed"
			if err := s.channelService.PutHarnessOperation(ctx, op); err != nil {
				return err
			}
		}
	}
	return nil
}

// noticeHarnessControl tells a Collaboration manager that the user changed
// control of one of its sessions.
func (s *Server) noticeHarnessControl(id string, c session.Control) error {
	if s.channelService == nil {
		return nil
	}
	link, err := s.channelService.HarnessLink(context.Background(), id)
	if errors.Is(err, channels.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	_, err = s.channelService.EnqueueSessionResult(context.Background(), channels.SessionResultEnqueueParams{
		ParentSessionRef: link.SourceSessionRef, ParentTurnID: link.SourceTurnID, SourceSessionRef: id,
		RequestID: fmt.Sprintf("harness-control:%s:%d", id, c.Revision),
		Body:      fmt.Sprintf("The user changed control of Harness session %s to %s. Automatic instructions and follow-up for this session are paused. Keep its progress; do not resume or create a replacement unless the user explicitly asks. Other tasks are unaffected.", id, c.State),
	})
	return err
}

func (s *Server) failHarnessOperation(ctx context.Context, op *channels.HarnessOperation, reason string) error {
	if err := s.validateHarnessScope(ctx, op.Actor, false); err != nil {
		op.State, op.Error = "failed", reason
		return s.channelService.PutHarnessOperation(ctx, *op)
	}
	_, err := s.channelService.EnqueueSessionResult(ctx, channels.SessionResultEnqueueParams{ParentSessionRef: op.Actor.SessionRef, ParentTurnID: op.Actor.TurnID, SourceSessionRef: op.Params.SessionID, RequestID: op.ID + ":failure", Body: "Session operation failed: " + reason + ". Inspect the existing session before deciding whether to retry.", TerminalState: channels.CollaborationTerminalFailed})
	if err != nil {
		return err
	}
	op.State, op.Error = "failed", reason
	return s.channelService.PutHarnessOperation(ctx, *op)
}
