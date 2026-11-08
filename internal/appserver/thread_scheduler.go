package appserver

import (
	"strings"
	"sync"
)

// threadTurnQueue owns one thread's queued user-turn pipeline: the pending
// entries, the in-flight admission claim, cancelled-submission tombstones,
// and the single-drain marker.
//
// queue.mu is a leaf mutex under the established order
// s.mu → th.streamMu → th.mu → queue.mu: code holding it must not acquire
// s.mu or th.mu. Durable held-input work performed inside the critical
// section serializes only this thread.
type threadTurnQueue struct {
	mu        sync.Mutex
	pending   []queuedTurn
	claimed   map[string]*queuedTurnClaim
	cancelled map[string]string
	draining  bool
}

// threadCompletionQueue owns one thread's pending agent/process completion
// wakeups and the single-drain marker for the synthetic turns they become.
//
// completions.mu is a leaf mutex with the same order as queue.mu
// (s.mu → th.streamMu → th.mu → completions.mu); nothing inside the
// critical section acquires s.mu or th.mu.
type threadCompletionQueue struct {
	mu       sync.Mutex
	pending  []agentCompletionTurn
	draining bool
}

// threadScheduler is the per-thread cell for in-memory turn scheduling.
// It is deliberately not part of threadState: admission replaces and the
// thread cache evicts projections while queued work, claims, and drain
// markers must keep their identity across those swaps. Cells therefore live
// in the Server registry until Close.
type threadScheduler struct {
	queue       threadTurnQueue
	completions threadCompletionQueue
	// heldMu serializes the read-modify-write compounds against this
	// thread's durable held-input rows (appendHeldUserTurns, findHeldUserTurn,
	// updateHeldUserTurn). It is independent of queue.mu; the two are never
	// held together.
	heldMu sync.Mutex
}

// schedulerFor returns the thread's scheduling cell, creating it on first
// use. Cells are never deleted: tombstone and drain state must outlive the
// cached threadState projection for the same thread.
func (s *Server) schedulerFor(threadID string) *threadScheduler {
	s.schedulersMu.Lock()
	defer s.schedulersMu.Unlock()
	if s.schedulers == nil {
		s.schedulers = make(map[string]*threadScheduler)
	}
	sched := s.schedulers[threadID]
	if sched == nil {
		sched = &threadScheduler{}
		s.schedulers[threadID] = sched
	}
	return sched
}

// peekScheduler reports the cell a thread already has, or nil. Read-only
// checks use it so listing every session does not allocate empty cells for
// threads that were never queued.
func (s *Server) peekScheduler(threadID string) *threadScheduler {
	s.schedulersMu.Lock()
	defer s.schedulersMu.Unlock()
	return s.schedulers[threadID]
}

func (s *Server) turnQueue(threadID string) *threadTurnQueue {
	return &s.schedulerFor(threadID).queue
}

func (s *Server) peekTurnQueue(threadID string) *threadTurnQueue {
	if sched := s.peekScheduler(threadID); sched != nil {
		return &sched.queue
	}
	return nil
}

func (s *Server) completionQueue(threadID string) *threadCompletionQueue {
	return &s.schedulerFor(threadID).completions
}

func (s *Server) peekCompletionQueue(threadID string) *threadCompletionQueue {
	if sched := s.peekScheduler(threadID); sched != nil {
		return &sched.completions
	}
	return nil
}

// schedulersSnapshot copies the registry so callers can walk every thread's
// queue without holding schedulersMu across cell locks.
func (s *Server) schedulersSnapshot() map[string]*threadScheduler {
	s.schedulersMu.Lock()
	defer s.schedulersMu.Unlock()
	out := make(map[string]*threadScheduler, len(s.schedulers))
	for id, sched := range s.schedulers {
		out[id] = sched
	}
	return out
}

// cancelledSubmission reports whether this exact submission id was cancelled
// for the given origin while the caller holds queue.mu.
func (q *threadTurnQueue) cancelledSubmission(id, origin string) bool {
	return q.cancelled[strings.TrimSpace(id)] == origin
}

// claim returns the in-flight admission claim for queueID while the caller
// holds queue.mu.
func (q *threadTurnQueue) claim(queueID string) *queuedTurnClaim {
	return q.claimed[strings.TrimSpace(queueID)]
}
