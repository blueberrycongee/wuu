package session

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/blueberrycongee/wuu/internal/securefs"
)

const threadLifecycleLeaseDirName = ".lifecycle-locks"

// ThreadLifecycleLease serializes short durable writes with thread deletion.
// The sidecar remains in place after release so every process keeps locking the
// same inode. The operating system releases the lock if its owner exits.
type ThreadLifecycleLease struct {
	mu   sync.Mutex
	file *os.File
}

// AcquireThreadLifecycleLease waits until it exclusively owns the thread's
// short lifecycle/write critical section. Long model execution deliberately
// uses ThreadExecutionLease instead, so participant posts can still arrive
// while a model turn is running.
func AcquireThreadLifecycleLease(sessDir, threadID string) (*ThreadLifecycleLease, error) {
	sessDir = strings.TrimSpace(sessDir)
	threadID = strings.TrimSpace(threadID)
	if sessDir == "" {
		return nil, errors.New("session directory is required")
	}
	if threadID == "" {
		return nil, errors.New("thread id is required")
	}

	path := threadLifecycleLeasePath(sessDir, threadID)
	file, err := securefs.OpenFile(path, os.O_CREATE|os.O_RDWR, securefs.FileMode)
	if err != nil {
		return nil, fmt.Errorf("open thread lifecycle lease: %w", err)
	}
	if err := lockThreadLifecycleFile(file); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("lock thread lifecycle lease: %w", err)
	}
	return &ThreadLifecycleLease{file: file}, nil
}

// Release relinquishes the lease. It is safe to call more than once.
func (l *ThreadLifecycleLease) Release() error {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file == nil {
		return nil
	}
	file := l.file
	l.file = nil
	unlockErr := unlockThreadLifecycleFile(file)
	closeErr := file.Close()
	return errors.Join(unlockErr, closeErr)
}

func threadLifecycleLeasePath(sessDir, threadID string) string {
	digest := sha256.Sum256([]byte(strings.TrimSpace(threadID)))
	return filepath.Join(sessDir, threadLifecycleLeaseDirName, hex.EncodeToString(digest[:])+".lock")
}
