package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/access"
	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/composition"
	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/executionauth"
	"github.com/blueberrycongee/wuu/internal/personalhost"
	pluginpkg "github.com/blueberrycongee/wuu/internal/plugin"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/principal"
	"github.com/blueberrycongee/wuu/internal/session"
	"github.com/blueberrycongee/wuu/internal/userspace"
)

type grantValidationFixture struct {
	*runtimeServiceConsumer
	revoked  bool
	calls    int
	identity principal.Identity
}

func (*grantValidationFixture) ProvidedServices() []pluginhost.ServiceDescriptor {
	return []pluginhost.ServiceDescriptor{{Name: executionauth.ServiceName, Version: "1.0.0", Methods: []pluginhost.ServiceMethodDescriptor{{Name: "validate", InputSchema: "execution.authorization.validate.input.v1", OutputSchema: "execution.authorization.validate.output.v1"}}}}
}
func (f *grantValidationFixture) InvokeService(_ context.Context, p pluginhost.ServiceInvokeParams) (json.RawMessage, error) {
	f.calls++
	var r executionauth.ValidateRequest
	if err := json.Unmarshal(p.Params, &r); err != nil {
		return nil, err
	}
	valid := !f.revoked && r.Reference.Provider == f.ID() && r.Reference.ID == "opaque" && r.Action == "personal/read" && len(r.Resources) == 1 && r.Resources[0].ID == "session-one"
	return json.Marshal(executionauth.Validation{Valid: valid, Principal: f.identity, Scope: r.Scope, ExpiresAt: time.Now().Add(time.Hour)})
}
func TestResourcePolicyValidatesSelectedExecutionProofWithoutOwner(t *testing.T) {
	provider := &grantValidationFixture{runtimeServiceConsumer: &runtimeServiceConsumer{id: "replacement.grants", state: pluginhost.StatePrepared}, identity: principal.LocalOwner().Identity()}
	registry, _ := pluginhost.NewServiceRegistryWithBindings(nil)
	policy, err := resourcePolicyProviderFactory().Build(NativeProviderContext{Registry: registry})
	if err != nil {
		t.Fatal(err)
	}
	consumer, err := resourceAccessConsumerFactory().Build(NativeProviderContext{})
	if err != nil {
		t.Fatal(err)
	}
	clients := []pluginhost.Client{provider, policy, consumer}
	conflicts := registry.RegisterClients(clients...)
	host := pluginhost.New(clients...)
	host.AttachServiceRegistry(registry, conflicts)
	defer host.Close(context.Background())
	if err := host.Activate(context.Background()); err != nil {
		t.Fatal(err)
	}
	proof := executionauth.Proof(executionauth.Reference{Provider: provider.ID(), ID: "opaque"}, executionauth.Scope{})
	request := access.Request{Principal: provider.identity, Action: "personal/read", Resources: []access.Resource{{Kind: "session", ID: "session-one"}}, Delegation: &proof}
	client := access.NewClient(boundServiceCall(host, access.ConsumerModule, access.ServiceName))
	decision, err := client.Authorize(context.Background(), request)
	if err != nil || !decision.Allow || request.Owner || provider.calls != 1 {
		t.Fatalf("selected grant not consulted without owner: %+v %v calls=%d", decision, err, provider.calls)
	}
	provider.revoked = true
	decision, err = client.Authorize(context.Background(), request)
	if err == nil && decision.Allow {
		t.Fatal("revoked proof became cached owner authority")
	}
	provider.revoked = false
	proof.Provider = "different.provider"
	request.Delegation = &proof
	decision, err = client.Authorize(context.Background(), request)
	if err == nil && decision.Allow {
		t.Fatal("grant retargeted to a replacement provider")
	}
}

// This selected task authority is independent of the native grant provider.
// The gateway observes its business registry; request JSON cannot assert scope.
type executionLineageFixture struct {
	*runtimeServiceConsumer
	revision int64
	worker   string
}

func (*executionLineageFixture) ProvidedServices() []pluginhost.ServiceDescriptor {
	return []pluginhost.ServiceDescriptor{{Name: "fixture.tasks", Version: "1.0.0", Methods: []pluginhost.ServiceMethodDescriptor{{Name: "resolve-lineage", InputSchema: "execution.lineage.resolve.input.v1", OutputSchema: "execution.lineage.resolve.output.v1"}}}}
}
func (f *executionLineageFixture) InvokeService(_ context.Context, p pluginhost.ServiceInvokeParams) (json.RawMessage, error) {
	var input struct {
		Kind, TaskID, SessionID, RequestID string
		Parent                             *executionauth.IssueResult `json:"parent"`
	}
	var wire struct {
		Kind      string                          `json:"kind"`
		TaskID    string                          `json:"task_id"`
		SessionID string                          `json:"session_id"`
		RequestID string                          `json:"request_id"`
		Parent    *executionauth.IssueResult      `json:"parent"`
		Creation  *pluginhost.SessionCreateParams `json:"creation"`
	}
	if err := json.Unmarshal(p.Params, &wire); err != nil {
		return nil, err
	}
	input.Kind, input.TaskID, input.SessionID, input.RequestID, input.Parent = wire.Kind, wire.TaskID, wire.SessionID, wire.RequestID, wire.Parent
	if input.TaskID != "task-one" {
		return nil, errors.New("unrecognized task")
	}
	out := executionauth.Lineage{TaskID: "task-one", Revision: f.revision, ContextDigest: "requirements-one", SessionID: "main-one"}
	if wire.Creation != nil {
		if input.Kind != "exact-run" || input.RequestID != "create-one" || input.Parent == nil {
			return nil, errors.New("unrecognized pending creation")
		}
		out.SessionID, out.RunID = "", "run-one"
		out.ParentTaskID, out.ParentRevision = input.Parent.Scope.TaskID, input.Parent.Scope.Revision
		return json.Marshal(struct {
			executionauth.Lineage
			Creation pluginhost.SessionCreateParams `json:"creation"`
		}{out, *wire.Creation})
	}
	if input.Kind == "exact-run" {
		if input.SessionID != f.worker || input.RequestID != "run-one" {
			return nil, errors.New("unrecognized actual run")
		}
		out.SessionID = f.worker
		out.RunID = "run-one"
	}
	if input.Parent != nil {
		out.ParentTaskID = input.Parent.Scope.TaskID
		out.ParentRevision = input.Parent.Scope.Revision
	}
	return json.Marshal(out)
}
func TestSelectedExecutionGatewayDurableDelegation(t *testing.T) {
	home := t.TempDir()
	binding := userspace.Binding{UserSpaceID: "local", ProductDomain: "personal"}
	item := serviceTestPlugin("fixture.task-provider", "plugin:fixture:tasks", "immutable-fingerprint")
	item.Root = t.TempDir()
	item.Runtime = &pluginpkg.RuntimeSpec{Command: "synthetic"}
	task := &executionLineageFixture{runtimeServiceConsumer: &runtimeServiceConsumer{id: item.ID, state: pluginhost.StatePrepared, requirements: append(pluginhost.KernelServiceRequirements(pluginhost.KernelExecutionAuthorizationService), pluginhost.ServiceRequirement{Name: "calendar.personal", MajorVersion: 1, Required: true})}, revision: 1, worker: "worker-one"}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	calendarItem := serviceTestPlugin("personal-calendar", "plugin:fixture:calendar", "calendar-fixture-fingerprint")
	calendarItem.Root, err = filepath.Abs(filepath.Join("..", "plugin", "bundled", "personal-calendar"))
	if err != nil {
		t.Fatal(err)
	}
	calendarItem.Runtime = &pluginpkg.RuntimeSpec{Command: node, Args: []string{"runtime.mjs"}, Timeout: 10}
	router := NewPluginSessionRouter()
	project := binding.Root(home)
	stateDir, err := userHostWorkspaceStateDir(home, project, binding)
	if err != nil {
		t.Fatal(err)
	}
	host, kernel, err := buildPluginHostForBinding(context.Background(), []pluginpkg.Plugin{item, calendarItem}, project, "", home, stateDir, nil, func(ctx context.Context, cfg pluginhost.ProcessConfig) (pluginhost.Client, error) {
		if cfg.ID == item.ID {
			return task, nil
		}
		return pluginhost.Start(ctx, cfg)
	}, router, nil, composition.DefaultConfig("headless", item.ID, calendarItem.ID, executionauth.ConsumerModule, executionauth.NativeModule), false, "", binding, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer host.Close(context.Background())
	if err = host.Activate(context.Background()); err != nil {
		t.Fatal(err)
	}
	core := &Session{PluginHost: host, Documents: kernel.state.documents, Sessions: kernel.state.sessions, UserSpaceID: binding.UserSpaceID, ProductDomain: binding.ProductDomain, ProviderName: "synthetic", Model: "model-one", Permissions: config.ResolvedPermissions{Mode: "standard"}, StreamRunner: &agent.StreamRunner{ProviderName: "synthetic", Model: "model-one"}}
	gen := &PluginGeneration{id: "captured-generation", state: kernel.state, host: host, active: []pluginpkg.Plugin{item, calendarItem}, models: captureGenerationModelBindings(core)}
	gen.refs.Store(1)
	core.pluginGeneration = gen
	kernel.bindStateGeneration(gen, core)
	unbind := router.BindExecutionAuthorization(core.HandleExecutionAuthorization)
	defer unbind()
	// The downstream Calendar process does not receive a borrowed Personal
	// handle. Its callbacks inherit the already validated nonowner principal.
	checksByAction := make(map[string]int)
	authorize := func(ctx context.Context, action, id string) error {
		caller, ok := principal.FromContext(ctx)
		if !ok || caller.IsOwner() {
			return errors.New("expected inherited bounded nonowner principal")
		}
		decision, err := core.AuthorizeResource(ctx, access.Request{Principal: caller.Identity(), Action: action, Resources: []access.Resource{{Kind: "user-space", ID: binding.UserSpaceID}, {Kind: "session", ID: id}}})
		if err != nil {
			return err
		}
		if !decision.Allow {
			return errors.New("selected downstream policy denied")
		}
		checksByAction[action]++
		return nil
	}
	unbindAccess := router.BindSessionResourceAccess(func(ctx context.Context, _ string, id string, ref *pluginhost.ExecutionAuthorizationReference, operation string) (context.Context, error) {
		if ref != nil {
			return ctx, errors.New("downstream consumer cannot borrow originating grant handle")
		}
		action := operation
		if !strings.HasPrefix(action, "personal/") {
			action = "user/session/" + action
		}
		return ctx, authorize(ctx, action, id)
	})
	defer unbindAccess()
	calendar := &settingsCalendarFixture{connected: true, expectedSession: "worker-one"}
	broker := personalhost.New(binding, personalhost.Options{Calendar: calendar, Authorize: func(ctx context.Context, op, id string) error { return authorize(ctx, "personal/"+op, id) }})
	defer broker.Close()
	unbindBroker := router.BindPersonalHost(broker.Call)
	defer unbindBroker()
	for _, id := range []string{"main-one", "worker-one"} {
		if _, err := core.Sessions.CreateInitialized(session.Session{ID: id, Owner: "plugin:" + item.ID, Visibility: "plugin", UserSpaceID: binding.UserSpaceID, ProductDomain: binding.ProductDomain, Provider: "synthetic", Model: "model-one", PermissionMode: "standard"}, nil); err != nil {
			t.Fatal(err)
		}
	}
	invoke := func(ctx context.Context, intent ExecutionAuthorizationIntent) (executionauth.IssueResult, error) {
		raw, _ := json.Marshal(intent)
		result, e := host.CallService(ctx, item.ID, pluginhost.KernelExecutionAuthorizationService, "call", raw)
		if e != nil {
			return executionauth.IssueResult{}, e
		}
		var grant executionauth.IssueResult
		err := json.Unmarshal(result, &grant)
		return grant, err
	}
	intent := ExecutionAuthorizationIntent{Operation: "issue", RequestID: "root-request", Kind: "task-lineage", AuthorityService: "fixture.tasks", TaskID: "task-one", SessionID: "main-one", MaxDepth: 2, ExpiresAt: time.Now().Add(time.Hour).UTC(), RequestedActions: []string{"user/session/create", "user/session/send", "personal/file.execute", "user/session/context", "service/calendar.personal/refresh", "personal/calendar.read.status", "personal/calendar.read.events", "personal/calendar.read.authorize"}}
	if _, err := invoke(context.Background(), intent); err == nil {
		t.Fatal("plugin trusted itself into owner issuance")
	}
	owner := principal.WithContext(context.Background(), principal.LocalOwner())
	parent, err := invoke(owner, intent)
	if err != nil {
		t.Fatal(err)
	}
	retry, err := invoke(owner, intent)
	if err != nil || retry.Reference != parent.Reference {
		t.Fatalf("lost-reply retry changed authority: %+v %v", retry, err)
	}
	// Publish a replacement model snapshot after admission while the selected
	// fixture storage/authority providers remain unchanged. Do not mutate the
	// original generation's model selection retained by existing invocations.
	nextModels := *gen.models
	nextModels.roles.Main.Model = "changed-default"
	nextModels.roles.Main.APIModel = "changed-default"
	next := &PluginGeneration{id: "changed-default-generation", state: kernel.state, host: host, active: gen.active, models: &nextModels}
	next.refs.Store(1)
	core.pluginGenerationMu.Lock()
	core.pluginGeneration = next
	core.pluginGenerationMu.Unlock()
	if gen.models.roles.Main.Model != "model-one" {
		t.Fatal("replacement changed an admitted model snapshot")
	}
	// The responsible conversation keeps its selected model after a global
	// default changes. Omitted child fields must consume the grant's captured
	// choice rather than retargeting or rejecting its already admitted task.
	creation := pluginhost.SessionCreateParams{RequestID: "create-one", AuthorizationTaskID: "task-one", ParentSessionID: "main-one", ContextSource: "fresh", ExecutionAuthorization: &parent.Reference}
	if _, resolved, err := core.RestoreExecutionCreation(context.Background(), item.ID, creation); err != nil || resolved.Provider != "synthetic" || resolved.Model != "model-one" || resolved.PermissionMode != "standard" {
		t.Fatalf("captured child selection drifted to changed defaults: %+v %v", resolved, err)
	}
	explicit := creation
	explicit.Provider, explicit.Model = "synthetic", "changed-default"
	if _, _, err := core.RestoreExecutionCreation(context.Background(), item.ID, explicit); err == nil {
		t.Fatal("explicit outside-bound model widened the grant")
	}
	explicit.Model = "model-one"
	if _, resolved, err := core.RestoreExecutionCreation(context.Background(), item.ID, explicit); err != nil || resolved.Model != "model-one" {
		t.Fatalf("explicit admitted model was rejected: %+v %v", resolved, err)
	}
	childIntent := intent
	childIntent.Kind = "exact-run"
	childIntent.RequestID = "run-one"
	childIntent.SessionID = "worker-one"
	childIntent.Parent = &parent.Reference
	child, err := invoke(context.Background(), childIntent)
	if err != nil {
		t.Fatalf("disconnected background derivation: %v", err)
	}
	query := json.RawMessage(`{"session_id":"worker-one","calendar_id":"primary","time_min":"2026-10-24T00:00:00Z","time_max":"2026-10-25T00:00:00Z","timezone":"UTC"}`)
	serviceIntent := ExecutionAuthorizationIntent{Operation: "call-service", Reference: &child.Reference, Service: "calendar.personal", Method: "refresh", Params: query}
	serviceCall := func(intent ExecutionAuthorizationIntent) (json.RawMessage, error) {
		raw, _ := json.Marshal(intent)
		result, err := host.CallService(context.Background(), item.ID, pluginhost.KernelExecutionAuthorizationService, "call", raw)
		if err != nil {
			return nil, err
		}
		return result, nil
	}
	agenda, err := serviceCall(serviceIntent)
	if err != nil || !strings.Contains(string(agenda), "Synthetic appointment") || calendar.reads != 1 || checksByAction["user/session/context"] == 0 || checksByAction["personal/calendar.read.events"] == 0 {
		t.Fatalf("bounded gateway → generated Calendar → selected native policy: %s %v reads=%d checks=%v", agenda, err, calendar.reads, checksByAction)
	}
	wrongMethod := serviceIntent
	wrongMethod.Method = "disconnect"
	if _, err := serviceCall(wrongMethod); err == nil {
		t.Fatal("read observation grant authorized another service method")
	}
	wrongSession := serviceIntent
	wrongSession.Params = json.RawMessage(`{"session_id":"main-one","calendar_id":"primary","time_min":"2026-10-24T00:00:00Z","time_max":"2026-10-25T00:00:00Z","timezone":"UTC"}`)
	if _, err := serviceCall(wrongSession); err == nil {
		t.Fatal("downstream selected policy allowed a foreign session")
	}
	resources := []access.Resource{{Kind: "user-space", ID: binding.UserSpaceID}, {Kind: "session", ID: "worker-one"}}
	admissionState, err := core.RetainState()
	if err != nil {
		t.Fatal(err)
	}
	turnState := admissionState.Retain()
	defer turnState.Close()
	delegated, err := core.RestoreExecutionAuthorization(WithInvocationState(context.Background(), admissionState), item.ID, child.Reference, "worker-one", "user/session/send", resources...)
	admissionState.Close()
	delegated = WithInvocationState(delegated, turnState)
	if err != nil {
		t.Fatal(err)
	}
	who, ok := principal.FromContext(delegated)
	if !ok || who.IsOwner() {
		t.Fatal("durable grant recreated owner")
	}
	if decision, err := core.AuthorizeResource(delegated, access.Request{Principal: who.Identity(), Action: "personal/file.execute", Resources: resources}); err != nil || !decision.Allow {
		t.Fatalf("selected admitted effect: %+v %v", decision, err)
	}
	// A fresh live admission may replace an expired delivery authorization
	// without changing the durable send/run identity. The issuance nonce is not
	// the business dedupe key, and background delegation cannot mint a new root.
	renewal := map[string]any{"operation": "issue", "request_id": "fresh-admission-nonce", "run_id": "run-one", "kind": "exact-run", "authority_service": "fixture.tasks", "task_id": "task-one", "session_id": "worker-one", "requested_actions": []string{"user/session/send", "user/session/context"}, "expires_at": time.Now().Add(2 * time.Hour).UTC()}
	renewalRaw, _ := json.Marshal(renewal)
	freshRaw, freshErr := host.CallService(owner, item.ID, pluginhost.KernelExecutionAuthorizationService, "call", renewalRaw)
	if freshErr != nil {
		t.Fatalf("fresh admission of same exact run: %v", freshErr)
	}
	var fresh executionauth.IssueResult
	if err := json.Unmarshal(freshRaw, &fresh); err != nil {
		t.Fatal(err)
	}
	if fresh.Scope.RunID != "run-one" || fresh.Reference == child.Reference {
		t.Fatalf("issuance nonce replaced run identity: %+v", fresh)
	}
	retryRaw, retryErr := host.CallService(owner, item.ID, pluginhost.KernelExecutionAuthorizationService, "call", renewalRaw)
	var freshRetry executionauth.IssueResult
	if retryErr != nil || json.Unmarshal(retryRaw, &freshRetry) != nil || freshRetry.Reference != fresh.Reference {
		t.Fatalf("fresh admission retry changed reference: %s %v", retryRaw, retryErr)
	}
	if _, err := host.CallService(delegated, item.ID, pluginhost.KernelExecutionAuthorizationService, "call", renewalRaw); err == nil {
		t.Fatal("delegated execution minted a fresh root grant")
	}
	// Concurrent tool calls share the verified principal, not a mutable graph
	// selection. Every check still consults current business control.
	checks := make(chan error, 8)
	for range 8 {
		go func() { checks <- who.Check(delegated) }()
	}
	for range 8 {
		if err := <-checks; err != nil {
			t.Fatalf("parallel retained execution check: %v", err)
		}
	}
	task.revision++
	if err := who.Check(delegated); err == nil {
		t.Fatal("retained invocation ignored changed task revision")
	}
	task.revision--
	closed := turnState.Retain()
	closed.Close()
	if err := who.Check(WithInvocationState(delegated, closed)); err == nil {
		t.Fatal("closed supplied capture fell back to current authority")
	}
	if _, err := core.RestoreExecutionAuthorization(context.Background(), item.ID, child.Reference, "unrelated", "user/session/send", resources...); err == nil {
		t.Fatal("grant retargeted session")
	}
	if _, err := invoke(owner, ExecutionAuthorizationIntent{Operation: "revoke", Reference: &parent.Reference}); err != nil {
		t.Fatal(err)
	}
	if err := who.Check(delegated); err == nil {
		t.Fatal("parent revocation retained child validity")
	}
	if _, err := serviceCall(serviceIntent); err == nil || calendar.reads != 1 {
		t.Fatalf("revoked observation dispatched another Calendar read: reads=%d err=%v", calendar.reads, err)
	}
	if _, err := core.RestoreExecutionAuthorization(context.Background(), item.ID, child.Reference, "worker-one", "user/session/send", resources...); err == nil {
		t.Fatal("revoked parent restarted background work")
	}
	t.Log("real selected graph: authenticated issue, stable retry, disconnected derivation, retained concurrent nonowner effects, generated Calendar downstream admission, exact-method/session denial and parent revocation passed")
}
