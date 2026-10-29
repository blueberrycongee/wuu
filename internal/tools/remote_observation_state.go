package tools

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
)

// ReadObservationTicket contains only worker-owned versioned observations.
// The worker retains it; its contents never cross into the plugin process.
type ReadObservationTicket struct {
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
	return &ReadObservationTicket{state: r, entries: entries}
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
	t.state.mu.Lock()
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
	return &ReadObservationTicket{state: r, entries: entries}, nil
}

// Commit invalidates only the exact captured observation revisions.
func (t *ReadObservationTicket) Commit(ctx context.Context) (bool, error) { return t.commit(ctx) }
