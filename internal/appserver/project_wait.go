package appserver

import (
	"context"
	"errors"

	"github.com/blueberrycongee/wuu/internal/session"
	"github.com/blueberrycongee/wuu/internal/tools"
)

func (s *Server) finishProjectDispatch(ctx context.Context, project, actor session.Session, clientID string, request tools.ProjectSessionRequest, result any, err error) (any, error) {
	if err != nil {
		return nil, err
	}
	block := false
	if request.Block != nil {
		block = *request.Block
	}
	if actor.ID == project.ID || !block {
		return result, nil
	}
	view := result.(managedSessionView)
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
	return s.waitSessionDispatch(ctx, metadata, request, clientID, func() error {
		if _, _, _, err := s.projectActor(actor.ID); err != nil {
			return err
		}
		if _, _, _, err := s.projectActor(metadata.ID); err != nil {
			return err
		}
		return nil
	}, nil)
}
