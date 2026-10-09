package appserver

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
	for _, th := range threads {
		if th == nil {
			continue
		}
		th.mu.Lock()
		rt := th.execRuntime
		if rt != nil && rt.PluginGeneration != nil &&
			!th.running && !th.admissionReserved && th.executionLease == nil && !th.runtimeSelectionMutation &&
			!s.rt.IsCurrentPluginGeneration(rt.PluginGeneration) && !threadRuntimeHasOutstandingWork(th.ID, rt) {
			retired = append(retired, detachThreadRuntimeLocked(th))
			th.maybeReleasePluginGenerationExecutionLeaseLocked()
		}
		th.mu.Unlock()
	}
	// Shutdown can wait for finalizers and host calls. Neither server nor thread
	// locks may be held while the detached generation drains.
	for _, detached := range retired {
		s.releaseDetachedThreadRuntime(detached)
	}
}
