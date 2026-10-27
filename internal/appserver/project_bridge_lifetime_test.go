package appserver

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/runtime"
)

// Failure cases: closing one connection retires another, an admitted callback
// jumps to the newest server/state, unbind leaks autonomous work, and a later
// connection cannot reuse the runtime after all earlier connections close.
func TestProjectBridgeRegistrationLifetime(t *testing.T) {
	bridge := NewProjectCoordinatorBridge()
	first := New(newTestRuntime(t, &fakeClient{}), &lockedBuffer{})
	second := New(newTestRuntime(t, &fakeClient{}), &lockedBuffer{})
	t.Cleanup(first.Close)
	t.Cleanup(second.Close)
	closeFirst, err := bridge.Bind(first)
	if err != nil {
		t.Fatal(err)
	}
	closeSecond, err := bridge.Bind(second)
	if err != nil {
		t.Fatal(err)
	}
	defer closeSecond()
	registry := pluginhost.NewServiceRegistry()
	host := pluginhost.New()
	host.AttachServiceRegistry(registry, nil)
	state := &runtime.State{Documents: first.rt.Documents, Sessions: first.rt.Sessions, Host: host}
	capture := func() (*runtime.State, error) { return state.Retain(), nil }
	scope, ctx, release, err := bridge.acquireBackground(context.Background(), registry, capture)
	if err != nil {
		t.Fatal(err)
	}
	if scope.registration.server != first || scope.state.Sessions != state.Sessions {
		t.Fatal("admission did not capture its original server and state")
	}
	unbound := make(chan struct{})
	go func() { closeFirst(); close(unbound) }()
	select {
	case <-ctx.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("unbind did not cancel its background pass")
	}
	select {
	case <-unbound:
		t.Fatal("unbind released a still-running pass")
	default:
	}
	release()
	select {
	case <-unbound:
	case <-time.After(5 * time.Second):
		t.Fatal("unbind did not join its background pass")
	}
	closeFirst()
	next, _, finish, err := bridge.acquireBackground(context.Background(), registry, capture)
	if err != nil {
		t.Fatal(err)
	}
	if next.registration.server != second {
		t.Fatal("closing first connection disconnected the surviving connection")
	}
	finish()
	closeSecond()
	if _, _, _, err := bridge.acquireBackground(context.Background(), registry, capture); err == nil {
		t.Fatal("admitted a pass without a live server")
	}
	third := New(newTestRuntime(t, &fakeClient{}), &lockedBuffer{})
	t.Cleanup(third.Close)
	closeThird, err := bridge.Bind(third)
	if err != nil {
		t.Fatal(err)
	}
	defer closeThird()
	next, _, finish, err = bridge.acquireBackground(context.Background(), registry, capture)
	if err != nil {
		t.Fatal(err)
	}
	if next.registration.server != third {
		t.Fatal("runtime did not accept a later connection")
	}
	finish()
}

func TestProjectBridgeRejectsUnreadyOrDifferentGraphState(t *testing.T) {
	bridge := NewProjectCoordinatorBridge()
	server := New(newTestRuntime(t, &fakeClient{}), &lockedBuffer{})
	t.Cleanup(server.Close)
	unbind, err := bridge.Bind(server)
	if err != nil {
		t.Fatal(err)
	}
	defer unbind()
	expected := pluginhost.NewServiceRegistry()
	unavailable := errors.New("generation is not published")
	if _, _, _, err := bridge.acquireBackground(context.Background(), expected, func() (*runtime.State, error) { return nil, unavailable }); !errors.Is(err, unavailable) {
		t.Fatalf("unready generation accepted: %v", err)
	}
	host := pluginhost.New()
	host.AttachServiceRegistry(pluginhost.NewServiceRegistry(), nil)
	state := &runtime.State{Documents: server.rt.Documents, Sessions: server.rt.Sessions, Host: host}
	if _, _, _, err := bridge.acquireBackground(context.Background(), expected, func() (*runtime.State, error) { return state.Retain(), nil }); err == nil {
		t.Fatal("borrowed another graph's current state")
	}
}

func TestProjectScopeUsesAuthenticatedCallerContext(t *testing.T) {
	bridge := NewProjectCoordinatorBridge()
	server := New(newTestRuntime(t, &fakeClient{}), &lockedBuffer{})
	t.Cleanup(server.Close)
	unbind, err := bridge.Bind(server)
	if err != nil {
		t.Fatal(err)
	}
	defer unbind()
	state := &runtime.State{Documents: server.rt.Documents, Sessions: server.rt.Sessions}
	origin := server.projectServiceContext(context.Background(), state)
	call := pluginhost.ServiceInvokeParams{CallerScope: &pluginhost.ExecutionScope{Context: origin}}
	scope, err := projectScopeForCall(context.Background(), call)
	if err != nil {
		t.Fatal(err)
	}
	if scope.registration.server != server || scope.state != state {
		t.Fatal("nested provider callback lost its originating server/state")
	}
	if _, err := projectScopeForCall(context.Background(), pluginhost.ServiceInvokeParams{}); err == nil {
		t.Fatal("accepted a caller without a captured host scope")
	}
}
