package titleservice

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sync"

	"github.com/blueberrycongee/wuu/internal/pluginhost"
)

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
	methods := make([]pluginhost.ServiceMethodDescriptor, 0, 2)
	for _, method := range []string{"plan", "resolve"} {
		methods = append(methods, pluginhost.ServiceMethodDescriptor{Name: method, InputSchema: ServiceName + "." + method + ".input.v1", OutputSchema: ServiceName + "." + method + ".output.v1"})
	}
	return []pluginhost.ServiceDescriptor{{Name: ServiceName, Version: "1.0.0", Methods: methods}}
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
		return fmt.Errorf("title provider cannot activate from %s", s.state)
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
		return nil, &pluginhost.HostServiceError{Code: "service_unavailable", Message: "title provider is not active"}
	}
	if p.Service != ServiceName {
		return nil, &pluginhost.HostServiceError{Code: "method_not_found", Message: "unknown title service"}
	}
	return InvokeNative(ctx, p.Method, p.Params)
}

// InvokeNative is the explicit embedded assembly used by generationless clients.
// Runtime compositions invoke Service through their selected graph instead.
func InvokeNative(ctx context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var result any
	decode := func(out any) error {
		d := json.NewDecoder(bytes.NewReader(raw))
		d.DisallowUnknownFields()
		if err := d.Decode(out); err != nil {
			return err
		}
		if err := d.Decode(new(any)); err != io.EOF {
			return fmt.Errorf("expected one title request")
		}
		return nil
	}
	switch method {
	case "plan":
		var in PlanRequest
		if err := decode(&in); err != nil {
			return nil, err
		}
		result = BuildPlan(in)
	case "resolve":
		var in struct {
			Text string `json:"text"`
		}
		if err := decode(&in); err != nil {
			return nil, err
		}
		result = struct {
			Title string `json:"title"`
		}{Clean(in.Text)}
	default:
		return nil, &pluginhost.HostServiceError{Code: "method_not_found", Message: "unknown title method"}
	}
	return json.Marshal(result)
}
