package appserver

import (
	"sort"
	"strings"
	"time"
)

const cachedThreadLimit = 8

type cachedThreadCandidate struct {
	id             string
	thread         *threadState
	lastAccessedAt time.Time
	updatedAt      time.Time
}

func (s *Server) pruneCachedThreads(keepIDs ...string) {
	if cleanup := s.evictCachedThreads(keepIDs...); cleanup != nil {
		cleanup()
	}
}

// evictCachedThreads removes idle projections and captures their old resources
// before returning. Callers may defer the joins, but never the cache mutation:
// the next request can already be using a newly loaded projection of that ID.
func (s *Server) evictCachedThreads(keepIDs ...string) func() {
	if s == nil || cachedThreadLimit <= 0 {
		return nil
	}
	keep := make(map[string]struct{}, len(keepIDs))
	for _, id := range keepIDs {
		id = strings.TrimSpace(id)
		if id != "" {
			keep[id] = struct{}{}
		}
	}

	s.mu.Lock()
	threadCount := len(s.threads)
	if threadCount <= cachedThreadLimit {
		s.mu.Unlock()
		return nil
	}
	candidates := make([]cachedThreadCandidate, 0, threadCount)
	for id, th := range s.threads {
		if _, ok := keep[id]; ok {
			continue
		}
		if candidate, ok := s.cachedThreadCandidateLocked(id, th); ok {
			candidates = append(candidates, candidate)
		}
	}
	s.mu.Unlock()

	candidates = s.filterCachedThreadCandidates(candidates)
	if len(candidates) == 0 {
		return nil
	}
	sort.Slice(candidates, func(i, j int) bool {
		left := candidateAccessTime(candidates[i])
		right := candidateAccessTime(candidates[j])
		if !left.Equal(right) {
			return left.Before(right)
		}
		return candidates[i].id < candidates[j].id
	})

	cleanups := make([]func(), 0, len(candidates))
	s.mu.Lock()
	evictCount := len(s.threads) - cachedThreadLimit
	for _, candidate := range candidates {
		if evictCount <= 0 {
			break
		}
		if _, ok := keep[candidate.id]; ok {
			continue
		}
		current := s.threads[candidate.id]
		if current == nil || current != candidate.thread {
			continue
		}
		if s.activeExecutionRunID(current.ID) != "" {
			continue
		}
		current.mu.Lock()
		if !cachedThreadStillIdleLocked(current) {
			current.mu.Unlock()
			continue
		}
		detached := detachThreadRuntimeLocked(current)
		current.maybeReleasePluginGenerationExecutionLeaseLocked()
		binding := current.state
		delete(s.threads, candidate.id)
		current.mu.Unlock()
		cleanups = append(cleanups, func() {
			s.releaseDetachedThreadRuntime(detached)
			if binding != nil {
				_ = binding.Close()
			}
		})
		evictCount--
	}
	s.mu.Unlock()

	if len(cleanups) == 0 {
		return nil
	}
	return func() {
		for _, cleanup := range cleanups {
			cleanup()
		}
	}
}

func (s *Server) cachedThreadCandidateLocked(id string, th *threadState) (cachedThreadCandidate, bool) {
	if th == nil || s.activeExecutionRunID(id) != "" {
		return cachedThreadCandidate{}, false
	}
	th.mu.Lock()
	defer th.mu.Unlock()
	if !cachedThreadStillIdleLocked(th) {
		return cachedThreadCandidate{}, false
	}
	return cachedThreadCandidate{
		id:             id,
		thread:         th,
		lastAccessedAt: th.LastAccessedAt,
		updatedAt:      th.UpdatedAt,
	}, true
}

func (s *Server) filterCachedThreadCandidates(candidates []cachedThreadCandidate) []cachedThreadCandidate {
	if len(candidates) == 0 {
		return nil
	}
	out := candidates[:0]
	for _, candidate := range candidates {
		if s.hasQueuedUserWork(candidate.id) {
			continue
		}
		if s.hasQueuedAgentCompletionWork(candidate.id) {
			continue
		}
		out = append(out, candidate)
	}
	return out
}

func candidateAccessTime(candidate cachedThreadCandidate) time.Time {
	if !candidate.lastAccessedAt.IsZero() {
		return candidate.lastAccessedAt
	}
	if !candidate.updatedAt.IsZero() {
		return candidate.updatedAt
	}
	return time.Time{}
}

func cachedThreadStillIdle(th *threadState) bool {
	if th == nil {
		return false
	}
	th.mu.Lock()
	defer th.mu.Unlock()
	return cachedThreadStillIdleLocked(th)
}

func cachedThreadStillIdleLocked(th *threadState) bool {
	if cachedThreadExecutionActiveLocked(th) {
		return false
	}
	if th.Ephemeral {
		return false
	}
	if !th.PersistHistory && !th.ReadOnly {
		return false
	}
	return true
}

func cachedThreadExecutionActiveLocked(th *threadState) bool {
	if th.running || th.executionLease != nil || th.admissionReserved || th.nativeHistoryMutation {
		return true
	}
	return th.execRuntime != nil &&
		th.execRuntime.AgentControl != nil &&
		th.execRuntime.AgentControl.HasOwnedWorkerExecutions()
}
