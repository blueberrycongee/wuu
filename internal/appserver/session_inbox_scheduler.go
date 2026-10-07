package appserver

import (
	"sort"
	"time"

	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/session"
)

const sessionInboxMaxRetryDelay = 2 * time.Second

type sessionInboxDrain struct {
	running   bool
	done      chan struct{}
	dirty     bool
	delay     time.Duration
	stopTimer func()
}

// One drain and timer per project coalesces delivery kicks. A bounded retry is
// still necessary: another process or a short metadata mutation can release a
// worker lease without sending this Server a completion event.
func (s *Server) drainSessionInbox(target string) {
	if s == nil || s.closed.Load() {
		return
	}
	metadata, found, err := session.Find(s.rt.SessionDir, target)
	if err != nil || !found {
		return
	}
	projectID := metadata.ID
	if projectExecutionDisabled(metadata.Source) {
		return
	}
	if metadata.Source == projectSessionSource || metadata.Source == fusionSideSource {
		projectID = metadata.ParentID
	}
	s.kickSessionInboxDrain(projectID)
	// Preserve synchronous delivery callers: join an in-flight pass, then settle
	// revoked input and steer running recipients even while starts are backed off.
	s.sessionInboxMu.Lock()
	state := s.sessionInboxDrains[projectID]
	var done <-chan struct{}
	if state != nil && state.running {
		done = state.done
	}
	s.sessionInboxMu.Unlock()
	if done != nil {
		<-done
	}
	s.drainSessionInboxTarget(target, false)
}

func (s *Server) kickSessionInboxDrain(projectID string) {
	if s == nil || s.closed.Load() || projectID == "" {
		return
	}
	s.sessionInboxMu.Lock()
	if s.closed.Load() {
		s.sessionInboxMu.Unlock()
		return
	}
	if s.sessionInboxDrains == nil {
		s.sessionInboxDrains = make(map[string]*sessionInboxDrain)
	}
	state := s.sessionInboxDrains[projectID]
	if state != nil {
		// Repeated kicks never reset the backoff or allocate another timer.
		state.dirty = true
		s.sessionInboxMu.Unlock()
		return
	}
	state = &sessionInboxDrain{running: true, done: make(chan struct{})}
	s.sessionInboxDrains[projectID] = state
	s.sessionInboxMu.Unlock()
	s.runSessionInboxDrain(projectID, state)
}

func (s *Server) runSessionInboxDrain(projectID string, state *sessionInboxDrain) {
	for {
		pending := s.drainSessionInboxPass(projectID)
		s.sessionInboxMu.Lock()
		if s.closed.Load() || s.sessionInboxDrains[projectID] != state {
			close(state.done)
			s.sessionInboxMu.Unlock()
			return
		}
		if !pending && state.dirty {
			// A kick racing the final scan cannot disappear with drain teardown.
			state.dirty = false
			s.sessionInboxMu.Unlock()
			continue
		}
		state.running = false
		close(state.done)
		state.dirty = false
		if !pending {
			delete(s.sessionInboxDrains, projectID)
			s.sessionInboxMu.Unlock()
			return
		}
		if state.delay == 0 {
			state.delay = threadExecutionLeaseRetryDelay
		} else {
			state.delay = min(state.delay*2, sessionInboxMaxRetryDelay)
		}
		retry := func() {
			s.sessionInboxMu.Lock()
			if s.closed.Load() || s.sessionInboxDrains[projectID] != state {
				s.sessionInboxMu.Unlock()
				return
			}
			state.stopTimer = nil
			state.running = true
			state.done = make(chan struct{})
			done := state.done
			s.sessionInboxMu.Unlock()
			if !s.startBackground(func() { s.runSessionInboxDrain(projectID, state) }) {
				s.sessionInboxMu.Lock()
				state.running = false
				close(done)
				s.sessionInboxMu.Unlock()
			}
		}
		if s.sessionInboxAfterFunc != nil {
			state.stopTimer = s.sessionInboxAfterFunc(state.delay, retry)
		} else {
			timer := time.AfterFunc(state.delay, retry)
			state.stopTimer = func() { timer.Stop() }
		}
		s.sessionInboxMu.Unlock()
		return
	}
}

func (s *Server) drainSessionInboxPass(projectID string) bool {
	workOwned := false
	if project, live := s.projectCoordinator(projectID); live {
		root, id, err := s.sessionWorkspace(project)
		workOwned = err == nil && s.ownsSessionWorkspace(root, id)
	}
	if workOwned {
		if work, err := session.PendingProjectWork(s.rt.SessionDir, projectID); err != nil {
			providers.DebugLogf("read work outbox: %v", err)
			return true
		} else {
			for _, w := range work {
				if err := s.reconcileProjectWork(w); err != nil {
					providers.DebugLogf("reconcile work outbox: %v", err)
				}
			}
		}
	}
	targets, err := session.InboxTargets(s.rt.SessionDir)
	if err != nil {
		providers.DebugLogf("list project inbox: %v", err)
		return true
	}
	type target struct {
		id     string
		worker bool
		oldest time.Time
	}
	var ready []target
	for _, id := range targets {
		m, found, err := session.Find(s.rt.SessionDir, id)
		if err != nil {
			return true
		}
		if !found || projectExecutionDisabled(m.Source) || m.ArchivedAt != nil || (m.ID != projectID && ((m.Source != projectSessionSource && m.Source != fusionSideSource) || m.ParentID != projectID)) {
			continue
		}
		root, workspaceID, err := s.sessionWorkspace(m)
		if err != nil || !s.ownsSessionWorkspace(root, workspaceID) {
			continue
		}
		messages, err := session.PendingInbox(s.rt.SessionDir, id)
		if err != nil {
			return true
		}
		if len(messages) == 0 {
			continue
		}
		ready = append(ready, target{id: id, worker: m.Source == projectSessionSource && (m.ProjectRole == "" || m.ProjectRole == "worker" || m.ProjectRole == "executor"), oldest: messages[0].CreatedAt})
	}
	// Coordination/steering is never withheld by worker capacity. Worker starts
	// use stable oldest-pending order within this Server's pass, not global FIFO.
	sort.SliceStable(ready, func(i, j int) bool {
		if ready[i].worker != ready[j].worker {
			return !ready[i].worker
		}
		if ready[i].oldest.Equal(ready[j].oldest) {
			return ready[i].id < ready[j].id
		}
		return ready[i].oldest.Before(ready[j].oldest)
	})
	full, pending := false, false
	for _, target := range ready {
		if s.closed.Load() {
			return false
		}
		if s.drainSessionInboxTarget(target.id, !target.worker || !full) {
			full = true
		}
		messages, err := session.PendingInbox(s.rt.SessionDir, target.id)
		if err != nil {
			pending = true
			continue
		}
		for _, message := range messages {
			if message.Wake {
				pending = true
				break
			}
		}
	}
	// Existing user/completion queues keep their own ordering and cancellation
	// rules; only capacity-blocked admissions join this coalesced retry loop.
	s.sessionInboxMu.Lock()
	callbacks := make(map[string]func() bool, len(s.projectCapacityRetries[projectID]))
	for key, retry := range s.projectCapacityRetries[projectID] {
		callbacks[key] = retry
	}
	s.sessionInboxMu.Unlock()
	keys := make([]string, 0, len(callbacks))
	for key := range callbacks {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if full || s.closed.Load() {
			break
		}
		s.sessionInboxMu.Lock()
		delete(s.projectCapacityRetries[projectID], key)
		s.sessionInboxMu.Unlock()
		full = callbacks[key]()
	}
	schemaPending, _ := s.drainExecutionSchemaRetries(projectID, !full)
	pending = pending || schemaPending
	s.sessionInboxMu.Lock()
	pending = pending || len(s.projectCapacityRetries[projectID]) > 0
	if len(s.projectCapacityRetries[projectID]) == 0 {
		delete(s.projectCapacityRetries, projectID)
	}
	s.sessionInboxMu.Unlock()
	if workOwned {
		if work, err := session.PendingProjectWork(s.rt.SessionDir, projectID); err != nil || len(work) > 0 {
			pending = true
		}
	}
	return pending
}

func (s *Server) closeSessionInboxDrains() {
	s.sessionInboxMu.Lock()
	defer s.sessionInboxMu.Unlock()
	for _, state := range s.sessionInboxDrains {
		if state.stopTimer != nil {
			state.stopTimer()
		}
	}
	s.sessionInboxDrains = nil
	s.projectCapacityRetries = nil
}

// deferProjectCapacityRetry bridges existing queues without changing their
// non-capacity busy/retry behavior. At most one retry per thread and queue kind
// is retained. No callback runs under the scheduler mutex.
func (s *Server) deferProjectCapacityRetry(threadID, kind string, retry func() bool) bool {
	metadata, found, err := session.Find(s.rt.SessionDir, threadID)
	if err != nil || !found || metadata.Source != projectSessionSource || metadata.ParentID == "" {
		return false
	}
	projectID := metadata.ParentID
	s.sessionInboxMu.Lock()
	if s.closed.Load() {
		s.sessionInboxMu.Unlock()
		return true
	}
	if s.projectCapacityRetries == nil {
		s.projectCapacityRetries = make(map[string]map[string]func() bool)
	}
	if s.projectCapacityRetries[projectID] == nil {
		s.projectCapacityRetries[projectID] = make(map[string]func() bool)
	}
	s.projectCapacityRetries[projectID][threadID+":"+kind] = retry
	s.sessionInboxMu.Unlock()
	s.startBackground(func() { s.kickSessionInboxDrain(projectID) })
	return true
}
