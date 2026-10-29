package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/executionenv"
	"github.com/blueberrycongee/wuu/internal/executionworker"
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
	mu          sync.Mutex
	registry    *pluginhost.ServiceRegistry
	processID   string
	environment tools.ProcessEnvironment
	key         string
	command     string
	timeoutMS   int64
}

func (*externalProcessTools) ProvidedServices() []pluginhost.ServiceDescriptor {
	return []pluginhost.ServiceDescriptor{toolservice.Descriptor()}
}
func (p *externalProcessTools) InvokeService(ctx context.Context, call pluginhost.ServiceInvokeParams) (json.RawMessage, error) {
	switch call.Method {
	case "resolve":
		return json.RawMessage(`{"found":true,"supports":true,"metadata":{"read_only":false,"risk":"high"}}`), nil
	case "execute":
		// Metadata uses the same authenticated callback and does not grant authority.
		location, err := p.registry.Call(ctx, p.ID(), pluginhost.ServiceCallParams{Service: "host.process", Method: "environment", ExecutionID: call.ExecutionID, Params: json.RawMessage(`{}`)})
		if err != nil {
			return nil, err
		}
		var environment tools.ProcessEnvironment
		if err := json.Unmarshal(location, &environment); err != nil {
			return nil, err
		}
		p.mu.Lock()
		p.environment = environment
		p.mu.Unlock()
		// Only this admitted callback execution can delegate to host.process.
		key := p.key
		if key == "" {
			key = "synthetic"
		}
		command := p.command
		if command == "" {
			command = "printf external-host-process"
		}
		startInput, _ := json.Marshal(pluginProcessStart{Key: key, Command: command, TimeoutMS: p.timeoutMS})
		raw, err := p.registry.Call(ctx, p.ID(), pluginhost.ServiceCallParams{Service: "host.process", Method: "start", ExecutionID: call.ExecutionID, Params: startInput})
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
	for _, mode := range []string{"local", "denied", "remote", "remote-denied", "remote-blocked", "remote-timeout", "remote-failure", "wrapped-readonly", "wrapped-first-readonly", "wrapped-first-allow", "wrapped-allow", "remote-wrapped-readonly", "direct-readonly"} {
		deny := strings.Contains(mode, "denied") || strings.Contains(mode, "readonly")
		t.Run(mode, func(t *testing.T) {
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
			if strings.Contains(mode, "denied") {
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
			processConfig := tools.ProcessScopeConfig{Manager: manager, RootDir: root, ThreadID: "thread", ActorID: "actor"}
			processRoot := root
			if strings.HasPrefix(mode, "remote") {
				remote := newRuntimeProcessEnvironment(t, "original")
				processConfig.ExecutionEnvironment = remote
				manager = remote.manager
				processRoot = remote.Root()
			}
			t.Cleanup(service.close)
			toolkitCreated := false
			executor := newSelectedToolExecutor(host, toolservice.NewScope(func(context.Context) (toolservice.Backend, error) {
				toolkitCreated = true
				return toolservice.Backend{}, errors.New("must not initialize default toolkit")
			}), selectedToolIdentity{threadID: "thread", actorID: "actor", cwd: root})
			runtime := &ThreadRuntime{PluginHost: host, StreamRunner: &agent.StreamRunner{Tools: executor}, processScopeConfig: processConfig}
			if strings.HasPrefix(mode, "wrapped-first-") {
				runtime.StreamRunner.Tools = &processScopeToolWrapper{ToolExecutor: executor}
			}
			runtime.ConfigureExecutionPermissions(config.ResolvedPermissions{Mode: "unconfined"}, false, nil)
			if strings.Contains(mode, "wrapped") && !strings.HasPrefix(mode, "wrapped-first-") {
				runtime.StreamRunner.Tools = &processScopeToolWrapper{ToolExecutor: executor}
			}
			if strings.Contains(mode, "readonly") || mode == "wrapped-allow" {
				runtime.ConfigureExecutionPermissions(config.ResolvedPermissions{Mode: "read_only"}, false, nil)
			}
			if mode == "wrapped-allow" {
				runtime.ConfigureExecutionPermissions(config.ResolvedPermissions{Mode: "unconfined"}, false, nil)
			}
			if mode == "remote-timeout" {
				provider.command = "printf external-host-process; read value"
				provider.timeoutMS = 30
			}
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
			started, err := manager.Get(service.processes[provider.processID].processID)
			if err != nil {
				t.Fatal(err)
			}
			canonicalRoot, err := filepath.EvalSymlinks(processRoot)
			if err != nil {
				t.Fatal(err)
			}
			if started.RootThreadID != "thread" || started.OwnerID != "actor" || started.CWD != canonicalRoot {
				t.Fatalf("wrong process identity: %+v", started)
			}
			if strings.HasPrefix(mode, "remote") && provider.processID == started.ID {
				t.Fatal("remote backend identifier escaped as an unscoped host handle")
			}
			if provider.environment.Root != processRoot {
				t.Fatalf("environment metadata points to wrong filesystem: %+v", provider.environment)
			}
			if mode == "remote-timeout" {
				input, _ := json.Marshal(map[string]any{"process_id": provider.processID, "offset_bytes": len("external-host-process"), "wait_ms": 1000})
				raw, serviceErr := host.CallService(turn, provider.ID(), "host.process", "read", input)
				if serviceErr != nil {
					t.Fatal(serviceErr)
				}
				var read struct {
					Process struct {
						State  proc.Status `json:"state"`
						Reason string      `json:"reason"`
					} `json:"process"`
				}
				if err := json.Unmarshal(raw, &read); err != nil || read.Process.State != proc.StatusStopped || read.Process.Reason != "timeout" {
					t.Fatalf("remote timeout not enforced: %s %v", raw, err)
				}
				return
			}
			if mode == "remote-failure" {
				remote := processConfig.ExecutionEnvironment.(*runtimeProcessEnvironment)
				remote.failure = errors.New("remote operation unknown; inspect original environment")
				provider.key = "failed-key"
				for i := 0; i < 2; i++ {
					_, err := executor.ExecuteResult(turn, providers.ToolCall{ID: fmt.Sprintf("failed-%d", i), Name: "external_process", Arguments: `{}`})
					if err == nil || !strings.Contains(err.Error(), "remote operation unknown") {
						t.Fatalf("missing retained failure: %v", err)
					}
				}
				if got := remote.starts.Load(); got != 2 {
					t.Fatalf("unknown operation was replayed: starts=%d", got)
				}
				return
			}
			if mode == "remote-blocked" {
				remote := processConfig.ExecutionEnvironment.(*runtimeProcessEnvironment)
				remote.entered = make(chan struct{}, 1)
				remote.release = make(chan struct{})
				provider.key = "blocked"
				done := make(chan error, 2)
				go func() {
					_, err := executor.ExecuteResult(turn, providers.ToolCall{ID: "blocked-call", Name: "external_process", Arguments: `{}`})
					done <- err
				}()
				<-remote.entered
				go func() {
					_, err := executor.ExecuteResult(turn, providers.ToolCall{ID: "duplicate-call", Name: "external_process", Arguments: `{}`})
					done <- err
				}()
				input, _ := json.Marshal(map[string]string{"process_id": provider.processID})
				stopped := make(chan error, 1)
				go func() {
					_, err := host.CallService(turn, provider.ID(), "host.process", "stop", input)
					if err != nil {
						stopped <- err
					} else {
						stopped <- nil
					}
				}()
				select {
				case err := <-stopped:
					if err != nil {
						t.Fatal(err)
					}
				case <-time.After(2 * time.Second):
					close(remote.release)
					t.Fatal("remote preparation blocked control of an accepted process")
				}
				closed := make(chan struct{})
				go func() { service.close(); close(closed) }()
				select {
				case <-closed:
				case <-time.After(2 * time.Second):
					close(remote.release)
					t.Fatal("generation closure did not cancel pending remote start")
				}
				for i := 0; i < 2; i++ {
					if err := <-done; err == nil {
						t.Fatal("cancelled pending start succeeded")
					}
				}
				if got := remote.starts.Load(); got != 2 {
					t.Fatalf("same key replayed remote start: calls=%d", got)
				}
				return
			}
			if strings.HasPrefix(mode, "remote") {
				replacement := newRuntimeProcessEnvironment(t, "replacement")
				runtime.processScopeConfig.ExecutionEnvironment = replacement
				runtime.ConfigureExecutionPermissions(config.ResolvedPermissions{Mode: "unconfined"}, false, nil)
				input, _ := json.Marshal(map[string]string{"process_id": provider.processID})
				raw, serviceErr := host.CallService(turn, provider.ID(), "host.process", "stop", input)
				if serviceErr != nil {
					t.Fatal(serviceErr)
				}
				var stopped struct {
					Environment tools.ProcessEnvironment `json:"environment"`
				}
				if err = json.Unmarshal(raw, &stopped); err != nil || stopped.Environment.Identity != "original" || stopped.Environment.Root != processRoot {
					t.Fatalf("accepted process retargeted: %s err=%v", raw, err)
				}
				processes, err := replacement.manager.List()
				if err != nil || len(processes) != 0 {
					t.Fatalf("replacement affected: %+v %v", processes, err)
				}
			}
			if len(security.invocations) != 1 {
				t.Fatalf("authorization calls=%d", len(security.invocations))
			}
			if _, err := host.CallService(turn, provider.ID(), "host.process", "environment", json.RawMessage(`{}`)); err == nil {
				t.Fatal("unscoped metadata acquired process authority")
			}
			if _, err := host.CallService(turn, provider.ID(), "host.process", "start", json.RawMessage(`{"key":"forged","command":"printf forbidden"}`)); err == nil {
				t.Fatal("unscoped host call acquired process authority")
			}
		})
	}
}

// The fixture crosses the real worker authority and remote-manager protocol,
// with a separate root; it must never execute through the host manager.
type runtimeProcessEnvironment struct {
	server           *executionworker.Server
	manager          *proc.Manager
	root, identity   string
	entered, release chan struct{}
	starts           atomic.Int32
	failure          error
}

func newRuntimeProcessEnvironment(t *testing.T, identity string) *runtimeProcessEnvironment {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("WUU_HOME", t.TempDir())
	r := &runtimeProcessEnvironment{server: executionworker.New(), root: t.TempDir(), identity: identity}
	raw, _ := json.Marshal(executionenv.Init{Version: executionenv.ProtocolVersion, Root: r.root, Session: "thread", Identity: identity})
	if _, err := r.server.Handle(context.Background(), "initialize", raw); err != nil {
		t.Fatal(err)
	}
	r.manager = proc.NewRemoteManager(r.root, r)
	t.Cleanup(func() { _ = r.server.Close() })
	return r
}
func (r *runtimeProcessEnvironment) Root() string                  { return r.root }
func (r *runtimeProcessEnvironment) Backend() string               { return "ssh" }
func (r *runtimeProcessEnvironment) Identity() string              { return r.identity }
func (r *runtimeProcessEnvironment) ProcessManager() *proc.Manager { return r.manager }
func (r *runtimeProcessEnvironment) Execute(context.Context, executionenv.ToolRequest) (toolresult.Result, error) {
	return toolresult.Result{}, errors.New("unexpected native tool fallback")
}

func (r *runtimeProcessEnvironment) StartProcess(ctx context.Context, request executionenv.ProcessRequest) (*proc.Process, error) {
	r.starts.Add(1)
	if r.failure != nil {
		return nil, r.failure
	}
	if r.entered != nil {
		select {
		case r.entered <- struct{}{}:
		default:
		}
		select {
		case <-r.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	var started proc.Process
	err := r.Request(ctx, "start", request, &started)
	return &started, err
}
func (r *runtimeProcessEnvironment) Request(ctx context.Context, method string, params, result any) error {
	raw, err := json.Marshal(params)
	if err != nil {
		return err
	}
	response, err := r.server.Handle(ctx, "process/"+method, raw)
	if err != nil {
		return err
	}
	return json.Unmarshal(response, result)
}
func TestSelectedProcessEnvironmentInspectionDoesNotProvision(t *testing.T) {
	environment := executionenv.NewEnvironment(executionenv.Profile{Backend: "ssh", Host: "invalid.example", Workspace: "/remote"}, "pinned", "thread", t.TempDir())
	scope := tools.NewProcessScope(tools.ProcessScopeConfig{ExecutionEnvironment: environment, RootDir: t.TempDir(), ThreadID: "thread", ActorID: "actor"})
	// No transport is needed: a nonsensical remote host must still be inspectable.
	done := make(chan error, 1)
	go func() {
		location, err := scope.Environment()
		if err == nil && (location.Identity != "pinned" || location.Backend != "ssh" || location.Root != "/remote/pinned") {
			err = errors.New("incorrect remote metadata")
		}
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("inspection attempted remote provisioning")
	}
}

// Independent backends may reuse IDs. Public reads/stops and timeout cleanup
// must all route through the exact manager captured by each host handle.
type collidingProcessTransport struct {
	mu           sync.Mutex
	label        string
	record       proc.Process
	reads, stops int
	stopped      chan struct{}
}

func (t *collidingProcessTransport) Request(ctx context.Context, method string, params, result any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	rawParams, _ := json.Marshal(params)
	var address struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(rawParams, &address); err != nil || address.ID != t.record.ID {
		return errors.New("host handle used as a backend process address")
	}
	var value any
	switch method {
	case "get":
		value = t.record
	case "read":
		t.reads++
		value = proc.OutputSnapshot{Process: t.record, Output: t.label, EndOffset: int64(len(t.label)), TotalBytes: int64(len(t.label))}
	case "stop":
		t.stops++
		t.record.Status = proc.StatusStopped
		value = t.record
		if t.stops == 1 {
			close(t.stopped)
		}
	default:
		return fmt.Errorf("unexpected process operation %s", method)
	}
	raw, _ := json.Marshal(value)
	return json.Unmarshal(raw, result)
}
func TestProcessHandlesSeparateBackendIdentity(t *testing.T) {
	ctx := context.Background()
	documents, err := persistence.Open(ctx, t.TempDir(), "jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer documents.Close()
	generation := &PluginGeneration{state: &runtimeState{status: pluginhost.StateActive, documents: documents, sessions: session.NewStore(ctx, documents)}}
	generation.refs.Store(1)
	kernel := newKernelHostServices(func() uint64 { return 1 }, nil)
	kernel.active = true
	kernel.stateGeneration = generation
	router := &PluginSessionRouter{}
	router.BindProcessSessions(func(_ context.Context, id string, _ bool) (string, bool, error) { return id, false, nil })
	service := newPluginHostServices(pluginpkg.Plugin{ID: "owner"}, t.TempDir(), t.TempDir(), router)
	service.activate()
	kernel.add("owner", service)
	defer service.close()
	invoker := &processServiceInvoker{parent: kernel}
	firstTransport := &collidingProcessTransport{label: "first", record: proc.Process{ID: "same-id", Status: proc.StatusRunning}, stopped: make(chan struct{})}
	secondTransport := &collidingProcessTransport{label: "second", record: proc.Process{ID: "same-id", Status: proc.StatusRunning}, stopped: make(chan struct{})}
	first := newPluginProcess(&firstTransport.record, proc.NewRemoteManager("/first", firstTransport), "session", pluginProcessStart{}, tools.ProcessEnvironment{Backend: "command", Root: "/first", Identity: "first"}, ctx)
	second := newPluginProcess(&secondTransport.record, proc.NewRemoteManager("/second", secondTransport), "session", pluginProcessStart{TimeoutMS: 30}, tools.ProcessEnvironment{Backend: "command", Root: "/second", Identity: "second"}, ctx)
	if first.id == second.id || first.processID != "same-id" || second.processID != "same-id" {
		t.Fatal("remote identifiers were not scoped")
	}
	service.processes = map[string]*pluginProcess{first.id: first, second.id: second}
	call := func(method, id string) json.RawMessage {
		t.Helper()
		input, _ := json.Marshal(map[string]any{"process_id": id})
		raw, err := invoker.InvokeService(ctx, pluginhost.ServiceInvokeParams{Caller: "owner", Method: method, Params: input})
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	for _, p := range []*pluginProcess{first, second} {
		var out struct {
			Output  string `json:"output"`
			Process struct {
				ID string `json:"id"`
			} `json:"process"`
		}
		if err := json.Unmarshal(call("read", p.id), &out); err != nil || out.Output != p.environment.Identity || out.Process.ID != p.id {
			t.Fatalf("read crossed environment: %+v %v", out, err)
		}
	}
	call("stop", first.id)
	select {
	case <-secondTransport.stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("second environment timeout did not stop its process")
	}
	var stopped struct {
		ID     string `json:"id"`
		Reason string `json:"reason"`
	}
	if err := json.Unmarshal(call("stop", second.id), &stopped); err != nil || stopped.ID != second.id || stopped.Reason != "timeout" {
		t.Fatalf("timeout receipt changed handle: %+v %v", stopped, err)
	}
	firstTransport.mu.Lock()
	firstReads, firstStops := firstTransport.reads, firstTransport.stops
	firstTransport.mu.Unlock()
	secondTransport.mu.Lock()
	secondReads, secondStops := secondTransport.reads, secondTransport.stops
	secondTransport.mu.Unlock()
	if firstReads != 1 || secondReads != 1 || firstStops != 1 || secondStops != 1 {
		t.Fatalf("wrong backend control counts: first=%d/%d second=%d/%d", firstReads, firstStops, secondReads, secondStops)
	}
}

// processScopeToolWrapper models a host policy decorator and preserves its
// underlying selected provider identity without granting tool authority.
type processScopeToolWrapper struct{ agent.ToolExecutor }

func (w *processScopeToolWrapper) UnwrapToolExecutor() agent.ToolExecutor { return w.ToolExecutor }
