package appserver

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/channels"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/runtime"
)

type collaborationFlowCall struct {
	request  providers.ChatRequest
	response chan providers.ChatResponse
}

type collaborationFlowProvider struct{ calls chan *collaborationFlowCall }

func (provider *collaborationFlowProvider) Chat(ctx context.Context, request providers.ChatRequest) (providers.ChatResponse, error) {
	call := &collaborationFlowCall{request: request, response: make(chan providers.ChatResponse, 1)}
	select {
	case provider.calls <- call:
	case <-ctx.Done():
		return providers.ChatResponse{}, ctx.Err()
	}
	select {
	case response := <-call.response:
		return response, nil
	case <-ctx.Done():
		return providers.ChatResponse{}, ctx.Err()
	}
}

func (provider *collaborationFlowProvider) next(t *testing.T) *collaborationFlowCall {
	t.Helper()
	select {
	case call := <-provider.calls:
		return call
	case <-time.After(5 * time.Second):
		t.Fatal("collaboration did not reach the next model call")
		return nil
	}
}

func newCollaborationFlowFixture(t *testing.T) (*collaborationRPCFixture, *collaborationFlowProvider) {
	t.Helper()
	fixture := newCollaborationRPCFixture(t)
	provider := &collaborationFlowProvider{calls: make(chan *collaborationFlowCall, 16)}
	fixture.server.rt.StreamRunner.Client = providers.AdaptStreamClient(provider)
	fixture.server.channelService.SetWakeSink(fixture.server)
	return fixture, provider
}

// startCollaborationFlow asks the real model/tool loop to create a same-identity
// child. Both calls stay under test control until their durable state is checked.
func startCollaborationFlow(t *testing.T, fixture *collaborationRPCFixture, provider *collaborationFlowProvider) (channels.CollaborationSessionBinding, channels.CollaborationSessionBinding, *collaborationFlowCall) {
	t.Helper()
	ctx := context.Background()
	const objective = "Identify the reconnect defect using an independent experiment"
	var result ChannelSessionResult
	fixture.rpc(t, MethodChannelSessionCreate, ChannelSessionCreateParams{AgentID: fixture.identity.ID, RoomID: fixture.room.ID, Prompt: objective, Title: "Reconnect investigation", RequestID: "flow-parent"}, &result)
	parent := result.Session
	parentCall := provider.next(t)
	if !strings.Contains(collaborationRequestText(parentCall.request), objective) {
		t.Fatal("the user objective did not reach the parent model")
	}
	args, err := json.Marshal(map[string]string{"action": "create", "room_id": fixture.room.ID, "prompt": "Run an independent reconnect experiment and report concrete evidence", "title": "Independent experiment", "request_id": "model-created-child"})
	if err != nil {
		t.Fatal(err)
	}
	parentCall.response <- providers.ChatResponse{ToolCalls: []providers.ToolCall{{ID: "create-child", Name: "chat_session", Arguments: string(args)}}}
	var continuation, childCall *collaborationFlowCall
	for range 2 {
		call := provider.next(t)
		isContinuation := false
		for _, message := range call.request.Messages {
			if message.Role == "tool" && message.ToolCallID == "create-child" {
				isContinuation = true
				var result struct {
					Error string `json:"error"`
				}
				if json.Unmarshal([]byte(message.Content), &result) == nil && result.Error != "" {
					t.Fatalf("model chat_session create failed: %s", result.Error)
				}
			}
		}
		if isContinuation {
			continuation = call
		} else {
			childCall = call
		}
	}
	if continuation == nil || childCall == nil {
		t.Fatal("the model's create call did not execute a separate child and return a tool result")
	}
	bindings, err := fixture.server.channelService.ListAllCollaborationSessions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var child channels.CollaborationSessionBinding
	for _, binding := range bindings {
		if binding.ParentSessionRef == parent.SessionRef {
			child = binding
		}
	}
	if child.SessionRef == "" || child.PrincipalID != parent.PrincipalID || child.SessionRef == parent.SessionRef || child.WorkID != "" || child.RunID != "" {
		t.Fatalf("dynamic child acquired the wrong identity or mandatory Work pipeline: %+v", child)
	}
	for _, binding := range []channels.CollaborationSessionBinding{parent, child} {
		thread := fixture.server.thread(binding.SessionRef)
		thread.mu.Lock()
		profile := thread.execRuntime.ExecutionProfile
		thread.mu.Unlock()
		if profile != runtime.CollaborationRuntimeVersion {
			t.Fatalf("session %s uses execution profile %q", binding.SessionRef, profile)
		}
	}
	continuation.response <- providers.ChatResponse{Content: "The independent experiment is running. I will use its evidence when it returns."}
	fixture.waitForCompletion(t)
	parent, err = fixture.server.channelService.LookupCollaborationSession(ctx, parent.SessionRef)
	if err != nil || parent.State != channels.CollaborationSessionWaiting {
		t.Fatalf("parent did not release its turn while the child runs: %+v, %v", parent, err)
	}
	waitForThreadLeaseRelease(t, fixture.server.rt.SessionDir, parent.SessionRef)
	child, err = fixture.server.channelService.LookupCollaborationSession(ctx, child.SessionRef)
	if err != nil || child.State != channels.CollaborationSessionRunning {
		t.Fatalf("waiting for a child interrupted it: %+v, %v", child, err)
	}
	return parent, child, childCall
}

func TestCollaborationModelCreatesParallelSessionAndContinuesFromItsEvidence(t *testing.T) {
	fixture, provider := newCollaborationFlowFixture(t)
	parent, child, childCall := startCollaborationFlow(t, fixture, provider)
	const evidence = "EXPERIMENT EVIDENCE: the retry callback retains the old socket reference"
	childCall.response <- providers.ChatResponse{Content: evidence}
	fixture.waitForCompletion(t)
	continuation := provider.next(t)
	prompt := collaborationRequestText(continuation.request)
	if !strings.Contains(prompt, evidence) || !strings.Contains(prompt, child.SessionRef) {
		t.Fatalf("parent did not receive durable child evidence with source provenance: %s", prompt)
	}
	continuation.response <- providers.ChatResponse{Content: "The independent experiment located the stale socket capture. The investigation is complete."}
	fixture.waitForCompletion(t)
	for _, ref := range []string{parent.SessionRef, child.SessionRef} {
		binding, err := fixture.server.channelService.LookupCollaborationSession(context.Background(), ref)
		if err != nil || binding.State != channels.CollaborationSessionCompleted || binding.WorkID != "" {
			t.Fatalf("final session state = %+v, %v", binding, err)
		}
	}
	pending, err := fixture.server.channelService.PendingCollaborationDispatches(context.Background(), fixture.identity.ID)
	if err != nil || len(pending) != 0 {
		t.Fatalf("completed flow left unacknowledged deliveries: %+v, %v", pending, err)
	}
}

func TestCollaborationRestartDeliversCompletedChildWithoutRerunningIt(t *testing.T) {
	fixture, provider := newCollaborationFlowFixture(t)
	parent, child, childCall := startCollaborationFlow(t, fixture, provider)
	const evidence = "PERSISTED EVIDENCE: only the stale socket callback reproduces the failure"
	// Hold the dispatch/settlement lock until the actual turn has durably ended.
	// Closing at this exact boundary models a host exit before channels settlement.
	var before channels.CollaborationSessionBinding
	func() {
		fixture.server.namedAgentMu.Lock()
		defer fixture.server.namedAgentMu.Unlock()
		childCall.response <- providers.ChatResponse{Content: evidence}
		waitForThreadLeaseRelease(t, fixture.server.rt.SessionDir, child.SessionRef)
		var err error
		before, err = fixture.server.channelService.LookupCollaborationSession(context.Background(), child.SessionRef)
		if err != nil || before.State != channels.CollaborationSessionRunning {
			t.Fatalf("test did not stop at the pre-settlement boundary: %+v, %v", before, err)
		}
		fixture.server.closed.Store(true)
	}()
	rt := fixture.server.rt
	fixture.server.Close()
	restarted := NewWithCredentialStore(rt, &lockedBuffer{}, nil, nil)
	t.Cleanup(restarted.Close)
	if restarted.startupErr != nil {
		t.Fatal(restarted.startupErr)
	}
	completed := make(chan string, 16)
	restarted.afterNamedAgentWakeCompletionForTest = func(identity string) { completed <- identity }
	continuation := provider.next(t)
	prompt := collaborationRequestText(continuation.request)
	if !strings.Contains(prompt, evidence) || !strings.Contains(prompt, child.SessionRef) {
		t.Fatalf("restart reran work or failed to deliver its persisted terminal result: %s", prompt)
	}
	recovered, err := restarted.channelService.LookupCollaborationSession(context.Background(), child.SessionRef)
	if err != nil || recovered.State != channels.CollaborationSessionCompleted || recovered.TurnID != before.TurnID {
		t.Fatalf("recovered child = %+v, %v", recovered, err)
	}
	continuation.response <- providers.ChatResponse{Content: "Recovered evidence confirms the stale callback; the investigation is complete."}
	select {
	case <-completed:
	case <-time.After(5 * time.Second):
		t.Fatal("recovered parent did not complete")
	}
	final, err := restarted.channelService.LookupCollaborationSession(context.Background(), parent.SessionRef)
	if err != nil || final.State != channels.CollaborationSessionCompleted {
		t.Fatalf("recovered parent = %+v, %v", final, err)
	}
	select {
	case extra := <-provider.calls:
		t.Fatalf("recovery launched duplicate model work: %s", collaborationRequestText(extra.request))
	default:
	}
}
