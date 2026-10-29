package session

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"

	"github.com/blueberrycongee/wuu/internal/persistence"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
)

// Service owns the default session algorithms and their resources. All callers,
// including native callers, cross the same serialized domain contract.
type Service struct {
	mu          sync.Mutex
	id          string
	state       pluginhost.State
	store       *documentStore
	resources   map[string]domainResource
	active      sync.WaitGroup
	pending     map[uint64]context.CancelFunc
	nextCall    uint64
	closed      chan struct{}
	closeErr    error
	inspections map[string]bool
}
type domainResource struct {
	caller string
	value  *InferenceJournalRuntime
}

func NewService(id string, documents persistence.Documents) *Service {
	return &Service{id: id, state: pluginhost.StatePrepared, store: newDocumentStore(context.Background(), documents), resources: make(map[string]domainResource), pending: make(map[uint64]context.CancelFunc), inspections: make(map[string]bool)}
}

// NewServiceWithJournalBackgroundDocuments supplies authority for journal-owned
// heartbeats and progress writes that outlive an individual service call.
func NewServiceWithJournalBackgroundDocuments(id string, documents, background persistence.Documents) *Service {
	s := NewService(id, documents)
	s.store.journalBackgroundDocuments = background
	return s
}
func (s *Service) ID() string { return s.id }
func (s *Service) Status() pluginhost.Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	return pluginhost.Status{ID: s.id, State: s.state}
}
func (s *Service) ProvidedServices() []pluginhost.ServiceDescriptor {
	methods := make([]pluginhost.ServiceMethodDescriptor, 0, len(domainMethods)+len(clientInputDomainMethods)+len(inspectionDomainMethods)+len(resourceMethods))
	for _, name := range append(append(append(append([]string{}, domainMethods...), clientInputDomainMethods...), inspectionDomainMethods...), resourceMethods...) {
		methods = append(methods, pluginhost.ServiceMethodDescriptor{Name: name, InputSchema: ServiceName + "." + name + ".input.v1", OutputSchema: ServiceName + "." + name + ".output.v1", Cleanup: name == "Journal.Close" || name == "ReleaseInspection" || name == "ReconcileInspection"})
	}
	return []pluginhost.ServiceDescriptor{{Name: ServiceName, Version: "1.0.0", Methods: methods}}
}
func (s *Service) RequiredServices() []pluginhost.ServiceRequirement {
	return []pluginhost.ServiceRequirement{{Name: persistence.ServiceName, MajorVersion: 1, Required: true}}
}
func (s *Service) Prepare(ctx context.Context) error { return ctx.Err() }
func (s *Service) Activate(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.state != pluginhost.StatePrepared {
		return fmt.Errorf("session service cannot activate from %s", s.state)
	}
	s.state = pluginhost.StateActive
	return nil
}

// Close stops admission and cancels active invocations without holding the state
// mutex across a dependency call. A deadline reports incomplete cleanup; a later
// Close can await the same drain. Dependencies must remain retained until it ends.
func (s *Service) Close(ctx context.Context) error {
	s.mu.Lock()
	if s.closed == nil {
		s.state = pluginhost.StateStopped
		s.closed = make(chan struct{})
		for _, cancel := range s.pending {
			cancel()
		}
		go s.drain(context.WithoutCancel(ctx))
	}
	closed := s.closed
	s.mu.Unlock()
	select {
	case <-closed:
		return s.closeErr
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (s *Service) drain(ctx context.Context) {
	s.active.Wait()
	s.mu.Lock()
	resources := s.resources
	s.resources = make(map[string]domainResource)
	s.mu.Unlock()
	var errs []error
	for _, r := range resources {
		errs = append(errs, r.value.closeContext(ctx))
	}
	s.closeErr = errors.Join(errs...)
	close(s.closed)
}

var resourceMethods = []string{"Journal.Close", "Journal.ReconcileOrphans", "Journal.Prune", "Journal.PrepareOperation", "Journal.PrepareAttempt", "Journal.UpsertSubmission", "Journal.MarkAttemptFirstEvent", "Journal.PrepareRecoveryAttempt", "Journal.CompleteAttempt", "Journal.CompleteOperation", "Journal.CompleteWorkflow"}

func containsMethod(methods []string, method string) bool {
	for _, name := range methods {
		if name == method {
			return true
		}
	}
	return false
}
func (s *Service) InvokeService(ctx context.Context, p pluginhost.ServiceInvokeParams) (json.RawMessage, error) {
	s.mu.Lock()
	if s.state != pluginhost.StateActive && p.Method != "ReleaseInspection" && p.Method != "ListInspections" && p.Method != "ReconcileInspection" {
		s.mu.Unlock()
		return nil, &pluginhost.HostServiceError{Code: "service_unavailable", Message: "session provider is not active"}
	}
	ctx, cancel := context.WithCancel(ctx)
	s.nextCall++
	callID := s.nextCall
	s.pending[callID] = cancel
	s.active.Add(1)
	s.mu.Unlock()
	defer func() { cancel(); s.mu.Lock(); delete(s.pending, callID); s.mu.Unlock(); s.active.Done() }()
	if err := ctx.Err(); err != nil {
		return nil, encodeDomainError(err)
	}
	if p.Caller == "" {
		return nil, &pluginhost.HostServiceError{Code: "invalid_request", Message: "session caller is required"}
	}
	var request invocation
	if err := json.Unmarshal(p.Params, &request); err != nil || request.Args == nil {
		return nil, &pluginhost.HostServiceError{Code: "invalid_request", Message: "session arguments must be an array"}
	}
	native := s.store.WithContext(ctx)
	native.caller = p.Caller
	native.providerID = s.id
	if p.Method == "ListInspections" || p.Method == "ReconcileInspection" {
		action := "state/inspections/list"
		if p.Method == "ReconcileInspection" {
			action = "state/inspections/reconcile"
		}
		if err := persistence.ValidateInspectionAdmission(p, action); err != nil {
			return nil, err
		}
		native.admission = p.Admission
	}
	var receiver any = native
	method := p.Method
	var releaseHandle string
	if containsMethod(resourceMethods, method) {
		if len(request.Args) < 1 {
			return nil, &pluginhost.HostServiceError{Code: "invalid_request", Message: "resource handle is required"}
		}
		var id string
		if err := json.Unmarshal(request.Args[0], &id); err != nil {
			return nil, encodeDomainError(err)
		}
		s.mu.Lock()
		r, found := s.resources[id]
		s.mu.Unlock()
		if !found || r.caller != p.Caller {
			return nil, &pluginhost.HostServiceError{Code: "resource_unavailable", Message: "session resource is absent or belongs to another caller"}
		}
		request.Args = request.Args[1:]
		method = strings.SplitN(method, ".", 2)[1]
		receiver = r.value
		if method != "Close" && method != "ReconcileOrphans" && method != "Prune" {
			if len(request.Args) < 1 {
				return nil, &pluginhost.HostServiceError{Code: "invalid_request", Message: "journal owner is required"}
			}
			var owner string
			if err := json.Unmarshal(request.Args[0], &owner); err != nil {
				return nil, encodeDomainError(err)
			}
			request.Args = request.Args[1:]
			journal := r.value.ForOwner(owner).(*documentInferenceJournal)
			journal.ctx = ctx
			receiver = journal
		}
		if method == "Close" {
			releaseHandle = id
		}

	} else if !containsMethod(domainMethods, method) && !containsMethod(clientInputDomainMethods, method) && !containsMethod(inspectionDomainMethods, method) {
		return nil, &pluginhost.HostServiceError{Code: "method_not_found", Message: "unknown session domain method"}
	}
	guardKey := ""
	guardCreated := false
	if method == "AcquireInspection" || method == "ReleaseInspection" {
		if len(request.Args) != 1 {
			return nil, encodeDomainError(persistence.ErrInvalid)
		}
		var id string
		if json.Unmarshal(request.Args[0], &id) != nil || id == "" || len(id) > 256 {
			return nil, encodeDomainError(persistence.ErrInvalid)
		}
		guardKey = sessionInspectionID(p.Caller, id)
		if method == "AcquireInspection" {
			s.mu.Lock()
			if !s.inspections[guardKey] {
				s.inspections[guardKey] = true
				guardCreated = true
				s.active.Add(1)
			}
			s.mu.Unlock()
		}
	}
	if method == "ReconcileInspection" {
		if len(request.Args) != 1 {
			return nil, encodeDomainError(persistence.ErrInvalid)
		}
		var resolution persistence.InspectionResolution
		if json.Unmarshal(request.Args[0], &resolution) != nil {
			return nil, encodeDomainError(persistence.ErrInvalid)
		}
		guardKey = resolution.ID
	}
	var values []any
	var err error
	if releaseHandle != "" {
		if len(request.Args) != 0 {
			return nil, &pluginhost.HostServiceError{Code: "invalid_request", Message: "journal Close takes no additional arguments"}
		}
		err = receiver.(*InferenceJournalRuntime).closeContext(ctx)
	} else {
		values, err = invokeDomainMethod(ctx, receiver, method, request.Args)
	}
	if err != nil {
		if guardCreated && (errors.Is(err, persistence.ErrInspectionUnsupported) || errors.Is(err, persistence.ErrInspectionReleased) || errors.Is(err, persistence.ErrInvalid) || errors.Is(err, persistence.ErrNamespaceOwnership)) {
			s.mu.Lock()
			if s.inspections[guardKey] {
				delete(s.inspections, guardKey)
				s.active.Done()
			}
			s.mu.Unlock()
		}
		return nil, encodeDomainError(err)
	}
	if method == "ReleaseInspection" || method == "ReconcileInspection" {
		s.mu.Lock()
		if s.inspections[guardKey] {
			delete(s.inspections, guardKey)
			s.active.Done()
		}
		s.mu.Unlock()
	}
	if releaseHandle != "" {
		s.mu.Lock()
		delete(s.resources, releaseHandle)
		s.mu.Unlock()
	}
	result := invocationResult{Values: make([]json.RawMessage, len(values))}
	for i, value := range values {
		if runtime, ok := value.(*InferenceJournalRuntime); ok {
			id := NewID()
			s.mu.Lock()
			s.resources[id] = domainResource{p.Caller, runtime}
			s.mu.Unlock()
			value = resourceHandle{Handle: id, RuntimeID: runtime.RuntimeID()}
		}

		raw, err := marshalDomainValue(value)
		if err != nil {
			return nil, encodeDomainError(err)
		}
		result.Values[i] = raw
	}
	return json.Marshal(result)
}

// Reflection only decodes the explicit method inventory above. Unexported
// helpers, transaction callbacks and future methods are never auto-published.
func invokeDomainMethod(ctx context.Context, receiver any, name string, raw []json.RawMessage) ([]any, error) {
	method := reflect.ValueOf(receiver).MethodByName(name)
	if !method.IsValid() {
		return nil, &pluginhost.HostServiceError{Code: "method_not_found", Message: "session method unavailable"}
	}
	typ := method.Type()
	args := make([]reflect.Value, typ.NumIn())
	offset := 0
	contextType := reflect.TypeOf((*context.Context)(nil)).Elem()
	if len(args) > 0 && typ.In(0) == contextType {
		args[0] = reflect.ValueOf(ctx)
		offset = 1
	}
	if len(raw) != len(args)-offset {
		return nil, &pluginhost.HostServiceError{Code: "invalid_request", Message: "session argument count mismatch"}
	}
	for i, item := range raw {
		value := reflect.New(typ.In(i + offset))
		if err := unmarshalDomainValue(item, value.Interface()); err != nil {
			return nil, &pluginhost.HostServiceError{Code: "invalid_request", Message: err.Error()}
		}
		args[i+offset] = value.Elem()
	}
	var results []reflect.Value
	if typ.IsVariadic() {
		results = method.CallSlice(args)
	} else {
		results = method.Call(args)
	}
	if len(results) > 0 && typ.Out(len(results)-1) == reflect.TypeOf((*error)(nil)).Elem() {
		last := results[len(results)-1]
		results = results[:len(results)-1]
		if !last.IsNil() {
			return nil, last.Interface().(error)
		}
	}
	out := make([]any, len(results))
	for i, value := range results {
		out[i] = value.Interface()
	}
	return out, nil
}

type resourceHandle struct {
	Handle    string `json:"handle"`
	RuntimeID string `json:"runtime_id,omitempty"`
}
