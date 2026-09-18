package appserver

import (
	"context"
	"errors"
	"github.com/blueberrycongee/wuu/internal/channels"
	"github.com/blueberrycongee/wuu/internal/runtime"
	"strings"

	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/session"
)

var errSessionInputApplied = errors.New("session input is already durable")

// trySubmitSessionInput is shared by extension sends and Collaboration. The
// caller retains queued work in its own durable policy/outbox; acceptance and
// steering use the same host runtime, execution lease and control fence.
func (s *Server) trySubmitSessionInput(ctx context.Context, th *threadState, msg providers.ChatMessage, mode string, snapshot turnRuntimeSnapshot) (pluginhost.SessionSendResult, bool, error) {
	if active, err := session.ThreadExecutionActive(s.rt.SessionDir, th.ID); err != nil {
		return pluginhost.SessionSendResult{}, false, err
	} else if !active {
		th.mu.Lock()
		if !th.running {
			err = s.refreshDurableThreadHistoryLocked(th)
		}
		th.mu.Unlock()
		if err != nil {
			return pluginhost.SessionSendResult{}, false, err
		}
	}
	if prior, ok := s.findSessionInput(th, msg.ClientID); ok {
		return prior, true, nil
	}
	if snapshot.Control != nil {
		if err := session.ValidateControl(s.rt.SessionDir, *snapshot.Control); err != nil {
			return pluginhost.SessionSendResult{}, false, err
		}
	}
	if mode == pluginhost.SessionIfRunningSteer {
		if turn, ok := s.steerSessionInput(th, msg, snapshot); ok {
			return pluginhost.SessionSendResult{State: pluginhost.TurnLifecycleRunning, SessionID: th.ID, TurnID: turn, Steered: true}, true, nil
		}
	}
	started, ok, err := s.startSubmittedSessionTurn(ctx, th, msg, snapshot)
	if errors.Is(err, errSessionInputApplied) {
		prior, found := s.findSessionInput(th, msg.ClientID)
		return prior, found, nil
	}
	if err != nil {
		return pluginhost.SessionSendResult{}, false, err
	}
	if ok {
		return pluginhost.SessionSendResult{State: pluginhost.TurnLifecycleRunning, SessionID: th.ID, TurnID: started.turnID}, true, nil
	}
	return pluginhost.SessionSendResult{State: pluginhost.TurnLifecycleQueued, SessionID: th.ID}, false, nil
}

func (s *Server) steerSessionInput(th *threadState, msg providers.ChatMessage, snapshot turnRuntimeSnapshot) (string, bool) {
	if th == nil {
		return "", false
	}
	th.mu.Lock()
	defer th.mu.Unlock()
	if !th.running || th.currentTurn == "" || th.currentTurnKind == TurnKindCompact || th.interrupting {
		return "", false
	}
	for _, existing := range th.pendingSteers {
		if existing.ClientID == msg.ClientID {
			return th.currentTurn, true
		}
	}
	if snapshot.Control != nil {
		if err := session.ValidateControl(s.rt.SessionDir, *snapshot.Control); err != nil {
			return "", false
		}
		if th.pendingSteerControls == nil {
			th.pendingSteerControls = make(map[string]session.Control)
		}
		th.pendingSteerControls[msg.ClientID] = *snapshot.Control
	}
	msg.Steered = true
	th.pendingSteers = append(th.pendingSteers, msg)
	th.signalSteerWakeLocked()
	return th.currentTurn, true
}

func (s *Server) findSessionInput(th *threadState, clientID string) (pluginhost.SessionSendResult, bool) {
	if th == nil || strings.TrimSpace(clientID) == "" {
		return pluginhost.SessionSendResult{}, false
	}
	th.mu.Lock()
	for _, pending := range th.pendingSteers {
		if pending.ClientID == clientID {
			result := pluginhost.SessionSendResult{
				State: pluginhost.TurnLifecycleRunning, SessionID: th.ID, TurnID: th.currentTurn, Steered: true,
			}
			th.mu.Unlock()
			return result, true
		}
	}
	steered := false
	for _, message := range th.History {
		if message.ClientID == clientID && message.Steered {
			steered = true
			break
		}
	}
	for _, turn := range th.Turns {
		for _, item := range turn.Items {
			if item.Type != ThreadItemUserMessage || item.SourceID != clientID {
				continue
			}
			state := pluginhost.TurnLifecycleCompleted
			if turn.Status == TurnStatusInProgress {
				state = pluginhost.TurnLifecycleRunning
			}
			result := pluginhost.SessionSendResult{State: state, SessionID: th.ID, TurnID: turn.ID, Steered: steered}
			th.mu.Unlock()
			return result, true
		}
	}
	th.mu.Unlock()

	s.queuedTurnMu.Lock()
	defer s.queuedTurnMu.Unlock()
	for _, entry := range s.pendingQueuedTurns[th.ID] {
		if entry.msg.ClientID == clientID {
			return pluginhost.SessionSendResult{State: pluginhost.TurnLifecycleQueued, SessionID: th.ID, QueueID: entry.id}, true
		}
	}
	return pluginhost.SessionSendResult{}, false
}

func (s *Server) startSubmittedSessionTurn(ctx context.Context, th *threadState, msg providers.ChatMessage, snapshot turnRuntimeSnapshot) (startedThreadTurn, bool, error) {
	// A submitted turn is an independent execution boundary. In particular it
	// must not inherit the caller's inference workflow, journal or operation ID;
	// those are owned by the originating session, not by the new executor.
	ctx = context.Background()
	var threadRuntime *runtime.ThreadRuntime
	started, ok, err := s.startThreadUserTurnWithAdmission(
		ctx, th, msg, snapshot, false, turnReadOnlyFail,
		turnAdmissionHooks{afterLease: func(admitted *threadState, _ *providers.ChatMessage) error {
			if snapshot.Control != nil && s.channelService != nil {
				link, err := s.channelService.HarnessLink(ctx, admitted.ID)
				if err == nil && link.Active && link.AgentID == snapshot.Control.ManagerID {
					if err := s.channelService.ReserveHarnessExecution(ctx, link); err != nil {
						return err
					}
				} else if err != nil && !errors.Is(err, channels.ErrNotFound) {
					return err
				}
			}
			var runtimeErr error
			threadRuntime, runtimeErr = s.ensureThreadRuntimeAfterAdmission(admitted)
			if runtimeErr == nil {
				s.foldFrozenWorkerTree(admitted, threadRuntime)
			}
			return runtimeErr
		}},
	)
	if err != nil {
		if errors.Is(err, errThreadExecutionBusy) {
			return startedThreadTurn{}, false, nil
		}
		return startedThreadTurn{}, false, err
	}
	if !ok {
		return startedThreadTurn{}, false, nil
	}
	launch, accepted := s.reserveBackground(func() {
		s.runTurn(started.ctx, th, threadRuntime, started.turnID, started.runtime, started.history)
	})
	if !accepted {
		return startedThreadTurn{}, false, errors.Join(errServerClosed, s.abortStartedThreadTurnDurably(th, started, errServerClosed))
	}
	defer launch.Cancel()
	if err := s.writeNotification(NotificationTurnStarted, TurnStartedNotification{ThreadID: th.ID, Turn: started.turn}); err != nil {
		return startedThreadTurn{}, false, errors.Join(err, s.abortStartedThreadTurnDurably(th, started, err))
	}
	launch.Commit()
	return started, true, nil
}

// pruneRevokedSteersLocked checks at consumption as well as admission. This also
// covers a user taking control through a different host process.
func (s *Server) pruneRevokedSteersLocked(th *threadState) {
	next := th.pendingSteers[:0]
	for _, msg := range th.pendingSteers {
		if c, ok := th.pendingSteerControls[msg.ClientID]; ok {
			if err := session.ValidateControl(s.rt.SessionDir, c); err != nil {
				delete(th.pendingSteerControls, msg.ClientID)
				continue
			}
		}
		next = append(next, msg)
	}
	th.pendingSteers = next
}

func (s *Server) revokeSessionInputs(id string) {
	if th := s.thread(id); th != nil {
		th.mu.Lock()
		s.pruneRevokedSteersLocked(th)
		th.mu.Unlock()
	}
	// Queued inputs retain their control snapshot and are checked at admission.
	s.kickQueuedTurnDrain(id)
}
