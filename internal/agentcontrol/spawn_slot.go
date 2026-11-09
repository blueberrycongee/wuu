package agentcontrol

import (
	"context"
	"strings"
	"sync"

	"github.com/blueberrycongee/wuu/internal/subagent"
)

// spawnSlotReservation bridges the process-local preparation window before
// Manager.Spawn makes a worker visible as running. maxParallel is an
// AgentControl-local limit, not a cross-process session semaphore; worker
// execution leases separately prevent two app-servers from running the same
// durable worker ID. Without this reservation, concurrent callers on one
// control can all observe the same free slot and oversubscribe maxParallel.
type spawnSlotReservation struct {
	control  *AgentControl
	workerID string
	once     sync.Once
}

func (c *AgentControl) tryReserveSpawnSlot(workerID string) (*spawnSlotReservation, bool) {
	if c == nil || c.manager == nil {
		return nil, false
	}
	c.spawnSlotMu.Lock()
	defer c.spawnSlotMu.Unlock()

	// A reservation covers only the preparation window before Manager.Spawn
	// registers the worker. Build one manager snapshot so a registered worker
	// and its not-yet-released reservation are never counted twice.
	registered := make(map[string]subagent.Status)
	running := 0
	for _, snap := range c.manager.List() {
		registered[snap.ID] = snap.Status
		if snap.Status == subagent.StatusRunning {
			running++
		}
	}
	pending := 0
	for reservation := range c.spawnReservations {
		id := strings.TrimSpace(reservation.workerID)
		if id == "" {
			pending++
			continue
		}
		if _, exists := registered[id]; !exists {
			pending++
		}
	}
	if running+pending >= c.maxParallel {
		return nil, false
	}
	reservation := &spawnSlotReservation{control: c, workerID: strings.TrimSpace(workerID)}
	if c.spawnReservations == nil {
		c.spawnReservations = make(map[*spawnSlotReservation]struct{})
	}
	c.spawnReservations[reservation] = struct{}{}
	return reservation, true
}

func (r *spawnSlotReservation) bindWorker(workerID string) {
	if r == nil || r.control == nil {
		return
	}
	c := r.control
	c.spawnSlotMu.Lock()
	if _, active := c.spawnReservations[r]; active {
		r.workerID = strings.TrimSpace(workerID)
	}
	c.spawnSlotMu.Unlock()
}

func (r *spawnSlotReservation) release() {
	r.releaseWithQueueKick(false)
}

func (r *spawnSlotReservation) releaseAndKickQueued() {
	r.releaseWithQueueKick(true)
}

func (r *spawnSlotReservation) releaseWithQueueKick(kickQueued bool) {
	if r == nil || r.control == nil {
		return
	}
	r.once.Do(func() {
		c := r.control
		c.spawnSlotMu.Lock()
		delete(c.spawnReservations, r)
		c.spawnSlotMu.Unlock()

		// Direct preparation uses kickQueued because a very short worker can
		// finish before Manager.Spawn returns. Queue drains already own their
		// retry/loop policy and release without a kick, avoiding recursive drains.
		if kickQueued && c.queuedWorkEnabled() && c.hasQueuedSpawns() {
			go c.maybeStartQueued(context.Background())
		}
	})
}

func (c *AgentControl) hasQueuedSpawns() bool {
	if c == nil {
		return false
	}
	c.queueMu.Lock()
	defer c.queueMu.Unlock()
	return len(c.queued) > 0
}
