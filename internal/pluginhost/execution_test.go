package pluginhost

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/toolresult"
)

func TestExecutionTrackerOwnershipAndLiveness(t *testing.T) {
	tracker := NewExecutionTracker()
	first := tracker.Begin("alpha")
	second := tracker.Begin("alpha")
	if first == second || first == "" || second == "" {
		t.Fatalf("execution ids must be unique per dispatch: %q %q", first, second)
	}

	if err := tracker.RecordUpdate("alpha", ExecutionUpdateParams{ExecutionID: first, Message: "halfway", Detail: json.RawMessage(`{"pct":50}`)}); err != nil {
		t.Fatalf("record update: %v", err)
	}
	snapshot := tracker.Snapshot()
	if len(snapshot) != 2 {
		t.Fatalf("snapshot = %+v", snapshot)
	}
	if err := tracker.RecordUpdate("beta", ExecutionUpdateParams{ExecutionID: first, Message: "intruder"}); err == nil || err.Code != "service_not_authorized" {
		t.Fatalf("foreign update = %v, want service_not_authorized", err)
	}
	if err := tracker.RecordUpdate("alpha", ExecutionUpdateParams{ExecutionID: "", Message: "blank"}); err == nil || err.Code != "invalid_request" {
		t.Fatalf("blank update = %v, want invalid_request", err)
	}

	tracker.End(first)
	if err := tracker.RecordUpdate("alpha", ExecutionUpdateParams{ExecutionID: first, Message: "late"}); err == nil || err.Code != "execution_not_found" {
		t.Fatalf("late update = %v, want execution_not_found", err)
	}
	if got := len(tracker.Snapshot()); got != 1 {
		t.Fatalf("snapshot after end = %d, want 1", got)
	}
	tracker.End(first)
	tracker.End(second)
	if got := len(tracker.Snapshot()); got != 0 {
		t.Fatalf("snapshot after ending both = %d, want 0", got)
	}
}

type executionToolClient struct {
	fakeClient
	tools []ToolRegistration
	hook  func(ToolExecuteParams) (ToolExecuteResult, error)
}

func (c *executionToolClient) Tools() []ToolRegistration {
	return append([]ToolRegistration(nil), c.tools...)
}

func (c *executionToolClient) ExecuteTool(_ context.Context, params ToolExecuteParams) (ToolExecuteResult, error) {
	return c.hook(params)
}

func TestHostExecuteToolMintsScopedExecutionID(t *testing.T) {
	textResult := ToolExecuteResult{Result: toolresult.Result{
		Content: []toolresult.ContentPart{{Type: toolresult.ContentTypeText, Text: "done"}},
	}}
	var host *Host
	var seen []string
	client := &executionToolClient{
		fakeClient: fakeClient{id: "acme.lookup", status: Status{State: StateActive}},
		tools: []ToolRegistration{{
			ID:          "search",
			Description: "Search local plugin data",
			InputSchema: map[string]any{"type": "object"},
		}},
		hook: func(params ToolExecuteParams) (ToolExecuteResult, error) {
			seen = append(seen, params.ExecutionID)
			if params.ExecutionID == "" {
				t.Error("tool.execute params carry no execution_id")
			}
			if err := host.RecordExecutionUpdate("acme.lookup", ExecutionUpdateParams{ExecutionID: params.ExecutionID, Message: "working"}); err != nil {
				t.Errorf("update during execution: %v", err)
			}
			if err := host.RecordExecutionUpdate("other.plugin", ExecutionUpdateParams{ExecutionID: params.ExecutionID, Message: "intruder"}); err == nil || err.Code != "service_not_authorized" {
				t.Errorf("foreign update during execution = %v, want service_not_authorized", err)
			}
			return textResult, nil
		},
	}
	host = New(client)

	definitions := host.ToolDefinitions()
	if len(definitions) != 1 {
		t.Fatalf("definitions = %+v", definitions)
	}
	for i := 0; i < 2; i++ {
		if _, err := host.ExecuteTool(context.Background(), definitions[0].Name, ToolExecuteInput{Arguments: json.RawMessage(`{}`)}); err != nil {
			t.Fatalf("execute %d: %v", i, err)
		}
	}
	if len(seen) != 2 || seen[0] == "" || seen[0] == seen[1] {
		t.Fatalf("execution ids = %v, want two distinct non-empty ids", seen)
	}
	if got := len(host.ExecutionSnapshots()); got != 0 {
		t.Fatalf("live executions after return = %d, want 0", got)
	}
	if err := host.RecordExecutionUpdate("acme.lookup", ExecutionUpdateParams{ExecutionID: seen[0], Message: "late"}); err == nil || err.Code != "execution_not_found" {
		t.Fatalf("late update = %v, want execution_not_found", err)
	}
}

func TestHostInvokeCapabilityMintsScopedExecutionID(t *testing.T) {
	var host *Host
	var seen string
	client := &fakeCapabilityClient{
		fakeClient: &fakeClient{id: "acme.slow", status: Status{State: StateActive}},
		capabilities: []CapabilityDescriptor{{
			ID:      CapabilityAgentTurnCompleted,
			Version: 1,
		}},
		invoke: func(params CapabilityInvokeParams) (CapabilityInvokeResult, error) {
			seen = params.ExecutionID
			if params.ExecutionID == "" {
				t.Error("capability.invoke params carry no execution_id")
			}
			if err := host.RecordExecutionUpdate("acme.slow", ExecutionUpdateParams{ExecutionID: params.ExecutionID, Message: "compacting"}); err != nil {
				t.Errorf("update during capability execution: %v", err)
			}
			output, _ := json.Marshal(AgentTurnCompletedOutput{})
			return CapabilityInvokeResult{Output: output}, nil
		},
	}
	host = New(client)

	capability, ok := host.Capability("acme.slow", CapabilityAgentTurnCompleted)
	if !ok {
		t.Fatal("capability not registered")
	}
	if err := host.InvokeCapability(context.Background(), capability, AgentTurnCompletedInput{}, &AgentTurnCompletedOutput{}); err != nil {
		t.Fatalf("invoke: %v", err)
	}
	if seen == "" {
		t.Fatal("no execution id observed")
	}
	if err := host.RecordExecutionUpdate("acme.slow", ExecutionUpdateParams{ExecutionID: seen, Message: "late"}); err == nil || !strings.Contains(err.Message, "not live") {
		t.Fatalf("late update = %v, want execution_not_found", err)
	}
}

func TestExecutionTrackerToolScopeIsTrustedAndEndsWaiters(t *testing.T) {
	tracker := NewExecutionTracker()
	id := tracker.BeginTool("ask-user", context.Background(), ToolExecuteInput{
		SessionID: "session-1", ThreadID: "thread-1", TurnID: "turn-1",
		ActorID: "actor-1", CallID: "call-1", Tool: "plugin_ask_user",
	})
	scope, serviceErr := tracker.ResolveTool("ask-user", id)
	if serviceErr != nil {
		t.Fatal(serviceErr)
	}
	if scope.ThreadID != "thread-1" || scope.TurnID != "turn-1" || scope.CallID != "call-1" {
		t.Fatalf("scope = %+v", scope.ExecutionSnapshot)
	}
	if _, serviceErr := tracker.ResolveTool("other", id); serviceErr == nil || serviceErr.Code != "service_not_authorized" {
		t.Fatalf("foreign resolve = %#v", serviceErr)
	}

	tracker.End(id)
	select {
	case <-scope.Context.Done():
		if !IsUserQuestionErrorCode(context.Cause(scope.Context), "execution_cancelled") {
			t.Fatalf("scope cause = %v", context.Cause(scope.Context))
		}
	case <-time.After(time.Second):
		t.Fatal("End did not cancel the execution scope")
	}
	if _, serviceErr := tracker.ResolveTool("ask-user", id); serviceErr == nil || serviceErr.Code != "execution_not_found" {
		t.Fatalf("late resolve = %#v", serviceErr)
	}
}

func TestExecutionTrackerRejectsCapabilityAsQuestionOwner(t *testing.T) {
	tracker := NewExecutionTracker()
	id := tracker.Begin("ask-user")
	defer tracker.End(id)
	if _, serviceErr := tracker.ResolveTool("ask-user", id); serviceErr == nil || serviceErr.Code != "invalid_execution_scope" {
		t.Fatalf("capability resolve = %#v", serviceErr)
	}
}

func TestExecutionTrackerCancelAllKeepsGenerationCause(t *testing.T) {
	tracker := NewExecutionTracker()
	id := tracker.BeginTool("ask-user", context.Background(), ToolExecuteInput{ThreadID: "thread", TurnID: "turn", CallID: "call"})
	scope, serviceErr := tracker.ResolveTool("ask-user", id)
	if serviceErr != nil {
		t.Fatal(serviceErr)
	}
	tracker.CancelAll(&UserQuestionError{Code: "generation_closed", Message: "retired"})
	<-scope.Context.Done()
	if !IsUserQuestionErrorCode(context.Cause(scope.Context), "generation_closed") {
		t.Fatalf("scope cause = %v", context.Cause(scope.Context))
	}
	tracker.End(id)
}

// Both dispatch paths must expose the tracked lifetime to the RPC client.
type executionLifecycleClient struct {
	*fakeCapabilityClient
	dispatch func(context.Context, string) error
	close    func() error
	invalid  bool
}

func (c *executionLifecycleClient) Tools() []ToolRegistration {
	return []ToolRegistration{{ID: "run", Description: "Run", InputSchema: map[string]any{"type": "object"}}}
}

func (c *executionLifecycleClient) ExecuteTool(ctx context.Context, params ToolExecuteParams) (ToolExecuteResult, error) {
	if err := c.dispatch(ctx, params.ExecutionID); err != nil {
		return ToolExecuteResult{}, err
	}
	if c.invalid {
		return ToolExecuteResult{Result: toolresult.Result{Content: []toolresult.ContentPart{{Type: "invalid"}}}}, nil
	}
	return ToolExecuteResult{Result: toolresult.Result{Content: []toolresult.ContentPart{{Type: toolresult.ContentTypeText, Text: "done"}}}}, nil
}

func (c *executionLifecycleClient) InvokeCapability(ctx context.Context, params CapabilityInvokeParams) (CapabilityInvokeResult, error) {
	if err := c.dispatch(ctx, params.ExecutionID); err != nil {
		return CapabilityInvokeResult{}, err
	}
	if c.invalid {
		return CapabilityInvokeResult{Output: json.RawMessage(`{"unexpected":true}`)}, nil
	}
	return CapabilityInvokeResult{Output: json.RawMessage(`{}`)}, nil
}

func (c *executionLifecycleClient) Close(context.Context) error {
	if c.close != nil {
		return c.close()
	}
	return nil
}

func newExecutionLifecycleHost(kind string, client *executionLifecycleClient) (*Host, func(context.Context) error) {
	client.fakeCapabilityClient = &fakeCapabilityClient{
		fakeClient:   &fakeClient{id: "lifecycle", status: Status{State: StateActive}},
		capabilities: []CapabilityDescriptor{{ID: CapabilityAgentTurnCompleted, Version: 1}},
	}
	host := New(client)
	return host, func(ctx context.Context) error {
		if kind == "tool" {
			_, err := host.ExecuteTool(ctx, host.ToolDefinitions()[0].Name, ToolExecuteInput{ThreadID: "thread", TurnID: "turn", CallID: "call"})
			return err
		}
		capability, _ := host.Capability(client.ID(), CapabilityAgentTurnCompleted)
		return host.InvokeCapability(ctx, capability, AgentTurnCompletedInput{}, &AgentTurnCompletedOutput{})
	}
}

func TestHostDispatchCancellationReachesClient(t *testing.T) {
	for _, kind := range []string{"tool", "capability"} {
		for _, trigger := range []string{"cancel_all", "retire", "caller"} {
			t.Run(kind+"/"+trigger, func(t *testing.T) {
				type contextKey struct{}
				deadline := time.Now().Add(time.Minute)
				parent, release := context.WithDeadline(context.Background(), deadline)
				defer release()
				caller, cancel := context.WithCancelCause(context.WithValue(parent, contextKey{}, "caller value"))
				defer cancel(nil)
				started := make(chan context.Context, 1)
				finished := make(chan error, 1)
				client := &executionLifecycleClient{dispatch: func(ctx context.Context, _ string) error {
					started <- ctx
					<-ctx.Done()
					return context.Cause(ctx)
				}}
				host, invoke := newExecutionLifecycleHost(kind, client)
				go func() { finished <- invoke(caller) }()
				var rpcContext context.Context
				select {
				case rpcContext = <-started:
				case <-time.After(time.Second):
					t.Fatal("dispatch never reached client")
				}
				if got := rpcContext.Value(contextKey{}); got != "caller value" {
					t.Fatalf("RPC lost caller context value: %v", got)
				}
				if got, ok := rpcContext.Deadline(); !ok || !got.Equal(deadline) {
					t.Fatalf("RPC lost caller deadline: %v, %v", got, ok)
				}
				cause := errors.New("execution stopped")
				switch trigger {
				case "cancel_all":
					host.CancelExecutions(cause)
					host.CancelExecutions(errors.New("second cancellation"))
				case "retire":
					client.close = func() error {
						if !errors.Is(context.Cause(rpcContext), cause) {
							t.Error("RPC must be canceled before client shutdown")
						}
						return nil
					}
					if outcome, found := host.RetirePlugin(context.Background(), client.ID(), cause); !found || outcome.Err != nil {
						t.Fatalf("retire = (%+v, %v)", outcome, found)
					}
				case "caller":
					cancel(cause)
				}
				select {
				case err := <-finished:
					if !errors.Is(err, cause) {
						t.Fatalf("dispatch error = %v, want original cancellation cause", err)
					}
				case <-time.After(time.Second):
					t.Fatal("tracked cancellation did not stop the active RPC")
				}
				if got := host.ExecutionSnapshots(); len(got) != 0 {
					t.Fatalf("canceled execution still live: %+v", got)
				}
				if trigger != "caller" && caller.Err() != nil {
					t.Fatal("execution cancellation canceled its caller")
				}
			})
		}
	}
}

func TestHostDispatchCompletionEndsTrackedContext(t *testing.T) {
	for _, kind := range []string{"tool", "capability"} {
		for _, outcome := range []string{"success", "client_error", "invalid_result"} {
			t.Run(kind+"/"+outcome, func(t *testing.T) {
				var rpcContext context.Context
				var executionID string
				calls := 0
				clientError := errors.New("client failed")
				client := &executionLifecycleClient{invalid: outcome == "invalid_result", dispatch: func(ctx context.Context, id string) error {
					calls++
					rpcContext, executionID = ctx, id
					if outcome == "client_error" {
						return clientError
					}
					return nil
				}}
				host, invoke := newExecutionLifecycleHost(kind, client)
				err := invoke(context.Background())
				if (err == nil) != (outcome == "success") || outcome == "client_error" && !errors.Is(err, clientError) {
					t.Fatalf("dispatch error = %v for %s", err, outcome)
				}
				if calls != 1 || executionID == "" {
					t.Fatalf("dispatch calls = %d, execution ID = %q", calls, executionID)
				}
				if !IsUserQuestionErrorCode(context.Cause(rpcContext), "execution_cancelled") {
					t.Fatalf("returned RPC context still open: %v", context.Cause(rpcContext))
				}
				if got := host.ExecutionSnapshots(); len(got) != 0 {
					t.Fatalf("completed execution still live: %+v", got)
				}
				if late := host.RecordExecutionUpdate(client.ID(), ExecutionUpdateParams{ExecutionID: executionID}); late == nil || late.Code != "execution_not_found" {
					t.Fatalf("late update = %v", late)
				}
				cause := context.Cause(rpcContext)
				host.CancelExecutions(errors.New("later retirement"))
				if context.Cause(rpcContext) != cause {
					t.Fatal("completed execution terminated again")
				}
			})
		}
	}
}

func TestHostCancellationReachesResultMaterialization(t *testing.T) {
	client := &executionLifecycleClient{dispatch: func(context.Context, string) error { return nil }}
	host, invoke := newExecutionLifecycleHost("tool", client)
	started := make(chan struct{})
	host.SetToolResultMaterializer(func(ctx context.Context, scope ToolExecutionScope, result *toolresult.Result) error {
		close(started)
		select {
		case <-ctx.Done():
			return context.Cause(ctx)
		case <-time.After(time.Second):
			return errors.New("materialization did not receive execution cancellation")
		}
	})
	finished := make(chan error, 1)
	go func() { finished <- invoke(context.Background()) }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("materialization did not start")
	}
	cause := errors.New("generation retired during materialization")
	host.CancelExecutions(cause)
	if err := <-finished; !errors.Is(err, cause) {
		t.Fatalf("materialization error = %v, want cancellation cause", err)
	}
	if got := host.ExecutionSnapshots(); len(got) != 0 {
		t.Fatalf("materialized execution still live: %+v", got)
	}
}

func TestHostCloseRevokesDispatchBeforeCancelingExecutions(t *testing.T) {
	client := &executionLifecycleClient{dispatch: func(context.Context, string) error {
		t.Error("new dispatch admitted while the host was canceling executions")
		return nil
	}}
	host, _ := newExecutionLifecycleHost("tool", client)
	toolName := host.ToolDefinitions()[0].Name
	capability, _ := host.Capability(client.ID(), CapabilityAgentTurnCompleted)
	id := host.executions.Begin(client.ID())
	record := host.executions.live[id]
	cancel := record.cancel
	defer func() {
		record.cancel = cancel
		host.executions.End(id)
	}()
	cancellations := 0
	// Observe the cancellation boundary synchronously: a new RPC admitted here
	// would miss CancelAll's snapshot and could outlive generation shutdown.
	record.cancel = func(cause error) {
		cancellations++
		cancel(cause)
		if _, err := host.ExecuteTool(context.Background(), toolName, ToolExecuteInput{}); err == nil || !strings.Contains(err.Error(), "not registered") {
			t.Errorf("tool dispatch during close = %v, want admission revoked", err)
		}
		if err := host.InvokeCapability(context.Background(), capability, AgentTurnCompletedInput{}, &AgentTurnCompletedOutput{}); err == nil || !strings.Contains(err.Error(), "not active") {
			t.Errorf("capability dispatch during close = %v, want admission revoked", err)
		}
	}
	client.close = func() error {
		if cancellations != 1 || !IsUserQuestionErrorCode(context.Cause(record.ctx), "generation_closed") {
			t.Errorf("client shutdown preceded execution cancellation: count=%d cause=%v", cancellations, context.Cause(record.ctx))
		}
		return nil
	}
	outcomes := host.CloseWithOutcomes(context.Background())
	if len(outcomes) != 1 || outcomes[0].Err != nil {
		t.Fatalf("close outcomes = %+v", outcomes)
	}
}
