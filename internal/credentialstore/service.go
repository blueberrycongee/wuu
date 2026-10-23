package credentialstore

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"sync"

	"github.com/blueberrycongee/wuu/internal/pluginhost"
)

const (
	ServiceName        = "state.credentials"
	OSModule           = "wuu.credentials.os"
	FileModule         = "wuu.credentials.file"
	maxCredentialBytes = 1 << 20
)

type EntryRequest struct {
	Namespace string `json:"namespace"`
	Key       string `json:"key"`
}

// Value is base64 on the wire; null means absent, while "" is a present empty
// entry. Providers must preserve this distinction for compare-and-exchange.
type EntryResult struct {
	Value []byte `json:"value"`
}
type SetRequest struct {
	EntryRequest
	Value []byte `json:"value"`
}
type CompareExchangeRequest struct {
	EntryRequest
	Expected []byte `json:"expected"`
	Value    []byte `json:"value"`
}
type CompareExchangeResult struct {
	Exchanged bool `json:"exchanged"`
}

// Service owns one credential backend for a composition generation. The
// opener runs only on activation and never substitutes a different backend.
type Service struct {
	mu    sync.RWMutex
	id    string
	state pluginhost.State
	open  func() (Store, error)
	store Store
}

func NewService(id string, open func() (Store, error)) *Service {
	return &Service{id: id, open: open, state: pluginhost.StatePrepared}
}
func (s *Service) ID() string { return s.id }
func (s *Service) Status() pluginhost.Status {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return pluginhost.Status{ID: s.id, State: s.state}
}
func (s *Service) ProvidedServices() []pluginhost.ServiceDescriptor {
	methods := make([]pluginhost.ServiceMethodDescriptor, 0, 4)
	for _, method := range []string{"get", "set", "delete", "compare_exchange"} {
		methods = append(methods, pluginhost.ServiceMethodDescriptor{Name: method, InputSchema: ServiceName + "." + method + ".input.v1", OutputSchema: ServiceName + "." + method + ".output.v1", Cleanup: true})
	}
	return []pluginhost.ServiceDescriptor{{Name: ServiceName, Version: "1.0.0", Methods: methods}}
}
func (*Service) RequiredServices() []pluginhost.ServiceRequirement { return nil }
func (s *Service) Activate(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state != pluginhost.StatePrepared {
		return credentialError("service_unavailable", "credential provider cannot activate")
	}
	store, err := s.open()
	if err != nil || store == nil {
		s.state = pluginhost.StateFailed
		return credentialError("credential_unavailable", "selected credential backend is unavailable")
	}
	s.store = store
	s.state = pluginhost.StateActive
	return nil
}
func (s *Service) Close(context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state = pluginhost.StateStopped
	store := s.store
	s.store = nil
	if closer, ok := store.(io.Closer); ok {
		if err := closer.Close(); err != nil {
			return credentialError("credential_unavailable", "credential backend cleanup failed")
		}
	}
	return nil
}

func (s *Service) InvokeService(ctx context.Context, params pluginhost.ServiceInvokeParams) (json.RawMessage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.state != pluginhost.StateActive {
		return nil, credentialError("service_unavailable", "credential provider is not active")
	}
	if params.Service != ServiceName {
		return nil, credentialError("service_unavailable", "credential service is unavailable")
	}
	var result any = struct{}{}
	var err error
	switch params.Method {
	case "get", "delete":
		var request EntryRequest
		if err := decodeCredentialRequest(params.Params, &request); err != nil {
			return nil, err
		}
		if err := validateEntry(request); err != nil {
			return nil, err
		}
		if params.Method == "delete" {
			err = s.store.Delete(ctx, request.Namespace, request.Key)
		} else {
			var value []byte
			value, err = s.store.Get(ctx, request.Namespace, request.Key)
			defer clear(value)
			if errors.Is(err, ErrNotFound) {
				value, err = nil, nil
			} else if err == nil && value == nil {
				value = []byte{}
			}
			result = EntryResult{Value: value}
		}
	case "set":
		var request SetRequest
		if err := decodeCredentialRequest(params.Params, &request); err != nil {
			return nil, err
		}
		defer clear(request.Value)
		if err := validateEntry(request.EntryRequest); err != nil {
			return nil, err
		}
		if request.Value == nil || len(request.Value) > maxCredentialBytes {
			return nil, credentialError("invalid_params", "credential value must be base64 within the size limit")
		}
		err = s.store.Set(ctx, request.Namespace, request.Key, request.Value)
	case "compare_exchange":
		var request CompareExchangeRequest
		if err := decodeCredentialRequest(params.Params, &request); err != nil {
			return nil, err
		}
		defer clear(request.Expected)
		defer clear(request.Value)
		if err := validateEntry(request.EntryRequest); err != nil {
			return nil, err
		}
		if len(request.Expected) > maxCredentialBytes || len(request.Value) > maxCredentialBytes {
			return nil, credentialError("invalid_params", "credential value exceeds the size limit")
		}
		exchanged := false
		err = s.store.Update(ctx, request.Namespace, request.Key, func(current []byte) ([]byte, error) {
			defer clear(current)
			if (current == nil) != (request.Expected == nil) || !bytes.Equal(current, request.Expected) {
				return nil, errCredentialConflict
			}
			exchanged = true
			return bytes.Clone(request.Value), nil
		})
		if errors.Is(err, errCredentialConflict) {
			err = nil
		}
		result = CompareExchangeResult{Exchanged: exchanged}
	default:
		return nil, credentialError("method_not_found", "credential method is unavailable")
	}
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, credentialError("request_cancelled", "credential request cancelled; a write may have been applied")
		}
		return nil, credentialError("credential_unavailable", "selected credential backend failed; a write may have been applied")
	}
	return json.Marshal(result)
}

var errCredentialConflict = errors.New("credential compare conflict")

func credentialError(code, message string) error {
	return &pluginhost.HostServiceError{Code: code, Message: message}
}
func validateEntry(request EntryRequest) error {
	if strings.TrimSpace(request.Namespace) == "" || strings.TrimSpace(request.Key) == "" || len(request.Namespace) > 1024 || len(request.Key) > 4096 || strings.ContainsAny(request.Namespace+request.Key, "\x00\r\n") {
		return credentialError("invalid_params", "credential namespace or key is invalid")
	}
	return nil
}
func decodeCredentialRequest(raw json.RawMessage, target any) error {
	if len(raw) > 3*maxCredentialBytes {
		return credentialError("invalid_params", "credential request exceeds the size limit")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return credentialError("invalid_params", "credential request is invalid")
	}
	if decoder.Decode(new(any)) != io.EOF {
		return credentialError("invalid_params", "credential request must contain one object")
	}
	return nil
}
