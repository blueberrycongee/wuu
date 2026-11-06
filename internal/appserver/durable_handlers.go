package appserver

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/blueberrycongee/wuu/internal/access"
	"github.com/blueberrycongee/wuu/internal/durablewait"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/runtime"
)

const (
	MethodDurableTasksList   = "durable/tasks/list"
	MethodDurableTasksGet    = "durable/tasks/get"
	MethodDurableWaitsList   = "durable/waits/list"
	MethodDurableWaitsDecide = "durable/waits/decide"
)

// This is a metadata projection, not a serialized plugin document. In particular
// checkpoints, prompts, arguments, outputs and freeform error text stay private.
type durableTaskView struct {
	ID            string `json:"id"`
	ParentID      string `json:"parent_id,omitempty"`
	Owner         string `json:"owner"`
	SessionID     string `json:"session_id"`
	RunID         string `json:"run_id"`
	Kind          string `json:"kind"`
	Status        string `json:"status"`
	Phase         string `json:"phase"`
	Attempt       int    `json:"attempt"`
	CreatedAt     string `json:"created_at"`
	UpdatedAt     string `json:"updated_at"`
	ErrorCode     string `json:"error_code,omitempty"`
	CheckpointRef string `json:"checkpoint_ref,omitempty"`
	TurnRef       string `json:"turn_ref,omitempty"`
	DurationMS    *int64 `json:"duration_ms,omitempty"`
	Usage         *struct {
		InputTokens  int64 `json:"input_tokens"`
		OutputTokens int64 `json:"output_tokens"`
	} `json:"usage,omitempty"`
	Wait *struct {
		RequestID string `json:"request_id"`
		Kind      string `json:"kind"`
		StartedAt string `json:"started_at"`
		ExpiresAt string `json:"expires_at,omitempty"`
		Status    string `json:"status"`
	} `json:"wait,omitempty"`
	SourceCallID   string `json:"source_call_id,omitempty"`
	SourceSeq      *int64 `json:"source_seq,omitempty"`
	ChildSessionID string `json:"child_session_id,omitempty"`
	RequestID      string `json:"request_id,omitempty"`
	Background     bool   `json:"background,omitempty"`
	Independent    bool   `json:"independent,omitempty"`
	Depth          *int   `json:"depth,omitempty"`
	ForkPoint      *int64 `json:"fork_point,omitempty"`
	WakeAt         string `json:"wake_at,omitempty"`
	Delivered      bool   `json:"delivered,omitempty"`
	Canceled       bool   `json:"canceled,omitempty"`
	ReceiptID      string `json:"receipt_id,omitempty"`
}

type durableWaitView struct {
	Version     int               `json:"version"`
	ID          string            `json:"id"`
	Kind        string            `json:"kind,omitempty"`
	Owner       durablewait.Owner `json:"owner"`
	Status      string            `json:"status"`
	EffectState string            `json:"effect_state"`
	ExpiresAt   time.Time         `json:"expires_at"`
	CreatedAt   time.Time         `json:"created_at"`
	DecidedAt   *time.Time        `json:"decided_at,omitempty"`
	BatchID     string            `json:"batch_id,omitempty"`
	CallsDigest string            `json:"calls_digest,omitempty"`
}

func (s *Server) handleDurableIngress(ctx context.Context, req Request) error {
	var tasks runtime.DurableTaskQuery
	var waits runtime.DurableWaitQuery
	taskRead := req.Method == MethodDurableTasksList || req.Method == MethodDurableTasksGet
	var target any = &waits
	if taskRead {
		target = &tasks
	}
	if err := decodeParams(req.Params, target); err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	sessionID := waits.SessionID
	if taskRead {
		sessionID = tasks.SessionID
	}
	if strings.TrimSpace(sessionID) == "" {
		return s.writeResponse(req.ID, nil, errors.New("session_id is required"))
	}
	var state *runtime.State
	var err error
	if runtime.HasInvocationState(ctx) {
		state, err = runtime.RetainProductInvocationState(ctx)
	} else {
		state, err = s.rt.RetainState()
	}
	if err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	defer state.Close()
	ctx = runtime.WithInvocationState(ctx, state)
	metadata, found, err := state.Sessions.WithContext(ctx).Find(sessionID)
	if err != nil {
		return s.writeResponse(req.ID, nil, errors.New("session metadata is unavailable"))
	}
	if !found {
		return s.writeResponse(req.ID, nil, errors.New("session not found"))
	}
	root, workspace, err := s.sessionWorkspace(metadata)
	if err != nil || !s.ownsSessionWorkspace(root, workspace) {
		return s.writeResponse(req.ID, nil, errors.New("session is outside this host"))
	}
	if req.Method == MethodDurableWaitsDecide && metadata.ArchivedAt != nil {
		return s.writeResponse(req.ID, nil, errors.New("archived sessions cannot accept decisions"))
	}
	if _, err := s.requireRequestAccess(req, req.Method, nil, access.Resource{Kind: "session", ID: sessionID}); err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	authorize := func(callCtx context.Context) error {
		if err := s.AuthorizeResource(callCtx, req.Method, access.Resource{Kind: "session", ID: sessionID}); err != nil {
			return err
		}
		if req.Method == MethodDurableWaitsDecide {
			// Scope acquisition and policy evaluation can wait. Do not accept a
			// decision for a session removed or archived while admission waited.
			current, found, err := state.Sessions.WithContext(callCtx).Find(sessionID)
			if err != nil || !found || current.ArchivedAt != nil {
				return errors.New("session cannot accept decisions")
			}
		}
		return nil
	}
	var raw json.RawMessage
	if taskRead {
		method := "list"
		if req.Method == MethodDurableTasksGet {
			method = "get"
		}
		raw, err = state.ReadDurableTasks(ctx, method, tasks, authorize)
	} else {
		method := "list"
		if req.Method == MethodDurableWaitsDecide {
			method = "decide"
		}
		raw, err = state.CallDurableWaits(ctx, method, waits, authorize)
	}
	if err != nil {
		var denied *access.Denied
		if errors.As(err, &denied) {
			return s.writeResponse(req.ID, nil, denied)
		}
		// Provider diagnostics can contain private tool inputs. Preserve typed
		// failure codes, but never forward their arbitrary message to the view.
		code := "durable_operation_failed"
		var serviceErr *pluginhost.HostServiceError
		if errors.As(err, &serviceErr) {
			code = serviceErr.Code
		}
		return s.writeJSON(Response{ID: req.ID, Error: &ResponseError{Code: code, Message: "Durable operation is unavailable or was rejected."}})
	}
	result, err := projectDurableResult(req.Method, raw, tasks, waits)
	return s.writeResponse(req.ID, result, err)
}

func projectDurableResult(method string, raw json.RawMessage, tasks runtime.DurableTaskQuery, waits runtime.DurableWaitQuery) (any, error) {
	invalid := errors.New("durable provider returned invalid or out-of-scope metadata")
	if len(raw) > 1024*1024 {
		return nil, invalid
	}
	validTask := func(task durableTaskView) bool {
		return task.ID != "" && task.SessionID == tasks.SessionID && (tasks.RunID == "" || task.RunID == tasks.RunID) && (tasks.TaskID == "" || task.ID == tasks.TaskID)
	}
	switch method {
	case MethodDurableTasksList:
		var result struct {
			Version  int               `json:"version"`
			Tasks    []durableTaskView `json:"tasks"`
			Revision string            `json:"revision"`
		}
		if json.Unmarshal(raw, &result) != nil || result.Version != 1 || result.Tasks == nil || len(result.Tasks) > 512 {
			return nil, invalid
		}
		for _, task := range result.Tasks {
			if !validTask(task) {
				return nil, invalid
			}
		}
		return result, nil
	case MethodDurableTasksGet:
		var result struct {
			Version  int              `json:"version"`
			Task     *durableTaskView `json:"task"`
			Revision string           `json:"revision"`
		}
		if json.Unmarshal(raw, &result) != nil || result.Version != 1 || (result.Task != nil && !validTask(*result.Task)) {
			return nil, invalid
		}
		return result, nil
	case MethodDurableWaitsList:
		var result struct {
			Version int               `json:"version"`
			Waits   []durableWaitView `json:"waits"`
			HasMore bool              `json:"has_more,omitempty"`
		}
		if json.Unmarshal(raw, &result) != nil || result.Waits == nil || len(result.Waits) > 512 {
			return nil, invalid
		}
		for _, wait := range result.Waits {
			if wait.ID == "" || wait.Version != 1 || wait.Owner.SessionID != waits.SessionID {
				return nil, invalid
			}
		}
		result.Version = 1
		return result, nil
	case MethodDurableWaitsDecide:
		var result durableWaitView
		if json.Unmarshal(raw, &result) != nil || result.Version != 1 || result.ID != waits.WaitID || result.Owner.SessionID != waits.SessionID {
			return nil, invalid
		}
		return result, nil
	default:
		return nil, invalid
	}
}
