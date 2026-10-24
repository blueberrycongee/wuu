package appserver

import (
	"sort"
	"time"

	"github.com/blueberrycongee/wuu/internal/providers"
)

const projectInboxMaxRetryDelay = 2 * time.Second

type projectCapacityRetry struct{ threadID, kind string }

type projectInboxDrain struct {
	running   bool
	done      chan struct{}
	dirty     bool
	delay     time.Duration
	stopTimer func()
}

// One drain and timer per project coalesces delivery kicks. A bounded retry is
// still necessary: another process or a short metadata mutation can release a
// worker lease without sending this Server a completion event.
func (s *ProjectCoordinationProvider) drainSessionInbox(target string) {
	if s == nil || !projectAgentEnabled || !s.schedulingAllowed() {
		return
	}
	metadata, found, err := s.store.Find(target)
	if err != nil || !found {
		return
	}
	projectID := metadata.ID
	if metadata.Source == projectSessionSource {
		projectID = metadata.ParentID
	}
	s.kickProjectInboxDrain(projectID)
	// Preserve synchronous delivery callers: join an in-flight pass, then settle
	// revoked input and steer running recipients even while starts are backed off.
	s.lifetime.projectInboxMu.Lock()
	state := s.lifetime.projectInboxDrains[projectID]
	var done <-chan struct{}
	if state != nil && state.running {
		done = state.done
	}
	s.lifetime.projectInboxMu.Unlock()
	if done != nil {
		select {
		case <-done:
		case <-s.ctx.Done():
			return
		}
	}
	s.drainSessionInboxTarget(target, false)
}

func (s *ProjectCoordinationProvider) kickProjectInboxDrain(projectID string) {
	if s == nil || !projectAgentEnabled || !s.schedulingAllowed() || projectID == "" {
		return
	}
	s.lifetime.projectInboxMu.Lock()
	if !s.schedulingAllowed() {
		s.lifetime.projectInboxMu.Unlock()
		return
	}
	if s.lifetime.projectInboxDrains == nil {
		s.lifetime.projectInboxDrains = make(map[string]*projectInboxDrain)
	}
	state := s.lifetime.projectInboxDrains[projectID]
	if state != nil {
		// Repeated kicks never reset the backoff or allocate another timer.
		state.dirty = true
		s.lifetime.projectInboxMu.Unlock()
		return
	}
	state = &projectInboxDrain{running: true, done: make(chan struct{})}
	s.lifetime.projectInboxDrains[projectID] = state
	s.lifetime.projectInboxMu.Unlock()
	s.runProjectInboxDrain(projectID, state)
}

func (s *ProjectCoordinationProvider) runProjectInboxDrain(projectID string, state *projectInboxDrain) {
	for {
		pending := s.drainProjectInboxPass(projectID)
		s.lifetime.projectInboxMu.Lock()
		if !s.schedulingAllowed() || s.lifetime.projectInboxDrains[projectID] != state {
			if s.lifetime.projectInboxDrains[projectID] == state {
				delete(s.lifetime.projectInboxDrains, projectID)
			}
			state.running = false
			close(state.done)
			s.lifetime.projectInboxMu.Unlock()
			return
		}
		if !pending && state.dirty {
			// A kick racing the final scan cannot disappear with drain teardown.
			state.dirty = false
			s.lifetime.projectInboxMu.Unlock()
			continue
		}
		state.running = false
		close(state.done)
		state.dirty = false
		if !pending {
			delete(s.lifetime.projectInboxDrains, projectID)
			s.lifetime.projectInboxMu.Unlock()
			return
		}
		if state.delay == 0 {
			state.delay = threadExecutionLeaseRetryDelay
		} else {
			state.delay = min(state.delay*2, projectInboxMaxRetryDelay)
		}
		retry := func() {
			s.lifetime.projectInboxMu.Lock()
			if !s.owner.schedulingAllowed() || s.lifetime.projectInboxDrains[projectID] != state {
				s.lifetime.projectInboxMu.Unlock()
				return
			}
			state.stopTimer = nil
			state.running = true
			state.done = make(chan struct{})
			done := state.done
			s.lifetime.projectInboxMu.Unlock()
			if !s.startBackground(func(operation *ProjectCoordinationProvider) { operation.runProjectInboxDrain(projectID, state) }) {
				s.lifetime.projectInboxMu.Lock()
				if s.lifetime.projectInboxDrains[projectID] == state {
					delete(s.lifetime.projectInboxDrains, projectID)
				}
				state.running = false
				close(done)
				s.lifetime.projectInboxMu.Unlock()
			}
		}
		if s.lifetime.projectInboxAfterFunc != nil {
			state.stopTimer = s.lifetime.projectInboxAfterFunc(state.delay, retry)
		} else {
			timer := time.AfterFunc(state.delay, retry)
			state.stopTimer = func() { timer.Stop() }
		}
		s.lifetime.projectInboxMu.Unlock()
		return
	}
}

func (s *ProjectCoordinationProvider) drainProjectInboxPass(projectID string) bool {
	workOwned := false
	if project, live := s.projectCoordinator(projectID); live {
		root, id, err := s.sessionWorkspace(project)
		workOwned = err == nil && s.ownsSessionWorkspace(root, id)
	}
	if workOwned {
		if work, err := s.store.PendingProjectWork(projectID); err != nil {
			providers.DebugLogf("read work outbox: %v", err)
			return true
		} else {
			for _, w := range work {
				if err := s.reconcileProjectWork(s.ctx, w); err != nil {
					providers.DebugLogf("reconcile work outbox: %v", err)
				}
			}
		}
	}
	targets, err := s.store.InboxTargets()
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
		m, found, err := s.store.Find(id)
		if err != nil {
			return true
		}
		if !found || m.ArchivedAt != nil || (m.ID != projectID && (m.Source != projectSessionSource || m.ParentID != projectID)) {
			continue
		}
		root, workspaceID, err := s.sessionWorkspace(m)
		if err != nil || !s.ownsSessionWorkspace(root, workspaceID) {
			continue
		}
		messages, err := s.store.PendingInbox(id)
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
		if !s.schedulingAllowed() {
			return false
		}
		if s.drainSessionInboxTarget(target.id, !target.worker || !full) {
			full = true
		}
		messages, err := s.store.PendingInbox(target.id)
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
	s.lifetime.projectInboxMu.Lock()
	callbacks := make(map[string]projectCapacityRetry, len(s.lifetime.projectCapacityRetries[projectID]))
	for key, retry := range s.lifetime.projectCapacityRetries[projectID] {
		callbacks[key] = retry
	}
	s.lifetime.projectInboxMu.Unlock()
	keys := make([]string, 0, len(callbacks))
	for key := range callbacks {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if full || !s.schedulingAllowed() {
			break
		}
		s.lifetime.projectInboxMu.Lock()
		delete(s.lifetime.projectCapacityRetries[projectID], key)
		s.lifetime.projectInboxMu.Unlock()
		retry := callbacks[key]
		if retry.kind == "queued" {
			full = s.tryDrainQueuedTurns(s.binding, retry.threadID, true)
		} else {
			full = s.tryDrainAgentCompletionTurns(s.binding, retry.threadID, true)
		}
	}
	if !s.schedulingAllowed() {
		return false
	}
	schemaPending, _ := s.drainExecutionSchemaRetries(s.binding, projectID, !full)
	pending = pending || schemaPending
	s.lifetime.projectInboxMu.Lock()
	pending = pending || len(s.lifetime.projectCapacityRetries[projectID]) > 0
	if len(s.lifetime.projectCapacityRetries[projectID]) == 0 {
		delete(s.lifetime.projectCapacityRetries, projectID)
	}
	s.lifetime.projectInboxMu.Unlock()
	if workOwned {
		if work, err := s.store.PendingProjectWork(projectID); err != nil || len(work) > 0 {
			pending = true
		}
	}
	return pending
}

func (s *ProjectCoordinationProvider) closeProjectInboxDrains() {
	s.lifetime.projectInboxMu.Lock()
	defer s.lifetime.projectInboxMu.Unlock()
	for _, state := range s.lifetime.projectInboxDrains {
		if state.stopTimer != nil {
			state.stopTimer()
		}
	}
	s.lifetime.projectInboxDrains = nil
	s.lifetime.projectCapacityRetries = nil
}

// deferProjectCapacityRetry bridges existing queues without changing their
// non-capacity busy/retry behavior. At most one retry per thread and queue kind
// is retained. No callback runs under the scheduler mutex.
func (s *ProjectCoordinationProvider) deferProjectCapacityRetry(threadID, kind string) bool {
	metadata, found, err := s.store.Find(threadID)
	if err != nil || !found || metadata.Source != projectSessionSource || metadata.ParentID == "" {
		return false
	}
	projectID := metadata.ParentID
	s.lifetime.projectInboxMu.Lock()
	if !s.schedulingAllowed() {
		s.lifetime.projectInboxMu.Unlock()
		return true
	}
	if s.lifetime.projectCapacityRetries == nil {
		s.lifetime.projectCapacityRetries = make(map[string]map[string]projectCapacityRetry)
	}
	if s.lifetime.projectCapacityRetries[projectID] == nil {
		s.lifetime.projectCapacityRetries[projectID] = make(map[string]projectCapacityRetry)
	}
	s.lifetime.projectCapacityRetries[projectID][threadID+":"+kind] = projectCapacityRetry{threadID, kind}
	s.lifetime.projectInboxMu.Unlock()
	s.startBackground(func(operation *ProjectCoordinationProvider) { operation.kickProjectInboxDrain(projectID) })
	return true
}
