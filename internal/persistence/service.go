package persistence

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/blueberrycongee/wuu/internal/persistence/inspectionjournal"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
)

const (
	SQLiteModule = "wuu.state.sqlite"
	JSONLModule  = "wuu.state.jsonl"
)

// Service is a native provider using the same declarations, invocation and
// lifecycle as a process provider. Preparing a composition does not create a
// backend generation or write logical records.
type Service struct {
	mu                sync.Mutex
	id, dir, kind     string
	state             pluginhost.State
	store             *Store
	prepared          preparedStore
	inspections       map[string]bool
	inspectionChanged chan struct{}
}

func NewService(id, dir, kind string) *Service {
	return &Service{id: id, dir: dir, kind: kind, state: pluginhost.StatePrepared, inspections: make(map[string]bool), inspectionChanged: make(chan struct{})}
}
func (s *Service) ID() string { return s.id }
func (s *Service) Status() pluginhost.Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	return pluginhost.Status{ID: s.id, State: s.state}
}
func (s *Service) ProvidedServices() []pluginhost.ServiceDescriptor {
	return []pluginhost.ServiceDescriptor{{Name: ServiceName, Version: "1.0.0", Methods: []pluginhost.ServiceMethodDescriptor{
		{Name: "authority", InputSchema: AuthorityInputSchema, OutputSchema: AuthorityOutputSchema, Preparation: true},
		{Name: "checkpoint", InputSchema: CheckpointInputSchema, OutputSchema: CheckpointOutputSchema, Preparation: true},
		{Name: "read", InputSchema: "state.documents.read.input.v1", OutputSchema: "state.documents.read.output.v1", Preparation: true},
		{Name: "list-inspections", InputSchema: ServiceName + ".list-inspections.input.v1", OutputSchema: ServiceName + ".list-inspections.output.v1"},
		{Name: "reconcile-inspection", InputSchema: ServiceName + ".reconcile-inspection.input.v1", OutputSchema: ServiceName + ".reconcile-inspection.output.v1", Cleanup: true},
		{Name: "claim-namespaces", InputSchema: ServiceName + ".claim-namespaces.input.v1", OutputSchema: ServiceName + ".claim-namespaces.output.v1"},
		{Name: "acquire-inspection", InputSchema: ServiceName + ".acquire-inspection.input.v1", OutputSchema: ServiceName + ".acquire-inspection.output.v1"},
		{Name: "release-inspection", InputSchema: ServiceName + ".release-inspection.input.v1", OutputSchema: ServiceName + ".release-inspection.output.v1", Cleanup: true},
		{Name: "commit", InputSchema: "state.documents.commit.input.v1", OutputSchema: "state.documents.commit.output.v1"},
		{Name: "import", InputSchema: "state.documents.import.input.v1", OutputSchema: "state.documents.import.output.v1"},
		{Name: "compact", InputSchema: "state.documents.compact.input.v1", OutputSchema: "state.documents.compact.output.v1"},
		{Name: "export", InputSchema: ExportInputSchema, OutputSchema: ExportOutputSchema, Preparation: true},
	}}}
}
func (s *Service) RequiredServices() []pluginhost.ServiceRequirement { return nil }

func (s *Service) Prepare(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.prepared.withBackend(ctx, s.dir, s.kind, func(backend, binding) error { return nil })
}
func (s *Service) Activate(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state != pluginhost.StatePrepared {
		return fmt.Errorf("state provider cannot activate from %s", s.state)
	}
	if err := s.prepared.close(); err != nil {
		s.state = pluginhost.StateFailed
		return err
	}
	store, err := Open(ctx, s.dir, s.kind)
	if err != nil {
		s.state = pluginhost.StateFailed
		return err
	}
	if err := store.retainPreparedCheckpoint(ctx, s.prepared.verified); err != nil {
		store.Close()
		s.state = pluginhost.StateFailed
		return err
	}
	s.store = store
	s.state = pluginhost.StateActive
	return nil
}
func (s *Service) Close(ctx context.Context) error {
	s.mu.Lock()
	s.state = pluginhost.StateStopped
	for len(s.inspections) > 0 {
		changed := s.inspectionChanged
		s.mu.Unlock()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-changed:
		}
		s.mu.Lock()
	}
	defer s.mu.Unlock()
	preparedErr := s.prepared.close()
	if s.store == nil {
		return preparedErr
	}
	err := errors.Join(preparedErr, s.store.Close())
	s.store = nil
	return err
}

func (s *Service) InvokeService(ctx context.Context, params pluginhost.ServiceInvokeParams) (json.RawMessage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state != pluginhost.StateActive && !((params.Method == "release-inspection" || params.Method == "reconcile-inspection" || params.Method == "list-inspections") && s.store != nil) && !(s.state == pluginhost.StatePrepared && (params.Method == "authority" || params.Method == "read" || params.Method == "export" || params.Method == "checkpoint")) {
		return nil, &pluginhost.HostServiceError{Code: "service_unavailable", Message: "state provider is not active"}
	}
	var authorizationErr error
	ctx, authorizationErr = namespaceInvocationContext(ctx, s.id, params)
	if authorizationErr != nil {
		return nil, &pluginhost.HostServiceError{Code: "permission_denied", Message: authorizationErr.Error()}
	}
	var result any
	var err error
	switch params.Method {
	case "claim-namespaces":
		var request struct {
			Namespaces []string `json:"namespaces"`
		}
		if err = decode(params.Params, &request); err == nil {
			err = s.store.ClaimNamespaces(ctx, request.Namespaces)
			result = struct{}{}
		}

	case "list-inspections":
		var request struct{}
		if err = decode(params.Params, &request); err == nil {
			result, err = s.store.ListInspections(ctx)
		}
	case "reconcile-inspection":
		var request InspectionResolution
		if err = decode(params.Params, &request); err == nil {
			if params.Caller == "" {
				err = ErrInvalid
				break
			}
			result, err = s.store.reconcileInspection(ctx, InspectionReconciler(params), request)
			if err == nil {
				delete(s.inspections, request.ID)
				close(s.inspectionChanged)
				s.inspectionChanged = make(chan struct{})
			}
		}

	case "acquire-inspection", "release-inspection":
		var request InspectionRequest
		if err = decode(params.Params, &request); err == nil {
			if params.Caller == "" {
				err = fmt.Errorf("%w: inspection caller required", ErrInvalid)
				break
			}
			if err = inspectionjournal.Validate(request.ID, request.Namespaces); err != nil {
				err = fmt.Errorf("%w: %v", ErrInvalid, err)
				break
			}
			if params.Method == "acquire-inspection" {
				existed := s.inspections[request.ID]
				s.inspections[request.ID] = true
				result, err = s.store.acquireInspection(ctx, params.Caller, request)
				if !existed && (errors.Is(err, ErrInspectionReleased) || errors.Is(err, inspectionjournal.ErrConflict) || errors.Is(err, ErrInvalid) || errors.Is(err, ErrNamespaceOwnership)) {
					delete(s.inspections, request.ID)
					close(s.inspectionChanged)
					s.inspectionChanged = make(chan struct{})
				}
			} else {
				err = s.store.releaseInspection(ctx, params.Caller, request)
				result = struct{}{}
				if err == nil {
					delete(s.inspections, request.ID)
					close(s.inspectionChanged)
					s.inspectionChanged = make(chan struct{})
				}
			}
		}

	case "authority":
		var request struct{}
		if err = decode(params.Params, &request); err == nil {
			result = LegacyAuthority()
		}
	case "checkpoint":
		var request struct{}
		if err = decode(params.Params, &request); err == nil {
			if s.state == pluginhost.StatePrepared {
				result, err = s.prepared.checkpoint(ctx, s.dir, s.kind)
			} else {
				result, err = s.store.Checkpoint(ctx)
			}
		}
	case "read":
		var query Query
		if err = decode(params.Params, &query); err == nil {
			if s.state == pluginhost.StatePrepared {
				result, err = s.prepared.view(ctx, s.dir, s.kind, query)
			} else {
				result, err = s.store.Read(ctx, query)
			}
		}
	case "commit":
		var request Transaction
		if err = decode(params.Params, &request); err == nil {
			result, err = s.store.Commit(ctx, request)
		}
	case "import":
		var request Snapshot
		if err = decode(params.Params, &request); err == nil {
			result, err = s.store.Import(ctx, request)
		}
	case "compact":
		var request struct{}
		if err = decode(params.Params, &request); err == nil {
			result, err = s.store.Compact(ctx)
		}
	case "export":
		var query *ExportQuery
		query, err = DecodeExportRequest(params.Params)
		if err == nil {
			if query != nil {
				if s.state == pluginhost.StatePrepared {
					result, err = s.prepared.exportPage(ctx, s.dir, s.kind, *query)
				} else {
					result, err = s.store.ExportPage(ctx, *query)
				}
			} else if s.state == pluginhost.StatePrepared {
				result, err = s.prepared.snapshot(ctx, s.dir, s.kind)
			} else {
				result, err = s.store.Export(ctx)
			}
		}
	default:
		return nil, &pluginhost.HostServiceError{Code: "method_not_found", Message: "unknown state provider method"}
	}
	if err != nil {
		code := "state_io_error"
		switch {
		case errors.Is(err, ErrNamespaceOwnership):
			code = "namespace_ownership"
		case errors.Is(err, ErrInspectionActive):
			code = "inspection_active"
		case errors.Is(err, ErrInspectionReleased):
			code = "inspection_released"
		case errors.Is(err, inspectionjournal.ErrConflict):
			code = "inspection_conflict"
		case errors.Is(err, ErrConflict):
			code = "state_conflict"
		case errors.Is(err, ErrOperationConflict):
			code = "state_operation_conflict"
		case errors.Is(err, ErrBindingChanged):
			code = "state_binding_changed"
		case errors.Is(err, ErrMigrationRequired):
			code = "state_migration_required"
		case errors.Is(err, ErrCorrupt):
			code = "state_corrupt"
		case errors.Is(err, ErrInvalid):
			code = "invalid_request"
		case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
			code = "request_cancelled"
		}
		return nil, &pluginhost.HostServiceError{Code: code, Message: err.Error()}
	}
	if snapshot, ok := result.(Snapshot); ok {
		return MarshalSnapshot(snapshot)
	}
	if page, ok := result.(ExportPage); ok {
		return MarshalExportPage(page)
	}
	return json.Marshal(result)
}

func decode(raw json.RawMessage, target any) error {
	if target, ok := target.(*Transaction); ok {
		decoded, err := DecodeTransaction(raw)
		if err == nil {
			*target = decoded
		}
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return fmt.Errorf("%w: expected one request object", ErrInvalid)
	}
	return nil
}
