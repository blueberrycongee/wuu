package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/composition"
	"github.com/blueberrycongee/wuu/internal/config"
	pluginpkg "github.com/blueberrycongee/wuu/internal/plugin"
	"github.com/blueberrycongee/wuu/internal/principal"
	"github.com/blueberrycongee/wuu/internal/session"
	"github.com/blueberrycongee/wuu/internal/userspace"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/executionauth"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
)

// These contracts protect existing sessions from target/run retargeting and
// prevent an agent-origin request from gaining human-input or whole-turn scope.
func TestExecutionSessionRouteBinding(t *testing.T) {
	binding := &executionauth.SessionRouteBinding{Locator: pluginhost.SessionLocatorV1{Version: 1, ExecutionHost: "coding-host", SessionID: "existing"}, Operations: []string{"send", "inspect", "cancel"}}
	request := pluginhost.SessionRouteRequest{Version: 1, Operation: "send", OperationID: "run", Locator: binding.Locator, Params: json.RawMessage(`{"request_id":"run","session_id":"existing","input":{"prompt":"bounded task"},"if_running":"queue"}`)}
	if err := validateExecutionSessionRoute(binding, "run", request); err != nil {
		t.Fatalf("%#v", err)
	}
	for name, mutate := range map[string]func(*pluginhost.SessionRouteRequest){
		"target": func(r *pluginhost.SessionRouteRequest) { r.Locator.SessionID = "other" },
		"host":   func(r *pluginhost.SessionRouteRequest) { r.Locator.ExecutionHost = "other" },
		"run":    func(r *pluginhost.SessionRouteRequest) { r.OperationID = "other" },
		"create": func(r *pluginhost.SessionRouteRequest) { r.Operation = "create" },
		"human": func(r *pluginhost.SessionRouteRequest) {
			r.Params = json.RawMessage(`{"request_id":"run","session_id":"existing","user_input_request_id":"human","input":{"prompt":"x"}}`)
		},
		"steer": func(r *pluginhost.SessionRouteRequest) {
			r.Params = json.RawMessage(`{"request_id":"run","session_id":"existing","if_running":"steer","input":{"prompt":"x"}}`)
		},
		"authorization": func(r *pluginhost.SessionRouteRequest) {
			r.Params = json.RawMessage(`{"request_id":"run","session_id":"existing","execution_authorization":{"provider":"p","id":"x"},"input":{"prompt":"x"}}`)
		},
	} {
		t.Run(name, func(t *testing.T) {
			changed := request
			mutate(&changed)
			if validateExecutionSessionRoute(binding, "run", changed) == nil {
				t.Fatal("route widened")
			}
		})
	}
	request.Operation = "inspect"
	request.OperationID = "inspect-run"
	request.Params = json.RawMessage(`{"session_id":"existing","request_id":"other"}`)
	if validateExecutionSessionRoute(binding, "run", request) == nil {
		t.Fatal("inspection escaped its run")
	}
	request.Operation = "cancel"
	request.Params = json.RawMessage(`{"session_id":"existing"}`)
	if validateExecutionSessionRoute(binding, "run", request) == nil {
		t.Fatal("session-wide cancellation allowed")
	}
}

type executionRouteFixture struct {
	*runtimeServiceConsumer
	calls       []pluginhost.SessionRouteRequest
	before      func()
	inspectTurn *pluginhost.SessionTurnInspection
	status      string
}

func (*executionRouteFixture) ProvidedServices() []pluginhost.ServiceDescriptor {
	return []pluginhost.ServiceDescriptor{pluginhost.SessionTransportDescriptor()}
}
func (f *executionRouteFixture) InvokeService(ctx context.Context, p pluginhost.ServiceInvokeParams) (json.RawMessage, error) {
	caller, ok := principal.FromContext(ctx)
	if !ok || caller.IsOwner() {
		return nil, errors.New("transport requires nonowner source authority")
	}
	if err := caller.Check(ctx); err != nil {
		return nil, err
	}
	var request pluginhost.SessionRouteRequest
	if err := json.Unmarshal(p.Params, &request); err != nil {
		return nil, err
	}
	f.calls = append(f.calls, request)
	var result any = pluginhost.SessionSendResult{State: "queued", SessionID: request.Locator.SessionID, QueueID: "queue-one"}
	if request.Operation == "inspect" {
		turn := f.inspectTurn
		if turn == nil {
			turn = &pluginhost.SessionTurnInspection{RequestID: "run-one", State: "queued", QueueID: "queue-one"}
		}
		result = pluginhost.SessionInspectResult{Session: pluginhost.SessionSummary{SessionID: request.Locator.SessionID}, Turn: turn}
	}
	if request.Operation == "cancel" {
		result = pluginhost.SessionCancelResult{SessionID: request.Locator.SessionID, QueueID: "queue-one", Cancelled: true}
	}
	if f.before != nil {
		f.before()
	}
	raw, _ := json.Marshal(result)
	status := f.status
	if status == "" {
		status = "completed"
	}
	return json.Marshal(pluginhost.SessionRouteResult{Version: 1, OperationID: request.OperationID, Locator: request.Locator, Status: status, Result: raw})
}

func TestSelectedExecutionRouteGateway(t *testing.T) {
	home := t.TempDir()
	binding := userspace.Binding{UserSpaceID: "local", ProductDomain: "personal"}
	item := serviceTestPlugin("fixture.task-provider", "plugin:fixture:tasks", "immutable-fingerprint")
	item.Root = t.TempDir()
	item.Runtime = &pluginpkg.RuntimeSpec{Command: "synthetic"}
	transportItem := serviceTestPlugin("fixture.transport", "plugin:fixture:transport", "transport-fingerprint")
	transportItem.Root = t.TempDir()
	transportItem.Runtime = &pluginpkg.RuntimeSpec{Command: "synthetic"}
	route := &executionauth.SessionRouteBinding{Locator: pluginhost.SessionLocatorV1{Version: 1, ExecutionHost: "registered-coding", SessionID: "existing-user-session"}, Operations: []string{"send", "inspect", "cancel"}}
	task := &executionLineageFixture{runtimeServiceConsumer: &runtimeServiceConsumer{id: item.ID, state: pluginhost.StatePrepared, requirements: append(pluginhost.KernelServiceRequirements(pluginhost.KernelExecutionAuthorizationService), pluginhost.ServiceRequirement{Name: pluginhost.SessionTransportService, MajorVersion: 1, Required: true})}, revision: 1, worker: "main-one", route: route}
	transport := &executionRouteFixture{runtimeServiceConsumer: &runtimeServiceConsumer{id: transportItem.ID, state: pluginhost.StatePrepared}}
	router := NewPluginSessionRouter()
	project := binding.Root(home)
	stateDir, err := userHostWorkspaceStateDir(home, project, binding)
	if err != nil {
		t.Fatalf("%#v", err)
	}
	host, kernel, err := buildPluginHostForBinding(context.Background(), []pluginpkg.Plugin{item, transportItem}, project, "", home, stateDir, nil, func(_ context.Context, cfg pluginhost.ProcessConfig) (pluginhost.Client, error) {
		if cfg.ID == item.ID {
			return task, nil
		}
		return transport, nil
	}, router, nil, composition.DefaultConfig("headless", item.ID, transportItem.ID, executionauth.ConsumerModule, executionauth.NativeModule), false, "", binding, nil)
	if err != nil {
		t.Fatalf("%#v", err)
	}
	defer host.Close(context.Background())
	if err = host.Activate(context.Background()); err != nil {
		t.Fatalf("%#v", err)
	}
	core := &Session{PluginHost: host, PluginSessionRouter: router, Documents: kernel.state.documents, Sessions: kernel.state.sessions, UserSpaceID: binding.UserSpaceID, ProductDomain: binding.ProductDomain, ProviderName: "synthetic", Model: "model-one", Permissions: config.ResolvedPermissions{Mode: "standard"}, StreamRunner: &agent.StreamRunner{ProviderName: "synthetic", Model: "model-one"}}
	gen := &PluginGeneration{id: "route-generation", state: kernel.state, host: host, active: []pluginpkg.Plugin{item, transportItem}, models: captureGenerationModelBindings(core)}
	gen.refs.Store(1)
	core.pluginGeneration = gen
	kernel.bindStateGeneration(gen, core)
	defer router.BindExecutionAuthorization(core.HandleExecutionAuthorization)()
	if _, err = core.Sessions.CreateInitialized(session.Session{ID: "main-one", Owner: "plugin:" + item.ID, Visibility: "plugin", UserSpaceID: binding.UserSpaceID, ProductDomain: binding.ProductDomain, Provider: "synthetic", Model: "model-one", PermissionMode: "standard"}, nil); err != nil {
		t.Fatalf("%#v", err)
	}
	invoke := func(ctx context.Context, intent ExecutionAuthorizationIntent) (json.RawMessage, error) {
		raw, _ := json.Marshal(intent)
		result, callErr := host.CallService(ctx, item.ID, pluginhost.KernelExecutionAuthorizationService, "call", raw)
		if callErr != nil {
			return nil, callErr
		}
		return result, nil
	}
	owner := principal.WithContext(context.Background(), principal.LocalOwner())
	issue := ExecutionAuthorizationIntent{Operation: "issue", RequestID: "root-route", Kind: "task-lineage", AuthorityService: "fixture.tasks", TaskID: "task-one", SessionID: "main-one", MaxDepth: 2, ExpiresAt: time.Now().Add(time.Hour).UTC(), RequestedActions: []string{"session/route/send", "session/route/inspect", "session/route/cancel", "user/session/send", "user/session/inspect"}}
	raw, err := invoke(owner, issue)
	if err != nil {
		t.Fatalf("%#v", err)
	}
	var root executionauth.IssueResult
	if err = json.Unmarshal(raw, &root); err != nil {
		t.Fatalf("%#v", err)
	}
	issue.Kind = "exact-run"
	issue.RequestID = "run-one"
	issue.Parent = &root.Reference
	raw, err = invoke(context.Background(), issue)
	if err != nil {
		t.Fatalf("%#v", err)
	}
	var run executionauth.IssueResult
	if err = json.Unmarshal(raw, &run); err != nil {
		t.Fatalf("%#v", err)
	}
	// A target-bound task still derives normal, narrowly scoped feedback grants
	// for its owned controller conversation without granting target operations.
	task.run = "feedback-one"
	feedbackIntent := issue
	feedbackIntent.RequestID = "feedback-one"
	feedbackIntent.RequestedActions = []string{"user/session/send", "user/session/inspect"}
	feedbackRaw, err := invoke(context.Background(), feedbackIntent)
	if err != nil {
		t.Fatal(err)
	}
	var feedback executionauth.IssueResult
	if err = json.Unmarshal(feedbackRaw, &feedback); err != nil {
		t.Fatal(err)
	}
	for _, action := range feedbackIntent.RequestedActions {
		if _, err = core.RestoreExecutionAuthorization(context.Background(), item.ID, feedback.Reference, "main-one", action, grantResources(feedback.Scope)...); err != nil {
			t.Fatal(err)
		}
	}
	forbidden := pluginhost.SessionRouteRequest{Version: 1, Operation: "send", OperationID: "feedback-one", Locator: route.Locator, Params: json.RawMessage(`{"request_id":"feedback-one","session_id":"existing-user-session","input":{"prompt":"not worker work"}}`)}
	if _, err = invoke(context.Background(), ExecutionAuthorizationIntent{Operation: "route-session", Reference: &feedback.Reference, Route: &forbidden}); err == nil {
		t.Fatal("feedback grant admitted target work")
	}
	task.run = "run-one"
	request := pluginhost.SessionRouteRequest{Version: 1, Operation: "send", OperationID: "run-one", Locator: route.Locator, Params: json.RawMessage(`{"request_id":"run-one","session_id":"existing-user-session","input":{"prompt":"existing work"}}`)}
	call := ExecutionAuthorizationIntent{Operation: "route-session", Reference: &run.Reference, Route: &request}
	if _, err = invoke(context.Background(), call); err != nil {
		t.Fatalf("%#v", err)
	}
	if len(transport.calls) != 1 {
		t.Fatal("send not dispatched")
	}
	transport.status = "unknown"
	unknown, err := invoke(context.Background(), call)
	if err != nil {
		t.Fatal(err)
	}
	var receipt pluginhost.SessionRouteResult
	if err = json.Unmarshal(unknown, &receipt); err != nil || receipt.Status != "unknown" || len(transport.calls) != 2 {
		t.Fatalf("unknown effect was replayed or concealed: %+v calls=%d err=%v", receipt, len(transport.calls), err)
	}
	transport.status = ""
	sendCalls := len(transport.calls)
	request.Operation = "cancel"
	request.OperationID = "cancel-one"
	request.Params = json.RawMessage(`{"session_id":"existing-user-session","queue_id":"other-request"}`)
	if _, err = invoke(context.Background(), call); err == nil {
		t.Fatal("cancel escaped its request")
	}
	if len(transport.calls) != sendCalls+1 || transport.calls[sendCalls].Operation != "inspect" {
		t.Fatal("mismatched cancellation reached effect")
	}
	request.Params = json.RawMessage(`{"session_id":"existing-user-session","queue_id":"queue-one"}`)
	if _, err = invoke(context.Background(), call); err != nil {
		t.Fatalf("%#v", err)
	}
	if len(transport.calls) != sendCalls+3 || transport.calls[sendCalls+2].Operation != "cancel" {
		t.Fatal("exact cancellation missing")
	}
	// Revocation while cancellation awaits its inspection must stop the effect.
	transport.before = func() { task.revision++ }
	before := len(transport.calls)
	if _, err = invoke(context.Background(), call); err == nil {
		t.Fatal("revision changed during cancellation preflight")
	}
	transport.before = nil
	task.revision--
	if len(transport.calls) != before+1 || transport.calls[len(transport.calls)-1].Operation != "inspect" {
		t.Fatal("stale preflight reached cancellation")
	}
	// The queue may start after the controller's observation. Reject stale queue
	// identity; a subsequent request-scoped observation can select its own turn.
	transport.inspectTurn = &pluginhost.SessionTurnInspection{RequestID: "run-one", State: "running", TurnID: "turn-one"}
	before = len(transport.calls)
	if _, err = invoke(context.Background(), call); err == nil {
		t.Fatal("stale queue cancellation was accepted")
	}
	if len(transport.calls) != before+1 {
		t.Fatal("stale queue reached cancel effect")
	}
	request.Params = json.RawMessage(`{"session_id":"existing-user-session","turn_id":"turn-one"}`)
	if _, err = invoke(context.Background(), call); err != nil {
		t.Fatal(err)
	}
	if len(transport.calls) != before+3 || transport.calls[len(transport.calls)-1].Operation != "cancel" {
		t.Fatal("running request was not cancellable by exact turn")
	}
	transport.inspectTurn = nil
	before = len(transport.calls)
	// An authority returning a changed target with the same business digest cannot
	// retarget an already issued grant: the host binds that target independently.
	task.route = &executionauth.SessionRouteBinding{Locator: pluginhost.SessionLocatorV1{Version: 1, ExecutionHost: "registered-coding", SessionID: "other"}, Operations: route.Operations}
	request.Locator = task.route.Locator
	request.Operation = "send"
	request.OperationID = "run-one"
	request.Params = json.RawMessage(`{"request_id":"run-one","session_id":"other","input":{"prompt":"x"}}`)
	if _, err = invoke(context.Background(), call); err == nil {
		t.Fatal("changed observed target reused grant")
	}
	if len(transport.calls) != before {
		t.Fatal("retargeted operation was dispatched")
	}
	task.route = route
	request.Locator = route.Locator
	request.Params = json.RawMessage(`{"request_id":"run-one","session_id":"existing-user-session","input":{"prompt":"x"}}`)
	_, err = invoke(owner, ExecutionAuthorizationIntent{Operation: "revoke", Reference: &root.Reference})
	if err != nil {
		t.Fatalf("%#v", err)
	}
	if _, err = invoke(context.Background(), call); err == nil {
		t.Fatal("revoked parent allowed route")
	}
	var refusal *pluginhost.HostServiceError
	if !errors.As(err, &refusal) || refusal.Code != "execution_authorization_required" {
		t.Fatalf("authorization refusal lost its code: %#v", err)
	}
	if len(transport.calls) != before {
		t.Fatal("revoked route was dispatched")
	}
}
