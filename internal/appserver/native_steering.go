package appserver

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"time"

	"github.com/blueberrycongee/wuu/internal/agentengine"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/session"
)

// Native steering drains only explicit human input. Native engines retain their
// own loop, approvals, tools and terminal events; generated completions keep the
// existing next-turn queue path. The returned join prevents terminal history
// from overtaking an in-flight native acknowledgement.
func (s *Server) startNativeSteering(ctx context.Context, th *threadState, turnID string, engine agentengine.SteeringSession) func() {
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-stop:
				return
			case <-ctx.Done():
				return
			default:
			}
			th.mu.Lock()
			if th.currentTurn != turnID || th.interrupting {
				th.mu.Unlock()
				return
			}
			var message providers.ChatMessage
			for _, pending := range th.pendingSteers {
				if isHumanUserMessage(pending) {
					message = providers.CloneChatMessage(pending)
					break
				}
			}
			th.resetSteerWakeLocked()
			wake := th.steerWake
			if message.ClientID == "" {
				th.mu.Unlock()
				select {
				case <-stop:
					return
				case <-ctx.Done():
					return
				case <-wake:
					continue
				}
			}
			entry := queuedTurn{id: message.ClientID, msg: message, origin: session.HeldUserWorkOriginSteer}
			entry.snapshot.ActiveDocument = th.steerDocumentOverrideLocked(entry.id)
			var dispatchErr error
			var expectedMessage []byte
			if th.PersistHistory {
				var found bool
				entry, found, dispatchErr = s.findHeldUserTurn(th.ID, message.ClientID)
				if dispatchErr == nil && (!found || !reflect.DeepEqual(entry.msg, message)) {
					dispatchErr = errors.New("accepted steering input changed before native dispatch; review the held message")
				}
				if dispatchErr == nil {
					var row session.HeldUserWork
					row, dispatchErr = heldUserWorkRow(entry)
					if dispatchErr == nil {
						dispatchErr = session.ClaimHeldUserWork(s.rt.SessionDir, th.ID, row)
						expectedMessage = row.MessageJSON
					}
				}
			}
			th.nativeSteerInFlight = message.ClientID
			th.mu.Unlock()
			unsupported := false
			if dispatchErr == nil {
				// Bound transport loss without holding the app-server dispatch loop.
				steerCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
				var nativeTurnID string
				nativeTurnID, dispatchErr = engine.Steer(steerCtx, message)
				if dispatchErr == nil {
					message.EngineTurnID = nativeTurnID
				}
				cancel()
				unsupported = errors.Is(dispatchErr, agentengine.ErrSteerUnsupported)
				if th.PersistHistory && (unsupported || errors.Is(dispatchErr, agentengine.ErrSteerRejected)) {
					releaseErr := session.ReleaseHeldUserWork(s.rt.SessionDir, th.ID, message.ClientID)
					if releaseErr != nil {
						unsupported = false
					}
					dispatchErr = errors.Join(dispatchErr, releaseErr)
				}
			}
			th.mu.Lock()
			th.nativeSteerInFlight = ""
			if unsupported {
				// Older native protocols keep the established next-turn fallback.
				th.nativeSteerUnsupported = true
				th.mu.Unlock()
				return
			}
			if dispatchErr == nil && th.PersistHistory {
				record := historyRecordFromPersistedMessage(persistedMessageFromChatMessage(message))
				record.ExpectedHeldMessage = expectedMessage
				message.Seq, dispatchErr = session.AppendHistoryRecordReturningSeq(s.rt.SessionDir, th.ID, record)
				if dispatchErr != nil {
					dispatchErr = fmt.Errorf("native input was accepted but could not be saved; delivery must not be retried: %w", dispatchErr)
				}
			}
			th.removePendingSteerLocked(message.ClientID)
			var batch []outboundNotification
			if dispatchErr == nil {
				th.History = append(th.History, message)
				batch = append(batch, th.materializeSteerLocked(turnID, message, time.Now().UTC())...)
			} else {
				th.removeSteerDocumentOverrideLocked(message.ClientID)
			}
			th.mu.Unlock()
			s.notifyOutboundBatch(batch)
			if dispatchErr != nil {
				if held, err := s.appendHeldUserTurns(th.ID, []queuedTurn{entry}); err == nil {
					s.notifyHeldUserTurnsWithError(th.ID, held, dispatchErr.Error())
				} else {
					providers.DebugLogf("retain native steering input %q: %v", message.ClientID, err)
				}
			}
		}
	}()
	return func() { close(stop); <-done }
}

func (s *Server) steerDeliveryModeLocked(th *threadState) string {
	if agentengine.NormalizeEngineID(th.EngineID) == agentengine.EngineWuu {
		return "active_turn"
	}
	if !th.PersistHistory || th.nativeSteerUnsupported || s.rt.Engines() == nil {
		return "queued"
	}
	if factory, ok := s.rt.Engines().Lookup(agentengine.NormalizeEngineID(th.EngineID)); ok {
		if descriptor, err := factory.Descriptor(context.Background()); err == nil && slices.Contains(descriptor.Capabilities, "active-turn-steering") {
			return "active_turn"
		}
	}
	return "queued"
}
