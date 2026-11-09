package appserver

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"

	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/runtime"
	"github.com/blueberrycongee/wuu/internal/session"
)

type sessionControlLock struct {
	mu   sync.Mutex
	refs int
}

// Control transactions serialize only the sessions whose fences they inspect
// or change. Retain every lock before waiting and take multi-session locks in
// sorted order, so peer messages cannot deadlock opposite-direction messages.
// The registry mutex never covers provider work or waits for a session lock.
func (s *Server) lockSessionControls(ids ...string) func() {
	ids = append([]string(nil), ids...)
	for i := range ids {
		ids[i] = strings.TrimSpace(ids[i])
	}
	slices.Sort(ids)
	ids = slices.Compact(ids)
	s.controlLocksMu.Lock()
	if s.controlLocks == nil {
		s.controlLocks = make(map[string]*sessionControlLock)
	}
	locks := make([]*sessionControlLock, len(ids))
	for i, id := range ids {
		lock := s.controlLocks[id]
		if lock == nil {
			lock = &sessionControlLock{}
			s.controlLocks[id] = lock
		}
		lock.refs++
		locks[i] = lock
	}
	s.controlLocksMu.Unlock()
	for _, lock := range locks {
		lock.mu.Lock()
	}
	return func() {
		for i := len(locks) - 1; i >= 0; i-- {
			locks[i].mu.Unlock()
		}
		s.controlLocksMu.Lock()
		defer s.controlLocksMu.Unlock()
		for i, lock := range locks {
			lock.refs--
			if lock.refs == 0 {
				delete(s.controlLocks, ids[i])
			}
		}
	}
}

func (s *Server) controlPluginSession(binding *runtime.State, ctx context.Context, pluginID string, p pluginhost.SessionControlParams) (pluginhost.SessionControlResult, error) {
	var restoreErr error
	ctx, restoreErr = s.restoreUserSessionAuthorization(ctx, pluginID, p.ExecutionAuthorization, "control", p.SessionID)
	if restoreErr != nil {
		return pluginhost.SessionControlResult{}, restoreErr
	}
	if err := s.authorizeUserSessionOperation(ctx, "control", p.SessionID); err != nil {
		return pluginhost.SessionControlResult{}, err
	}
	unlock := s.lockSessionControls(p.SessionID)
	defer unlock()
	// Authority may have been revoked while waiting for a same-session fence.
	if err := s.authorizeUserSessionOperation(ctx, "control", p.SessionID); err != nil {
		return pluginhost.SessionControlResult{}, err
	}
	manager := "plugin:" + strings.TrimSpace(pluginID)
	m, ok, err := binding.Sessions.Find(p.SessionID)
	if err != nil {
		return pluginhost.SessionControlResult{}, err
	}
	if !ok {
		return pluginhost.SessionControlResult{}, session.ErrSessionNotFound
	}
	if err := s.requireUserSessionBinding(m); err != nil {
		return pluginhost.SessionControlResult{}, err
	}
	if m.Visibility == "plugin" && m.Owner != manager {
		return pluginhost.SessionControlResult{}, errors.New("session control requires a shared or owned ordinary session")
	}
	c, _, err := binding.Sessions.ReadControl(p.SessionID)
	if err != nil {
		return pluginhost.SessionControlResult{}, err
	}
	if p.State != "" {
		if p.State != session.ControlActive && p.State != session.ControlPaused && p.State != session.ControlReleased {
			return pluginhost.SessionControlResult{}, errors.New("state must be active, paused, or released")
		}
		c, err = binding.Sessions.ChangeControl(p.SessionID, manager, p.State, p.Revision)
		if err != nil {
			return pluginhost.SessionControlResult{}, err
		}
		s.revokeSessionInputs(binding, p.SessionID)
		s.publishSessionControl(binding, p.SessionID)
	}
	return pluginhost.SessionControlResult{SessionID: p.SessionID, ManagerID: c.ManagerID, State: c.State, Revision: c.Revision}, nil
}

func (s *Server) pluginSessionControl(binding *runtime.State, pluginID, id string, revision int64) (*session.Control, error) {
	c, ok, err := binding.Sessions.ReadControl(id)
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

func (s *Server) readThreadSessionControl(binding *runtime.State, id string) (*ThreadSessionControl, error) {
	c, ok, err := binding.Sessions.ReadControl(id)
	if err != nil || !ok {
		return nil, err
	}
	return threadSessionControl(c), nil
}

// threadSessionControl exposes the plugin manager of a managed session.
func threadSessionControl(c session.Control) *ThreadSessionControl {
	if c.ManagerID == "" || c.State == session.ControlReleased {
		return nil
	}
	if !strings.HasPrefix(c.ManagerID, "plugin:") {
		return nil
	}
	return &ThreadSessionControl{ManagerID: c.ManagerID, ManagerName: c.ManagerID, State: c.State, Revision: c.Revision}
}

// takeSessionControlForInput yields managed sessions to explicit user input.
func (s *Server) takeSessionControlForInput(binding *runtime.State, id string, timings ...*rpcRequestTiming) error {
	return s.takeSessionControl(binding, id, session.ControlTakenOver, timings...)
}

// takeSessionControl records a human takeover or pause of a managed session.
// Admitted automatic instructions are revoked before the manager is told.
func (s *Server) takeSessionControl(binding *runtime.State, id, state string, timings ...*rpcRequestTiming) error {
	if s == nil || s.rt == nil {
		return nil
	}
	var timing *rpcRequestTiming
	if len(timings) != 0 {
		timing = timings[0].buffered()
	}
	defer timing.flush()
	started := timing.now()
	unlock := s.lockSessionControls(id)
	timing.span("session_control_mutex_wait", started)
	defer unlock()
	started = timing.now()
	c, ok, err := binding.Sessions.ReadControl(id)
	timing.span("session_control_read", started)
	if err != nil {
		return err
	}
	if !ok || c.State == session.ControlReleased {
		return nil
	}
	if c.State == state {
		return nil
	}
	if _, err := binding.Sessions.ChangeControl(id, c.ManagerID, state, c.Revision); err != nil {
		return err
	}
	s.revokeSessionInputs(binding, id)
	s.publishSessionControl(binding, id)
	return nil
}

func (s *Server) publishSessionControl(binding *runtime.State, id string) {
	th := s.threadForState(binding, id)
	if th == nil {
		return
	}
	c, err := s.readThreadSessionControl(binding, id)
	if err != nil {
		return
	}
	th.mu.Lock()
	th.SessionControl = c
	snapshot := th.snapshotLocked()
	th.mu.Unlock()
	_ = s.notifyThreadUpdated(binding, snapshot)
}
