package appserver

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/session"
)

var errThreadExecutionBusy = errors.New("thread execution is owned by another app-server")
var errRetryableTurnAdmission = errors.New("turn admission can be retried")
var errAgentCompletionAlreadyDelivered = errors.New("agent completion is already delivered")

// Background queues should make progress promptly after the owner exits
// without hot-spinning against the lease while it is live.
const threadExecutionLeaseRetryDelay = 200 * time.Millisecond

func (s *Server) refreshExtensions(cfg config.Config) error {
	if s != nil && s.refreshExtensionsForTest != nil {
		return s.refreshExtensionsForTest(cfg)
	}
	if s == nil || s.rt == nil {
		return errors.New("runtime is not initialized")
	}
	return s.rt.RefreshExtensions(cfg)
}

func (s *Server) tryAcquireThreadExecutionLeaseLocked(th *threadState) (bool, error) {
	if th == nil {
		return false, errors.New("thread is required")
	}
	usesInteractiveExtensions := strings.TrimSpace(th.NamedAgentID) == "" || (s.rt != nil && s.rt.HasCollaborationTools())
	if usesInteractiveExtensions && s != nil && s.pluginGenerationMutation.Load() {
		return false, nil
	}
	if th.admissionReserved || th.executionLease != nil || th.runtimeSelectionMutation {
		// The same server may be between durable admission and startTurnLocked,
		// where running is intentionally still false but the lease is already a
		// local reservation. Treat it exactly like an external owner.
		return false, nil
	}
	newPluginLease := false
	if usesInteractiveExtensions && th.pluginExecutionLease == nil && s != nil && s.rt != nil && strings.TrimSpace(s.rt.WuuHome) != "" {
		lease, acquired, err := session.TryAcquirePluginGenerationExecutionLease(s.rt.WuuHome)
		if err != nil {
			return false, fmt.Errorf("acquire plugin generation execution lease: %w", err)
		}
		if !acquired {
			return false, nil
		}
		s.pluginGenerationRefreshMu.Lock()
		needsRecovery := s.rt.PluginGenerationNeedsRecovery()
		if epoch := lease.Epoch(); epoch != s.pluginGenerationEpoch.Load() || needsRecovery {
			if err := s.refreshExtensions(s.currentExtensionConfig()); err != nil {
				s.pluginGenerationRefreshMu.Unlock()
				_ = lease.Release()
				return false, fmt.Errorf("refresh plugin generation %d: %w", epoch, err)
			}
			s.pluginGenerationEpoch.Store(epoch)
			if needsRecovery {
				s.pluginRuntimeRevision.Add(1)
			}
		}
		s.pluginGenerationRefreshMu.Unlock()
		th.pluginExecutionLease = lease
		newPluginLease = true
	}
	th.admissionReserved = true
	if !th.PersistHistory {
		return true, nil
	}
	if s == nil || s.rt == nil || strings.TrimSpace(s.rt.SessionDir) == "" {
		th.admissionReserved = false
		if newPluginLease {
			th.releasePluginGenerationExecutionLeaseLocked()
		}
		return false, errors.New("session directory is required for durable thread execution")
	}
	lease, acquired, err := session.TryAcquireThreadExecutionLease(s.rt.SessionDir, th.ID)
	if err != nil {
		th.admissionReserved = false
		if newPluginLease {
			th.releasePluginGenerationExecutionLeaseLocked()
		}
		return false, fmt.Errorf("acquire execution lease for thread %q: %w", th.ID, err)
	}
	if !acquired {
		th.admissionReserved = false
		if newPluginLease {
			th.releasePluginGenerationExecutionLeaseLocked()
		}
		return false, nil
	}
	th.executionLease = lease
	return true, nil
}

func (s *Server) refreshDurableThreadHistoryLocked(th *threadState) error {
	if th == nil || !th.PersistHistory {
		return nil
	}
	loaded, err := s.loadPersistedThreadSnapshot(th.ID)
	if err != nil {
		return fmt.Errorf("refresh durable state for thread %q: %w", th.ID, err)
	}
	if loaded.repairNeeded {
		if err := s.rewriteChatHistoryUnderExecutionLease(s.rt.SessionDir, th.ID, loaded.repairedHistory, loaded.baselineSeq); err != nil {
			return fmt.Errorf("persist repaired history for thread %q: %w", th.ID, err)
		}
		// Re-read display rows after the rewrite so Turns and provider history
		// are rebuilt from the same committed snapshot.
		loaded, err = s.loadPersistedThreadSnapshot(th.ID)
		if err != nil {
			return fmt.Errorf("reload repaired state for thread %q: %w", th.ID, err)
		}
	}
	// Durable state is authoritative once execution ownership is ours. A
	// different app-server may have completed turns, changed focus, compacted,
	// or edited the thread since this process loaded its cached snapshot.
	applySessionMetadata(th, loaded.metadata)
	th.WorkspaceKind = workspaceKindForCWD(s.rt.WuuHome, th.CWD)
	th.History = cloneHistory(loaded.history)
	th.historyHeadSeq = loaded.baselineSeq
	th.Turns = turnsFromPersistedHistory(th.ID, loaded.displayHistory, time.Now().UTC(), s.resolveParticipantSummary)
	s.restorePluginToolLabels(th.Turns)
	th.Turns = applyTokenUsageMetasToTurns(th.Turns, loaded.tokenMetas)
	th.currentTurn = ""
	th.currentTurnKind = ""
	th.currentExecutionRunID = ""
	th.currentTurnResumed = false
	th.nextItemIndex = 0
	th.activeAgentItemID = ""
	th.activeReasoningItemID = ""
	th.toolItems = make(map[string]string)
	return nil
}

func threadExecutionBusyError(threadID string) error {
	return fmt.Errorf("thread %q already has a running turn in another app-server: %w", threadID, errThreadExecutionBusy)
}

// tryAcquireThreadMutationLease serializes destructive durable mutations with
// model turns without attaching the lease to currentTurn. Callers must release
// the returned lease after the mutation is fully committed.
func (s *Server) tryAcquireThreadMutationLease(threadID string) (*session.ThreadExecutionLease, error) {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return nil, errors.New("thread id is required")
	}
	if s == nil || s.rt == nil || strings.TrimSpace(s.rt.SessionDir) == "" {
		return nil, errors.New("session directory is required for durable thread mutation")
	}
	lease, acquired, err := session.TryAcquireThreadExecutionLease(s.rt.SessionDir, threadID)
	if err != nil {
		return nil, fmt.Errorf("acquire mutation lease for thread %q: %w", threadID, err)
	}
	if !acquired {
		return nil, threadExecutionBusyError(threadID)
	}
	return lease, nil
}

func releaseThreadMutationLease(threadID string, lease *session.ThreadExecutionLease) {
	if lease == nil {
		return
	}
	if err := lease.Release(); err != nil {
		providers.DebugLogf("release mutation lease for thread %q: %v", threadID, err)
	}
}

func (s *Server) rewriteChatHistoryUnderExecutionLease(sessDir, threadID string, history []providers.ChatMessage, baselineSeq int) error {
	if s != nil && s.rewriteChatHistoryForTest != nil {
		return s.rewriteChatHistoryForTest(sessDir, threadID, history)
	}
	return rewriteChatHistoryAtBaseline(sessDir, threadID, history, baselineSeq)
}

func (s *Server) scheduleThreadExecutionLeaseRetry(retry func()) {
	if s == nil || retry == nil || s.closed.Load() {
		return
	}
	time.AfterFunc(threadExecutionLeaseRetryDelay, func() {
		if !s.closed.Load() {
			retry()
		}
	})
}

func (th *threadState) releaseThreadExecutionLeaseLocked() {
	if th == nil {
		return
	}
	th.admissionReserved = false
	if th.executionLease == nil {
		th.maybeReleasePluginGenerationExecutionLeaseLocked()
		return
	}
	lease := th.executionLease
	th.executionLease = nil
	if err := lease.Release(); err != nil {
		providers.DebugLogf("release execution lease for thread %q: %v", th.ID, err)
	}
	th.maybeReleasePluginGenerationExecutionLeaseLocked()
}

func (th *threadState) maybeReleasePluginGenerationExecutionLeaseLocked() {
	if th == nil || th.pluginExecutionLease == nil || th.running {
		return
	}
	if th.execRuntime != nil && threadRuntimeHasOutstandingWork(th.ID, th.execRuntime) {
		th.schedulePluginGenerationLeaseReleaseLocked()
		return
	}
	th.releasePluginGenerationExecutionLeaseLocked()
}

func (th *threadState) schedulePluginGenerationLeaseReleaseLocked() {
	if th == nil || th.pluginLeaseReleaseLoop {
		return
	}
	th.pluginLeaseReleaseLoop = true
	go func() {
		ticker := time.NewTicker(threadExecutionLeaseRetryDelay)
		defer ticker.Stop()
		for range ticker.C {
			th.mu.Lock()
			if th.pluginExecutionLease == nil {
				th.pluginLeaseReleaseLoop = false
				th.mu.Unlock()
				return
			}
			if !th.running && (th.execRuntime == nil || !threadRuntimeHasOutstandingWork(th.ID, th.execRuntime)) {
				th.releasePluginGenerationExecutionLeaseLocked()
				th.pluginLeaseReleaseLoop = false
				th.mu.Unlock()
				return
			}
			th.mu.Unlock()
		}
	}()
}

func (th *threadState) releasePluginGenerationExecutionLeaseLocked() {
	if th == nil || th.pluginExecutionLease == nil {
		return
	}
	lease := th.pluginExecutionLease
	th.pluginExecutionLease = nil
	if err := lease.Release(); err != nil {
		providers.DebugLogf("release plugin generation execution lease for thread %q: %v", th.ID, err)
	}
}

// abortStartedThreadTurn rolls back the in-memory execution state when a turn
// was admitted but its goroutine was never launched. Cancellation alone is not
// enough because no runner exists to observe it and release the durable lease.
func abortStartedThreadTurn(th *threadState, started startedThreadTurn, cause error) {
	if started.cancel != nil {
		started.cancel()
	}
	if th == nil || strings.TrimSpace(started.turnID) == "" {
		return
	}
	if cause == nil {
		cause = errors.New("turn start aborted")
	}
	th.mu.Lock()
	defer th.mu.Unlock()
	if th.currentTurn != started.turnID {
		return
	}
	th.completeTurnLocked(started.turnID, TurnStatusFailed, cause, time.Now().UTC(), "", "", false)
}

const turnTerminalHistoryRecord = "turn_terminal"

// streamReconnectHistoryRecord marks the durable form of a settled
// stream_reconnect turn item, written only when the turn ends failed or
// interrupted so the settled row ("网络异常 · 第 n/m 次重试") survives reload.
const streamReconnectHistoryRecord = "stream_reconnect"

func (s *Server) persistTurnTerminal(th *threadState, turnID string, kind TurnKind, status TurnStatus, cause error, at time.Time, reconnect *ThreadItem) error {
	if s == nil || s.rt == nil || th == nil || !th.PersistHistory || strings.TrimSpace(turnID) == "" {
		return nil
	}
	if kind == TurnKindCompact {
		return nil
	}
	if status != TurnStatusCompleted && status != TurnStatusFailed && status != TurnStatusInterrupted {
		return nil
	}
	clientID := strings.TrimSpace(turnID)
	if kind == TurnKindInternal {
		// Internal continuations have no durable user-message boundary. Reload
		// intentionally folds their output into the current visible turn, so their
		// failure marker must target that same aggregate instead of an ephemeral ID.
		if status == TurnStatusCompleted {
			return nil
		}
		clientID = ""
	}
	if status == TurnStatusCompleted && kind != TurnKindUser {
		return nil
	}
	message := ""
	if cause != nil {
		message = cause.Error()
	}
	if err := session.AppendHistoryRecord(s.rt.SessionDir, th.ID, session.HistoryRecord{
		Role:           "meta",
		Content:        turnTerminalHistoryRecord,
		DisplayContent: message,
		ClientID:       clientID,
		StopReason:     string(status),
		At:             at,
	}); err != nil {
		return err
	}
	if reconnect == nil || status == TurnStatusCompleted {
		// Successful turns never persist a reconnect row: a recovery that
		// worked leaves no trace in history.
		return nil
	}
	return session.AppendHistoryRecord(s.rt.SessionDir, th.ID, session.HistoryRecord{
		Role:           "meta",
		Content:        streamReconnectHistoryRecord,
		DisplayContent: reconnect.Text,
		Cause:          reconnect.Reason,
		ClientID:       clientID,
		RetryCount:     reconnect.RetryCount,
		MaxRetries:     reconnect.MaxRetries,
		At:             at,
	})
}

// abortStartedThreadTurnDurably records a terminal projection for an ordinary
// user message that was already appended before its runner could be launched.
// Meta records are excluded from provider history but let restart projection
// distinguish a failed prelaunch from a completed user-only turn.
func (s *Server) abortStartedThreadTurnDurably(th *threadState, started startedThreadTurn, cause error) error {
	if cause == nil {
		cause = errors.New("turn start aborted")
	}
	var persistErr error
	if started.userMsgSeq > 0 {
		persistErr = s.persistTurnTerminal(th, started.turnID, TurnKindUser, TurnStatusFailed, cause, time.Now().UTC(), nil)
	}
	abortStartedThreadTurn(th, started, cause)
	if persistErr != nil {
		return fmt.Errorf("persist aborted turn %q: %w", started.turnID, persistErr)
	}
	return nil
}
