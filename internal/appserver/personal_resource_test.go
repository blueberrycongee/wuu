package appserver

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/access"
	"github.com/blueberrycongee/wuu/internal/personalreview"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/principal"
	"github.com/blueberrycongee/wuu/internal/runtime"
)

// A declared plugin dependency identifies code, not the person authorized to
// inspect or mutate a private user host. Every domain entry needs both.
func TestPersonalSessionOperationsRequireAuthenticatedResource(t *testing.T) {
	f := newPersonalBoundaryFixture(t, nil)
	server := newOwnerHandlerFixture(f.rt, &lockedBuffer{})
	binding, err := f.rt.RetainState()
	if err != nil {
		t.Fatal(err)
	}
	defer binding.Close()
	unauthenticated := runtime.WithInvocationState(context.Background(), binding)
	cases := map[string]func(context.Context) error{
		"control": func(ctx context.Context) error {
			_, err := server.controlPluginSession(binding, ctx, "personal", pluginhost.SessionControlParams{SessionID: f.id})
			return err
		},
		"process-session": func(ctx context.Context) error {
			_, _, err := server.resolvePluginProcessSession(ctx, f.id, false)
			return err
		},
		"create": func(ctx context.Context) error {
			_, err := server.createPluginSession(ctx, "personal", pluginhost.SessionCreateParams{})
			return err
		},
		"list": func(ctx context.Context) error {
			_, err := server.listPluginSessions(ctx, "personal", pluginhost.SessionListParams{})
			return err
		},
		"inspect": func(ctx context.Context) error {
			_, err := server.inspectPluginSession(ctx, "personal", pluginhost.SessionInspectParams{SessionID: f.id})
			return err
		},
		"send": func(ctx context.Context) error {
			_, err := server.sendPluginSession(ctx, "personal", pluginhost.SessionSendParams{SessionID: f.id})
			return err
		},
		"cancel": func(ctx context.Context) error {
			_, err := server.cancelPluginSession(ctx, "personal", pluginhost.SessionCancelParams{SessionID: f.id})
			return err
		},
	}
	for name, call := range cases {
		t.Run(name, func(t *testing.T) {
			if err := call(unauthenticated); err == nil || !strings.Contains(err.Error(), "authenticated") {
				t.Fatalf("missing transport principal reached operation: %v", err)
			}
		})
	}
	server.access.policy = access.PolicyFunc(func(_ context.Context, req access.Request) (access.Decision, error) {
		return access.Decision{Allow: req.Action != "user/session/inspect"}, nil
	})
	if err := cases["inspect"](runtime.WithInvocationState(f.owner, binding)); err == nil {
		t.Fatal("selected resource denial ignored")
	}
}

func TestPersonalNestedResponseRechecksSessionResource(t *testing.T) {
	f := newPersonalBoundaryFixture(t, nil)
	server := newOwnerHandlerFixture(f.rt, &lockedBuffer{})
	allowed := true
	server.access.policy = access.PolicyFunc(func(_ context.Context, req access.Request) (access.Decision, error) {
		for _, resource := range req.Resources {
			if resource.Kind == "session" && resource.ID == f.id && !allowed {
				return access.Decision{Allow: false}, nil
			}
		}
		return access.Decision{Allow: true}, nil
	})
	req := Request{ID: json.RawMessage(`"nested"`), Method: MethodPluginClientRequest, Params: mustJSON(PluginClientRequestParams{ID: "fixture-personal", Fingerprint: "fixture-fingerprint", GraphGeneration: f.rt.ExtensionSnapshot().GenerationID, Method: "review.execution.status"})}
	if err := server.trackAuthorizedRequest(f.owner, req); err != nil {
		t.Fatal(err)
	}
	ctx := server.captureAuthorizedResponse(f.owner, req)
	if err := server.authorizePersonalOperation(ctx, "review.execution.status", f.id); err != nil {
		t.Fatal(err)
	}
	server.access.mu.Lock()
	pending := server.access.pending[string(req.ID)]
	server.access.mu.Unlock()
	if len(pending.extra) == 0 {
		t.Fatal("nested Personal scope absent from transport replay guard")
	}
	allowed = false
	for _, scope := range pending.extra {
		if err := server.authorizeResource(pending.ctx, scope.action, scope.delegated, scope.resources...); err != nil {
			return
		}
	}
	t.Fatal("revoked session response remains deliverable")
}

// A closed admitted capture cannot be replaced by the host's ambient current
// generation. Recovering an action requires explicit bound reauthorization.
func TestPersonalAuthorityRejectsRetiredInvocation(t *testing.T) {
	f := newPersonalBoundaryFixture(t, nil)
	bound, err := f.rt.RetainState()
	if err != nil {
		t.Fatal(err)
	}
	ctx := runtime.WithInvocationState(f.owner, bound)
	if err := bound.Close(); err != nil {
		t.Fatal(err)
	}
	scope := personalreview.Scope{SessionID: f.id, TaskID: "conversation:" + f.id + ":" + strconv.Itoa(f.sourceSeq)}
	called := false
	err = f.host.actions.withAuthority(ctx, scope, func(personalreview.Authority) error { called = true; return nil })
	if err == nil || called {
		t.Fatal("closed action capture silently rebound to current authority")
	}
}

func TestPersonalResourcePolicyReceivesOpaqueDelegation(t *testing.T) {
	f := newPersonalBoundaryFixture(t, nil)
	server := newOwnerHandlerFixture(f.rt, &lockedBuffer{})
	worker, err := principal.Authenticated(principal.Identity{Kind: "agent", Issuer: "test-grant", Tenant: "test", Subject: "worker"})
	if err != nil {
		t.Fatal(err)
	}
	proof := access.Delegation{Provider: "selected-grants", Grant: "opaque-id", Scope: json.RawMessage(`{"task_id":"one"}`)}
	ctx := access.WithDelegation(principal.WithContext(context.Background(), worker), proof)
	server.access.policy = access.PolicyFunc(func(_ context.Context, req access.Request) (access.Decision, error) {
		return access.Decision{Allow: !req.Owner && req.Delegation != nil && req.Delegation.Provider == proof.Provider && req.Delegation.Grant == proof.Grant}, nil
	})
	if err := server.authorizeUserSessionOperation(ctx, "inspect", f.id); err != nil {
		t.Fatalf("opaque proof lost before selected policy: %v", err)
	}
	if err := server.authorizeUserSessionOperation(principal.WithContext(context.Background(), worker), "inspect", f.id); err == nil {
		t.Fatal("grant principal alone became authority")
	}
}

func TestPersonalLiveOwnerCanInspectOrStopAfterTaskGrantRevoked(t *testing.T) {
	f := newPersonalBoundaryFixture(t, nil)
	state, err := f.rt.RetainState()
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	stale := &pluginhost.ExecutionAuthorizationReference{Provider: "retired-grant-provider", ID: "revoked-grant"}
	owner := runtime.WithInvocationState(f.owner, state)
	if _, err := f.host.server.inspectPluginSession(owner, "personal", pluginhost.SessionInspectParams{SessionID: f.id, ExecutionAuthorization: stale}); err != nil {
		t.Fatalf("fresh owner cannot inspect after grant revoked: %v", err)
	}
	if _, err := f.host.server.cancelPluginSession(owner, "personal", pluginhost.SessionCancelParams{SessionID: f.id, ExecutionAuthorization: stale}); err != nil {
		t.Fatalf("fresh owner cannot stop after grant revoked: %v", err)
	}
	for _, ctx := range []context.Context{runtime.WithInvocationState(context.Background(), state), access.WithDelegation(owner, access.Delegation{Provider: "retired-grant-provider", Grant: "revoked-grant"})} {
		if _, err := f.host.server.inspectPluginSession(ctx, "personal", pluginhost.SessionInspectParams{SessionID: f.id, ExecutionAuthorization: stale}); err == nil {
			t.Fatal("background or delegated principal bypassed revoked grant")
		}
	}
	if _, err := f.host.server.sendPluginSession(owner, "personal", pluginhost.SessionSendParams{SessionID: f.id, RequestID: "stale-send", ExecutionAuthorization: stale, Input: pluginhost.SessionInput{Prompt: "must not send"}}); err == nil {
		t.Fatal("live cleanup reauthorization widened to automatic send")
	}
}

// A sampled progress snapshot must be discarded if session authority changes,
// including while a terminal inspection is waiting for the active turn.
func TestPersonalProgressInspectRechecksResourceAuthority(t *testing.T) {
	f := newPersonalBoundaryFixture(t, nil)
	srv := f.host.server
	state, err := f.rt.RetainState()
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	ctx := runtime.WithInvocationState(f.owner, state)
	th, err := srv.ensureThreadLoaded(state, f.id)
	if err != nil {
		t.Fatal(err)
	}
	th.mu.Lock()
	th.Turns = append(th.Turns, Turn{ID: "progress-turn", Status: TurnStatusInProgress, Items: []ThreadItem{
		{ID: "input", Type: ThreadItemUserMessage, SourceID: pluginSessionRequestClientID("personal", "progress-run")},
		{ID: "output", Type: ThreadItemAgentMessage, Status: ThreadItemStatusInProgress, Text: "sampled output"},
	}})
	th.mu.Unlock()
	params := pluginhost.SessionInspectParams{SessionID: f.id, RequestID: "progress-run", IncludeProgress: true}
	var checks atomic.Int32
	srv.access.policy = access.PolicyFunc(func(_ context.Context, req access.Request) (access.Decision, error) {
		return access.Decision{Allow: req.Action != "user/session/inspect" || checks.Add(1) < 3}, nil
	})
	view, err := srv.inspectPluginSession(ctx, "personal", params)
	if err == nil || checks.Load() != 3 || view.Session.SessionID != "" || view.Progress != nil || view.Receipt != nil || view.AcceptedInput != nil || len(view.PendingInputs) != 0 {
		t.Fatalf("revoked snapshot escaped: view=%+v, checks=%d, err=%v", view, checks.Load(), err)
	}
	var allowed atomic.Bool
	allowed.Store(true)
	checks.Store(0)
	ready := make(chan struct{})
	srv.access.policy = access.PolicyFunc(func(_ context.Context, req access.Request) (access.Decision, error) {
		if req.Action == "user/session/inspect" && checks.Add(1) == 5 {
			close(ready)
		}
		return access.Decision{Allow: allowed.Load()}, nil
	})
	params.Wait, params.TimeoutMS = pluginhost.SessionInspectWaitTerminal, 5000
	type response struct {
		view pluginhost.SessionInspectResult
		err  error
	}
	finished := make(chan response, 1)
	go func() {
		result, err := srv.inspectPluginSession(ctx, "personal", params)
		finished <- response{result, err}
	}()
	select {
	case <-ready:
	case <-time.After(3 * time.Second):
		t.Fatal("terminal wait did not recheck its subscribed snapshot")
	}
	waitForPluginTurnWaiters(t, srv, "personal", "progress-run", 1)
	allowed.Store(false)
	srv.pluginTurnWaiters.notify("personal", "progress-run")
	select {
	case got := <-finished:
		if got.err == nil || got.view.Session.SessionID != "" || got.view.Progress != nil || got.view.Receipt != nil {
			t.Fatalf("revoked waiter returned a snapshot: %+v, %v", got.view, got.err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("revoked waiter did not finish")
	}
}

// A control request waiting behind a same-session operation must not use an
// authorization decision that was revoked while the lock was held.
func TestPersonalControlRechecksResourceAfterLockWait(t *testing.T) {
	f := newPersonalBoundaryFixture(t, nil)
	srv := f.host.server
	state, err := f.rt.RetainState()
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	var allowed atomic.Bool
	allowed.Store(true)
	authorized := make(chan struct{}, 1)
	srv.access.policy = access.PolicyFunc(func(_ context.Context, req access.Request) (access.Decision, error) {
		if req.Action == "user/session/control" {
			select {
			case authorized <- struct{}{}:
			default:
			}
		}
		return access.Decision{Allow: allowed.Load()}, nil
	})
	unlock := srv.lockSessionControls(f.id)
	defer func() {
		if unlock != nil {
			unlock()
		}
	}()
	finished := make(chan error, 1)
	go func() {
		_, err := srv.controlPluginSession(state, runtime.WithInvocationState(f.owner, state), "personal", pluginhost.SessionControlParams{SessionID: f.id})
		finished <- err
	}()
	select {
	case <-authorized:
	case <-time.After(3 * time.Second):
		t.Fatal("control request was not authorized")
	}
	allowed.Store(false)
	unlock()
	unlock = nil
	select {
	case err := <-finished:
		if err == nil {
			t.Fatal("control request used authority revoked during lock wait")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("control request did not finish")
	}
}
