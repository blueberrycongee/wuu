package pluginhost

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/composition"
)

type fakeServiceProcess struct {
	id          string
	state       State
	onStatus    func()
	provided    []ServiceDescriptor
	required    []ServiceRequirement
	invoke      func(ctx context.Context, params ServiceInvokeParams) (json.RawMessage, error)
	notified    []ServiceChangedParams
	notifyMu    sync.Mutex
	invokeCalls []ServiceInvokeParams
}

func (c *fakeServiceProcess) ID() string { return c.id }
func (c *fakeServiceProcess) Status() Status {
	if c.onStatus != nil {
		c.onStatus()
	}
	return Status{ID: c.id, State: c.state}
}
func (c *fakeServiceProcess) Close(context.Context) error {
	if c.state == StateActive {
		c.state = StateStopped
	}
	return nil
}
func (c *fakeServiceProcess) ProvidedServices() []ServiceDescriptor  { return c.provided }
func (c *fakeServiceProcess) RequiredServices() []ServiceRequirement { return c.required }
func (c *fakeServiceProcess) InvokeService(ctx context.Context, params ServiceInvokeParams) (json.RawMessage, error) {
	c.invokeCalls = append(c.invokeCalls, params)
	if c.invoke != nil {
		return c.invoke(ctx, params)
	}
	return json.RawMessage(`{"ok":true}`), nil
}
func (c *fakeServiceProcess) NotifyServiceChanged(_ context.Context, params ServiceChangedParams) error {
	c.notifyMu.Lock()
	defer c.notifyMu.Unlock()
	c.notified = append(c.notified, params)
	return nil
}

func searchService(version string) ServiceDescriptor {
	return ServiceDescriptor{
		Name:    "search.provider",
		Version: version,
		Methods: []ServiceMethodDescriptor{
			{Name: "query", InputSchema: "search.query.request.v1", OutputSchema: "search.query.response.v1"},
		},
	}
}

func TestBuildServiceRegistry(t *testing.T) {
	providerV1 := &fakeServiceProcess{id: "search-a", state: StatePrepared, provided: []ServiceDescriptor{searchService("1.0.0")}}
	providerDup := &fakeServiceProcess{id: "search-b", state: StatePrepared, provided: []ServiceDescriptor{searchService("1.1.0")}}
	providerV2 := &fakeServiceProcess{id: "search-c", state: StatePrepared, provided: []ServiceDescriptor{searchService("2.0.0")}}
	consumer := &fakeServiceProcess{id: "notes", state: StatePrepared, required: []ServiceRequirement{{Name: "search.provider", MajorVersion: 2, Required: true}}}

	registry, conflicts := BuildServiceRegistry(providerV1, providerDup, providerV2, consumer)
	if len(conflicts) != 1 || conflicts[0].PluginID != "search-b" {
		t.Fatalf("conflicts = %+v, want one conflict for search-b", conflicts)
	}
	if registry.HasProvider("search.provider", 1) || !registry.HasProvider("search.provider", 2) {
		t.Fatal("ambiguous major 1 must be unavailable; unique major 2 must remain available")
	}
	if err := registry.CheckSatisfaction(consumer.RequiredServices()); err != nil {
		t.Fatalf("CheckSatisfaction() = %v, want nil", err)
	}
}

func TestServiceRegistryConfiguredProvider(t *testing.T) {
	for _, reverse := range []bool{false, true} {
		registry, err := NewServiceRegistryWithBindings([]composition.Binding{{Service: "search.provider", Major: 1, Provider: "chosen"}})
		if err != nil {
			t.Fatal(err)
		}
		chosen := &fakeServiceProcess{id: "chosen", state: StateActive, provided: []ServiceDescriptor{searchService("1.1.0")}}
		other := &fakeServiceProcess{id: "other", state: StateActive, provided: []ServiceDescriptor{searchService("1.0.0")}}
		consumer := &fakeServiceProcess{id: "reader", state: StateActive, required: []ServiceRequirement{{Name: "search.provider", MajorVersion: 1, Required: true}}}
		clients := []Client{chosen, other, consumer}
		if reverse {
			clients[0], clients[1] = clients[1], clients[0]
		}
		if conflicts := registry.RegisterClients(clients...); len(conflicts) != 0 {
			t.Fatalf("explicit selection should resolve ambiguity: %+v", conflicts)
		}
		if err := registry.ValidateBindings(); err != nil {
			t.Fatal(err)
		}
		registry.Activate()
		call := ServiceCallParams{Service: "search.provider", Method: "query"}
		if _, err := registry.Call(context.Background(), "reader", call); err != nil {
			t.Fatal(err)
		}
		if len(chosen.invokeCalls) != 1 || len(other.invokeCalls) != 0 {
			t.Fatal("call did not use the configured provider")
		}
		snapshot := registry.Snapshot(7)
		if len(snapshot.Services) != 1 || snapshot.Services[0].BindingSource != "composition" {
			t.Fatalf("binding provenance missing: %+v", snapshot)
		}
		chosen.state = StateFailed
		if _, err := registry.Call(context.Background(), "reader", call); err == nil {
			t.Fatal("a crashed selected provider must fail the call")
		}
		registry.RevokePlugin(context.Background(), "chosen")
		if _, err := registry.Call(context.Background(), "reader", call); err == nil || len(other.invokeCalls) != 0 {
			t.Fatal("revocation must not fall back to an unselected provider")
		}
	}
}

func TestServiceRegistryMissingSelectionAndDuplicateBinding(t *testing.T) {
	binding := composition.Binding{Service: "search.provider", Major: 1, Provider: "missing"}
	if _, err := NewServiceRegistryWithBindings([]composition.Binding{binding, binding}); err == nil {
		t.Fatal("duplicate binding must fail before loading providers")
	}
	registry, err := NewServiceRegistryWithBindings([]composition.Binding{binding})
	if err != nil {
		t.Fatal(err)
	}
	registry.RegisterClients(&fakeServiceProcess{id: "other", state: StateActive, provided: []ServiceDescriptor{searchService("1.0.0")}})
	if err := registry.ValidateBindings(); err == nil || !strings.Contains(err.Error(), "missing") {
		t.Fatalf("missing selected provider diagnostic = %v", err)
	}
	if registry.HasProvider("search.provider", 1) {
		t.Fatal("another provider cannot satisfy an explicit selection")
	}
}

func TestServiceRegistryCheckSatisfaction(t *testing.T) {
	registry, _ := BuildServiceRegistry(&fakeServiceProcess{id: "search", state: StatePrepared, provided: []ServiceDescriptor{searchService("1.4.0")}})
	if err := registry.CheckSatisfaction([]ServiceRequirement{{Name: "search.provider", MajorVersion: 2, Required: true}}); err == nil || !strings.Contains(err.Error(), "provided majors") {
		t.Fatalf("version mismatch error = %v", err)
	}
	if err := registry.CheckSatisfaction([]ServiceRequirement{{Name: "memory.index", MajorVersion: 1, Required: true}}); err == nil || !strings.Contains(err.Error(), "has no provider") {
		t.Fatalf("missing service error = %v", err)
	}
	if err := registry.CheckSatisfaction([]ServiceRequirement{{Name: "memory.index", MajorVersion: 1}}); err != nil {
		t.Fatalf("optional requirement must not block: %v", err)
	}
}

func TestServiceRegistryCall(t *testing.T) {
	provider := &fakeServiceProcess{id: "search", state: StateActive, provided: []ServiceDescriptor{searchService("1.0.0")}}
	consumer := &fakeServiceProcess{id: "notes", state: StateActive, required: []ServiceRequirement{
		{Name: "search.provider", MajorVersion: 1, Required: true},
		{Name: "memory.index", MajorVersion: 1},
	}}
	stranger := &fakeServiceProcess{id: "stranger", state: StateActive}

	registry, _ := BuildServiceRegistry(provider, consumer, stranger)
	call := ServiceCallParams{Service: "search.provider", Method: "query", Params: json.RawMessage(`{"q":"x"}`)}

	if _, err := registry.Call(context.Background(), "notes", call); err == nil || err.Code != "service_unavailable" {
		t.Fatalf("inactive registry call = %v, want service_unavailable", err)
	}
	registry.Activate()

	if _, err := registry.Call(context.Background(), "stranger", call); err == nil || err.Code != "service_not_authorized" {
		t.Fatalf("undeclared consumer = %v, want service_not_authorized", err)
	}
	// A declared-but-unprovided service resolves to not_found; an undeclared
	// one stays not_authorized so consumers cannot probe what exists.
	if _, err := registry.Call(context.Background(), "notes", ServiceCallParams{Service: "memory.index", Method: "query"}); err == nil || err.Code != "service_unavailable" {
		t.Fatalf("declared unprovided service = %v, want service_unavailable", err)
	}
	if _, err := registry.Call(context.Background(), "notes", ServiceCallParams{Service: "unknown.service", Method: "query"}); err == nil || err.Code != "service_not_authorized" {
		t.Fatalf("undeclared service = %v, want service_not_authorized", err)
	}
	majorMismatch := &fakeServiceProcess{id: "notes2", state: StateActive, required: []ServiceRequirement{{Name: "search.provider", MajorVersion: 2, Required: true}}}
	registry2, _ := BuildServiceRegistry(provider, majorMismatch)
	registry2.Activate()
	if _, err := registry2.Call(context.Background(), "notes2", call); err == nil || err.Code != "service_unavailable" {
		t.Fatalf("major mismatch = %v, want service_unavailable", err)
	}
	if _, err := registry.Call(context.Background(), "notes", ServiceCallParams{Service: "search.provider", Method: "delete"}); err == nil || err.Code != "method_not_found" {
		t.Fatalf("undeclared method = %v, want method_not_found", err)
	}
	if _, err := registry.Call(context.Background(), "notes", ServiceCallParams{Service: " ", Method: "query"}); err == nil || err.Code != "invalid_request" {
		t.Fatalf("blank service = %v, want invalid_request", err)
	}

	result, err := registry.Call(context.Background(), "notes", call)
	if err != nil {
		t.Fatalf("routed call failed: %v", err)
	}
	if string(result) != `{"ok":true}` {
		t.Fatalf("result = %s", result)
	}
	if len(provider.invokeCalls) != 1 || provider.invokeCalls[0].Caller != "notes" || provider.invokeCalls[0].Method != "query" {
		t.Fatalf("invoke params = %+v", provider.invokeCalls)
	}

	// Transport cancellation ends a service invocation even while its host-owned
	// root remains live. Nested participants must not cancel their siblings.
	host := New(provider, consumer)
	host.AttachServiceRegistry(registry, nil)
	rootID := host.executions.BeginTool("notes", context.Background(), ToolExecuteInput{})
	defer host.executions.End(rootID)
	callerContext, cancelCaller := context.WithCancel(context.Background())
	defer cancelCaller()
	key := serviceKey{name: "search.provider", major: 1}
	rootContext, _, releaseRoot, rootErr := registry.acquireExecution(callerContext, rootID, "notes", key, "search", true)
	if rootErr != nil {
		t.Fatal(rootErr)
	}
	defer releaseRoot()
	nestedCaller, cancelNested := context.WithCancel(context.Background())
	defer cancelNested()
	nestedContext, _, releaseNested, nestedErr := registry.acquireExecution(nestedCaller, rootID, "search", key, "search", true)
	if nestedErr != nil {
		t.Fatal(nestedErr)
	}
	defer releaseNested()
	cancelNested()
	if _, _, release, cancelledErr := registry.acquireExecution(nestedCaller, rootID, "search", key, "search", true); cancelledErr == nil || cancelledErr.Code != "request_cancelled" {
		if release != nil {
			release()
		}
		t.Fatalf("cancelled transport admitted another invocation: %v", cancelledErr)
	}
	select {
	case <-nestedContext.Done():
	case <-time.After(time.Second):
		t.Fatal("nested caller transport loss did not cancel its service")
	}
	if rootContext.Err() != nil {
		t.Fatal("nested caller transport loss cancelled the shared chain")
	}
	cancelCaller()
	select {
	case <-rootContext.Done():
	case <-time.After(time.Second):
		t.Fatal("caller transport loss did not cancel its rooted service")
	}
	owner, ownerErr := host.ResolveExecution("notes", rootID)
	if ownerErr != nil || owner.Context.Err() != nil {
		t.Fatalf("service transport loss cancelled its independent owner: %+v, %v", owner, ownerErr)
	}
	releaseNested()
	releaseRoot()
	// Updates stop autonomous work while a retained foreground owner keeps
	// its declared services, independently of provider transport.
	host.StopBackground()
	if _, err := registry.Call(context.Background(), "notes", call); err == nil || err.Code != "generation_closed" {
		t.Fatalf("retired autonomous call = %v", err)
	}
	scopedCall := call
	scopedCall.ExecutionID = rootID
	if _, err := registry.Call(context.Background(), "notes", scopedCall); err != nil {
		t.Fatalf("retained foreground call failed: %v", err)
	}
	finishCleanup := registry.allowShutdownCleanup("notes")
	finishCleanup()
	finishReplacementCleanup := registry.allowShutdownCleanup("notes")
	defer finishReplacementCleanup()
	finishCleanup()
	if _, err := registry.Call(context.Background(), "notes", call); err != nil {
		t.Fatalf("old disposer revoked newer shutdown cleanup: %v", err)
	}
}

func TestServiceProviderCallerScope(t *testing.T) {
	provider := &fakeServiceProcess{id: "search", state: StateActive, provided: []ServiceDescriptor{searchService("1.0.0")}}
	consumer := &fakeServiceProcess{id: "notes", state: StateActive, required: []ServiceRequirement{{Name: "search.provider", MajorVersion: 1, Required: true}}}
	stranger := &fakeServiceProcess{id: "stranger", state: StateActive, required: consumer.required}
	registry, _ := BuildServiceRegistry(provider, consumer, stranger)
	host := New(provider, consumer, stranger)
	host.AttachServiceRegistry(registry, nil)
	registry.Activate()
	root := host.executions.BeginTool("notes", context.Background(), ToolExecuteInput{ThreadID: "thread", TurnID: "turn", CallID: "call", CWD: "/project"})
	defer host.executions.End(root)
	var callbackContext, ownerContext context.Context
	provider.invoke = func(ctx context.Context, params ServiceInvokeParams) (json.RawMessage, error) {
		scope, scopeErr := params.CallerExecution(true)
		if scopeErr != nil {
			t.Fatal(scopeErr)
		}
		callbackContext, ownerContext = ctx, scope.Context
		if params.ExecutionID == root {
			t.Fatal("provider callback reused the caller's execution identity")
		}
		wire, err := json.Marshal(params)
		if err != nil {
			t.Fatal(err)
		}
		var decoded struct {
			CallerScope *struct {
				ID       string `json:"id"`
				PluginID string `json:"plugin_id"`
				Kind     string `json:"kind"`
				CWD      string `json:"cwd"`
			} `json:"caller_scope"`
		}
		if err := json.Unmarshal(wire, &decoded); err != nil {
			t.Fatal(err)
		}
		if decoded.CallerScope == nil || decoded.CallerScope.ID != root || decoded.CallerScope.PluginID != "notes" || decoded.CallerScope.Kind != "tool" || decoded.CallerScope.CWD != "/project" {
			t.Fatalf("missing authenticated caller scope: %s", wire)
		}
		if _, err := host.ResolveToolExecution("search", params.ExecutionID); err == nil || err.Code != "invalid_execution_scope" {
			t.Fatalf("provider callback acquired tool authority: %v", err)
		}
		return json.RawMessage(`{}`), nil
	}
	call := ServiceCallParams{Service: "search.provider", Method: "query", ExecutionID: root}
	if _, err := registry.Call(context.Background(), "notes", call); err != nil {
		t.Fatal(err)
	}
	if callbackContext.Err() == nil || ownerContext.Err() != nil {
		t.Fatal("callback and caller did not retain separate lifetimes")
	}
	if _, err := registry.Call(context.Background(), "stranger", call); err == nil || err.Code != "service_not_authorized" {
		t.Fatalf("forged caller scope was accepted: %v", err)
	}
	if _, err := host.ResolveExecution("notes", root); err != nil {
		t.Fatalf("callback ended its caller: %v", err)
	}
	host.executions.End(root)
	if ownerContext.Err() == nil {
		t.Fatal("caller resources survived their owner")
	}
	if _, err := registry.Call(context.Background(), "notes", call); err == nil || err.Code != "execution_not_found" {
		t.Fatalf("late call reopened ended caller: %v", err)
	}
}

func TestServiceCallerResourcesEndWhenProviderStops(t *testing.T) {
	provider := &fakeServiceProcess{id: "search", state: StateActive, provided: []ServiceDescriptor{searchService("1.0.0")}}
	consumer := &fakeServiceProcess{id: "notes", state: StateActive, required: []ServiceRequirement{{Name: "search.provider", MajorVersion: 1}}}
	registry, _ := BuildServiceRegistry(provider, consumer)
	host := New(provider, consumer)
	host.AttachServiceRegistry(registry, nil)
	registry.Activate()
	root := host.executions.BeginTool("notes", context.Background(), ToolExecuteInput{})
	defer host.executions.End(root)
	var resourceContext context.Context
	provider.invoke = func(_ context.Context, params ServiceInvokeParams) (json.RawMessage, error) {
		resourceContext = params.CallerScope.Context
		return json.RawMessage(`{}`), nil
	}
	if _, err := registry.Call(context.Background(), "notes", ServiceCallParams{Service: "search.provider", Method: "query", ExecutionID: root}); err != nil {
		t.Fatal(err)
	}
	if _, retired := host.RetirePlugin(context.Background(), "search", errors.New("provider stopped")); !retired {
		t.Fatal("provider did not retire")
	}
	if resourceContext.Err() == nil {
		t.Fatal("native provider retained resources after retirement")
	}
	owner, err := host.ResolveExecution("notes", root)
	if err != nil || owner.Context.Err() != nil {
		t.Fatalf("retiring optional provider cancelled independent consumer: %+v, %v", owner, err)
	}
}

func TestServiceRegistryCallProviderFailures(t *testing.T) {
	provider := &fakeServiceProcess{id: "search", state: StateActive, provided: []ServiceDescriptor{searchService("1.0.0")}}
	consumer := &fakeServiceProcess{id: "notes", state: StateActive, required: []ServiceRequirement{{Name: "search.provider", MajorVersion: 1, Required: true}}}
	registry, _ := BuildServiceRegistry(provider, consumer)
	registry.Activate()
	call := ServiceCallParams{Service: "search.provider", Method: "query"}

	provider.state = StateFailed
	if _, err := registry.Call(context.Background(), "notes", call); err == nil || err.Code != "service_unavailable" {
		t.Fatalf("dead provider = %v, want service_unavailable", err)
	}
	provider.state = StateActive

	provider.invoke = func(context.Context, ServiceInvokeParams) (json.RawMessage, error) {
		return nil, &remoteCallError{code: "quota_exceeded", message: "quota exceeded"}
	}
	if _, err := registry.Call(context.Background(), "notes", call); err == nil || err.Code != "quota_exceeded" {
		t.Fatalf("provider error = %v, want code passthrough", err)
	}

	provider.invoke = func(context.Context, ServiceInvokeParams) (json.RawMessage, error) {
		return nil, errors.New("transport broken")
	}
	if _, err := registry.Call(context.Background(), "notes", call); err == nil || err.Code != "service_unavailable" {
		t.Fatalf("transport error = %v, want service_unavailable", err)
	}
}

func TestServiceRegistryCloseNotifiesConsumers(t *testing.T) {
	provider := &fakeServiceProcess{id: "search", state: StateActive, provided: []ServiceDescriptor{searchService("1.0.0")}}
	consumer := &fakeServiceProcess{id: "notes", state: StateActive, required: []ServiceRequirement{{Name: "search.provider", MajorVersion: 1, Required: true}}}
	unrelated := &fakeServiceProcess{id: "other", state: StateActive, required: []ServiceRequirement{{Name: "memory.index", MajorVersion: 1}}}
	registry, _ := BuildServiceRegistry(provider, consumer, unrelated)
	registry.Activate()
	registry.Close(context.Background())

	if len(consumer.notified) != 1 || consumer.notified[0].Service != "search.provider" || consumer.notified[0].Reason != "provider_closed" {
		t.Fatalf("consumer notifications = %+v", consumer.notified)
	}
	if len(unrelated.notified) != 0 {
		t.Fatalf("unrelated consumer must not be notified: %+v", unrelated.notified)
	}
	if _, err := registry.Call(context.Background(), "notes", ServiceCallParams{Service: "search.provider", Method: "query"}); err == nil || err.Code != "service_unavailable" {
		t.Fatalf("call after close = %v, want service_unavailable", err)
	}
}

func TestServiceRegistryCloseDrainsAcceptedExecutionAndAllowsNestedCalls(t *testing.T) {
	service := func(name string) ServiceDescriptor {
		return ServiceDescriptor{Name: name, Version: "1.0.0", Methods: []ServiceMethodDescriptor{{Name: "run"}}}
	}
	inner := &fakeServiceProcess{id: "inner", state: StateActive, provided: []ServiceDescriptor{service("inner")}}
	outer := &fakeServiceProcess{
		id: "outer", state: StateActive,
		provided: []ServiceDescriptor{service("outer")},
		required: []ServiceRequirement{{Name: "inner", MajorVersion: 1}},
	}
	consumer := &fakeServiceProcess{id: "caller", state: StateActive, required: []ServiceRequirement{{Name: "outer", MajorVersion: 1, Required: true}}}
	registry, conflicts := BuildServiceRegistry(outer, inner, consumer)
	if len(conflicts) != 0 {
		t.Fatalf("conflicts = %+v", conflicts)
	}
	registry.Activate()
	started := make(chan struct{})
	continueNested := make(chan struct{})
	outer.invoke = func(ctx context.Context, params ServiceInvokeParams) (json.RawMessage, error) {
		close(started)
		<-continueNested
		result, serviceErr := registry.Call(ctx, "outer", ServiceCallParams{Service: "inner", Method: "run", ExecutionID: params.ExecutionID})
		if serviceErr != nil {
			return nil, serviceErr
		}
		return result, nil
	}
	callDone := make(chan *HostServiceError, 1)
	go func() {
		_, serviceErr := registry.Call(context.Background(), "caller", ServiceCallParams{Service: "outer", Method: "run", ExecutionID: "exec-drain"})
		callDone <- serviceErr
	}()
	<-started
	closeDone := make(chan struct{})
	go func() {
		registry.Close(context.Background())
		close(closeDone)
	}()
	deadline := time.Now().Add(time.Second)
	for {
		registry.mu.RLock()
		open := registry.open
		registry.mu.RUnlock()
		if !open {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("registry did not enter draining state")
		}
		time.Sleep(time.Millisecond)
	}
	if _, serviceErr := registry.Call(context.Background(), "caller", ServiceCallParams{Service: "outer", Method: "run", ExecutionID: "exec-new"}); serviceErr == nil || serviceErr.Code != "service_unavailable" {
		t.Fatalf("new execution during drain = %v", serviceErr)
	}
	select {
	case <-closeDone:
		t.Fatal("close returned before accepted execution completed")
	default:
	}
	close(continueNested)
	if serviceErr := <-callDone; serviceErr != nil {
		t.Fatalf("accepted execution failed during drain: %v", serviceErr)
	}
	select {
	case <-closeDone:
	case <-time.After(time.Second):
		t.Fatal("close did not converge after accepted execution completed")
	}
	if len(inner.invokeCalls) != 1 || inner.invokeCalls[0].ExecutionID != "exec-drain" {
		t.Fatalf("nested calls = %+v", inner.invokeCalls)
	}
}

func TestServiceRegistryCloseCancelsExecutionAtDeadline(t *testing.T) {
	provider := &fakeServiceProcess{id: "slow", state: StateActive, provided: []ServiceDescriptor{{
		Name: "slow", Version: "1.0.0", Methods: []ServiceMethodDescriptor{{Name: "run"}},
	}}}
	started := make(chan struct{})
	cancelled := make(chan struct{})
	provider.invoke = func(ctx context.Context, _ ServiceInvokeParams) (json.RawMessage, error) {
		close(started)
		<-ctx.Done()
		close(cancelled)
		return nil, ctx.Err()
	}
	consumer := &fakeServiceProcess{id: "caller", state: StateActive, required: []ServiceRequirement{{Name: "slow", MajorVersion: 1, Required: true}}}
	registry, _ := BuildServiceRegistry(provider, consumer)
	registry.Activate()
	go registry.Call(context.Background(), "caller", ServiceCallParams{Service: "slow", Method: "run", ExecutionID: "exec-timeout"})
	<-started
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	registry.Close(ctx)
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("drain deadline did not cancel the live execution")
	}
}

func TestServiceCompletionWaitsForNestedExecutionChain(t *testing.T) {
	for _, transport := range []string{"native", "wire-context", "background"} {
		t.Run(transport, func(t *testing.T) {
			service := func(name string) ServiceDescriptor {
				return ServiceDescriptor{Name: name, Version: "1.0.0", Methods: []ServiceMethodDescriptor{{Name: "run"}}}
			}
			inner := &fakeServiceProcess{id: "inner", state: StateActive, provided: []ServiceDescriptor{service("inner")}}
			outer := &fakeServiceProcess{id: "outer", state: StateActive, provided: []ServiceDescriptor{service("outer")},
				required: []ServiceRequirement{{Name: "inner", MajorVersion: 1, Required: true}}}
			caller := &fakeServiceProcess{id: "caller", state: StateActive, required: []ServiceRequirement{{Name: "outer", MajorVersion: 1, Required: true}}}
			registry, conflicts := BuildServiceRegistry(inner, outer, caller)
			if len(conflicts) != 0 {
				t.Fatal(conflicts)
			}
			host := New(inner, outer, caller)
			host.AttachServiceRegistry(registry, conflicts)
			if err := host.Activate(context.Background()); err != nil {
				t.Fatal(err)
			}
			defer host.Close(context.Background())
			var order []string
			var completedContext context.Context
			finalize := func(name string) error {
				registry.mu.RLock()
				active := len(registry.executions)
				registry.mu.RUnlock()
				if active != 0 {
					t.Errorf("%s cleanup ran with %d service executions still active", name, active)
				}
				order = append(order, name+"-cleanup")
				return errors.New(name + " cleanup failed")
			}
			inner.invoke = func(ctx context.Context, _ ServiceInvokeParams) (json.RawMessage, error) {
				completedContext = ctx
				if !DeferServiceCompletion(ctx, func() error { return finalize("inner") }) {
					t.Fatal("inner invocation has no completion boundary")
				}
				order = append(order, "inner-return")
				return json.RawMessage(`{}`), nil
			}
			outer.invoke = func(ctx context.Context, params ServiceInvokeParams) (json.RawMessage, error) {
				if !DeferServiceCompletion(ctx, func() error { return finalize("outer") }) {
					t.Fatal("outer invocation has no completion boundary")
				}
				if transport != "native" {
					// A process sends only its authenticated execution identity.
					ctx = context.Background()
				}
				if _, err := registry.Call(ctx, outer.id, ServiceCallParams{Service: "inner", Method: "run", ExecutionID: params.ExecutionID}); err != nil {
					t.Fatalf("nested call finalized its parent: %v", err)
				}
				order = append(order, "outer-return")
				return nil, errors.New("operation failed")
			}
			var err *HostServiceError
			if transport == "background" {
				key, provider, targetErr := registry.backgroundTarget(outer.id, "outer", "run")
				if targetErr != nil {
					t.Fatal(targetErr)
				}
				_, err = registry.invokeBackground(context.Background(), outer.id, key, provider, "run", json.RawMessage(`{}`), "")
			} else {
				_, err = host.CallService(context.Background(), caller.id, "outer", "run", json.RawMessage(`{}`))
			}
			if err == nil || !strings.Contains(err.Message, "operation failed") || !strings.Contains(err.Message, "inner cleanup failed") || !strings.Contains(err.Message, "outer cleanup failed") {
				t.Fatalf("lost call or cleanup error: %v", err)
			}
			if want := []string{"inner-return", "outer-return", "inner-cleanup", "outer-cleanup"}; !reflect.DeepEqual(order, want) {
				t.Fatalf("completion order = %v, want %v", order, want)
			}
			if DeferServiceCompletion(completedContext, func() error { return nil }) {
				t.Fatal("completed invocation accepted another finalizer")
			}
		})
	}
}

func TestServiceRegistrySnapshot(t *testing.T) {
	providerZeta := &fakeServiceProcess{id: "zeta", state: StateActive, provided: []ServiceDescriptor{searchService("1.2.0")}}
	providerAlpha := &fakeServiceProcess{id: "alpha", state: StateActive, provided: []ServiceDescriptor{{
		Name: "agent.factory", Version: "2.0.0",
		Methods: []ServiceMethodDescriptor{
			{Name: "spawn", InputSchema: "agent.spawn.request.v1", OutputSchema: "agent.spawn.response.v1"},
			{Name: "list", InputSchema: "agent.list.request.v1", OutputSchema: "agent.list.response.v1"},
		},
	}}}
	registry, conflicts := BuildServiceRegistry(providerZeta, providerAlpha)
	if len(conflicts) != 0 {
		t.Fatalf("conflicts = %+v", conflicts)
	}

	snapshot := registry.Snapshot(7)
	if snapshot.Generation != 7 {
		t.Fatalf("generation = %d, want 7", snapshot.Generation)
	}
	if len(snapshot.Services) != 2 {
		t.Fatalf("services = %+v", snapshot.Services)
	}
	first, second := snapshot.Services[0], snapshot.Services[1]
	if first.Service != "agent.factory" || first.Provider != "alpha" || first.Version != "2.0.0" {
		t.Fatalf("first entry = %+v", first)
	}
	if strings.Join(first.Methods, ",") != "list,spawn" {
		t.Fatalf("methods not sorted: %+v", first.Methods)
	}
	if second.Service != "search.provider" || second.Provider != "zeta" || second.Version != "1.2.0" {
		t.Fatalf("second entry = %+v", second)
	}
	if again := registry.Snapshot(7); !reflect.DeepEqual(snapshot, again) {
		t.Fatalf("snapshot not deterministic: %+v vs %+v", snapshot, again)
	}
}

func TestServiceRegistryCallPreservesTypedProviderErrors(t *testing.T) {
	provider := &fakeServiceProcess{
		id: "slow.tool", state: StateActive, provided: []ServiceDescriptor{searchService("1.0.0")},
		invoke: func(_ context.Context, params ServiceInvokeParams) (json.RawMessage, error) {
			return nil, &HostServiceError{Code: "execution_not_found", Message: "execution exec-1 is not live"}
		},
	}
	consumer := &fakeServiceProcess{id: "notes", state: StateActive, required: []ServiceRequirement{{Name: "search.provider", MajorVersion: 1, Required: true}}}
	registry, conflicts := BuildServiceRegistry(provider, consumer)
	if len(conflicts) != 0 {
		t.Fatalf("conflicts = %+v", conflicts)
	}
	registry.Activate()
	_, err := registry.Call(context.Background(), "notes", ServiceCallParams{Service: "search.provider", Method: "query"})
	if err == nil || err.Code != "execution_not_found" || err.Message != "execution exec-1 is not live" {
		t.Fatalf("typed provider error = %v, want execution_not_found", err)
	}
}

func TestServiceRegistryNativeIdentityCannotBorrowAnotherExecution(t *testing.T) {
	provider := &fakeServiceProcess{id: "provider", state: StateActive, provided: []ServiceDescriptor{searchService("1.0.0")}}
	native := &fakeServiceProcess{id: "kernel", state: StateActive, required: []ServiceRequirement{{Name: "search.provider", MajorVersion: 1, Required: true}}}
	owner := &fakeServiceProcess{id: "owner", state: StateActive}
	host := New(provider, native, owner)
	registry, conflicts := BuildServiceRegistry(provider, native, owner)
	host.AttachServiceRegistry(registry, conflicts)
	registry.Activate()
	id := host.executions.Begin(owner.ID())
	defer host.executions.End(id)
	_, err := registry.Call(context.Background(), native.ID(), ServiceCallParams{Service: "search.provider", Method: "query", ExecutionID: id})
	if err == nil || err.Code != "service_not_authorized" {
		t.Fatalf("native identity borrowed another caller's execution: %v", err)
	}
	if len(provider.invokeCalls) != 0 {
		t.Fatal("unauthorized call reached the provider")
	}
}

func TestServiceRegistryNativeAutonomousWorkStopsWithGeneration(t *testing.T) {
	provider := &fakeServiceProcess{id: "provider", state: StateActive, provided: []ServiceDescriptor{searchService("1.0.0")}}
	native := &fakeServiceProcess{id: "kernel", state: StateActive, required: []ServiceRequirement{{Name: "search.provider", MajorVersion: 1, Required: true}}}
	host := New(provider, native)
	registry, conflicts := BuildServiceRegistry(provider, native)
	host.AttachServiceRegistry(registry, conflicts)
	registry.Activate()
	host.StopBackground()
	_, err := registry.Call(context.Background(), native.ID(), ServiceCallParams{Service: "search.provider", Method: "query"})
	if err == nil || err.Code != "generation_closed" {
		t.Fatalf("native identity started autonomous work after retirement: %v", err)
	}
}

func TestHostCallServiceUsesDeclaredConsumer(t *testing.T) {
	provider := &fakeServiceProcess{id: "driver", state: StateActive, provided: []ServiceDescriptor{{
		Name: "driver.demo", Version: "1.0.0", Methods: []ServiceMethodDescriptor{{Name: "run"}},
	}}}
	consumer := &fakeServiceProcess{id: "native-caller", state: StateActive, required: []ServiceRequirement{{Name: "driver.demo", MajorVersion: 1, Required: true}}}
	undeclared := &fakeServiceProcess{id: "kernel", state: StateActive}
	host := New(provider, consumer, undeclared)
	registry, conflicts := BuildServiceRegistry(provider, consumer, undeclared)
	host.AttachServiceRegistry(registry, conflicts)
	if _, err := host.CallService(context.Background(), consumer.ID(), "driver.demo", "run", nil); err == nil || err.Code != "service_unavailable" {
		t.Fatalf("inactive call = %v", err)
	}
	registry.Activate()
	if _, err := host.CallService(context.Background(), undeclared.ID(), "driver.demo", "run", nil); err == nil || err.Code != "service_not_authorized" {
		t.Fatalf("undeclared call = %v", err)
	}
	if _, err := host.CallService(context.Background(), consumer.ID(), "driver.demo", "create", nil); err == nil || err.Code != "method_not_found" {
		t.Fatalf("undeclared method = %v", err)
	}
	if _, err := host.CallService(context.Background(), consumer.ID(), " ", "run", nil); err == nil || err.Code != "invalid_request" {
		t.Fatalf("blank service = %v", err)
	}
	result, err := host.CallService(context.Background(), consumer.ID(), "driver.demo", "run", json.RawMessage(`{"instance_id":"i-1"}`))
	if err != nil || string(result) != `{"ok":true}` {
		t.Fatalf("native callback result = %s, error = %v", result, err)
	}
	if len(provider.invokeCalls) != 1 {
		t.Fatalf("invoke calls = %+v", provider.invokeCalls)
	}
	got := provider.invokeCalls[0]
	if got.Caller != consumer.ID() || got.CallerScope == nil || got.CallerScope.PluginID != consumer.ID() || got.CallerScope.ID == got.ExecutionID {
		t.Fatalf("native consumer and provider scopes = %+v", got)
	}
	if got.CallerScope.Context.Err() == nil {
		t.Fatal("native caller scope remained live after callback completion")
	}
	if outcome, found := host.RetirePlugin(context.Background(), consumer.ID(), nil); !found || outcome.Err != nil {
		t.Fatalf("retire native caller: %+v, found=%v", outcome, found)
	}
	if _, err := host.CallService(context.Background(), consumer.ID(), "driver.demo", "run", nil); err == nil {
		t.Fatal("retired native caller dispatched work")
	}
}

// Tool authority must originate in the native dispatch, never request JSON or
// an ordinary downstream service callback. It ends with this exact invocation.
func TestHostCallToolServiceDelegatesOnlySelectedInvocation(t *testing.T) {
	provider := &fakeServiceProcess{id: "tools", state: StateActive, provided: []ServiceDescriptor{{
		Name: "agent.tools", Version: "1.0.0", Methods: []ServiceMethodDescriptor{{Name: "execute"}},
	}}, required: []ServiceRequirement{{Name: "search.provider", MajorVersion: 1, Required: true}}}
	search := &fakeServiceProcess{id: "search", state: StateActive, provided: []ServiceDescriptor{searchService("1.0.0")}}
	consumer := &fakeServiceProcess{id: "dispatcher", state: StateActive, required: []ServiceRequirement{{Name: "agent.tools", MajorVersion: 1, Required: true}}}
	stranger := &fakeServiceProcess{id: "stranger", state: StateActive}
	host := New(provider, search, consumer, stranger)
	registry, conflicts := BuildServiceRegistry(provider, search, consumer, stranger)
	host.AttachServiceRegistry(registry, conflicts)
	registry.Activate()
	input := ToolExecuteInput{SessionID: "session", ThreadID: "thread", TurnID: "turn", ActorID: "actor", CallID: "call", Tool: "echo", CWD: "/project"}
	var callbackID string
	var callbackContext context.Context
	search.invoke = func(_ context.Context, p ServiceInvokeParams) (json.RawMessage, error) {
		if _, err := host.ResolveToolExecution(search.ID(), p.ExecutionID); err == nil || err.Code != "invalid_execution_scope" {
			t.Fatalf("ordinary downstream callback acquired tool authority: %v", err)
		}
		return json.RawMessage(`{}`), nil
	}
	provider.invoke = func(ctx context.Context, p ServiceInvokeParams) (json.RawMessage, error) {
		scope, err := host.ResolveToolExecution(provider.ID(), p.ExecutionID)
		if err != nil {
			t.Fatal(err)
		}
		callbackID, callbackContext = p.ExecutionID, ctx
		if p.Caller != consumer.ID() || p.CallerScope == nil || p.CallerScope.ID == p.ExecutionID ||
			scope.ThreadID != input.ThreadID || scope.TurnID != input.TurnID || scope.CallID != input.CallID ||
			scope.ActorID != input.ActorID || scope.CWD != input.CWD || scope.Tool != input.Tool || scope.SessionID != input.SessionID {
			t.Fatalf("dispatch identity differs from host input: %+v, caller=%+v", scope, p.CallerScope)
		}
		if _, err := host.ResolveToolExecution(stranger.ID(), p.ExecutionID); err == nil || err.Code != "service_not_authorized" {
			t.Fatalf("another provider acquired this callback: %v", err)
		}
		result, callErr := registry.Call(ctx, provider.ID(), ServiceCallParams{Service: "search.provider", Method: "query", ExecutionID: p.ExecutionID})
		if callErr != nil {
			return nil, callErr
		}
		return result, nil
	}
	if _, err := host.CallToolService(context.Background(), stranger.ID(), "agent.tools", "execute", nil, input); err == nil || err.Code != "service_not_authorized" {
		t.Fatalf("undeclared consumer dispatched a tool service: %v", err)
	}
	forged := json.RawMessage(`{"thread_id":"other","actor_id":"other","execution_id":"forged","caller_scope":{"kind":"tool"}}`)
	if _, err := host.CallToolService(context.Background(), consumer.ID(), "agent.tools", "execute", forged, input); err != nil {
		t.Fatal(err)
	}
	if callbackContext.Err() == nil || len(host.ExecutionSnapshots()) != 0 {
		t.Fatal("tool service execution outlived its invocation")
	}
	if _, err := host.ResolveToolExecution(provider.ID(), callbackID); err == nil || err.Code != "execution_not_found" {
		t.Fatalf("completed callback retained authority: %v", err)
	}
	provider.invoke = func(_ context.Context, p ServiceInvokeParams) (json.RawMessage, error) {
		if _, err := host.ResolveToolExecution(provider.ID(), p.ExecutionID); err == nil || err.Code != "invalid_execution_scope" {
			t.Fatalf("ordinary service payload forged tool authority: %v", err)
		}
		return json.RawMessage(`{}`), nil
	}
	if _, err := host.CallService(context.Background(), consumer.ID(), "agent.tools", "execute", forged); err != nil {
		t.Fatal(err)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	count := len(provider.invokeCalls)
	if _, err := host.CallToolService(cancelled, consumer.ID(), "agent.tools", "execute", nil, input); err == nil || err.Code != "request_cancelled" {
		t.Fatalf("cancelled dispatch = %v", err)
	}
	consumer.state = StateStopped
	if _, err := host.CallToolService(context.Background(), consumer.ID(), "agent.tools", "execute", nil, input); err == nil || err.Code != "service_unavailable" {
		t.Fatalf("stopped consumer dispatched: %v", err)
	}
	if len(provider.invokeCalls) != count {
		t.Fatal("cancelled or stopped consumer reached provider")
	}
}
