package appserver

import (
	"context"
	"encoding/json"
	"github.com/blueberrycongee/wuu/internal/clientauthority"
	"github.com/blueberrycongee/wuu/internal/composition"
	"github.com/blueberrycongee/wuu/internal/config"
	pluginpkg "github.com/blueberrycongee/wuu/internal/plugin"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/blueberrycongee/wuu/internal/personalconnectors"
	"github.com/blueberrycongee/wuu/internal/personalhost"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/runtime"
)

// Accepted account work must finish before its broker closes. A different
// generation owns a different broker; retiring the old one cannot close it.
func TestPersonalAccountGenerationDrainsBeforeClosingOnlyItsResources(t *testing.T) {
	f := newPersonalBoundaryFixture(t, &fakeClient{})
	state, err := f.rt.RetainState()
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	entered, finish := make(chan struct{}), make(chan struct{})
	first, second := &accountLifecycleBroker{entered: entered, finish: finish}, &accountLifecycleBroker{}
	makeProvider := func(b *accountLifecycleBroker) *personalAccountsProvider {
		p := newPersonalAccountsProvider(runtime.NativeProviderContext{Registry: state.Host.ServiceRegistry(), UserBinding: f.rt.UserBinding()}, func(runtime.NativeProviderContext) (personalhost.Options, error) {
			return personalhost.Options{Connectors: b}, nil
		})
		if err := p.Activate(context.Background()); err != nil {
			t.Fatal(err)
		}
		return p
	}
	old, next := makeProvider(first), makeProvider(second)
	ctx := context.WithValue(runtime.WithInvocationState(f.owner, state), personalAccountsScopeKey{}, f.host.server)
	raw := json.RawMessage(`{"operation":"connectors.list","input":{}}`)
	call := pluginhost.ServiceInvokeParams{Service: PersonalAccountsService, Method: "call", Params: raw}
	result := make(chan error, 1)
	go func() { _, err := old.InvokeService(ctx, call); result <- err }()
	<-entered
	closing := make(chan error, 1)
	closingContext, cancelClose := context.WithCancel(context.Background())
	cancelClose()
	go func() { closing <- old.Close(closingContext) }()
	<-old.stopping
	if first.closed.Load() != 0 {
		t.Fatal("closed accepted operation's broker")
	}
	if _, err := old.InvokeService(ctx, call); err == nil {
		t.Fatal("stopped provider admitted new call")
	}
	if _, err := next.InvokeService(ctx, call); err != nil {
		t.Fatal(err)
	}
	close(finish)
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	if err := <-closing; err != nil {
		t.Fatal(err)
	}
	if first.closed.Load() != 1 || second.closed.Load() != 0 {
		t.Fatalf("close counts old=%d new=%d", first.closed.Load(), second.closed.Load())
	}
	if err := next.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if second.closed.Load() != 1 {
		t.Fatal("replacement resources not closed exactly once")
	}
}

func TestUncalledPersonalAccountProviderConstructsNoResources(t *testing.T) {
	var builds atomic.Int64
	p := newPersonalAccountsProvider(runtime.NativeProviderContext{}, func(runtime.NativeProviderContext) (personalhost.Options, error) {
		builds.Add(1)
		return personalhost.Options{}, nil
	})
	if err := p.Activate(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := p.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if builds.Load() != 0 {
		t.Fatal("activation constructed unused account resources")
	}
}

type accountLifecycleBroker struct {
	closed          atomic.Int64
	entered, finish chan struct{}
}

func (b *accountLifecycleBroker) List(context.Context, personalconnectors.Actor) ([]personalconnectors.Connection, error) {
	if b.entered != nil {
		close(b.entered)
		<-b.finish
	}
	return nil, nil
}
func (*accountLifecycleBroker) Call(context.Context, personalconnectors.Actor, personalconnectors.Request) (personalconnectors.Result, error) {
	return personalconnectors.Result{}, nil
}
func (*accountLifecycleBroker) Actions(context.Context, personalconnectors.Actor) ([]personalconnectors.Result, error) {
	return nil, nil
}
func (*accountLifecycleBroker) Decide(context.Context, personalconnectors.Actor, string, string, bool) (personalconnectors.Result, error) {
	return personalconnectors.Result{}, nil
}
func (*accountLifecycleBroker) Disconnect(context.Context, personalconnectors.Actor, string) error {
	return nil
}
func (b *accountLifecycleBroker) Close() error { b.closed.Add(1); return nil }

// Exercise composition publication, not a pointer swap: fresh calls select the
// replacement, retained calls keep their original provider, and disable has no
// implicit native fallback even though the user-space remains Personal.
func TestPersonalAccountsCompositionSelectionAndReload(t *testing.T) {
	home := t.TempDir()
	cfg := config.Default()
	cfg.DefaultProvider = "fixture"
	cfg.Providers = map[string]config.ProviderConfig{"fixture": {Type: "openai-compatible", BaseURL: "http://127.0.0.1:9/v1", APIKey: "synthetic", Model: "fixture"}}
	cfg.Composition = &composition.Config{Version: composition.Version, Profile: "headless", Patches: []composition.Patch{{Modules: map[string]bool{personalActionsConsumerID: true, PersonalEffectModule: true, PersonalAccountsModule: true, PersonalAccountsConsumer: true}}}}
	bindings := NewRuntimeBindings()
	var brokers []*accountLifecycleBroker
	bindings.SetPersonalAccountsFactory(func(runtime.NativeProviderContext) (personalhost.Options, error) {
		b := &accountLifecycleBroker{}
		brokers = append(brokers, b)
		return personalhost.Options{Connectors: b}, nil
	})
	const replacement = "fixture.account-replacement"
	factories := append(bindings.NativeProviders(), runtime.NativeProviderFactory{ID: replacement, Build: func(runtime.NativeProviderContext) (pluginhost.Client, error) {
		return &accountReplacement{id: replacement}, nil
	}})
	plugins := []pluginpkg.Plugin{}
	rt, err := runtime.NewSession(runtime.Options{RootDir: t.TempDir(), WuuHome: home, HomeDir: home, UserSpaceID: "accounts-space", ProductDomain: "personal", Config: cfg, DiscoveredPlugins: &plugins, NativeProviders: factories, NonInteractive: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, err := rt.Cleanup(); err != nil {
			t.Error(err)
		}
	}()
	host, err := NewUserHost(rt, personalhost.Options{}, WithRuntimeBindings(bindings))
	if err != nil {
		t.Fatal(err)
	}
	defer host.Close()
	owner, err := clientauthority.AdmitDesktop(personalTestOwner(context.Background()), rt.UserBinding(), strings.NewReader(`{"version":1,"user_space_id":"accounts-space","product_domain":"personal"}`))
	if err != nil {
		t.Fatal(err)
	}
	call := func(ctx context.Context) json.RawMessage {
		t.Helper()
		raw, err := host.personal.Call(ctx, "connectors.list", json.RawMessage(`{}`), "")
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	if len(brokers) != 0 {
		t.Fatal("host boot eagerly created account resources")
	}
	call(owner)
	old, err := rt.RetainState()
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	reload := func() {
		t.Helper()
		candidate, err := rt.PreflightExtensionPolicy(cfg)
		if err != nil {
			t.Fatal(err)
		}
		if err = rt.ActivatePluginGeneration(candidate, nil); err != nil {
			t.Fatal(err)
		}
	}
	reload()
	call(owner)
	if len(brokers) != 2 {
		t.Fatalf("generation factories=%d", len(brokers))
	}
	call(runtime.WithInvocationState(owner, old))
	if brokers[0].closed.Load() != 0 {
		t.Fatal("retained provider closed prematurely")
	}
	if err = old.Close(); err != nil {
		t.Fatal(err)
	}
	if brokers[0].closed.Load() != 1 || brokers[1].closed.Load() != 0 {
		t.Fatal("old cleanup closed wrong generation")
	}
	cfg.Composition.Patches = append(cfg.Composition.Patches, composition.Patch{Modules: map[string]bool{PersonalAccountsModule: false, replacement: true}, Services: []composition.Binding{{Service: PersonalAccountsService, Major: 1, Provider: replacement}}})
	reload()
	if got := string(call(owner)); got != `{"replacement":true}` {
		t.Fatalf("selected replacement result=%s", got)
	}
	if len(brokers) != 2 {
		t.Fatal("replacement constructed native fallback")
	}
	cfg.Composition.Patches = append(cfg.Composition.Patches, composition.Patch{Modules: map[string]bool{replacement: false}})
	// Remove the now-disabled explicit binding while leaving the consumer alive.
	cfg.Composition.Patches[len(cfg.Composition.Patches)-2].Services = nil
	reload()
	if _, err := host.personal.Call(owner, "connectors.list", json.RawMessage(`{}`), ""); err == nil {
		t.Fatal("disabled service fell back to native accounts")
	}
	if len(brokers) != 2 {
		t.Fatal("disabled provider constructed resources")
	}
}

type accountReplacement struct {
	personalActionsConsumer
	id string
}

func (p *accountReplacement) ID() string { return p.id }
func (p *accountReplacement) Status() pluginhost.Status {
	s := p.personalActionsConsumer.Status()
	s.ID = p.id
	return s
}
func (*accountReplacement) RequiredServices() []pluginhost.ServiceRequirement { return nil }
func (*accountReplacement) ProvidedServices() []pluginhost.ServiceDescriptor {
	return []pluginhost.ServiceDescriptor{PersonalAccountsDescriptor()}
}
func (*accountReplacement) InvokeService(context.Context, pluginhost.ServiceInvokeParams) (json.RawMessage, error) {
	return json.RawMessage(`{"value":{"replacement":true}}`), nil
}
