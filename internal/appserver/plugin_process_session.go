package appserver

import (
	"context"
	"strings"

	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/runtime"
	"github.com/blueberrycongee/wuu/internal/session"
)

func (s *Server) resolvePluginProcessSession(ctx context.Context, id string, followParent bool) (string, bool, error) {
	binding, err := runtime.RetainInvocationState(ctx)
	if err != nil {
		return id, false, err
	}
	defer binding.Close()
	for {
		if err := ctx.Err(); err != nil {
			return id, false, err
		}
		metadata, found, err := binding.Sessions.Find(id)
		if err != nil {
			return id, false, err
		}
		if !found || metadata.ArchivedAt != nil {
			return id, true, nil
		}
		if !followParent || metadata.Visibility != pluginhost.SessionVisibilityPlugin || metadata.ParentID == "" {
			return id, false, nil
		}
		running := false
		if th := s.threadForState(binding, id); th != nil {
			th.mu.Lock()
			running = th.running || th.admissionReserved || th.executionLease != nil
			th.mu.Unlock()
		}
		s.queuedTurnMu.Lock()
		running = running || len(s.pendingQueuedTurns[id]) != 0
		for key, claim := range s.claimedQueuedTurns {
			if strings.HasPrefix(key, id+"\x00") && !claim.cancelled {
				running = true
				break
			}
		}
		s.queuedTurnMu.Unlock()
		if !running {
			// A child can be admitting a turn or executing in another server
			// before this server's running flag changes. Handoff is permanent.
			running, err = session.ThreadExecutionActive(s.rt.SessionDir, id)
			if err != nil {
				return id, false, err
			}
		}
		if running {
			return id, false, nil
		}
		id = metadata.ParentID
	}
}
