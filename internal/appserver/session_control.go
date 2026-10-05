package appserver

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/session"
	"github.com/blueberrycongee/wuu/internal/tools"
)

func (s *Server) controlPluginSession(_ context.Context, pluginID string, p pluginhost.SessionControlParams) (pluginhost.SessionControlResult, error) {
	s.controlMu.Lock()
	defer s.controlMu.Unlock()
	manager := "plugin:" + strings.TrimSpace(pluginID)
	m, ok, err := session.Find(s.rt.SessionDir, p.SessionID)
	if err != nil {
		return pluginhost.SessionControlResult{}, err
	}
	if !ok {
		return pluginhost.SessionControlResult{}, session.ErrSessionNotFound
	}
	if m.Visibility == "plugin" && m.Owner != manager {
		return pluginhost.SessionControlResult{}, errors.New("session control requires a shared or owned ordinary session")
	}
	c, _, err := session.ReadControl(s.rt.SessionDir, p.SessionID)
	if err != nil {
		return pluginhost.SessionControlResult{}, err
	}
	if p.State != "" {
		if p.State != session.ControlActive && p.State != session.ControlPaused && p.State != session.ControlReleased {
			return pluginhost.SessionControlResult{}, errors.New("state must be active, paused, or released")
		}
		c, err = session.ChangeControl(s.rt.SessionDir, p.SessionID, manager, p.State, p.Revision)
		if err != nil {
			return pluginhost.SessionControlResult{}, err
		}
		s.revokeSessionInputs(p.SessionID)
		s.publishSessionControl(p.SessionID)
	}
	return pluginhost.SessionControlResult{SessionID: p.SessionID, ManagerID: c.ManagerID, State: c.State, Revision: c.Revision}, nil
}

func (s *Server) pluginSessionControl(pluginID, id string, revision int64) (*session.Control, error) {
	c, ok, err := session.ReadControl(s.rt.SessionDir, id)
	if err != nil {
		return nil, err
	}
	if !ok || c.State == session.ControlReleased {
		return nil, nil
	}
	if c.ManagerID != "plugin:"+pluginID || c.Revision != revision {
		return nil, session.ErrControlChanged
	}
	return &c, nil
}

func (s *Server) readThreadSessionControl(id string) (*ThreadSessionControl, error) {
	c, ok, err := session.ReadControl(s.rt.SessionDir, id)
	if err != nil || !ok {
		return nil, err
	}
	return s.threadSessionControl(id, c), nil
}

// threadSessionControl names the manager. A fence left by a project that was
// archived or deleted no longer manages the session, so it is not shown.
func (s *Server) threadSessionControl(_ string, c session.Control) *ThreadSessionControl {
	if c.ManagerID == "" || c.State == session.ControlReleased {
		return nil
	}
	name := c.ManagerID
	if !strings.HasPrefix(c.ManagerID, "plugin:") {
		project, live := s.projectCoordinator(c.ManagerID)
		if !live {
			return nil
		}
		name = project.Title
	}
	return &ThreadSessionControl{ManagerID: c.ManagerID, ManagerName: name, State: c.State, Revision: c.Revision}
}

// takeSessionControlForInput applies the user's message to a managed session.
// A project keeps its control state: a managed session goes on under the
// project, which learns what the user wrote from the turn's result, and a
// session the user holds stays theirs. Other managers yield to the user.
func (s *Server) takeSessionControlForInput(id string) error {
	if s == nil || s.rt == nil {
		return nil
	}
	c, ok, err := session.ReadControl(s.rt.SessionDir, id)
	if err != nil {
		return err
	}
	if ok && c.State != session.ControlReleased && !strings.HasPrefix(c.ManagerID, "plugin:") {
		if _, live := s.projectCoordinator(c.ManagerID); live {
			return nil
		}
	}
	return s.takeSessionControl(id, session.ControlTakenOver)
}

// takeSessionControl records a human takeover or pause of a managed session.
// Admitted automatic instructions are revoked before the manager is told.
func (s *Server) takeSessionControl(id, state string) error {
	if s == nil || s.rt == nil {
		return nil
	}
	s.controlMu.Lock()
	defer s.controlMu.Unlock()
	c, ok, err := session.ReadControl(s.rt.SessionDir, id)
	if err != nil {
		return err
	}
	if !ok || c.State == session.ControlReleased {
		return nil
	}
	project, live := s.projectCoordinator(c.ManagerID)
	if live {
		// Stop revokes previously admitted inputs, not membership. A later
		// explicit instruction uses the new revision and remains admissible.
		state = session.ControlActive
	} else if c.State == state {
		return nil
	}
	c, err = session.ChangeControl(s.rt.SessionDir, id, c.ManagerID, state, c.Revision)
	if err != nil {
		return err
	}
	s.revokeSessionInputs(id)
	s.publishSessionControl(id)
	if live {
		if w, found, err := s.workForMember(project.ID, id); err != nil {
			return err
		} else if found && w.Phase != "stopped" {
			if _, err := s.projectWork(context.Background(), project.ID, fmt.Sprintf("member-stop:%s:%d", id, c.Revision), tools.ProjectWorkRequest{Operation: "stop", WorkID: w.ID, Revision: w.Revision}); err != nil {
				return err
			}
		}
	}

	if live {
		s.enqueueProjectInput(project.ID, id, fmt.Sprintf("project-control:%s:%d", id, c.Revision), projectCauseStopped,
			"The user stopped this session. Respect their stop intent; do not automatically restart the stopped work. The session remains a project member and may receive new instructions later.", false)
	}
	return nil
}

func (s *Server) publishSessionControl(id string) {
	th := s.thread(id)
	if th == nil {
		return
	}
	c, err := s.readThreadSessionControl(id)
	if err != nil {
		return
	}
	th.mu.Lock()
	th.SessionControl = c
	snapshot := th.snapshotLocked()
	th.mu.Unlock()
	_ = s.notifyThreadUpdated(snapshot)
}

// Legacy clients must not recreate the removed project takeover workflow.
func (s *Server) handleThreadTakeControl(req Request) error {
	return s.writeResponse(req.ID, nil, errors.New("project takeover is no longer supported; send a message or stop the current turn instead"))
}

func (s *Server) handleThreadControl(_ context.Context, req Request) error {
	return s.writeResponse(req.ID, nil, errors.New("returning project control is no longer supported; project membership remains active after user intervention"))
}
