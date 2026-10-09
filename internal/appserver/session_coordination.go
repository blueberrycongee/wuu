package appserver

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/session"
	"github.com/blueberrycongee/wuu/internal/tools"
)

const (
	sessionWaitDefault = time.Minute
	sessionWaitMaximum = 5 * time.Minute
)

type managedSessionView struct {
	Role                  string     `json:"role"`
	SessionID             string     `json:"session_id"`
	Title                 string     `json:"title"`
	State                 string     `json:"state"`
	Control               string     `json:"control,omitempty"`
	Workspace             string     `json:"workspace"`
	LatestCompletedTurnID string     `json:"latest_completed_turn_id,omitempty"`
	TurnID                string     `json:"turn_id,omitempty"`
	TurnStatus            TurnStatus `json:"turn_status,omitempty"`
	FinalOutput           string     `json:"final_output,omitempty"`
	ClientID              string     `json:"client_id,omitempty"`
	DeliveryState         string     `json:"delivery_state,omitempty"`
	Error                 *TurnError `json:"error,omitempty"`
	TimedOut              bool       `json:"timed_out,omitempty"`
}

func (s *Server) managedSessionView(metadata session.Session) (managedSessionView, error) {
	view := managedSessionView{Role: projectRoleForSession(metadata), SessionID: metadata.ID, Title: metadata.Title, State: "idle", Workspace: "shared", LatestCompletedTurnID: metadata.LatestCompletedTurnID}
	if metadata.Source == fusionSideSource {
		view.Role = "sidekick"
	}
	if metadata.WorktreePath != "" {
		view.Workspace = "worktree"
	}
	if active, err := session.ThreadExecutionActive(s.rt.SessionDir, metadata.ID); err != nil {
		return view, err
	} else if active {
		view.State = "running"
	}
	if metadata.Source == fusionSideSource {
		if th := s.thread(metadata.ID); th != nil {
			th.mu.Lock()
			if threadRuntimeHasOutstandingWork(th.ID, th.execRuntime) {
				view.State = "running"
			}
			th.mu.Unlock()
		}
	}
	if control, ok, err := session.ReadControl(s.rt.SessionDir, metadata.ID); err != nil {
		return view, err
	} else if ok {
		view.Control = control.State
	}
	return view, nil
}

func (s *Server) dispatchSessionInput(metadata session.Session, clientID string) (managedSessionView, error) {
	s.drainSessionInbox(metadata.ID)
	return s.sessionDispatchReceipt(metadata, clientID)
}

func (s *Server) sessionDispatchReceipt(metadata session.Session, clientID string) (managedSessionView, error) {
	view, err := s.managedSessionView(metadata)
	if err != nil {
		return view, err
	}
	view.ClientID = clientID
	view.DeliveryState = "queued"
	th, err := s.ensureThreadLoaded(metadata.ID)
	if err != nil {
		return view, err
	}
	// A pending steer may still move to the next turn. Publish an exact turn
	// only once the dispatch is consumed into that turn's user items.
	th.mu.Lock()
	for _, turn := range th.Turns {
		for _, item := range turn.Items {
			if item.Type == ThreadItemUserMessage && item.SourceID == clientID {
				view.TurnID = turn.ID
			}
		}
	}
	for _, input := range th.pendingSteers {
		if input.ClientID == clientID {
			view.DeliveryState = "steering"
		}
	}
	th.mu.Unlock()
	if view.TurnID == "" {
		if turns, err := s.loadDurableSessionTurns(metadata.ID); err != nil {
			return view, err
		} else {
			for _, turn := range turns {
				for _, item := range turn.Items {
					if item.SourceID == clientID {
						view.TurnID = turn.ID
					}
				}
			}
		}
	}
	if view.TurnID != "" {
		view.DeliveryState = "consumed"
		return view, nil
	}
	if pending, err := session.PendingInbox(s.rt.SessionDir, metadata.ID); err != nil {
		return view, err
	} else {
		for _, input := range pending {
			if input.ClientID == clientID {
				view.State = "queued"
				break
			}
		}
	}
	return view, nil
}

// waitSessionDispatch is shared by Project Agent and Fusion. It follows the
// persisted input ID through steering and checks exact terminal evidence.
func (s *Server) waitSessionDispatch(ctx context.Context, metadata session.Session, request tools.ProjectSessionRequest, clientID string, validate func() error, wake <-chan struct{}) (any, error) {
	timeout := sessionWaitDefault
	if request.TimeoutMS < 0 {
		return nil, errors.New("timeout_ms must be positive")
	}
	if request.TimeoutMS > 0 {
		// Compare before conversion to avoid duration overflow.
		if request.TimeoutMS >= int(sessionWaitMaximum/time.Millisecond) {
			timeout = sessionWaitMaximum
		} else {
			timeout = time.Duration(request.TimeoutMS) * time.Millisecond
		}
	}
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	// Other hosts do not share idle signals. Recheck admission and control
	// fences without generating model polling turns.
	recheck := time.NewTicker(100 * time.Millisecond)
	defer recheck.Stop()
	th, err := s.ensureThreadLoaded(metadata.ID)
	if err != nil {
		return nil, err
	}
	th.mu.Lock()
	idle := th.addIdleWaiterLocked()
	th.mu.Unlock()
	defer func() { th.mu.Lock(); th.removeIdleWaiterLocked(idle); th.mu.Unlock() }()
	turnID := request.TurnID
	for {
		if err := validate(); err != nil {
			return nil, err
		}
		if clientID != "" {
			if err := session.ValidateInboxControls(s.rt.SessionDir, clientID); err != nil {
				return nil, err
			}
			if turnID == "" {
				s.drainSessionInbox(metadata.ID)
			}
		}
		turns, err := s.loadDurableSessionTurns(metadata.ID)
		if err != nil {
			return nil, err
		}
		if turnID == "" {
			if clientID == "" && len(turns) > 0 {
				turnID = turns[len(turns)-1].ID
			} else {
				for _, turn := range turns {
					for _, item := range turn.Items {
						if item.Type == ThreadItemUserMessage && item.SourceID == clientID {
							turnID = turn.ID
						}
					}
				}
			}
		}
		view, err := s.managedSessionView(metadata)
		if err != nil {
			return nil, err
		}
		view.TurnID = turnID
		found := false
		for _, turn := range turns {
			if turn.ID == turnID {
				found = true
				view.TurnStatus = turn.Status
				if turn.Status != TurnStatusInProgress {
					view.FinalOutput = finalAnswerText(turn)
					view.Error = turn.Error
				}
				break
			}
		}
		if found && view.TurnStatus != TurnStatusInProgress {
			return view, nil
		}
		if !found && turnID != "" {
			return nil, errors.New("turn not found in target session")
		}
		if turnID == "" {
			if clientID == "" {
				return nil, errors.New("target session has no turn to wait for")
			}
			pending, err := session.PendingInbox(s.rt.SessionDir, metadata.ID)
			if err != nil {
				return nil, err
			}
			queued := false
			for _, input := range pending {
				queued = queued || input.ClientID == clientID
			}
			if queued {
				view.State = "queued"
			}
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-wake:
			view.State = "lead_input"
			return view, nil
		case <-deadline.C:
			view.TimedOut = true
			return view, nil
		case <-idle:
			idle = nil
		case <-recheck.C:
		}
	}
}

// loadDurableSessionTurns reads output and terminal evidence from one active
// physical transcript. Provider checkpoints may omit terminal metadata; cached
// UI turns can default unfinished history to completed while a later turn runs.
func (s *Server) loadDurableSessionTurns(id string) ([]Turn, error) {
	history, err := loadPersistedMessages(s.rt.SessionDir, id, true)
	if err != nil {
		return nil, err
	}
	turns := turnsFromPersistedHistory(id, history, time.Now().UTC(), s.resolveParticipantSummary)
	terminals := make(map[string]persistedMessage)
	for _, record := range history {
		if record.Role == "meta" && record.Content == turnTerminalHistoryRecord && record.ClientID != "" {
			if _, ok := parseTurnTerminalStatus(record.StopReason); ok {
				terminals[record.ClientID] = record
			}
		}
	}
	// A missing lease is not proof of success after a crash. A later turn's
	// lease likewise must not hide an earlier exact terminal result.
	for index := range turns {
		turns[index].Status = TurnStatusInProgress
		if terminal, ok := terminals[turns[index].ID]; ok {
			turns[index].Status = TurnStatus(terminal.StopReason)
			applyTokenUsageToTurn(&turns[index], providers.TokenUsage{InputTokens: terminal.InputTokens, OutputTokens: terminal.OutputTokens, CacheReadTokens: terminal.CacheReadTokens, CacheCreationTokens: terminal.CacheCreationTokens}, 0, terminal.Model)
		}
	}
	return turns, nil
}

// ensureOwnedThreadLoaded loads a session only in the host that executes its
// workspace; another project's host leaves it alone.
func (s *Server) ensureOwnedThreadLoaded(id string) (*threadState, error) {
	metadata, found, err := session.Find(s.rt.SessionDir, id)
	if err != nil || !found || metadata.ArchivedAt != nil {
		return nil, err
	}
	root, workspaceID, err := s.sessionWorkspace(metadata)
	if err != nil || !s.ownsSessionWorkspace(root, workspaceID) {
		return nil, err
	}
	return s.ensureThreadLoaded(id)
}

// drainSessionInbox hands pending host input to its session: it steers a
// running turn or, for input that wakes the session, starts one. Input that
// cannot be admitted now stays pending for the session's next turn start or
// end, the next delivery, or the next start-up.
func (s *Server) drainSessionInboxTarget(target string, allowStart bool) (capacityFull bool) {
	s.inboxMu.Lock()
	defer s.inboxMu.Unlock()
	th, err := s.ensureOwnedThreadLoaded(target)
	if err != nil || th == nil || projectExecutionDisabled(th.Source) {
		return
	}
	pending, err := session.PendingInbox(s.rt.SessionDir, target)
	if err != nil {
		providers.DebugLogf("read inbox for %q: %v", target, err)
		return
	}
	valid := pending[:0]
	for _, message := range pending {
		// Old pending control notices no longer describe the project's policy;
		// retain delivered history, but do not replay their return-control advice.
		obsolete := message.Cause == projectCauseTakeover || message.Cause == projectCausePause || message.Cause == projectCauseReturn
		if err := session.ValidateInboxControls(s.rt.SessionDir, message.ClientID); err != nil {
			if !errors.Is(err, session.ErrControlChanged) {
				providers.DebugLogf("validate work inbox: %v", err)
				return
			}
			obsolete = true
		}
		for _, control := range message.Controls {
			if err := session.ValidateControl(s.rt.SessionDir, control); err != nil {
				if !errors.Is(err, session.ErrControlChanged) {
					providers.DebugLogf("validate inbox %q: %v", message.ClientID, err)
					return
				}
				obsolete = true
				break
			}
		}
		if message.Cause == "project_message" || message.Cause == "project" {
			sourceProject, _, _, sourceErr := s.projectActor(message.RelatedSessionID)
			targetProject, _, _, targetErr := s.projectActor(target)
			obsolete = obsolete || sourceErr != nil || targetErr != nil || sourceProject.ID != targetProject.ID
		}
		if message.Cause == "fusion" {
			_, _, err := s.fusionActor(message.RelatedSessionID, target)
			obsolete = obsolete || err != nil
		}
		if message.Cause == "fusion_result" {
			_, _, err := s.fusionActor(target, message.RelatedSessionID)
			obsolete = obsolete || err != nil
		}
		if obsolete {
			if err := session.SettleInbox(s.rt.SessionDir, message.ClientID, target); err != nil {
				providers.DebugLogf("discard obsolete inbox %q: %v", message.ClientID, err)
				return
			}
			continue
		}
		valid = append(valid, message)
	}
	pending = valid
	wake := slices.ContainsFunc(pending, func(message session.InboxMessage) bool { return message.Wake })
	for _, message := range pending {
		msg := providers.ChatMessage{
			Role: "user", Content: message.Content, ClientID: message.ClientID,
			Origin: sessionInputHost, Cause: message.Cause,
			PresentationKind: sessionPresentationMessage, RelatedSessionID: message.RelatedSessionID, ReadOnly: true,
		}
		if related, found, err := session.Find(s.rt.SessionDir, message.RelatedSessionID); err == nil && found {
			msg.Name = related.Title
		}
		permissions, err := s.resolveThreadTurnPermissions(th, nil)
		if err != nil {
			providers.DebugLogf("resolve inbox permissions for %q: %v", target, err)
			return
		}
		snapshot := turnRuntimeSnapshot{}.withPermissions(permissions)
		for _, control := range message.Controls {
			if control.SessionID == target {
				snapshot.Control = &control
			}
		}
		if !wake || !allowStart {
			// Do not fall through to starting a turn if the target became idle
			// between inspecting the inbox and admitting this informational input.
			if _, ok := s.steerSessionInput(th, msg, snapshot); !ok {
				return
			}
			continue
		}
		if _, ok, err := s.trySubmitSessionInput(context.Background(), th, msg, sessionIfRunningSteer, snapshot); err != nil || !ok {
			if err != nil {
				providers.DebugLogf("deliver inbox %q: %v", message.ClientID, err)
				capacityFull = errors.Is(err, session.ErrProjectWorkerCapacity)
			}
			return
		}
	}
	return
}
