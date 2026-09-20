package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/config"
	pluginpkg "github.com/blueberrycongee/wuu/internal/plugin"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
)

func TestKernelHostServicesRouteThroughRegistryAndPreserveStorageScope(t *testing.T) {
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
