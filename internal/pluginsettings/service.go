package pluginsettings

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"sync"

	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/securefs"
	"github.com/blueberrycongee/wuu/internal/storelock"
)

const ServiceName = "state.plugin-settings"
const Module = "wuu.settings.local"

type ReadRequest struct {
	Subject string `json:"subject"`
	Scope   Scope  `json:"scope"`
}
type SettingsView struct {
	Document Document `json:"document"`
	Revision string   `json:"revision"`
}
type StateView struct {
	Document StateDocument `json:"document"`
	Revision string        `json:"revision"`
}
type UpdateRequest struct {
	ReadRequest
	ExpectedRevision string                     `json:"expected_revision"`
	Fingerprint      string                     `json:"fingerprint"`
	Values           map[string]json.RawMessage `json:"values"`
}
type UpdateStateRequest struct {
	ReadRequest
	ExpectedRevision string            `json:"expected_revision"`
	Values           map[string]string `json:"values"`
}

// Service owns plugin settings and opaque state in existing user/workspace
// files. Subject identity is supplied by an admitted consumer; ordinary plugin
// requests still pass through that consumer's manifest ownership checks.
type Service struct {
	mu                sync.RWMutex
	id, home, project string
	state             pluginhost.State
}

func NewService(id, home, project string) *Service {
	return &Service{id: id, home: home, project: project, state: pluginhost.StatePrepared}
}
func (s *Service) ID() string { return s.id }
func (s *Service) Status() pluginhost.Status {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return pluginhost.Status{ID: s.id, State: s.state}
}
func (*Service) RequiredServices() []pluginhost.ServiceRequirement { return nil }
func (*Service) ProvidedServices() []pluginhost.ServiceDescriptor {
	var methods []pluginhost.ServiceMethodDescriptor
	for _, method := range []string{"read", "update", "read_state", "update_state"} {
		methods = append(methods, pluginhost.ServiceMethodDescriptor{Name: method, InputSchema: ServiceName + "." + method + ".input.v1", OutputSchema: ServiceName + "." + method + ".output.v1", Cleanup: true})
	}
	return []pluginhost.ServiceDescriptor{{Name: ServiceName, Version: "1.0.0", Methods: methods}}
}
func (s *Service) Activate(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state != pluginhost.StatePrepared {
		return settingsError("service_unavailable", "settings provider cannot activate")
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
	if s.state != pluginhost.StateActive || params.Service != ServiceName {
		return nil, settingsError("service_unavailable", "settings provider is unavailable")
	}
	var result any
	var err error
	switch params.Method {
	case "read", "read_state":
		var request ReadRequest
		if err = decodeSettingsRequest(params.Params, &request); err != nil {
			return nil, err
		}
		if params.Method == "read" {
			var document Document
			document, err = Read(s.home, s.project, request.Subject, request.Scope)
			result = SettingsView{Document: document, Revision: documentRevision(document)}
		} else {
			var document StateDocument
			document, err = ReadState(s.home, s.project, request.Subject, request.Scope)
			result = StateView{Document: document, Revision: documentRevision(document)}
		}
	case "update":
		var request UpdateRequest
		if err = decodeSettingsRequest(params.Params, &request); err != nil {
			return nil, err
		}
		result, err = s.update(ctx, request)
	case "update_state":
		var request UpdateStateRequest
		if err = decodeSettingsRequest(params.Params, &request); err != nil {
			return nil, err
		}
		result, err = s.updateState(ctx, request)
	default:
		return nil, settingsError("method_not_found", "settings method is unavailable")
	}
	if err != nil {
		var hostErr *pluginhost.HostServiceError
		if errors.As(err, &hostErr) {
			return nil, hostErr
		}
		return nil, settingsError("settings_unavailable", "selected settings backend failed; a write may have been applied")
	}
	return json.Marshal(result)
}
func (s *Service) update(ctx context.Context, request UpdateRequest) (SettingsView, error) {
	document := Document{SchemaVersion: SchemaVersion, PluginID: request.Subject, Fingerprint: request.Fingerprint, Values: request.Values}
	if validateDocument(document, request.Subject) != nil {
		return SettingsView{}, settingsError("invalid_params", "invalid settings document")
	}
	path, dir, err := documentPath(s.home, s.project, request.Subject, request.Scope)
	if err != nil {
		return SettingsView{}, settingsError("invalid_params", "invalid settings subject or scope")
	}
	lock, err := storelock.Acquire(dir)
	if err != nil {
		return SettingsView{}, err
	}
	defer lock.Release()
	if err := ctx.Err(); err != nil {
		return SettingsView{}, err
	}
	current, err := readLocked(path, request.Subject)
	if err != nil {
		return SettingsView{}, err
	}
	if request.ExpectedRevision != documentRevision(current) {
		return SettingsView{}, settingsError("settings_conflict", "settings changed")
	}
	data, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return SettingsView{}, err
	}
	data = append(data, '\n')
	if len(data) > maxDocumentBytes {
		return SettingsView{}, settingsError("invalid_params", "settings document exceeds the size limit")
	}
	if err := securefs.WriteFileAtomic(path, data); err != nil {
		return SettingsView{}, err
	}
	return SettingsView{Document: document, Revision: documentRevision(document)}, nil
}
func (s *Service) updateState(ctx context.Context, request UpdateStateRequest) (StateView, error) {
	document := StateDocument{SchemaVersion: SchemaVersion, PluginID: request.Subject, Values: request.Values}
	if validateStateDocument(document, request.Subject) != nil {
		return StateView{}, settingsError("invalid_params", "invalid state document")
	}
	path, dir, err := stateDocumentPath(s.home, s.project, request.Subject, request.Scope)
	if err != nil {
		return StateView{}, settingsError("invalid_params", "invalid settings subject or scope")
	}
	lock, err := storelock.Acquire(dir)
	if err != nil {
		return StateView{}, err
	}
	defer lock.Release()
	if err := ctx.Err(); err != nil {
		return StateView{}, err
	}
	current, err := readStateDocument(path, request.Subject)
	if err != nil {
		return StateView{}, err
	}
	if request.ExpectedRevision != documentRevision(current) {
		return StateView{}, settingsError("settings_conflict", "plugin state changed")
	}
	data, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return StateView{}, err
	}
	data = append(data, '\n')
	if len(data) > maxDocumentBytes+MaxStateValueBytes {
		return StateView{}, settingsError("invalid_params", "state document exceeds the size limit")
	}
	if err := securefs.WriteFileAtomic(path, data); err != nil {
		return StateView{}, err
	}
	return StateView{Document: document, Revision: documentRevision(document)}, nil
}
func documentRevision(document any) string {
	raw, _ := json.Marshal(document)
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}
func settingsError(code, message string) error {
	return &pluginhost.HostServiceError{Code: code, Message: message}
}
func decodeSettingsRequest(raw json.RawMessage, target any) error {
	if len(raw) > 4*(maxDocumentBytes+MaxStateValueBytes) {
		return settingsError("invalid_params", "settings request exceeds the size limit")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(target) != nil || decoder.Decode(new(any)) != io.EOF {
		return settingsError("invalid_params", "settings request is invalid")
	}
	return nil
}
