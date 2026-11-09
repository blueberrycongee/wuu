package toolbatch

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/toolresult"
)

func Descriptor() pluginhost.ServiceDescriptor {
	return pluginhost.ServiceDescriptor{Name: ServiceName, Version: "1.0.0", Methods: []pluginhost.ServiceMethodDescriptor{{Name: "execute", InputSchema: ServiceName + ".execute.input.v1", OutputSchema: ServiceName + ".execute.output.v1"}}}
}
func StreamingDescriptor() pluginhost.ServiceDescriptor {
	descriptor := Descriptor()
	descriptor.Methods = append(descriptor.Methods, pluginhost.ServiceMethodDescriptor{Name: "stream_admit", InputSchema: ServiceName + ".stream.admit.input.v1", OutputSchema: ServiceName + ".stream.admit.output.v1"})
	return descriptor
}
func ResourceDescriptor() pluginhost.ServiceDescriptor {
	methods := []pluginhost.ServiceMethodDescriptor{}
	for _, name := range []string{"resolve", "execute", "settle", "cancel"} {
		methods = append(methods, pluginhost.ServiceMethodDescriptor{Name: name, InputSchema: ResourceService + "." + name + ".input.v1", OutputSchema: ResourceService + "." + name + ".output.v1"})
	}
	return pluginhost.ServiceDescriptor{Name: ResourceService, Version: "1.0.0", Methods: methods}
}

type ResourceRequest struct {
	BatchID string             `json:"batch_id"`
	Index   *int               `json:"index"`
	Result  *toolresult.Result `json:"result,omitempty"`
}
type Completion struct {
	Completed bool `json:"completed"`
}
type scopeKey struct{}
type resourceScope struct {
	owner, batchID string
	resources      Resources
}

// WithResources binds authority in the authenticated execution context, never in
// an external payload. Nested service callbacks retain this original generation.
func WithResources(ctx context.Context, owner, batchID string, resources Resources) context.Context {
	return context.WithValue(ctx, scopeKey{}, resourceScope{owner: owner, batchID: batchID, resources: resources})
}
func InvokeResources(ctx context.Context, p pluginhost.ServiceInvokeParams) (json.RawMessage, error) {
	scope, ok := ctx.Value(scopeKey{}).(resourceScope)
	if !ok || scope.resources == nil || p.Caller != scope.owner {
		return nil, &pluginhost.HostServiceError{Code: "execution_not_authorized", Message: "tool batch resources require the owning batch callback"}
	}
	var input ResourceRequest
	if err := Decode(p.Params, &input); err != nil {
		return nil, err
	}
	if input.Index == nil {
		return nil, errors.New("tool batch resource index is required")
	}
	if input.BatchID != scope.batchID {
		return nil, errors.New("tool batch resource identity mismatch")
	}
	var result any
	var err error
	switch p.Method {
	case "resolve":
		result, err = scope.resources.Resolve(ctx, *input.Index)
	case "execute":
		result, err = scope.resources.Execute(ctx, *input.Index)
	case "cancel":
		result, err = scope.resources.Cancel(ctx, *input.Index)
	case "settle":
		if input.Result == nil {
			return nil, errors.New("settle requires a result")
		}
		result, err = scope.resources.Settle(ctx, *input.Index, *input.Result)
	default:
		return nil, errors.New("unknown tool batch resource method")
	}
	if err != nil {
		return nil, err
	}
	return json.Marshal(result)
}
func Decode(raw json.RawMessage, target any) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(target); err != nil {
		return err
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return errors.New("expected one JSON value")
	}
	return nil
}

// Service is the explicit @1 compatibility provider, not a host fallback.
// Selecting another agent.tool.batch provider means this adapter is never run.
type Service struct {
	router pluginhost.ServiceRouter
	mu     sync.Mutex
	state  pluginhost.State
}

func NewService(router pluginhost.ServiceRouter) *Service {
	return &Service{router: router, state: pluginhost.StatePrepared}
}
func (*Service) ID() string { return AdapterModule }
func (s *Service) Status() pluginhost.Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	return pluginhost.Status{ID: s.ID(), State: s.state}
}
func (*Service) ProvidedServices() []pluginhost.ServiceDescriptor {
	return []pluginhost.ServiceDescriptor{StreamingDescriptor()}
}
func (*Service) RequiredServices() []pluginhost.ServiceRequirement {
	return []pluginhost.ServiceRequirement{{Name: ResourceService, MajorVersion: 1, Required: true}}
}
func (s *Service) Activate(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state = pluginhost.StateActive
	return nil
}
func (s *Service) Close(context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state = pluginhost.StateStopped
	return nil
}
func (s *Service) InvokeService(ctx context.Context, p pluginhost.ServiceInvokeParams) (json.RawMessage, error) {
	if s.Status().State != pluginhost.StateActive {
		return nil, errors.New("batch adapter is not active")
	}
	if p.Service == ServiceName && p.Method == "stream_admit" {
		var request StreamRequest
		if err := Decode(p.Params, &request); err != nil {
			return nil, err
		}
		admitted, err := (V1Adapter{}).AdmitStream(ctx, request)
		if err != nil {
			return nil, err
		}
		return json.Marshal(StreamAdmission{Admit: admitted})
	}
	if p.Service != ServiceName || p.Method != "execute" {
		return nil, errors.New("unknown batch method")
	}
	var req Request
	if err := Decode(p.Params, &req); err != nil {
		return nil, err
	}
	if len(req.Calls) == 0 || len(req.Calls) > MaxCalls {
		return nil, errors.New("invalid batch size")
	}
	resources := remoteResources{router: s.router, owner: s.ID(), executionID: p.ExecutionID, batchID: req.ID}
	if err := (V1Adapter{}).ExecuteBatch(ctx, req, resources); err != nil {
		return nil, err
	}
	return json.Marshal(Completion{Completed: true})
}

type remoteResources struct {
	router                      pluginhost.ServiceRouter
	owner, executionID, batchID string
}

func (r remoteResources) call(ctx context.Context, method string, index int, result *toolresult.Result, out any) error {
	raw, err := json.Marshal(ResourceRequest{BatchID: r.batchID, Index: &index, Result: result})
	if err != nil {
		return err
	}
	response, callErr := r.router.RouteServiceCall(ctx, r.owner, pluginhost.ServiceCallParams{Service: ResourceService, Method: method, ExecutionID: r.executionID, Params: raw})
	if callErr != nil {
		return fmt.Errorf("batch %s: %w", method, callErr)
	}
	return Decode(response, out)
}
func (r remoteResources) Resolve(ctx context.Context, index int) (out Resolution, err error) {
	err = r.call(ctx, "resolve", index, nil, &out)
	return
}
func (r remoteResources) Execute(ctx context.Context, index int) (out Outcome, err error) {
	err = r.call(ctx, "execute", index, nil, &out)
	return
}
func (r remoteResources) Cancel(ctx context.Context, index int) (out Outcome, err error) {
	err = r.call(ctx, "cancel", index, nil, &out)
	return
}
func (r remoteResources) Settle(ctx context.Context, index int, result toolresult.Result) (out Outcome, err error) {
	err = r.call(ctx, "settle", index, &result, &out)
	return
}
