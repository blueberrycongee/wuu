package loopdriver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/blueberrycongee/wuu/internal/pluginhost"
)

// Service exposes a native driver through the same versioned protocol as a
// process driver. Instances and callbacks belong to this module generation.
type Service struct {
	id      string
	name    string
	driver  Driver
	router  pluginhost.ServiceRouter
	mu      sync.Mutex
	state   pluginhost.State
	next    uint64
	entries map[string]*serviceInstance
}

type serviceInstance struct {
	instance  Instance
	caller    string
	execution ExecutionContext
	done      chan struct{}
}

func NewService(id, name string, driver Driver, router pluginhost.ServiceRouter) *Service {
	return &Service{id: id, name: name, driver: driver, router: router, state: pluginhost.StatePrepared, entries: make(map[string]*serviceInstance)}
}

func (s *Service) ID() string { return s.id }
func (s *Service) Status() pluginhost.Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	return pluginhost.Status{ID: s.id, State: s.state}
}
func (s *Service) ProvidedServices() []pluginhost.ServiceDescriptor {
	return []pluginhost.ServiceDescriptor{{Name: s.name, Version: "1.0.0", Methods: []pluginhost.ServiceMethodDescriptor{
		{Name: RemoteMethodDescriptor, Preparation: true},
		{Name: RemoteMethodCreate}, {Name: RemoteMethodResume}, {Name: RemoteMethodRun},
		{Name: RemoteMethodShutdown, Cleanup: true},
	}}}
}
func (s *Service) RequiredServices() []pluginhost.ServiceRequirement {
	return []pluginhost.ServiceRequirement{
		{Name: DriverModelLoopService, MajorVersion: 1, Required: true},
		{Name: DriverCheckpointService, MajorVersion: 1, Required: true},
	}
}
func (s *Service) Activate(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state != pluginhost.StatePrepared {
		return fmt.Errorf("driver service cannot activate from %s", s.state)
	}
	s.state = pluginhost.StateActive
	return nil
}
func (s *Service) Close(context.Context) error {
	s.mu.Lock()
	s.state = pluginhost.StateStopped
	entries := s.entries
	s.entries = make(map[string]*serviceInstance)
	s.mu.Unlock()
	for _, entry := range entries {
		entry.instance.Cancel("driver service stopped")
	}
	for _, entry := range entries {
		entry.stop()
	}
	return nil
}
func (entry *serviceInstance) stop() {
	entry.instance.Shutdown()
	if entry.done != nil {
		<-entry.done
	}
}

func (s *Service) InvokeService(ctx context.Context, params pluginhost.ServiceInvokeParams) (json.RawMessage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	state := s.state
	s.mu.Unlock()
	if state != pluginhost.StateActive && !(state == pluginhost.StatePrepared && params.Method == RemoteMethodDescriptor) {
		return nil, errors.New("driver service is unavailable")
	}
	switch params.Method {
	case RemoteMethodDescriptor:
		descriptor, err := s.driver.Descriptor(ctx)
		if err != nil {
			return nil, err
		}
		return json.Marshal(descriptor)
	case RemoteMethodCreate, RemoteMethodResume:
		var request remoteResumeParams
		if err := json.Unmarshal(params.Params, &request); err != nil {
			return nil, err
		}
		execution := ExecutionContext{SessionID: request.Execution.SessionID, ExecutionID: request.Execution.ExecutionID}
		if err := requireExecutionID(execution); err != nil {
			return nil, err
		}
		var instance Instance
		var err error
		if params.Method == RemoteMethodResume {
			instance, err = s.driver.Resume(ctx, execution, request.Input, request.Checkpoint)
		} else {
			instance, err = s.driver.Create(ctx, execution, request.Input)
		}
		if err != nil {
			return nil, err
		}
		if instance == nil {
			return nil, errors.New("driver returned a nil instance")
		}
		s.mu.Lock()
		if s.state != pluginhost.StateActive || ctx.Err() != nil {
			s.mu.Unlock()
			instance.Shutdown()
			return nil, errors.New("driver creation ended after its caller or generation closed")
		}
		s.next++
		id := fmt.Sprintf("%s/%d", s.id, s.next)
		s.entries[id] = &serviceInstance{instance: instance, caller: params.Caller, execution: execution}
		s.mu.Unlock()
		return json.Marshal(remoteInstanceResult{InstanceID: id, Checkpoint: instance.Checkpoint()})
	case RemoteMethodRun, RemoteMethodShutdown:
		var request remoteRunParams
		if err := json.Unmarshal(params.Params, &request); err != nil {
			return nil, err
		}
		s.mu.Lock()
		entry := s.entries[request.InstanceID]
		if entry == nil || entry.caller != params.Caller {
			s.mu.Unlock()
			return nil, errors.New("driver instance is unavailable to this caller")
		}
		if params.Method == RemoteMethodShutdown {
			delete(s.entries, request.InstanceID)
			s.mu.Unlock()
			entry.stop()
			return json.RawMessage(`{}`), nil
		}
		if entry.done != nil {
			s.mu.Unlock()
			return nil, errors.New("driver instance has already run")
		}
		entry.done = make(chan struct{})
		s.mu.Unlock()
		defer close(entry.done)
		gateway := serviceGateway{router: s.router, caller: s.id, scopeID: params.ExecutionID, executionID: entry.execution.ExecutionID}
		outcome, runErr := entry.instance.Run(ctx, gateway)
		result := remoteRunResult{Status: outcome.Status, ReceiptID: outcome.ReceiptID, Checkpoint: outcome.Checkpoint}
		if runErr != nil {
			result.Error = runErr.Error()
		}
		return json.Marshal(result)
	default:
		return nil, fmt.Errorf("unknown driver method %q", params.Method)
	}
}

type serviceGateway struct {
	router      pluginhost.ServiceRouter
	caller      string
	scopeID     string
	executionID string
}

func (g serviceGateway) ExecuteModelLoop(ctx context.Context, input PersistedInput, policy LoopPolicy) (ModelLoopReceipt, error) {
	raw, err := json.Marshal(DriverModelLoopParams{ExecutionID: g.executionID, Input: input, Policy: policy})
	if err != nil {
		return ModelLoopReceipt{}, err
	}
	reply, callErr := g.router.RouteServiceCall(ctx, g.caller, pluginhost.ServiceCallParams{Service: DriverModelLoopService, Method: DriverModelLoopMethod, ExecutionID: g.scopeID, Params: raw})
	if callErr != nil {
		return ModelLoopReceipt{}, callErr
	}
	var result DriverModelLoopResult
	if err := json.Unmarshal(reply, &result); err != nil {
		return ModelLoopReceipt{}, err
	}
	return ModelLoopReceipt{ID: result.ReceiptID}, nil
}
func (g serviceGateway) WriteCheckpoint(ctx context.Context, checkpoint Checkpoint) error {
	raw, err := json.Marshal(DriverCheckpointParams{ExecutionID: g.executionID, Checkpoint: checkpoint})
	if err != nil {
		return err
	}
	_, callErr := g.router.RouteServiceCall(ctx, g.caller, pluginhost.ServiceCallParams{Service: DriverCheckpointService, Method: DriverCheckpointMethod, ExecutionID: g.scopeID, Params: raw})
	if callErr != nil {
		return callErr
	}
	return nil
}
