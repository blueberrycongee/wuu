package appserver

import (
	"github.com/blueberrycongee/wuu/internal/runtime"
	"github.com/blueberrycongee/wuu/internal/session"
)

type sessionCacheKey struct {
	store *session.Store
	id    string
}

// Cached projections belong to the authority that produced them. A request
// against a replacement authority must load its own projection.
func (s *Server) threadForState(binding *runtime.State, id string) *threadState {
	th := s.thread(id)
	if th == nil || th.state == nil || th.state.Sessions != binding.Sessions {
		return nil
	}
	return th
}

// Transfer one reference before dispatch, including a dispatch rejected during
// shutdown. The caller may close its request capture as soon as this returns.
func (s *Server) startBackgroundWithState(binding *runtime.State, work func(*runtime.State)) bool {
	held := binding.Retain()
	if held == nil {
		return false
	}
	if !s.startBackground(func() { defer held.Close(); work(held) }) {
		_ = held.Close()
		return false
	}
	return true
}

func (s *Server) reserveBackgroundWithState(binding *runtime.State, work func(*runtime.State)) (*backgroundLaunch, bool) {
	launch := &backgroundLaunch{decision: make(chan bool, 1)}
	if !s.startBackgroundWithState(binding, func(held *runtime.State) {
		if <-launch.decision {
			work(held)
		}
	}) {
		return nil, false
	}
	return launch, true
}
