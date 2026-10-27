// Package permissionpolicy implements the default permission decisions behind
// the same public security.authorize contract used by external providers.
package permissionpolicy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"sync"

	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/tools"
)

const Module = "wuu.permissions.default"

type Service struct {
	mu     sync.RWMutex
	id     string
	state  pluginhost.State
	leases map[string]authorizationLease
}

func NewService(id string) *Service {
	return &Service{id: id, state: pluginhost.StatePrepared, leases: map[string]authorizationLease{}}
}
func (s *Service) ID() string { return s.id }
func (s *Service) Status() pluginhost.Status {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return pluginhost.Status{ID: s.id, State: s.state}
}
func (*Service) ProvidedServices() []pluginhost.ServiceDescriptor {
	descriptor := pluginhost.SecurityAuthorizeDescriptor()
	for _, name := range []string{"acquire-authorization", "release-authorization"} {
		descriptor.Methods = append(descriptor.Methods, pluginhost.ServiceMethodDescriptor{Name: name, InputSchema: "security.authorize." + name + ".input.v1", OutputSchema: "security.authorize." + name + ".output.v1", Cleanup: name == "release-authorization"})
	}
	return []pluginhost.ServiceDescriptor{descriptor}
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

type authorizationLease struct {
	request  string
	decision pluginhost.AuthorizationDecision
	released bool
}

func decodeRequest(raw json.RawMessage, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(target) != nil || decoder.Decode(new(any)) != io.EOF {
		return &pluginhost.HostServiceError{Code: "invalid_params", Message: "permission request is invalid"}
	}
	return nil
}
func (s *Service) InvokeService(ctx context.Context, params pluginhost.ServiceInvokeParams) (json.RawMessage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if params.Service != pluginhost.SecurityAuthorizeService {
		return nil, errors.New("permission service unavailable")
	}
	if params.Method == "release-authorization" {
		var request pluginhost.AuthorizationReleaseRequest
		if err := decodeRequest(params.Params, &request); err != nil {
			return nil, err
		}
		if params.Caller == "" || strings.TrimSpace(request.ID) == "" || len(request.ID) > 256 {
			return nil, errors.New("permission lease caller and ID required")
		}
		key := params.Caller + "\x00" + request.ID
		lease := s.leases[key]
		lease.released = true
		s.leases[key] = lease
		return json.RawMessage(`{}`), nil
	}
	if s.state != pluginhost.StateActive {
		return nil, &pluginhost.HostServiceError{Code: "service_unavailable", Message: "permission provider is unavailable"}
	}
	switch params.Method {
	case pluginhost.SecurityAuthorizeMethod:
		var request pluginhost.AuthorizationRequest
		if err := decodeRequest(params.Params, &request); err != nil {
			return nil, err
		}
		decision, err := decide(request)
		if err != nil {
			return nil, err
		}
		return json.Marshal(decision)
	case "acquire-authorization":
		var request pluginhost.AuthorizationAcquireRequest
		if err := decodeRequest(params.Params, &request); err != nil {
			return nil, err
		}
		if params.Caller == "" || strings.TrimSpace(request.ID) == "" || len(request.ID) > 256 {
			return nil, errors.New("permission lease caller and ID required")
		}
		key := params.Caller + "\x00" + request.ID
		raw, _ := json.Marshal(request.Request)
		lease, found := s.leases[key]
		if found && (lease.released || lease.request != string(raw)) {
			return nil, errors.New("permission lease was released or changed")
		}
		if !found {
			decision, err := decide(request.Request)
			if err != nil {
				return nil, err
			}
			lease = authorizationLease{request: string(raw), decision: decision}
			s.leases[key] = lease
		}
		// This provider's policy has no mutable state. The captured session mode is
		// fenced by the caller's selected session authority; registry service scopes
		// retain this immutable policy generation until effect cleanup completes.
		return json.Marshal(pluginhost.AuthorizationLease{ID: request.ID, Decision: lease.decision})
	default:
		return nil, &pluginhost.HostServiceError{Code: "method_not_found", Message: "permission method is unavailable"}
	}
}
func decide(request pluginhost.AuthorizationRequest) (pluginhost.AuthorizationDecision, error) {
	if request.Tool.Name == "" {
		return pluginhost.AuthorizationDecision{}, errors.New("permission tool is required")
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
		return pluginhost.AuthorizationDecision{}, &pluginhost.HostServiceError{Code: "invalid_params", Message: "permission mode is unknown"}
	}
	decision := pluginhost.AuthorizationDecision{Outcome: "allow"}
	if err := boundary.Check(tools.ToolInfo{Name: request.Tool.Name, Kind: tools.ToolKind(request.Tool.Kind), ReadOnly: request.Tool.ReadOnly, Destructive: request.Tool.Destructive}, providers.ToolCall{Name: request.Tool.Name, Arguments: request.Tool.Arguments}); err != nil {
		decision = pluginhost.AuthorizationDecision{Outcome: "deny", Reason: "this agent is read-only; it may observe but not modify"}
	}
	return decision, nil
}
