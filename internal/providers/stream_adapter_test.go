package providers

import (
	"context"
	"errors"
	"testing"
)

type unaryOnlyClient struct {
	resp ChatResponse
	err  error
}

func (c *unaryOnlyClient) Chat(context.Context, ChatRequest) (ChatResponse, error) {
	if c.err != nil {
		return ChatResponse{}, c.err
	}
	return c.resp, nil
}

func TestAdaptStreamClient_WrapsUnaryResponseIntoEvents(t *testing.T) {
	usage := &TokenUsage{InputTokens: 10, OutputTokens: 4}
	client := &unaryOnlyClient{resp: ChatResponse{
		Content: "hello",
		Phase:   MessagePhaseFinalAnswer,
		ToolCalls: []ToolCall{{
			ID:        "call_1",
			Name:      "run_shell",
			Arguments: `{"command":"pwd"}`,
		}},
		Usage:      usage,
		StopReason: "length",
		Truncated:  true,
	}}

	streamClient := AdaptStreamClient(client)
	ch, err := streamClient.StreamChat(context.Background(), ChatRequest{Model: "test"})
	if err != nil {
		t.Fatalf("StreamChat returned error: %v", err)
	}

	var events []StreamEvent
	for event := range ch {
		events = append(events, event)
	}
	if len(events) != 4 {
		t.Fatalf("expected 4 events, got %d", len(events))
	}
	if events[0].Type != EventContentDelta || events[0].Content != "hello" || events[0].Phase != MessagePhaseFinalAnswer {
		t.Fatalf("unexpected first event: %+v", events[0])
	}
	if events[1].Type != EventToolUseStart || events[1].ToolCall == nil || events[1].ToolCall.ID != "call_1" {
		t.Fatalf("unexpected tool start event: %+v", events[1])
	}
	if events[2].Type != EventToolUseEnd || events[2].ToolCall == nil || events[2].ToolCall.Arguments != `{"command":"pwd"}` {
		t.Fatalf("unexpected tool end event: %+v", events[2])
	}
	if events[3].Type != EventDone || events[3].Usage != usage || !events[3].Truncated || events[3].StopReason != "length" {
		t.Fatalf("unexpected done event: %+v", events[3])
	}
}

func TestAdaptStreamClient_PropagatesUnaryErrorFromStreamChat(t *testing.T) {
	wantErr := errors.New("chat failed")
	client := &unaryOnlyClient{err: wantErr}
	streamClient := AdaptStreamClient(client)
	_, err := streamClient.StreamChat(context.Background(), ChatRequest{Model: "test"})
	if !errors.Is(err, wantErr) {
		t.Fatalf("expected %v, got %v", wantErr, err)
	}
}
