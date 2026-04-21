package providers

import "context"

// AdaptStreamClient returns a streaming-capable view of client.
//
// If client already implements StreamClient, it is returned unchanged.
// Otherwise this wraps unary Chat responses into a synthetic stream:
// content/tool events followed by a terminal done event.
func AdaptStreamClient(client Client) StreamClient {
	if client == nil {
		return nil
	}
	if streamClient, ok := client.(StreamClient); ok {
		return streamClient
	}
	return adaptedStreamClient{client: client}
}

type adaptedStreamClient struct {
	client Client
}

func (a adaptedStreamClient) Chat(ctx context.Context, req ChatRequest) (ChatResponse, error) {
	return a.client.Chat(ctx, req)
}

func (a adaptedStreamClient) StreamChat(ctx context.Context, req ChatRequest) (<-chan StreamEvent, error) {
	resp, err := a.client.Chat(ctx, req)
	if err != nil {
		return nil, err
	}

	events := make([]StreamEvent, 0, 1+len(resp.ToolCalls)*2+1)
	if len(resp.ReasoningBlocks) > 0 {
		for _, block := range resp.ReasoningBlocks {
			if block.Thinking != "" {
				events = append(events, StreamEvent{Type: EventThinkingDelta, Content: block.Thinking})
			}
			reasoningBlock := block
			events = append(events, StreamEvent{Type: EventThinkingDone, ReasoningBlock: &reasoningBlock})
		}
	} else if resp.ReasoningContent != "" {
		events = append(events, StreamEvent{Type: EventThinkingDelta, Content: resp.ReasoningContent})
		events = append(events, StreamEvent{Type: EventThinkingDone})
	}
	if resp.Content != "" {
		events = append(events, StreamEvent{Type: EventContentDelta, Content: resp.Content})
	}
	for _, call := range resp.ToolCalls {
		toolCall := call
		events = append(events, StreamEvent{
			Type:     EventToolUseStart,
			ToolCall: &ToolCall{ID: toolCall.ID, Name: toolCall.Name},
		})
		events = append(events, StreamEvent{
			Type:     EventToolUseEnd,
			ToolCall: &toolCall,
		})
	}
	events = append(events, StreamEvent{
		Type:       EventDone,
		Usage:      resp.Usage,
		StopReason: resp.StopReason,
		Truncated:  resp.Truncated,
	})

	ch := make(chan StreamEvent, len(events))
	for _, event := range events {
		ch <- event
	}
	close(ch)
	return ch, nil
}
