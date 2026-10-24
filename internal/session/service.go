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
	mu        sync.Mutex
	id        string
	state     pluginhost.State
	store     *documentStore
	resources map[string]domainResource
}
type domainResource struct {
	caller string
	value  any
}

func NewService(id string, documents persistence.Documents) *Service {
	return &Service{id: id, state: pluginhost.StatePrepared, store: newDocumentStore(context.Background(), documents), resources: make(map[string]domainResource)}
}
func (s *Service) ID() string { return s.id }
func (s *Service) Status() pluginhost.Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	return pluginhost.Status{ID: s.id, State: s.state}
}
func (s *Service) ProvidedServices() []pluginhost.ServiceDescriptor {
	methods := make([]pluginhost.ServiceMethodDescriptor, 0, len(domainMethods)+len(resourceMethods))
	for _, name := range append(append([]string{}, domainMethods...), resourceMethods...) {
		methods = append(methods, pluginhost.ServiceMethodDescriptor{Name: name, InputSchema: ServiceName + "." + name + ".input.v1", OutputSchema: ServiceName + "." + name + ".output.v1", Cleanup: name == "Journal.Close"})
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
func (s *Service) Close(context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state = pluginhost.StateStopped
	var errs []error
	for id, r := range s.resources {
		switch v := r.value.(type) {
		case *InferenceJournalRuntime:
			errs = append(errs, v.Close())
		case *DocumentThreadExecutionLease:
			errs = append(errs, v.Release())
		}
		delete(s.resources, id)
	}
	return errors.Join(errs...)
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
	defer s.mu.Unlock()
	if s.state != pluginhost.StateActive {
		return nil, &pluginhost.HostServiceError{Code: "service_unavailable", Message: "session provider is not active"}
	}
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
	var receiver any = s.store.WithContext(ctx)
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
		r, found := s.resources[id]
		if !found || r.caller != p.Caller {
			return nil, &pluginhost.HostServiceError{Code: "resource_unavailable", Message: "session resource is absent or belongs to another caller"}
		}
		request.Args = request.Args[1:]
		method = strings.SplitN(method, ".", 2)[1]
		switch value := r.value.(type) {
		case *InferenceJournalRuntime:
			if !strings.HasPrefix(p.Method, "Journal.") {
				return nil, &pluginhost.HostServiceError{Code: "invalid_request", Message: "wrong resource kind"}
			}
			receiver = value
			if method != "Close" && method != "ReconcileOrphans" && method != "Prune" {
				if len(request.Args) < 1 {
					return nil, &pluginhost.HostServiceError{Code: "invalid_request", Message: "journal owner is required"}
				}
				var owner string
				if err := json.Unmarshal(request.Args[0], &owner); err != nil {
					return nil, encodeDomainError(err)
				}
				request.Args = request.Args[1:]
				receiver = value.ForOwner(owner)
			}
		case *DocumentThreadExecutionLease:
			if !strings.HasPrefix(p.Method, "Lease.") {
				return nil, &pluginhost.HostServiceError{Code: "invalid_request", Message: "wrong resource kind"}
			}
			receiver = value
		}
		if method == "Close" || method == "Release" {
			releaseHandle = id
		}
	} else if !containsMethod(domainMethods, method) {
		return nil, &pluginhost.HostServiceError{Code: "method_not_found", Message: "unknown session domain method"}
	}
	values, err := invokeDomainMethod(ctx, receiver, method, request.Args)
	if err != nil {
		return nil, encodeDomainError(err)
	}
	if releaseHandle != "" {
		delete(s.resources, releaseHandle)
	}
	result := invocationResult{Values: make([]json.RawMessage, len(values))}
	for i, value := range values {
		switch v := value.(type) {
		case *InferenceJournalRuntime:
			id := NewID()
			s.resources[id] = domainResource{p.Caller, v}
			value = resourceHandle{Handle: id, RuntimeID: v.RuntimeID()}
		case *DocumentThreadExecutionLease:
			if v == nil {
				value = nil
			} else {
				id := NewID()
				s.resources[id] = domainResource{p.Caller, v}
				value = resourceHandle{Handle: id}
			}
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
