package agentcontrol

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/subagent"
)

// agentResultConsumerFrozenSnapshot marks a held worker result as consumed by
// the whole-tree status snapshot the next user turn receives after a freeze.
// Claiming it keeps later terminal-record replays from waking the cancelled
// parent with a duplicate delivery.
const agentResultConsumerFrozenSnapshot = "frozen_tree_snapshot"

// FrozenWorkerResult is one worker result settled by ResolveFrozenWorkerTree:
// it reached a terminal state around the freeze but its parent delivery was
// gated, so the next root turn's snapshot is its consumer.
type FrozenWorkerResult struct {
	Snapshot subagent.SubAgentSnapshot
}

// FreezeWorkerTree implements turn/interrupt's "freeze this work" contract:
// cancel every live worker and queued spawn on this control, preserve their
// partial results as resumable state, and gate nested-result wakes so a
// terminal transition racing the freeze cannot restart a cancelled parent.
// The freeze holds until ResolveFrozenWorkerTree.
func (c *AgentControl) FreezeWorkerTree() {
	if c == nil {
		return
	}
	c.treeFrozen.Store(true)
	c.StopAll()
}

// WorkerTreeFrozen reports whether a turn interrupt froze this control's
// worker tree and no user turn has consumed the freeze yet.
func (c *AgentControl) WorkerTreeFrozen() bool {
	if c == nil {
		return false
	}
	return c.treeFrozen.Load()
}

// ResolveFrozenWorkerTree lifts the freeze for the next user turn. Deferred
// terminal records left by gated deliveries are settled here: their held
// results are claimed for the returned snapshot and the records acknowledged,
// so recovery neither re-delivers them nor auto-restarts their parents — the
// root decides which workers to resume (send_message).
func (c *AgentControl) ResolveFrozenWorkerTree() []FrozenWorkerResult {
	if c == nil {
		return nil
	}
	defer c.treeFrozen.Store(false)
	if strings.TrimSpace(c.harnessDir) == "" {
		return nil
	}
	dir := filepath.Join(c.harnessDir, workerTerminalFinalizationDirName)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []FrozenWorkerResult
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		data, readErr := os.ReadFile(filepath.Join(dir, entry.Name()))
		if readErr != nil {
			continue
		}
		var rec workerTerminalFinalizationRecord
		if decodeErr := json.Unmarshal(data, &rec); decodeErr != nil || rec.SchemaVersion != workerTerminalFinalizationSchema || strings.TrimSpace(rec.AgentID) == "" || !subagent.IsTerminal(rec.Status) {
			continue
		}
		snap := rec.notification().Snapshot
		resultID, claimed, consumedBy, claimErr := c.claimAgentResultDelivery(snap, agentResultConsumerFrozenSnapshot)
		if claimErr != nil {
			// Leave the record for terminal recovery rather than dropping a
			// result the snapshot could not claim durably.
			providers.DebugLogf("agentcontrol: claim frozen result %s: %v", rec.AgentID, claimErr)
			continue
		}
		if !claimed && consumedBy == agentResultConsumerNestedPending {
			// A pre-freeze delivery reserved the result but its wake was
			// gated; move the claim to the snapshot so startup replay does
			// not restart the parent.
			if _, _, transitionErr := c.transitionAgentResultDeliveryConsumer(resultID, agentResultConsumerNestedPending, agentResultConsumerFrozenSnapshot); transitionErr != nil {
				providers.DebugLogf("agentcontrol: transition frozen result %s: %v", rec.AgentID, transitionErr)
				continue
			}
		}
		if ackErr := c.acknowledgeWorkerTerminalFinalization(rec.AgentID); ackErr != nil {
			providers.DebugLogf("agentcontrol: acknowledge frozen terminal %s: %v", rec.AgentID, ackErr)
		}
		out = append(out, FrozenWorkerResult{Snapshot: snap})
	}
	return out
}
