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

	"github.com/blueberrycongee/wuu/internal/persistence"
	"github.com/blueberrycongee/wuu/internal/persistence/inspectionjournal"
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
	mu                           sync.RWMutex
	id, home, stateRoot, project string
	state                        pluginhost.State
	guardMu                      sync.Mutex
	guards                       map[string]bool
	guardChanged                 chan struct{}
}

func NewService(id, home, project string) *Service {
	return NewServiceWithStateRoot(id, home, home, project)
}

// NewServiceWithStateRoot keeps configuration and business storage scopes
// explicit. A selected local provider may share settings across product spaces
// without copying or exposing their isolated opaque state through that home.
func NewServiceWithStateRoot(id, home, stateRoot, project string) *Service {
	return &Service{id: id, home: home, stateRoot: stateRoot, project: project, state: pluginhost.StatePrepared, guards: make(map[string]bool), guardChanged: make(chan struct{})}
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
	for _, method := range []string{"read", "update", "read-state", "update-state", "transfer-state", "acquire-inspection", "release-inspection", "list-inspections", "reconcile-inspection", "inspection-authority"} {
		methods = append(methods, pluginhost.ServiceMethodDescriptor{Name: method, InputSchema: ServiceName + "." + method + ".input.v1", OutputSchema: ServiceName + "." + method + ".output.v1", Preparation: method == "read" || method == "read-state", Cleanup: method == "release-inspection" || method == "reconcile-inspection"})
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
func (s *Service) Close(ctx context.Context) error {
	s.mu.Lock()
	s.state = pluginhost.StateStopped
	s.mu.Unlock()
	for {
		s.guardMu.Lock()
		active := len(s.guards)
		changed := s.guardChanged
		s.guardMu.Unlock()
		if active == 0 {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-changed:
		}
	}
}

func (s *Service) InvokeService(ctx context.Context, params pluginhost.ServiceInvokeParams) (json.RawMessage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	readOnly := params.Method == "read" || params.Method == "read-state"
	if (s.state != pluginhost.StateActive && params.Method != "release-inspection" && params.Method != "reconcile-inspection" && params.Method != "list-inspections" && !(s.state == pluginhost.StatePrepared && readOnly)) || params.Service != ServiceName {
		return nil, settingsError("service_unavailable", "settings provider is unavailable")
	}
	if params.Method == "list-inspections" || params.Method == "reconcile-inspection" {
		action := "state/inspections/list"
		if params.Method == "reconcile-inspection" {
			action = "state/inspections/reconcile"
		}
		if err := persistence.ValidateInspectionAdmission(params, action); err != nil {
			return nil, err
		}
	}
	var result any
	var err error
	switch params.Method {
	case "inspection-authority":
		var request struct{}
		if err = decodeSettingsRequest(params.Params, &request); err != nil {
			return nil, err
		}
		result = persistence.InspectionTopology{Version: 1, StorageTopology: persistence.SingleHost, ExecutionTopology: persistence.SingleHost}

	case "list-inspections":
		var request struct {
			Scope Scope `json:"scope"`
		}
		if err = decodeSettingsRequest(params.Params, &request); err != nil {
			return nil, err
		}
		result, err = s.listInspections(ctx, request.Scope)
	case "reconcile-inspection":
		var request InspectionResolution
		if err = decodeSettingsRequest(params.Params, &request); err != nil {
			return nil, err
		}
		result, err = s.reconcileInspection(ctx, persistence.InspectionReconciler(params), request)

	case "acquire-inspection", "release-inspection":
		var request InspectionRequest
		if err = decodeSettingsRequest(params.Params, &request); err != nil {
			return nil, err
		}
		result, err = s.inspectState(ctx, params.Caller, request, params.Method == "release-inspection")
		if params.Method == "release-inspection" {
			result = struct{}{}
		}

	case "read", "read-state":
		var request ReadRequest
		if err = decodeSettingsRequest(params.Params, &request); err != nil {
			return nil, err
		}
		if params.Method == "read" {
			var document Document
			document, err = Read(s.home, s.project, request.Subject, request.Scope)
			result = SettingsView{Document: document, Revision: documentRevision(document)}
		} else {
			var path string
			path, _, err = stateDocumentPath(s.stateRoot, s.project, request.Subject, request.Scope)
			if err == nil {
				var record stateRecord
				record, err = readStateRecord(path, request.Subject)
				result = StateView{Document: visibleStateDocument(record), Revision: documentRevision(record)}
			}
		}
	case "update":
		var request UpdateRequest
		if err = decodeSettingsRequest(params.Params, &request); err != nil {
			return nil, err
		}
		result, err = s.update(ctx, request)
	case "update-state":
		var request UpdateStateRequest
		if err = decodeSettingsRequest(params.Params, &request); err != nil {
			return nil, err
		}
		result, err = s.updateState(ctx, request)
	case "transfer-state":
		var request StateTransfer
		if err = decodeSettingsRequest(params.Params, &request); err != nil {
			return nil, err
		}
		if err = validateStateTransfer(request); err != nil {
			return nil, settingsError("invalid_params", "invalid state ownership transfer")
		}
		err = moveStatePrefixContext(ctx, s.stateRoot, s.project, request, nil)
		result = struct{}{}
	default:
		return nil, settingsError("method_not_found", "settings method is unavailable")
	}
	if err != nil {
		if errors.Is(err, inspectionjournal.ErrActive) {
			return nil, settingsError("inspection_active", err.Error())
		}
		if errors.Is(err, inspectionjournal.ErrReleased) {
			return nil, settingsError("inspection_released", err.Error())
		}
		if errors.Is(err, inspectionjournal.ErrConflict) {
			return nil, settingsError("inspection_conflict", err.Error())
		}
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
	lock, err := storelock.AcquireContext(ctx, dir)
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
	path, dir, err := stateDocumentPath(s.stateRoot, s.project, request.Subject, request.Scope)
	if err != nil {
		return StateView{}, settingsError("invalid_params", "invalid settings subject or scope")
	}
	lock, err := storelock.AcquireContext(ctx, dir)
	if err != nil {
		return StateView{}, err
	}
	defer lock.Release()
	if err := ctx.Err(); err != nil {
		return StateView{}, err
	}
	current, err := readStateRecord(path, request.Subject)
	if err != nil {
		return StateView{}, err
	}
	if request.ExpectedRevision != documentRevision(current) {
		return StateView{}, settingsError("settings_conflict", "plugin state changed")
	}
	if err := checkStateInspection(dir, request.Subject); err != nil {
		return StateView{}, err
	}
	if err := replaceVisibleStateValues(&current, request.Values); err != nil {
		return StateView{}, settingsError("state_ownership_changed", "plugin state prefix has transferred ownership")
	}
	data, err := encodeStateRecord(current, request.Subject)
	if err != nil {
		return StateView{}, settingsError("invalid_params", "invalid state document or size limit")
	}
	if err := writeStateFile(path, data); err != nil {
		return StateView{}, err
	}
	return StateView{Document: visibleStateDocument(current), Revision: documentRevision(current)}, nil
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
