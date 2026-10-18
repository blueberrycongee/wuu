package appserver

import (
	"strings"

	"github.com/blueberrycongee/wuu/internal/providers"
)

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
		if s.rt.UserBinding().IsUser() {
			th.mu.Lock()
			owner := th.Owner
			th.mu.Unlock()
			if pluginID, owned := strings.CutPrefix(owner, "plugin:"); owned && pluginID != "" && !s.rt.UserHostPluginAvailable(pluginID) {
				// Do not retain autonomous execution merely because the disabled
				// extension's conversation still pins its old runtime. Keep the
				// session and its named-agent binding for an explicit later resume.
				th.mu.Lock()
				th.workerTreeFrozen = true
				cancel := th.cancel
				th.mu.Unlock()
				if _, err := s.interruptThreadExecution(th.ID, "", ""); err != nil {
					// A held-input persistence failure must not leave the provider
					// executing after its owner has been disabled.
					if cancel != nil {
						cancel()
					}
					providers.DebugLogf("cancel unavailable user-host plugin %q session %q: %v", pluginID, th.ID, err)
				}
				// Idle sessions can still have accepted queue entries. The shared
				// interrupt core drains these only when a foreground turn exists.
				queued := s.discardQueuedTurns(th.ID)
				for _, entry := range queued {
					s.notifyPluginTurnDiscarded(th.ID, entry, "session owner plugin is unavailable")
				}
				s.notifyQueuedTurnsDequeued(th.ID, queuedTurnIDs(queued))
			}
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
