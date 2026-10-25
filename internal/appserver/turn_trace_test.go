package appserver

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/credentialstore"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/runtime"
	"github.com/blueberrycongee/wuu/internal/sessiontrace"
	"github.com/blueberrycongee/wuu/internal/tools"
)

func TestPersistTurnTraceUsesCapturedIndex(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "target.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("write target: %v", err)
	}
	kit, err := tools.New(root)
	if err != nil {
		t.Fatalf("tools.New: %v", err)
	}
	sessionDir := filepath.Join(t.TempDir(), "session-artifacts")
	kit.SetSessionDir(sessionDir)
	if _, err := kit.Execute(context.Background(), providers.ToolCall{
		ID:        "call-old-read",
		Name:      "read_file",
		Arguments: `{"path":"target.txt"}`,
	}); err != nil {
		t.Fatalf("old read_file: %v", err)
	}
	toolRecordStart := len(kit.ToolTelemetry())
	if _, err := kit.Execute(context.Background(), providers.ToolCall{
		ID:        "call-new-read",
		Name:      "read_file",
		Arguments: `{"path":"target.txt"}`,
	}); err != nil {
		t.Fatalf("new read_file: %v", err)
	}

	startedAt := time.Now().UTC().Add(-time.Second)
	completedAt := time.Now().UTC()
	duration := int64(1000)
	provider := &turnTraceIndexProvider{}
	srv, binding := newTurnTraceIndexFixture(t, provider)
	// A successor without this provider must not redirect the admitted turn.
	srv.rt = newTestRuntime(t, &fakeClient{})
	runner := &agent.StreamRunner{LoopDriver: agent.DefaultDriver{}, Model: "gpt-test", APIModel: "gpt-test-api"}
	tracePath, err := srv.persistTurnTrace(binding, &runtime.ThreadRuntime{Toolkit: kit}, runner, "thread-1", turnRuntimeSnapshot{
		ProviderName:   "openai",
		PermissionMode: config.PermissionModeUnconfined,
	}, Turn{
		ID:          "turn-1",
		Status:      TurnStatusCompleted,
		StartedAt:   &startedAt,
		CompletedAt: &completedAt,
		DurationMS:  &duration,
	}, agent.LoopResult{
		Content:      "done",
		InputTokens:  10,
		OutputTokens: 20,
	}, nil, toolRecordStart, []sessiontrace.RequestContextRecord{{
		StepIndex:         0,
		TransientMessages: 2,
		ContentBytes:      256,
		BlockKinds:        []string{"ENVIRONMENT", "TASK"},
	}}, []sessiontrace.ProviderStateRecord{{
		StepIndex:              1,
		Provider:               "openai",
		Protocol:               "responses_websocket",
		Transport:              "websocket",
		ReplayMode:             "previous_response_id",
		PreviousResponseIDUsed: true,
		ConnectionReused:       true,
		FallbackActive:         true,
		FallbackReason:         "websocket_failed_before_first_event",
		InputItems:             1,
		FullInputItems:         4,
		DeltaInputItems:        1,
	}}, []sessiontrace.CompactRecord{{
		Reason:            "proactive",
		Status:            "failed",
		TokensBefore:      252001,
		LastResponseTotal: 240000,
		PendingDelta:      12001,
		UsageAdjustment:   "provider_response",
		MessagesBefore:    42,
	}}, []sessiontrace.BarrierToolBatchRejectionRecord{{
		StepIndex:     0,
		BarrierTool:   "barrier_tool",
		SiblingTools:  []string{"run_shell"},
		ToolCallCount: 2,
	}})
	if err != nil {
		t.Fatalf("persistTurnTrace: %v", err)
	}
	if tracePath != "selected://thread-1/turn-1" || provider.calls != 1 || len(provider.batches) != 1 {
		t.Fatalf("captured index receipt = %q, calls = %d, batches = %d", tracePath, provider.calls, len(provider.batches))
	}
	if _, err := os.Stat(sessiontrace.Path(sessionDir)); !os.IsNotExist(err) {
		t.Fatalf("external index wrote a local trace: %v", err)
	}
	data, err := json.Marshal(provider.batches[0].Events)
	if err != nil {
		t.Fatal(err)
	}
	trace := string(data)
	for _, want := range []string{`"type":"turn"`, `"type":"context_requests"`, `"type":"provider_states"`, `"type":"compact_attempts"`, `"type":"barrier_tool_batch_rejected"`, `"type":"tool_inventory"`, `"type":"tool_records"`, `"type":"final"`, `"provider_name":"openai"`, `"model":"gpt-test"`, `"permission_mode":"unconfined"`, `"model_profile"`, `"family":"gpt"`, `"default_write_mode":"exact_edit"`, `"name":"read_file"`, `"ENVIRONMENT"`, `"step_index":1`, `"transport":"websocket"`, `"connection_reused":true`, `"fallback_reason":"websocket_failed_before_first_event"`, `"previous_response_id_used":true`, `"tokens_before":252001`, `"last_response_total":240000`, `"pending_delta":12001`, `"usage_adjustment":"provider_response"`, `"barrier_tool":"barrier_tool"`, `"sibling_tools":["run_shell"]`, `"tool_call_count":2`} {
		if !strings.Contains(trace, want) {
			t.Fatalf("session trace missing %s:\n%s", want, trace)
		}
	}
	if strings.Contains(trace, "call-old-read") || !strings.Contains(trace, "call-new-read") {
		t.Fatalf("session trace should include only this turn's tool records:\n%s", trace)
	}
}

// An uncertain append may already be durable at the selected provider. The
// caller must report it once and must never write a competing local trace.
func TestPersistTurnTraceDoesNotReplayUncertainAppend(t *testing.T) {
	provider := &turnTraceIndexProvider{err: errors.New("response lost after append")}
	srv, binding := newTurnTraceIndexFixture(t, provider)
	kit, err := tools.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "unused-local-trace")
	kit.SetSessionDir(dir)
	path, err := srv.persistTurnTrace(binding, &runtime.ThreadRuntime{Toolkit: kit}, nil, "thread-1", turnRuntimeSnapshot{}, Turn{ID: "turn-1", Status: TurnStatusCompleted}, agent.LoopResult{}, nil, 0, nil, nil, nil)
	if err == nil || path != "" || provider.calls != 1 {
		t.Fatalf("uncertain append: path=%q calls=%d err=%v", path, provider.calls, err)
	}
	if _, err := os.Stat(sessiontrace.Path(dir)); !os.IsNotExist(err) {
		t.Fatalf("uncertain selected write fell back to a local trace: %v", err)
	}
}

// A model answer remains successful when only its selected index write fails,
// but the public completion must expose that unconfirmed secondary outcome.
func TestTurnCompletionReportsUnconfirmedIndexWrite(t *testing.T) {
	provider := &turnTraceIndexProvider{err: errors.New("private-index-payload-secret")}
	fixture, _ := newTurnTraceIndexFixture(t, provider)
	fixture.rt.StreamRunner.Client = providers.AdaptStreamClient(&fakeClient{response: providers.ChatResponse{Content: "answer"}})
	out := &lockedBuffer{}
	srv := New(fixture.rt, out)
	defer srv.Close()
	if err := srv.handleLine(context.Background(), []byte(`{"id":"create","method":"thread/start"}`)); err != nil {
		t.Fatal(err)
	}
	created := responseByID(t, parseOutput(t, out.String()), "create")
	if created["error"] != nil {
		t.Fatalf("create: %v", created)
	}
	id := remarshal[ThreadStartResult](t, created["result"]).Thread.ID
	raw, _ := json.Marshal(map[string]any{"id": "start", "method": MethodTurnStart, "params": TurnStartParams{ThreadID: id, Prompt: "hello"}})
	if err := srv.handleLine(context.Background(), raw); err != nil {
		t.Fatal(err)
	}
	start := responseByID(t, parseOutput(t, out.String()), "start")
	if start["error"] != nil {
		t.Fatalf("start: %v", start)
	}
	messages := waitForMethod(t, out, NotificationTurnCompleted)
	params := notificationByMethod(t, messages, NotificationTurnCompleted)["params"].(map[string]any)
	if diagnostic, _ := params["index_error"].(string); diagnostic == "" || strings.Contains(diagnostic, "private-index-payload-secret") {
		t.Fatalf("missing or unsafe index outcome: %v", params)
	}
	if params["content"] != "answer" || params["trace_path"] != nil {
		t.Fatalf("model answer or trace outcome: %v", params)
	}
	if strings.Contains(out.String(), "private-index-payload-secret") {
		t.Fatal("index payload leaked")
	}
	srv.Close()
	if provider.calls != 1 {
		t.Fatalf("unconfirmed index append retried %d times", provider.calls)
	}
}

type turnTraceIndexProvider struct {
	calls   int
	batches []pluginhost.DataAppendTurnParams
	err     error
}

func (*turnTraceIndexProvider) ID() string { return "test.turn-index" }
func (p *turnTraceIndexProvider) Status() pluginhost.Status {
	return pluginhost.Status{ID: p.ID(), State: pluginhost.StateActive}
}
func (*turnTraceIndexProvider) Close(context.Context) error                       { return nil }
func (*turnTraceIndexProvider) RequiredServices() []pluginhost.ServiceRequirement { return nil }
func (*turnTraceIndexProvider) ProvidedServices() []pluginhost.ServiceDescriptor {
	return []pluginhost.ServiceDescriptor{pluginhost.KernelDataQueryDescriptor()}
}
func (p *turnTraceIndexProvider) InvokeService(_ context.Context, input pluginhost.ServiceInvokeParams) (json.RawMessage, error) {
	if input.Method != pluginhost.DataAppendTurnMethod {
		return nil, errors.New("unexpected index method")
	}
	p.calls++
	var batch pluginhost.DataAppendTurnParams
	if err := json.Unmarshal(input.Params, &batch); err != nil {
		return nil, err
	}
	p.batches = append(p.batches, batch)
	if p.err != nil {
		return nil, p.err
	}
	return json.Marshal(pluginhost.DataAppendTurnResult{Version: pluginhost.DataQueryServiceVersion, ThreadID: batch.ThreadID, TurnID: batch.TurnID, Committed: true, DiagnosticPath: "selected://" + batch.ThreadID + "/" + batch.TurnID})
}

type turnTraceIndexConsumer struct{}

func (*turnTraceIndexConsumer) ID() string { return runtime.StateServicesConsumer }
func (c *turnTraceIndexConsumer) Status() pluginhost.Status {
	return pluginhost.Status{ID: c.ID(), State: pluginhost.StateActive}
}
func (*turnTraceIndexConsumer) Close(context.Context) error { return nil }
func (*turnTraceIndexConsumer) RequiredServices() []pluginhost.ServiceRequirement {
	return []pluginhost.ServiceRequirement{{Name: pluginhost.KernelDataQueryService, MajorVersion: pluginhost.DataQueryServiceMajor, Required: true}}
}
func (*turnTraceIndexConsumer) ProvidedServices() []pluginhost.ServiceDescriptor { return nil }

func newTurnTraceIndexFixture(t *testing.T, provider *turnTraceIndexProvider) (*Server, *runtime.State) {
	t.Helper()
	rt := newTestRuntime(t, &fakeClient{})
	consumer := &turnTraceIndexConsumer{}
	attachTestModelGeneration(t, rt, provider, consumer)
	return &Server{rt: rt}, testRuntimeState(t, rt)
}

type nativeTraceServiceConsumer struct{ configSourceConsumer }

func (*nativeTraceServiceConsumer) RequiredServices() []pluginhost.ServiceRequirement {
	return append((&configSourceConsumer{}).RequiredServices(), pluginhost.ServiceRequirement{
		Name: pluginhost.KernelDataQueryService, MajorVersion: pluginhost.DataQueryServiceMajor, Required: true,
	})
}

func bindNativeTraceForTest(t *testing.T, rt *runtime.Session) {
	t.Helper()
	source := config.NewService(config.Module, rt.ConfigPath, "")
	home := os.Getenv("HOME")
	credentials := credentialstore.NewService("test.trace-credentials", func() (credentialstore.Store, error) {
		return nativeCredentialFixture(t, home), nil
	})
	index := runtime.NewDataQueryService(runtime.DataQueryModule, t.TempDir())
	consumer := &nativeTraceServiceConsumer{}
	attachTestModelGeneration(t, rt, source, credentials, index, consumer)
}
