package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/composition"
	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/hooks"
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
	"github.com/stretchr/testify/require"
)

type continuationBarrier struct {
	*runtimeServiceConsumer
	entered, release chan struct{}
	once             sync.Once
}

func (*continuationBarrier) ProvidedServices() []pluginhost.ServiceDescriptor {
	return []pluginhost.ServiceDescriptor{{Name: "fixture.control", Version: "1.0.0", Methods: []pluginhost.ServiceMethodDescriptor{{Name: "barrier", InputSchema: "fixture.barrier.input.v1", OutputSchema: "fixture.barrier.output.v1"}}}}
}
func (b *continuationBarrier) InvokeService(ctx context.Context, _ pluginhost.ServiceInvokeParams) (json.RawMessage, error) {
	b.once.Do(func() { close(b.entered) })
	select {
	case <-b.release:
		return json.RawMessage(`{}`), nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// The policy may change while an admitted decorator is paused. Retaining the
// invocation must not retain a permission decision made before the actual effect.
type continuationSecurity struct {
	*securityServiceProvider
	mu sync.Mutex
}

func (s *continuationSecurity) InvokeService(ctx context.Context, params pluginhost.ServiceInvokeParams) (json.RawMessage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.securityServiceProvider.InvokeService(ctx, params)
}
func (s *continuationSecurity) setDecision(decision json.RawMessage) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.decision = decision
}
func (s *continuationSecurity) invocationCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.invocations)
}

type continuationProcessGraph struct {
	host       *pluginhost.Host
	executor   *selectedToolExecutor
	manager    *proc.Manager
	barrier    *continuationBarrier
	generation *PluginGeneration
	security   *continuationSecurity
}

func newContinuationProcessGraph(t *testing.T, label string, chain bool, deny bool, native ...bool) *continuationProcessGraph {
	t.Helper()
	fixture := os.Getenv("WUU_CONTINUATION_FIXTURE")
	if fixture == "" {
		t.Skip("run node scripts/service-continuation-e2e.mjs")
	}
	node, err := exec.LookPath("node")
	require.NoError(t, err)
	root := t.TempDir()
	manager, err := proc.NewManager(root, t.TempDir())
	require.NoError(t, err)
	documents, err := persistence.Open(context.Background(), t.TempDir(), "jsonl")
	require.NoError(t, err)
	t.Cleanup(func() { _ = documents.Close() })
	state := &runtimeState{status: pluginhost.StateActive, documents: documents, sessions: session.NewStore(context.Background(), documents)}
	kernel := newKernelHostServices(func() uint64 { return 1 }, nil)
	kernel.active = true
	kernel.stateGeneration = &PluginGeneration{state: state}
	kernel.stateGeneration.refs.Store(1)
	router := &PluginSessionRouter{}
	router.BindProcessSessions(func(_ context.Context, id string, _ bool) (string, bool, error) { return id, false, nil })
	baseID, wrapperID := "independent.process-tools", "independent.tool-decorator"
	nativeDelegate := len(native) != 0 && native[0]
	if nativeDelegate {
		baseID = toolservice.DefaultModule
	}
	bindings := []composition.Binding{{Service: toolservice.ServiceName, Major: 1, Provider: wrapperID}, {Consumer: wrapperID, Service: toolservice.ServiceName, Major: 1, Provider: baseID}}
	if chain {
		bindings[1].Provider = "independent.inner-decorator"
		bindings = append(bindings, composition.Binding{Consumer: "independent.inner-decorator", Service: toolservice.ServiceName, Major: 1, Provider: baseID})
	}
	registry, err := pluginhost.NewServiceRegistryWithBindings(bindings)
	require.NoError(t, err)
	registry.SetGraphGeneration(label)
	clients := []pluginhost.Client{}
	plugins := []pluginpkg.Plugin{}
	start := func(id, file string, args ...string) {
		client, err := pluginhost.Start(context.Background(), pluginhost.ProcessConfig{ID: id, Command: node, Args: append([]string{filepath.Join(fixture, file)}, args...), PluginRoot: fixture, ProjectRoot: root, WuuHome: t.TempDir(), ServiceRouter: registry, PrepareOnly: true, Timeout: 10 * time.Second})
		require.NoError(t, err)
		clients = append(clients, client)
		plugins = append(plugins, pluginpkg.Plugin{Manifest: pluginpkg.Manifest{ID: id}})
	}
	if nativeDelegate {
		clients = append(clients, toolservice.NewService())
		require.NoError(t, os.WriteFile(filepath.Join(root, "sample.txt"), []byte(label), 0600))
	} else {
		start(baseID, "tools.js", label)
	}
	start(wrapperID, "decorator.js")
	if chain {
		start("independent.inner-decorator", "tool-service-decorator.js")
	}
	services := newPluginHostServices(pluginpkg.Plugin{ID: baseID}, root, t.TempDir(), router)
	services.activate()
	kernel.add(baseID, services)
	barrier := &continuationBarrier{runtimeServiceConsumer: &runtimeServiceConsumer{id: "fixture.control", state: pluginhost.StatePrepared}, entered: make(chan struct{}), release: make(chan struct{})}
	security := &continuationSecurity{securityServiceProvider: &securityServiceProvider{descriptors: []pluginhost.ServiceDescriptor{pluginhost.SecurityAuthorizeDescriptor()}, decision: json.RawMessage(`{"outcome":"allow"}`)}}
	if deny {
		security.setDecision(json.RawMessage(`{"outcome":"deny","reason":"fixture denial"}`))
	}
	consumer := &runtimeServiceConsumer{id: toolExecutionConsumer, state: pluginhost.StatePrepared, requirements: []pluginhost.ServiceRequirement{{Name: toolservice.ServiceName, MajorVersion: 1, Required: true}, {Name: pluginhost.SecurityAuthorizeService, MajorVersion: 1, Required: true}}}
	clients = append(clients, &processGatewayFixture{&processServiceInvoker{parent: kernel}}, security, consumer, barrier)
	require.Empty(t, registry.RegisterClients(clients...))
	require.NoError(t, registry.ValidateBindings())
	host := pluginhost.New(clients...)
	host.AttachServiceRegistry(registry, nil)
	t.Cleanup(func() { _ = host.Close(context.Background()) })
	require.NoError(t, host.Activate(context.Background()))
	executor := newSelectedToolExecutor(host, toolservice.NewScope(func(context.Context) (toolservice.Backend, error) {
		if nativeDelegate {
			kit, err := tools.New(root)
			return toolservice.Backend{Executor: kit, Toolkit: kit}, err
		}
		t.Error("native toolkit fallback initialized")
		return toolservice.Backend{}, errors.New("native toolkit forbidden")
	}), selectedToolIdentity{threadID: "thread", actorID: "actor", cwd: root})
	runtime := &ThreadRuntime{PluginHost: host, StreamRunner: &agent.StreamRunner{Tools: executor}, processScopeConfig: tools.ProcessScopeConfig{Manager: manager, RootDir: root, ThreadID: "thread", ActorID: "actor"}}
	runtime.ConfigureExecutionPermissions(config.ResolvedPermissions{Mode: "unconfined"}, false, nil)
	// Keep the real generation publication/revocation path aware of every module.
	for _, client := range clients {
		found := false
		for _, p := range plugins {
			found = found || p.ID == client.ID()
		}
		if !found {
			plugins = append(plugins, pluginpkg.Plugin{Manifest: pluginpkg.Manifest{ID: client.ID()}})
		}
	}
	generation := &PluginGeneration{id: label, host: host, hooks: hooks.NewDispatcher(nil), plugins: plugins, active: plugins}
	return &continuationProcessGraph{host: host, executor: executor, manager: manager, barrier: barrier, generation: generation, security: security}
}
func (g *continuationProcessGraph) execute(ctx context.Context, id, name, args string) (toolresult.Result, error) {
	return g.executor.ExecuteResult(loopdriver.WithExecutionContext(ctx, loopdriver.ExecutionContext{SessionID: "thread", ExecutionID: "turn"}), providers.ToolCall{ID: id, Name: name, Arguments: args})
}
func TestServiceContinuationSDKProcess(t *testing.T) {
	for _, mode := range []string{"single", "chain", "ordinary", "denied", "failure", "duplicate"} {
		t.Run(mode, func(t *testing.T) {
			graph := newContinuationProcessGraph(t, "base-v1", mode == "chain", mode == "denied")
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			name, args := "process", `{}`
			if mode == "ordinary" {
				args = `{"ordinary":true}`
			}
			if mode == "duplicate" {
				args = `{"duplicate":true}`
			}
			if mode == "failure" {
				name = "fail"
			}
			result, err := graph.execute(ctx, "call", name, args)
			if mode == "ordinary" || mode == "denied" || mode == "failure" {
				require.Error(t, err)
				if mode == "ordinary" {
					require.Contains(t, err.Error(), "scope")
				}
				processes, listErr := graph.manager.List()
				require.NoError(t, listErr)
				require.Empty(t, processes, "rejection must not create a process record")
				return
			}
			require.NoError(t, err)
			require.Equal(t, "base-v1", result.TextProjection())
			context := graph.executor.TakeAdditionalContext(providers.ToolCall{ID: "call"})
			require.Contains(t, context, "base:base-v1")
			require.Equal(t, map[bool]int{false: 1, true: 2}[mode == "chain"], strings.Count(context, "selected decorator"))
			require.Equal(t, 1, graph.security.invocationCount(), "duplicate must not start a second process")
			raw, _ := json.Marshal(result.StructuredContent)
			var detail struct {
				ID string `json:"process_id"`
			}
			require.NoError(t, json.Unmarshal(raw, &detail))
			started, err := graph.manager.Get(detail.ID)
			require.NoError(t, err)
			require.Equal(t, "thread", started.RootThreadID)
			require.Equal(t, "actor", started.OwnerID)
		})
	}
}
func TestServiceContinuationSDKReplacementAndRemoval(t *testing.T) {
	for _, mode := range []string{"replace", "cancel", "remove", "remove-owner", "revoke-permission"} {
		t.Run(mode, func(t *testing.T) {
			old := newContinuationProcessGraph(t, "old-process", false, false)
			owner := testGenerationSession(t, old.generation)
			defer owner.Cleanup()
			retained := owner.RetainPluginGeneration()
			defer owner.ReleasePluginGeneration(retained)
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			type outcome struct {
				result toolresult.Result
				err    error
			}
			done := make(chan outcome, 1)
			go func() {
				result, err := old.execute(ctx, "old", "process", `{"pause":true}`)
				done <- outcome{result, err}
			}()
			select {
			case <-old.barrier.entered:
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			switch mode {
			case "replace":
				next := newContinuationProcessGraph(t, "new-process", false, false)
				require.NoError(t, owner.ActivatePluginGeneration(next.generation, nil))
				result, err := next.execute(ctx, "new", "process", `{}`)
				require.NoError(t, err)
				require.Equal(t, "new-process", result.TextProjection())
			case "cancel":
				cancel()
			case "remove", "remove-owner":
				removed := "independent.process-tools"
				if mode == "remove-owner" {
					removed = "independent.tool-decorator"
				}
				outcome, found := old.host.RetirePlugin(ctx, removed, errors.New("explicit removal"))
				require.True(t, found)
				require.NoError(t, outcome.Err)
			case "revoke-permission":
				old.security.setDecision(json.RawMessage(`{"outcome":"deny","reason":"permission revoked while paused"}`))
			}
			close(old.barrier.release)
			select {
			case result := <-done:
				if mode == "replace" {
					require.NoError(t, result.err)
					require.Equal(t, "old-process", result.result.TextProjection())
					require.Equal(t, 1, old.security.invocationCount())
				} else {
					require.Error(t, result.err)
					if mode == "revoke-permission" {
						require.Equal(t, 1, old.security.invocationCount())
					} else {
						require.Zero(t, old.security.invocationCount())
					}
					processes, listErr := old.manager.List()
					require.NoError(t, listErr)
					require.Empty(t, processes)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("old callback did not settle")
			}
			if mode == "remove" || mode == "remove-owner" {
				_, err := old.execute(context.Background(), "after-remove", "process", `{}`)
				require.Error(t, err)
			}
			t.Log("paused-before-next: " + mode + " settled through the original owner and explicit authority boundary")
		})
	}
}

// The default implementation must not use its private backend scope as a
// privileged ordinary-call shortcut unavailable to an external implementation.
func TestServiceContinuationSDKNativeDelegate(t *testing.T) {
	for _, mode := range []string{"continued", "ordinary", "list", "resolve"} {
		t.Run(mode, func(t *testing.T) {
			graph := newContinuationProcessGraph(t, "native-delegate-result", false, false, true)
			args := `{"path":"sample.txt"}`
			if mode == "ordinary" {
				args = `{"path":"sample.txt","ordinary":true}`
			}
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			if mode == "list" || mode == "resolve" {
				_, err := graph.host.CallService(toolservice.WithScope(ctx, graph.executor.scope), toolExecutionConsumer, toolservice.ServiceName, mode, json.RawMessage(`{"probe_native_execute":true}`))
				require.Error(t, err, "ordinary metadata callback must not execute rewritten native tool params")
				require.Contains(t, err.Error(), "scope")
				return
			}
			result, err := graph.execute(ctx, "native", "read_file", args)
			if mode == "ordinary" {
				require.Error(t, err, "native implementation must require the same selected tool authority as an external provider")
				require.Contains(t, err.Error(), "scope")
				return
			}
			require.NoError(t, err)
			require.Contains(t, result.TextProjection(), "native-delegate-result")
			require.Contains(t, graph.executor.TakeAdditionalContext(providers.ToolCall{ID: "native"}), "selected decorator")
		})
	}
}
