package appserver

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/agent"
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
	rt.PluginHost = pluginhost.New(owner, other)
	out := &lockedBuffer{}
	srv := New(rt, out)
	t.Cleanup(srv.Close)
	if _, err := rt.PluginSessionRouter.Create(context.Background(), owner.id, pluginhost.SessionCreateParams{
		RequestID: "wrong-workspace", Visibility: pluginhost.SessionVisibilityUser, ContextSource: pluginhost.SessionContextFresh,
		WorkspaceID: "workspace-two", WorkspaceRoot: rt.RootDir,
	}); err == nil {
		t.Fatal("session creation accepted a different target workspace")
	}

	created, err := rt.PluginSessionRouter.Create(context.Background(), owner.id, pluginhost.SessionCreateParams{
		RequestID: "create-1", Visibility: pluginhost.SessionVisibilityUser, ContextSource: pluginhost.SessionContextFresh,
		WorkspaceID: rt.WorkspaceID, WorkspaceRoot: rt.RootDir,
	})
	if err != nil || created.SessionID == "" || !created.Created {
		t.Fatalf("create = %+v, %v", created, err)
	}
	result, err := rt.PluginSessionRouter.Send(context.Background(), owner.id, pluginhost.SessionSendParams{
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

	persisted, ok, err := session.Find(rt.SessionDir, result.SessionID)
	if err != nil || !ok || persisted.Source != "plugin:schedule" || persisted.Owner != "plugin:schedule" || persisted.Visibility != pluginhost.SessionVisibilityUser {
		t.Fatalf("persisted thread = %+v, %t, %v", persisted, ok, err)
	}
	records, err := session.LoadHistoryRecords(rt.SessionDir, result.SessionID, true)
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
	loaded, err := srv.ensureThreadLoaded(result.SessionID)
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
	rt.PluginHost = pluginhost.New(owner)
	out := &lockedBuffer{}
	srv := New(rt, out)
	created, err := rt.PluginSessionRouter.Create(context.Background(), owner.id, pluginhost.SessionCreateParams{
		RequestID: "create-instructions", Visibility: pluginhost.SessionVisibilityUser, ContextSource: pluginhost.SessionContextFresh,
		WorkspaceID: rt.WorkspaceID, WorkspaceRoot: rt.RootDir, Instructions: instructions,
	})
	if err != nil {
		t.Fatal(err)
	}
	send := func(requestID string, out *lockedBuffer) {
		t.Helper()
		result, err := rt.PluginSessionRouter.Send(context.Background(), owner.id, pluginhost.SessionSendParams{
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
	rt.PluginHost = pluginhost.New(owner)
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

	created, err := rt.PluginSessionRouter.Create(context.Background(), owner.id, pluginhost.SessionCreateParams{
		RequestID: "create", Visibility: pluginhost.SessionVisibilityPlugin, ContextSource: pluginhost.SessionContextFresh,
	})
	if err != nil {
		t.Fatal(err)
	}
	callCtx, cancelCall := context.WithCancel(context.Background())
	result, err := rt.PluginSessionRouter.Send(callCtx, owner.id, pluginhost.SessionSendParams{
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
	created, err := rt.PluginSessionRouter.Create(context.Background(), "subagent", pluginhost.SessionCreateParams{RequestID: "owned", Name: "review_parser", Visibility: pluginhost.SessionVisibilityPlugin, ContextSource: pluginhost.SessionContextFresh})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rt.PluginSessionRouter.Send(context.Background(), "subagent", pluginhost.SessionSendParams{RequestID: "run", SessionID: created.SessionID, Input: pluginhost.SessionInput{Prompt: "work"}}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-chatStarted:
	case <-time.After(3 * time.Second):
		t.Fatal("turn did not start")
	}
	listed, err := rt.PluginSessionRouter.List(context.Background(), "subagent", pluginhost.SessionListParams{})
	if err != nil || len(listed.Sessions) != 1 || listed.Sessions[0].SessionID != created.SessionID || listed.Sessions[0].Name != "review_parser" || listed.Sessions[0].State != pluginhost.TurnLifecycleRunning {
		t.Fatalf("list = %+v, %v", listed, err)
	}
	other, err := rt.PluginSessionRouter.List(context.Background(), "other", pluginhost.SessionListParams{})
	if err != nil || len(other.Sessions) != 0 {
		t.Fatalf("other list = %+v, %v", other, err)
	}
	if _, err := rt.PluginSessionRouter.Cancel(context.Background(), "other", pluginhost.SessionCancelParams{SessionID: created.SessionID}); err == nil {
		t.Fatal("another plugin cancelled an owned session")
	}
	cancelled, err := rt.PluginSessionRouter.Cancel(context.Background(), "subagent", pluginhost.SessionCancelParams{SessionID: created.SessionID})
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
	rt.PluginHost = pluginhost.New(observer)
	srv := New(rt, &lockedBuffer{})
	t.Cleanup(func() { close(release); srv.Close() })

	created, err := rt.PluginSessionRouter.Create(context.Background(), observer.id, pluginhost.SessionCreateParams{
		RequestID: "owned-turn", Visibility: pluginhost.SessionVisibilityPlugin, ContextSource: pluginhost.SessionContextFresh,
	})
	if err != nil {
		t.Fatal(err)
	}
	started, err := rt.PluginSessionRouter.Send(context.Background(), observer.id, pluginhost.SessionSendParams{
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
	queued, err := rt.PluginSessionRouter.Send(context.Background(), observer.id, pluginhost.SessionSendParams{
		RequestID: "queued-turn", SessionID: created.SessionID, Input: pluginhost.SessionInput{Prompt: "later"},
	})
	if err != nil || queued.QueueID == "" {
		t.Fatalf("queued send = %+v, %v", queued, err)
	}
	queuedCancel, err := rt.PluginSessionRouter.Cancel(context.Background(), observer.id, pluginhost.SessionCancelParams{SessionID: created.SessionID, QueueID: queued.QueueID})
	if err != nil || !queuedCancel.Cancelled || queuedCancel.QueueID != queued.QueueID {
		t.Fatalf("queued cancel = %+v, %v", queuedCancel, err)
	}

	mismatch, err := rt.PluginSessionRouter.Cancel(context.Background(), observer.id, pluginhost.SessionCancelParams{SessionID: created.SessionID, TurnID: "not-current"})
	if err != nil || mismatch.Cancelled {
		t.Fatalf("mismatched cancel = %+v, %v", mismatch, err)
	}
	cancelled, err := rt.PluginSessionRouter.Cancel(context.Background(), observer.id, pluginhost.SessionCancelParams{SessionID: created.SessionID, TurnID: started.TurnID})
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
	rt.PluginHost = pluginhost.New(lifecycle)
	out := &lockedBuffer{}
	srv := New(rt, out)
	t.Cleanup(func() {
		close(lifecycle.release)
		srv.Close()
	})

	created, err := rt.PluginSessionRouter.Create(context.Background(), lifecycle.ID(), pluginhost.SessionCreateParams{
		RequestID: "create", Name: "child", Visibility: pluginhost.SessionVisibilityPlugin, ContextSource: pluginhost.SessionContextFresh,
	})
	if err != nil {
		t.Fatal(err)
	}
	started, err := rt.PluginSessionRouter.Send(context.Background(), lifecycle.ID(), pluginhost.SessionSendParams{
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
	queued, err := rt.PluginSessionRouter.Send(context.Background(), lifecycle.ID(), pluginhost.SessionSendParams{
		RequestID: "second", SessionID: created.SessionID, Input: pluginhost.SessionInput{Prompt: "second"},
	})
	if err != nil || queued.State != pluginhost.TurnLifecycleQueued || queued.QueueID == "" {
		t.Fatalf("second send = %+v, %v", queued, err)
	}

	cancelled := make(chan error, 1)
	go func() {
		_, cancelErr := rt.PluginSessionRouter.Cancel(context.Background(), lifecycle.ID(), pluginhost.SessionCancelParams{SessionID: created.SessionID})
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
	params := pluginhost.SessionCreateParams{RequestID: "same-create", Visibility: pluginhost.SessionVisibilityPlugin, ContextSource: pluginhost.SessionContextFresh}
	first, err := rt.PluginSessionRouter.Create(context.Background(), "dream", params)
	if err != nil || !first.Created {
		t.Fatalf("first create = %+v, %v", first, err)
	}
	second, err := rt.PluginSessionRouter.Create(context.Background(), "dream", params)
	if err != nil || second.Created || second.SessionID != first.SessionID {
		t.Fatalf("idempotent create = %+v, %v", second, err)
	}
	sources, err := srv.threadSearchSources()
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
	rt.PluginHost = pluginhost.New(owner)
	out := &lockedBuffer{}
	srv := New(rt, out)
	t.Cleanup(srv.Close)

	created, err := rt.PluginSessionRouter.Create(context.Background(), owner.id, pluginhost.SessionCreateParams{RequestID: "create", Visibility: pluginhost.SessionVisibilityUser, ContextSource: pluginhost.SessionContextFresh})
	if err != nil {
		t.Fatal(err)
	}
	first, err := rt.PluginSessionRouter.Send(context.Background(), owner.id, pluginhost.SessionSendParams{RequestID: "first", SessionID: created.SessionID, Input: pluginhost.SessionInput{Prompt: "first"}})
	if err != nil || first.State != pluginhost.TurnLifecycleRunning {
		t.Fatalf("first submit = %+v, %v", first, err)
	}
	select {
	case <-chatStarted:
	case <-time.After(3 * time.Second):
		t.Fatal("first turn did not start")
	}
	second, err := rt.PluginSessionRouter.Send(context.Background(), owner.id, pluginhost.SessionSendParams{
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
	owner := &pluginTurnLifecycleClient{id: "subagent", calls: make(chan pluginhost.AgentTurnLifecycleInput, 8)}
	rt.PluginHost = pluginhost.New(owner)
	out := &lockedBuffer{}
	srv := New(rt, out)
	t.Cleanup(srv.Close)

	created, err := rt.PluginSessionRouter.Create(context.Background(), owner.id, pluginhost.SessionCreateParams{RequestID: "create", Visibility: pluginhost.SessionVisibilityPlugin, ContextSource: pluginhost.SessionContextFresh})
	if err != nil {
		t.Fatal(err)
	}
	first, err := rt.PluginSessionRouter.Send(context.Background(), owner.id, pluginhost.SessionSendParams{RequestID: "first", SessionID: created.SessionID, Input: pluginhost.SessionInput{Prompt: "work"}})
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
	steered, err := rt.PluginSessionRouter.Send(context.Background(), owner.id, params)
	if err != nil || !steered.Steered || steered.State != pluginhost.TurnLifecycleRunning || steered.TurnID != first.TurnID || steered.QueueID != "" {
		t.Fatalf("steer = %+v, %v", steered, err)
	}
	replayed, err := rt.PluginSessionRouter.Send(context.Background(), owner.id, params)
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

	close(releaseFirst)
	waitForTurnCompletedForThread(t, out, created.SessionID)
	thread.mu.Lock()
	turns := len(thread.Turns)
	thread.mu.Unlock()
	if turns != 1 {
		t.Fatalf("steer created %d turns, want 1", turns)
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
	if err := session.PutPluginTurnLifecycleOutbox(rt.SessionDir, "ghost", input.RequestID, payload); err != nil {
		t.Fatal(err)
	}
	if err := srv.notifyPluginTurnLifecycle(context.Background(), "ghost", input); err != nil {
		t.Fatalf("notifyPluginTurnLifecycle: %v", err)
	}
	entries, err := session.ListPluginTurnLifecycleOutbox(rt.SessionDir)
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
	if err := session.AppendHistoryRecords(rt.SessionDir, sourceID, []session.HistoryRecord{
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

	page, err := session.ReadHistoryPage(context.Background(), rt.SessionDir, started.ID, 1, 20)
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
	metadata, ok, err := session.Find(rt.SessionDir, started.ID)
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
