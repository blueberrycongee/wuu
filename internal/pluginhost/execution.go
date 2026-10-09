package pluginhost

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Execution scope vocabulary. One execution is exactly one tool.execute or
// capability.invoke dispatch: open rides the invoke frame itself (the
// execution_id param), close rides its response, and cancel is the only
// mid-flight frame. Cancel is fire-and-forget on the host side — the core's
// terminal state never waits for a plugin acknowledgement, and any response
// the plugin writes for it is discarded.
const (
	// ExecutionCancelMethod is the Host → Plugin signal translating Go
	// context cancellation of the dispatching call. Plugins translate it
	// into whatever local cancellation primitive they own; the core builds
	// no task tree on top of it.
	ExecutionCancelMethod = "execution.cancel"

	// ExecutionUpdateService is the kernel service a plugin calls to report
	// progress for an execution it owns.
	ExecutionUpdateService = "execution.update"
)

// ExecutionUpdateParams is the pluginhost-side view of one execution.update
// call. Detail carries arbitrary plugin-owned progress payload.
type ExecutionUpdateParams struct {
	ExecutionID string          `json:"execution_id"`
	Message     string          `json:"message,omitempty"`
	Detail      json.RawMessage `json:"detail,omitempty"`
}

// ExecutionCancelParams is the wire body of an execution.cancel frame.
type ExecutionCancelParams struct {
	ExecutionID string `json:"execution_id"`
}

// ExecutionSnapshot is the host's recorded view of one live execution.
type ExecutionSnapshot struct {
	ID        string          `json:"id"`
	PluginID  string          `json:"plugin_id"`
	SessionID string          `json:"session_id,omitempty"`
	ThreadID  string          `json:"thread_id,omitempty"`
	TurnID    string          `json:"turn_id,omitempty"`
	ActorID   string          `json:"actor_id,omitempty"`
	CallID    string          `json:"call_id,omitempty"`
	Tool      string          `json:"tool,omitempty"`
	CWD       string          `json:"cwd,omitempty"`
	Message   string          `json:"message,omitempty"`
	Detail    json.RawMessage `json:"detail,omitempty"`
	UpdatedAt time.Time       `json:"updated_at,omitempty"`
}

type ToolExecutionScope struct {
	ExecutionSnapshot
	Context context.Context
}

type executionRecord struct {
	snapshot *ExecutionSnapshot
	ctx      context.Context
	cancel   context.CancelCauseFunc
	tool     bool
}

// ExecutionTracker owns the live execution table for one Host. IDs are
// monotonic per host process and precise to a single dispatch, so a cancel
// can never hit a later execution in the same session. Records are removed
// when the invoke returns; a late update for a removed ID fails with
// execution_not_found and can never reopen it.
type ExecutionTracker struct {
	seq  atomic.Uint64
	mu   sync.Mutex
	live map[string]*executionRecord
}

func NewExecutionTracker() *ExecutionTracker {
	return &ExecutionTracker{live: make(map[string]*executionRecord)}
}

// Begin registers one dispatch and returns its execution ID.
func (t *ExecutionTracker) Begin(pluginID string) string {
	id, _ := t.begin(pluginID, context.Background(), ToolExecuteInput{}, false)
	return id
}

func (t *ExecutionTracker) BeginTool(pluginID string, ctx context.Context, input ToolExecuteInput) string {
	id, _ := t.begin(pluginID, ctx, input, true)
	return id
}

func (t *ExecutionTracker) begin(pluginID string, ctx context.Context, input ToolExecuteInput, tool bool) (string, context.Context) {
	if ctx == nil {
		ctx = context.Background()
	}
	id := fmt.Sprintf("exec-%d", t.seq.Add(1))
	executionCtx, cancel := context.WithCancelCause(ctx)
	t.mu.Lock()
	t.live[id] = &executionRecord{
		snapshot: &ExecutionSnapshot{
			ID: id, PluginID: pluginID, SessionID: input.SessionID, ThreadID: input.ThreadID,
			TurnID: input.TurnID, ActorID: input.ActorID, CallID: input.CallID, Tool: input.Tool, CWD: input.CWD,
		},
		ctx: executionCtx, cancel: cancel, tool: tool,
	}
	t.mu.Unlock()
	return id, executionCtx
}

// End closes the execution. It is idempotent and never blocks: the core's
// terminal state is decided by the invoke returning, not by plugin
// acknowledgement.
func (t *ExecutionTracker) End(id string) {
	t.mu.Lock()
	record := t.live[id]
	delete(t.live, id)
	t.mu.Unlock()
	if record != nil {
		record.cancel(&UserQuestionError{Code: "execution_cancelled", Message: "owning execution ended"})
	}
}

func (t *ExecutionTracker) CancelAll(cause error) {
	if cause == nil {
		cause = context.Canceled
	}
	t.mu.Lock()
	records := make([]*executionRecord, 0, len(t.live))
	for _, record := range t.live {
		records = append(records, record)
	}
	t.mu.Unlock()
	for _, record := range records {
		record.cancel(cause)
	}
}

// CancelPlugin cancels only executions owned by pluginID. Records stay live
// until their dispatch returns and calls End, so late updates cannot attach to
// a replacement plugin generation.
func (t *ExecutionTracker) CancelPlugin(pluginID string, cause error) {
	if t == nil {
		return
	}
	pluginID = strings.TrimSpace(pluginID)
	if pluginID == "" {
		return
	}
	if cause == nil {
		cause = context.Canceled
	}
	t.mu.Lock()
	var records []*executionRecord
	for _, record := range t.live {
		if record.snapshot.PluginID == pluginID {
			records = append(records, record)
		}
	}
	t.mu.Unlock()
	for _, record := range records {
		record.cancel(cause)
	}
}

func (t *ExecutionTracker) ResolveTool(callerPluginID, executionID string) (ToolExecutionScope, *HostServiceError) {
	id := strings.TrimSpace(executionID)
	if id == "" {
		return ToolExecutionScope{}, &HostServiceError{Code: "invalid_request", Message: "execution-scoped service requires execution_id"}
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	record := t.live[id]
	if record == nil {
		return ToolExecutionScope{}, &HostServiceError{Code: "execution_not_found", Message: fmt.Sprintf("execution %s is not live", id)}
	}
	if record.snapshot.PluginID != callerPluginID {
		return ToolExecutionScope{}, &HostServiceError{Code: "service_not_authorized", Message: fmt.Sprintf("execution %s belongs to plugin %q", id, record.snapshot.PluginID)}
	}
	if !record.tool || strings.TrimSpace(record.snapshot.ThreadID) == "" || strings.TrimSpace(record.snapshot.TurnID) == "" || strings.TrimSpace(record.snapshot.CallID) == "" {
		return ToolExecutionScope{}, &HostServiceError{Code: "invalid_execution_scope", Message: "service requires a live scoped tool execution"}
	}
	return ToolExecutionScope{ExecutionSnapshot: *record.snapshot, Context: record.ctx}, nil
}

// RecordUpdate validates that caller owns the live execution and stores its
// latest progress.
func (t *ExecutionTracker) RecordUpdate(callerPluginID string, params ExecutionUpdateParams) *HostServiceError {
	id := strings.TrimSpace(params.ExecutionID)
	if id == "" {
		return &HostServiceError{Code: "invalid_request", Message: "execution.update requires execution_id"}
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	record := t.live[id]
	if record == nil {
		return &HostServiceError{Code: "execution_not_found", Message: fmt.Sprintf("execution %s is not live", id)}
	}
	owner := record.snapshot.PluginID
	if owner != callerPluginID {
		return &HostServiceError{Code: "service_not_authorized", Message: fmt.Sprintf("execution %s belongs to plugin %q", id, owner)}
	}
	snapshot := record.snapshot
	snapshot.Message = params.Message
	if len(params.Detail) != 0 {
		snapshot.Detail = append(json.RawMessage(nil), params.Detail...)
	}
	snapshot.UpdatedAt = time.Now().UTC()
	return nil
}

// Snapshot returns the live executions, ordered by ID, for diagnostics.
func (t *ExecutionTracker) Snapshot() []ExecutionSnapshot {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]ExecutionSnapshot, 0, len(t.live))
	for _, record := range t.live {
		out = append(out, *record.snapshot)
	}
	return out
}
