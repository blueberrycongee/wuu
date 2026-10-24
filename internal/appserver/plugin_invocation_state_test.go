package appserver

import (
	"context"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/runtime"
)

func TestPluginSessionInvocationKeepsOldStateAfterPublication(t *testing.T) {
	old := newTestRuntime(t, &fakeClient{})
	current := newTestRuntime(t, &fakeClient{})
	current.PluginSessionRouter = runtime.NewPluginSessionRouter()
	srv := New(current, &lockedBuffer{})
	t.Cleanup(srv.Close)
	binding, err := old.RetainState()
	if err != nil {
		t.Fatal(err)
	}
	defer binding.Close()
	ctx := runtime.WithInvocationState(context.Background(), binding)
	created, err := srv.createPluginSession(ctx, "scheduler", pluginhost.SessionCreateParams{RequestID: "old-create", Visibility: pluginhost.SessionVisibilityPlugin, ContextSource: pluginhost.SessionContextFresh})
	if err != nil {
		t.Fatal(err)
	}
	listed, err := srv.listPluginSessions(ctx, "scheduler", pluginhost.SessionListParams{})
	if err != nil || len(listed.Sessions) != 1 || listed.Sessions[0].SessionID != created.SessionID {
		t.Fatalf("old list: %+v %v", listed, err)
	}
	control, err := current.PluginSessionRouter.Control(ctx, "scheduler", pluginhost.SessionControlParams{SessionID: created.SessionID, State: "active"})
	if err != nil {
		t.Fatalf("old control: %v", err)
	}
	if persisted, found, err := binding.Sessions.ReadControl(created.SessionID); err != nil || !found || persisted.Revision != control.Revision {
		t.Fatalf("old control persistence: %+v %v %v", persisted, found, err)
	}
	if _, found, err := current.Sessions.Find(created.SessionID); err != nil || found {
		t.Fatalf("callback crossed into new state: %v %v", found, err)
	}
	if _, err := srv.listPluginSessions(context.Background(), "scheduler", pluginhost.SessionListParams{}); err == nil {
		t.Fatal("missing invocation fell back to current state")
	}
}

func TestPluginLifecycleAsyncKeepsCapturedHostAndOutbox(t *testing.T) {
	old := newTestRuntime(t, &fakeClient{})
	observer := &blockingPluginTurnLifecycleClient{started: make(chan struct{}), release: make(chan struct{})}
	old.PluginHost = pluginhost.New(observer)
	binding, err := old.RetainState()
	if err != nil {
		t.Fatal(err)
	}
	current := newTestRuntime(t, &fakeClient{})
	latest := &pluginTurnLifecycleClient{id: observer.ID(), calls: make(chan pluginhost.AgentTurnLifecycleInput, 1)}
	current.PluginHost = pluginhost.New(latest)
	srv := New(current, &lockedBuffer{})
	t.Cleanup(srv.Close)
	input := pluginhost.AgentTurnLifecycleInput{RequestID: "late-old", State: pluginhost.TurnLifecycleCompleted, ThreadID: "old-thread"}
	srv.notifyPluginTurnLifecycleAsync(binding, observer.ID(), input)
	select {
	case <-observer.started:
	case <-time.After(5 * time.Second):
		t.Fatal("captured observer was not invoked")
	}
	if _, found, err := binding.Sessions.FindPluginTurnLifecycle(observer.ID(), input.RequestID, input.ThreadID, ""); err != nil || !found {
		t.Fatalf("old durable event: %v %v", found, err)
	}
	if _, found, err := current.Sessions.FindPluginTurnLifecycle(observer.ID(), input.RequestID, input.ThreadID, ""); err != nil || found {
		t.Fatalf("event crossed state: %v %v", found, err)
	}
	oldHost := binding.Host
	if _, err := old.Cleanup(); err != nil {
		t.Fatal(err)
	}
	if err := binding.Close(); err != nil {
		t.Fatal(err)
	}
	close(observer.release)
	srv.Close()
	if _, active := oldHost.Capability(observer.ID(), pluginhost.CapabilityAgentTurnLifecycle); active {
		t.Fatal("async delivery leaked its retired generation")
	}
	select {
	case event := <-latest.calls:
		t.Fatalf("old notification reached replacement: %+v", event)
	default:
	}
}

func pluginInvocationContext(t *testing.T, rt *runtime.Session, ctx context.Context) context.Context {
	t.Helper()
	return runtime.WithInvocationState(ctx, testRuntimeState(t, rt))
}
