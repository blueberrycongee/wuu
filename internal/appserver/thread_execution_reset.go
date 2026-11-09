package appserver

import (
	"context"
	"time"

	"github.com/blueberrycongee/wuu/internal/providers"
)

const threadExecutionResetPollInterval = 100 * time.Millisecond

// watchThreadExecutionReset bridges a durable cross-process reset request into
// the existing in-process interruption path. The execution lease remains the
// sole ownership authority; reset never steals or removes it.
func (s *Server) watchThreadExecutionReset(ctx context.Context, th *threadState, turnID string) func() {
	if s == nil || th == nil {
		return func() {}
	}
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(threadExecutionResetPollInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-stop:
				return
			case <-ticker.C:
				th.mu.Lock()
				if th.currentTurn != turnID || th.executionLease == nil {
					th.mu.Unlock()
					return
				}
				lease := th.executionLease
				th.mu.Unlock()
				requested, err := lease.ResetRequested()
				if err != nil {
					providers.DebugLogf("read thread execution reset for %q: %v", th.ID, err)
					continue
				}
				if !requested {
					continue
				}
				if _, err := s.interruptThreadExecution(th.state, th.ID, "", ""); err != nil {
					providers.DebugLogf("apply thread execution reset for %q: %v", th.ID, err)
				}
				return
			}
		}
	}()
	var stopped bool
	return func() {
		if !stopped {
			close(stop)
			stopped = true
		}
		<-done
	}
}
