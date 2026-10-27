package access

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"io"
	"sync"
)

func Descriptor() pluginhost.ServiceDescriptor {
	return pluginhost.ServiceDescriptor{Name: ServiceName, Version: "1.0.0", Methods: []pluginhost.ServiceMethodDescriptor{{Name: "authorize", InputSchema: "resource.authorization.request.v1", OutputSchema: "resource.authorization.decision.v1"}}}
}

// Client calls exactly the selected policy; no provider failure falls back.
type Client struct {
	call func(context.Context, string, json.RawMessage) (json.RawMessage, error)
}

func NewClient(call func(context.Context, string, json.RawMessage) (json.RawMessage, error)) *Client {
	return &Client{call: call}
}
func (c *Client) Authorize(ctx context.Context, r Request) (Decision, error) {
	if err := r.Validate(); err != nil {
		return Decision{}, err
	}
	raw, err := json.Marshal(r)
	if err != nil {
		return Decision{}, err
	}
	result, err := c.call(ctx, "authorize", raw)
	if err != nil {
		return Decision{}, err
	}
	var wire struct {
		Allow  *bool  `json:"allow"`
		Reason string `json:"reason,omitempty"`
	}
	dec := json.NewDecoder(bytes.NewReader(result))
	dec.DisallowUnknownFields()
	if dec.Decode(&wire) != nil || dec.Decode(new(any)) != io.EOF || wire.Allow == nil {
		return Decision{}, errors.New("invalid resource policy decision")
	}
	return Decision{Allow: *wire.Allow, Reason: wire.Reason}, nil
}

type Service struct {
	mu       sync.RWMutex
	id       string
	state    pluginhost.State
	policy   Policy
	requires []pluginhost.ServiceRequirement
}

func NewService(id string, policy Policy, requires ...pluginhost.ServiceRequirement) *Service {
	return &Service{id: id, state: pluginhost.StatePrepared, policy: policy, requires: requires}
}
func (s *Service) ID() string { return s.id }
func (s *Service) Status() pluginhost.Status {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return pluginhost.Status{ID: s.id, State: s.state}
}
func (s *Service) ProvidedServices() []pluginhost.ServiceDescriptor {
	return []pluginhost.ServiceDescriptor{Descriptor()}
}
func (s *Service) RequiredServices() []pluginhost.ServiceRequirement {
	return append([]pluginhost.ServiceRequirement(nil), s.requires...)
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
func (s *Service) InvokeService(ctx context.Context, input pluginhost.ServiceInvokeParams) (json.RawMessage, error) {
	if s.Status().State != pluginhost.StateActive || input.Service != ServiceName {
		return nil, errors.New("resource policy unavailable")
	}
	if input.Method != "authorize" {
		return nil, errors.New("unknown resource policy method")
	}
	var request Request
	dec := json.NewDecoder(bytes.NewReader(input.Params))
	dec.DisallowUnknownFields()
	if dec.Decode(&request) != nil || dec.Decode(new(any)) != io.EOF {
		return nil, errors.New("invalid authorization request")
	}
	if err := request.Validate(); err != nil {
		return nil, err
	}
	decision, err := s.policy.Authorize(ctx, request)
	if err != nil {
		return nil, err
	}
	return json.Marshal(decision)
}
