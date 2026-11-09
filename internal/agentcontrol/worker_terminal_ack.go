package agentcontrol

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/securefs"
	"github.com/blueberrycongee/wuu/internal/subagent"
)

const (
	workerTerminalFinalizationDirName = "terminal-finalizations"
	workerTerminalFinalizationSchema  = "wuu/worker-terminal-finalization/v1"
	workerTerminalRetryDelay          = 100 * time.Millisecond
	workerTerminalRecoveryPoll        = 250 * time.Millisecond
)

// workerTerminalFinalizationRecord is the durable handoff between the worker
// execution owner and terminal consumers such as the app-server participant
// run store. It carries the exact terminal generation so recovery never has to
// infer it from a later best-effort notification or a mutable manager entry.
type workerTerminalFinalizationRecord struct {
	SchemaVersion string                             `json:"schema_version"`
	AgentID       string                             `json:"agent_id"`
	Status        subagent.Status                    `json:"status"`
	Snapshot      workerTerminalFinalizationSnapshot `json:"snapshot"`
	CreatedAt     time.Time                          `json:"created_at"`
}

type workerTerminalFinalizationSnapshot struct {
	ID                  string          `json:"id"`
	ParticipantID       string          `json:"participant_id,omitempty"`
	Type                string          `json:"type,omitempty"`
	TaskName            string          `json:"task_name,omitempty"`
	AgentProfile        string          `json:"agent_profile,omitempty"`
	AgentPath           string          `json:"agent_path,omitempty"`
	ParentID            string          `json:"parent_id,omitempty"`
	Description         string          `json:"description,omitempty"`
	WorkerRoot          string          `json:"worker_root,omitempty"`
	Model               string          `json:"model,omitempty"`
	ModelPin            string          `json:"model_pin,omitempty"`
	Status              subagent.Status `json:"status"`
	StartedAt           time.Time       `json:"started_at,omitempty"`
	CompletedAt         time.Time       `json:"completed_at,omitempty"`
	Result              string          `json:"result,omitempty"`
	Error               string          `json:"error,omitempty"`
	InputTokens         int             `json:"input_tokens,omitempty"`
	OutputTokens        int             `json:"output_tokens,omitempty"`
	CacheCreationTokens int             `json:"cache_creation_tokens,omitempty"`
	CacheReadTokens     int             `json:"cache_read_tokens,omitempty"`
}

func newWorkerTerminalFinalizationRecord(n subagent.Notification) workerTerminalFinalizationRecord {
	snap := n.Snapshot
	status := n.Status
	if status == "" {
		status = snap.Status
	}
	agentID := strings.TrimSpace(snap.ID)
	if agentID == "" {
		agentID = strings.TrimSpace(n.AgentID)
	}
	errText := ""
	if snap.Error != nil {
		errText = snap.Error.Error()
	}
	return workerTerminalFinalizationRecord{
		SchemaVersion: workerTerminalFinalizationSchema,
		AgentID:       agentID,
		Status:        status,
		Snapshot: workerTerminalFinalizationSnapshot{
			ID:                  agentID,
			ParticipantID:       snap.ParticipantID,
			Type:                snap.Type,
			TaskName:            snap.TaskName,
			AgentProfile:        snap.AgentProfile,
			AgentPath:           snap.AgentPath,
			ParentID:            snap.ParentID,
			Description:         snap.Description,
			WorkerRoot:          snap.WorkerRoot,
			Model:               snap.Model,
			ModelPin:            snap.ModelPin,
			Status:              status,
			StartedAt:           snap.StartedAt,
			CompletedAt:         snap.CompletedAt,
			Result:              snap.Result,
			Error:               errText,
			InputTokens:         snap.InputTokens,
			OutputTokens:        snap.OutputTokens,
			CacheCreationTokens: snap.CacheCreationTokens,
			CacheReadTokens:     snap.CacheReadTokens,
		},
		CreatedAt: time.Now().UTC(),
	}
}

func (r workerTerminalFinalizationRecord) notification() subagent.Notification {
	snap := subagent.SubAgentSnapshot{
		ID:                  r.Snapshot.ID,
		ParticipantID:       r.Snapshot.ParticipantID,
		Type:                r.Snapshot.Type,
		TaskName:            r.Snapshot.TaskName,
		AgentProfile:        r.Snapshot.AgentProfile,
		AgentPath:           r.Snapshot.AgentPath,
		ParentID:            r.Snapshot.ParentID,
		Description:         r.Snapshot.Description,
		WorkerRoot:          r.Snapshot.WorkerRoot,
		Model:               r.Snapshot.Model,
		ModelPin:            r.Snapshot.ModelPin,
		Status:              r.Snapshot.Status,
		StartedAt:           r.Snapshot.StartedAt,
		CompletedAt:         r.Snapshot.CompletedAt,
		Result:              r.Snapshot.Result,
		InputTokens:         r.Snapshot.InputTokens,
		OutputTokens:        r.Snapshot.OutputTokens,
		CacheCreationTokens: r.Snapshot.CacheCreationTokens,
		CacheReadTokens:     r.Snapshot.CacheReadTokens,
	}
	if strings.TrimSpace(r.Snapshot.Error) != "" {
		snap.Error = errors.New(r.Snapshot.Error)
	}
	return subagent.Notification{AgentID: r.AgentID, Status: r.Status, Snapshot: snap}
}

func workerTerminalFinalizationPath(rootDir, workerID string) string {
	rootDir = strings.TrimSpace(rootDir)
	workerID = strings.TrimSpace(workerID)
	if rootDir == "" || workerID == "" {
		return ""
	}
	digest := sha256.Sum256([]byte(workerID))
	return filepath.Join(rootDir, workerTerminalFinalizationDirName, hex.EncodeToString(digest[:])+".json")
}

func (c *AgentControl) persistWorkerTerminalFinalization(n subagent.Notification) error {
	if c == nil || strings.TrimSpace(c.harnessDir) == "" {
		return nil
	}
	rec := newWorkerTerminalFinalizationRecord(n)
	if rec.AgentID == "" {
		return errors.New("persist worker terminal finalization: agent id is required")
	}
	if !subagent.IsTerminal(rec.Status) {
		return fmt.Errorf("persist worker terminal finalization %s: status %q is not terminal", rec.AgentID, rec.Status)
	}
	data, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return fmt.Errorf("encode worker terminal finalization %s: %w", rec.AgentID, err)
	}
	path := workerTerminalFinalizationPath(c.harnessDir, rec.AgentID)
	if err := securefs.WriteFileAtomic(path, data); err != nil {
		return fmt.Errorf("write worker terminal finalization %s: %w", rec.AgentID, err)
	}
	return nil
}

func (c *AgentControl) acknowledgeWorkerTerminalFinalization(workerID string) error {
	if c == nil {
		return nil
	}
	path := workerTerminalFinalizationPath(c.harnessDir, workerID)
	if path == "" {
		return nil
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove worker terminal finalization %s: %w", workerID, err)
	}
	return nil
}

func (c *AgentControl) workerTerminalFinalizationPending(workerID string) (bool, error) {
	if c == nil {
		return false, nil
	}
	path := workerTerminalFinalizationPath(c.harnessDir, workerID)
	if path == "" {
		return false, nil
	}
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, fmt.Errorf("inspect worker terminal finalization %s: %w", workerID, err)
	}
	return true, nil
}

// HasPendingWorkerTerminalFinalizations reports durable terminal ownership
// that still needs an attached consumer to acknowledge it. Unlike the OS
// lease, this survives process exit.
func (c *AgentControl) HasPendingWorkerTerminalFinalizations() bool {
	pending, err := c.PendingWorkerTerminalFinalizations()
	return err != nil || pending
}

// PendingWorkerTerminalFinalizations reports durable terminal ownership and
// preserves the inspection error so shutdown callers can return a diagnostic
// instead of deleting worktrees when the barrier cannot be verified.
func (c *AgentControl) PendingWorkerTerminalFinalizations() (bool, error) {
	if c == nil || strings.TrimSpace(c.harnessDir) == "" {
		return false, nil
	}
	return WorkerTerminalFinalizationsPending(c.harnessDir)
}

// WorkerTerminalFinalizationsPending reports whether a harness directory owns
// any unacknowledged terminal intent, including a malformed record that must be
// repaired rather than silently deleted with its parent thread.
func WorkerTerminalFinalizationsPending(rootDir string) (bool, error) {
	rootDir = strings.TrimSpace(rootDir)
	if rootDir == "" {
		return false, nil
	}
	entries, err := os.ReadDir(filepath.Join(rootDir, workerTerminalFinalizationDirName))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".json") {
			return true, nil
		}
	}
	return false, nil
}

func (c *AgentControl) retryWorkerTerminalStep(workerID, step string, allowDurableYield bool, work func() error) bool {
	for attempt := 1; ; attempt++ {
		if err := work(); err == nil {
			return true
		} else if attempt == 1 || attempt%10 == 0 {
			providers.DebugLogf("agentcontrol: worker %s terminal %s attempt %d: %v", workerID, step, attempt, err)
		}
		if allowDurableYield && c.workerTerminalYieldRequested() {
			return false
		}
		time.Sleep(workerTerminalRetryDelay)
	}
}

// prepareWorkerTerminal is the first phase installed on subagent.Manager. It
// runs before the final worker snapshot is persisted, making this exact
// terminal generation the crash-recovery authority for every later phase.
// After YieldWorkerTerminalFinalizations a persist failure stops the manager's
// prepare retries: the durable launch intent (queue payload) or startup task
// reconciliation is then the restart recovery authority instead of a terminal
// record this process can no longer write.
func (c *AgentControl) prepareWorkerTerminal(n subagent.Notification) error {
	err := c.persistWorkerTerminalFinalization(n)
	if err != nil && c.workerTerminalYieldRequested() {
		return fmt.Errorf("%w: agent control yielded terminal finalizations: %v", subagent.ErrTerminalPrepareStopped, err)
	}
	return err
}

type workerTerminalCommitOutcome uint8

const (
	workerTerminalCommitted workerTerminalCommitOutcome = iota
	workerTerminalContinued
	workerTerminalDeferred
)

type workerNotificationOutcome uint8

const (
	workerNotificationSettled workerNotificationOutcome = iota
	workerNotificationContinued
	workerNotificationDeferred
)

func (c *AgentControl) finalizeWorkerTerminalWithAck(n subagent.Notification) workerTerminalCommitOutcome {
	workerID := strings.TrimSpace(n.AgentID)
	if workerID == "" {
		workerID = strings.TrimSpace(n.Snapshot.ID)
	}
	// A queued launcher owns the launch-intent acknowledgement until
	// Manager.Spawn is published. A zero-latency terminal observer must not
	// start a competing retry loop: leave the exact terminal record durable,
	// attempt lease release (which the launcher temporarily blocks), and let
	// recovery replay the whole terminal transaction after queue ack commits.
	if c.queuedLaunchAcknowledgementPending(workerID) {
		return workerTerminalDeferred
	}
	durable := workerTerminalFinalizationPath(c.harnessDir, workerID) != ""
	var notificationOutcome workerNotificationOutcome
	if !c.retryWorkerTerminalStep(workerID, "internal finalize", durable, func() error {
		outcome, err := c.consumeWorkerNotification(n)
		if err == nil {
			notificationOutcome = outcome
		}
		return err
	}) {
		return workerTerminalDeferred
	}
	// A requires_report completion can synchronously admit its one mechanical
	// closing turn. Its prepared intent remains the recovery authority until the
	// closing generation overwrites it; execution ownership must not be released.
	// Use the outcome of this exact notification instead of the manager's mutable
	// snapshot: a zero-latency closing turn may already be terminal by the time
	// this observer resumes.
	if notificationOutcome == workerNotificationContinued {
		return workerTerminalContinued
	}
	// An undelivered nested result leaves the durable terminal record as the
	// delivery authority: recovery replays it once the parent can claim, so
	// none of the acknowledgement steps below may consume the record now.
	if notificationOutcome == workerNotificationDeferred {
		return workerTerminalDeferred
	}
	if c.hasWorkerTerminalFinalizers() {
		if !c.retryWorkerTerminalStep(workerID, "external finalize", durable, func() error {
			return c.runWorkerTerminalFinalizers(n)
		}) {
			return workerTerminalDeferred
		}
	}
	// A queued worker can finish while its launch acknowledgement is retrying.
	// Consume that launch intent before acknowledging the terminal record; as
	// long as the terminal record remains durable, queue recovery must never run
	// the same worker generation again.
	if !c.retryWorkerTerminalStep(workerID, "queue acknowledge", durable, func() error {
		_, err := c.ackQueuedSpawn(workerID)
		return err
	}) {
		return workerTerminalDeferred
	}
	if !c.retryWorkerTerminalStep(workerID, "acknowledge", durable, func() error {
		return c.acknowledgeWorkerTerminalFinalization(workerID)
	}) {
		return workerTerminalDeferred
	}
	return workerTerminalCommitted
}

// YieldWorkerTerminalFinalizations stops in-memory retries only after their
// exact terminal intent is durable. Server shutdown uses this to hand
// ownership to the pending-record barrier instead of either hanging forever
// on an external store outage or releasing an unrecorded terminal state.
func (c *AgentControl) YieldWorkerTerminalFinalizations() {
	if c == nil || c.workerTerminalYield == nil {
		return
	}
	// Serialize the yield signal with recovery admission. A recovery either
	// joins the wait group before this point or observes the closed signal and
	// never starts; WaitGroup.Add can therefore never race a zero-count Wait.
	c.workerTerminalRecoveryMu.Lock()
	c.workerTerminalYieldOnce.Do(func() { close(c.workerTerminalYield) })
	c.workerTerminalRecoveryMu.Unlock()
	c.workerTerminalRecoveryWG.Wait()
}

func (c *AgentControl) workerTerminalYieldRequested() bool {
	if c == nil || c.workerTerminalYield == nil {
		return false
	}
	select {
	case <-c.workerTerminalYield:
		return true
	default:
		return false
	}
}

// StartWorkerTerminalRecovery begins replaying durable terminal intents. The
// app-server calls it only after the finalizer and owning thread runtime are
// fully installed, preventing recovered completions from racing construction.
// It is safe to call more than once.
func (c *AgentControl) StartWorkerTerminalRecovery() {
	if c == nil || strings.TrimSpace(c.harnessDir) == "" {
		return
	}
	c.workerTerminalRecoveryOnce.Do(func() {
		go c.recoverPendingWorkerTerminals()
	})
}

func (c *AgentControl) recoverPendingWorkerTerminals() {
	ticker := time.NewTicker(workerTerminalRecoveryPoll)
	defer ticker.Stop()
	for {
		if c.workerTerminalYieldRequested() {
			return
		}
		c.scanPendingWorkerTerminals()
		if c.statusStop == nil {
			<-ticker.C
			continue
		}
		select {
		case <-ticker.C:
		case <-c.statusStop:
			return
		}
	}
}

func (c *AgentControl) scanPendingWorkerTerminals() {
	if c.workerTerminalYieldRequested() {
		return
	}
	dir := filepath.Join(c.harnessDir, workerTerminalFinalizationDirName)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			providers.DebugLogf("agentcontrol: scan terminal finalizations: %v", err)
		}
		return
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		data, readErr := os.ReadFile(filepath.Join(dir, entry.Name()))
		if readErr != nil {
			providers.DebugLogf("agentcontrol: read terminal finalization %s: %v", entry.Name(), readErr)
			continue
		}
		var rec workerTerminalFinalizationRecord
		if decodeErr := json.Unmarshal(data, &rec); decodeErr != nil || rec.SchemaVersion != workerTerminalFinalizationSchema || strings.TrimSpace(rec.AgentID) == "" || !subagent.IsTerminal(rec.Status) {
			providers.DebugLogf("agentcontrol: invalid terminal finalization %s: %v", entry.Name(), decodeErr)
			continue
		}
		expected := filepath.Base(workerTerminalFinalizationPath(c.harnessDir, rec.AgentID))
		if entry.Name() != expected {
			providers.DebugLogf("agentcontrol: terminal finalization %s does not match worker %s", entry.Name(), rec.AgentID)
			continue
		}
		c.startPendingWorkerTerminalRecovery(rec)
	}
}

func (c *AgentControl) startPendingWorkerTerminalRecovery(rec workerTerminalFinalizationRecord) {
	if c.workerTerminalYieldRequested() {
		return
	}
	workerID := strings.TrimSpace(rec.AgentID)
	// A parked waiting_children parent keeps its terminal record deferred on
	// purpose: the wake path (child delivery -> Followup -> fresh terminal)
	// owns re-driving it. Rescanning it every poll would just re-park.
	if c.manager != nil {
		if sa := c.manager.Get(workerID); sa != nil && sa.Snapshot().Status == subagent.StatusWaitingChildren {
			return
		}
	}
	c.workerTerminalRecoveryMu.Lock()
	if c.workerTerminalRecovering == nil {
		c.workerTerminalRecovering = make(map[string]struct{})
	}
	// Yield closes the signal while holding this same mutex. Recheck here so a
	// scanner that observed the old state cannot register work behind shutdown.
	if c.workerTerminalYieldRequested() {
		c.workerTerminalRecoveryMu.Unlock()
		return
	}
	if _, recovering := c.workerTerminalRecovering[workerID]; recovering {
		c.workerTerminalRecoveryMu.Unlock()
		return
	}
	c.workerTerminalRecovering[workerID] = struct{}{}
	c.workerTerminalRecoveryWG.Add(1)
	c.workerTerminalRecoveryMu.Unlock()
	go func() {
		defer c.workerTerminalRecoveryWG.Done()
		defer func() {
			c.workerTerminalRecoveryMu.Lock()
			delete(c.workerTerminalRecovering, workerID)
			c.workerTerminalRecoveryMu.Unlock()
		}()
		if c.workerTerminalYieldRequested() {
			return
		}
		unlockTransition := c.lockWorkerTransition(workerID)
		defer unlockTransition()
		if c.workerTerminalYieldRequested() {
			return
		}
		pending, err := c.workerTerminalFinalizationPending(workerID)
		if err != nil || !pending || c.workerExecutionOwned(workerID) {
			return
		}
		if c.workerTerminalYieldRequested() {
			return
		}
		acquired, err := c.acquireWorkerExecution(workerID)
		if err != nil || !acquired {
			return
		}
		releaseExecution := true
		defer func() {
			if releaseExecution {
				c.releaseWorkerExecution(workerID)
				c.cleanupRetiringWorkerTerminalFinalizers()
			}
		}()
		c.workerReleaseHookMu.Lock()
		beforeRecovery := c.beforeWorkerTerminalRecoveryForTest
		c.workerReleaseHookMu.Unlock()
		if beforeRecovery != nil {
			beforeRecovery(workerID)
		}
		// The lease was acquired after the earlier checks. If shutdown yielded
		// while acquire was in progress, hand the durable intent back without
		// invoking an external finalizer that may be about to retire.
		if c.workerTerminalYieldRequested() {
			return
		}
		if outcome := c.finalizeWorkerTerminalWithAck(rec.notification()); outcome == workerTerminalContinued {
			// A requires_report completion admitted its closing turn. That new
			// generation owns the same lease and its normal terminal observer will
			// release it; recovery must not expose the in-flight turn to contenders.
			releaseExecution = false
		}
	}()
}
