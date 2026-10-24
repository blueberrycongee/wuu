package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/blueberrycongee/wuu/internal/loopdriver"
	"github.com/blueberrycongee/wuu/internal/persistence"
	pluginpkg "github.com/blueberrycongee/wuu/internal/plugin"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	proc "github.com/blueberrycongee/wuu/internal/process"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/session"
	"github.com/blueberrycongee/wuu/internal/toolresult"
	"github.com/blueberrycongee/wuu/internal/tools"
	"github.com/blueberrycongee/wuu/internal/toolservice"
)

type processGatewayFixture struct{ *processServiceInvoker }

func (*processGatewayFixture) ProvidedServices() []pluginhost.ServiceDescriptor {
	return []pluginhost.ServiceDescriptor{pluginhost.KernelProcessDescriptor()}
}
func (*processGatewayFixture) RequiredServices() []pluginhost.ServiceRequirement { return nil }
func (p *processGatewayFixture) Close(ctx context.Context) error                 { return p.parent.Close(ctx) }

type externalProcessTools struct {
	*runtimeServiceConsumer
	registry  *pluginhost.ServiceRegistry
	processID string
}

func (*externalProcessTools) ProvidedServices() []pluginhost.ServiceDescriptor {
	return []pluginhost.ServiceDescriptor{toolservice.Descriptor()}
}
func (p *externalProcessTools) InvokeService(ctx context.Context, call pluginhost.ServiceInvokeParams) (json.RawMessage, error) {
	switch call.Method {
	case "resolve":
		return json.RawMessage(`{"found":true,"supports":true,"metadata":{"read_only":false,"risk":"high"}}`), nil
	case "execute":
		// Only this admitted callback execution can delegate to host.process.
		raw, err := p.registry.Call(ctx, p.ID(), pluginhost.ServiceCallParams{Service: "host.process", Method: "start", ExecutionID: call.ExecutionID, Params: json.RawMessage(`{"key":"synthetic","command":"printf external-host-process"}`)})
		if err != nil {
			return nil, err
		}
		var started struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(raw, &started); err != nil {
			return nil, err
		}
		p.processID = started.ID
		input, _ := json.Marshal(map[string]any{"process_id": started.ID, "wait_ms": 1000, "max_bytes": 1024})
		raw, err = p.registry.Call(ctx, p.ID(), pluginhost.ServiceCallParams{Service: "host.process", Method: "read", ExecutionID: call.ExecutionID, Params: input})
		if err != nil {
			return nil, err
		}
		var output struct {
			Output string `json:"output"`
		}
		if err := json.Unmarshal(raw, &output); err != nil {
			return nil, err
		}
		return json.Marshal(toolservice.ExecutionResult{Result: toolresult.FromText(output.Output)})
	default:
		return nil, errors.New("unexpected tool service call")
	}
}

func TestExternalToolProviderUsesHostProcessWithoutToolkit(t *testing.T) {
	for _, deny := range []bool{false, true} {
		t.Run(map[bool]string{false: "allowed", true: "denied"}[deny], func(t *testing.T) {
			ctx := context.Background()
			root := t.TempDir()
			manager, err := proc.NewManager(root, t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			documents, err := persistence.Open(ctx, t.TempDir(), "jsonl")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = documents.Close() })
			state := &runtimeState{status: pluginhost.StateActive, documents: documents, sessions: session.NewStore(ctx, documents)}
			generation := &PluginGeneration{state: state}
			generation.refs.Store(1)
			kernel := newKernelHostServices(func() uint64 { return 1 }, nil)
			kernel.active = true
			kernel.stateGeneration = generation
			router := &PluginSessionRouter{}
			router.BindProcessSessions(func(_ context.Context, id string, _ bool) (string, bool, error) { return id, false, nil })
			provider := &externalProcessTools{runtimeServiceConsumer: &runtimeServiceConsumer{id: "external-process-tools", state: pluginhost.StatePrepared, requirements: []pluginhost.ServiceRequirement{{Name: "host.process", MajorVersion: 1, Required: true}}}}
			service := newPluginHostServices(pluginpkg.Plugin{ID: provider.ID()}, root, t.TempDir(), router)
			service.activate()
			kernel.add(provider.ID(), service)
			gateway := &processGatewayFixture{&processServiceInvoker{parent: kernel}}
			security := &securityServiceProvider{descriptors: []pluginhost.ServiceDescriptor{pluginhost.SecurityAuthorizeDescriptor()}, decision: json.RawMessage(`{"outcome":"allow"}`)}
			if deny {
				security.decision = json.RawMessage(`{"outcome":"deny","reason":"fixture denial"}`)
			}
			consumer := &runtimeServiceConsumer{id: toolExecutionConsumer, state: pluginhost.StatePrepared, requirements: []pluginhost.ServiceRequirement{{Name: toolservice.ServiceName, MajorVersion: 1, Required: true}, {Name: pluginhost.SecurityAuthorizeService, MajorVersion: 1, Required: true}}}
			clients := []pluginhost.Client{provider, gateway, security, consumer}
			registry, conflicts := pluginhost.BuildServiceRegistry(clients...)
			if len(conflicts) != 0 {
				t.Fatal(conflicts)
			}
			host := pluginhost.New(clients...)
			host.AttachServiceRegistry(registry, conflicts)
			provider.registry = registry
			t.Cleanup(func() { _ = host.Close(ctx) })
			if err := host.Activate(ctx); err != nil {
				t.Fatal(err)
			}
			processScope := newHostProcessScope(host, tools.ProcessScopeConfig{Manager: manager, RootDir: root, ThreadID: "thread", ActorID: "actor", PermissionMode: "unconfined", Boundary: tools.UnconfinedBoundary()})
			toolkitCreated := false
			executor := newSelectedToolExecutor(host, toolservice.NewScope(func(context.Context) (toolservice.Backend, error) {
				toolkitCreated = true
				return toolservice.Backend{}, errors.New("must not initialize default toolkit")
			}), selectedToolIdentity{threadID: "thread", actorID: "actor", cwd: root, processScope: processScope})
			turn := loopdriver.WithExecutionContext(ctx, loopdriver.ExecutionContext{SessionID: "thread", ExecutionID: "turn"})
			result, err := executor.ExecuteResult(turn, providers.ToolCall{ID: "call", Name: "external_process", Arguments: `{"thread_id":"forged"}`})
			if toolkitCreated {
				t.Fatal("external process initialized default Toolkit")
			}
			if deny {
				if err == nil || provider.processID != "" {
					t.Fatalf("denied command started: id=%s err=%v", provider.processID, err)
				}
				return
			}
			if err != nil || !strings.Contains(result.TextProjection(), "external-host-process") {
				t.Fatalf("actual process output: %+v %v", result, err)
			}
			started, err := manager.Get(provider.processID)
			if err != nil {
				t.Fatal(err)
			}
			canonicalRoot, err := filepath.EvalSymlinks(root)
			if err != nil {
				t.Fatal(err)
			}
			if started.RootThreadID != "thread" || started.OwnerID != "actor" || started.CWD != canonicalRoot {
				t.Fatalf("wrong process identity: %+v", started)
			}
			if len(security.invocations) != 1 {
				t.Fatalf("authorization calls=%d", len(security.invocations))
			}
			if _, err := host.CallService(turn, provider.ID(), "host.process", "start", json.RawMessage(`{"key":"forged","command":"printf forbidden"}`)); err == nil {
				t.Fatal("unscoped host call acquired process authority")
			}
		})
	}
}
