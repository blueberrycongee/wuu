package runtime

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/config"
	pluginpkg "github.com/blueberrycongee/wuu/internal/plugin"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
)

func TestKernelHostServicesRouteThroughRegistryAndPreserveStorageScope(t *testing.T) {
	if root := os.Getenv("WUU_FILE_LOCK_E2E_HELPER"); root != "" {
		handler := newPluginHostServices(serviceTestPlugin("helper", "plugin:user:helper", "one"), root, root, nil)
		handler.activate()
		defer handler.close()
		if _, err := handler.invokeFileLock(context.Background(), "acquire", json.RawMessage(`{"root":"workspace_state","path":"cross-process.lock"}`), root); err != nil {
			t.Fatal(err)
		}
		fmt.Println("file-lock-ready")
		select {}
	}
	home, workspace := t.TempDir(), t.TempDir()
	item := serviceTestPlugin("demo", "plugin:user:demo", "generation")
	handler := newPluginHostServices(item, workspace, home, nil)
	kernel := newKernelHostServices(nil, nil)
	kernel.add(item.ID, handler)
	registry, conflicts := pluginhost.BuildServiceRegistry(kernel)
	kernel.bindRegistry(registry)
	if len(conflicts) != 0 {
		t.Fatalf("conflicts = %+v", conflicts)
	}
	for _, descriptor := range pluginhost.KernelServiceDescriptors() {
		if !registry.HasProvider(descriptor.Name, 1) {
			t.Fatalf("kernel service %q was not registered", descriptor.Name)
		}
	}
	registry.AllowPreflight(item.ID, pluginhost.KernelPreflightRequirements())

	if _, serviceErr := registry.Call(context.Background(), item.ID, pluginhost.ServiceCallParams{
		Service: pluginhost.KernelStorageSetService, Method: pluginhost.KernelServiceMethod,
		Params: json.RawMessage(`{"scope":"workspace","key":"state","value":"prepared"}`),
	}); serviceErr == nil || serviceErr.Code != "service_not_authorized" {
		t.Fatalf("prepare write error = %#v", serviceErr)
	}

	registry.RegisterClients(&kernelConsumer{id: item.ID, requirements: pluginhost.KernelServiceRequirements(
		pluginhost.KernelStorageGetService, pluginhost.KernelStorageSetService,
	)})
	registry.Activate()
	if _, serviceErr := registry.Call(context.Background(), item.ID, pluginhost.ServiceCallParams{
		Service: pluginhost.KernelStorageSetService, Method: pluginhost.KernelServiceMethod,
		Params: json.RawMessage(`{"scope":"workspace","key":"state","value":"active"}`),
	}); serviceErr != nil {
		t.Fatal(serviceErr)
	}
	result, serviceErr := registry.Call(context.Background(), item.ID, pluginhost.ServiceCallParams{
		Service: pluginhost.KernelStorageGetService, Method: pluginhost.KernelServiceMethod,
		Params: json.RawMessage(`{"scope":"workspace","key":"state"}`),
	})
	if serviceErr != nil || string(result) != `{"value":"active"}` {
		t.Fatalf("get = %s, error = %#v", result, serviceErr)
	}

	t.Run("file lock boundaries and process recovery", func(t *testing.T) {
		kernel.bindWorkspaceStateDir(workspace)
		registry.RegisterClients(&kernelConsumer{id: item.ID, requirements: pluginhost.KernelServiceRequirements(pluginhost.KernelFileLockService)})
		lockCall := func(ctx context.Context, method string, value any) (json.RawMessage, *pluginhost.HostServiceError) {
			raw, _ := json.Marshal(value)
			return registry.Call(ctx, item.ID, pluginhost.ServiceCallParams{Service: pluginhost.KernelFileLockService, Method: method, Params: raw})
		}
		for _, path := range []string{"../outside.lock", filepath.Join(workspace, "absolute.lock")} {
			if _, err := lockCall(context.Background(), "acquire", pluginhost.FileLockAcquireParams{Root: "workspace_state", Path: path}); err == nil {
				t.Fatalf("accepted unsafe path %q", path)
			}
		}
		outside := t.TempDir()
		if err := os.Symlink(outside, filepath.Join(workspace, "outside")); err == nil {
			if _, err := lockCall(context.Background(), "acquire", pluginhost.FileLockAcquireParams{Root: "workspace_state", Path: "outside/escape.lock"}); err == nil {
				t.Fatal("accepted symlink escape")
			}
		}
		helper := exec.Command(os.Args[0], "-test.run=^TestKernelHostServicesRouteThroughRegistryAndPreserveStorageScope$")
		helper.Env = append(os.Environ(), "WUU_FILE_LOCK_E2E_HELPER="+workspace)
		stdout, err := helper.StdoutPipe()
		if err != nil {
			t.Fatal(err)
		}
		var stderr bytes.Buffer
		helper.Stderr = &stderr
		if err := helper.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = helper.Process.Kill() })
		scanner := bufio.NewScanner(stdout)
		if !scanner.Scan() || scanner.Text() != "file-lock-ready" {
			t.Fatalf("lock helper failed: %s %s", scanner.Text(), stderr.String())
		}
		blocked, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
		_, lockErr := lockCall(blocked, "acquire", pluginhost.FileLockAcquireParams{Root: "workspace_state", Path: "cross-process.lock"})
		cancel()
		if lockErr == nil {
			t.Fatal("cross-process OS lock did not exclude second owner")
		}
		if err := helper.Process.Kill(); err != nil {
			t.Fatal(err)
		}
		_ = helper.Wait()
		ctx, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		acquired, lockErr := lockCall(ctx, "acquire", pluginhost.FileLockAcquireParams{Root: "workspace_state", Path: "cross-process.lock"})
		if lockErr != nil {
			t.Fatal(lockErr)
		}
		var token pluginhost.FileLockAcquireResult
		if err := json.Unmarshal(acquired, &token); err != nil {
			t.Fatal(err)
		}
		other := newPluginHostServices(serviceTestPlugin("other", "plugin:user:other", "one"), workspace, home, nil)
		other.activate()
		defer other.close()
		releaseRaw, _ := json.Marshal(pluginhost.FileLockReleaseParams{LockID: token.LockID})
		foreign, err := other.invokeFileLock(ctx, "release", releaseRaw, workspace)
		if err != nil || string(foreign) != `{"released":false}` {
			t.Fatalf("foreign release = %s, %v", foreign, err)
		}
		cleanupLock, lockErr := lockCall(ctx, "acquire", pluginhost.FileLockAcquireParams{Root: "workspace_state", Path: "retirement.lock"})
		if lockErr != nil {
			t.Fatal(lockErr)
		}
		var cleanupToken pluginhost.FileLockAcquireResult
		if err := json.Unmarshal(cleanupLock, &cleanupToken); err != nil {
			t.Fatal(err)
		}
		retiringHost := pluginhost.New()
		retiringHost.AttachServiceRegistry(registry, nil)
		retiringHost.StopBackground()
		if _, err := lockCall(ctx, "acquire", pluginhost.FileLockAcquireParams{Root: "workspace_state", Path: "late.lock"}); err == nil || err.Code != "generation_closed" {
			t.Fatalf("retired timer reacquired a lock: %v", err)
		}
		if released, err := lockCall(ctx, "release", pluginhost.FileLockReleaseParams{LockID: cleanupToken.LockID}); err != nil || string(released) != `{"released":true}` {
			t.Fatalf("retained cleanup could not release its owned lock: %s, %v", released, err)
		}
		handler.close()
		acquireRaw, _ := json.Marshal(pluginhost.FileLockAcquireParams{Root: "workspace_state", Path: "cross-process.lock"})
		if _, err := other.invokeFileLock(ctx, "acquire", acquireRaw, workspace); err != nil {
			t.Fatalf("generation close leaked lock: %v", err)
		}
		tracker := pluginhost.NewExecutionTracker()
		id := tracker.Begin("owner")
		if _, handled, err := tracker.ResolveContext("foreign", id); !handled || err == nil || err.Code != "service_not_authorized" {
			t.Fatalf("foreign root context = %v, %v", handled, err)
		}
		tracker.End(id)
		if _, handled, err := tracker.ResolveContext("owner", id); !handled || err == nil || err.Code != "execution_not_found" {
			t.Fatalf("stale root context = %v, %v", handled, err)
		}
		t.Log("OS cross-process exclusion, cancellation, crash release, generation release, path confinement and root-owner rejection passed")
	})

	t.Run("TypeScript memory runtime and nested service locks", func(t *testing.T) {
		node, err := exec.LookPath("node")
		if err != nil {
			t.Skip("Node.js is required for the TypeScript process journey")
		}
		memoryRoot, err := filepath.Abs("../plugin/bundled/memory")
		if err != nil {
			t.Fatal(err)
		}
		fixtureRoot := t.TempDir()
		fixture := `import { createInterface } from 'node:readline';
const send=x=>process.stdout.write(JSON.stringify(x)+'\n');let seq=0;const pending=new Map();
const tool=(id)=>({id,description:id,input_schema:{type:'object',properties:{content:{type:'string'}},additionalProperties:false}});
createInterface({input:process.stdin}).on('line',line=>{const q=JSON.parse(line);if(!q.method){const f=pending.get(q.id);if(f){pending.delete(q.id);f(q)}return}if(q.method==='execution.cancel')return;
if(q.method==='initialize'){send({id:q.id,result:{protocol_version:3,lifecycle_version:1,tools:['append','hold','crash'].map(tool),required_services:[{name:'memory.session',major_version:1,required:true},{name:'host.file-lock',major_version:1,required:true}]}});return}
if(q.method==='activate'||q.method==='shutdown'||q.method==='service.changed'){send({id:q.id,result:null});return}
if(q.method==='tool.execute'){if(q.params.tool_id==='crash'){process.exit(3)}const id='fixture-'+(++seq);pending.set(id,r=>send(r.error?{id:q.id,error:r.error}:{id:q.id,result:{result:{content:[{type:'text',text:JSON.stringify(r.result)}]}}}));const hold=q.params.tool_id==='hold';send({id,method:'host.service.call',params:{service:hold?'host.file-lock':'memory.session',method:hold?'acquire':'append',execution_id:q.params.execution_id,params:hold?{root:'workspace_state',path:'held.lock'}:{thread_id:'shared',target:'notes',content:q.params.arguments.content,source:'nested'}}});}});`
		if err := os.WriteFile(filepath.Join(fixtureRoot, "runtime.mjs"), []byte(fixture), 0o600); err != nil {
			t.Fatal(err)
		}
		stateDir, userHome, project := t.TempDir(), t.TempDir(), t.TempDir()
		makeHost := func(withConsumer bool) (*pluginhost.Host, *kernelHostServices) {
			memory := serviceTestPlugin("memory", "plugin:user:memory", "one")
			memory.Root = memoryRoot
			memory.Runtime = &pluginpkg.RuntimeSpec{Command: node, Args: []string{"runtime.mjs"}, Timeout: 5}
			items := []pluginpkg.Plugin{memory}
			if withConsumer {
				consumer := serviceTestPlugin("consumer", "plugin:user:consumer", "one")
				consumer.Root = fixtureRoot
				consumer.Runtime = &pluginpkg.RuntimeSpec{Command: node, Args: []string{"runtime.mjs"}, Timeout: 5}
				items = append(items, consumer)
			}
			host, services, err := buildPluginHost(context.Background(), items, project, "workspace", userHome, stateDir, nil, func(ctx context.Context, cfg pluginhost.ProcessConfig) (pluginhost.Client, error) {
				return pluginhost.Start(ctx, cfg)
			}, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = host.Close(context.Background()) })
			if err := activatePluginHost(context.Background(), host); err != nil {
				t.Fatal(err)
			}
			return host, services
		}
		first, firstKernel := makeHost(true)
		second, _ := makeHost(false)
		run := func(ctx context.Context, host *pluginhost.Host, pluginID, toolID string, args any) error {
			for _, def := range host.ToolDefinitions() {
				tool, ok := host.Tool(def.Name)
				if !ok || tool.PluginID != pluginID || tool.Registration.ID != toolID {
					continue
				}
				raw, _ := json.Marshal(args)
				_, err := host.ExecuteTool(ctx, def.Name, pluginhost.ToolExecuteInput{SessionID: "shared", ThreadID: "shared", TurnID: "turn", CallID: "call", CWD: project, Arguments: raw})
				return err
			}
			return fmt.Errorf("missing %s/%s", pluginID, toolID)
		}
		ctx, stop := context.WithTimeout(context.Background(), 20*time.Second)
		defer stop()
		var workers sync.WaitGroup
		errs := make(chan error, 24)
		for i := 0; i < 24; i++ {
			workers.Add(1)
			go func(i int) {
				defer workers.Done()
				host := first
				if i%2 != 0 {
					host = second
				}
				errs <- run(ctx, host, "memory", "session_memory", map[string]string{"action": "append", "target": "notes", "content": fmt.Sprintf("memory-line-%02d", i)})
			}(i)
		}
		workers.Wait()
		close(errs)
		for err := range errs {
			if err != nil {
				t.Fatal(err)
			}
		}
		notesPath := filepath.Join(stateDir, "sessions", "shared", "memory", "notes.md")
		notes, err := os.ReadFile(notesPath)
		if err != nil {
			t.Fatal(err)
		}
		for i := 0; i < 24; i++ {
			if strings.Count(string(notes), fmt.Sprintf("memory-line-%02d", i)) != 1 {
				t.Fatalf("lost or repeated append %d: %s", i, notes)
			}
		}
		if err := run(ctx, first, "consumer", "append", map[string]string{"content": "nested-root-append"}); err != nil {
			t.Fatalf("nested service append: %v", err)
		}
		lockRaw, _ := json.Marshal(pluginhost.FileLockAcquireParams{Root: "workspace_state", Path: "sessions/shared/memory/notes.md.lock"})
		blocker := newPluginHostServices(serviceTestPlugin("blocker", "plugin:user:blocker", "one"), project, userHome, nil)
		blocker.activate()
		defer blocker.close()
		if _, err := blocker.invokeFileLock(ctx, "acquire", lockRaw, stateDir); err != nil {
			t.Fatal(err)
		}
		cancelled, cancel := context.WithTimeout(ctx, 80*time.Millisecond)
		err = run(cancelled, first, "consumer", "append", map[string]string{"content": "must-not-be-written"})
		cancel()
		if err == nil {
			t.Fatal("nested lock wait ignored root cancellation")
		}
		blocker.close()
		if err := run(ctx, first, "consumer", "append", map[string]string{"content": "after-cancel"}); err != nil {
			t.Fatalf("post-cancellation append: %v", err)
		}
		notes, err = os.ReadFile(notesPath)
		if err != nil || !strings.Contains(string(notes), "nested-root-append") || !strings.Contains(string(notes), "after-cancel") || strings.Contains(string(notes), "must-not-be-written") {
			t.Fatalf("nested persistence = %s, %v", notes, err)
		}
		if err := run(ctx, first, "consumer", "hold", map[string]string{}); err != nil {
			t.Fatal(err)
		}
		if err := run(ctx, first, "consumer", "crash", map[string]string{}); err == nil {
			t.Fatal("fixture did not crash")
		}
		memoryServices := firstKernel.services["memory"]
		heldRaw, _ := json.Marshal(pluginhost.FileLockAcquireParams{Root: "workspace_state", Path: "held.lock"})
		if _, err := memoryServices.invokeFileLock(ctx, "acquire", heldRaw, stateDir); err != nil {
			t.Fatalf("plugin process crash leaked lock: %v", err)
		}
		t.Log("two real TypeScript processes: 24 concurrent appends preserved; nested root context/cancellation recovered; process EOF released held host lock")
	})

	registry.Close(context.Background())
	if _, serviceErr := registry.Call(context.Background(), item.ID, pluginhost.ServiceCallParams{
		Service: pluginhost.KernelStorageGetService, Method: pluginhost.KernelServiceMethod,
		Params: json.RawMessage(`{"scope":"workspace","key":"state"}`),
	}); serviceErr == nil || serviceErr.Code != "service_unavailable" {
		t.Fatalf("closed error = %#v", serviceErr)
	}
}

func TestKernelRegistryIntrospectRoutesThroughRegistry(t *testing.T) {
	home, workspace := t.TempDir(), t.TempDir()
	item := serviceTestPlugin("demo", "plugin:user:demo", "generation")
	handler := newPluginHostServices(item, workspace, home, nil)
	kernel := newKernelHostServices(func() uint64 { return 42 }, nil)
	kernel.add(item.ID, handler)
	registry, conflicts := pluginhost.BuildServiceRegistry(kernel)
	kernel.bindRegistry(registry)
	if len(conflicts) != 0 {
		t.Fatalf("conflicts = %+v", conflicts)
	}
	registry.RegisterClients(&kernelConsumer{id: item.ID, requirements: pluginhost.KernelServiceRequirements(
		pluginhost.KernelRegistryIntrospectService,
	)})
	registry.Activate()

	result, serviceErr := registry.Call(context.Background(), item.ID, pluginhost.ServiceCallParams{
		Service: pluginhost.KernelRegistryIntrospectService, Method: pluginhost.KernelServiceMethod,
	})
	if serviceErr != nil {
		t.Fatal(serviceErr)
	}
	var snapshot pluginhost.ServiceRegistrySnapshot
	if err := json.Unmarshal(result, &snapshot); err != nil {
		t.Fatalf("snapshot decode: %v", err)
	}
	if snapshot.Generation != 42 {
		t.Fatalf("generation = %d, want 42", snapshot.Generation)
	}
	foundStorage, foundIntrospect := false, false
	for _, entry := range snapshot.Services {
		if entry.Provider != "kernel" || !entry.Kernel {
			t.Fatalf("unexpected non-kernel entry: %+v", entry)
		}
		switch entry.Service {
		case pluginhost.KernelStorageGetService:
			foundStorage = true
		case pluginhost.KernelRegistryIntrospectService:
			foundIntrospect = true
		}
	}
	if !foundStorage || !foundIntrospect {
		t.Fatalf("services = %+v", snapshot.Services)
	}

	if _, serviceErr := registry.Call(context.Background(), "stranger", pluginhost.ServiceCallParams{
		Service: pluginhost.KernelRegistryIntrospectService, Method: pluginhost.KernelServiceMethod,
	}); serviceErr == nil || serviceErr.Code != "service_not_authorized" {
		t.Fatalf("undeclared caller error = %#v, want service_not_authorized", serviceErr)
	}
}

func TestKernelExecutionUpdateRoutesToExecutionTable(t *testing.T) {
	home, workspace := t.TempDir(), t.TempDir()
	item := serviceTestPlugin("demo", "plugin:user:demo", "generation")
	handler := newPluginHostServices(item, workspace, home, nil)
	recorder := &fakeExecutionRecorder{}
	kernel := newKernelHostServices(nil, recorder)
	kernel.add(item.ID, handler)
	registry, conflicts := pluginhost.BuildServiceRegistry(kernel)
	kernel.bindRegistry(registry)
	if len(conflicts) != 0 {
		t.Fatalf("conflicts = %+v", conflicts)
	}
	registry.RegisterClients(&kernelConsumer{id: item.ID, requirements: pluginhost.KernelServiceRequirements(
		pluginhost.KernelExecutionUpdateService,
	)})
	registry.Activate()

	result, serviceErr := registry.Call(context.Background(), item.ID, pluginhost.ServiceCallParams{
		Service: pluginhost.KernelExecutionUpdateService, Method: pluginhost.KernelServiceMethod,
		Params: json.RawMessage(`{"execution_id":"exec-7","message":"halfway","detail":{"pct":50}}`),
	})
	if serviceErr != nil {
		t.Fatal(serviceErr)
	}
	if string(result) != `{}` {
		t.Fatalf("update result = %s", result)
	}
	if recorder.caller != item.ID || recorder.params.ExecutionID != "exec-7" || recorder.params.Message != "halfway" {
		t.Fatalf("recorder = %q %+v", recorder.caller, recorder.params)
	}

	recorder.err = &pluginhost.HostServiceError{Code: "execution_not_found", Message: "execution exec-7 is not live"}
	if _, serviceErr := registry.Call(context.Background(), item.ID, pluginhost.ServiceCallParams{
		Service: pluginhost.KernelExecutionUpdateService, Method: pluginhost.KernelServiceMethod,
		Params: json.RawMessage(`{"execution_id":"exec-7","message":"late"}`),
	}); serviceErr == nil || serviceErr.Code != "execution_not_found" {
		t.Fatalf("typed tracker error = %#v, want execution_not_found", serviceErr)
	}

	if _, serviceErr := registry.Call(context.Background(), "stranger", pluginhost.ServiceCallParams{
		Service: pluginhost.KernelExecutionUpdateService, Method: pluginhost.KernelServiceMethod,
		Params: json.RawMessage(`{"execution_id":"exec-7"}`),
	}); serviceErr == nil || serviceErr.Code != "service_not_authorized" {
		t.Fatalf("undeclared caller error = %#v, want service_not_authorized", serviceErr)
	}
}

func TestNonInteractiveRuntimeRejectsUserQuestionServices(t *testing.T) {
	root := t.TempDir()
	t.Setenv("WUU_HOME", filepath.Join(root, "state"))
	rt, err := NewSession(Options{
		RootDir: root, HomeDir: root, SafeMode: true, NonInteractive: true,
		Config: config.Config{DefaultProvider: "test", Providers: map[string]config.ProviderConfig{
			"test": {Type: "openai-compatible", BaseURL: "https://example.test/v1", APIKey: "synthetic", Model: "gpt-test"},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Cleanup()
	registry := rt.PluginHost.ServiceRegistry()
	services := []string{pluginhost.KernelUserQuestionAskService, pluginhost.KernelUserQuestionOfferService}
	registry.RegisterClients(&kernelConsumer{id: "question-test", requirements: pluginhost.KernelServiceRequirements(services...)})
	for _, service := range services {
		_, err := registry.Call(context.Background(), "question-test", pluginhost.ServiceCallParams{
			Service: service, Method: pluginhost.KernelServiceMethod,
			Params: json.RawMessage(`{"questions":[{"id":"q","question":"Choose","options":[{"label":"A"}]}]}`),
		})
		if err == nil || err.Code != "service_unavailable" {
			t.Fatalf("%s: got %v, want unavailable without waiting for a human", service, err)
		}
	}
}

func TestKernelUserQuestionUsesTrustedToolExecutionScope(t *testing.T) {
	executionCtx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	recorder := &fakeExecutionRecorder{scope: pluginhost.ToolExecutionScope{
		ExecutionSnapshot: pluginhost.ExecutionSnapshot{
			ID: "exec-trusted", PluginID: "ask-user", ThreadID: "thread-trusted",
			TurnID: "turn-trusted", ActorID: "actor-trusted", CallID: "call-trusted",
		},
		Context: executionCtx,
	}}
	broker := pluginhost.NewUserQuestionBroker()
	kernel := newKernelHostServices(nil, recorder)
	kernel.bindUserQuestions(broker)
	events, unsubscribe := broker.Subscribe(2)
	defer unsubscribe()

	type invokeResult struct {
		result json.RawMessage
		err    error
	}
	done := make(chan invokeResult, 1)
	go func() {
		result, err := (&userQuestionOfferInvoker{parent: kernel}).InvokeService(context.Background(), pluginhost.ServiceInvokeParams{
			Method: pluginhost.KernelServiceMethod, Caller: "ask-user", ExecutionID: "exec-trusted",
			Params: json.RawMessage(`{"questions":[{"id":"choice","question":"Choose","options":[{"label":"A"}]}]}`),
		})
		done <- invokeResult{result: result, err: err}
	}()
	var requested pluginhost.UserQuestionEvent
	select {
	case requested = <-events:
	case <-time.After(time.Second):
		t.Fatal("question was not published")
	}
	if requested.Request == nil || requested.Request.ThreadID != "thread-trusted" || requested.Request.TurnID != "turn-trusted" || requested.Request.CallID != "call-trusted" {
		t.Fatalf("request = %+v", requested.Request)
	}
	if requested.Request.Mode != pluginhost.UserQuestionModeOffer {
		t.Fatalf("mode = %q, want offer", requested.Request.Mode)
	}
	select {
	case outcome := <-done:
		if outcome.err != nil || !strings.Contains(string(outcome.result), requested.Request.RequestID) {
			t.Fatalf("invoke = %s, %v", outcome.result, outcome.err)
		}
	case <-time.After(time.Second):
		t.Fatal("offer service did not return")
	}
	if err := broker.Respond(requested.Request.RequestID, pluginhost.UserQuestionAnswer{Answers: []pluginhost.UserQuestionAnswerItem{{ID: "choice", Selected: []string{"A"}}}}); err != nil {
		t.Fatal(err)
	}
}

func TestKernelUserQuestionRejectsSpoofedOwnershipPayload(t *testing.T) {
	recorder := &fakeExecutionRecorder{scope: pluginhost.ToolExecutionScope{
		ExecutionSnapshot: pluginhost.ExecutionSnapshot{ID: "exec-1", PluginID: "ask-user", ThreadID: "trusted", TurnID: "trusted", CallID: "trusted"},
		Context:           context.Background(),
	}}
	kernel := newKernelHostServices(nil, recorder)
	kernel.bindUserQuestions(pluginhost.NewUserQuestionBroker())
	_, err := (&userQuestionAskInvoker{parent: kernel}).InvokeService(context.Background(), pluginhost.ServiceInvokeParams{
		Method: pluginhost.KernelServiceMethod, Caller: "ask-user", ExecutionID: "exec-1",
		Params: json.RawMessage(`{"thread_id":"spoofed","turn_id":"spoofed","questions":[{"id":"q","question":"Q","options":[{"label":"A"}]}]}`),
	})
	var serviceErr *pluginhost.HostServiceError
	if !errors.As(err, &serviceErr) || serviceErr.Code != "invalid_request" {
		t.Fatalf("spoofed payload error = %#v", err)
	}
}

func TestKernelUserQuestionEndsWithOwningExecution(t *testing.T) {
	executionCtx, cancelExecution := context.WithCancelCause(context.Background())
	recorder := &fakeExecutionRecorder{scope: pluginhost.ToolExecutionScope{
		ExecutionSnapshot: pluginhost.ExecutionSnapshot{ID: "exec-1", PluginID: "ask-user", ThreadID: "thread-1", TurnID: "turn-1", CallID: "call-1"},
		Context:           executionCtx,
	}}
	broker := pluginhost.NewUserQuestionBroker()
	kernel := newKernelHostServices(nil, recorder)
	kernel.bindUserQuestions(broker)
	events, unsubscribe := broker.Subscribe(2)
	defer unsubscribe()
	done := make(chan error, 1)
	go func() {
		_, err := (&userQuestionAskInvoker{parent: kernel}).InvokeService(context.Background(), pluginhost.ServiceInvokeParams{
			Method: pluginhost.KernelServiceMethod, Caller: "ask-user", ExecutionID: "exec-1",
			Params: json.RawMessage(`{"questions":[{"id":"q","question":"Q","options":[{"label":"A"}]}]}`),
		})
		done <- err
	}()
	select {
	case <-events:
	case <-time.After(time.Second):
		t.Fatal("question was not published")
	}
	cancelExecution(&pluginhost.UserQuestionError{Code: "execution_cancelled", Message: "turn interrupted"})
	select {
	case err := <-done:
		var serviceErr *pluginhost.HostServiceError
		if !errors.As(err, &serviceErr) || serviceErr.Code != "execution_cancelled" {
			t.Fatalf("execution cancellation error = %#v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("execution cancellation did not release question")
	}
	if pending := broker.List("thread-1"); len(pending) != 0 {
		t.Fatalf("pending after execution cancellation = %+v", pending)
	}
}

type fakeExecutionRecorder struct {
	caller     string
	params     pluginhost.ExecutionUpdateParams
	err        *pluginhost.HostServiceError
	scope      pluginhost.ToolExecutionScope
	resolveErr *pluginhost.HostServiceError
}

func (f *fakeExecutionRecorder) RecordExecutionUpdate(caller string, params pluginhost.ExecutionUpdateParams) *pluginhost.HostServiceError {
	f.caller, f.params = caller, params
	return f.err
}

func (f *fakeExecutionRecorder) ResolveToolExecution(string, string) (pluginhost.ToolExecutionScope, *pluginhost.HostServiceError) {
	return f.scope, f.resolveErr
}

type kernelConsumer struct {
	id           string
	requirements []pluginhost.ServiceRequirement
}

func (c *kernelConsumer) ID() string { return c.id }
func (c *kernelConsumer) Status() pluginhost.Status {
	return pluginhost.Status{ID: c.id, State: pluginhost.StatePrepared}
}
func (c *kernelConsumer) Close(context.Context) error                      { return nil }
func (c *kernelConsumer) ProvidedServices() []pluginhost.ServiceDescriptor { return nil }
func (c *kernelConsumer) RequiredServices() []pluginhost.ServiceRequirement {
	return c.requirements
}

func serviceTestPlugin(id, subject, fingerprint string) pluginpkg.Plugin {
	return pluginpkg.Plugin{Manifest: pluginpkg.Manifest{ID: id}, SubjectID: subject, Fingerprint: fingerprint}
}
