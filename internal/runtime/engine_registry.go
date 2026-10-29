package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/agentengine"
	"github.com/blueberrycongee/wuu/internal/loopdriver"
	"github.com/blueberrycongee/wuu/internal/modelwire"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/pluginprotocol"
	"github.com/blueberrycongee/wuu/internal/providers"
)

func (s *State) CallEngine(ctx context.Context, id agentengine.EngineID, method string, input, output any) error {
	if s == nil || s.Host == nil {
		return errors.New("engine generation is unavailable")
	}
	return callSelectedEngine(ctx, s.Host, id, method, input, output)
}
func (s *Session) CallEngine(ctx context.Context, id agentengine.EngineID, method string, input, output any) error {
	state, err := s.RetainState()
	if err != nil {
		return err
	}
	defer state.Close()
	return state.CallEngine(ctx, id, method, input, output)
}
func callSelectedEngine(ctx context.Context, host *pluginhost.Host, id agentengine.EngineID, method string, input, output any) error {
	raw, err := json.Marshal(input)
	if err != nil {
		return err
	}
	result, callErr := host.CallService(ctx, engineExecutionConsumer, "engine."+string(id), method, raw)
	if callErr != nil {
		return callErr
	}
	if output != nil {
		return json.Unmarshal(result, output)
	}
	return nil
}
func newSelectedEngineRegistry(owner *Session) *agentengine.Registry {
	factory := func(id agentengine.EngineID) *selectedEngineFactory {
		return &selectedEngineFactory{owner: owner, id: id}
	}
	return agentengine.NewResolvedRegistry(func(id agentengine.EngineID) (agentengine.Factory, bool) {
		f := factory(id)
		var descriptor selectedEngineDescriptor
		if err := owner.CallEngine(context.Background(), id, "descriptor", struct{}{}, &descriptor); err != nil || !descriptor.Enabled {
			return nil, false
		}
		return f, true
	}, func() []agentengine.Descriptor {
		state, err := owner.RetainState()
		if err != nil {
			return nil
		}
		defer state.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		return state.EngineDescriptors(ctx)
	})
}

type selectedEngineDescriptor struct {
	ID                agentengine.EngineID `json:"id"`
	Version           string               `json:"version"`
	Capabilities      []string             `json:"capabilities"`
	RequiresHostModel *bool                `json:"requires_host_model,omitempty"`
	Enabled           bool                 `json:"enabled"`
}

func (d selectedEngineDescriptor) descriptor() agentengine.Descriptor {
	return agentengine.Descriptor{ID: d.ID, Version: d.Version, Capabilities: d.Capabilities, RequiresHostModel: d.RequiresHostModel}
}

type selectedEngineFactory struct {
	owner *Session
	id    agentengine.EngineID
}

func (f *selectedEngineFactory) Descriptor(ctx context.Context) (agentengine.Descriptor, error) {
	var result selectedEngineDescriptor
	err := f.owner.CallEngine(ctx, f.id, "descriptor", struct{}{}, &result)
	if err == nil && result.ID != f.id {
		err = errors.New("engine descriptor identity does not match its selected service")
	}
	return result.descriptor(), err
}
func (f *selectedEngineFactory) Open(ctx context.Context, in agentengine.OpenRequest) (agentengine.Session, error) {
	return f.Resume(ctx, agentengine.ResumeRequest{OpenRequest: in})
}
func (f *selectedEngineFactory) Resume(ctx context.Context, in agentengine.ResumeRequest) (agentengine.Session, error) {
	thread := in.ThreadID
	if thread == "" {
		thread = in.SessionID
	}
	root := in.RootDir
	if root == "" {
		root = f.owner.RootDir
	}
	return f.SessionForThread(ctx, agentengine.ThreadBinding{ThreadID: thread, RootDir: root, ExternalRef: in.ExternalSessionRef})
}
func (f *selectedEngineFactory) SessionForThread(ctx context.Context, binding agentengine.ThreadBinding) (agentengine.Session, error) {
	state, err := f.owner.RetainState()
	if err != nil {
		return nil, err
	}
	defer state.Close()
	var descriptor selectedEngineDescriptor
	if err := state.CallEngine(ctx, f.id, "descriptor", struct{}{}, &descriptor); err != nil {
		return nil, err
	}
	if descriptor.ID != f.id || !descriptor.Enabled {
		return nil, errors.New("selected engine is unavailable")
	}
	if f.owner.UserBinding().IsUser() && !slices.Contains(descriptor.Capabilities, userHostContextCapability) {
		return nil, errors.New("engine does not enforce explicit user-host context")
	}
	selection := ThreadModelSelection{Model: binding.Model, Effort: binding.Effort, Speed: binding.Speed, PermissionMode: binding.PermissionMode}
	var rt *ThreadRuntime
	if slices.Contains(descriptor.Capabilities, "host-managed-continuation") {
		rt, err = f.owner.NewThreadRuntimeForState(state, binding.ThreadID, binding.RootDir, selection)
	} else {
		rt, err = f.owner.NewThreadCapabilityRuntimeForState(state, binding.ThreadID, binding.RootDir, selection)
	}
	if err != nil {
		return nil, err
	}
	rt.EngineID = f.id
	session, err := f.owner.bindSelectedEngine(ctx, rt, binding, f.id)
	if err != nil {
		_ = closeEngineRuntime(f.owner, rt)
		return nil, err
	}
	session.ownedRuntime = rt
	return session, nil
}

func (s *Session) bindSelectedEngine(ctx context.Context, rt *ThreadRuntime, binding agentengine.ThreadBinding, id agentengine.EngineID) (*selectedEngineSession, error) {
	generation := rt.PluginGeneration
	if generation == nil || generation.engineGateways == nil {
		return nil, errors.New("engine callback generation is unavailable")
	}
	if s.UserBinding().IsUser() {
		var descriptor selectedEngineDescriptor
		if err := callSelectedEngine(ctx, generation.host, id, "descriptor", struct{}{}, &descriptor); err != nil {
			return nil, err
		}
		if descriptor.ID != id || !descriptor.Enabled || !slices.Contains(descriptor.Capabilities, userHostContextCapability) {
			return nil, errors.New("engine does not enforce explicit user-host context")
		}
	}
	owner, ok := generation.host.ServiceRegistry().ProviderPluginIDFor(engineExecutionConsumer, "engine."+string(id), 1)
	if !ok {
		return nil, fmt.Errorf("selected engine %s is unavailable", id)
	}
	generation.retain()
	gateway := &engineSessionGateway{owner: owner, binding: binding, runtime: rt}
	token, remove := generation.engineGateways.register(gateway)
	input := pluginprotocol.EngineBinding{ThreadID: binding.ThreadID, RootDir: binding.RootDir, Model: binding.Model, Effort: binding.Effort, Speed: binding.Speed, ModelOptions: binding.ModelOptions, PermissionMode: binding.PermissionMode, Instructions: binding.Instructions, ExternalRef: binding.ExternalRef, GatewayID: token}
	for _, server := range binding.MCPServers {
		input.MCPServers = append(input.MCPServers, pluginprotocol.EngineMCPServer{Name: server.Name, URL: server.URL})
	}
	var handle pluginprotocol.EngineSessionParams
	if err := callSelectedEngine(ctx, generation.host, id, "bind", input, &handle); err != nil {
		remove()
		s.ReleasePluginGeneration(generation)
		return nil, err
	}
	if handle.SessionID == "" {
		remove()
		s.ReleasePluginGeneration(generation)
		return nil, errors.New("engine provider returned an empty session handle")
	}
	return &selectedEngineSession{owner: s, id: id, generation: generation, gateway: gateway, remove: remove, handle: handle}, nil
}

type selectedEngineSession struct {
	owner        *Session
	id           agentengine.EngineID
	generation   *PluginGeneration
	gateway      *engineSessionGateway
	remove       func()
	handle       pluginprotocol.EngineSessionParams
	ownedRuntime *ThreadRuntime
	mu           sync.Mutex
	cancel       context.CancelFunc
	done         chan struct{}
	closing      bool
	closed       chan struct{}
	closeErr     error
}

func (s *selectedEngineSession) RunTurn(ctx context.Context, input agentengine.TurnInput, sink agentengine.EventSink) (agentengine.TurnResult, error) {
	s.mu.Lock()
	if s.closing || s.done != nil {
		s.mu.Unlock()
		return agentengine.TurnResult{}, errors.New("engine session is closed or already running")
	}
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	s.cancel, s.done = cancel, done
	s.mu.Unlock()
	s.gateway.mu.Lock()
	s.gateway.sink, s.gateway.runCtx = sink, ctx
	s.gateway.mu.Unlock()
	defer func() {
		cancel()
		s.gateway.mu.Lock()
		s.gateway.sink, s.gateway.runCtx = nil, nil
		s.gateway.mu.Unlock()
		s.mu.Lock()
		s.cancel, s.done = nil, nil
		close(done)
		s.mu.Unlock()
	}()
	var output pluginprotocol.EngineRunResult
	err := callSelectedEngine(ctx, s.generation.host, s.id, "run", pluginprotocol.EngineRunParams{EngineSessionParams: s.handle, Messages: modelwire.DriverMessagesToWire(input.History)}, &output)
	var result agent.LoopResult
	if err == nil || output.Result.Version != 0 {
		var decodeErr error
		result, decodeErr = engineResultFromWire(output.Result)
		err = errors.Join(err, decodeErr)
	}
	if output.Failure != nil {
		err = errors.Join(err, &loopdriver.FailureError{Failure: *output.Failure})
	}
	return agentengine.TurnResult{Result: result}, err
}
func (s *selectedEngineSession) Interrupt(ctx context.Context, reason string) error {
	// Ask the selected provider before local cancellation so native interruption
	// acknowledgments can be persisted. A failed acknowledgment is not retried.
	err := callSelectedEngine(ctx, s.generation.host, s.id, "interrupt", pluginprotocol.EngineInterruptParams{EngineSessionParams: s.handle, Reason: reason}, nil)
	s.mu.Lock()
	cancel := s.cancel
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	return err
}
func (s *selectedEngineSession) Steer(ctx context.Context, message providers.ChatMessage) (string, error) {
	messages := modelwire.DriverMessagesToWire([]providers.ChatMessage{message})
	var result pluginprotocol.EngineSteerResult
	if err := callSelectedEngine(ctx, s.generation.host, s.id, "steer", pluginprotocol.EngineSteerParams{EngineSessionParams: s.handle, Message: messages[0]}, &result); err != nil {
		// A failed RPC may have submitted input. Never infer non-delivery from
		// diagnostic text or turn it into an automatic next-turn replay.
		return "", err
	}
	switch result.Disposition {
	case "", "accepted":
		return result.TurnID, nil
	case "unsupported", "rejected":
		if result.TurnID != "" {
			return "", errors.New("conflicting engine steering receipt; delivery is unconfirmed")
		}
		if result.Disposition == "unsupported" {
			return "", agentengine.ErrSteerUnsupported
		}
		return "", agentengine.ErrSteerRejected
	default:
		return "", fmt.Errorf("unknown engine steering disposition %q; delivery is unconfirmed", result.Disposition)
	}
}
func (s *selectedEngineSession) Close(ctx context.Context) error {
	s.mu.Lock()
	if s.closed == nil {
		s.closing = true
		s.closed = make(chan struct{})
		cancel, runDone := s.cancel, s.done
		go func() {
			if cancel != nil {
				cancel()
			}
			if runDone != nil {
				<-runDone
			}
			err := callSelectedEngine(context.Background(), s.generation.host, s.id, "close", s.handle, nil)
			s.remove()
			s.owner.ReleasePluginGeneration(s.generation)
			if s.ownedRuntime != nil {
				err = errors.Join(err, closeEngineRuntime(s.owner, s.ownedRuntime))
			}
			s.mu.Lock()
			s.closeErr = err
			close(s.closed)
			s.mu.Unlock()
		}()
	}
	done := s.closed
	s.mu.Unlock()
	select {
	case <-done:
		return s.closeErr
	case <-ctx.Done():
		return ctx.Err()
	}
}

func closeEngineRuntime(owner *Session, rt *ThreadRuntime) error {
	var err error
	if control := rt.NativeAgentControl(); control != nil {
		control.BeginShutdown()
		control.StopAll()
		control.YieldWorkerTerminalFinalizations()
		_ = control.WaitOwnedWorkerExecutions(context.Background())
		err = shutdownAndCleanupAgentControl(control)
	}
	err = errors.Join(err, rt.CloseNativeToolScope(context.Background()))
	owner.ReleasePluginGeneration(rt.PluginGeneration)
	rt.PluginGeneration = nil
	return errors.Join(err, rt.CloseArtifacts(), rt.CloseComposition())
}

// EngineDescriptors lists the selected engine services in this captured generation.
func (state *State) EngineDescriptors(ctx context.Context) []agentengine.Descriptor {
	if state == nil || state.Host == nil || state.Host.ServiceRegistry() == nil {
		return nil
	}
	seen := map[string]bool{}
	var result []agentengine.Descriptor
	for _, service := range state.Host.ServiceRegistry().Snapshot(0).Services {
		if !strings.HasPrefix(service.Service, "engine.") || seen[service.Service] {
			continue
		}
		seen[service.Service] = true
		id := agentengine.EngineID(strings.TrimPrefix(service.Service, "engine."))
		var descriptor selectedEngineDescriptor
		if err := state.CallEngine(ctx, id, "descriptor", struct{}{}, &descriptor); err == nil && descriptor.ID == id {
			result = append(result, descriptor.descriptor())
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result
}

// RetainEngine owns a warm provider scope across short-lived turns. Releasing
// it uses the same captured provider, even after settings or composition updates.
func (s *Session) RetainEngine(ctx context.Context, id agentengine.EngineID) (func() error, error) {
	state, err := s.RetainState()
	if err != nil {
		return nil, err
	}
	defer state.Close()
	return state.RetainEngine(ctx, id)
}
func (s *State) RetainEngine(ctx context.Context, id agentengine.EngineID) (func() error, error) {
	if s == nil || s.Generation == nil {
		return nil, errors.New("engine generation is unavailable")
	}
	state := s.Retain()
	if state == nil {
		return nil, errors.New("engine generation is closed")
	}
	var lease pluginprotocol.EngineSessionParams
	if err := state.CallEngine(ctx, id, "retain", struct{}{}, &lease); err != nil {
		_ = state.Close()
		return nil, err
	}
	var once sync.Once
	var closeErr error
	return func() error {
		once.Do(func() {
			closeErr = state.CallEngine(context.Background(), id, "release", lease, nil)
			closeErr = errors.Join(closeErr, state.Close())
		})
		return closeErr
	}, nil
}
