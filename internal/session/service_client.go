package session

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/blueberrycongee/wuu/internal/persistence"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
)

const ServiceName = "session.domain"
const DefaultModule = "wuu.session.default"

// Call invokes a method through one captured service binding. The composition
// owner retains that binding until all Store resources have been closed.
type Call func(context.Context, string, json.RawMessage) (json.RawMessage, error)

// Store is a transport-only session domain client. It never opens storage or
// runs the default domain implementation when a selected provider fails.
type Store struct {
	ctx  context.Context
	call Call
}

func NewClient(ctx context.Context, call Call) *Store   { return &Store{ctx: ctx, call: call} }
func (s *Store) WithContext(ctx context.Context) *Store { return NewClient(ctx, s.call) }

// NewStore explicitly assembles the default domain with supplied documents.
// Production compositions select session.domain and construct NewClient instead.
func NewStore(ctx context.Context, documents persistence.Documents) *Store {
	service := NewService("local.session.domain", documents)
	_ = service.Activate(ctx)
	return NewClient(ctx, func(ctx context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
		return service.InvokeService(ctx, pluginhost.ServiceInvokeParams{Caller: "local.session.consumer", Method: method, Params: raw})
	})
}

type invocation struct {
	Args []json.RawMessage `json:"args"`
}
type invocationResult struct {
	Values []json.RawMessage `json:"values"`
}

func (s *Store) invoke(method string, args []any, results []any) error {
	if err := s.ctx.Err(); err != nil {
		return err
	}
	if s.call == nil {
		return &pluginhost.HostServiceError{Code: "service_unavailable", Message: "session domain is not bound"}
	}
	request := invocation{Args: make([]json.RawMessage, len(args))}
	for i, arg := range args {
		raw, err := marshalDomainValue(arg)
		if err != nil {
			return err
		}
		request.Args[i] = raw
	}
	raw, err := json.Marshal(request)
	if err != nil {
		return err
	}
	raw, err = s.call(s.ctx, method, raw)
	if err != nil {
		return decodeDomainError(err)
	}
	var response invocationResult
	if err := json.Unmarshal(raw, &response); err != nil {
		return fmt.Errorf("decode %s.%s: %w", ServiceName, method, err)
	}
	if response.Values == nil || len(response.Values) != len(results) {
		return fmt.Errorf("%s.%s returned %d values; expected %d", ServiceName, method, len(response.Values), len(results))
	}
	for i, result := range results {
		if err := unmarshalDomainValue(response.Values[i], result); err != nil {
			return fmt.Errorf("decode %s.%s result %d: %w", ServiceName, method, i, err)
		}
	}
	return nil
}

var domainErrors = []struct {
	code string
	err  error
}{
	{"session_admission_changed", errAdmissionChanged}, {"session_not_found", ErrSessionNotFound}, {"session_not_archived", ErrSessionNotArchived},
	{"history_unavailable", ErrHistoryUnavailable}, {"history_snapshot_gone", ErrHistorySnapshotGone},
	{"control_changed", ErrControlChanged}, {"held_work_settled", ErrHeldUserWorkSettled}, {"held_work_unconfirmed", ErrHeldUserWorkUnconfirmed},
	{"context_seed_invalid", ErrContextSeedInvalid}, {"session_launch_conflict", ErrSessionLaunchConflict}, {"session_launch_not_found", ErrSessionLaunchNotFound},
	{"participant_not_found", ErrParticipantNotFound}, {"project_work_not_found", ErrProjectWorkNotFound}, {"project_work_changed", ErrProjectWorkChanged}, {"project_worker_capacity", ErrProjectWorkerCapacity},
	{"state_conflict", persistence.ErrConflict}, {"state_operation_conflict", persistence.ErrOperationConflict}, {"state_binding_changed", persistence.ErrBindingChanged}, {"state_migration_required", persistence.ErrMigrationRequired}, {"state_corrupt", persistence.ErrCorrupt}, {"invalid_request", persistence.ErrInvalid},
	{"request_cancelled", context.Canceled}, {"deadline_exceeded", context.DeadlineExceeded},
}

func encodeDomainError(err error) error {
	if err == nil {
		return nil
	}
	var serviceErr *pluginhost.HostServiceError
	if errors.As(err, &serviceErr) {
		return serviceErr
	}
	for _, entry := range domainErrors {
		if errors.Is(err, entry.err) {
			return &pluginhost.HostServiceError{Code: entry.code, Message: err.Error()}
		}
	}
	return &pluginhost.HostServiceError{Code: "session_operation_failed", Message: err.Error()}
}
func decodeDomainError(err error) error {
	var serviceErr *pluginhost.HostServiceError
	if errors.As(err, &serviceErr) {
		for _, entry := range domainErrors {
			if entry.code == serviceErr.Code {
				return errors.Join(entry.err, err)
			}
		}
	}
	return err
}
