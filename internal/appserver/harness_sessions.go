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
	if p.Action == "list" {
		return s.listHarnessSessions(ctx, actor, p)
	}
	if p.Action == "inspect" {
		return s.inspectHarnessSession(ctx, p)
	}
	s.harnessMu.Lock()
	defer s.harnessMu.Unlock()
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
	if err := s.validateHarnessActor(ctx, actor); err != nil {
		return nil, err
	}
	p.Prompt = strings.TrimSpace(p.Prompt)
	if p.Action == "create" {
		if p.WorkspaceRoot == "" && p.WorkspaceID == "" {
			return nil, errors.New("create requires the task's workspace_root or workspace_id; identity homes are not project defaults")
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
	} else {
		if _, err := s.sharedHarnessSession(p.SessionID); err != nil {
			return nil, err
		}
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
	op, _, err := s.channelService.ReserveHarnessOperation(ctx, actor, p, id, c.Revision)
	if err != nil {
		return nil, err
	}
	if op.State == "pending" {
		if err := s.applyHarnessOperationLocked(ctx, &op); err != nil {
			return nil, err
		}
	}
	metadata, err := s.sharedHarnessSession(op.Params.SessionID)
	if err != nil {
		return nil, err
	}
	view, err := s.harnessSessionView(ctx, metadata)
	if err != nil {
		return nil, err
	}
	return map[string]any{"session": view, "operation_id": op.ID, "state": op.State, "turn_id": op.TurnID, "error": op.Error}, nil
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
	limit := p.Limit
	if limit < 1 || limit > 30 {
		limit = 8
	}
	var page session.HistoryPage
	if p.Query != "" {
		page, err = session.SearchHistoryQuery(ctx, s.rt.SessionDir, session.HistorySearchQuery{SessionID: m.ID, Query: p.Query, BeforeSeq: p.Before, Limit: limit})
	} else {
		head, readErr := session.ReadHistoryPage(ctx, s.rt.SessionDir, m.ID, 1, 1)
		if readErr != nil {
			return nil, readErr
		}
		end := head.HeadSeq
		if p.Before > 0 && p.Before <= end {
			end = p.Before - 1
		}
		start := max(1, end-limit+1)
		page, err = session.ReadHistoryQuery(ctx, s.rt.SessionDir, session.HistoryReadQuery{SessionID: m.ID, StartSeq: start, EndSeq: end, Limit: limit})
		page.HasMore = start > 1
		if page.HasMore {
			page.Next = &session.HistoryCursor{SessionID: m.ID, SnapshotSeq: head.HeadSeq, Seq: start}
		}
	}
	if err != nil {
		return nil, err
	}
	items := make([]map[string]any, 0, len(page.Records))
	for _, r := range page.Records {
		if r.Role == "system" || r.Hidden {
			continue
		}
		items = append(items, map[string]any{"seq": r.Seq, "role": r.Role, "name": r.Name, "content": harnessExcerpt(r.Content, 1600), "tool_calls": harnessExcerpt(string(r.ToolCalls), 1600), "tool_result": harnessExcerpt(string(r.ToolResult), 1600), "truncated": len([]rune(r.Content)) > 1600})
	}
	return map[string]any{"session": v, "history": items, "page": map[string]any{"has_more": page.HasMore, "next": page.Next}, "note": "Session reports are evidence to assess; inspect artifacts and checks before declaring the user's goal complete."}, nil
}

func harnessExcerpt(text string, limit int) string {
	r := []rune(text)
	if len(r) > limit {
		return string(r[:limit]) + "\n[excerpt]"
	}
	return text
}

func (s *Server) applyHarnessOperationLocked(ctx context.Context, op *channels.HarnessOperation) error {
	p := op.Params
	defer s.publishSessionControl(p.SessionID)
	if err := s.validateHarnessScope(ctx, op.Actor, false); err != nil {
		return err
	}
	if p.Action == "create" {
		if _, ok, err := session.Find(s.rt.SessionDir, p.SessionID); err != nil {
			return err
		} else if !ok {
			if _, err := s.createHostSessionThread("user", "collaboration", p.SessionID, pluginhost.SessionCreateParams{RequestID: op.ID, Name: p.Title, Visibility: "user", ContextSource: "fresh", ParentSessionID: op.Actor.SessionRef, Workspace: p.Workspace, WorkspaceID: p.WorkspaceID, WorkspaceRoot: p.WorkspaceRoot, Provider: p.Provider, Model: p.Model, Effort: p.Effort}); err != nil {
				return err
			}
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
		if err := s.interruptHarnessSession(p.SessionID); err != nil {
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
	msg, err := literalUserMessageFromPrompt(p.Prompt, nil, nil)
	if err != nil {
		return err
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
		if linkErr == nil && link.Active && link.RoomID == op.Actor.RoomID && link.WorkID == op.Actor.WorkID {
			link.SourceTurnID, link.ControlRevision = op.Actor.TurnID, op.Revision
			if p.Mode == "steer" {
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

func (s *Server) interruptHarnessSession(id string) error {
	if th := s.thread(id); th != nil {
		_, err := s.interruptThreadExecution(id, "", "")
		return err
	}
	_, err := session.RequestThreadExecutionReset(s.rt.SessionDir, id)
	return err
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

func (s *Server) reconcileHarnessSessions(ctx context.Context) error {
	s.harnessMu.Lock()
	defer s.harnessMu.Unlock()
	links, err := s.channelService.HarnessLinks(ctx, "", "")
	if err != nil {
		return err
	}
	for _, link := range links {
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

func (s *Server) reconcileHarnessLink(ctx context.Context, link channels.HarnessSessionLink) error {
	c, ok, err := session.ReadControl(s.rt.SessionDir, link.SessionID)
	if err != nil {
		return err
	}
	if !ok || c.ManagerID != link.AgentID || c.State != session.ControlActive {
		return nil
	}
	if err := s.validateHarnessScope(ctx, channels.HarnessSessionActor{AgentID: link.AgentID, SessionRef: link.SourceSessionRef, TurnID: link.SourceTurnID, RoomID: link.RoomID, WorkID: link.WorkID, GoalRevision: link.GoalRevision}, false); err != nil {
		if errors.Is(err, session.ErrControlChanged) || errors.Is(err, channels.ErrNotFound) {
			if _, err := session.ChangeControl(s.rt.SessionDir, link.SessionID, link.AgentID, session.ControlPaused, c.Revision); err != nil {
				return err
			}
			return s.interruptHarnessSession(link.SessionID)
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
	}
	c, _, err := session.ReadControl(s.rt.SessionDir, link.SessionID)
	if err != nil {
		return err
	}
	if c.Revision == revision && c.State == session.ControlActive && s.validateHarnessScope(ctx, actor, false) == nil {
		body := fmt.Sprintf("Harness session %s finished turn %s (%s). This is an execution result, not proof that the user's goal is complete. Inspect the session and artifacts, check against the latest request, then continue with session send if needed. Results belong to room %s.\n\n%s", link.SessionID, turn.ID, turn.Status, actor.RoomID, harnessExcerpt(text, 2400))
		_, err = s.channelService.EnqueueSessionResult(ctx, channels.SessionResultEnqueueParams{ParentSessionRef: actor.SessionRef, ParentTurnID: actor.TurnID, SourceSessionRef: link.SessionID, RequestID: "harness-result:" + link.SessionID + ":" + turn.ID, Body: body})
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
	for _, op := range ops {
		if op.Params.SessionID == link.SessionID && op.State == "submitted" && op.TurnID == turn.ID {
			op.State = "completed"
			if err := s.channelService.PutHarnessOperation(ctx, op); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Server) takeHarnessControl(id, state string) error {
	if s == nil || s.rt == nil {
		return nil
	}
	s.harnessMu.Lock()
	defer s.harnessMu.Unlock()
	c, ok, err := session.ReadControl(s.rt.SessionDir, id)
	if err != nil {
		return err
	}
	if !ok || c.State == session.ControlReleased || c.State == state {
		return nil
	}
	c, err = session.ChangeControl(s.rt.SessionDir, id, c.ManagerID, state, c.Revision)
	if err != nil {
		return err
	}
	s.revokeSessionInputs(id)
	s.publishSessionControl(id)
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
		Body:      fmt.Sprintf("The user changed control of Harness session %s to %s. Automatic instructions and follow-up for this session are paused. Keep its progress; do not resume or create a replacement unless the user explicitly asks. Other tasks are unaffected.", id, state),
	})
	return err
}

func (s *Server) failHarnessOperation(ctx context.Context, op *channels.HarnessOperation, reason string) error {
	if err := s.validateHarnessScope(ctx, op.Actor, false); err != nil {
		op.State, op.Error = "failed", reason
		return s.channelService.PutHarnessOperation(ctx, *op)
	}
	_, err := s.channelService.EnqueueSessionResult(ctx, channels.SessionResultEnqueueParams{ParentSessionRef: op.Actor.SessionRef, ParentTurnID: op.Actor.TurnID, SourceSessionRef: op.Params.SessionID, RequestID: op.ID + ":failure", Body: "Session operation failed: " + reason + ". Inspect the existing session before deciding whether to retry."})
	if err != nil {
		return err
	}
	op.State, op.Error = "failed", reason
	return s.channelService.PutHarnessOperation(ctx, *op)
}
