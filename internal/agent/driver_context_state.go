package agent

import (
	"context"

	"github.com/blueberrycongee/wuu/internal/loopdriver"
	"github.com/blueberrycongee/wuu/internal/modelwire"
	"github.com/blueberrycongee/wuu/internal/pluginprotocol"
	"github.com/blueberrycongee/wuu/internal/providers"
)

func (g *streamTurnGateway) ReadContextState(ctx context.Context) (pluginprotocol.DriverContextState, error) {
	ctx, leave, err := g.enter(ctx)
	if err != nil {
		return pluginprotocol.DriverContextState{}, err
	}
	defer leave()
	if err := g.prepare(ctx); err != nil {
		return pluginprotocol.DriverContextState{}, err
	}
	r := g.runner
	r.retainedContextMu.Lock()
	retained := retainedContextToWire(r.retainedRequestContext)
	r.retainedContextMu.Unlock()
	if retained == nil && r.RequestContextStore != nil {
		checkpoint, found, err := r.RequestContextStore.Load(ctx)
		if err != nil {
			return pluginprotocol.DriverContextState{}, err
		}
		if found && checkpoint.Provider == r.ProviderName && checkpoint.Model == r.requestModel() && checkpoint.DriverID == g.descriptor.ID && checkpoint.DriverVersion == g.descriptor.Version {
			retained = retainedContextToWire(checkpoint.State)
		}
	}
	usage, _ := r.prepareUsageTrackerForContract(g.activeHistory, r.currentUsageContract())
	return pluginprotocol.DriverContextState{Retained: retained, Usage: usageToWire(usage)}, nil
}

// SaveContextState commits request insertions before dispatch. Identity is taken
// from the execution, and the transcript remains owned by context operations.
func (g *streamTurnGateway) SaveContextState(ctx context.Context, state pluginprotocol.DriverContextState) error {
	ctx, leave, err := g.enter(ctx)
	if err != nil {
		return err
	}
	defer leave()
	if err := g.prepare(ctx); err != nil {
		return err
	}
	r := g.runner
	retained := retainedContextFromWire(state.Retained)
	if r.RequestContextStore != nil {
		checkpoint := RequestContextCheckpoint{Provider: r.ProviderName, Model: r.requestModel(), DriverID: g.descriptor.ID, DriverVersion: g.descriptor.Version, State: retained}
		if err := r.RequestContextStore.Save(ctx, checkpoint); err != nil {
			return err
		}
	}
	r.storeRetainedRequestContext(retained)
	if state.Usage != nil {
		usage := usageFromWire(state.Usage)
		usage.setMediaInputPolicy(providers.ResolveMediaInput(g.client, r.MediaInput, r.ProviderOptions))
		r.commitUsageTrackerForContract(usage, g.activeHistory, r.currentUsageContract())
		g.contextTokens = usage.EstimateCurrent()
	}
	return nil
}

func retainedContextToWire(state *RetainedRequestContextState) *pluginprotocol.DriverRetainedContext {
	if state == nil {
		return nil
	}
	result := &pluginprotocol.DriverRetainedContext{DurableLen: state.DurableLen, DurableHash: state.DurableHash}
	for _, item := range state.Messages {
		result.Messages = append(result.Messages, pluginprotocol.DriverRetainedMessage{AfterDurable: item.AfterDurable, Message: modelwire.DriverMessagesToWire([]providers.ChatMessage{item.Message})[0]})
	}
	return result
}

func retainedContextFromWire(state *pluginprotocol.DriverRetainedContext) *RetainedRequestContextState {
	if state == nil {
		return nil
	}
	result := &RetainedRequestContextState{DurableLen: state.DurableLen, DurableHash: state.DurableHash}
	for _, item := range state.Messages {
		result.Messages = append(result.Messages, RetainedContextMessage{AfterDurable: item.AfterDurable, Message: modelwire.DriverMessagesFromWire([]pluginprotocol.DriverMessageV1{item.Message})[0]})
	}
	return result
}

func usageToWire(usage *UsageTracker) *pluginprotocol.DriverContextUsage {
	usage.mu.Lock()
	defer usage.mu.Unlock()
	return &pluginprotocol.DriverContextUsage{LastResponseTotal: usage.lastResponseTotal, LastSuccessfulRequestTokens: usage.lastSuccessfulRequestTokens, LastResponseContract: usage.lastResponseContract, PendingDelta: usage.pendingDelta, Adjustment: string(usage.adjustment)}
}

func usageFromWire(usage *pluginprotocol.DriverContextUsage) *UsageTracker {
	if usage == nil {
		return NewUsageTracker()
	}
	return &UsageTracker{lastResponseTotal: usage.LastResponseTotal, lastSuccessfulRequestTokens: usage.LastSuccessfulRequestTokens, lastResponseContract: usage.LastResponseContract, pendingDelta: usage.PendingDelta, adjustment: UsageAdjustment(usage.Adjustment)}
}

type driverResourceContextStore struct {
	turn     loopdriver.TurnGateway
	identity RequestContextCheckpoint
}

func (s driverResourceContextStore) Load(ctx context.Context) (RequestContextCheckpoint, bool, error) {
	state, err := s.turn.ReadContextState(ctx)
	checkpoint := s.identity
	checkpoint.State = retainedContextFromWire(state.Retained)
	return checkpoint, checkpoint.State != nil, err
}

func (s driverResourceContextStore) Save(ctx context.Context, checkpoint RequestContextCheckpoint) error {
	return s.turn.SaveContextState(ctx, pluginprotocol.DriverContextState{Retained: retainedContextToWire(checkpoint.State)})
}
