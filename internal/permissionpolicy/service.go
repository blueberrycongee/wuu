// Package permissionpolicy implements the default permission decisions behind
// the same public security.authorize contract used by external providers.
package permissionpolicy

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"sync"

	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/tools"
)

const Module = "wuu.permissions.default"

type Service struct {
	mu    sync.RWMutex
	id    string
	state pluginhost.State
}

func NewService(id string) *Service { return &Service{id: id, state: pluginhost.StatePrepared} }
func (s *Service) ID() string       { return s.id }
func (s *Service) Status() pluginhost.Status {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return pluginhost.Status{ID: s.id, State: s.state}
}
func (*Service) ProvidedServices() []pluginhost.ServiceDescriptor {
	return []pluginhost.ServiceDescriptor{pluginhost.SecurityAuthorizeDescriptor()}
}
func (*Service) RequiredServices() []pluginhost.ServiceRequirement { return nil }
func (s *Service) Activate(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state != pluginhost.StatePrepared {
		return &pluginhost.HostServiceError{Code: "service_unavailable", Message: "permission provider cannot activate"}
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
func (s *Service) InvokeService(ctx context.Context, params pluginhost.ServiceInvokeParams) (json.RawMessage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.state != pluginhost.StateActive || params.Service != pluginhost.SecurityAuthorizeService {
		return nil, &pluginhost.HostServiceError{Code: "service_unavailable", Message: "permission provider is unavailable"}
	}
	if params.Method != pluginhost.SecurityAuthorizeMethod {
		return nil, &pluginhost.HostServiceError{Code: "method_not_found", Message: "permission method is unavailable"}
	}
	var request pluginhost.AuthorizationRequest
	decoder := json.NewDecoder(bytes.NewReader(params.Params))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&request) != nil || decoder.Decode(new(any)) != io.EOF || request.Tool.Name == "" {
		return nil, &pluginhost.HostServiceError{Code: "invalid_params", Message: "permission request is invalid"}
	}
	var boundary tools.WorkspaceBoundary
	switch request.PermissionMode {
	case "", config.PermissionModeStandard:
		boundary = tools.StandardBoundary()
	case config.PermissionModeReadOnly:
		boundary = tools.ReadOnlyBoundary()
	case config.PermissionModeUnconfined:
		boundary = tools.UnconfinedBoundary()
	default:
		return nil, &pluginhost.HostServiceError{Code: "invalid_params", Message: "permission mode is unknown"}
	}
	decision := pluginhost.AuthorizationDecision{Outcome: "allow"}
	if err := boundary.Check(tools.ToolInfo{Name: request.Tool.Name, Kind: tools.ToolKind(request.Tool.Kind), ReadOnly: request.Tool.ReadOnly, Destructive: request.Tool.Destructive}, providers.ToolCall{Name: request.Tool.Name, Arguments: request.Tool.Arguments}); err != nil {
		decision = pluginhost.AuthorizationDecision{Outcome: "deny", Reason: "this agent is read-only; it may observe but not modify"}
	}
	return json.Marshal(decision)
}
