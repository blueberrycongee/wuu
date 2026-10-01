package appserver

import (
	"context"
	"errors"
	"time"

	"github.com/blueberrycongee/wuu/internal/session"
	"github.com/blueberrycongee/wuu/internal/tools"
)

const (
	projectWaitDefault = time.Minute
	projectWaitMaximum = 5 * time.Minute
)

func (s *Server) finishProjectDispatch(ctx context.Context, project, actor session.Session, clientID string, request tools.ProjectSessionRequest, result any, err error) (any, error) {
	if err != nil {
		return nil, err
	}
	block := request.Action == "side"
	if request.Block != nil {
		block = *request.Block
	}
	if !block {
		return result, nil
	}
	view := result.(projectSessionView)
	request.SessionID, request.TurnID = view.SessionID, view.TurnID
	// Legacy create-side can return an existing side without dispatching.
	recorded, err := session.InboxHas(s.rt.SessionDir, clientID)
	if err != nil {
		return nil, err
	}
	if !recorded {
		clientID = ""
	} else {
		// Follow the consumed dispatch, not a volatile steer's provisional turn.
		request.TurnID = ""
	}
	return s.waitProjectSession(ctx, project, actor, request, clientID)
}

// Waiting observes one dispatch/turn; it never cancels the target execution.
func (s *Server) waitProjectSession(ctx context.Context, project, actor session.Session, request tools.ProjectSessionRequest, clientID string) (any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	metadata, err := s.projectManagedSession(project.ID, request.SessionID)
	if err != nil {
		return nil, err
	}
	if actor.ID != project.ID && (actor.ProjectRole != "side" || projectRoleForSession(metadata) != "worker") {
		return nil, errors.New("only the lead can wait for members; the side can wait for workers")
	}
	timeout := projectWaitDefault
	if request.TimeoutMS < 0 {
		return nil, errors.New("timeout_ms must be positive")
	}
	if request.TimeoutMS > 0 {
		// Compare before conversion to avoid duration overflow.
		if request.TimeoutMS >= int(projectWaitMaximum/time.Millisecond) {
			timeout = projectWaitMaximum
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
		if _, _, _, err := s.projectActor(actor.ID); err != nil {
			return nil, err
		}
		if _, _, _, err := s.projectActor(metadata.ID); err != nil {
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
		turns, err := s.loadDurableProjectTurns(metadata.ID)
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
		view, err := s.projectSessionView(metadata)
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
		case <-deadline.C:
			view.TimedOut = true
			return view, nil
		case <-idle:
			idle = nil
		case <-recheck.C:
		}
	}
}
