package persistence

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"

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
	mu            sync.Mutex
	id, dir, kind string
	state         pluginhost.State
	store         *Store
	prepared      preparedStore
}

func NewService(id, dir, kind string) *Service {
	return &Service{id: id, dir: dir, kind: kind, state: pluginhost.StatePrepared}
}
func (s *Service) ID() string { return s.id }
func (s *Service) Status() pluginhost.Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	return pluginhost.Status{ID: s.id, State: s.state}
}
func (s *Service) ProvidedServices() []pluginhost.ServiceDescriptor {
	return []pluginhost.ServiceDescriptor{{Name: ServiceName, Version: "1.0.0", Methods: []pluginhost.ServiceMethodDescriptor{
		{Name: "read", InputSchema: "state.documents.read.input.v1", OutputSchema: "state.documents.read.output.v1", Preparation: true},
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
	s.store = store
	s.state = pluginhost.StateActive
	return nil
}
func (s *Service) Close(context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state = pluginhost.StateStopped
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
	if s.state != pluginhost.StateActive && !(s.state == pluginhost.StatePrepared && (params.Method == "read" || params.Method == "export")) {
		return nil, &pluginhost.HostServiceError{Code: "service_unavailable", Message: "state provider is not active"}
	}
	var result any
	var err error
	switch params.Method {
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
