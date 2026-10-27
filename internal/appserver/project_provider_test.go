package appserver

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/composition"
	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/persistence"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/runtime"
	"github.com/blueberrycongee/wuu/internal/session"
)

// Failure cases: a disabled provider keeps timers alive, an unavailable selected
// provider falls back to native policy, and a late event mutates a new binding.
func TestProjectCoordinationProviderLifecycle(t *testing.T) {
	if !projectAgentEnabled {
		t.Skip("project_agent build only")
	}
	documents, err := persistence.Open(context.Background(), t.TempDir(), "sqlite")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = documents.Close() })
	store := session.NewStore(context.Background(), documents)
	srv := New(newTestRuntime(t, &fakeClient{}), &lockedBuffer{})
	t.Cleanup(srv.Close)
	native := srv.NewProjectCoordinationProvider(store)
	consumer := NewProjectCoordinationConsumer()
	registry, err := pluginhost.NewServiceRegistryWithBindings([]composition.Binding{{Service: ProjectCoordinationService, Major: 1, Provider: native.ID()}})
	if err != nil {
		t.Fatal(err)
	}
	bridge := native.bridge
	mechanism, err := bridge.MechanismFactory().Build(runtime.NativeProviderContext{Sessions: store, Registry: registry, RetainState: func() (*runtime.State, error) { return native.retainState() }})
	if err != nil {
		t.Fatal(err)
	}
	native.registry = registry
	background := &projectBackgroundTestService{}
	stateProvider := persistence.NewService(persistence.SQLiteModule, t.TempDir(), "sqlite")
	cfg := config.Default()
	cfg.Agent.ProjectMaxParallel = 2
	configuration := config.NewMemoryService("independent.config", cfg)
	sessions := session.NewService(session.DefaultModule, documents)
	conflicts := registry.RegisterClients(native, consumer, stateProvider, sessions, mechanism, background, configuration)
	if len(conflicts) != 0 {
		t.Fatalf("conflicts: %v", conflicts)
	}
	host := pluginhost.New(native, consumer, stateProvider, sessions, mechanism, background, configuration)
	background.host = host
	host.AttachServiceRegistry(registry, conflicts)
	bindProjectTestGraph(t, native, host)
	if err := host.Activate(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = host.Close(context.Background()) })
	select {
	case <-native.lifetime.schedulerReady:
	case <-time.After(5 * time.Second):
		t.Fatal("scheduler did not start")
	}
	raw, err := json.Marshal(session.Session{Source: projectSource})
	if err != nil {
		t.Fatal(err)
	}
	output, callErr := host.CallService(srv.projectServiceContext(context.Background(), native.binding), ProjectCoordinationConsumer, ProjectCoordinationService, "instructions", raw)
	if callErr != nil || len(output) == 0 {
		t.Fatalf("native policy: %s %v", output, callErr)
	}
	var admission ProjectAdmissionPolicy
	if err := callProjectCoordination(srv.projectServiceContext(context.Background(), native.binding), host, "admission", ProjectCoordinationRequest{}, &admission); err != nil || admission.MaxParallel != 2 {
		t.Fatalf("admission did not use selected configuration: %+v %v", admission, err)
	}
	timerFired := make(chan func(), 1)
	var stopped atomic.Bool
	timerStopped := make(chan struct{})
	native.lifetime.projectInboxAfterFunc = func(_ time.Duration, fire func()) func() {
		timerFired <- fire
		return func() {
			if stopped.CompareAndSwap(false, true) {
				close(timerStopped)
			}
		}
	}
	if err := documents.Close(); err != nil {
		t.Fatal(err)
	}
	if err := callProjectCoordination(srv.projectServiceContext(context.Background(), native.binding), host, "schedule", ProjectCoordinationRequest{ProjectID: "retry-storage"}, nil); err != nil {
		t.Fatal(err)
	}
	fire := <-timerFired
	host.StopBackground()
	select {
	case <-timerStopped:
	case <-time.After(5 * time.Second):
		t.Fatal("generation stop left retry timer armed")
	}
	if native.lifetime.closed.Load() {
		t.Fatal("background stop closed retained foreground provider")
	}
	if _, err := native.InvokeService(srv.projectServiceContext(context.Background(), native.binding), pluginhost.ServiceInvokeParams{Service: ProjectCoordinationService, Method: "instructions", Params: raw}); err != nil {
		t.Fatalf("retained foreground policy unavailable: %v", err)
	}
	if _, ok := host.RetirePlugin(context.Background(), native.ID(), errors.New("disabled")); !ok {
		t.Fatal("provider not retired")
	}
	if !stopped.Load() {
		t.Fatal("retirement left retry timer armed")
	}
	fire()
	select {
	case <-timerFired:
		t.Fatal("late timer scheduled another generation")
	default:
	}
	if !native.lifetime.closed.Load() {
		t.Fatal("provider lifecycle remained active")
	}
	_, callErr = host.CallService(srv.projectServiceContext(context.Background(), native.binding), ProjectCoordinationConsumer, ProjectCoordinationService, "instructions", raw)
	if callErr == nil {
		t.Fatal("disabled provider still handled request")
	}
	if native.startBackground(func(*ProjectCoordinationProvider) { t.Error("stopped scheduler ran") }) {
		t.Fatal("accepted background work after stop")
	}
}

// A prepared replacement must not schedule until mechanisms are attached; a
// close between graph preparation and binding must remain permanently stopped.
func TestProjectCoordinationBridgeDefersAndFencesActivation(t *testing.T) {
	if !projectAgentEnabled {
		t.Skip("project_agent build only")
	}
	documents, err := persistence.Open(context.Background(), t.TempDir(), "jsonl")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = documents.Close() })
	bridge := NewProjectCoordinatorBridge()
	client, err := bridge.ProviderFactory().Build(runtime.NativeProviderContext{Sessions: session.NewStore(context.Background(), documents), Registry: pluginhost.NewServiceRegistry(), RetainState: func() (*runtime.State, error) { return nil, errors.New("not published") }})
	if err != nil {
		t.Fatal(err)
	}
	provider := client.(*ProjectCoordinationProvider)
	if err := provider.Activate(context.Background()); err != nil {
		t.Fatal(err)
	}
	if provider.startBackground(func(*ProjectCoordinationProvider) { t.Error("unbound provider scheduled") }) {
		t.Fatal("accepted work before binding")
	}
	if err := provider.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	srv := New(newTestRuntime(t, &fakeClient{}), &lockedBuffer{})
	t.Cleanup(srv.Close)
	bridge.Bind(srv)
	if provider.startBackground(func(*ProjectCoordinationProvider) { t.Error("retired provider scheduled") }) {
		t.Fatal("binding revived provider")
	}
}

// The fixture only adapts the real Host background machinery to its public
// service; scheduling policy remains entirely in the provider under test.
type projectBackgroundTestService struct{ host *pluginhost.Host }

func (*projectBackgroundTestService) ID() string { return "test.background" }
func (*projectBackgroundTestService) Status() pluginhost.Status {
	return pluginhost.Status{ID: "test.background", State: pluginhost.StateActive}
}
func (*projectBackgroundTestService) ProvidedServices() []pluginhost.ServiceDescriptor {
	return []pluginhost.ServiceDescriptor{pluginhost.KernelBackgroundDescriptor()}
}
func (*projectBackgroundTestService) RequiredServices() []pluginhost.ServiceRequirement { return nil }
func (*projectBackgroundTestService) Close(context.Context) error                       { return nil }
func (b *projectBackgroundTestService) InvokeService(ctx context.Context, call pluginhost.ServiceInvokeParams) (json.RawMessage, error) {
	var input pluginhost.BackgroundStartParams
	if err := json.Unmarshal(call.Params, &input); err != nil {
		return nil, err
	}
	result, err := b.host.StartBackground(ctx, call.Caller, input)
	if err != nil {
		return nil, err
	}
	return json.Marshal(result)
}

// A completion admitted by a retained generation must persist its result even
// after autonomous dispatch stops; the successor must not interpret it instead.
func TestProjectCoordinationRetainedCompletionPersistsWithoutDispatch(t *testing.T) {
	if !projectAgentEnabled {
		t.Skip("project_agent build only")
	}
	documents, err := persistence.Open(context.Background(), t.TempDir(), "sqlite")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = documents.Close() })
	store := session.NewStore(context.Background(), documents)
	srv := New(newTestRuntime(t, &fakeClient{}), &lockedBuffer{})
	t.Cleanup(srv.Close)
	for _, metadata := range []session.Session{
		{ID: "retained-project", Source: projectSource, CWD: srv.rt.RootDir, Owner: "user"},
		{ID: "retained-member", Source: projectSessionSource, ParentID: "retained-project", CWD: srv.rt.RootDir, Owner: "user"},
	} {
		if _, err := store.CreateInitialized(metadata, nil); err != nil {
			t.Fatal(err)
		}
	}
	if _, ok, err := store.ReadControl("retained-member"); err != nil {
		t.Fatal(err)
	} else if !ok {
		if _, err := store.ChangeControl("retained-member", "retained-project", session.ControlActive, 0); err != nil {
			t.Fatal(err)
		}
	}
	provider := srv.NewProjectCoordinationProvider(store)
	registry := pluginhost.NewServiceRegistry()
	host := pluginhost.New()
	host.AttachServiceRegistry(registry, nil)
	provider.registry = registry
	bindProjectTestGraph(t, provider, host)
	provider.lifetime.active.Store(true)
	provider.stopScheduling()
	defer provider.Close(context.Background())
	raw, _ := json.Marshal(ProjectCoordinationRequest{SessionID: "retained-member", Turn: Turn{ID: "retained-turn", Status: TurnStatusCompleted, Items: []ThreadItem{{Type: ThreadItemAgentMessage, Text: "Completed in original generation"}}}})
	ctx := srv.projectServiceContext(context.Background(), provider.binding)
	ctx.Value(projectCallScopeKey{}).(*projectCallScope).thread = &threadState{ID: "retained-member", Source: projectSessionSource, ProjectID: "retained-project", Title: "Original admitted title", state: provider.binding}
	srv.Close()
	if _, err := provider.InvokeService(ctx, pluginhost.ServiceInvokeParams{Service: ProjectCoordinationService, Method: "turn", Params: raw}); err != nil {
		t.Fatal(err)
	}
	pending, err := store.PendingInbox("retained-project")
	if err != nil || len(pending) != 1 || pending[0].RelatedSessionID != "retained-member" || !strings.Contains(pending[0].Content, "Original admitted title") {
		t.Fatalf("retained completion was lost: %+v %v", pending, err)
	}
	if len(provider.lifetime.projectInboxDrains) != 0 {
		t.Fatal("retained completion restarted background dispatch")
	}
}

func (s *Server) NewProjectCoordinationProvider(store *session.Store) *ProjectCoordinationProvider {
	p := newProjectCoordinationProvider(store)
	bridge := NewProjectCoordinatorBridge()
	if _, err := bridge.Bind(s); err != nil {
		panic(err)
	}
	p.bridge, p.Server = bridge, s
	return p
}

func bindProjectTestGraph(t *testing.T, provider *ProjectCoordinationProvider, host *pluginhost.Host) {
	t.Helper()
	binding := &runtime.State{Documents: provider.rt.Documents, Sessions: provider.rt.Sessions, Host: host}
	provider.binding = binding
	provider.retainState = func() (*runtime.State, error) { return binding.Retain(), nil }
	t.Cleanup(func() { _ = binding.Close() })
}

// The last background reference can synchronously retire its own generation.
// Provider shutdown must not wait on the same pass that is releasing that ref.
func TestProjectBackgroundFinalReferenceDoesNotWaitOnItself(t *testing.T) {
	if !projectAgentEnabled {
		t.Skip("project_agent build only")
	}
	rt := newTestRuntime(t, &fakeClient{})
	server := New(rt, &lockedBuffer{})
	t.Cleanup(server.Close)
	provider := server.NewProjectCoordinationProvider(rt.Sessions)
	provider.lifetime.active.Store(true)
	close(provider.lifetime.schedulerReady)
	registry := pluginhost.NewServiceRegistry()
	closed := make(chan error, 1)
	observed := &projectCloseResultProbe{ProjectCoordinationProvider: provider, result: closed}
	conflicts := registry.RegisterClients(observed)
	if len(conflicts) != 0 {
		t.Fatalf("graph: %v", conflicts)
	}
	host := pluginhost.New(observed)
	host.AttachServiceRegistry(registry, conflicts)
	provider.registry = registry
	// Capture the fixture graph before its first State, rather than redirecting
	// the server's already retained generation by mutating PluginHost.
	selected := &runtime.Session{Documents: rt.Documents, Sessions: rt.Sessions, PluginHost: host}
	provider.retainState = selected.RetainState
	entered := make(chan *runtime.PluginGeneration, 1)
	finish := make(chan struct{})
	if !provider.startBackground(func(operation *ProjectCoordinationProvider) {
		entered <- operation.binding.Generation
		<-finish
	}) {
		t.Fatal("background pass rejected")
	}
	generation := <-entered
	// Drop the publishing Session reference while the admitted pass still owns
	// its reference, exactly as replacement does before asynchronous teardown.
	generation.Release()
	close(finish)
	select {
	case err := <-closed:
		if err != nil {
			t.Fatalf("final background reference blocked its own shutdown: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("generation did not finish shutting down")
	}
}

type projectCloseResultProbe struct {
	*ProjectCoordinationProvider
	result chan<- error
}

func (p *projectCloseResultProbe) Close(ctx context.Context) error {
	err := p.ProjectCoordinationProvider.Close(ctx)
	p.result <- err
	return err
}
