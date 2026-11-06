package durablewait

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/blueberrycongee/wuu/internal/persistence"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"sort"
	"sync"
	"time"
)

// Request carries host-derived owner information only for attested registration
// and dispatch. A public plugin cannot turn these JSON fields into authority.
type Request struct {
	Source InputRef `json:"source,omitempty"`
	Intent
	Owner     Owner  `json:"owner"`
	Fresh     bool   `json:"fresh,omitempty"`
	SessionID string `json:"session_id,omitempty"`
	WaitID    string `json:"wait_id,omitempty"`
	Decision  string `json:"decision,omitempty"`
}
type Service struct {
	mu        sync.RWMutex
	state     pluginhost.State
	store     *Store
	documents persistence.Documents
}

func NewService(documents persistence.Documents) *Service {
	return &Service{state: pluginhost.StatePrepared, store: New(documents, time.Now), documents: documents}
}
func (s *Service) ID() string { return Module }
func (s *Service) Status() pluginhost.Status {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return pluginhost.Status{ID: Module, State: s.state}
}
func (s *Service) RequiredServices() []pluginhost.ServiceRequirement {
	return []pluginhost.ServiceRequirement{{Name: persistence.ServiceName, MajorVersion: 1, Required: true}}
}
func (s *Service) ProvidedServices() []pluginhost.ServiceDescriptor {
	methods := []pluginhost.ServiceMethodDescriptor{}
	for _, method := range []string{"register", "inspect", "list", "decide", "fence", "dispatch", "resolve"} {
		methods = append(methods, pluginhost.ServiceMethodDescriptor{Name: method, InputSchema: ServiceName + "." + method + ".input.v1", OutputSchema: ServiceName + "." + method + ".output.v1"})
	}
	return []pluginhost.ServiceDescriptor{{Name: ServiceName, Version: "1.0.0", Methods: methods}}
}
func (s *Service) Activate(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := persistence.ClaimNamespaces(ctx, s.documents, []string{Namespace, GateNamespace}); err != nil {
		return err
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
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.state != pluginhost.StateActive {
		return nil, hostError("service_unavailable", errors.New("durable waits are not active"))
	}
	var request Request
	if err := json.Unmarshal(params.Params, &request); err != nil {
		return nil, hostError("invalid_request", err)
	}
	attested := params.Admission != nil && params.Admission.Version == 1 && params.Admission.Action == "durable-wait."+params.Method && params.Admission.InputJSON == string(params.Params)
	var result any
	var err error
	switch params.Method {
	case "register", "dispatch":
		if !attested {
			return nil, hostError("permission_denied", ErrOwner)
		}
		if params.Method == "dispatch" {
			err = s.store.DispatchBatch(ctx, request.Owner, request.BatchID, request.CallsDigest, request.WaitID)
			result = struct{}{}
		} else if request.BatchID != "" {
			result, err = s.store.ReserveBatch(ctx, request.Owner, request.Intent, request.Fresh)
		} else {
			result, err = s.store.Register(ctx, request.Owner, request.Intent)
		}
	case "resolve":
		if !attested {
			return nil, hostError("permission_denied", ErrOwner)
		}
		result, err = s.store.ResolveInput(ctx, request.Owner, request.WaitID, request.Source)
	case "list":
		if !attested || request.SessionID == "" {
			return nil, hostError("permission_denied", ErrOwner)
		}
		var records []Record
		records, err = s.store.List(ctx, request.SessionID)
		// Bound the view, not authoritative storage. Active waits come first;
		// truncation is explicit even when active waits exceed the view budget.
		sort.Slice(records, func(i, j int) bool {
			activeI := records[i].Status == "pending" || records[i].Status == "approved"
			activeJ := records[j].Status == "pending" || records[j].Status == "approved"
			if activeI != activeJ {
				return activeI
			}
			if records[i].CreatedAt.Equal(records[j].CreatedAt) {
				return records[i].ID < records[j].ID
			}
			return records[i].CreatedAt.After(records[j].CreatedAt)
		})
		more := len(records) > 256
		if more {
			records = records[:256]
		}
		for index := range records {
			records[index].Question = ""
		}
		result = struct {
			Waits   []Record `json:"waits"`
			HasMore bool     `json:"has_more"`
		}{records, more}
	case "inspect", "decide", "fence":
		if request.WaitID == "" {
			request.WaitID = request.ID
		}
		var record Record
		record, err = s.store.Lookup(ctx, request.WaitID)
		if err != nil {
			break
		}
		if request.SessionID == "" || record.Owner.SessionID != request.SessionID || (!attested && record.Owner.PluginID != params.Caller) {
			return nil, hostError("permission_denied", ErrOwner)
		}
		switch params.Method {
		case "inspect":
			result = record
		case "decide":
			if !attested || (params.Admission.Principal.Kind != "user" && params.Admission.Principal.Kind != "device") || (record.Kind != "approval" && !(record.Kind == "" && record.BatchID != "")) {
				return nil, hostError("permission_denied", ErrOwner)
			}
			result, err = s.store.Decide(ctx, record.Owner, record.ID, request.Decision)
		case "fence":
			result, err = s.store.Fence(ctx, record.Owner, record.ID)
		}
	default:
		return nil, hostError("method_not_found", ErrInvalid)
	}
	if err != nil {
		return nil, serviceError(err)
	}
	return json.Marshal(result)
}
func hostError(code string, err error) error {
	return &pluginhost.HostServiceError{Code: code, Message: err.Error()}
}
func serviceError(err error) error {
	code := "wait_error"
	switch {
	case errors.Is(err, ErrOwner):
		code = "permission_denied"
	case errors.Is(err, ErrInvalid):
		code = "invalid_request"
	case errors.Is(err, ErrConflict):
		code = "wait_conflict"
	case errors.Is(err, ErrExpired):
		code = "wait_expired"
	case errors.Is(err, ErrUnknown):
		code = "uncertain_tool_effect"
	case errors.Is(err, ErrNotFound):
		code = "wait_not_found"
	}
	return hostError(code, err)
}
