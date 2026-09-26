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

func (s *Server) threadSessionControl(id string, c session.Control) *ThreadSessionControl {
	if c.ManagerID == "" || c.State == session.ControlReleased {
		return nil
	}
	return &ThreadSessionControl{ManagerID: c.ManagerID, ManagerName: c.ManagerID, State: c.State, Revision: c.Revision}
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

// handleThreadControl is a human action. Model and extension control continues
// through its owner-fenced API; it cannot invoke this return-to-manager path.
func (s *Server) handleThreadControl(_ context.Context, req Request) error {
	return s.writeResponse(req.ID, nil, errors.New("no manager accepts returned sessions"))
}
