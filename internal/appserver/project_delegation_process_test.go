//go:build project_agent

package appserver

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/composition"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/session"
	"github.com/blueberrycongee/wuu/internal/tools"
)

// A public SDK decorator must keep delegated scheduling, not just synchronous
// replies. Stopping autonomous work must preserve settlement in the old graph;
// removing the decorator must never activate a direct native fallback.
func TestProjectCoordinationSDKDecorator(t *testing.T) {
	fixture := os.Getenv("WUU_DELEGATION_FIXTURE")
	if fixture == "" {
		t.Skip("run node scripts/provider-delegation-e2e.mjs")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	for _, scenario := range []struct {
		depth  int
		retire bool
	}{{1, false}, {2, false}, {1, true}} {
		depth := scenario.depth
		name := strings.Repeat("decorator-", depth) + "native"
		if scenario.retire {
			name += "-remove"
		}
		t.Run(name, func(t *testing.T) {
			bindings := []composition.Binding{{Service: ProjectCoordinationService, Major: 1, Provider: "external.outer"}, {Consumer: "external.outer", Service: ProjectCoordinationService, Major: 1, Provider: ProjectCoordinationModule}}
			ids := []string{"external.outer"}
			if depth == 2 {
				ids = append(ids, "external.inner")
				bindings[1].Provider = "external.inner"
				bindings = append(bindings, composition.Binding{Consumer: "external.inner", Service: ProjectCoordinationService, Major: 1, Provider: ProjectCoordinationModule})
			}
			registry, err := pluginhost.NewServiceRegistryWithBindings(bindings)
			if err != nil {
				t.Fatal(err)
			}
			var clients []pluginhost.Client
			entered, release := make(chan struct{}), make(chan struct{})
			unblock := sync.OnceFunc(func() { close(release) })
			defer unblock()
			for _, id := range ids {
				process, err := pluginhost.Start(context.Background(), pluginhost.ProcessConfig{ID: id, Command: node, Args: []string{filepath.Join(fixture, "project-decorator.js")}, PluginRoot: fixture, ProjectRoot: t.TempDir(), WuuHome: t.TempDir(), ServiceRouter: registry, PrepareOnly: true, Timeout: 5 * time.Second})
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = process.Close(context.Background()) })
				if id == ids[0] {
					clients = append(clients, &projectSettlementGate{ProcessClient: process, entered: entered, release: release})
				} else {
					clients = append(clients, process)
				}
			}
			rt := newTestRuntime(t, &fakeClient{})
			initAppserverGitRepo(t, rt.RootDir)
			kit, err := tools.New(rt.RootDir)
			if err != nil {
				t.Fatal(err)
			}
			rt.Toolkit = kit
			rt.WorkspaceID = "delegated-project"
			model := newGatedProvider(16)
			rt.StreamRunner.Client = providers.AdaptStreamClient(model)
			out := &lockedBuffer{}
			srv := newProjectProviderTestServerGraph(t, rt, out, registry, clients...)
			client := &rpcClient{server: srv, out: out}
			calls := &projectCalls{provider: model}
			root := startProject(t, client, "Delegated project")
			metadata, found, err := srv.rt.Sessions.Find(root.ID)
			if err != nil || !found {
				t.Fatalf("project persistence: %v %v", found, err)
			}
			instructions, err := ProjectInstructions(context.Background(), srv.rt.PluginHost, metadata)
			if err != nil || strings.Count(instructions, "Report concrete verification evidence.") != depth {
				t.Fatalf("instruction decoration: %q %v", instructions, err)
			}
			native := projectFixtureProvider(srv)
			retire := func() {
				t.Helper()
				outcome, found := srv.rt.PluginHost.RetirePlugin(context.Background(), ids[0], nil)
				if !found || outcome.Err != nil {
					t.Fatalf("retire decorator: %+v %v", outcome, found)
				}
				raw, _ := json.Marshal(session.Session{Source: projectSource})
				if _, err := srv.rt.PluginHost.CallService(context.Background(), ProjectCoordinationConsumer, ProjectCoordinationService, "instructions", raw); err == nil {
					t.Fatal("removed decorator fell back to native policy")
				}
				if native.selected() {
					t.Fatal("removed decorator left native in the selected chain")
				}
				if native.startBackground(func(*ProjectCoordinationProvider) { t.Error("removed decorator enabled fallback") }) {
					t.Fatal("removed decorator revived scheduler")
				}
			}
			if scenario.retire {
				enteredPass, resumePass := make(chan struct{}), make(chan struct{})
				resume := sync.OnceFunc(func() { close(resumePass) })
				defer resume()
				allowed := make(chan bool, 1)
				if !native.startBackground(func(operation *ProjectCoordinationProvider) {
					close(enteredPass)
					<-resumePass
					allowed <- operation.schedulingAllowed()
				}) {
					t.Fatal("selected coordinator did not admit the pending pass")
				}
				<-enteredPass
				retire()
				resume()
				if <-allowed {
					t.Fatal("already admitted pass retained scheduling authority after its decorator was removed")
				}
				return
			}
			value, err := srv.projectSessionHandler(root.ID)(context.Background(), "queued-worker", tools.ProjectSessionRequest{Action: "create", Prompt: "Run through the delegated scheduler", Workspace: "shared"})
			if err != nil {
				t.Fatal(err)
			}
			worker := value.(projectSessionView)
			held := calls.next(t, worker.SessionID, "Run through the delegated scheduler")
			original := srv.thread(worker.SessionID).state
			if original.Host != srv.rt.PluginHost {
				t.Fatal("accepted task lost its selected graph")
			}
			held.response <- providersResponse("Completed in the admitted decorated graph")
			select {
			case <-entered:
			case <-time.After(5 * time.Second):
				t.Fatal("completion did not reach its admitted decorator callback")
			}
			srv.rt.PluginHost.StopBackground()
			select {
			case <-native.lifetime.ctx.Done():
			case <-time.After(5 * time.Second):
				t.Fatal("delegated scheduler did not stop")
			}
			if native.startBackground(func(*ProjectCoordinationProvider) { t.Error("stopped generation dispatched") }) {
				t.Fatal("new background admission after stop")
			}
			unblock()
			waitForThread(t, srv, worker.SessionID, func(th Thread) bool {
				return th.Status == ThreadStatusIdle && th.LatestCompletedTurnID == worker.TurnID
			})
			drained := make(chan struct{})
			go func() { srv.backgroundWG.Wait(); close(drained) }()
			select {
			case <-drained:
			case <-time.After(5 * time.Second):
				t.Fatal("accepted turn did not drain in its original graph")
			}
			settled := session.NewClient(context.Background(), func(ctx context.Context, method string, params json.RawMessage) (json.RawMessage, error) {
				result, callErr := original.Host.CallService(ctx, ProjectCoordinationConsumer, session.ServiceName, method, params)
				if callErr != nil {
					return nil, callErr
				}
				return result, nil
			})
			pending, err := settled.PendingInbox(root.ID)
			if err != nil || len(pending) != 1 || !strings.Contains(pending[0].Content, "Completed in the admitted decorated graph") {
				t.Fatalf("retained completion: %+v %v", pending, err)
			}
			calls.assertIdle(t)
			if srv.thread(worker.SessionID).state.Host != original.Host {
				t.Fatal("completion rebound accepted task")
			}
			retire()
		})
	}
}

// Hold the admitted callback before its real external SDK handler. No routing,
// caller identity, declaration or execution lifetime is replaced by the gate.
type projectSettlementGate struct {
	*pluginhost.ProcessClient
	entered chan struct{}
	release <-chan struct{}
}

func (g *projectSettlementGate) InvokeService(ctx context.Context, call pluginhost.ServiceInvokeParams) (json.RawMessage, error) {
	if call.Method == "turn" {
		close(g.entered)
		select {
		case <-g.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return g.ProcessClient.InvokeService(ctx, call)
}
