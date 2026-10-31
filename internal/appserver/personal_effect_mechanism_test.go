package appserver

import (
	"context"
	"encoding/json"
	"github.com/blueberrycongee/wuu/internal/runtime"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/toolresult"
)

// Release must cancel before waiting, and an executing effect cannot be replayed.
func TestPersonalEffectReleaseCancelsAndDrains(t *testing.T) {
	b := NewPersonalEffectBridge()
	registry := pluginhost.NewServiceRegistry()
	entered, cancelled, finish := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	r := newPersonalEffectResource()
	r.owner, r.registry = "coordinator", registry
	r.use = func(ctx context.Context) (toolresult.Result, error) {
		calls.Add(1)
		close(entered)
		<-ctx.Done()
		close(cancelled)
		<-finish
		return toolresult.Result{}, ctx.Err()
	}
	id, err := b.add(r)
	if err != nil {
		t.Fatal(err)
	}
	running := make(chan error, 1)
	go func() { _, err := b.execute(context.Background(), id, "coordinator", nil); running <- err }()
	<-entered
	if _, err := b.execute(context.Background(), id, "coordinator", nil); err == nil {
		t.Fatal("replayed executing effect")
	}
	released := make(chan struct{})
	go func() { b.ReleaseCoordinator(registry, "coordinator"); close(released) }()
	<-cancelled
	select {
	case <-released:
		t.Fatal("release returned before effect quiesced")
	default:
	}
	close(finish)
	<-running
	<-released
	if calls.Load() != 1 {
		t.Fatal("effect executed more than once")
	}
	if _, err := b.execute(context.Background(), id, "coordinator", nil); err == nil {
		t.Fatal("released effect executed")
	}
}

func TestPersonalEffectOwnershipAndGenerationCleanup(t *testing.T) {
	b := NewPersonalEffectBridge()
	old, next := pluginhost.NewServiceRegistry(), pluginhost.NewServiceRegistry()
	add := func(registry *pluginhost.ServiceRegistry) string {
		r := newPersonalEffectResource()
		r.owner, r.registry = "coordinator", registry
		r.use = func(context.Context) (toolresult.Result, error) { return toolresult.Result{}, nil }
		id, err := b.add(r)
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	oldID, nextID := add(old), add(next)
	if _, err := b.execute(context.Background(), nextID, "foreign", nil); err == nil {
		t.Fatal("foreign coordinator executed effect")
	}
	b.ReleaseCoordinator(old, "coordinator")
	if _, err := b.execute(context.Background(), oldID, "coordinator", nil); err == nil {
		t.Fatal("old effect survived cleanup")
	}
	if _, err := b.execute(context.Background(), nextID, "coordinator", nil); err != nil {
		t.Fatal(err)
	}
	b.Close()
	if _, err := b.add(newPersonalEffectResource()); err == nil {
		t.Fatal("closed bridge admitted resource")
	}
}

type personalEffectCoordinatorFixture struct{}

func (*personalEffectCoordinatorFixture) ID() string { return personalActionsConsumerID }
func (*personalEffectCoordinatorFixture) Status() pluginhost.Status {
	return pluginhost.Status{ID: personalActionsConsumerID, State: pluginhost.StateActive}
}
func (*personalEffectCoordinatorFixture) Activate(context.Context) error { return nil }
func (*personalEffectCoordinatorFixture) Close(context.Context) error    { return nil }
func (*personalEffectCoordinatorFixture) RequiredServices() []pluginhost.ServiceRequirement {
	return nil
}
func (*personalEffectCoordinatorFixture) ProvidedServices() []pluginhost.ServiceDescriptor {
	return []pluginhost.ServiceDescriptor{{Name: "personal.coordination", Version: "1.0.0", Methods: []pluginhost.ServiceMethodDescriptor{{Name: "call", InputSchema: "fixture.input.v1", OutputSchema: "fixture.output.v1"}}}}
}
func (*personalEffectCoordinatorFixture) InvokeService(context.Context, pluginhost.ServiceInvokeParams) (json.RawMessage, error) {
	return nil, nil
}

// A process callback carries host provenance in CallerScope, not request JSON.
// The prepared file survives that callback but still checks frozen file bytes.
func TestPersonalEffectPublicFileContract(t *testing.T) {
	f := newPersonalBoundaryFixture(t, &fakeClient{})
	state, err := f.rt.RetainState()
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	registry := state.Host.ServiceRegistry()
	b := NewPersonalEffectBridge()
	defer b.Close()
	client, err := b.ProviderFactory().Build(runtime.NativeProviderContext{Registry: registry})
	if err != nil {
		t.Fatal(err)
	}
	m := client.(*personalEffectMechanisms)
	_ = m.Activate(context.Background())
	ctx := b.Context(f.owner, f.host.server, state, f.kit)
	invoke := func(method string, input any) (json.RawMessage, error) {
		raw, _ := json.Marshal(input)
		return m.InvokeService(context.Background(), pluginhost.ServiceInvokeParams{Caller: personalActionsConsumerID, Method: method, Params: raw, CallerScope: &pluginhost.ExecutionScope{Context: ctx}})
	}
	raw, err := invoke("prepare-file", f.call())
	if err != nil {
		t.Fatal(err)
	}
	var prepared PersonalPreparedFile
	if err := json.Unmarshal(raw, &prepared); err != nil {
		t.Fatal(err)
	}
	if _, err := invoke("execute", PersonalEffectReference{prepared.EffectID}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(f.rt.RootDir, "note.txt"))
	if err != nil || string(data) != "saved once" {
		t.Fatalf("file=%q err=%v", data, err)
	}
	if _, err := invoke("execute", PersonalEffectReference{prepared.EffectID}); err == nil {
		t.Fatal("file effect replayed")
	}
	raw, err = invoke("prepare-file", f.call())
	if err != nil {
		t.Fatal(err)
	}
	_ = json.Unmarshal(raw, &prepared)
	if err := os.WriteFile(filepath.Join(f.rt.RootDir, "note.txt"), []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := invoke("execute", PersonalEffectReference{prepared.EffectID}); err == nil {
		t.Fatal("stale frozen file executed")
	}
	if _, err := m.InvokeService(context.Background(), pluginhost.ServiceInvokeParams{Caller: personalActionsConsumerID, Method: "prepare-file", Params: json.RawMessage(`{}`)}); err == nil {
		t.Fatal("wire call without host scope accepted")
	}
}

func TestPersonalEffectConnectorPublicExecuteAndRelease(t *testing.T) {
	f := newPersonalBoundaryFixture(t, &fakeClient{})
	state, err := f.rt.RetainState()
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	registry := state.Host.ServiceRegistry()
	b := NewPersonalEffectBridge()
	defer b.Close()
	client, err := b.ProviderFactory().Build(runtime.NativeProviderContext{Registry: registry})
	if err != nil {
		t.Fatal(err)
	}
	m := client.(*personalEffectMechanisms)
	_ = m.Activate(context.Background())
	ctx := b.Context(f.owner, f.host.server, state, nil)
	var uses atomic.Int32
	id, release, err := b.RegisterConnector(ctx, state, func(context.Context) error { uses.Add(1); return nil })
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	raw, _ := json.Marshal(PersonalEffectReference{id})
	if _, err := m.InvokeService(ctx, pluginhost.ServiceInvokeParams{Caller: personalActionsConsumerID, Method: "execute", Params: raw}); err != nil {
		t.Fatal(err)
	}
	if uses.Load() != 1 {
		t.Fatal("connector effect not executed")
	}
	release()
	b.ReleaseCoordinator(registry, personalActionsConsumerID)
	if _, _, err := b.RegisterConnector(ctx, state, func(context.Context) error { t.Fatal("disabled callback"); return nil }); err == nil {
		t.Fatal("disabled coordinator admitted new effect")
	}
}

// add publishes the unregister callback and resource under the same bridge
// mutex used by retirement. A callback cannot read a partially initialized entry.
func TestPersonalEffectConcurrentRegisterAndRetire(t *testing.T) {
	for i := 0; i < 100; i++ {
		b := NewPersonalEffectBridge()
		registry := pluginhost.NewServiceRegistry()
		r := newPersonalEffectResource()
		r.owner, r.registry = "coordinator", registry
		begin := make(chan struct{})
		added := make(chan error, 1)
		retired := make(chan struct{})
		go func() {
			<-begin
			_, err := b.add(r)
			if err != nil {
				r.finish(context.Background())
			}
			added <- err
		}()
		go func() { <-begin; registry.RevokePlugin(context.Background(), "coordinator"); close(retired) }()
		close(begin)
		<-added
		<-retired
		b.mu.Lock()
		remaining := len(b.resources)
		b.mu.Unlock()
		if remaining != 0 {
			t.Fatalf("retired owner retained %d resources", remaining)
		}
		b.Close()
	}
}
