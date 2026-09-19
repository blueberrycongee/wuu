package peers

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"

	pluginapi "github.com/blueberrycongee/wuu/packages/plugin-go"
)

type messagingHost struct {
	mu           sync.Mutex
	stored       *string
	sends        []pluginapi.SessionSendParams
	turns        map[string]pluginapi.SessionTurnInspection
	workspaceID  string
	sessions     []pluginapi.SessionSummary
	steerReplies bool
}

func (h *messagingHost) InitializeParams() pluginapi.InitializeParams {
	return pluginapi.InitializeParams{WorkspaceID: h.workspaceID}
}
func (h *messagingHost) CallHost(ctx context.Context, method string, params, out any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	var result any
	switch method {
	case pluginapi.HostServiceSessionList:
		result = pluginapi.SessionListResult{Sessions: []pluginapi.SessionSummary{
			{SessionID: "source", Name: "Source"}, {SessionID: "target", Name: "Target"},
		}}
		if h.sessions != nil {
			result = pluginapi.SessionListResult{Sessions: h.sessions}
		}
	case pluginapi.HostServiceSessionInspect:
		p := params.(pluginapi.SessionInspectParams)
		inspected := pluginapi.SessionInspectResult{Session: pluginapi.SessionSummary{SessionID: p.SessionID, WorkspaceID: h.workspaceID}}
		if turn, ok := h.turns[p.RequestID]; ok {
			inspected.Turn = &turn
		}
		result = inspected
	case pluginapi.HostServiceStorageGet:
		result = pluginapi.StorageGetResult{Value: h.stored}
	case pluginapi.HostServiceStorageCompareExchange:
		p := params.(pluginapi.StorageCompareExchangeParams)
		swapped := (p.Expected == nil && h.stored == nil) || (p.Expected != nil && h.stored != nil && *p.Expected == *h.stored)
		if swapped {
			h.stored = p.Value
		}
		result = pluginapi.StorageCompareExchangeResult{Swapped: swapped}
	case pluginapi.HostServiceSessionSend:
		p := params.(pluginapi.SessionSendParams)
		if h.turns == nil {
			h.turns = make(map[string]pluginapi.SessionTurnInspection)
		}
		turn, exists := h.turns[p.RequestID]
		if !exists || turn.State == "discarded" && turn.Retryable {
			h.sends = append(h.sends, p)
			turn = pluginapi.SessionTurnInspection{RequestID: p.RequestID, State: "queued", QueueID: fmt.Sprintf("queued-%d", len(h.sends))}
			if h.steerReplies && strings.HasPrefix(p.RequestID, responsePrefix) {
				turn.State, turn.TurnID, turn.QueueID = "running", "active-turn", ""
			}
			h.turns[p.RequestID] = turn
		}
		result = pluginapi.SessionSendResult{
			SessionID: p.SessionID, State: turn.State, QueueID: turn.QueueID, TurnID: turn.TurnID,
			Steered: h.steerReplies && strings.HasPrefix(p.RequestID, responsePrefix) && turn.State == "running",
		}
	default:
		return fmt.Errorf("unexpected host method %s", method)
	}
	raw, err := json.Marshal(result)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, out)
}

func TestMessagingRoundTripPreservesBodyAndSourceWithoutReplyLoop(t *testing.T) {
	h := &messagingHost{}
	ctx := context.Background()
	body := "Please coordinate the overlapping work.\n" + strings.Repeat("保留消息正文。", 200)
	args, _ := json.Marshal(map[string]string{"target_session_id": "target", "message": body})
	call := pluginapi.ToolCall{ToolID: "send_message", SessionID: "source", Arguments: args}
	if _, err := executeTool(ctx, h, call); err != nil {
		t.Fatal(err)
	}
	if len(h.sends) != 1 {
		t.Fatalf("deliveries = %d", len(h.sends))
	}
	request := h.sends[0]
	if request.SessionID != "target" || request.Presentation.Text != body || request.Presentation.RelatedSessionID != "source" || request.Presentation.Kind != "session_message" || request.IfRunning != "queue" {
		t.Fatalf("request = %+v", request)
	}
	if !strings.Contains(request.Input.Prompt, body) || request.Input.Prompt == body {
		t.Fatal("model input must retain both message and plugin-owned context")
	}
	// A retried tool call must not enqueue the same request twice.
	if _, err := executeTool(ctx, h, call); err != nil {
		t.Fatal(err)
	}
	if len(h.sends) != 1 {
		t.Fatal("duplicate request delivered")
	}
	reply := "Changes are ready to integrate."
	input, _ := json.Marshal(pluginapi.TurnLifecycleInput{RequestID: request.RequestID, State: "completed", FinalOutput: reply})
	for range 2 {
		if _, err := invokeCapability(ctx, h, pluginapi.CapabilityCall{Capability: capabilityLifecycle, Input: input}); err != nil {
			t.Fatal(err)
		}
	}
	if len(h.sends) != 2 {
		t.Fatalf("reply must be delivered once: %d sends", len(h.sends))
	}
	response := h.sends[1]
	if response.IfRunning != pluginapi.SessionIfRunningSteer {
		t.Fatalf("receipt must join active work without requiring another response: %+v", response)
	}
	if response.SessionID != "source" || response.Presentation.Text != reply || response.Presentation.RelatedSessionID != "target" || response.Presentation.Kind != "session_message" {
		t.Fatalf("reply = %+v", response)
	}
	input, _ = json.Marshal(pluginapi.TurnLifecycleInput{RequestID: response.RequestID, State: "completed", FinalOutput: "Acknowledged"})
	if _, err := invokeCapability(ctx, h, pluginapi.CapabilityCall{Capability: capabilityLifecycle, Input: input}); err != nil {
		t.Fatal(err)
	}
	if len(h.sends) != 2 {
		t.Fatal("automatic reply loop")
	}
}

func TestMessagingRejectsSelfUnknownAndRefusedTargets(t *testing.T) {
	h := &messagingHost{}
	ctx := context.Background()
	for _, target := range []string{"source", "missing"} {
		args, _ := json.Marshal(map[string]string{"target_session_id": target, "message": "hello"})
		if _, err := executeTool(ctx, h, pluginapi.ToolCall{ToolID: "send_message", SessionID: "source", Arguments: args}); err == nil {
			t.Fatalf("accepted target %q", target)
		}
	}
	if _, err := executeTool(ctx, h, pluginapi.ToolCall{ToolID: "peer_policy", SessionID: "target", Arguments: json.RawMessage(`{"inbound":"refuse"}`)}); err != nil {
		t.Fatal(err)
	}
	if _, err := executeTool(ctx, h, pluginapi.ToolCall{ToolID: "send_message", SessionID: "source", Arguments: json.RawMessage(`{"target_session_id":"target","message":"hello"}`)}); err != nil {
		t.Fatal(err)
	}
	if len(h.sends) != 0 {
		t.Fatal("refused or invalid request was delivered")
	}
}
