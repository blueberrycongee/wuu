package tools

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// ReadObservationTicket contains only worker-owned versioned observations.
// The worker retains it; its contents never cross into the plugin process.
var observationTicketSequence atomic.Uint64

type ReadObservationTicket struct {
	id        uint64
	mu        sync.Mutex
	state     *readFileState
	entries   map[string]readFileObservation
	committed bool
	changed   bool
}

func (r *readFileState) prepareInvalidation(paths []string) *ReadObservationTicket {
	r.mu.RLock()
	defer r.mu.RUnlock()
	entries := make(map[string]readFileObservation, len(paths))
	for _, path := range paths {
		if value, ok := r.state[path]; ok {
			entries[path] = value
		}
	}
	return &ReadObservationTicket{id: observationTicketSequence.Add(1), state: r, entries: entries}
}

func (t *ReadObservationTicket) commit(ctx context.Context) (bool, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.committed {
		return t.changed, nil
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	for !t.state.mu.TryLock() {
		timer := time.NewTimer(5 * time.Millisecond)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return false, ctx.Err()
		case <-timer.C:
		}
	}
	defer t.state.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return false, err
	}
	for path, observed := range t.entries {
		current, ok := t.state.state[path]
		if ok && observed.revision != 0 && current.revision == observed.revision {
			delete(t.state.state, path)
			t.changed = true
		}
	}
	t.committed = true
	return t.changed, nil
}

// capturePathObservation snapshots aliases of one resolved worker path. Any
// later native observation gets a fresh revision and survives Commit.
const maxObservationScan = 4096
const maxObservationAliases = 128
const maxObservationPathBytes = 32 * 1024

func (r *readFileState) capturePathObservation(ctx context.Context, resolved string, opened os.FileInfo) (*ReadObservationTicket, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	target, err := filepath.EvalSymlinks(resolved)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(target)
	if err != nil || !os.SameFile(info, opened) {
		return nil, errors.New("remote read target changed while preparing observation")
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	if len(r.state) > maxObservationScan {
		return nil, errors.New("remote read observation scan limit exceeded")
	}
	entries := make(map[string]readFileObservation)
	bytes := 0
	for path, value := range r.state {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		candidate, err := os.Stat(path)
		if err == nil && os.SameFile(candidate, opened) {
			bytes += len(path)
			if len(entries) >= maxObservationAliases || bytes > maxObservationPathBytes {
				return nil, errors.New("remote read observation alias limit exceeded")
			}
			entries[path] = value
		}
	}
	return &ReadObservationTicket{id: observationTicketSequence.Add(1), state: r, entries: entries}, nil
}

// Commit invalidates only the exact captured observation revisions.
func (t *ReadObservationTicket) Commit(ctx context.Context) (bool, error) { return t.commit(ctx) }

// CommitReadObservationBatch applies a bounded set of tickets to exactly one
// actor store under one lock. Different stores or mixed terminal states fail
// before any observation changes.
func CommitReadObservationBatch(ctx context.Context, tickets []*ReadObservationTicket) ([]bool, error) {
	if len(tickets) == 0 || len(tickets) > 128 {
		return nil, errors.New("invalid remote observation batch size")
	}
	ordered := append([]*ReadObservationTicket(nil), tickets...)
	for _, ticket := range ordered {
		if ticket == nil || ticket.id == 0 {
			return nil, errors.New("invalid remote observation ticket")
		}
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].id < ordered[j].id })
	for i := 1; i < len(ordered); i++ {
		if ordered[i] == ordered[i-1] {
			return nil, errors.New("duplicate remote observation ticket")
		}
	}
	for _, ticket := range ordered {
		ticket.mu.Lock()
	}
	defer func() {
		for i := len(ordered) - 1; i >= 0; i-- {
			ordered[i].mu.Unlock()
		}
	}()
	state := ordered[0].state
	if state == nil {
		return nil, errors.New("observation store unavailable")
	}
	for _, ticket := range ordered {
		if ticket.state != state || ticket.committed {
			return nil, errors.New("incompatible remote observation batch")
		}
	}
	for !state.mu.TryLock() {
		timer := time.NewTimer(5 * time.Millisecond)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
	defer state.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	results := make([]bool, len(tickets))
	for i, ticket := range tickets {
		for path, observation := range ticket.entries {
			if current, ok := state.state[path]; ok && observation.revision != 0 && current.revision == observation.revision {
				delete(state.state, path)
				results[i] = true
			}
		}
		ticket.committed = true
		ticket.changed = results[i]
	}
	return results, nil
}
