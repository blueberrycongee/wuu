package appserver

import (
	"errors"
	"fmt"
	"strings"

	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/session"
)

// acquireThreadLifecycleWriteLease serializes short append-and-route writes
// with permanent thread deletion without excluding a long model execution.
func (s *Server) acquireThreadLifecycleWriteLease(threadID string) (*session.ThreadLifecycleLease, error) {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return nil, errors.New("thread id is required")
	}
	if s == nil || s.rt == nil || strings.TrimSpace(s.rt.SessionDir) == "" {
		return nil, errors.New("session directory is required for durable thread lifecycle")
	}
	lease, err := session.AcquireThreadLifecycleLease(s.rt.SessionDir, threadID)
	if err != nil {
		return nil, fmt.Errorf("acquire lifecycle lease for thread %q: %w", threadID, err)
	}
	return lease, nil
}

func releaseThreadLifecycleWriteLease(threadID string, lease *session.ThreadLifecycleLease) {
	if lease == nil {
		return
	}
	if err := lease.Release(); err != nil {
		providers.DebugLogf("release lifecycle lease for thread %q: %v", threadID, err)
	}
}
