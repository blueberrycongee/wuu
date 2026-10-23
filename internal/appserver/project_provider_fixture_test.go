//go:build project_agent

package appserver

import (
	"context"
	"encoding/json"
	"io"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/persistence"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/runtime"
	"github.com/blueberrycongee/wuu/internal/session"
	"github.com/blueberrycongee/wuu/internal/tools"
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
	return projectFixtureProvider(s).reconcileProjectWork(context.Background(), w)
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
	registerTestWuuEngine(t, rt)
	registry := pluginhost.NewServiceRegistry()
	state := persistence.NewService(persistence.SQLiteModule, filepath.Join(rt.SessionDir, "documents"), "sqlite")
	documents := persistence.NewClient(func(ctx context.Context, method string, params json.RawMessage) (json.RawMessage, error) {
		result, err := registry.Call(ctx, ProjectCoordinationConsumer, pluginhost.ServiceCallParams{Service: persistence.ServiceName, Method: method, Params: params})
		if err != nil {
			return nil, err
		}
		return result, nil
	})
	rt.Documents = documents
	rt.Sessions = session.NewStore(context.Background(), documents)
	native := newProjectCoordinationProvider(rt.Sessions)
	native.registry = registry
	bridge := NewProjectCoordinatorBridge()
	mechanism, err := bridge.MechanismFactory().Build(runtime.NativeProviderContext{Sessions: rt.Sessions, Registry: registry})
	if err != nil {
		t.Fatal(err)
	}
	consumer := NewProjectCoordinationConsumer()
	background := &projectBackgroundTestService{}
	clients := []pluginhost.Client{background, native, state, consumer, mechanism}
	conflicts := registry.RegisterClients(clients...)
	if len(conflicts) != 0 {
		t.Fatalf("project fixture graph: %v", conflicts)
	}
	host := pluginhost.New(clients...)
	background.host = host
	host.AttachServiceRegistry(registry, conflicts)
	if err := host.Activate(context.Background()); err != nil {
		t.Fatal(err)
	}
	rt.PluginHost = host
	server := New(rt, out)
	projectFixtureProviders.Store(server, native)
	bridge.Bind(server)
	if err := native.bindServer(server); err != nil {
		t.Fatal(err)
	}
	select {
	case <-native.lifetime.schedulerReady:
	case <-time.After(5 * time.Second):
		t.Fatal("scheduler did not start")
	}
	t.Cleanup(func() { server.Close(); stopProjectFixture(server); _ = host.Close(context.Background()) })
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
	return s.projectSessionHandlerForHost(id, s.rt.PluginHost)
}
func (s *Server) drainSessionInbox(id string) { s.drainSessionInboxForHost(id, s.rt.PluginHost) }
func (s *Server) acquireProjectWorkTool(member session.Session, call providers.ToolCall, base agent.ToolExecutor) (func(), error) {
	return s.acquireProjectWorkToolForBinding(member, call, base, s.rt.PluginHost, s.rt.Sessions)
}
