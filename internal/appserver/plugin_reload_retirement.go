package appserver

import "github.com/blueberrycongee/wuu/internal/runtime"

// retireIdlePluginRuntimes releases superseded generations without waiting for
// another user turn. Call after publishing a generation and releasing the
// refresh mutex: turn admission takes that mutex while holding a thread lock.
func (s *Server) retireIdlePluginRuntimes() {
	if s == nil || s.rt == nil || s.closed.Load() {
		return
	}
	s.mu.Lock()
	threads := make([]*threadState, 0, len(s.threads))
	for _, th := range s.threads {
		threads = append(threads, th)
	}
	s.mu.Unlock()
	var retired []detachedThreadRuntime
	var states []*runtime.State
	for _, th := range threads {
		if th == nil {
			continue
		}
		th.mu.Lock()
		rt := th.execRuntime
		if th.state != nil && th.state.Generation != nil &&
			!th.running && !th.admissionReserved && th.executionLease == nil && !th.runtimeSelectionMutation &&
			!th.nativeHistoryMutation && s.activeExecutionRunID(th.ID) == "" &&
			(!s.rt.IsCurrentPluginGeneration(th.state.Generation) || (rt != nil && rt.CompositionID != "")) && !threadRuntimeHasOutstandingWork(th.ID, rt) {
			retired = append(retired, detachThreadRuntimeLocked(th))
			th.maybeReleasePluginGenerationExecutionLeaseLocked()
			if !s.rt.IsCurrentPluginGeneration(th.state.Generation) {
				states = append(states, th.state)
			}
		}
		th.mu.Unlock()
	}
	// Shutdown can wait for finalizers and host calls. Neither server nor thread
	// locks may be held while the detached generation drains.
	for _, detached := range retired {
		s.releaseDetachedThreadRuntime(detached)
	}
	// Keep the display projection cached, but release its superseded ownership.
	// The next request loads through its own captured store; running callbacks
	// already hold independent State references through their final writes.
	for _, state := range states {
		_ = state.Close()
	}
}
