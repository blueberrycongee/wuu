package appserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/runtime"
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

// Resolve provider policy before acquiring thread or execution lease locks.
func (s *Server) threadProjectAdmission(ctx context.Context, th *threadState) (int, error) {
	metadata, found, err := th.state.Sessions.Find(th.ID)
	if err != nil {
		return 0, err
	}
	if !found || (metadata.Source != projectSource && metadata.Source != projectSessionSource) {
		return 0, nil
	}
	policy, err := ProjectAdmission(s.projectServiceContext(ctx, th.state), th.state.Host, th.ID)
	return policy.MaxParallel, err
}

func (s *Server) tryAcquireThreadExecutionLeaseLocked(th *threadState, maxParallel int) (bool, error) {
	if th == nil {
		return false, errors.New("thread is required")
	}
	if projectExecutionDisabled(th.Source) {
		return false, errProjectAgentDisabled
	}
	if s != nil && s.pluginGenerationMutation.Load() {
		return false, nil
	}
	if th.admissionReserved || th.executionLease != nil || th.runtimeSelectionMutation {
		// The same server may be between durable admission and startTurnLocked,
		// where running is intentionally still false but the lease is already a
		// local reservation. Treat it exactly like an external owner.
		return false, nil
	}
	newPluginLease := false
	if th.pluginExecutionLease == nil && s != nil && s.rt != nil && strings.TrimSpace(s.rt.WuuHome) != "" {
		lease, acquired, err := session.TryAcquirePluginGenerationExecutionLease(s.rt.WuuHome)
		if err != nil {
			return false, fmt.Errorf("acquire plugin generation execution lease: %w", err)
		}
		if !acquired {
			return false, nil
		}
		// This function owns th.mu. Never initialize or activate plugins here:
		// their synchronous host callbacks can inspect or submit to this thread.
		if !s.pluginGenerationRefreshMu.TryLock() {
			_ = lease.Release()
			return false, nil
		}
		needsRefresh := lease.Epoch() != s.pluginGenerationEpoch.Load() || s.rt.PluginGenerationNeedsRecovery()
		s.pluginGenerationRefreshMu.Unlock()
		if needsRefresh {
			_ = lease.Release()
			_ = s.startBackgroundWithState(th.state, func(binding *runtime.State) {
				if err := s.refreshPluginGenerationIfChanged(); err != nil && !pluginGenerationRefreshPending(err) {
					providers.DebugLogf("refresh plugin generation before admission: %v", err)
				}
			})
			return false, nil
		}
		th.pluginExecutionLease = lease
		newPluginLease = true
	}
	current, err := s.rt.RetainState()
	if err != nil {
		if newPluginLease {
			th.releasePluginGenerationExecutionLeaseLocked()
		}
		return false, err
	}
	matches := th.state != nil && (current.Sessions == th.state.Sessions || s.activeExecutionRunID(th.ID) != "" || (th.execRuntime != nil && threadRuntimeHasOutstandingWork(th.ID, th.execRuntime)))
	_ = current.Close()
	if !matches {
		if newPluginLease {
			th.releasePluginGenerationExecutionLeaseLocked()
		}
		return false, errors.Join(errRetryableTurnAdmission, errors.New("selected state changed before execution admission"))
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
	lease, acquired, err := th.state.Sessions.TryAcquireThreadExecutionLeaseWithProjectLimit(s.rt.SessionDir, th.ID, maxParallel)
	if err != nil {
		th.admissionReserved = false
		if newPluginLease {
			th.releasePluginGenerationExecutionLeaseLocked()
		}
		if errors.Is(err, session.ErrProjectWorkerCapacity) {
			return false, errors.Join(errRetryableTurnAdmission, err)
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
	loaded, err := s.loadPersistedThreadSnapshot(th.state, th.ID)
	if err != nil {
		return fmt.Errorf("refresh durable state for thread %q: %w", th.ID, err)
	}
	if projectExecutionDisabled(loaded.metadata.Source) {
		return errProjectAgentDisabled
	}
	if loaded.repairNeeded {
		if err := s.rewriteChatHistoryUnderExecutionLease(th.state, s.rt.SessionDir, th.ID, loaded.repairedHistory, loaded.baselineSeq); err != nil {
			return fmt.Errorf("persist repaired history for thread %q: %w", th.ID, err)
		}
		// Re-read display rows after the rewrite so Turns and provider history
		// are rebuilt from the same committed snapshot.
		loaded, err = s.loadPersistedThreadSnapshot(th.state, th.ID)
		if err != nil {
			return fmt.Errorf("reload repaired state for thread %q: %w", th.ID, err)
		}
	}
	if loaded.workspaceRelocated {
		root, id, err := s.sessionWorkspace(loaded.metadata)
		if err != nil {
			return err
		}
		if !s.ownsSessionWorkspace(root, id) {
			return errors.New("session must execute in its relocated project runtime")
		}
		m := loaded.metadata
		if _, err := th.state.Sessions.UpdateWorkspaceBinding(th.ID, m.CWD, m.WorktreePath, m.WorktreeBaseHEAD, m.WorktreeBaseRepo); err != nil {
			return err
		}
	}
	// Durable state is authoritative once execution ownership is ours. A
	// different app-server may have completed turns, changed focus, compacted,
	// or edited the thread since this process loaded its cached snapshot.
	applySessionMetadata(th, loaded.metadata)
	th.WorkspaceKind = workspaceKindForCWD(s.rt.WuuHome, th.CWD)
	th.History = cloneHistory(loaded.history)
	th.historyHeadSeq = loaded.baselineSeq
	th.Turns = turnsFromPersistedHistory(th.ID, loaded.displayHistory, time.Now().UTC(), s.participantResolver(th.state))
	s.restorePluginToolLabels(th.Turns)
	th.Turns = applyTokenUsageMetasToTurns(th.Turns, loaded.tokenMetas)
	th.currentTurn = ""
	th.currentTurnKind = ""
	th.currentExecutionRunID = ""
	th.currentTurnResumed = false
	th.nextItemIndex = 0
	th.activeAgentItemID = ""
	th.agentStream = nil
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
func (s *Server) tryAcquireThreadMutationLease(binding *runtime.State, threadID string) (*session.DocumentThreadExecutionLease, error) {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return nil, errors.New("thread id is required")
	}
	if s == nil || s.rt == nil || strings.TrimSpace(s.rt.SessionDir) == "" {
		return nil, errors.New("session directory is required for durable thread mutation")
	}
	lease, acquired, err := binding.Sessions.TryAcquireThreadExecutionLease(s.rt.SessionDir, threadID)
	if err != nil {
		return nil, fmt.Errorf("acquire mutation lease for thread %q: %w", threadID, err)
	}
	if !acquired {
		return nil, threadExecutionBusyError(threadID)
	}
	return lease, nil
}

func releaseThreadMutationLease(threadID string, lease *session.DocumentThreadExecutionLease) {
	if lease == nil {
		return
	}
	if err := lease.Release(); err != nil {
		providers.DebugLogf("release mutation lease for thread %q: %v", threadID, err)
	}
}

func (s *Server) rewriteChatHistoryUnderExecutionLease(binding *runtime.State, sessDir, threadID string, history []providers.ChatMessage, baselineSeq int) error {
	if s != nil && s.rewriteChatHistoryForTest != nil {
		return s.rewriteChatHistoryForTest(sessDir, threadID, history)
	}
	return rewriteChatHistoryAtBaseline(binding.Sessions, threadID, history, baselineSeq)
}

func (s *Server) scheduleThreadExecutionLeaseRetry(binding *runtime.State, retry func(*runtime.State)) {
	if s == nil || retry == nil || s.closed.Load() {
		return
	}
	held := binding.Retain()
	if held == nil {
		return
	}
	time.AfterFunc(threadExecutionLeaseRetryDelay, func() {
		defer held.Close()
		if !s.closed.Load() {
			retry(held)
		}
	})
}

// Retries of new admissions own no old generation while waiting. Completion
// acknowledgements still use scheduleThreadExecutionLeaseRetry with their
// original capture because they finish already-admitted work.
func (s *Server) scheduleThreadAdmissionRetry(retry func(*runtime.State)) {
	if s == nil || retry == nil || s.closed.Load() {
		return
	}
	time.AfterFunc(threadExecutionLeaseRetryDelay, func() {
		if s.closed.Load() {
			return
		}
		binding, err := s.rt.RetainState()
		if err != nil {
			providers.DebugLogf("capture state for admission retry: %v", err)
			s.scheduleThreadAdmissionRetry(retry)
			return
		}
		defer binding.Close()
		retry(binding)
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
			// A finalizer may have released the lease before this poll. It must
			// still notify retirement; releasing the lease is not equivalent to
			// releasing the thread runtime's reference to its old generation.
			if th.pluginExecutionLease == nil || (!th.running && (th.execRuntime == nil || !threadRuntimeHasOutstandingWork(th.ID, th.execRuntime))) {
				th.releasePluginGenerationExecutionLeaseLocked()
				th.pluginLeaseReleaseLoop = false
				onQuiescent := th.onPluginLeaseQuiescent
				th.mu.Unlock()
				// This is the reliable drain boundary, including recovered
				// finalizations and terminal notifications dropped by fanout.
				if onQuiescent != nil {
					onQuiescent()
				}
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

func (s *Server) persistTurnTerminal(th *threadState, turnID string, kind TurnKind, status TurnStatus, cause *TurnError, at time.Time, reconnect *ThreadItem, providerName, model string, usage providers.TokenUsage) error {
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
	structuredCause := ""
	if cause != nil {
		message = cause.Message
		// Cause on turn_terminal meta rows holds the structured diagnostic.
		// DisplayContent stays plain text for older readers. Keeping both in
		// the same row makes status and recovery facts one durable write.
		encoded, err := json.Marshal(cause)
		if err != nil {
			return err
		}
		structuredCause = string(encoded)
	}
	var startedAt, answerReadyAt *time.Time
	th.mu.Lock()
	for _, turn := range th.Turns {
		if turn.ID != turnID {
			continue
		}
		if turn.StartedAt != nil {
			value := *turn.StartedAt
			startedAt = &value
		}
		if turn.AnswerReadyAt != nil {
			value := *turn.AnswerReadyAt
			answerReadyAt = &value
		}
		break
	}
	th.mu.Unlock()
	// Settlement is also the visible completion boundary when an engine did
	// not emit a final message. Keep the same boundary as finishTurnLocked.
	if answerReadyAt == nil && status == TurnStatusCompleted {
		answerReadyAt = &at
	}
	if err := th.state.Sessions.AppendHistoryRecord(th.ID, session.HistoryRecord{
		Role:                "meta",
		Content:             turnTerminalHistoryRecord,
		Provider:            providerName,
		Model:               model,
		InputTokens:         usage.InputTokens,
		OutputTokens:        usage.OutputTokens,
		CacheCreationTokens: usage.CacheCreationTokens,
		CacheReadTokens:     usage.CacheReadTokens,
		DisplayContent:      message,
		Cause:               structuredCause,
		ClientID:            clientID,
		StopReason:          string(status),
		At:                  at,
		StartedAt:           startedAt,
		AnswerReadyAt:       answerReadyAt,
	}); err != nil {
		return err
	}
	if reconnect == nil || status == TurnStatusCompleted {
		// Successful turns never persist a reconnect row: a recovery that
		// worked leaves no trace in history.
		return nil
	}
	return th.state.Sessions.AppendHistoryRecord(th.ID, session.HistoryRecord{
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
		diagnostic := BuildTurnError(cause, "")
		persistErr = s.persistTurnTerminal(th, started.turnID, TurnKindUser, TurnStatusFailed, &diagnostic, time.Now().UTC(), nil, "", "", providers.TokenUsage{})
	}
	abortStartedThreadTurn(th, started, cause)
	if persistErr != nil {
		return fmt.Errorf("persist aborted turn %q: %w", started.turnID, persistErr)
	}
	return nil
}
