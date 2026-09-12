package appserver

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/channels"
	"github.com/blueberrycongee/wuu/internal/providers"
)

// Real session reconstruction and persistence take longer under the race
// detector; events still determine completion, not the timeout duration.
const collaborationTestWaitTimeout = 30 * time.Second

type collaborationRPCCall struct {
	request providers.ChatRequest
	ctx     context.Context
	release chan struct{}
	stream  chan providers.StreamEvent
}

type collaborationRPCProvider struct {
	started chan *collaborationRPCCall
}

func (client *collaborationRPCProvider) Chat(context.Context, providers.ChatRequest) (providers.ChatResponse, error) {
	return providers.ChatResponse{}, fmt.Errorf("unexpected non-streaming request")
}

func (client *collaborationRPCProvider) StreamChat(ctx context.Context, request providers.ChatRequest) (<-chan providers.StreamEvent, error) {
	call := &collaborationRPCCall{request: request, ctx: ctx, release: make(chan struct{}), stream: make(chan providers.StreamEvent)}
	client.started <- call
	events := make(chan providers.StreamEvent, 2)
	go func() {
		defer close(events)
		for {
			select {
			case <-ctx.Done():
				events <- providers.StreamEvent{Type: providers.EventError, Error: ctx.Err()}
				return
			case event := <-call.stream:
				events <- event
			case <-call.release:
				events <- providers.StreamEvent{Type: providers.EventContentDelta, Content: "Independent result"}
				events <- providers.StreamEvent{Type: providers.EventDone}
				return
			}
		}
	}()
	return events, nil
}

type collaborationRPCFixture struct {
	server    *Server
	out       *lockedBuffer
	provider  *collaborationRPCProvider
	identity  channels.NamedAgent
	room      channels.Room
	completed chan string
	nextID    int
}

func newCollaborationRPCFixture(t *testing.T) *collaborationRPCFixture {
	t.Helper()
	provider := &collaborationRPCProvider{started: make(chan *collaborationRPCCall, 16)}
	rt := newTestRuntime(t, &fakeClient{})
	rt.WuuHome = filepath.Join(t.TempDir(), "collaboration")
	rt.StreamRunner.Client = provider
	attachNamedAgentTestToolkit(t, rt)
	out := &lockedBuffer{}
	server := NewWithCredentialStore(rt, out, nil, nil)
	t.Cleanup(server.Close)
	if server.startupErr != nil {
		t.Fatal(server.startupErr)
	}
	server.channelService.SetWakeSink(nil)
	completed := make(chan string, 16)
	server.afterNamedAgentWakeCompletionForTest = func(identity string) { completed <- identity }
	credential, err := server.channelService.CreateNamedAgent(context.Background(), channels.CreateNamedAgentParams{Name: "Researcher", Autostart: true})
	if err != nil {
		t.Fatal(err)
	}
	return &collaborationRPCFixture{server: server, out: out, provider: provider, identity: credential.Agent, room: createAppserverTestRoom(t, server.channelService, credential.Agent), completed: completed}
}

func (fixture *collaborationRPCFixture) rpc(t *testing.T, method string, params, target any) {
	t.Helper()
	fixture.nextID++
	id := json.RawMessage(fmt.Sprintf("%d", fixture.nextID))
	encodedParams, err := json.Marshal(params)
	if err != nil {
		t.Fatal(err)
	}
	request, err := json.Marshal(Request{ID: id, Method: method, Params: encodedParams})
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.server.handleLine(context.Background(), request); err != nil {
		t.Fatalf("%s: %v", method, err)
	}
	// Turn notifications may interleave with the RPC response on the same writer.
	for _, line := range strings.Split(strings.TrimSpace(fixture.out.String()), "\n") {
		var envelope struct {
			ID     json.RawMessage `json:"id"`
			Result json.RawMessage `json:"result"`
			Error  *ResponseError  `json:"error"`
		}
		if err := json.Unmarshal([]byte(line), &envelope); err != nil || string(envelope.ID) != string(id) {
			continue
		}
		if envelope.Error != nil {
			t.Fatalf("%s error: %+v", method, envelope.Error)
		}
		if err := json.Unmarshal(envelope.Result, target); err != nil {
			t.Fatalf("decode %s: %v", method, err)
		}
		return
	}
	t.Fatalf("%s did not return response %s", method, id)
}

func (fixture *collaborationRPCFixture) create(t *testing.T, objective string) (channels.CollaborationSessionBinding, *collaborationRPCCall) {
	t.Helper()
	var result ChannelSessionResult
	fixture.rpc(t, MethodChannelSessionCreate, ChannelSessionCreateParams{AgentID: fixture.identity.ID, RoomID: fixture.room.ID, Title: objective, Prompt: objective, RequestID: objective}, &result)
	if result.Session.WorkID != "" || result.Session.RunID != "" || result.Session.NamedAgentID != fixture.identity.ID {
		t.Fatalf("independent session acquired an unrelated work item or identity: %+v", result.Session)
	}
	return result.Session, fixture.nextCall(t)
}

func (fixture *collaborationRPCFixture) nextCall(t *testing.T) *collaborationRPCCall {
	t.Helper()
	select {
	case call := <-fixture.provider.started:
		return call
	case <-time.After(collaborationTestWaitTimeout):
		t.Fatal("session did not reach the provider")
		return nil
	}
}

func (fixture *collaborationRPCFixture) waitForCompletion(t *testing.T) {
	t.Helper()
	select {
	case identity := <-fixture.completed:
		if identity != fixture.identity.ID {
			t.Fatalf("completed identity = %q", identity)
		}
	case <-time.After(collaborationTestWaitTimeout):
		t.Fatal("session completion was not persisted")
	}
}

func collaborationRequestText(request providers.ChatRequest) string {
	var text strings.Builder
	for _, message := range request.Messages {
		text.WriteString(message.Content)
		text.WriteByte('\n')
	}
	return text.String()
}

func TestChannelSessionRPCConcurrentIdentityAndReadOnlySnapshots(t *testing.T) {
	fixture := newCollaborationRPCFixture(t)
	first, firstCall := fixture.create(t, "Explore first independent hypothesis")
	second, secondCall := fixture.create(t, "Explore second independent hypothesis")
	if first.SessionRef == second.SessionRef {
		t.Fatal("two objectives reused the same session")
	}
	if firstCall.ctx.Err() != nil || secondCall.ctx.Err() != nil {
		t.Fatal("starting another session cancelled a sibling execution")
	}
	for _, pair := range []struct {
		binding channels.CollaborationSessionBinding
		call    *collaborationRPCCall
		other   string
	}{{first, firstCall, second.Objective}, {second, secondCall, first.Objective}} {
		text := collaborationRequestText(pair.call.request)
		if !strings.Contains(text, pair.binding.Objective) || strings.Contains(text, pair.other) {
			t.Fatalf("session %q did not receive an isolated objective: %s", pair.binding.SessionRef, text)
		}
		var read ChannelSessionReadResult
		fixture.rpc(t, MethodChannelSessionRead, ChannelSessionRefParams{SessionRef: pair.binding.SessionRef}, &read)
		if !read.Thread.ReadOnly || read.Thread.ID != pair.binding.SessionRef || read.Thread.Status != ThreadStatusInProgress {
			t.Fatalf("running session snapshot: %+v", read.Thread)
		}
	}
	var listed ChannelSessionListResult
	fixture.rpc(t, MethodChannelSessionList, ChannelSessionListParams{AgentID: fixture.identity.ID, RoomID: fixture.room.ID}, &listed)
	if len(listed.Sessions) != 2 {
		t.Fatalf("identity has %d sessions, want 2", len(listed.Sessions))
	}
	for _, binding := range listed.Sessions {
		if binding.State != channels.CollaborationSessionRunning {
			t.Fatalf("concurrent session %q state = %s", binding.SessionRef, binding.State)
		}
	}
	close(firstCall.release)
	fixture.waitForCompletion(t)
	close(secondCall.release)
	fixture.waitForCompletion(t)
}

func TestChannelSessionRPCTargetedFollowupStopAndResumePreserveSibling(t *testing.T) {
	fixture := newCollaborationRPCFixture(t)
	first, firstCall := fixture.create(t, "Investigate transport recovery")
	second, secondCall := fixture.create(t, "Investigate history recovery")
	const followup = "Only transport should inspect the stale socket callback"
	var result ChannelSessionResult
	fixture.rpc(t, MethodChannelSessionSend, ChannelSessionRefParams{SessionRef: first.SessionRef, Prompt: followup, RequestID: "targeted-followup"}, &result)
	close(firstCall.release)
	continued := fixture.nextCall(t)
	fixture.waitForCompletion(t)
	if !strings.Contains(collaborationRequestText(continued.request), followup) {
		t.Fatal("targeted follow-up did not reach its session")
	}
	var sibling ChannelSessionReadResult
	fixture.rpc(t, MethodChannelSessionRead, ChannelSessionRefParams{SessionRef: second.SessionRef}, &sibling)
	for _, turn := range sibling.Thread.Turns {
		for _, item := range turn.Items {
			if strings.Contains(item.Text, followup) {
				t.Fatal("targeted follow-up leaked into the sibling history")
			}
		}
	}
	fixture.rpc(t, MethodChannelSessionStop, ChannelSessionRefParams{SessionRef: first.SessionRef}, &result)
	select {
	case <-continued.ctx.Done():
	case <-time.After(collaborationTestWaitTimeout):
		t.Fatal("stop did not cancel the target provider call")
	}
	fixture.waitForCompletion(t)
	if result.Session.State != channels.CollaborationSessionCancelled {
		t.Fatalf("stopped state = %s", result.Session.State)
	}
	if secondCall.ctx.Err() != nil {
		t.Fatalf("stopping a session cancelled its sibling: %v", secondCall.ctx.Err())
	}
	fixture.rpc(t, MethodChannelSessionRead, ChannelSessionRefParams{SessionRef: second.SessionRef}, &sibling)
	if sibling.Thread.Status != ThreadStatusInProgress || sibling.Session.State != channels.CollaborationSessionRunning {
		t.Fatalf("sibling stopped after unrelated cancellation: %+v", sibling.Session)
	}
	fixture.rpc(t, MethodChannelSessionResume, ChannelSessionRefParams{SessionRef: first.SessionRef}, &result)
	resumed := fixture.nextCall(t)
	if result.Session.SessionRef != first.SessionRef || !strings.Contains(collaborationRequestText(resumed.request), followup) {
		t.Fatal("resume replaced the session or lost its previous context")
	}
	close(resumed.release)
	fixture.waitForCompletion(t)
	close(secondCall.release)
	fixture.waitForCompletion(t)
}

func TestChannelSessionRPCDrainsDurableQueueAfterCapacityRelease(t *testing.T) {
	t.Setenv("WUU_COLLAB_GLOBAL_RUN_LIMIT", "1")
	fixture := newCollaborationRPCFixture(t)
	_, firstCall := fixture.create(t, "First experiment")
	var second ChannelSessionResult
	fixture.rpc(t, MethodChannelSessionCreate, ChannelSessionCreateParams{AgentID: fixture.identity.ID, RoomID: fixture.room.ID, Prompt: "Second experiment", RequestID: "queued-experiment"}, &second)
	if second.Session.State != channels.CollaborationSessionQueued {
		t.Fatalf("capacity did not queue second session: %+v", second.Session)
	}
	select {
	case unexpected := <-fixture.provider.started:
		t.Fatalf("queued session executed early: %s", collaborationRequestText(unexpected.request))
	default:
	}
	close(firstCall.release)
	fixture.waitForCompletion(t)
	next := fixture.nextCall(t)
	if !strings.Contains(collaborationRequestText(next.request), "Second experiment") {
		t.Fatal("released capacity did not run the queued objective")
	}
	close(next.release)
	fixture.waitForCompletion(t)
	binding, err := fixture.server.channelService.LookupCollaborationSession(context.Background(), second.Session.SessionRef)
	if err != nil || binding.State != channels.CollaborationSessionCompleted {
		t.Fatalf("queued session did not complete: %+v, %v", binding, err)
	}
}

func TestChannelSessionReadRefreshesIdleCacheAfterAnotherServerCompletes(t *testing.T) {
	fixture := newCollaborationRPCFixture(t)
	binding, call := fixture.create(t, "Inspect shared execution")
	// A reader on another workspace can cache the prompt while the execution
	// owner is still streaming. It must not keep serving that prompt forever.
	reader := &Server{rt: fixture.server.rt, channelService: fixture.server.channelService, threads: make(map[string]*threadState), out: &lockedBuffer{}}
	cached, err := reader.ensureThreadLoaded(binding.SessionRef)
	if err != nil {
		t.Fatal(err)
	}
	if threadIsRunning(cached) {
		t.Fatal("reader acquired the owner's execution")
	}
	close(call.release)
	fixture.waitForCompletion(t)
	readerFixture := &collaborationRPCFixture{server: reader, out: reader.out.(*lockedBuffer)}
	var read ChannelSessionReadResult
	readerFixture.rpc(t, MethodChannelSessionRead, ChannelSessionRefParams{SessionRef: binding.SessionRef}, &read)
	found := false
	for _, turn := range read.Thread.Turns {
		for _, item := range turn.Items {
			if item.Type == ThreadItemAgentMessage && strings.Contains(item.Text, "Independent result") {
				found = true
			}
		}
	}
	if !found || !read.Thread.ReadOnly || read.Session.State != channels.CollaborationSessionCompleted {
		t.Fatalf("reader returned a stale session: %+v", read)
	}
}

type collaborationNotificationWriter struct {
	buffer  *lockedBuffer
	methods chan string
}

func (w *collaborationNotificationWriter) Write(p []byte) (int, error) {
	n, err := w.buffer.Write(p)
	var envelope struct {
		Method string `json:"method"`
	}
	if json.Unmarshal(p, &envelope) == nil && envelope.Method != "" {
		w.methods <- envelope.Method
	}
	return n, err
}

func TestChannelSessionStreamsExecutionEventsBeforeCompletion(t *testing.T) {
	fixture := newCollaborationRPCFixture(t)
	methods := make(chan string, 128)
	fixture.server.out = &collaborationNotificationWriter{buffer: fixture.out, methods: methods}
	binding, call := fixture.create(t, "Inspect streaming progress")
	waitForMethod := func(expected string) {
		t.Helper()
		deadline := time.NewTimer(collaborationTestWaitTimeout)
		defer deadline.Stop()
		for {
			select {
			case method := <-methods:
				if method == expected {
					return
				}
			case <-deadline.C:
				t.Fatalf("session never emitted %s", expected)
			}
		}
	}
	waitForMethod(NotificationTurnStarted)
	call.stream <- providers.StreamEvent{Type: providers.EventThinkingDelta, Content: "Inspecting callers"}
	waitForMethod(NotificationReasoningDelta)
	call.stream <- providers.StreamEvent{Type: providers.EventContentDelta, Content: "Live progress. "}
	waitForMethod(NotificationAgentMessageDelta)
	var read ChannelSessionReadResult
	fixture.rpc(t, MethodChannelSessionRead, ChannelSessionRefParams{SessionRef: binding.SessionRef}, &read)
	turn := read.Thread.Turns[len(read.Thread.Turns)-1]
	if turn.Status != TurnStatusInProgress {
		t.Fatalf("stream was only visible after completion: %+v", turn)
	}
	found := false
	for _, item := range turn.Items {
		if item.Text == "Live progress. " {
			found = true
		}
	}
	if !found {
		t.Fatalf("live snapshot lost streamed text: %+v", turn)
	}
	close(call.release)
	fixture.waitForCompletion(t)
	waitForMethod(NotificationTurnCompleted)
}
