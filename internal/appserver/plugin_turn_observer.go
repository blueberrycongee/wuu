package appserver

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/runtime"
	"github.com/blueberrycongee/wuu/internal/session"
)

const pluginObserverDeliveryTimeout = 5 * time.Second

func (s *Server) notifyPluginTurnLifecycle(binding *runtime.State, ctx context.Context, pluginID string, input pluginhost.AgentTurnLifecycleInput) error {
	if s == nil || s.rt == nil {
		return nil
	}
	terminal, err := s.persistPluginTurnLifecycle(binding, pluginID, input)
	if err != nil {
		return err
	}
	origin, err := s.retainPluginTurnOrigin(binding, pluginID, input.RequestID)
	if err != nil {
		return err
	}
	defer origin.Close()
	return s.deliverPluginTurnLifecycle(origin, ctx, pluginID, input, terminal)
}

// notifyPluginTurnLifecycleAsync persists terminal observations before
// scheduling their best-effort delivery. Plugin helpers use a single ordered
// request worker, so a synchronous callback from a host service (for example,
// close_agent cancelling a queued child) can deadlock the host call that is
// waiting for the callback's response. Durable outbox state lets the next
// replay retry delivery when the helper becomes available again.
func (s *Server) notifyPluginTurnLifecycleAsync(binding *runtime.State, pluginID string, input pluginhost.AgentTurnLifecycleInput) {
	if s == nil || s.rt == nil {
		return
	}
	terminal, err := s.persistPluginTurnLifecycle(binding, pluginID, input)
	if err != nil {
		providers.DebugLogf("persist plugin turn lifecycle for %q: %v", input.RequestID, err)
		return
	}
	origin, err := s.retainPluginTurnOrigin(binding, pluginID, input.RequestID)
	if err != nil {
		providers.DebugLogf("retain plugin turn lifecycle origin for %q: %v", input.RequestID, err)
		return
	}
	defer origin.Close()
	if !s.startBackgroundWithState(origin, func(binding *runtime.State) {
		ctx, cancel := context.WithTimeout(context.Background(), pluginObserverDeliveryTimeout)
		defer cancel()
		if err := s.deliverPluginTurnLifecycle(binding, ctx, pluginID, input, terminal); err != nil {
			providers.DebugLogf("deliver plugin turn lifecycle for %q: %v", input.RequestID, err)
		}
	}) {
		// Keep a terminal record in the outbox if shutdown races delivery.
		return
	}
}

// notifyPluginTurnInterruptedAsync exposes a turn-scoped cancellation signal
// without making plugin observers part of the host's interruption critical
// path. The event is intentionally broadcast: the host does not know which
// plugin-defined work is related to the turn, and each trusted plugin owns its
// own propagation policy.
func (s *Server) notifyPluginTurnInterruptedAsync(binding *runtime.State, input pluginhost.AgentTurnInterruptedInput) {
	if s == nil || s.rt == nil {
		return
	}
	host := binding.Host
	if host == nil {
		return
	}
	capabilities := host.Capabilities(pluginhost.CapabilityAgentTurnInterrupted)
	if len(capabilities) == 0 {
		return
	}
	s.startBackgroundWithState(binding, func(binding *runtime.State) {
		var pending sync.WaitGroup
		for _, capability := range capabilities {
			pending.Add(1)
			go func() {
				defer pending.Done()
				ctx, cancel := context.WithTimeout(context.Background(), pluginObserverDeliveryTimeout)
				defer cancel()
				var output pluginhost.AgentTurnInterruptedOutput
				if err := host.InvokeCapability(ctx, capability, input, &output); err != nil {
					if policyErr := host.HandleCapabilityError(capability, err); policyErr != nil {
						providers.DebugLogf("deliver turn interruption to plugin %q: %v", capability.PluginID, policyErr)
					}
				}
			}()
		}
		pending.Wait()
	})
}

func (s *Server) persistPluginTurnLifecycle(binding *runtime.State, pluginID string, input pluginhost.AgentTurnLifecycleInput) (bool, error) {
	if s.clientInputReceiptsEnabled() {
		if err := binding.Sessions.SettleClientInputExecution(s.clientInputScope(input.ThreadID, pluginID), input.RequestID, input.TurnID, input.State, input.Error); err != nil {
			return false, err
		}
	}
	terminal := pluginTurnLifecycleTerminal(input.State)
	if !terminal {
		return false, nil
	}
	payload, err := json.Marshal(input)
	if err != nil {
		return true, fmt.Errorf("encode plugin turn lifecycle: %w", err)
	}
	if err := binding.Sessions.PutPluginTurnLifecycleOutbox(pluginID, input.RequestID, payload); err != nil {
		return true, err
	}
	// Wake inspect callers only after the terminal record is durable. They can
	// then re-read the retained lifecycle state without depending on plugin
	// callback delivery or its outbox acknowledgement.
	s.pluginTurnWaiters.notify(pluginID, input.RequestID)
	return true, nil
}

func (s *Server) deliverPluginTurnLifecycle(binding *runtime.State, ctx context.Context, pluginID string, input pluginhost.AgentTurnLifecycleInput, terminal bool) error {
	host := binding.Host
	capability, ok := host.Capability(pluginID, pluginhost.CapabilityAgentTurnLifecycle)
	if !ok {
		// The owning plugin is no longer active (disabled, removed, or failed),
		// so the event can never be acknowledged. Drop terminal events instead
		// of leaving them to replay forever; non-terminal events have no outbox
		// row. This keeps plugin effects reversible when a plugin is disabled.
		if terminal {
			if err := binding.Sessions.DeletePluginTurnLifecycleOutbox(pluginID, input.RequestID); err != nil {
				providers.DebugLogf("drop plugin lifecycle outbox event for inactive plugin %q/%q: %v", pluginID, input.RequestID, err)
			}
		}
		return nil
	}
	var output pluginhost.AgentTurnLifecycleOutput
	if err := host.InvokeCapability(ctx, capability, input, &output); err != nil {
		if terminal {
			s.schedulePluginTurnLifecycleReplay(binding)
		}
		return host.HandleCapabilityError(capability, err)
	}
	if terminal {
		if err := binding.Sessions.DeletePluginTurnLifecycleOutbox(pluginID, input.RequestID); err != nil {
			s.schedulePluginTurnLifecycleReplay(binding)
			return err
		}
	}
	return nil
}

const pluginTurnLifecycleReplayDelay = time.Second

// Replay admission is scoped to the captured authority. A retry from an old
// generation must neither suppress nor read a replacement generation's outbox.
type pluginLifecycleReplayState struct {
	mu      sync.Mutex
	pending map[*session.Store]bool
}

func (s *Server) schedulePluginTurnLifecycleReplay(binding *runtime.State) {
	if s == nil {
		return
	}
	s.pluginLifecycleReplay.mu.Lock()
	if s.pluginLifecycleReplay.pending == nil {
		s.pluginLifecycleReplay.pending = make(map[*session.Store]bool)
	}
	if s.pluginLifecycleReplay.pending[binding.Sessions] {
		s.pluginLifecycleReplay.mu.Unlock()
		return
	}
	s.pluginLifecycleReplay.pending[binding.Sessions] = true
	s.pluginLifecycleReplay.mu.Unlock()
	clear := func() {
		s.pluginLifecycleReplay.mu.Lock()
		delete(s.pluginLifecycleReplay.pending, binding.Sessions)
		s.pluginLifecycleReplay.mu.Unlock()
	}
	if !s.startBackgroundWithState(binding, func(held *runtime.State) {
		timer := time.NewTimer(pluginTurnLifecycleReplayDelay)
		defer timer.Stop()
		<-timer.C
		clear()
		if !s.closed.Load() {
			s.replayPendingPluginTurnLifecycles(held)
		}
	}) {
		clear()
	}
}

func pluginTurnLifecycleTerminal(state string) bool {
	switch state {
	case pluginhost.TurnLifecycleCompleted, pluginhost.TurnLifecycleFailed,
		pluginhost.TurnLifecycleInterrupted, pluginhost.TurnLifecycleDiscarded:
		return true
	default:
		return false
	}
}

func (s *Server) replayPendingPluginTurnLifecycles(binding *runtime.State) {
	if s == nil || s.rt == nil {
		return
	}
	entries, err := binding.Sessions.ListPluginTurnLifecycleOutbox()
	if err != nil {
		providers.DebugLogf("list pending plugin turn lifecycle events: %v", err)
		return
	}
	for _, entry := range entries {
		var input pluginhost.AgentTurnLifecycleInput
		if err := json.Unmarshal(entry.Payload, &input); err != nil {
			providers.DebugLogf("decode pending plugin turn lifecycle event %q/%q: %v", entry.PluginID, entry.RequestID, err)
			continue
		}
		if err := s.notifyPluginTurnLifecycle(binding, context.Background(), entry.PluginID, input); err != nil {
			providers.DebugLogf("replay pending plugin turn lifecycle event %q/%q: %v", entry.PluginID, entry.RequestID, err)
		}
	}
}

// notifyPluginTurnCompleted delivers a settled-turn observation to every
// active registration. Observers cannot change host scheduling or turn state.
func (s *Server) notifyPluginTurnCompleted(binding *runtime.State, ctx context.Context, input pluginhost.AgentTurnCompletedInput) error {
	if s == nil || s.rt == nil {
		return nil
	}
	host := binding.Host
	for _, capability := range host.Capabilities(pluginhost.CapabilityAgentTurnCompleted) {
		var output pluginhost.AgentTurnCompletedOutput
		if err := host.InvokeCapability(ctx, capability, input, &output); err != nil {
			if policyErr := host.HandleCapabilityError(capability, err); policyErr != nil {
				return policyErr
			}
			continue
		}
	}
	return nil
}
