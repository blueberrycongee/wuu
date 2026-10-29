package runtime

import (
	"context"
	"sync"
)

// nativeCleanupTracker is shared by a Session and its private thread snapshots.
// Tasks retain their own resource owners, so a timed-out waiter does not close
// resources that are still in use.
type nativeCleanupTracker struct {
	mu      sync.Mutex
	pending map[<-chan struct{}]struct{}
}

func (s *Session) nativeCleanupOwner() *nativeCleanupTracker {
	s.nativeCleanupMu.Lock()
	defer s.nativeCleanupMu.Unlock()
	if s.nativeCleanups == nil {
		s.nativeCleanups = &nativeCleanupTracker{pending: make(map[<-chan struct{}]struct{})}
	}
	return s.nativeCleanups
}
func (s *Session) runNativeCleanup(f func()) {
	tracker := s.nativeCleanupOwner()
	done := make(chan struct{})
	tracker.mu.Lock()
	tracker.pending[done] = struct{}{}
	tracker.mu.Unlock()
	go func() {
		defer func() { close(done); tracker.mu.Lock(); delete(tracker.pending, done); tracker.mu.Unlock() }()
		f()
	}()
}
func (s *Session) waitNativeCleanups(ctx context.Context) error {
	tracker := s.nativeCleanupOwner()
	for {
		tracker.mu.Lock()
		var pending <-chan struct{}
		for done := range tracker.pending {
			pending = done
			break
		}
		tracker.mu.Unlock()
		if pending == nil {
			return nil
		}
		select {
		case <-pending:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}
