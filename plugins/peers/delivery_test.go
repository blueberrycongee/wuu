package peers

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	pluginapi "github.com/blueberrycongee/wuu/packages/plugin-go"
)

type uncertainSendHost struct {
	*messagingHost
	failOnce bool
	accept   bool
}

type blockedReplyHost struct {
	*messagingHost
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (h *blockedReplyHost) CallHost(ctx context.Context, method string, params, out any) error {
	if method == pluginapi.HostServiceSessionSend && strings.HasPrefix(params.(pluginapi.SessionSendParams).RequestID, responsePrefix) {
		h.once.Do(func() { close(h.entered) })
		select {
		case <-h.release:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return h.messagingHost.CallHost(ctx, method, params, out)
}

func (h *uncertainSendHost) CallHost(ctx context.Context, method string, params, out any) error {
	if method == pluginapi.HostServiceSessionSend && h.failOnce {
		h.failOnce = false
		if h.accept {
			if err := h.messagingHost.CallHost(ctx, method, params, out); err != nil {
				return err
			}
		}
		return context.Canceled
	}
	return h.messagingHost.CallHost(ctx, method, params, out)
}

func peerSendCall() pluginapi.ToolCall {
	return pluginapi.ToolCall{ToolID: "send_message", SessionID: "source", CallID: "call-1", Arguments: json.RawMessage(`{"target_session_id":"target","message":"Review this"}`)}
}

func requestState(t *testing.T, host pluginapi.Host) requestRecord {
	t.Helper()
	state, err := readState(context.Background(), host)
	if err != nil || len(state.Requests) != 1 {
		t.Fatalf("request state: %+v, %v", state, err)
	}
	for _, record := range state.Requests {
		return record
	}
	panic("unreachable")
}

func observeLifecycle(t *testing.T, host *messagingHost, input pluginapi.TurnLifecycleInput) {
	t.Helper()
	host.mu.Lock()
	if host.turns == nil {
		host.turns = make(map[string]pluginapi.SessionTurnInspection)
	}
	host.turns[input.RequestID] = pluginapi.SessionTurnInspection{
		RequestID: input.RequestID, State: input.State, TurnID: input.TurnID,
		FinalOutput: input.FinalOutput, Error: input.Error, Retryable: input.Retryable,
	}
	host.mu.Unlock()
	data, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := invokeCapability(context.Background(), host, pluginapi.CapabilityCall{Capability: capabilityLifecycle, Input: data}); err != nil {
		t.Fatal(err)
	}
}

func TestUncertainSendRetainsCorrelationAndRetriesSameIdentity(t *testing.T) {
	for _, accepted := range []bool{false, true} {
		name := "before_acceptance"
		if accepted {
			name = "after_acceptance"
		}
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			h := &uncertainSendHost{messagingHost: &messagingHost{}, failOnce: true, accept: accepted}
			call := peerSendCall()
			if _, err := executeTool(ctx, h, call); !errors.Is(err, context.Canceled) {
				t.Fatalf("send: %v", err)
			}
			before := requestState(t, h)
			// Neither the short dedup window nor a new handler instance may
			// discard the identity of an uncertain send.
			if _, err := updateState(ctx, h, func(state *persistedState) error { state.Recent = nil; return nil }); err != nil {
				t.Fatal(err)
			}
			if _, err := Handler().ExecuteTool(ctx, h, call); err != nil {
				t.Fatal(err)
			}
			if len(h.sends) != 1 || h.sends[0].RequestID != requestPrefix+before.ID {
				t.Fatalf("retry duplicated the request: %+v", h.sends)
			}
			observeLifecycle(t, h.messagingHost, pluginapi.TurnLifecycleInput{RequestID: h.sends[0].RequestID, State: "completed", FinalOutput: "Review result"})
			if len(h.sends) != 2 || h.sends[1].Presentation.Text != "Review result" {
				t.Fatalf("terminal result lost: %+v", h.sends)
			}
		})
	}
}

func TestCancelledSendStillReturnsAcceptedTargetsResult(t *testing.T) {
	h := &uncertainSendHost{messagingHost: &messagingHost{}, failOnce: true, accept: true}
	_, _ = executeTool(context.Background(), h, peerSendCall())
	observeLifecycle(t, h.messagingHost, pluginapi.TurnLifecycleInput{RequestID: h.sends[0].RequestID, State: "completed", FinalOutput: "Result after cancellation"})
	if len(h.sends) != 2 || h.sends[1].Presentation.Text != "Result after cancellation" {
		t.Fatalf("result lost without an explicit retry: %+v", h.sends)
	}
}

func TestQueuedReplySurvivesShutdownButHonorsUserCancellation(t *testing.T) {
	for _, retryable := range []bool{false, true} {
		name := "user_cancelled"
		if retryable {
			name = "host_shutdown"
		}
		t.Run(name, func(t *testing.T) {
			h := &messagingHost{}
			ctx := context.Background()
			if _, err := executeTool(ctx, h, peerSendCall()); err != nil {
				t.Fatal(err)
			}
			observeLifecycle(t, h, pluginapi.TurnLifecycleInput{RequestID: h.sends[0].RequestID, State: "completed", FinalOutput: "Important result"})
			if requestState(t, h).Replied {
				t.Fatal("queue acceptance was treated as delivery")
			}
			reply := h.sends[1]
			observeLifecycle(t, h, pluginapi.TurnLifecycleInput{RequestID: reply.RequestID, State: "discarded", Retryable: retryable})
			// A fresh controller models restart; the original lifecycle outbox
			// event has already been acknowledged, but its inspection survives.
			(&controller{host: h}).reconcileRequests(ctx)
			if !retryable {
				if len(h.sends) != 2 || !requestState(t, h).Replied {
					t.Fatalf("user cancellation was retried: %+v", h.sends)
				}
				return
			}
			if len(h.sends) != 3 || h.sends[2].RequestID != reply.RequestID || h.sends[2].Presentation.Text != reply.Presentation.Text {
				t.Fatalf("reply was not recovered with the original identity/body: %+v", h.sends)
			}
			(&controller{host: h}).reconcileRequests(ctx)
			if len(h.sends) != 3 {
				t.Fatal("maintenance duplicated a live queued reply")
			}
			observeLifecycle(t, h, pluginapi.TurnLifecycleInput{RequestID: reply.RequestID, State: "running", TurnID: "reply-turn"})
			if !requestState(t, h).Replied {
				t.Fatal("durable receipt did not settle reply")
			}
			observeLifecycle(t, h, pluginapi.TurnLifecycleInput{RequestID: reply.RequestID, State: "completed", TurnID: "reply-turn", FinalOutput: "Acknowledged"})
			if len(h.sends) != 3 {
				t.Fatal("reply started an automatic response loop")
			}
		})
	}
}

func TestRecoveryUsesCompletedResultInsteadOfRequestTimeout(t *testing.T) {
	ctx := context.Background()
	h := &messagingHost{}
	if _, err := executeTool(ctx, h, peerSendCall()); err != nil {
		t.Fatal(err)
	}
	requestID := h.sends[0].RequestID
	// A terminal receipt exists, but its notification and the process were
	// lost before the plugin could send the reply.
	h.turns[requestID] = pluginapi.SessionTurnInspection{RequestID: requestID, State: "completed", FinalOutput: "The real successful result"}
	if _, err := updateState(ctx, h, func(state *persistedState) error {
		for id, record := range state.Requests {
			record.CreatedAt = time.Now().Add(-25 * time.Hour)
			state.Requests[id] = record
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	(&controller{host: h}).reconcileRequests(ctx)
	if len(h.sends) != 2 || h.sends[1].Presentation.Text != "The real successful result" {
		t.Fatalf("completed output was replaced by timeout: %+v", h.sends)
	}
	if requestState(t, h).State != "completed" {
		t.Fatal("terminal state regressed")
	}
	// Delayed progress must not regress the terminal record either.
	observeLifecycle(t, h, pluginapi.TurnLifecycleInput{RequestID: requestID, State: "running"})
	if requestState(t, h).State != "completed" {
		t.Fatal("delayed progress overwrote completion")
	}
}

func TestReadOnlyDiscoveryDoesNotRunDeliveryMaintenance(t *testing.T) {
	h := &messagingHost{}
	ctx := context.Background()
	if _, err := executeTool(ctx, h, peerSendCall()); err != nil {
		t.Fatal(err)
	}
	requestID := h.sends[0].RequestID
	h.turns[requestID] = pluginapi.SessionTurnInspection{RequestID: requestID, State: "completed", FinalOutput: strings.Repeat("result", 10)}
	handler := Handler()
	if err := handler.Initialize(ctx, h, pluginapi.InitializeParams{}); err != nil {
		t.Fatal(err)
	}
	if _, err := handler.ExecuteTool(ctx, h, pluginapi.ToolCall{ToolID: "list_peers", SessionID: "source"}); err != nil {
		t.Fatal(err)
	}
	if len(h.sends) != 1 {
		t.Fatal("read-only discovery delivered a message")
	}
}

func TestPeerDiscoveryAndSendStayWithinHostWorkspace(t *testing.T) {
	h := &messagingHost{workspaceID: "project-a", sessions: []pluginapi.SessionSummary{
		{SessionID: "source", WorkspaceID: "project-a"},
		{SessionID: "target", WorkspaceID: "project-a"},
		{SessionID: "local-fork", WorkspaceID: "project-a", ParentSessionID: "target"},
		{SessionID: "foreign", WorkspaceID: "project-b"},
	}}
	ctx := context.Background()
	result, err := listPeers(ctx, h, "source")
	if err != nil {
		t.Fatal(err)
	}
	var listed struct {
		Peers []pluginapi.SessionSummary `json:"peers"`
	}
	if err := json.Unmarshal([]byte(result.Content[0].Text), &listed); err != nil {
		t.Fatal(err)
	}
	ids := map[string]bool{}
	for _, peer := range listed.Peers {
		ids[peer.SessionID] = true
	}
	if len(ids) != 2 || !ids["target"] || !ids["local-fork"] {
		t.Fatalf("discovery escaped workspace or lost a local fork: %+v", listed.Peers)
	}
	call := peerSendCall()
	call.Arguments = json.RawMessage(`{"target_session_id":"foreign","message":"Review this"}`)
	if _, err := executeTool(ctx, h, call); err == nil || len(h.sends) != 0 {
		t.Fatalf("foreign target was accepted: %v, %+v", err, h.sends)
	}
	if _, err := executeTool(ctx, h, peerSendCall()); err != nil || len(h.sends) != 1 {
		t.Fatalf("local target rejected: %v, %+v", err, h.sends)
	}
}

func TestFailedReplyRecoveryPreservesCompletedOutputPastTimeout(t *testing.T) {
	ctx := context.Background()
	base := &messagingHost{}
	if _, err := executeTool(ctx, base, peerSendCall()); err != nil {
		t.Fatal(err)
	}
	input := pluginapi.TurnLifecycleInput{RequestID: base.sends[0].RequestID, State: "completed", FinalOutput: "Successful output"}
	base.turns[input.RequestID] = pluginapi.SessionTurnInspection{RequestID: input.RequestID, State: input.State, FinalOutput: input.FinalOutput}
	h := &uncertainSendHost{messagingHost: base, failOnce: true}
	raw, _ := json.Marshal(input)
	if _, err := invokeCapability(ctx, h, pluginapi.CapabilityCall{Capability: capabilityLifecycle, Input: raw}); err == nil {
		t.Fatal("expected reply failure")
	}
	if _, err := updateState(ctx, h, func(state *persistedState) error {
		for id, record := range state.Requests {
			record.CreatedAt = time.Now().Add(-25 * time.Hour)
			state.Requests[id] = record
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	(&controller{host: h}).reconcileRequests(ctx)
	if len(h.sends) != 2 || h.sends[1].Presentation.Text != input.FinalOutput || requestState(t, h).State != "completed" {
		t.Fatalf("failed reply was replaced by timeout: %+v", h.sends)
	}
}

func TestExplicitRetryCallRetainsItsAliasAfterUncertainSend(t *testing.T) {
	ctx := context.Background()
	h := &uncertainSendHost{messagingHost: &messagingHost{}, failOnce: true, accept: true}
	_, _ = executeTool(ctx, h, peerSendCall())
	call := peerSendCall()
	call.CallID = "explicit-retry"
	if _, err := executeTool(ctx, h, call); err != nil {
		t.Fatal(err)
	}
	observeLifecycle(t, h.messagingHost, pluginapi.TurnLifecycleInput{RequestID: h.sends[0].RequestID, State: "completed", FinalOutput: "result"})
	observeLifecycle(t, h.messagingHost, pluginapi.TurnLifecycleInput{RequestID: h.sends[1].RequestID, State: "running", TurnID: "reply-turn"})
	if _, err := updateState(ctx, h, func(state *persistedState) error { state.Recent = nil; return nil }); err != nil {
		t.Fatal(err)
	}
	for _, retry := range []pluginapi.ToolCall{call, peerSendCall()} {
		if _, err := executeTool(ctx, h, retry); err != nil {
			t.Fatal(err)
		}
	}
	if len(h.sends) != 2 {
		t.Fatalf("tool retry lost its original identity: %+v", h.sends)
	}
}

func TestConcurrentTerminalReplayCannotDeliverTwoReplies(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	h := &blockedReplyHost{messagingHost: &messagingHost{}, entered: make(chan struct{}), release: make(chan struct{})}
	if _, err := executeTool(ctx, h, peerSendCall()); err != nil {
		t.Fatal(err)
	}
	id := requestState(t, h).ID
	done := make(chan error, 1)
	go func() { done <- replyToTerminal(ctx, h, id, "completed", "result") }()
	select {
	case <-h.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("reply send did not start")
	}
	if err := replyToTerminal(ctx, h, id, "completed", "result"); err == nil {
		t.Fatal("concurrent delivery did not retain its pending event")
	}
	close(h.release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := replyToTerminal(ctx, h, id, "completed", "result"); err != nil {
		t.Fatal(err)
	}
	if len(h.sends) != 2 {
		t.Fatalf("concurrent replay duplicated reply: %+v", h.sends)
	}
}

func TestQueuedReplyRecoveryAfterAbruptHostLoss(t *testing.T) {
	ctx := context.Background()
	h := &messagingHost{}
	if _, err := executeTool(ctx, h, peerSendCall()); err != nil {
		t.Fatal(err)
	}
	observeLifecycle(t, h, pluginapi.TurnLifecycleInput{RequestID: h.sends[0].RequestID, State: "completed", FinalOutput: "Durable result"})
	reply := h.sends[1]
	// No discarded notification is available after abrupt process loss.
	delete(h.turns, reply.RequestID)
	(&controller{host: h}).reconcileRequests(ctx)
	if len(h.sends) != 3 || h.sends[2].RequestID != reply.RequestID || h.sends[2].Presentation.Text != reply.Presentation.Text {
		t.Fatalf("lost in-memory queue was not recovered: %+v", h.sends)
	}
}
