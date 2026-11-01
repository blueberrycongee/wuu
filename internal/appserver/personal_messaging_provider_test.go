package appserver

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/composition"
	"github.com/blueberrycongee/wuu/internal/persistence"
	"github.com/blueberrycongee/wuu/internal/personalim"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/runtime"
	"github.com/blueberrycongee/wuu/internal/userspace"
)

// Messaging must start after host readiness without a UI call, stop before a
// new native writer starts, and reject raw plugin calls without owner admission.
func TestPersonalMessagingColdStartAndGenerationReplacement(t *testing.T) {
	documents, err := persistence.Open(context.Background(), t.TempDir(), "jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer documents.Close()
	bindings := NewRuntimeBindings()
	var starts atomic.Int32
	started := make(chan struct{}, 3)
	bindings.SetPersonalMessagingFactory(func(ctx context.Context, input runtime.NativeProviderContext, host *UserHost, current func() bool) (*personalim.Bridge, error) {
		starts.Add(1)
		started <- struct{}{}
		return personalim.New(personalim.Options{Documents: documents, Client: func(context.Context, personalim.Binding) (personalim.Client, error) {
			t.Fatal("unconfigured startup cannot dispatch")
			return nil, nil
		}})
	})
	registry, err := pluginhost.NewServiceRegistryWithBindings([]composition.Binding{{Service: PersonalMessagingService, Major: 1, Provider: PersonalMessagingModule}})
	if err != nil {
		t.Fatal(err)
	}
	host := pluginhost.New()
	host.AttachServiceRegistry(registry, nil)
	input := runtime.NativeProviderContext{Registry: registry, UserBinding: userspace.Binding{UserSpaceID: "owner", ProductDomain: "personal"}}
	first := newPersonalMessagingProvider(input, bindings)
	registry.RegisterClients(first)
	if err = first.Activate(context.Background()); err != nil {
		t.Fatal(err)
	}
	if starts.Load() != 0 {
		t.Fatal("started before UserHost was ready")
	}
	if err = bindings.BindPersonalMessagingHost(context.Background(), &UserHost{runtime: &runtime.Session{PluginHost: host}}); err != nil {
		t.Fatal(err)
	}
	if starts.Load() != 1 {
		t.Fatal("cold startup waited for a UI operation")
	}
	<-started
	unpublishedInput := input
	unpublishedInput.Registry = pluginhost.NewServiceRegistry()
	candidate := newPersonalMessagingProvider(unpublishedInput, bindings)
	if err = candidate.Activate(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err = candidate.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if starts.Load() != 1 || first.Status().State != pluginhost.StateActive {
		t.Fatal("unpublished candidate replaced the live transport")
	}
	next := newPersonalMessagingProvider(input, bindings)
	if err = next.Activate(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("published replacement did not start")
	}
	if starts.Load() != 2 || first.Status().State != pluginhost.StateStopped {
		t.Fatal("previous generation still owns its transport")
	}
	_, err = next.InvokeService(context.Background(), pluginhost.ServiceInvokeParams{Service: PersonalMessagingService, Method: "call", Params: json.RawMessage(`{"operation":"im.status","input":{}}`)})
	if err == nil {
		t.Fatal("unadmitted plugin called owner messaging control")
	}
	if err = next.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}

// An enabled native module is not permission to consume input when composition
// selected another provider (or removed the route entirely).
func TestPersonalMessagingUnselectedNeverStarts(t *testing.T) {
	for _, external := range []bool{false, true} {
		t.Run(map[bool]string{false: "unbound", true: "external"}[external], func(t *testing.T) {
			bindings := NewRuntimeBindings()
			var starts atomic.Int32
			bindings.SetPersonalMessagingFactory(func(context.Context, runtime.NativeProviderContext, *UserHost, func() bool) (*personalim.Bridge, error) {
				starts.Add(1)
				return nil, nil
			})
			selected := "external.messaging"
			registry, err := pluginhost.NewServiceRegistryWithBindings([]composition.Binding{{Service: PersonalMessagingService, Major: 1, Provider: selected}})
			if err != nil {
				t.Fatal(err)
			}
			input := runtime.NativeProviderContext{Registry: registry, UserBinding: userspace.Binding{UserSpaceID: "owner", ProductDomain: "personal"}}
			native := newPersonalMessagingProvider(input, bindings)
			registry.RegisterClients(native)
			if external {
				registry.RegisterClients(&messagingExternalFixture{personalMessagingProvider: newPersonalMessagingProvider(input, bindings)})
			}
			h := pluginhost.New()
			h.AttachServiceRegistry(registry, nil)
			host := &UserHost{runtime: &runtime.Session{PluginHost: h}}
			if err = bindings.BindPersonalMessagingHost(context.Background(), host); err != nil {
				t.Fatal(err)
			}
			if err = native.Activate(context.Background()); err != nil {
				t.Fatal(err)
			}
			done := make(chan struct{})
			go func() { native.watch.Wait(); close(done) }()
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("published unselected provider kept publication watcher alive")
			}
			if native.published(host) || starts.Load() != 0 {
				t.Fatal("unselected native provider started")
			}
			_ = native.Close(context.Background())
		})
	}
}

type messagingExternalFixture struct{ *personalMessagingProvider }

func (*messagingExternalFixture) ID() string { return "external.messaging" }
