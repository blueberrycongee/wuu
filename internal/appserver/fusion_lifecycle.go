package appserver

import (
	"errors"
	"fmt"

	"github.com/blueberrycongee/wuu/internal/session"
)

// fusionLeadIDForSession preserves ownership even when archive or disabling
// Fusion hides the active session-control fence from clients.
func fusionLeadIDForSession(metadata session.Session) string {
	if metadata.Source == fusionSideSource {
		return metadata.ParentID
	}
	return ""
}

// Hold the Lead lease before resolving membership: ordinary delegation holds
// that same lease, so neither this host nor another can create a new Sidekick
// outside the lifecycle operation. Hold every member through commit and cleanup.
func (s *Server) beginThreadLifecycleMutation(id string) ([]session.Session, func(), error) {
	var leases []*session.ThreadExecutionLease
	release := func() {
		for _, lease := range leases {
			_ = lease.Release()
		}
	}
	leadLease, err := s.tryAcquireThreadMutationLease(id)
	if err != nil {
		return nil, release, err
	}
	leases = append(leases, leadLease)
	members, err := session.FusionLifecycleSessions(s.rt.SessionDir, id)
	if err != nil {
		release()
		return nil, func() {}, err
	}
	for index, member := range members {
		if th := s.thread(member.ID); th != nil {
			th.mu.Lock()
			busy := th.running || (member.Source == fusionSideSource && threadRuntimeHasOutstandingWork(th.ID, th.execRuntime))
			th.mu.Unlock()
			if busy || threadHasActiveAgents(th) {
				release()
				return nil, func() {}, errors.New("cannot archive a running thread or mutate its lifecycle while it has active agents")
			}
		}
		if index == 0 {
			continue
		}
		lease, err := s.tryAcquireThreadMutationLease(member.ID)
		if err != nil {
			release()
			return nil, func() {}, fmt.Errorf("Sidekick must be idle before changing the Fusion lifecycle: %w", err)
		}
		leases = append(leases, lease)
	}
	return members, release, nil
}
