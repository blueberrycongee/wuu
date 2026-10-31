package appserver

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/compactionservice"
	pluginpkg "github.com/blueberrycongee/wuu/internal/plugin"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/runtime"
	"github.com/blueberrycongee/wuu/internal/session"
)

type pluginTurnLifecycleClient struct {
	id    string
	calls chan pluginhost.AgentTurnLifecycleInput
}

type pluginTurnInterruptedClient struct {
	id    string
	calls chan pluginhost.AgentTurnInterruptedInput
}

type blockingPluginTurnLifecycleClient struct {
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (c *blockingPluginTurnLifecycleClient) ID() string { return "subagent" }
func (c *blockingPluginTurnLifecycleClient) Status() pluginhost.Status {
	return pluginhost.Status{ID: c.ID(), State: pluginhost.StateActive}
}
func (c *blockingPluginTurnLifecycleClient) Close(context.Context) error { return nil }
func (c *blockingPluginTurnLifecycleClient) ProtocolVersion() int {
	return pluginhost.CapabilityProtocolVersion
}
func (c *blockingPluginTurnLifecycleClient) Capabilities() []pluginhost.CapabilityDescriptor {
	return []pluginhost.CapabilityDescriptor{{ID: pluginhost.CapabilityAgentTurnLifecycle, Kind: pluginhost.SeamObserve, Version: 1}}
}
func (c *blockingPluginTurnLifecycleClient) InvokeCapability(ctx context.Context, _ pluginhost.CapabilityInvokeParams) (pluginhost.CapabilityInvokeResult, error) {
	c.once.Do(func() { close(c.started) })
	select {
	case <-c.release:
		return pluginhost.CapabilityInvokeResult{Output: []byte(`{}`)}, nil
	case <-ctx.Done():
		return pluginhost.CapabilityInvokeResult{}, ctx.Err()
	}
}

func (c *pluginTurnLifecycleClient) ID() string { return c.id }
func (c *pluginTurnLifecycleClient) Status() pluginhost.Status {
	return pluginhost.Status{ID: c.id, State: pluginhost.StateActive}
}
func (c *pluginTurnLifecycleClient) Close(context.Context) error { return nil }
func (c *pluginTurnLifecycleClient) ProtocolVersion() int {
	return pluginhost.CapabilityProtocolVersion
}
func (c *pluginTurnLifecycleClient) Capabilities() []pluginhost.CapabilityDescriptor {
	return []pluginhost.CapabilityDescriptor{{ID: pluginhost.CapabilityAgentTurnLifecycle, Kind: pluginhost.SeamObserve, Version: 1}}
}
func (c *pluginTurnLifecycleClient) InvokeCapability(_ context.Context, params pluginhost.CapabilityInvokeParams) (pluginhost.CapabilityInvokeResult, error) {
	var input pluginhost.AgentTurnLifecycleInput
	if err := json.Unmarshal(params.Input, &input); err != nil {
		return pluginhost.CapabilityInvokeResult{}, err
	}
	c.calls <- input
	return pluginhost.CapabilityInvokeResult{Output: json.RawMessage(`{}`)}, nil
}

func (c *pluginTurnInterruptedClient) ID() string { return c.id }
func (c *pluginTurnInterruptedClient) Status() pluginhost.Status {
	return pluginhost.Status{ID: c.id, State: pluginhost.StateActive}
}
func (c *pluginTurnInterruptedClient) Close(context.Context) error { return nil }
func (c *pluginTurnInterruptedClient) ProtocolVersion() int {
	return pluginhost.CapabilityProtocolVersion
}
func (c *pluginTurnInterruptedClient) Capabilities() []pluginhost.CapabilityDescriptor {
	return []pluginhost.CapabilityDescriptor{{ID: pluginhost.CapabilityAgentTurnInterrupted, Kind: pluginhost.SeamObserve, Version: 1}}
}
func (c *pluginTurnInterruptedClient) InvokeCapability(_ context.Context, params pluginhost.CapabilityInvokeParams) (pluginhost.CapabilityInvokeResult, error) {
	var input pluginhost.AgentTurnInterruptedInput
	if err := json.Unmarshal(params.Input, &input); err != nil {
		return pluginhost.CapabilityInvokeResult{}, err
	}
	c.calls <- input
	return pluginhost.CapabilityInvokeResult{Output: json.RawMessage(`{}`)}, nil
}

func TestPluginTurnRequestContextIsBoundedAndRequestOnly(t *testing.T) {
	segments, err := pluginTurnRequestContext([]pluginhost.SessionContextBlock{{Content: "private trigger facts"}})
	if err != nil || len(segments) != 1 || len(segments[0].Blocks) != 1 {
		t.Fatalf("segments = %+v, err = %v", segments, err)
	}
	if segments[0].Lifecycle != agent.ContextSegmentRequestOnly || segments[0].Durable || segments[0].VisibleInUI {
		t.Fatalf("plugin context is not request-only: %+v", segments[0])
	}
	tooMany := make([]pluginhost.SessionContextBlock, pluginhost.MaxSessionSendContextBlocks+1)
	for index := range tooMany {
		tooMany[index].Content = "x"
	}
	if _, err := pluginTurnRequestContext(tooMany); err == nil {
		t.Fatal("oversized context block list was accepted")
	}
}

func TestPluginSessionCreateAndSendPersistProvenanceAndTargetLifecycle(t *testing.T) {
	client := &fakeClient{response: providersResponse("done")}
	rt := newTestRuntime(t, client)
	rt.WorkspaceID = "workspace-one"
	rt.PluginSessionRouter = runtime.NewPluginSessionRouter()
	owner := &pluginTurnLifecycleClient{id: "schedule", calls: make(chan pluginhost.AgentTurnLifecycleInput, 2)}
	other := &pluginTurnLifecycleClient{id: "other", calls: make(chan pluginhost.AgentTurnLifecycleInput, 1)}
	attachTestModelGeneration(t, rt, owner, other)
	out := &lockedBuffer{}
	srv := New(rt, out)
	t.Cleanup(srv.Close)
	if _, err := rt.PluginSessionRouter.Create(pluginInvocationContext(t, rt, context.Background()), owner.id, pluginhost.SessionCreateParams{
		RequestID: "wrong-workspace", Visibility: pluginhost.SessionVisibilityUser, ContextSource: pluginhost.SessionContextFresh,
		WorkspaceID: "workspace-two", WorkspaceRoot: rt.RootDir,
	}); err == nil {
		t.Fatal("session creation accepted a different target workspace")
	}

	created, err := rt.PluginSessionRouter.Create(pluginInvocationContext(t, rt, context.Background()), owner.id, pluginhost.SessionCreateParams{
		RequestID: "create-1", Visibility: pluginhost.SessionVisibilityUser, ContextSource: pluginhost.SessionContextFresh,
		WorkspaceID: rt.WorkspaceID, WorkspaceRoot: rt.RootDir,
	})
	if err != nil || created.SessionID == "" || !created.Created {
		t.Fatalf("create = %+v, %v", created, err)
	}
	result, err := rt.PluginSessionRouter.Send(pluginInvocationContext(t, rt, context.Background()), owner.id, pluginhost.SessionSendParams{
		RequestID: "request-1", SessionID: created.SessionID,
		Input:        pluginhost.SessionInput{Prompt: "internal inspect prompt", ContextBlocks: []pluginhost.SessionContextBlock{{Kind: "TRIGGER", Source: "schedule", Content: "fired at 09:00"}}},
		Presentation: &pluginhost.SessionInputPresentation{Kind: pluginhost.SessionPresentationQueryBubble, Text: "后台任务已唤醒 Agent"}, Cause: "schedule:daily",
	})
	if err != nil || result.State != pluginhost.TurnLifecycleRunning || result.SessionID == "" || result.TurnID == "" {
		t.Fatalf("send = %+v, %v", result, err)
	}
	waitForTurnCompletedForThread(t, out, result.SessionID)

	select {
	case lifecycle := <-owner.calls:
		if lifecycle.State != pluginhost.TurnLifecycleCompleted || lifecycle.RequestID != "request-1" || lifecycle.ThreadID != result.SessionID || lifecycle.TurnID != result.TurnID || lifecycle.FinalOutput != "done" {
			t.Fatalf("lifecycle = %+v", lifecycle)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("owner did not receive terminal lifecycle")
	}
	select {
	case lifecycle := <-other.calls:
		t.Fatalf("unrelated plugin received lifecycle: %+v", lifecycle)
	default:
	}

	persisted, ok, err := rt.Sessions.Find(result.SessionID)
	if err != nil || !ok || persisted.Source != "plugin:schedule" || persisted.Owner != "plugin:schedule" || persisted.Visibility != pluginhost.SessionVisibilityUser {
		t.Fatalf("persisted thread = %+v, %t, %v", persisted, ok, err)
	}
	records, err := rt.Sessions.LoadHistoryRecords(result.SessionID, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, record := range records {
		if strings.Contains(record.Content, "fired at 09:00") {
			t.Fatalf("request-only context leaked into history: %+v", record)
		}
	}
	var generated *session.HistoryRecord
	for index := range records {
		if records[index].Origin == pluginhost.SessionInputPlugin {
			generated = &records[index]
			break
		}
	}
	if generated == nil || generated.OriginID != owner.id || generated.Cause != "schedule:daily" || generated.DisplayContent != "后台任务已唤醒 Agent" || generated.Content != "internal inspect prompt" || !generated.ReadOnly {
		t.Fatalf("generated query provenance = %+v", generated)
	}
	client.mu.Lock()
	requests := append([]providers.ChatRequest(nil), client.requests...)
	client.mu.Unlock()
	providerHasGeneratedQuery := false
	providerHasRequestContext := false
	if len(requests) == 1 {
		for _, message := range requests[0].Messages {
			providerHasGeneratedQuery = providerHasGeneratedQuery || (message.Role == "user" && message.Origin == pluginhost.SessionInputPlugin && strings.Contains(message.Content, "internal inspect prompt"))
			providerHasRequestContext = providerHasRequestContext || strings.Contains(message.Content, "fired at 09:00")
		}
	}
	if !providerHasGeneratedQuery || !providerHasRequestContext {
		t.Fatalf("provider request missing plugin context: %+v", requests)
	}
	loaded, err := srv.ensureThreadLoaded(testRuntimeState(t, rt), result.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	loaded.mu.Lock()
	item := loaded.Turns[0].Items[0]
	loaded.mu.Unlock()
	if item.Text != "后台任务已唤醒 Agent" || item.InputText != "internal inspect prompt" || strings.Contains(item.Text, "internal inspect prompt") || item.Origin != pluginhost.SessionInputPlugin || !item.ReadOnly || item.PresentationKind != pluginhost.SessionPresentationQueryBubble {
		t.Fatalf("query bubble projection = %+v", item)
	}
}

// Create-time instructions are session state. Runtime prompt refreshes on turn
// preparation and reload must keep them in front of the model.
func TestPluginSessionInstructionsReachProviderAfterReload(t *testing.T) {
	const instructions = "Answer only with the release checklist."
	client := &fakeClient{response: providersResponse("done")}
	rt := newTestRuntime(t, client)
	rt.WorkspaceID = "workspace-one"
	rt.PluginSessionRouter = runtime.NewPluginSessionRouter()
	owner := &pluginTurnLifecycleClient{id: "schedule", calls: make(chan pluginhost.AgentTurnLifecycleInput, 4)}
	attachTestModelGeneration(t, rt, owner)
	out := &lockedBuffer{}
	srv := New(rt, out)
	created, err := rt.PluginSessionRouter.Create(pluginInvocationContext(t, rt, context.Background()), owner.id, pluginhost.SessionCreateParams{
		RequestID: "create-instructions", Visibility: pluginhost.SessionVisibilityUser, ContextSource: pluginhost.SessionContextFresh,
		WorkspaceID: rt.WorkspaceID, WorkspaceRoot: rt.RootDir, Instructions: instructions,
	})
	if err != nil {
		t.Fatal(err)
	}
	send := func(requestID string, out *lockedBuffer) {
		t.Helper()
		result, err := rt.PluginSessionRouter.Send(pluginInvocationContext(t, rt, context.Background()), owner.id, pluginhost.SessionSendParams{
			RequestID: requestID, SessionID: created.SessionID, Input: pluginhost.SessionInput{Prompt: requestID},
		})
		if err != nil {
			t.Fatal(err)
		}
		waitForTurnCompletedForThread(t, out, result.SessionID)
	}
	assertInstructed := func(index int) {
		t.Helper()
		client.mu.Lock()
		defer client.mu.Unlock()
		if len(client.requests) <= index {
			t.Fatalf("provider saw %d requests, want request %d", len(client.requests), index)
		}
		for _, message := range client.requests[index].Messages {
			if message.Role == "system" && strings.Contains(message.Content, "# Session instructions\n\n"+instructions) {
				return
			}
		}
		t.Fatalf("request %d system prompt lacks session instructions: %+v", index, client.requests[index].Messages)
	}
	send("first", out)
	assertInstructed(0)
	srv.Close()

	rt.PluginSessionRouter = runtime.NewPluginSessionRouter()
	reloaded := &lockedBuffer{}
	srv = New(rt, reloaded)
	t.Cleanup(srv.Close)
	send("after reload", reloaded)
	assertInstructed(1)
}

func TestPluginSessionSendTurnOutlivesHostCallContext(t *testing.T) {
	client := &turnContextClient{started: make(chan context.Context, 1), release: make(chan struct{})}
	rt := newTestRuntime(t, &fakeClient{})
	rt.StreamRunner.Client = providers.AdaptStreamClient(client)
	rt.PluginSessionRouter = runtime.NewPluginSessionRouter()
	owner := &pluginTurnLifecycleClient{id: "subagent", calls: make(chan pluginhost.AgentTurnLifecycleInput, 1)}
	attachTestModelGeneration(t, rt, owner)
	out := &lockedBuffer{}
	srv := New(rt, out)
	t.Cleanup(func() {
		select {
		case <-client.release:
		default:
			close(client.release)
		}
		srv.Close()
	})

	created, err := rt.PluginSessionRouter.Create(pluginInvocationContext(t, rt, context.Background()), owner.id, pluginhost.SessionCreateParams{
		RequestID: "create", Visibility: pluginhost.SessionVisibilityPlugin, ContextSource: pluginhost.SessionContextFresh,
	})
	if err != nil {
		t.Fatal(err)
	}
	callCtx, cancelCall := context.WithCancel(context.Background())
	result, err := rt.PluginSessionRouter.Send(pluginInvocationContext(t, rt, callCtx), owner.id, pluginhost.SessionSendParams{
		RequestID: "run", SessionID: created.SessionID, Input: pluginhost.SessionInput{Prompt: "work"},
	})
	if err != nil || result.State != pluginhost.TurnLifecycleRunning {
		t.Fatalf("send = %+v, %v", result, err)
	}

	var turnCtx context.Context
	select {
	case turnCtx = <-client.started:
	case <-time.After(3 * time.Second):
		t.Fatal("turn did not reach provider")
	}
	cancelCall()
	if err := turnCtx.Err(); err != nil {
		t.Fatalf("turn inherited completed host call context: %v", err)
	}
	close(client.release)
	waitForTurnCompletedForThread(t, out, result.SessionID)
	select {
	case lifecycle := <-owner.calls:
		if lifecycle.State != pluginhost.TurnLifecycleCompleted || lifecycle.RequestID != "run" || lifecycle.FinalOutput != "done" {
			t.Fatalf("lifecycle = %+v", lifecycle)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("owner did not receive completed lifecycle")
	}
}

type turnContextClient struct {
	started chan context.Context
	release chan struct{}
}

func (client *turnContextClient) Chat(ctx context.Context, _ providers.ChatRequest) (providers.ChatResponse, error) {
	client.started <- ctx
	select {
	case <-client.release:
		return providersResponse("done"), nil
	case <-ctx.Done():
		return providers.ChatResponse{}, ctx.Err()
	}
}

func TestPluginSessionListAndCancelAreOwnerScoped(t *testing.T) {
	chatStarted := make(chan struct{})
	release := make(chan struct{})
	rt := newTestRuntime(t, &fakeClient{response: providersResponse("done"), onChat: func(_ int, _ providers.ChatRequest) { close(chatStarted); <-release }})
	rt.PluginSessionRouter = runtime.NewPluginSessionRouter()
	srv := New(rt, &lockedBuffer{})
	t.Cleanup(func() { close(release); srv.Close() })
	created, err := rt.PluginSessionRouter.Create(pluginInvocationContext(t, rt, context.Background()), "subagent", pluginhost.SessionCreateParams{RequestID: "owned", Name: "review_parser", Visibility: pluginhost.SessionVisibilityPlugin, ContextSource: pluginhost.SessionContextFresh})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rt.PluginSessionRouter.Send(pluginInvocationContext(t, rt, context.Background()), "subagent", pluginhost.SessionSendParams{RequestID: "run", SessionID: created.SessionID, Input: pluginhost.SessionInput{Prompt: "work"}}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-chatStarted:
	case <-time.After(3 * time.Second):
		t.Fatal("turn did not start")
	}
	listed, err := rt.PluginSessionRouter.List(pluginInvocationContext(t, rt, context.Background()), "subagent", pluginhost.SessionListParams{})
	if err != nil || len(listed.Sessions) != 1 || listed.Sessions[0].SessionID != created.SessionID || listed.Sessions[0].Name != "review_parser" || listed.Sessions[0].State != pluginhost.TurnLifecycleRunning {
		t.Fatalf("list = %+v, %v", listed, err)
	}
	other, err := rt.PluginSessionRouter.List(pluginInvocationContext(t, rt, context.Background()), "other", pluginhost.SessionListParams{})
	if err != nil || len(other.Sessions) != 0 {
		t.Fatalf("other list = %+v, %v", other, err)
	}
	if _, err := rt.PluginSessionRouter.Cancel(pluginInvocationContext(t, rt, context.Background()), "other", pluginhost.SessionCancelParams{SessionID: created.SessionID}); err == nil {
		t.Fatal("another plugin cancelled an owned session")
	}
	cancelled, err := rt.PluginSessionRouter.Cancel(pluginInvocationContext(t, rt, context.Background()), "subagent", pluginhost.SessionCancelParams{SessionID: created.SessionID})
	if err != nil || !cancelled.Cancelled {
		t.Fatalf("cancel = %+v, %v", cancelled, err)
	}
}

func TestPluginSessionCancelIsTurnScopedAndBroadcastsInterruption(t *testing.T) {
	chatStarted := make(chan struct{})
	release := make(chan struct{})
	rt := newTestRuntime(t, &fakeClient{response: providersResponse("done"), onChat: func(_ int, _ providers.ChatRequest) { close(chatStarted); <-release }})
	rt.PluginSessionRouter = runtime.NewPluginSessionRouter()
	observer := &pluginTurnInterruptedClient{id: "orchestrator", calls: make(chan pluginhost.AgentTurnInterruptedInput, 1)}
	attachTestModelGeneration(t, rt, observer)
	srv := New(rt, &lockedBuffer{})
	t.Cleanup(func() { close(release); srv.Close() })

	created, err := rt.PluginSessionRouter.Create(pluginInvocationContext(t, rt, context.Background()), observer.id, pluginhost.SessionCreateParams{
		RequestID: "owned-turn", Visibility: pluginhost.SessionVisibilityPlugin, ContextSource: pluginhost.SessionContextFresh,
	})
	if err != nil {
		t.Fatal(err)
	}
	started, err := rt.PluginSessionRouter.Send(pluginInvocationContext(t, rt, context.Background()), observer.id, pluginhost.SessionSendParams{
		RequestID: "run-turn", SessionID: created.SessionID, Input: pluginhost.SessionInput{Prompt: "work"},
	})
	if err != nil || started.TurnID == "" {
		t.Fatalf("send = %+v, %v", started, err)
	}
	select {
	case <-chatStarted:
	case <-time.After(3 * time.Second):
		t.Fatal("turn did not start")
	}
	queued, err := rt.PluginSessionRouter.Send(pluginInvocationContext(t, rt, context.Background()), observer.id, pluginhost.SessionSendParams{
		RequestID: "queued-turn", SessionID: created.SessionID, Input: pluginhost.SessionInput{Prompt: "later"},
	})
	if err != nil || queued.QueueID == "" {
		t.Fatalf("queued send = %+v, %v", queued, err)
	}
	queuedCancel, err := rt.PluginSessionRouter.Cancel(pluginInvocationContext(t, rt, context.Background()), observer.id, pluginhost.SessionCancelParams{SessionID: created.SessionID, QueueID: queued.QueueID})
	if err != nil || !queuedCancel.Cancelled || queuedCancel.QueueID != queued.QueueID {
		t.Fatalf("queued cancel = %+v, %v", queuedCancel, err)
	}

	mismatch, err := rt.PluginSessionRouter.Cancel(pluginInvocationContext(t, rt, context.Background()), observer.id, pluginhost.SessionCancelParams{SessionID: created.SessionID, TurnID: "not-current"})
	if err != nil || mismatch.Cancelled {
		t.Fatalf("mismatched cancel = %+v, %v", mismatch, err)
	}
	cancelled, err := rt.PluginSessionRouter.Cancel(pluginInvocationContext(t, rt, context.Background()), observer.id, pluginhost.SessionCancelParams{SessionID: created.SessionID, TurnID: started.TurnID})
	if err != nil || !cancelled.Cancelled || cancelled.TurnID != started.TurnID {
		t.Fatalf("exact cancel = %+v, %v", cancelled, err)
	}
	select {
	case input := <-observer.calls:
		if input.ThreadID != created.SessionID || input.TurnID != started.TurnID {
			t.Fatalf("interruption = %+v", input)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("plugin did not receive turn interruption")
	}
}

func TestPluginSessionCancelDoesNotWaitForDiscardedLifecycleObserver(t *testing.T) {
	stream := newBlockingStreamClient("must not complete")
	client := &fakeClient{}
	rt := newTestRuntime(t, client)
	rt.StreamRunner.Client = providers.AdaptStreamClient(stream)
	rt.PluginSessionRouter = runtime.NewPluginSessionRouter()
	lifecycle := &blockingPluginTurnLifecycleClient{started: make(chan struct{}), release: make(chan struct{})}
	attachTestModelGeneration(t, rt, lifecycle)
	out := &lockedBuffer{}
	srv := New(rt, out)
	t.Cleanup(func() {
		close(lifecycle.release)
		srv.Close()
	})

	created, err := rt.PluginSessionRouter.Create(pluginInvocationContext(t, rt, context.Background()), lifecycle.ID(), pluginhost.SessionCreateParams{
		RequestID: "create", Name: "child", Visibility: pluginhost.SessionVisibilityPlugin, ContextSource: pluginhost.SessionContextFresh,
	})
	if err != nil {
		t.Fatal(err)
	}
	started, err := rt.PluginSessionRouter.Send(pluginInvocationContext(t, rt, context.Background()), lifecycle.ID(), pluginhost.SessionSendParams{
		RequestID: "first", SessionID: created.SessionID, Input: pluginhost.SessionInput{Prompt: "first"},
	})
	if err != nil || started.State != pluginhost.TurnLifecycleRunning {
		t.Fatalf("first send = %+v, %v", started, err)
	}
	select {
	case <-stream.started:
	case <-time.After(2 * time.Second):
		t.Fatal("child turn did not start")
	}
	queued, err := rt.PluginSessionRouter.Send(pluginInvocationContext(t, rt, context.Background()), lifecycle.ID(), pluginhost.SessionSendParams{
		RequestID: "second", SessionID: created.SessionID, Input: pluginhost.SessionInput{Prompt: "second"},
	})
	if err != nil || queued.State != pluginhost.TurnLifecycleQueued || queued.QueueID == "" {
		t.Fatalf("second send = %+v, %v", queued, err)
	}

	cancelled := make(chan error, 1)
	go func() {
		_, cancelErr := rt.PluginSessionRouter.Cancel(pluginInvocationContext(t, rt, context.Background()), lifecycle.ID(), pluginhost.SessionCancelParams{SessionID: created.SessionID})
		cancelled <- cancelErr
	}()
	select {
	case err := <-cancelled:
		if err != nil {
			t.Fatalf("cancel = %v", err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("cancel waited for a plugin lifecycle observer")
	}
	select {
	case <-lifecycle.started:
	case <-time.After(2 * time.Second):
		t.Fatal("discarded lifecycle observer was not scheduled")
	}
}

func TestPluginSessionCreateIsIdempotentAndPrivateSessionsStayOutOfSearch(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{response: providersResponse("done")})
	rt.PluginSessionRouter = runtime.NewPluginSessionRouter()
	srv := New(rt, &lockedBuffer{})
	t.Cleanup(srv.Close)
	params := pluginhost.SessionCreateParams{Speed: "fast", RequestID: "same-create", Visibility: pluginhost.SessionVisibilityPlugin, ContextSource: pluginhost.SessionContextFresh}
	first, err := rt.PluginSessionRouter.Create(pluginInvocationContext(t, rt, context.Background()), "dream", params)
	if err != nil || !first.Created {
		t.Fatalf("first create = %+v, %v", first, err)
	}
	saved, found, err := rt.Sessions.Find(first.SessionID)
	if err != nil || !found || saved.Speed != "fast" {
		t.Fatalf("plugin speed selection was lost: %+v, %v", saved, err)
	}
	second, err := rt.PluginSessionRouter.Create(pluginInvocationContext(t, rt, context.Background()), "dream", params)
	if err != nil || second.Created || second.SessionID != first.SessionID {
		t.Fatalf("idempotent create = %+v, %v", second, err)
	}
	sources, err := srv.threadSearchSources(testRuntimeState(t, rt))
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range sources {
		if source.entry.thread.ID == first.SessionID {
			t.Fatalf("private plugin session leaked into ordinary search: %+v", source)
		}
	}
}

func TestPluginSessionSendQueuesBusyThreadAndReportsLaterTransitions(t *testing.T) {
	chatStarted := make(chan struct{})
	releaseFirst := make(chan struct{})
	client := &fakeClient{
		responses: []providers.ChatResponse{providersResponse("first"), providersResponse("second")},
		onChat: func(call int, _ providers.ChatRequest) {
			if call == 1 {
				close(chatStarted)
				<-releaseFirst
			}
		},
	}
	rt := newTestRuntime(t, client)
	rt.PluginSessionRouter = runtime.NewPluginSessionRouter()
	owner := &pluginTurnLifecycleClient{id: "schedule", calls: make(chan pluginhost.AgentTurnLifecycleInput, 8)}
	attachTestModelGeneration(t, rt, owner)
	out := &lockedBuffer{}
	srv := New(rt, out)
	t.Cleanup(srv.Close)

	created, err := rt.PluginSessionRouter.Create(pluginInvocationContext(t, rt, context.Background()), owner.id, pluginhost.SessionCreateParams{RequestID: "create", Visibility: pluginhost.SessionVisibilityUser, ContextSource: pluginhost.SessionContextFresh})
	if err != nil {
		t.Fatal(err)
	}
	first, err := rt.PluginSessionRouter.Send(pluginInvocationContext(t, rt, context.Background()), owner.id, pluginhost.SessionSendParams{RequestID: "first", SessionID: created.SessionID, Input: pluginhost.SessionInput{Prompt: "first"}})
	if err != nil || first.State != pluginhost.TurnLifecycleRunning {
		t.Fatalf("first submit = %+v, %v", first, err)
	}
	select {
	case <-chatStarted:
	case <-time.After(3 * time.Second):
		t.Fatal("first turn did not start")
	}
	second, err := rt.PluginSessionRouter.Send(pluginInvocationContext(t, rt, context.Background()), owner.id, pluginhost.SessionSendParams{
		RequestID: "second", SessionID: first.SessionID, Input: pluginhost.SessionInput{Prompt: "second"},
	})
	if err != nil || second.State != pluginhost.TurnLifecycleQueued || second.QueueID == "" {
		t.Fatalf("second submit = %+v, %v", second, err)
	}
	close(releaseFirst)
	waitForTurnCompletedForThread(t, out, first.SessionID)

	deadline := time.After(5 * time.Second)
	states := make([]string, 0, 2)
	for len(states) < 2 {
		select {
		case lifecycle := <-owner.calls:
			if lifecycle.RequestID == "second" {
				states = append(states, lifecycle.State)
				if lifecycle.QueueID != second.QueueID || lifecycle.ThreadID != first.SessionID {
					t.Fatalf("second lifecycle = %+v", lifecycle)
				}
			}
		case <-deadline:
			t.Fatalf("second lifecycle states = %v", states)
		}
	}
	if strings.Join(states, ",") != pluginhost.TurnLifecycleRunning+","+pluginhost.TurnLifecycleCompleted {
		t.Fatalf("second lifecycle states = %v", states)
	}
}

func TestPluginSessionSendSteersBusyThreadWithoutQueueingAnotherTurn(t *testing.T) {
	chatStarted := make(chan struct{})
	steerRequest := make(chan struct{})
	releaseFirst := make(chan struct{})
	releaseSecond := make(chan struct{})
	unblockFirst := sync.OnceFunc(func() { close(releaseFirst) })
	unblockSecond := sync.OnceFunc(func() { close(releaseSecond) })
	client := &fakeClient{
		// Keep a real next step in this turn: a final response instead retains
		// the unconsumed steer for a follow-up, covered by the Peers tests.
		responses: []providers.ChatResponse{{Content: "working", FinishReason: providers.FinishReasonContinue}, providersResponse("second")},
		onChat: func(call int, _ providers.ChatRequest) {
			if call == 1 {
				close(chatStarted)
				<-releaseFirst
			}
			if call == 2 {
				close(steerRequest)
				<-releaseSecond
			}
		},
	}
	rt := newTestRuntime(t, client)
	rt.PluginSessionRouter = runtime.NewPluginSessionRouter()
	owner := &pluginTurnLifecycleClient{id: "subagent", calls: make(chan pluginhost.AgentTurnLifecycleInput, 8)}
	attachTestModelGeneration(t, rt, owner)
	out := &lockedBuffer{}
	srv := New(rt, out)
	t.Cleanup(func() { unblockFirst(); unblockSecond(); srv.Close() })

	created, err := rt.PluginSessionRouter.Create(pluginInvocationContext(t, rt, context.Background()), owner.id, pluginhost.SessionCreateParams{RequestID: "create", Visibility: pluginhost.SessionVisibilityPlugin, ContextSource: pluginhost.SessionContextFresh})
	if err != nil {
		t.Fatal(err)
	}
	first, err := rt.PluginSessionRouter.Send(pluginInvocationContext(t, rt, context.Background()), owner.id, pluginhost.SessionSendParams{RequestID: "first", SessionID: created.SessionID, Input: pluginhost.SessionInput{Prompt: "work"}})
	if err != nil || first.State != pluginhost.TurnLifecycleRunning {
		t.Fatalf("first submit = %+v, %v", first, err)
	}
	select {
	case <-chatStarted:
	case <-time.After(3 * time.Second):
		t.Fatal("first turn did not start")
	}

	params := pluginhost.SessionSendParams{
		RequestID: "steer", SessionID: created.SessionID,
		Input: pluginhost.SessionInput{Prompt: "summarize now"}, IfRunning: pluginhost.SessionIfRunningSteer,
	}
	steered, err := rt.PluginSessionRouter.Send(pluginInvocationContext(t, rt, context.Background()), owner.id, params)
	if err != nil || !steered.Steered || steered.State != pluginhost.TurnLifecycleRunning || steered.TurnID != first.TurnID || steered.QueueID != "" {
		t.Fatalf("steer = %+v, %v", steered, err)
	}
	replayed, err := rt.PluginSessionRouter.Send(pluginInvocationContext(t, rt, context.Background()), owner.id, params)
	if err != nil || !replayed.Steered || replayed.TurnID != first.TurnID {
		t.Fatalf("idempotent steer = %+v, %v", replayed, err)
	}

	srv.queuedTurnMu.Lock()
	queued := len(srv.pendingQueuedTurns[created.SessionID])
	srv.queuedTurnMu.Unlock()
	if queued != 0 {
		t.Fatalf("steer queued %d follow-up turn(s)", queued)
	}
	thread := srv.thread(created.SessionID)
	thread.mu.Lock()
	pendingSteers := len(thread.pendingSteers)
	thread.mu.Unlock()
	if pendingSteers != 1 {
		t.Fatalf("pending steers = %d, want 1", pendingSteers)
	}
	for requestID, wantAvailable := range map[string]bool{"first": true, "steer": false} {
		view, err := rt.PluginSessionRouter.Inspect(pluginInvocationContext(t, rt, context.Background()), owner.id, pluginhost.SessionInspectParams{SessionID: created.SessionID, RequestID: requestID, IncludeProgress: true})
		if err != nil || view.Progress == nil || view.Progress.Available != wantAvailable {
			t.Fatalf("pending-steer progress for %s = %+v, %v", requestID, view.Progress, err)
		}
	}

	unblockFirst()
	select {
	case <-steerRequest:
	case <-time.After(3 * time.Second):
		t.Fatal("steer was not submitted to the provider")
	}
	client.mu.Lock()
	requests := append([]providers.ChatRequest(nil), client.requests...)
	client.mu.Unlock()
	if len(requests) != 2 {
		t.Fatalf("provider requests = %d, want 2", len(requests))
	}
	consumed := 0
	for _, message := range requests[1].Messages {
		if message.Role == "user" && message.Content == params.Input.Prompt {
			consumed++
		}
	}
	if consumed != 1 {
		t.Fatalf("second provider request consumed %d steers, want 1", consumed)
	}
	for _, requestID := range []string{"first", "steer"} {
		view, err := rt.PluginSessionRouter.Inspect(pluginInvocationContext(t, rt, context.Background()), owner.id, pluginhost.SessionInspectParams{SessionID: created.SessionID, RequestID: requestID, IncludeProgress: true})
		if err != nil || view.Progress == nil || view.Progress.Available || view.Progress.Reason != "request_not_exclusive" {
			t.Fatalf("consumed-steer progress for %s = %+v, %v", requestID, view.Progress, err)
		}
	}
	unblockSecond()
	waitForTurnCompletedForThread(t, out, created.SessionID)
	waitPluginGenerationWatchTest(t, func() bool {
		thread.mu.Lock()
		idle := !thread.running && !thread.admissionReserved && thread.executionLease == nil
		thread.mu.Unlock()
		return idle && !srv.hasQueuedUserWork(created.SessionID)
	})
	thread.mu.Lock()
	turns := len(thread.Turns)
	thread.mu.Unlock()
	if turns != 1 {
		t.Fatalf("steer created %d turns, want 1", turns)
	}
	history, err := loadChatMessages(rt.Sessions, created.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	persistedSteers := 0
	for _, message := range history {
		if message.ClientID == pluginSessionRequestClientID(owner.id, "steer") && message.Content == params.Input.Prompt && message.Steered {
			persistedSteers++
		}
	}
	if persistedSteers != 1 {
		t.Fatalf("persisted steer identity count=%d, want 1", persistedSteers)
	}

}

func TestQueuedUserWorkHasPriorityOverPluginWakeups(t *testing.T) {
	srv := &Server{pendingQueuedTurns: map[string][]queuedTurn{}}
	threadID := "priority-thread"
	srv.pendingQueuedTurns[threadID] = []queuedTurn{
		{id: "plugin", snapshot: turnRuntimeSnapshot{PluginTurn: &pluginTurnReference{PluginID: "example", RequestID: "continue"}}},
		{id: "user"},
	}
	first, ok := srv.takeNextQueuedUserTurn(threadID)
	if !ok || first.id != "user" {
		t.Fatalf("first queued turn = %+v, %t", first, ok)
	}
	second, ok := srv.takeNextQueuedUserTurn(threadID)
	if !ok || second.id != "plugin" {
		t.Fatalf("second queued turn = %+v, %t", second, ok)
	}
}

func TestPluginTurnLifecycleOutboxDropsInactivePluginEvents(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{response: providersResponse("done")})
	// An empty host has no lifecycle capability: delivery is impossible, so
	// terminal events must be dropped instead of replayed forever.
	rt.PluginHost = pluginhost.New()
	out := &lockedBuffer{}
	srv := New(rt, out)
	t.Cleanup(srv.Close)

	input := pluginhost.AgentTurnLifecycleInput{
		RequestID: "peer:req:ghost", State: pluginhost.TurnLifecycleCompleted,
		ThreadID: "thread-1", FinalOutput: "done",
	}
	payload, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	if err := rt.Sessions.PutPluginTurnLifecycleOutbox("ghost", input.RequestID, payload); err != nil {
		t.Fatal(err)
	}
	if err := srv.notifyPluginTurnLifecycle(testRuntimeState(t, rt), context.Background(), "ghost", input); err != nil {
		t.Fatalf("notifyPluginTurnLifecycle: %v", err)
	}
	entries, err := rt.Sessions.ListPluginTurnLifecycleOutbox()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("inactive plugin lifecycle events were not dropped: %+v", entries)
	}
}

type handoffBriefProvider struct{}

func (handoffBriefProvider) CompactionKey() string   { return "notecompaction" }
func (handoffBriefProvider) CompactionPriority() int { return 10 }
func (handoffBriefProvider) CompactionNotesEnabled() bool {
	return false
}
func (handoffBriefProvider) Compact(_ context.Context, _ string, messages []providers.ChatMessage) ([]providers.ChatMessage, error) {
	return messages, nil
}
func (handoffBriefProvider) PlanCompactionNote(_ context.Context, _ string, _, _ []providers.ChatMessage, _ agent.CompactionNote) (agent.CompactionNotePlan, error) {
	return agent.CompactionNotePlan{Prompt: "update the note", MaxBytes: 24_000}, nil
}
func (handoffBriefProvider) PlanHandoffBrief(_ context.Context, _ string, _ []providers.ChatMessage, _ agent.CompactionNote, _, _ string, _ int) (agent.CompactionNotePlan, error) {
	return agent.CompactionNotePlan{Prompt: "write the brief", MaxBytes: 24_000}, nil
}
func (handoffBriefProvider) CompactWithNote(_ context.Context, _ string, messages []providers.ChatMessage, _ agent.CompactionNote) (agent.CompactionNoteReplacement, error) {
	return agent.CompactionNoteReplacement{Messages: messages, CoveredMessages: len(messages)}, nil
}

func TestHandoffThreadStartUsesTargetModelWithoutCopyingSourceHistory(t *testing.T) {
	client := &fakeClient{
		// Background note requests share this client with the destination turn.
		// A finite response queue makes success depend on their scheduling.
		response: providersResponse("# Handoff brief\nContinue from the verified performance fix."),
	}
	rt := newTestRuntime(t, client)
	writeTargetedUpdateConfig(t, rt)
	bindConfigSourceForTest(t, rt)
	rt.PluginSessionRouter = runtime.NewPluginSessionRouter()
	rt.StreamRunner.CompactionRegistry = agent.NewCompactionRegistry()
	rt.StreamRunner.CompactionRegistry.Register(handoffBriefProvider{})
	out := &lockedBuffer{}
	srv := New(rt, out)
	t.Cleanup(srv.Close)

	if err := srv.handleLine(context.Background(), []byte(`{"id":"source","method":"thread/start"}`)); err != nil {
		t.Fatalf("source thread/start: %v", err)
	}
	sourceID := remarshal[ThreadStartResult](t, responseByID(t, parseOutput(t, out.String()), "source")["result"]).Thread.ID
	if err := rt.Sessions.AppendHistoryRecords(sourceID, []session.HistoryRecord{
		{Role: "user", Content: "SENTINEL-SOURCE-ONLY"},
		{Role: "assistant", Content: "investigation notes"},
	}); err != nil {
		t.Fatalf("append source history: %v", err)
	}

	payload, err := json.Marshal(map[string]any{
		"id":     "handoff",
		"method": "thread/start",
		"params": ThreadStartParams{
			Provider: "fake-provider",
			Model:    "fake-model",
			Handoff: &ThreadHandoffParams{
				RequestID:       "handoff-1",
				Revision:        1,
				ParentSessionID: sourceID,
				Intent:          "reconsider the visual layout",
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.handleLine(context.Background(), payload); err != nil {
		t.Fatalf("handoff thread/start: %v", err)
	}
	started := remarshal[ThreadStartResult](t, responseByID(t, parseOutput(t, out.String()), "handoff")["result"]).Thread
	if started.ModelProvider != "fake-provider" || started.Model != "fake-model" || started.Source != "desktop:handoff" {
		t.Fatalf("handoff thread = %+v", started)
	}
	waitForTurnCompletedForThread(t, out, started.ID)

	page, err := rt.Sessions.ReadHistoryPage(started.ID, 1, 20)
	if err != nil {
		t.Fatalf("read target history: %v", err)
	}
	var sawSeed, sawIntent, sawSentinel bool
	for _, record := range page.Records {
		if record.Content == "SENTINEL-SOURCE-ONLY" {
			sawSentinel = true
		}
		if record.Name == "context_seed" && strings.Contains(record.Content, "Handoff brief") {
			sawSeed = true
		}
		if record.Role == "user" && record.Content == "reconsider the visual layout" {
			sawIntent = true
		}
	}
	if sawSentinel || !sawSeed || !sawIntent {
		t.Fatalf("target history = %+v", page.Records)
	}
	metadata, ok, err := rt.Sessions.Find(started.ID)
	if err != nil || !ok || metadata.Owner != "user" || metadata.Provider != "fake-provider" || metadata.Model != "fake-model" {
		t.Fatalf("target metadata = %+v ok=%v err=%v", metadata, ok, err)
	}

	client.mu.Lock()
	requests := append([]providers.ChatRequest(nil), client.requests...)
	client.mu.Unlock()
	var firstTurn *providers.ChatRequest
	for index := range requests {
		req := &requests[index]
		for _, msg := range req.Messages {
			if msg.Role == "user" && strings.Contains(msg.Content, "reconsider the visual layout") && msg.Cause == session.SessionLaunchKindHandoff {
				firstTurn = req
			}
		}
	}
	if firstTurn == nil {
		t.Fatalf("missing destination first-turn request: %+v", requests)
	}
	if firstTurn.Model != "fake-model" {
		t.Fatalf("first turn request = %+v", firstTurn)
	}
	var sawBrief, sawLaunch bool
	for _, msg := range firstTurn.Messages {
		if strings.Contains(msg.Content, "SENTINEL-SOURCE-ONLY") {
			t.Fatalf("first turn copied source history: %+v", firstTurn.Messages)
		}
		if strings.Contains(msg.Content, "Handoff brief") {
			sawBrief = true
		}
		if msg.Role == "user" && strings.Contains(msg.Content, "reconsider the visual layout") {
			sawLaunch = true
		}
	}
	if !sawBrief || !sawLaunch {
		t.Fatalf("first turn messages = %+v", firstTurn.Messages)
	}

	if err := srv.handleLine(context.Background(), payload); err != nil {
		t.Fatalf("idempotent handoff thread/start: %v", err)
	}
	again := remarshal[ThreadStartResult](t, responseByID(t, parseOutput(t, out.String()), "handoff")["result"]).Thread
	if again.ID != started.ID {
		t.Fatalf("idempotent target = %q, want %q", again.ID, started.ID)
	}
	if len(again.Turns) != len(started.Turns) {
		t.Fatalf("idempotent retry created extra destination turns: before=%d after=%d", len(started.Turns), len(again.Turns))
	}
}

// Exercise the public plugin router against real Git repositories and persisted sessions.
func TestPluginWorkspaceCommittedDelivery(t *testing.T) {
	for _, mode := range []string{"committed", "mixed", "conflict", "untracked"} {
		t.Run(mode, func(t *testing.T) {
			rt := newTestRuntime(t, &fakeClient{response: providersResponse("unused")})
			initAppserverGitRepo(t, rt.RootDir)
			rt.PluginSessionRouter = runtime.NewPluginSessionRouter()
			srv := New(rt, &lockedBuffer{})
			t.Cleanup(srv.Close)
			ctx := context.Background()
			created, err := rt.PluginSessionRouter.Create(pluginInvocationContext(t, rt, ctx), "workspace-test", pluginhost.SessionCreateParams{
				RequestID: "create", Visibility: pluginhost.SessionVisibilityUser, ContextSource: pluginhost.SessionContextFresh, Workspace: "worktree",
			})
			if err != nil {
				t.Fatal(err)
			}
			metadata, found, err := rt.Sessions.Find(created.SessionID)
			if err != nil || !found {
				t.Fatalf("session lookup: found=%t err=%v", found, err)
			}
			write := func(root, name, content string) {
				t.Helper()
				if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0644); err != nil {
					t.Fatal(err)
				}
			}
			write(metadata.WorktreePath, "README.md", "committed output\n")
			runSessionWorkspaceGit(t, metadata.WorktreePath, "add", "README.md")
			runSessionWorkspaceGit(t, metadata.WorktreePath, "commit", "-qm", "worker output")
			wantFiles := []string{"README.md"}
			wantReadme := "committed output\n"
			if mode == "mixed" {
				write(metadata.WorktreePath, "staged.txt", "staged output\n")
				runSessionWorkspaceGit(t, metadata.WorktreePath, "add", "staged.txt")
				wantReadme = "committed and unstaged output\n"
				write(metadata.WorktreePath, "README.md", wantReadme)
				wantFiles = append(wantFiles, "staged.txt")
			}
			if mode == "conflict" {
				write(rt.RootDir, "README.md", "parent output\n")
			}
			if mode == "untracked" {
				write(metadata.WorktreePath, "untracked.txt", "keep me\n")
			}
			status, statusErr := rt.PluginSessionRouter.WorkspaceStatus(pluginInvocationContext(t, rt, ctx), "workspace-test", pluginhost.WorkspaceStatusParams{SessionID: created.SessionID})
			if statusErr != nil {
				t.Fatal(statusErr)
			}
			if !status.Dirty || !strings.Contains(status.Diff, "output") {
				t.Errorf("missing cumulative delivery: %+v", status)
			}
			if mode == "committed" || mode == "mixed" {
				if !reflect.DeepEqual(status.ChangedFiles, wantFiles) || !status.CanApply {
					t.Errorf("status=%+v, want files %v", status, wantFiles)
				}
				inspected, err := rt.PluginSessionRouter.Inspect(pluginInvocationContext(t, rt, ctx), "workspace-test", pluginhost.SessionInspectParams{SessionID: created.SessionID})
				if err != nil || inspected.Workspace == nil || !inspected.Workspace.Dirty || !reflect.DeepEqual(inspected.Workspace.ChangedFiles, wantFiles) {
					t.Errorf("inspect=%+v err=%v", inspected, err)
				}
			} else if status.CanApply {
				t.Errorf("unsafe workspace advertised as applicable: %+v", status)
			}
			result, err := rt.PluginSessionRouter.WorkspaceApply(pluginInvocationContext(t, rt, ctx), "workspace-test", pluginhost.WorkspaceApplyParams{SessionID: created.SessionID})
			if mode == "conflict" || mode == "untracked" {
				if err == nil {
					t.Fatalf("unsafe apply succeeded: %+v", result)
				}
				if _, err := os.Stat(metadata.WorktreePath); err != nil {
					t.Fatalf("workspace lost: %v", err)
				}
				stored, found, err := rt.Sessions.Find(created.SessionID)
				if err != nil || !found || stored.WorktreePath != metadata.WorktreePath {
					t.Fatalf("binding lost: %+v %v", stored, err)
				}
				parent, err := os.ReadFile(filepath.Join(rt.RootDir, "README.md"))
				expected := "hello\n"
				if mode == "conflict" {
					expected = "parent output\n"
				}
				if err != nil || string(parent) != expected {
					t.Fatalf("parent changed: %q %v", parent, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !result.Applied || !result.Discarded || !reflect.DeepEqual(result.ChangedFiles, wantFiles) {
				t.Fatalf("apply=%+v", result)
			}
			parent, err := os.ReadFile(filepath.Join(rt.RootDir, "README.md"))
			if err != nil || string(parent) != wantReadme {
				t.Fatalf("parent=%q err=%v", parent, err)
			}
			if mode == "mixed" {
				staged, err := os.ReadFile(filepath.Join(rt.RootDir, "staged.txt"))
				if err != nil || string(staged) != "staged output\n" {
					t.Fatalf("staged output=%q err=%v", staged, err)
				}
			}
			if _, err := os.Stat(metadata.WorktreePath); !os.IsNotExist(err) {
				t.Fatalf("applied worktree remains: %v", err)
			}
			stored, found, err := rt.Sessions.Find(created.SessionID)
			if err != nil || !found || stored.WorktreePath != "" {
				t.Fatalf("workspace not rebound: %+v %v", stored, err)
			}
		})
	}
}

// A plugin delivery reference does not make an authenticated user submission a
// generated wakeup. Stopping one reply must preserve later Personal messages,
// their idempotency identity, and their eventual lifecycle receipt.
func TestUserHostCancelReplyPreservesQueuedClientInput(t *testing.T) {
	for _, tc := range []struct{ name, mode, prompt string }{
		{name: "queue", mode: pluginhost.SessionIfRunningQueue, prompt: "another independent request"},
		{name: "steer", mode: pluginhost.SessionIfRunningSteer, prompt: "another independent request"},
		{name: "literal user text", mode: pluginhost.SessionIfRunningQueue, prompt: "<process_recheck>literal user request</process_recheck>"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mode := tc.mode
			firstStarted, releaseFirst, secondStarted, releaseSecond := make(chan struct{}), make(chan struct{}), make(chan struct{}), make(chan struct{})
			var release, releaseNext sync.Once
			client := &fakeClient{responses: []providers.ChatResponse{providersResponse("interrupted"), providersResponse("next reply")}, onChat: func(call int, _ providers.ChatRequest) {
				if call == 1 {
					close(firstStarted)
					<-releaseFirst
				}
				if call == 2 {
					close(secondStarted)
					<-releaseSecond
				}
			}}
			rt := newTestRuntime(t, client)
			rt.UserSpaceID, rt.ProductDomain = "local", "personal"
			owner := &pluginTurnLifecycleClient{id: "fixture", calls: make(chan pluginhost.AgentTurnLifecycleInput, 32)}
			rt.ActivePlugins = []pluginpkg.Plugin{{Manifest: pluginpkg.Manifest{ID: owner.id}}}
			attachTestModelGeneration(t, rt, owner)
			out := &lockedBuffer{}
			srv := New(rt, out)
			t.Cleanup(func() {
				release.Do(func() { close(releaseFirst) })
				releaseNext.Do(func() { close(releaseSecond) })
				srv.Close()
			})
			created, err := srv.createPluginSession(pluginInvocationContext(t, srv.rt, context.Background()), owner.id, pluginhost.SessionCreateParams{RequestID: "persistent-main", Visibility: pluginhost.SessionVisibilityPlugin, ContextSource: pluginhost.SessionContextFresh, AgentID: "assistant"})
			if err != nil {
				t.Fatal(err)
			}
			first, err := srv.sendPluginSession(pluginInvocationContext(t, srv.rt, context.Background()), owner.id, pluginhost.SessionSendParams{RequestID: "first", SessionID: created.SessionID, Input: pluginhost.SessionInput{Prompt: "first reply"}})
			if err != nil || first.State != pluginhost.TurnLifecycleRunning {
				t.Fatalf("first send: %+v %v", first, err)
			}
			select {
			case <-firstStarted:
			case <-time.After(3 * time.Second):
				t.Fatal("first reply not running")
			}
			background, err := srv.sendPluginSession(pluginInvocationContext(t, srv.rt, context.Background()), owner.id, pluginhost.SessionSendParams{RequestID: "background", SessionID: created.SessionID, Input: pluginhost.SessionInput{Prompt: "generated continuation"}})
			if err != nil || background.State != pluginhost.TurnLifecycleQueued {
				t.Fatalf("background send: %+v %v", background, err)
			}
			proof := runtime.ClientUserInput{RequestID: "foreground", Prompt: tc.prompt}
			ctx := runtime.WithClientUserInput(context.Background(), owner.id, proof)
			params := pluginhost.SessionSendParams{IfRunning: mode, RequestID: "user-next", UserInputRequestID: proof.RequestID, SessionID: created.SessionID, Input: pluginhost.SessionInput{Prompt: proof.Prompt}}
			if _, err := srv.sendPluginSession(pluginInvocationContext(t, srv.rt, context.Background()), owner.id, params); err == nil {
				t.Fatal("untrusted generated call impersonated a user")
			}
			queued, err := srv.sendPluginSession(pluginInvocationContext(t, srv.rt, ctx), owner.id, params)
			wantState := pluginhost.TurnLifecycleQueued
			if mode == pluginhost.SessionIfRunningSteer {
				wantState = pluginhost.TurnLifecycleRunning
			}
			if err != nil || queued.State != wantState || (mode == pluginhost.SessionIfRunningQueue && queued.QueueID == "") || (mode == pluginhost.SessionIfRunningSteer && (!queued.Steered || queued.TurnID != first.TurnID)) {
				t.Fatalf("user send: %+v %v", queued, err)
			}
			cancelled, err := srv.cancelPluginSession(pluginInvocationContext(t, srv.rt, context.Background()), owner.id, pluginhost.SessionCancelParams{SessionID: created.SessionID, TurnID: first.TurnID})
			if err != nil || !cancelled.Cancelled {
				t.Fatalf("stop reply: %+v %v", cancelled, err)
			}
			// Hold the successor inside the provider so the retry deterministically
			// observes queue promotion, not a scheduling-dependent pending entry.
			release.Do(func() { close(releaseFirst) })
			select {
			case <-secondStarted:
			case <-time.After(3 * time.Second):
				t.Fatal("queued user message did not resume after stopped reply")
			}
			retry, err := srv.sendPluginSession(pluginInvocationContext(t, srv.rt, ctx), owner.id, params)
			// Live non-receipt lookup identifies an admitted turn by TurnID;
			// QueueID is optional there, but the promotion and receipt retain it.
			if err != nil || retry.State != pluginhost.TurnLifecycleRunning || retry.TurnID == "" || retry.TurnID == first.TurnID || retry.Steered || (mode == pluginhost.SessionIfRunningQueue && retry.QueueID != queued.QueueID) {
				t.Fatalf("stop lost promoted identity: %+v %+v %v", queued, retry, err)
			}
			promotedQueueID := queued.QueueID
			if mode == pluginhost.SessionIfRunningSteer {
				promotedQueueID = pluginSessionRequestClientID(owner.id, params.RequestID)
			}
			promoted := false
			for _, message := range parseOutput(t, out.String()) {
				if message["method"] != NotificationTurnStarted {
					continue
				}
				started := remarshal[TurnStartedNotification](t, message["params"])
				if started.ThreadID == created.SessionID && started.Turn.ID == retry.TurnID && started.QueueID == promotedQueueID {
					promoted = true
				}
			}
			if !promoted {
				t.Fatalf("queued identity %q did not promote into %q", promotedQueueID, retry.TurnID)
			}
			releaseNext.Do(func() { close(releaseSecond) })
			deadline := time.After(5 * time.Second)
			for {
				select {
				case receipt := <-owner.calls:
					if receipt.RequestID == "background" && receipt.State == pluginhost.TurnLifecycleRunning {
						t.Fatal("generated wakeup survived cancellation")
					}
					if receipt.RequestID == params.RequestID {
						if receipt.State == pluginhost.TurnLifecycleDiscarded {
							t.Fatalf("user message discarded: %+v", receipt)
						}
						if receipt.State == pluginhost.TurnLifecycleCompleted {
							if receipt.TurnID != retry.TurnID || receipt.QueueID != promotedQueueID {
								t.Fatalf("completion lost promoted identity: %+v", receipt)
							}
							history, err := loadChatMessages(rt.Sessions, created.SessionID)
							if err != nil {
								t.Fatal(err)
							}
							count := 0
							for _, msg := range history {
								if msg.ClientID == pluginSessionRequestClientID(owner.id, params.RequestID) {
									count++
									if msg.Origin != "user" || msg.Content != proof.Prompt {
										t.Fatalf("user provenance changed: %+v", msg)
									}
								}
							}
							if count != 1 {
								t.Fatalf("user input admitted %d times", count)
							}
							return
						}
					}
				case <-deadline:
					t.Fatal("preserved user input never completed")
				}
			}
		})
	}
}

func TestPluginProcessRecipientWaitsForChildExecutionOwnership(t *testing.T) {
	for _, activity := range []string{"durable_lease", "admission", "claimed_queue"} {
		t.Run(activity, func(t *testing.T) {
			rt := newTestRuntime(t, &fakeClient{})
			srv := New(rt, &lockedBuffer{})
			t.Cleanup(srv.Close)
			parent, err := srv.createPluginSession(pluginInvocationContext(t, rt, context.Background()), "subagent", pluginhost.SessionCreateParams{
				RequestID: "parent", Visibility: pluginhost.SessionVisibilityUser, ContextSource: pluginhost.SessionContextFresh,
			})
			if err != nil {
				t.Fatal(err)
			}
			child, err := srv.createPluginSession(pluginInvocationContext(t, rt, context.Background()), "subagent", pluginhost.SessionCreateParams{
				RequestID: "child", Visibility: pluginhost.SessionVisibilityPlugin, ContextSource: pluginhost.SessionContextFresh, ParentSessionID: parent.SessionID,
			})
			if err != nil {
				t.Fatal(err)
			}
			th := srv.thread(child.SessionID)
			var release func()
			switch activity {
			case "durable_lease":
				lease, acquired, err := rt.Sessions.TryAcquireThreadExecutionLease(rt.SessionDir, child.SessionID)
				if err != nil || !acquired {
					t.Fatalf("reserve child execution: %v, %v", acquired, err)
				}
				release = func() {
					if err := lease.Release(); err != nil {
						t.Error(err)
					}
				}
			case "admission":
				th.mu.Lock()
				th.admissionReserved = true
				th.mu.Unlock()
				release = func() { th.mu.Lock(); th.admissionReserved = false; th.mu.Unlock() }
			case "claimed_queue":
				entry := queuedTurn{id: "child-followup", msg: providers.ChatMessage{Role: "user", Content: "continue"}}
				if !srv.enqueueQueuedUserTurn(child.SessionID, entry) {
					t.Fatal("queue rejected")
				}
				if _, ok := srv.takeNextQueuedUserTurn(child.SessionID); !ok {
					t.Fatal("queue claim missing")
				}
				release = func() { srv.settleQueuedTurnClaim(child.SessionID, entry, false) }
			}
			var once sync.Once
			releaseOnce := func() { once.Do(release) }
			t.Cleanup(releaseOnce)
			id, closed, err := srv.resolvePluginProcessSession(pluginInvocationContext(t, rt, context.Background()), child.SessionID, true)
			if err != nil || closed || id != child.SessionID {
				t.Fatalf("active child recipient = %q, closed=%v, err=%v", id, closed, err)
			}
			releaseOnce()
			id, closed, err = srv.resolvePluginProcessSession(pluginInvocationContext(t, rt, context.Background()), child.SessionID, true)
			if err != nil || closed || id != parent.SessionID {
				t.Fatalf("completed child recipient = %q, closed=%v, err=%v", id, closed, err)
			}
		})
	}
}

func TestPluginProcessDeliveryHonorsRecipientControl(t *testing.T) {
	for _, manager := range []string{"project-controller", "plugin:coordinator"} {
		for _, state := range []string{session.ControlActive, session.ControlPaused, session.ControlTakenOver} {
			t.Run(manager+"/"+state, func(t *testing.T) {
				rt := newTestRuntime(t, &fakeClient{})
				srv := New(rt, &lockedBuffer{})
				t.Cleanup(srv.Close)
				visibility := pluginhost.SessionVisibilityUser
				if strings.HasPrefix(manager, "plugin:") {
					visibility = pluginhost.SessionVisibilityPlugin
				}
				created, err := srv.createPluginSession(pluginInvocationContext(t, rt, context.Background()), "subagent", pluginhost.SessionCreateParams{
					RequestID: "recipient", Visibility: visibility, ContextSource: pluginhost.SessionContextFresh,
				})
				if err != nil {
					t.Fatal(err)
				}
				control, err := rt.Sessions.ChangeControl(created.SessionID, manager, state, 0)
				if err != nil {
					t.Fatal(err)
				}
				th := srv.thread(created.SessionID)
				th.mu.Lock()
				th.running, th.currentTurn = true, "ongoing-turn"
				th.mu.Unlock()
				t.Cleanup(func() { th.mu.Lock(); th.running, th.currentTurn = false, ""; th.mu.Unlock() })
				params := pluginhost.SessionSendParams{
					RequestID: "observation", SessionID: created.SessionID, IfRunning: pluginhost.SessionIfRunningSteer,
					Input: pluginhost.SessionInput{Prompt: "The monitored command completed."},
				}
				if _, err := srv.sendPluginSession(pluginInvocationContext(t, rt, context.Background()), "monitor", params); err == nil {
					t.Fatal("ordinary plugin send bypassed recipient ownership or control")
				}
				ctx := pluginhost.WithProcessSessionDelivery(context.Background(), created.SessionID)
				result, err := srv.sendPluginSession(pluginInvocationContext(t, rt, ctx), "monitor", params)
				if state != session.ControlActive {
					if err == nil {
						t.Fatalf("process event bypassed %s control: %+v", state, result)
					}
					return
				}
				if err != nil || !result.Steered || result.SessionID != created.SessionID {
					t.Fatalf("authorized process observation rejected: %+v, %v", result, err)
				}
				th.mu.Lock()
				captured := th.pendingSteerControls[pluginSessionRequestClientID("monitor", params.RequestID)]
				th.mu.Unlock()
				if captured != control {
					t.Fatalf("delivery control = %+v, want %+v", captured, control)
				}
				if _, err := rt.Sessions.ChangeControl(created.SessionID, manager, session.ControlPaused, control.Revision); err != nil {
					t.Fatal(err)
				}
				th.mu.Lock()
				srv.pruneRevokedSteersLocked(th)
				remaining := len(th.pendingSteers)
				th.mu.Unlock()
				if remaining != 0 {
					t.Fatal("paused control did not revoke accepted process observation")
				}
			})
		}
	}
}

// Replacement may happen before handoff admission or while plugin planning
// yields. Neither boundary may send an old transcript to the successor client.
func TestHandoffBriefDoesNotCrossCapturedState(t *testing.T) {
	for _, replaceAt := range []string{"before-admission", "during-plan"} {
		t.Run(replaceAt, func(t *testing.T) {
			oldClient := &fakeClient{response: providersResponse("# Original brief")}
			newClient := &fakeClient{response: providersResponse("# Successor brief")}
			original := newTestRuntime(t, oldClient)
			successor := newTestRuntime(t, newClient)
			original.Model, original.StreamRunner.Model = "original-model", "original-model"
			successor.Model, successor.StreamRunner.Model = "successor-model", "successor-model"
			var plannedModel string
			successorPlans := 0
			planner := handoffBriefPlanningProvider{plan: func(model string) { plannedModel = model }}
			original.StreamRunner.CompactionRegistry = agent.NewCompactionRegistry()
			original.StreamRunner.CompactionRegistry.Register(planner)
			successor.StreamRunner.CompactionRegistry = agent.NewCompactionRegistry()
			successor.StreamRunner.CompactionRegistry.Register(handoffBriefPlanningProvider{plan: func(string) { successorPlans++ }})
			if _, err := original.Sessions.CreateWithMetadata("source", original.RootDir); err != nil {
				t.Fatal(err)
			}
			if err := original.Sessions.AppendHistoryRecords("source", []session.HistoryRecord{{Role: "user", Content: "ORIGINAL-TRANSCRIPT"}}); err != nil {
				t.Fatal(err)
			}
			binding, err := original.RetainState()
			if err != nil {
				t.Fatal(err)
			}
			defer binding.Close()
			srv := &Server{rt: original}
			if replaceAt == "before-admission" {
				srv.rt = successor
			} else {
				// This embedded runtime replaces its root runner during the callback,
				// matching publication's in-place client/model update after admission.
				planner.plan = func(model string) {
					plannedModel = model
					original.Model = successor.Model
					original.StreamRunner = successor.StreamRunner
				}
				original.StreamRunner.CompactionRegistry.Register(planner)
			}
			params := pluginhost.SessionCreateParams{RequestID: "captured-handoff", Visibility: pluginhost.SessionVisibilityPlugin, Provider: "fake-provider", Model: "original-model", ContextSource: pluginhost.SessionContextSourceSeed, Seed: &pluginhost.SessionContextSeed{Source: pluginhost.SessionHistorySnapshot{SessionID: "source"}}}
			var result pluginhost.SessionCreateParams
			if replaceAt == "before-admission" {
				_, err = srv.createPluginSession(runtime.WithInvocationState(context.Background(), binding), "handoff-test", params)
			} else {
				result, err = srv.prepareHandoffSeed(binding, context.Background(), params)
			}
			newClient.mu.Lock()
			newerCalls := len(newClient.requests)
			newClient.mu.Unlock()
			if newerCalls != 0 || successorPlans != 0 {
				t.Fatalf("captured transcript reached successor client: calls=%d result=%+v err=%v", newerCalls, result.Seed, err)
			}
			oldClient.mu.Lock()
			originalRequests := append([]providers.ChatRequest(nil), oldClient.requests...)
			oldClient.mu.Unlock()
			if replaceAt == "before-admission" {
				if !errors.Is(err, runtime.ErrStateGenerationChanged) || plannedModel != "" || len(originalRequests) != 0 {
					t.Fatalf("stale handoff was not rejected before effects: planned=%q calls=%d err=%v", plannedModel, len(originalRequests), err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(originalRequests) != 1 || originalRequests[0].Model != "original-model" {
				t.Fatalf("fork lost captured model: %+v", originalRequests)
			}
			if result.Seed.Body != "# Original brief" || result.Seed.Provenance.SourceModel != "original-model" || plannedModel != "original-model" {
				t.Fatalf("mixed handoff: seed=%+v plannedModel=%q", result.Seed, plannedModel)
			}
		})
	}
}

type handoffBriefPlanningProvider struct {
	handoffBriefProvider
	plan func(string)
}

func (p handoffBriefPlanningProvider) PlanHandoffBrief(_ context.Context, model string, _ []providers.ChatMessage, _ agent.CompactionNote, _, _ string, _ int) (agent.CompactionNotePlan, error) {
	p.plan(model)
	return agent.CompactionNotePlan{Prompt: "write the brief", MaxBytes: 24000}, nil
}

func TestHandoffPreservesSelectedUnlimitedPlan(t *testing.T) {
	body := strings.Repeat("x", 25*1024)
	rt := newTestRuntime(t, &fakeClient{response: providersResponse(body)})
	attempts := 0
	rt.StreamRunner.SelectedCompaction = compactionservice.NewClient(func(_ context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
		switch method {
		case "describe":
			return json.Marshal(compactionservice.Policy{Key: "external", Guidance: []providers.ChatMessage{}})
		case "handoff":
			return json.Marshal(compactionservice.HandoffPlan{Prompt: "selected plan", MaxBytes: 0})
		case "note-attempt":
			attempts++
			var in compactionservice.NoteAttemptRequest
			if err := json.Unmarshal(raw, &in); err != nil {
				return nil, err
			}
			if in.Plan.MaxBytes != 0 {
				t.Errorf("host changed selected limit to %d", in.Plan.MaxBytes)
			}
			return json.Marshal(compactionservice.NoteAttemptPlan{Invoke: true, Messages: []providers.ChatMessage{{Role: "user", Content: "selected inference"}}, Prompt: "selected inference", MaxBytes: 0})
		default:
			t.Fatalf("unexpected policy method %s", method)
			return nil, nil
		}
	})
	if _, err := rt.Sessions.CreateWithMetadata("source", rt.RootDir); err != nil {
		t.Fatal(err)
	}
	if err := rt.Sessions.AppendHistoryRecords("source", []session.HistoryRecord{{Role: "user", Content: "source"}}); err != nil {
		t.Fatal(err)
	}
	binding, err := rt.RetainState()
	if err != nil {
		t.Fatal(err)
	}
	defer binding.Close()
	srv := &Server{rt: rt}
	result, err := srv.prepareHandoffSeed(binding, context.Background(), pluginhost.SessionCreateParams{ContextSource: pluginhost.SessionContextSourceSeed, Seed: &pluginhost.SessionContextSeed{Source: pluginhost.SessionHistorySnapshot{SessionID: "source"}}})
	if err != nil || result.Seed.Body != body || attempts != 1 {
		t.Fatalf("selected unlimited handoff rejected: bytes=%d attempts=%d err=%v", len(result.Seed.Body), attempts, err)
	}
}

func TestDelegatedChildResolvedModelPreservesParentOptions(t *testing.T) {
	for _, inherit := range []bool{true, false} {
		t.Run(map[bool]string{true: "omitted", false: "explicit"}[inherit], func(t *testing.T) {
			rt := newTestRuntime(t, &fakeClient{})
			srv := New(rt, &lockedBuffer{})
			t.Cleanup(srv.Close)
			parent := session.Session{ID: "model-parent", Owner: "plugin:subagent", Visibility: "plugin", CWD: rt.RootDir,
				Provider: "fake-provider", Model: "fake-model", Variant: "retained-variant", Effort: "high", Speed: "fast", ModelOptions: map[string]string{"reasoning": "retained"}, PermissionMode: "standard"}
			if _, err := rt.Sessions.CreateInitialized(parent, nil); err != nil {
				t.Fatal(err)
			}
			binding := testRuntimeState(t, rt)
			defer binding.Close()
			created, err := srv.createPluginSessionThread(binding, parent.Owner, pluginhost.SessionCreateParams{
				RequestID: "resolved-child", ParentSessionID: parent.ID, ContextSource: "fresh", Visibility: "plugin",
				Provider: parent.Provider, Model: parent.Model, PermissionMode: parent.PermissionMode,
			}, inherit)
			if err != nil {
				t.Fatal(err)
			}
			child, found, err := rt.Sessions.Find(created.ID)
			if err != nil || !found {
				t.Fatalf("created child missing: %v", err)
			}
			if child.Provider != parent.Provider || child.Model != parent.Model || child.PermissionMode != parent.PermissionMode {
				t.Fatalf("admitted selection changed: %+v", child)
			}
			if inherit {
				if child.Variant != parent.Variant || child.Effort != parent.Effort || child.Speed != parent.Speed || !reflect.DeepEqual(child.ModelOptions, parent.ModelOptions) {
					t.Fatalf("omitted selection lost inherited options: %+v", child)
				}
			} else if child.Variant != "" || child.Effort != "" || child.Speed != "" || len(child.ModelOptions) != 0 {
				t.Fatalf("explicit model selection no longer resets inherited options: %+v", child)
			}
		})
	}
}

func TestPluginSessionCreationIdentityIsOwnedDiscoveryMetadata(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{response: providersResponse("done")})
	rt.PluginSessionRouter = runtime.NewPluginSessionRouter()
	srv := New(rt, &lockedBuffer{})
	t.Cleanup(srv.Close)
	ctx := pluginInvocationContext(t, rt, context.Background())
	for _, visibility := range []string{pluginhost.SessionVisibilityPlugin, pluginhost.SessionVisibilityUser} {
		t.Run(visibility, func(t *testing.T) {
			requestID := "creation-" + visibility
			created, err := rt.PluginSessionRouter.Create(ctx, "subagent", pluginhost.SessionCreateParams{
				RequestID: requestID, Name: "Recover creation", Visibility: visibility, ContextSource: pluginhost.SessionContextFresh,
			})
			if err != nil {
				t.Fatal(err)
			}
			// Discovery must work before a first turn exists, after the create response was lost.
			listed, err := rt.PluginSessionRouter.List(ctx, "subagent", pluginhost.SessionListParams{Scope: pluginhost.SessionListScopeOwned})
			if err != nil {
				t.Fatal(err)
			}
			var found *pluginhost.SessionSummary
			for i := range listed.Sessions {
				if listed.Sessions[i].SessionID == created.SessionID {
					found = &listed.Sessions[i]
				}
			}
			if found == nil {
				t.Fatal("created session missing from owned discovery")
			}
			assertCreationIdentity := func(value any, expected string) {
				t.Helper()
				raw, err := json.Marshal(value)
				if err != nil {
					t.Fatal(err)
				}
				var fields map[string]any
				if err := json.Unmarshal(raw, &fields); err != nil {
					t.Fatal(err)
				}
				actual, present := fields["creation_request_id"]
				if expected == "" {
					if present {
						t.Fatalf("creation identity leaked: %s", raw)
					}
				} else if actual != expected {
					t.Fatalf("creation identity = %v, want %q: %s", actual, expected, raw)
				}
			}
			assertCreationIdentity(found, requestID)
			inspected, err := rt.PluginSessionRouter.Inspect(ctx, "subagent", pluginhost.SessionInspectParams{SessionID: created.SessionID})
			if err != nil {
				t.Fatal(err)
			}
			assertCreationIdentity(inspected.Session, requestID)
			if inspected.Turn != nil {
				t.Fatalf("new session unexpectedly has a turn: %+v", inspected.Turn)
			}
			if visibility != pluginhost.SessionVisibilityUser {
				return
			}
			for _, caller := range []string{"subagent", "other"} {
				shared, err := rt.PluginSessionRouter.List(ctx, caller, pluginhost.SessionListParams{Scope: pluginhost.SessionListScopeShared})
				if err != nil {
					t.Fatal(err)
				}
				for _, item := range shared.Sessions {
					assertCreationIdentity(item, "")
				}
			}
			borrowed, err := rt.PluginSessionRouter.Inspect(ctx, "other", pluginhost.SessionInspectParams{SessionID: created.SessionID, RequestID: "own-correlated-request"})
			if err != nil {
				t.Fatal(err)
			}
			assertCreationIdentity(borrowed.Session, "")
		})
	}
}
