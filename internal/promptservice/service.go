package promptservice

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sync"

	"github.com/blueberrycongee/wuu/internal/pluginhost"
)

// Service participates in the ordinary graph; it owns no private registry.
type Service struct {
	mu    sync.Mutex
	state pluginhost.State
}

func NewService() *Service    { return &Service{state: pluginhost.StatePrepared} }
func (s *Service) ID() string { return NativeModule }
func (s *Service) Status() pluginhost.Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	return pluginhost.Status{ID: s.ID(), State: s.state}
}
func (s *Service) ProvidedServices() []pluginhost.ServiceDescriptor {
	return []pluginhost.ServiceDescriptor{{Name: ServiceName, Version: "1.0.0", Methods: []pluginhost.ServiceMethodDescriptor{{Name: "assemble", InputSchema: ServiceName + ".assemble.input.v1", OutputSchema: ServiceName + ".assemble.output.v1"}}}}
}
func (s *Service) RequiredServices() []pluginhost.ServiceRequirement { return nil }
func (s *Service) Prepare(ctx context.Context) error                 { return ctx.Err() }
func (s *Service) Activate(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state != pluginhost.StatePrepared {
		return fmt.Errorf("prompt provider cannot activate from %s", s.state)
	}
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
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s.Status().State != pluginhost.StateActive {
		return nil, &pluginhost.HostServiceError{Code: "service_unavailable", Message: "prompt provider is not active"}
	}
	if p.Service != ServiceName || p.Method != "assemble" {
		return nil, &pluginhost.HostServiceError{Code: "method_not_found", Message: "unknown prompt method"}
	}
	var in Request
	d := json.NewDecoder(bytes.NewReader(p.Params))
	d.DisallowUnknownFields()
	if err := d.Decode(&in); err != nil {
		return nil, &pluginhost.HostServiceError{Code: "invalid_params", Message: err.Error()}
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return nil, &pluginhost.HostServiceError{Code: "invalid_params", Message: "expected one request object"}
	}
	result, err := Assemble(in)
	if err != nil {
		return nil, &pluginhost.HostServiceError{Code: "invalid_params", Message: err.Error()}
	}
	return json.Marshal(result)
}
