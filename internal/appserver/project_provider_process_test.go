package appserver

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/composition"
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
		result, err := registry.Call(ctx, ProjectCoordinationModule, pluginhost.ServiceCallParams{Service: persistence.ServiceName, Method: method, Params: params})
		if err != nil {
			return nil, err
		}
		return result, nil
	})
	srv := New(newTestRuntime(t, &fakeClient{}), &lockedBuffer{})
	t.Cleanup(srv.Close)
	native := srv.NewProjectCoordinationProvider(session.NewStore(context.Background(), nativeDocuments))
	native.registry = registry
	bridge := NewProjectCoordinatorBridge()
	bridge.Bind(srv)
	mechanism, err := bridge.MechanismFactory().Build(runtime.NativeProviderContext{Sessions: session.NewStore(context.Background(), nativeDocuments), Registry: registry})
	if err != nil {
		t.Fatal(err)
	}
	consumer := NewProjectCoordinationConsumer()
	background := &projectBackgroundTestService{}
	clients := []pluginhost.Client{background, native, external, state, consumer, mechanism}
	conflicts := registry.RegisterClients(clients...)
	if len(conflicts) != 0 {
		t.Fatalf("conflicts: %v", conflicts)
	}
	host := pluginhost.New(clients...)
	background.host = host
	host.AttachServiceRegistry(registry, conflicts)
	if err := host.Activate(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = host.Close(context.Background()) })
	if native.startBackground(func() { t.Error("unselected native coordinator scheduled") }) {
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
	if _, ok := host.RetirePlugin(context.Background(), external.ID(), nil); !ok {
		t.Fatal("external provider not retired")
	}
	if err := callProjectCoordination(context.Background(), host, "work", request, &restored); err == nil {
		t.Fatal("disabled selection fell back to native coordinator")
	}
	if native.startBackground(func() { t.Error("native fallback scheduled") }) {
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
			write(request.ID, pluginhost.CapabilityInitializeResult{ProtocolVersion: pluginhost.CapabilityProtocolVersion, LifecycleVersion: pluginhost.RuntimeLifecycleVersion, ProvidedServices: []pluginhost.ServiceDescriptor{ProjectCoordinationDescriptor()}, RequiredServices: []pluginhost.ServiceRequirement{{Name: persistence.ServiceName, MajorVersion: 1, Required: true}}})
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
			if invoke.Method != "work" {
				os.Exit(5)
			}
			call := func(method string, value, result any) {
				params, _ := json.Marshal(value)
				raw, _ := json.Marshal(pluginhost.ServiceCallParams{Service: persistence.ServiceName, Method: method, ExecutionID: invoke.ExecutionID, Params: params})
				if encoder.Encode(pluginhost.HostServiceCall{ID: "state-" + request.ID + "-" + method, Method: pluginhost.ServiceCallMethod, Params: raw}) != nil || !scanner.Scan() {
					os.Exit(6)
				}
				var response pluginhost.HostServiceResult
				if json.Unmarshal(scanner.Bytes(), &response) != nil || response.Error != nil {
					os.Exit(7)
				}
				if result != nil && json.Unmarshal(response.Result, result) != nil {
					os.Exit(8)
				}
			}
			const namespace = "independent.project-work.v1"
			var view persistence.View
			call("read", persistence.Query{Namespace: namespace, Keys: []string{input.CallID}}, &view)
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
				call("commit", persistence.Transaction{ID: "external-work:" + input.CallID, ExpectedRevision: view.Revision, Writes: []persistence.Write{{Namespace: namespace, Key: input.CallID, Value: value}}}, nil)
			}
			write(request.ID, work)
		default:
			os.Exit(11)
		}
	}
}
