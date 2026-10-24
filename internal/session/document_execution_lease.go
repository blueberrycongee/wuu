package session

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/blueberrycongee/wuu/internal/securefs"
)

// DocumentThreadExecutionLease retains a captured authority and an OS lease.
// The descriptor, never a heartbeat timeout, determines execution ownership.
type DocumentThreadExecutionLease struct {
	remote          *Store
	mu              sync.Mutex
	file            *os.File
	threadID        string
	resetGeneration int64
}

type threadExecutionDocument struct {
	ThreadID                string
	Generation, RequestedAt int64
	OwnerID                 string
}

// Admission and reset share a local process lock independently of the selected
// document backend. documentStore revision CAS also fences concurrent membership edits.
// Lock acquisition and probes stay outside retried document callbacks.
func acquireThreadAdmission(ctx context.Context, dir string) (*os.File, error) {
	file, err := securefs.OpenFile(filepath.Join(dir, threadExecutionLeaseDirName, "admission.lock"), os.O_CREATE|os.O_RDWR, securefs.FileMode)
	if err != nil {
		return nil, err
	}
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		if err := ctx.Err(); err != nil {
			_ = file.Close()
			return nil, err
		}
		acquired, err := tryLockThreadExecutionFile(file)
		if err != nil {
			_ = file.Close()
			return nil, err
		}
		if acquired {
			return file, nil
		}
		select {
		case <-ctx.Done():
			_ = file.Close()
			return nil, ctx.Err()
		case <-ticker.C:
		}
	}
}

// Native-domain tests use the same host admission mechanism as selected clients.
func (s *documentStore) TryAcquireThreadExecutionLease(dir, id string) (*DocumentThreadExecutionLease, bool, error) {
	return NewStore(s.ctx, s.documents).TryAcquireThreadExecutionLease(dir, id)
}
func (s *documentStore) TryAcquireThreadExecutionLeaseWithProjectLimit(dir, id string, limit int) (*DocumentThreadExecutionLease, bool, error) {
	return NewStore(s.ctx, s.documents).TryAcquireThreadExecutionLeaseWithProjectLimit(dir, id, limit)
}
func (s *documentStore) RequestThreadExecutionReset(dir, id string) (bool, error) {
	return NewStore(s.ctx, s.documents).RequestThreadExecutionReset(dir, id)
}

func (l *DocumentThreadExecutionLease) ResetRequested() (bool, error) {
	if l == nil {
		return false, nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file == nil {
		return false, nil
	}
	var requested bool
	err := l.remote.invoke("ThreadExecutionResetRequested", []any{l.threadID, l.resetGeneration}, []any{&requested})
	return requested, err
}

func (l *DocumentThreadExecutionLease) Release() error {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file == nil {
		return nil
	}
	file := l.file
	l.file, l.remote = nil, nil
	return errors.Join(unlockThreadExecutionFile(file), file.Close())
}
