//go:build project_agent

package appserver

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/access"
	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/compactionservice"
	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/credentialstore"
	"github.com/blueberrycongee/wuu/internal/persistence"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/promptservice"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/runtime"
	"github.com/blueberrycongee/wuu/internal/session"
	"github.com/blueberrycongee/wuu/internal/titleservice"
	"github.com/blueberrycongee/wuu/internal/tools"
	"github.com/blueberrycongee/wuu/internal/toolservice"
)

// Existing behavioral suites exercise the native provider's recovery boundary
// directly while request/response checks use the selected service graph.
var projectFixtureProviders sync.Map

func projectFixtureProvider(s *Server) *ProjectCoordinationProvider {
	value, ok := projectFixtureProviders.Load(s)
	if !ok {
		panic("project fixture has no bound provider")
	}
	return value.(*ProjectCoordinationProvider)
}
func (s *Server) currentProjectWorkTurn(id string) (string, error) {
	return projectFixtureProvider(s).currentProjectWorkTurn(id)
}
func (s *Server) recordWorkTurn(th *threadState, turn Turn) (bool, error) {
	return projectFixtureProvider(s).recordWorkTurn(th, turn)
}
func (s *Server) workInput(w session.ProjectWork, id, sender, brief string) *session.ProjectWorkInput {
	return projectFixtureProvider(s).workInput(w, id, sender, brief)
}
func (s *Server) reconcileProjectWork(w session.ProjectWork) error {
	p := projectFixtureProvider(s)
	return p.reconcileProjectWork(p.ctx, w)
}
func (s *Server) projectSessions(id string) ([]session.Session, error) {
	return projectFixtureProvider(s).projectSessions(id)
}
func (s *Server) drainProjectInboxPass(id string) bool {
	return projectFixtureProvider(s).drainProjectInboxPass(id)
}
func stopProjectFixture(s *Server) {
	if p, ok := projectFixtureProviders.LoadAndDelete(s); ok {
		_ = p.(*ProjectCoordinationProvider).Close(context.Background())
	}
}

func newProjectProviderTestServer(t *testing.T, source *runtime.Session, out io.Writer) *Server {
	t.Helper()
	rt := &runtime.Session{ProviderName: source.ProviderName, Model: source.Model, RootDir: source.RootDir, WorkspaceID: source.WorkspaceID, StateDir: source.StateDir, ConfigPath: source.ConfigPath, HomeDir: source.HomeDir, WuuHome: source.WuuHome, ConfigLoadMode: source.ConfigLoadMode, SessionDir: source.SessionDir, StreamRunner: source.StreamRunner, Toolkit: source.Toolkit, HookDispatcher: source.HookDispatcher, ProjectModels: source.ProjectModels}
	registry := pluginhost.NewServiceRegistry()
	state := persistence.NewService(persistence.SQLiteModule, filepath.Join(rt.SessionDir, "documents"), "sqlite")
	documents := persistence.NewClient(func(ctx context.Context, method string, params json.RawMessage) (json.RawMessage, error) {
		result, err := registry.Call(ctx, session.DefaultModule, pluginhost.ServiceCallParams{Service: persistence.ServiceName, Method: method, Params: params})
		if err != nil {
			return nil, err
		}
		return result, nil
	})
	rt.Documents = documents
	rt.Sessions = projectProviderStore(registry, ProjectCoordinationConsumer)
	sessions := session.NewService(session.DefaultModule, documents)
	registerTestWuuEngine(t, rt)
	bridge := NewProjectCoordinatorBridge()
	nativeClient, err := bridge.ProviderFactory().Build(runtime.NativeProviderContext{Documents: documents, Sessions: rt.Sessions, Registry: registry, RetainState: rt.RetainState})
	if err != nil {
		t.Fatal(err)
	}
	native := nativeClient.(*ProjectCoordinationProvider)
	mechanism, err := bridge.MechanismFactory().Build(runtime.NativeProviderContext{Sessions: rt.Sessions, Registry: registry, RetainState: rt.RetainState})
	if err != nil {
		t.Fatal(err)
	}
	consumer := NewProjectCoordinationConsumer()
	background := &projectBackgroundTestService{}
	configuration := config.NewService(config.Module, rt.ConfigPath, "")
	stateConsumer := &configSourceConsumer{}
	home := os.Getenv("HOME")
	credentials := credentialstore.NewService("test.selected-credentials", func() (credentialstore.Store, error) { return nativeCredentialFixture(t, home), nil })
	model := &embeddedModelStreamFixture{client: rt.StreamRunner.Client}
	clients := []pluginhost.Client{access.NewService(access.NativeModule, access.OwnerOnly{}), &embeddedAccessConsumer{}, background, native, state, sessions, consumer, mechanism, configuration, credentials, stateConsumer,
		model, &embeddedModelConfigurationConsumer{}, &embeddedCapacityFixture{leases: map[string]string{}}, &embeddedEngineConsumer{}, &embeddedEngineMetadata{factory: rt.WuuEngine()},
		promptservice.NewService(), titleservice.NewService(), &embeddedCompactionFixture{compactionservice.NewService()}, &embeddedModelConsumer{}, toolservice.NewService(), &embeddedToolsConsumer{}}
	conflicts := registry.RegisterClients(clients...)
	if len(conflicts) != 0 {
		t.Fatalf("project fixture graph: %v", conflicts)
	}
	host := pluginhost.New(clients...)
	background.host = host
	model.host = host
	host.AttachServiceRegistry(registry, conflicts)
	t.Cleanup(func() { _ = host.Close(context.Background()) })
	rt.PluginHost = host
	if err := host.Activate(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Server captures the generation, so its journal must already be bound.
	journal, err := rt.Sessions.NewInferenceJournalRuntime("project-fixture")
	if err != nil {
		t.Fatal(err)
	}
	rt.InferenceJournalRuntime = journal
	t.Cleanup(func() { _ = journal.Close() })
	server := New(rt, out)
	if _, err := bridge.Bind(server); err != nil {
		t.Fatal(err)
	}
	binding, err := rt.RetainState()
	if err != nil {
		t.Fatal(err)
	}
	view := *native
	view.Server, view.binding = server, binding
	view.ctx = server.projectServiceContext(context.Background(), binding)
	view.store = native.store.WithContext(view.ctx)
	projectFixtureProviders.Store(server, &view)
	t.Cleanup(func() { server.Close(); stopProjectFixture(server); _ = binding.Close() })
	select {
	case <-native.lifetime.schedulerReady:
	case <-time.After(5 * time.Second):
		t.Fatal("scheduler did not start")
	}
	return server
}

func (s *Server) projectActor(id string) (session.Session, session.Session, *session.Control, error) {
	return projectFixtureProvider(s).projectActor(id)
}
func (s *Server) waitProjectSession(ctx context.Context, project, actor session.Session, request tools.ProjectSessionRequest, clientID string) (any, error) {
	return projectFixtureProvider(s).waitProjectSession(ctx, project, actor, request, clientID)
}
func (s *Server) projectDispatchReceipt(metadata session.Session, id string) (projectSessionView, error) {
	return projectFixtureProvider(s).projectDispatchReceipt(metadata, id)
}
func (s *Server) loadDurableProjectTurns(id string) ([]Turn, error) {
	return projectFixtureProvider(s).loadDurableProjectTurns(id)
}
func (s *Server) ensureOwnedThreadLoaded(id string) (*threadState, error) {
	return projectFixtureProvider(s).ensureOwnedThreadLoaded(id)
}
func (s *Server) recoverProjectWork(id string) error {
	return projectFixtureProvider(s).recoverProjectWork(id)
}

func (s *Server) projectSessionHandler(id string) tools.ProjectSessionHandler {
	return func(ctx context.Context, callID string, request tools.ProjectSessionRequest) (any, error) {
		binding, err := s.rt.RetainState()
		if err != nil {
			return nil, err
		}
		defer binding.Close()
		return s.projectSessionHandlerForState(id, binding)(ctx, callID, request)
	}
}
func (s *Server) projectFixtureBinding() *runtime.State {
	binding, err := s.rt.RetainState()
	if err != nil {
		panic(err)
	}
	return binding
}
func (s *Server) drainSessionInbox(id string) {
	binding := s.projectFixtureBinding()
	defer binding.Close()
	s.drainSessionInboxForState(id, binding)
}
func (s *Server) acquireProjectWorkTool(member session.Session, call providers.ToolCall, base agent.ToolExecutor) (func(), error) {
	binding, err := s.rt.RetainState()
	if err != nil {
		return nil, err
	}
	release, err := s.acquireProjectWorkToolForBinding(binding, member, call, base)
	if err != nil {
		_ = binding.Close()
		return release, err
	}
	return func() { release(); _ = binding.Close() }, nil
}
func (s *Server) projectTestWork(ctx context.Context, actorID, callID string, request tools.ProjectWorkRequest) (any, error) {
	binding, err := s.rt.RetainState()
	if err != nil {
		return nil, err
	}
	defer binding.Close()
	return s.projectWork(binding, ctx, actorID, callID, request)
}
func (s *Server) projectTestCoordinator(id string) (session.Session, bool) {
	binding := s.projectFixtureBinding()
	defer binding.Close()
	return s.projectCoordinator(binding, id)
}
func (s *Server) projectTestRecoverInbox() {
	binding := s.projectFixtureBinding()
	defer binding.Close()
	s.recoverProjectInbox(binding)
}
func (s *Server) projectTestNoticeUserMessage(th *threadState, message providers.ChatMessage) {
	binding := th.state.Retain()
	defer binding.Close()
	s.noticeProjectUserMessage(binding, th, message)
}
func (s *Server) projectTestKickInboxDrain(id string) {
	binding := s.projectFixtureBinding()
	defer binding.Close()
	s.kickProjectInboxDrain(binding, id)
}
