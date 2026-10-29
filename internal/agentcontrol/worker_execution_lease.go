package agentcontrol

import (
	"context"
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

const workerExecutionLeaseDirName = ".worker-locks"

var errWorkerExecutionBusy = errors.New("worker execution is owned by another app-server")

// workerExecutionLease keeps one worker run single-owner across app-server
// processes. The lock sidecar is never removed, so all contenders continue to
// lock the same inode; process exit releases the operating-system lock.
type workerExecutionLease struct {
	mu       sync.Mutex
	file     *os.File
	released chan struct{}
	once     sync.Once
}

func newWorkerExecutionLease(file *os.File) *workerExecutionLease {
	return &workerExecutionLease{file: file, released: make(chan struct{})}
}

func tryAcquireWorkerExecutionLease(rootDir, workerID string) (*workerExecutionLease, bool, error) {
	rootDir = strings.TrimSpace(rootDir)
	workerID = strings.TrimSpace(workerID)
	if rootDir == "" {
		return newWorkerExecutionLease(nil), true, nil
	}
	if workerID == "" {
		return nil, false, errors.New("worker id is required")
	}
	digest := sha256.Sum256([]byte(workerID))
	path := filepath.Join(rootDir, workerExecutionLeaseDirName, hex.EncodeToString(digest[:])+".lock")
	file, err := securefs.OpenFile(path, os.O_CREATE|os.O_RDWR, securefs.FileMode)
	if err != nil {
		return nil, false, fmt.Errorf("open worker execution lease: %w", err)
	}
	acquired, err := tryLockWorkerExecutionFile(file)
	if err != nil {
		_ = file.Close()
		return nil, false, fmt.Errorf("lock worker execution lease: %w", err)
	}
	if !acquired {
		_ = file.Close()
		return nil, false, nil
	}
	return newWorkerExecutionLease(file), true, nil
}

func (l *workerExecutionLease) release() error {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	file := l.file
	l.file = nil
	var err error
	if file != nil {
		err = errors.Join(unlockWorkerExecutionFile(file), file.Close())
	}
	l.once.Do(func() {
		if l.released != nil {
			close(l.released)
		}
	})
	return err
}

func (l *workerExecutionLease) releasedSignal() <-chan struct{} {
	if l == nil {
		return closedWorkerExecutionSignal()
	}
	// The signal is initialized before publication and never replaced. Reading
	// it must not wait for the persistence acknowledgement holding lease.mu.
	return l.released
}

func closedWorkerExecutionSignal() <-chan struct{} {
	done := make(chan struct{})
	close(done)
	return done
}

func (c *AgentControl) acquireWorkerExecution(workerID string) (bool, error) {
	if c == nil {
		return false, errors.New("agent control is required")
	}
	workerID = strings.TrimSpace(workerID)
	if workerID == "" {
		return false, errors.New("worker id is required")
	}
	c.workerLeaseMu.Lock()
	defer c.workerLeaseMu.Unlock()
	if c.workerLeases == nil {
		c.workerLeases = make(map[string]*workerExecutionLease)
	}
	if c.workerLeases[workerID] != nil {
		return false, nil
	}
	lease, acquired, err := tryAcquireWorkerExecutionLease(c.harnessDir, workerID)
	if err != nil {
		return false, err
	}
	if !acquired {
		return false, fmt.Errorf("worker %q is already running in another app-server: %w", workerID, errWorkerExecutionBusy)
	}
	c.workerLeases[workerID] = lease
	return true, nil
}

func (c *AgentControl) releaseWorkerExecution(workerID string) {
	if c == nil {
		return
	}
	workerID = strings.TrimSpace(workerID)
	c.workerLeaseMu.Lock()
	lease := c.workerLeases[workerID]
	c.workerLeaseMu.Unlock()
	if lease != nil {
		_ = lease.release()
	}
	// Keep local ownership visible until the operating-system lease is
	// physically released. A queued-launch acknowledgement may deliberately
	// hold lease.mu while a zero-latency terminal path reaches this method;
	// deleting the map entry first would let shutdown conclude that the runtime
	// was idle even though another process still could not acquire the worker.
	c.workerLeaseMu.Lock()
	if c.workerLeases[workerID] == lease {
		delete(c.workerLeases, workerID)
	}
	c.workerLeaseMu.Unlock()
}

// lockWorkerExecutionRelease prevents a fast terminal worker from releasing
// its cross-process lease while a queued launcher is acknowledging the durable
// payload that created it. The manager may finish immediately after Spawn, so
// the queue-to-runtime handoff needs this per-worker barrier.
func (c *AgentControl) lockWorkerExecutionRelease(workerID string) func() {
	if c == nil {
		return func() {}
	}
	workerID = strings.TrimSpace(workerID)
	c.workerLeaseMu.Lock()
	lease := c.workerLeases[workerID]
	if lease != nil {
		lease.mu.Lock()
	}
	c.workerLeaseMu.Unlock()
	if lease == nil {
		return func() {}
	}
	return lease.mu.Unlock
}

func (c *AgentControl) workerExecutionOwned(workerID string) bool {
	if c == nil {
		return false
	}
	c.workerLeaseMu.Lock()
	defer c.workerLeaseMu.Unlock()
	return c.workerLeases[strings.TrimSpace(workerID)] != nil
}

func (c *AgentControl) workerExecutionReleaseSignal(workerID string) <-chan struct{} {
	if c == nil {
		return closedWorkerExecutionSignal()
	}
	c.workerLeaseMu.Lock()
	lease := c.workerLeases[strings.TrimSpace(workerID)]
	c.workerLeaseMu.Unlock()
	if lease == nil {
		return closedWorkerExecutionSignal()
	}
	return lease.releasedSignal()
}

// OwnedWorkerExecutionCount returns the number of worker executions whose
// cross-process leases are currently held by this AgentControl.
func (c *AgentControl) OwnedWorkerExecutionCount() int {
	if c == nil {
		return 0
	}
	c.workerLeaseMu.Lock()
	defer c.workerLeaseMu.Unlock()
	count := 0
	for _, lease := range c.workerLeases {
		if lease != nil {
			count++
		}
	}
	return count
}

// WaitOwnedWorkerExecutions waits for physical release of the currently owned
// execution leases. Call BeginShutdown first to prevent new admission while
// draining. Cancellation stops waiting; it does not release another execution.
func (c *AgentControl) WaitOwnedWorkerExecutions(ctx context.Context) error {
	if c == nil {
		return nil
	}
	c.workerLeaseMu.Lock()
	signals := make([]<-chan struct{}, 0, len(c.workerLeases))
	for _, lease := range c.workerLeases {
		signals = append(signals, lease.releasedSignal())
	}
	c.workerLeaseMu.Unlock()
	for _, released := range signals {
		select {
		case <-released:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

// HasOwnedWorkerExecutions reports whether this AgentControl still owns any
// cross-process worker execution lease.
func (c *AgentControl) HasOwnedWorkerExecutions() bool {
	return c.OwnedWorkerExecutionCount() > 0
}

// WorkerExecutionActive reports whether workerID has either a live operating-
// system execution lease or a durable terminal intent that has not been
// acknowledged. A successful physical probe releases its temporary lease
// before returning.
func WorkerExecutionActive(rootDir, workerID string) (bool, error) {
	return workerExecutionActive(rootDir, workerID, tryAcquireWorkerExecutionLease)
}

func workerExecutionActive(rootDir, workerID string, acquire func(string, string) (*workerExecutionLease, bool, error)) (bool, error) {
	if pendingPath := workerTerminalFinalizationPath(rootDir, workerID); pendingPath != "" {
		if _, err := os.Stat(pendingPath); err == nil {
			return true, nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return false, fmt.Errorf("inspect worker terminal ownership: %w", err)
		}
	}
	lease, acquired, err := acquire(rootDir, workerID)
	if err != nil {
		return false, err
	}
	if !acquired {
		return true, nil
	}
	// Terminal preparation writes its durable record while the worker lease is
	// still held. Inspect it under that same lease, so a finalizing worker cannot
	// publish the record between an earlier stat and this ownership check.
	active := false
	var inspectErr error
	if pendingPath := workerTerminalFinalizationPath(rootDir, workerID); pendingPath != "" {
		if _, err := os.Stat(pendingPath); err == nil {
			active = true
		} else if !errors.Is(err, os.ErrNotExist) {
			inspectErr = fmt.Errorf("inspect worker terminal ownership: %w", err)
		}
	}
	if err := lease.release(); err != nil {
		return false, errors.Join(inspectErr, fmt.Errorf("release worker execution probe: %w", err))
	}
	return active, inspectErr
}
