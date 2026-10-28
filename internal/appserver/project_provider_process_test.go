package appserver

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/composition"
	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/persistence"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/runtime"
	"github.com/blueberrycongee/wuu/internal/session"
	"github.com/blueberrycongee/wuu/internal/tools"
)

const projectProviderHelperEnv = "WUU_PROJECT_PROVIDER_HELPER"

// This second provider is a separate process with a different work policy. It
// stores its own business record through state.documents, without calling the
// native coordinator or modifying the app-server's implementation.
func TestProjectCoordinationExternalReplacement(t *testing.T) {
	if os.Getenv(projectProviderHelperEnv) == "1" {
		runProjectProviderHelper()
		return
	}
	if !projectAgentEnabled {
		t.Skip("project_agent build only")
	}
	registry, err := pluginhost.NewServiceRegistryWithBindings([]composition.Binding{{Service: ProjectCoordinationService, Major: 1, Provider: "independent.coordinator"}})
	if err != nil {
		t.Fatal(err)
	}
	external, err := pluginhost.Start(context.Background(), pluginhost.ProcessConfig{ID: "independent.coordinator", Command: os.Args[0], Args: []string{"-test.run=^TestProjectCoordinationExternalReplacement$"}, Env: map[string]string{projectProviderHelperEnv: "1"}, PluginRoot: t.TempDir(), ProjectRoot: t.TempDir(), WuuHome: t.TempDir(), Timeout: 5 * time.Second, ServiceRouter: registry, PrepareOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = external.Close(context.Background()) })
	state := persistence.NewService(persistence.SQLiteModule, t.TempDir(), "sqlite")
	nativeDocuments := persistence.NewClient(func(ctx context.Context, method string, params json.RawMessage) (json.RawMessage, error) {
		result, err := registry.Call(ctx, session.DefaultModule, pluginhost.ServiceCallParams{Service: persistence.ServiceName, Method: method, Params: params})
		if err != nil {
			return nil, err
		}
		return result, nil
	})
	srv := New(newTestRuntime(t, &fakeClient{}), &lockedBuffer{})
	t.Cleanup(srv.Close)
	sessions := session.NewService(session.DefaultModule, nativeDocuments)
	store := projectProviderStore(registry, ProjectCoordinationModule)
	native := srv.NewProjectCoordinationProvider(store)
	native.registry = registry
	bridge := native.bridge
	mechanism, err := bridge.MechanismFactory().Build(runtime.NativeProviderContext{Sessions: store, Registry: registry, RetainState: func() (*runtime.State, error) { return native.retainState() }})
	if err != nil {
		t.Fatal(err)
	}
	consumer := NewProjectCoordinationConsumer()
	background := &projectBackgroundTestService{}
	configuration := config.NewMemoryService("independent.config", config.Default())
	clients := []pluginhost.Client{background, native, external, state, sessions, consumer, mechanism, configuration}
	conflicts := registry.RegisterClients(clients...)
	if len(conflicts) != 0 {
		t.Fatalf("conflicts: %v", conflicts)
	}
	callbacks := make(chan *projectCallScope, 2)
	if err := registry.WrapServiceInvoker(ManagedSessionService, 1, func(invoker pluginhost.ServiceInvoker) pluginhost.ServiceInvoker {
		return &projectMechanismScopeProbe{ServiceInvoker: invoker, callbacks: callbacks}
	}); err != nil {
		t.Fatal(err)
	}
	host := pluginhost.New(clients...)
	background.host = host
	host.AttachServiceRegistry(registry, conflicts)
	bindProjectTestGraph(t, native, host)
	if err := host.Activate(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = host.Close(context.Background()) })
	if native.startBackground(func(*ProjectCoordinationProvider) { t.Error("unselected native coordinator scheduled") }) {
		t.Fatal("binding did not suppress native scheduling")
	}
	request := ProjectCoordinationRequest{ActorID: "project-external", CallID: "work-one", Work: tools.ProjectWorkRequest{Operation: "create", Brief: "Review manually"}}
	var result projectWorkView
	if err := callProjectCoordination(context.Background(), host, "work", request, &result); err != nil {
		t.Fatal(err)
	}
	if result.Phase != "manual-review" || result.ID != "work-one" {
		t.Fatalf("external policy not selected: %+v", result)
	}
	var restored projectWorkView
	request.Work.Operation = "get"
	if err := callProjectCoordination(context.Background(), host, "work", request, &restored); err != nil {
		t.Fatal(err)
	}
	if restored.ID != result.ID || restored.Phase != result.Phase || restored.Brief != result.Brief {
		t.Fatalf("external persisted work changed: %+v", restored)
	}
	// A process callback must keep the originating connection even after a
	// second connection registers. Closing the first must preserve the second.
	second := New(srv.rt, &lockedBuffer{})
	t.Cleanup(second.Close)
	if _, err := bridge.Bind(second); err != nil {
		t.Fatal(err)
	}
	backgroundOrigin := srv.projectServiceContext(context.Background(), native.binding)
	backgroundCall := func(key string) *pluginhost.HostServiceError {
		t.Helper()
		input, _ := json.Marshal(ProjectCoordinationRequest{SessionID: "missing-managed-session"})
		job, startErr := host.StartBackground(backgroundOrigin, external.ID(), pluginhost.BackgroundStartParams{Key: key, Service: ProjectCoordinationService, Method: "coordinator", Params: input})
		if startErr != nil {
			t.Fatal(startErr)
		}
		ticker := time.NewTicker(time.Millisecond)
		defer ticker.Stop()
		deadline := time.NewTimer(5 * time.Second)
		defer deadline.Stop()
		for {
			snapshot, inspectErr := host.InspectBackground(external.ID(), job.JobID)
			if inspectErr != nil {
				t.Fatal(inspectErr)
			}
			if snapshot.State != "running" {
				return snapshot.Error
			}
			select {
			case <-ticker.C:
			case <-deadline.C:
				t.Fatal("external background callback did not settle")
			}
		}
	}
	if err := callProjectCoordination(context.Background(), host, "coordinator", ProjectCoordinationRequest{SessionID: "missing-managed-session"}, nil); err == nil {
		t.Fatal("unscoped foreground callback acquired background authority")
	}
	for index, origin := range []*Server{srv, second} {
		if err := backgroundCall(fmt.Sprintf("external-background-%d", index)); err == nil || err.Code != "project_session_missing" {
			t.Fatalf("external background mechanism result: %v", err)
		}
		ctx := origin.projectServiceContext(context.Background(), native.binding)
		err := callProjectCoordination(ctx, host, "coordinator", ProjectCoordinationRequest{SessionID: "missing-managed-session"}, nil)
		var serviceErr *pluginhost.HostServiceError
		if !errors.As(err, &serviceErr) || serviceErr.Code != "project_session_missing" {
			t.Fatalf("nested mechanism result: %v", err)
		}
		select {
		case callback := <-callbacks:
			if callback.registration.server != origin || callback.state.Sessions != native.binding.Sessions {
				t.Fatal("process callback changed originating server or state")
			}
		case <-time.After(5 * time.Second):
			t.Fatal("process provider did not reach the managed-session mechanism")
		}
		if origin == srv {
			srv.Close()
		}
	}
	if err := callProjectCoordination(second.projectServiceContext(context.Background(), native.binding), host, "work", request, &restored); err != nil {
		t.Fatalf("connection close retired the shared provider: %v", err)
	}
	second.Close()
	if err := backgroundCall("external-background-disconnected"); err == nil || err.Code != "project_request_failed" {
		t.Fatalf("background without a live host connection: %v", err)
	}
	if _, ok := host.RetirePlugin(context.Background(), external.ID(), nil); !ok {
		t.Fatal("external provider not retired")
	}
	if err := callProjectCoordination(context.Background(), host, "work", request, &restored); err == nil {
		t.Fatal("disabled selection fell back to native coordinator")
	}
	if native.startBackground(func(*ProjectCoordinationProvider) { t.Error("native fallback scheduled") }) {
		t.Fatal("retirement enabled native fallback")
	}
}

func runProjectProviderHelper() {
	scanner := bufio.NewScanner(os.Stdin)
	encoder := json.NewEncoder(os.Stdout)
	write := func(id string, result any) {
		if encoder.Encode(map[string]any{"id": id, "result": result}) != nil {
			os.Exit(2)
		}
	}
	for scanner.Scan() {
		var request struct {
			ID     string          `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if json.Unmarshal(scanner.Bytes(), &request) != nil {
			os.Exit(3)
		}
		switch request.Method {
		case "initialize":
			write(request.ID, pluginhost.CapabilityInitializeResult{ProtocolVersion: pluginhost.CapabilityProtocolVersion, LifecycleVersion: pluginhost.RuntimeLifecycleVersion, ProvidedServices: []pluginhost.ServiceDescriptor{ProjectCoordinationDescriptor()}, RequiredServices: []pluginhost.ServiceRequirement{{Name: persistence.ServiceName, MajorVersion: 1, Required: true}, {Name: ManagedSessionService, MajorVersion: 1, Required: true}}})
		case "prepare", "activate", "service.changed", "execution.cancel":
			write(request.ID, struct{}{})
		case "shutdown":
			write(request.ID, struct{}{})
			return
		case pluginhost.ServiceInvokeMethod:
			var invoke pluginhost.ServiceInvokeParams
			var input ProjectCoordinationRequest
			if json.Unmarshal(request.Params, &invoke) != nil || json.Unmarshal(invoke.Params, &input) != nil {
				os.Exit(4)
			}
			call := func(service, method string, value, result any) *pluginhost.HostServiceError {
				params, _ := json.Marshal(value)
				raw, _ := json.Marshal(pluginhost.ServiceCallParams{Service: service, Method: method, ExecutionID: invoke.ExecutionID, Params: params})
				callID := "state-" + request.ID + "-" + method
				if encoder.Encode(pluginhost.HostServiceCall{ID: callID, Method: pluginhost.ServiceCallMethod, Params: raw}) != nil {
					os.Exit(6)
				}
				var response pluginhost.HostServiceResult
				for {
					if !scanner.Scan() {
						os.Exit(7)
					}
					var frame struct {
						ID     string `json:"id"`
						Method string `json:"method"`
					}
					if json.Unmarshal(scanner.Bytes(), &frame) != nil {
						os.Exit(7)
					}
					// Lifecycle requests can arrive while a host callback is pending.
					if frame.Method == "execution.cancel" || frame.Method == "service.changed" {
						write(frame.ID, struct{}{})
						continue
					}
					if frame.ID != callID || frame.Method != "" || json.Unmarshal(scanner.Bytes(), &response) != nil {
						os.Exit(7)
					}
					break
				}
				if response.Error != nil {
					return response.Error
				}
				if result != nil && json.Unmarshal(response.Result, result) != nil {
					os.Exit(8)
				}
				return nil
			}
			if invoke.Method == "coordinator" {
				err := call(ManagedSessionService, "interrupt", map[string]string{"session_id": input.SessionID}, nil)
				response := pluginhost.HostServiceResult{ID: request.ID, Error: err}
				if err == nil {
					response.Result = json.RawMessage(`{}`)
				}
				if encoder.Encode(response) != nil {
					os.Exit(12)
				}
				continue
			}
			if invoke.Method != "work" {
				os.Exit(5)
			}
			const namespace = "independent.project-work.v1"
			var view persistence.View
			if err := call(persistence.ServiceName, "read", persistence.Query{Namespace: namespace, Keys: []string{input.CallID}}, &view); err != nil {
				os.Exit(7)
			}
			var work projectWorkView
			if len(view.Documents) > 0 {
				if json.Unmarshal(view.Documents[0].Value, &work) != nil {
					os.Exit(9)
				}
			} else {
				if input.Work.Operation != "create" {
					os.Exit(10)
				}
				work = projectWorkView{ID: input.CallID, ProjectID: input.ActorID, Phase: "manual-review", Brief: input.Work.Brief, Revision: 1, Version: 1}
				value, _ := json.Marshal(work)
				if err := call(persistence.ServiceName, "commit", persistence.Transaction{ID: "external-work:" + input.CallID, ExpectedRevision: view.Revision, Writes: []persistence.Write{{Namespace: namespace, Key: input.CallID, Value: value}}}, nil); err != nil {
					os.Exit(7)
				}
			}
			write(request.ID, work)
		default:
			os.Exit(11)
		}
	}
}

// The probe observes the authenticated callback before forwarding to the real
// mechanism. It does not replace its routing, graph validation or store access.
type projectMechanismScopeProbe struct {
	pluginhost.ServiceInvoker
	callbacks chan<- *projectCallScope
}

func (p *projectMechanismScopeProbe) InvokeService(ctx context.Context, call pluginhost.ServiceInvokeParams) (json.RawMessage, error) {
	scope, err := projectScopeForCall(ctx, call)
	background := pluginhost.IsBackgroundExecution(ctx)
	if call.CallerScope != nil {
		background = background || pluginhost.IsBackgroundExecution(call.CallerScope.Context)
	}
	if err == nil && !background {
		p.callbacks <- scope
	}
	return p.ServiceInvoker.InvokeService(ctx, call)
}
