package agentcontrol

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/blueberrycongee/wuu/internal/agentthread"
	"github.com/blueberrycongee/wuu/internal/subagent"
)

type workerTerminalFinalizer struct {
	finalize func(subagent.Notification) error
	retiring bool
}

func (c *AgentControl) lockWorkerTransition(workerID string) func() {
	if c == nil {
		return func() {}
	}
	workerID = strings.TrimSpace(workerID)
	c.workerTransitionMu.Lock()
	if c.workerTransitions == nil {
		c.workerTransitions = make(map[string]*sync.Mutex)
	}
	transition := c.workerTransitions[workerID]
	if transition == nil {
		transition = &sync.Mutex{}
		c.workerTransitions[workerID] = transition
	}
	c.workerTransitionMu.Unlock()
	transition.Lock()
	return transition.Unlock
}

// workerTerminalFollowupWait bounds how long a follow-up may wait for the
// target worker's reliable terminal transition to settle. Callers without a
// cancellable context would otherwise hang their turn forever behind a
// terminal record that cannot be finalized.
const workerTerminalFollowupWait = 30 * time.Second

var errWorkerEdgeClosed = errors.New("worker edge is closed")

func (c *AgentControl) workerEdgeClosed(workerID string) bool {
	// StopAll cancels running turns before it can persist each closed edge.
	// Treat the whole tree as closed during that wave so a cancelled child's
	// result is released and settled instead of waking its parked parent.
	c.queueStartMu.Lock()
	stoppingAll := c.queuePauses > 0
	c.queueStartMu.Unlock()
	if stoppingAll {
		return true
	}
	meta, ok := c.threads.Resolve(workerID)
	return ok && meta.Source.EdgeStatus == agentthread.EdgeClosed
}

func (c *AgentControl) prepareWorkerFollowup(ctx context.Context, workerID string, automatic bool) (subagent.SubAgentSnapshot, bool, func(), error) {
	if ctx == nil {
		ctx = context.Background()
	}
	deadline := time.NewTimer(workerTerminalFollowupWait)
	defer deadline.Stop()
	deadlineErr := func() error {
		return fmt.Errorf("worker %q terminal transition did not settle within %s", workerID, workerTerminalFollowupWait)
	}
	for {
		if c.isStopping() {
			return subagent.SubAgentSnapshot{}, false, nil, errAgentControlStopping
		}
		unlockTransition := c.lockWorkerTransition(workerID)
		// Stop and automatic resumption share this lock. A cancellation result
		// must not resurrect its stopped parent, even before that parent's
		// asynchronous terminal observer has finished.
		if automatic && c.workerEdgeClosed(workerID) {
			// Unlike other errors, this returns the held lock: the caller must
			// settle its result reservation before an explicit resume reopens
			// this edge and starts applying pending results.
			return subagent.SubAgentSnapshot{}, false, unlockTransition, errWorkerEdgeClosed
		}
		if pending, err := c.workerTerminalFinalizationPending(workerID); err != nil {
			unlockTransition()
			return subagent.SubAgentSnapshot{}, false, nil, err
		} else if pending {
			if !c.hasWorkerTerminalFinalizers() {
				unlockTransition()
				return subagent.SubAgentSnapshot{}, false, nil, fmt.Errorf("worker %q has unacknowledged terminal state and no terminal finalizer is attached", workerID)
			}
			unlockTransition()
			timer := time.NewTimer(workerTerminalRetryDelay)
			select {
			case <-timer.C:
			case <-deadline.C:
				timer.Stop()
				return subagent.SubAgentSnapshot{}, false, nil, deadlineErr()
			case <-ctx.Done():
				timer.Stop()
				return subagent.SubAgentSnapshot{}, false, nil, ctx.Err()
			}
			continue
		}
		newLease, err := c.acquireWorkerExecution(workerID)
		if err != nil {
			unlockTransition()
			return subagent.SubAgentSnapshot{}, false, nil, err
		}
		sa := c.manager.Get(workerID)
		if sa == nil {
			rehydrated, rehydrateErr := c.rehydrateAgent(workerID)
			if rehydrateErr != nil {
				if newLease {
					c.releaseWorkerExecution(workerID)
				}
				unlockTransition()
				return subagent.SubAgentSnapshot{}, false, nil, rehydrateErr
			}
			if rehydrated == nil {
				if newLease {
					c.releaseWorkerExecution(workerID)
				}
				unlockTransition()
				return subagent.SubAgentSnapshot{}, false, nil, c.missingAgentError(workerID)
			}
			sa = rehydrated
		}
		if newLease {
			if err := c.manager.ReconcileToolLedger(ctx, workerID); err != nil {
				c.releaseWorkerExecution(workerID)
				unlockTransition()
				return subagent.SubAgentSnapshot{}, false, nil, fmt.Errorf("recover tool ledger for worker %q: %w", workerID, err)
			}
		}
		current := sa.Snapshot()
		if isFinalSubAgentStatus(current.Status) && !newLease {
			// The manager has published its terminal snapshot, but the lease is
			// still locally owned, so the reliable terminal observer has not yet
			// finished. Yield the transition lock to that observer and retry only
			// after it hands off execution ownership.
			released := c.workerExecutionReleaseSignal(workerID)
			unlockTransition()
			select {
			case <-released:
				continue
			case <-deadline.C:
				return subagent.SubAgentSnapshot{}, false, nil, deadlineErr()
			case <-ctx.Done():
				return subagent.SubAgentSnapshot{}, false, nil, ctx.Err()
			}
		}
		return current, newLease, unlockTransition, nil
	}
}

// SubscribeWorkerTerminalFinalizer registers reliable terminal work that must
// acknowledge success before another process can run the worker. Returning an
// error keeps the terminal intent pending and retains execution ownership. The
// returned function retires the subscription immediately when the control is
// idle, or after its last already-owned worker lease reaches a terminal state.
func (c *AgentControl) SubscribeWorkerTerminalFinalizer(finalize func(subagent.Notification) error) func() {
	if c == nil || finalize == nil {
		return func() {}
	}
	c.workerTerminalFinalizerMu.Lock()
	c.nextWorkerTerminalFinalizerID++
	id := c.nextWorkerTerminalFinalizerID
	if c.workerTerminalFinalizers == nil {
		c.workerTerminalFinalizers = make(map[uint64]*workerTerminalFinalizer)
	}
	c.workerTerminalFinalizers[id] = &workerTerminalFinalizer{finalize: finalize}
	c.workerTerminalFinalizerMu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			c.workerTerminalFinalizerMu.Lock()
			if finalizer := c.workerTerminalFinalizers[id]; finalizer != nil {
				finalizer.retiring = true
			}
			c.workerTerminalFinalizerMu.Unlock()
			c.cleanupRetiringWorkerTerminalFinalizers()
		})
	}
}

func (c *AgentControl) runWorkerTerminalFinalizers(notification subagent.Notification) error {
	if c == nil {
		return nil
	}
	c.workerTerminalFinalizerMu.Lock()
	finalizers := make([]func(subagent.Notification) error, 0, len(c.workerTerminalFinalizers))
	for _, finalizer := range c.workerTerminalFinalizers {
		if finalizer != nil && finalizer.finalize != nil {
			finalizers = append(finalizers, finalizer.finalize)
		}
	}
	c.workerTerminalFinalizerMu.Unlock()
	if len(finalizers) == 0 {
		return errors.New("no worker terminal finalizer is attached")
	}
	var err error
	for _, finalize := range finalizers {
		err = errors.Join(err, finalize(notification))
	}
	return err
}

func (c *AgentControl) hasWorkerTerminalFinalizers() bool {
	if c == nil {
		return false
	}
	c.workerTerminalFinalizerMu.Lock()
	defer c.workerTerminalFinalizerMu.Unlock()
	return len(c.workerTerminalFinalizers) > 0
}

func (c *AgentControl) cleanupRetiringWorkerTerminalFinalizers() {
	if c == nil || c.HasOwnedWorkerExecutions() {
		return
	}
	c.workerTerminalFinalizerMu.Lock()
	defer c.workerTerminalFinalizerMu.Unlock()
	if c.HasOwnedWorkerExecutions() {
		return
	}
	for id, finalizer := range c.workerTerminalFinalizers {
		if finalizer == nil || finalizer.retiring {
			delete(c.workerTerminalFinalizers, id)
		}
	}
}
