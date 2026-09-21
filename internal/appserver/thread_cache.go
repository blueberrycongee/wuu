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
	if s == nil || cachedThreadLimit <= 0 {
		return
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
		return
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
		return
	}
	sort.Slice(candidates, func(i, j int) bool {
		left := candidateAccessTime(candidates[i])
		right := candidateAccessTime(candidates[j])
		if !left.Equal(right) {
			return left.Before(right)
		}
		return candidates[i].id < candidates[j].id
	})

	evicted := make([]*threadState, 0, len(candidates))
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
		if !cachedThreadStillIdle(current) {
			continue
		}
		delete(s.threads, candidate.id)
		evicted = append(evicted, current)
		evictCount--
	}
	s.mu.Unlock()

	for _, th := range evicted {
		s.releaseThreadRuntime(th)
	}
}

func (s *Server) cachedThreadCandidateLocked(id string, th *threadState) (cachedThreadCandidate, bool) {
	if th == nil {
		return cachedThreadCandidate{}, false
	}
	th.mu.Lock()
	defer th.mu.Unlock()
	if cachedThreadExecutionActiveLocked(th) {
		return cachedThreadCandidate{}, false
	}
	if th.Ephemeral {
		return cachedThreadCandidate{}, false
	}
	if !th.PersistHistory && !th.ReadOnly {
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
	if th.running || th.executionLease != nil || th.admissionReserved {
		return true
	}
	return th.execRuntime != nil &&
		th.execRuntime.AgentControl != nil &&
		th.execRuntime.AgentControl.HasOwnedWorkerExecutions()
}
