package appserver

import (
	"context"
	"errors"
	"strings"

	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/session"
)

func (s *Server) controlPluginSession(_ context.Context, pluginID string, p pluginhost.SessionControlParams) (pluginhost.SessionControlResult, error) {
	s.harnessMu.Lock()
	defer s.harnessMu.Unlock()
	manager := "plugin:" + strings.TrimSpace(pluginID)
	m, ok, err := session.Find(s.rt.SessionDir, p.SessionID)
	if err != nil {
		return pluginhost.SessionControlResult{}, err
	}
	if !ok {
		return pluginhost.SessionControlResult{}, session.ErrSessionNotFound
	}
	if m.Visibility == "plugin" && m.Owner != manager || isNamedAgentSessionSource(m.Source) {
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
	if err != nil || !ok || c.State == session.ControlReleased {
		return nil, err
	}
	name := c.ManagerID
	if s.channelService != nil {
		if agent, err := s.channelService.GetAgentRuntime(context.Background(), c.ManagerID); err == nil {
			name = agent.Name
		}
	}
	return &ThreadSessionControl{ManagerID: c.ManagerID, ManagerName: name, State: c.State, Revision: c.Revision}, nil
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
