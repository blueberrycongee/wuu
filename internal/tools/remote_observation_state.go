package tools

import (
	"context"
	"errors"
	"math"
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
	opened    os.FileInfo
	native    *nativeReadObservation
}

type nativeReadObservation struct {
	path  string
	entry ReadFileEntry
}

// stageNativeRead keeps a native observation private until the worker commits
// its receipt. It is never populated from a plugin result.
func (t *ReadObservationTicket) stageNativeRead(path string, entry ReadFileEntry, opened os.FileInfo) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.committed || t.native != nil || opened == nil || t.opened == nil || !os.SameFile(opened, t.opened) {
		return errors.New("invalid prepared native observation")
	}
	t.native = &nativeReadObservation{path: path, entry: entry}
	return nil
}

// applyLocked is called only while the ticket and state locks are held.
func (t *ReadObservationTicket) applyLocked() bool {
	if t.native != nil {
		path := t.native.path
		// A newer read through any hard-link alias wins over this older
		// staged observation. Conservatively skip publication if the bounded
		// identity check cannot establish that the snapshot is still current.
		if len(t.state.state) > maxObservationScan {
			return false
		}
		currentFile, err := os.Stat(path)
		if err != nil || !os.SameFile(currentFile, t.opened) {
			return false
		}
		for alias, previous := range t.entries {
			current, exists := t.state.state[alias]
			if !exists || previous.revision == 0 || current.revision != previous.revision {
				return false
			}
			info, err := os.Stat(alias)
			if err != nil || !os.SameFile(info, t.opened) {
				return false
			}
		}
		for alias, current := range t.state.state {
			info, err := os.Stat(alias)
			if err == nil && os.SameFile(info, t.opened) {
				previous, exists := t.entries[alias]
				if !exists || previous.revision == 0 || previous.revision != current.revision {
					return false
				}
			}
		}
		if t.state.state == nil {
			t.state.state = make(map[string]readFileObservation)
		}
		t.state.revision++
		t.state.state[path] = readFileObservation{entry: t.native.entry, revision: t.state.revision}
		return true
	}
	changed := false
	for path, observed := range t.entries {
		current, ok := t.state.state[path]
		if ok && observed.revision != 0 && current.revision == observed.revision {
			delete(t.state.state, path)
			changed = true
		}
	}
	return changed
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
	if t.native != nil && t.state.revision == math.MaxUint64 {
		return false, errors.New("native observation revision exhausted")
	}
	t.changed = t.applyLocked()
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
	return &ReadObservationTicket{id: observationTicketSequence.Add(1), state: r, entries: entries, opened: opened}, nil
}

// HasNativeRead reports whether the worker staged a native line observation.
func (t *ReadObservationTicket) HasNativeRead() bool {
	if t == nil {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.native != nil
}

// Commit invalidates only the exact captured observation revisions.
func (t *ReadObservationTicket) Commit(ctx context.Context) (bool, error) { return t.commit(ctx) }

// ValidateReadObservationBatch rejects incompatible snapshots before a worker
// reserves a durable batch, so a conflict is known rather than an uncertain commit.
func ValidateReadObservationBatch(tickets []*ReadObservationTicket) error {
	if len(tickets) == 0 || len(tickets) > 128 {
		return errors.New("invalid remote observation batch size")
	}
	ordered := append([]*ReadObservationTicket(nil), tickets...)
	for _, t := range ordered {
		if t == nil || t.id == 0 {
			return errors.New("invalid remote observation ticket")
		}
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].id < ordered[j].id })
	for i := 1; i < len(ordered); i++ {
		if ordered[i] == ordered[i-1] {
			return errors.New("duplicate remote observation ticket")
		}
	}
	for _, t := range ordered {
		t.mu.Lock()
	}
	defer func() {
		for i := len(ordered) - 1; i >= 0; i-- {
			ordered[i].mu.Unlock()
		}
	}()
	state := ordered[0].state
	if state == nil {
		return errors.New("observation store unavailable")
	}
	for _, t := range ordered {
		if t.state != state || t.committed {
			return errors.New("incompatible remote observation batch")
		}
	}
	for i, t := range tickets {
		if t.native == nil {
			continue
		}
		if t.opened == nil {
			return errors.New("native observation identity unavailable")
		}
		for j, other := range tickets {
			if i != j && (other.opened == nil || os.SameFile(t.opened, other.opened)) {
				return errors.New("conflicting native read observations")
			}
		}
	}
	return nil
}

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
	// Native writes and any other read of the same physical file cannot be
	// ordered safely from independently prepared snapshots. Reject the whole
	// batch before making any change.
	nativeCount := uint64(0)
	for i, ticket := range tickets {
		if ticket.native == nil {
			continue
		}
		nativeCount++
		if ticket.opened == nil {
			return nil, errors.New("native observation identity unavailable")
		}
		for j, other := range tickets {
			if i == j {
				continue
			}
			if other.opened == nil || os.SameFile(ticket.opened, other.opened) {
				return nil, errors.New("conflicting native read observations")
			}
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
	if nativeCount > math.MaxUint64-state.revision {
		return nil, errors.New("native observation revision exhausted")
	}
	results := make([]bool, len(tickets))
	for i, ticket := range tickets {
		results[i] = ticket.applyLocked()
		ticket.committed = true
		ticket.changed = results[i]
	}
	return results, nil
}
