package peers

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	pluginapi "github.com/blueberrycongee/wuu/packages/plugin-go"
)

type messagingHost struct {
	stored *string
	sends  []pluginapi.SessionSendParams
}

func (h *messagingHost) InitializeParams() pluginapi.InitializeParams {
	return pluginapi.InitializeParams{}
}
func (h *messagingHost) CallHost(_ context.Context, method string, params, out any) error {
	var result any
	switch method {
	case pluginapi.HostServiceSessionList:
		result = pluginapi.SessionListResult{Sessions: []pluginapi.SessionSummary{
			{SessionID: "source", Name: "Source"}, {SessionID: "target", Name: "Target"},
		}}
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
		h.sends = append(h.sends, p)
		result = pluginapi.SessionSendResult{SessionID: p.SessionID, State: "queued", QueueID: "queued-1"}
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
