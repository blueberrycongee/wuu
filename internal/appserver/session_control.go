package appserver

import (
	"context"
	"errors"
	"strings"

	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/session"
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
	if !ok || c.State == session.ControlReleased || c.State == state {
		return nil
	}
	c, err = session.ChangeControl(s.rt.SessionDir, id, c.ManagerID, state, c.Revision)
	if err != nil {
		return err
	}
	s.revokeSessionInputs(id)
	s.publishSessionControl(id)
	s.noticeProjectControl(id, c)
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

// handleThreadControl is a human action: it returns a taken-over or paused
// session to its project. Model and extension control goes through their
// owner-fenced APIs and cannot use this path.
func (s *Server) handleThreadControl(_ context.Context, req Request) error {
	var p struct {
		ThreadID string `json:"thread_id"`
		Revision int64  `json:"revision"`
	}
	if err := decodeParams(req.Params, &p); err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	s.controlMu.Lock()
	c, exists, err := session.ReadControl(s.rt.SessionDir, p.ThreadID)
	if err == nil && (!exists || c.Revision != p.Revision) {
		err = session.ErrControlChanged
	}
	if err == nil {
		_, err = s.projectManagedSession(c.ManagerID, p.ThreadID)
	}
	if err == nil && c.State != session.ControlActive {
		err = s.settleUserControlledTurn(c.ManagerID, p.ThreadID)
	}
	if err == nil && c.State != session.ControlActive {
		c, err = session.ChangeControl(s.rt.SessionDir, p.ThreadID, c.ManagerID, session.ControlActive, c.Revision)
	}
	s.controlMu.Unlock()
	if err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	s.publishSessionControl(p.ThreadID)
	s.noticeProjectControl(p.ThreadID, c)
	return s.writeResponse(req.ID, map[string]any{"control": s.threadSessionControl(p.ThreadID, c)}, nil)
}
