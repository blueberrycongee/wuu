package providers

import "context"

// ToolDefinition describes a callable tool exposed to the model.
type ToolDefinition struct {
	Name        string
	Description string
	InputSchema map[string]any
}

// ToolCall is a model requested tool execution.
type ToolCall struct {
	ID        string
	Name      string
	Arguments string
}

// ChatMessage is a generic multi-provider chat message.
type ChatMessage struct {
	Role       string
	Name       string
	Content    string
	ToolCallID string
	ToolCalls  []ToolCall
}

// ChatRequest is the normalized request payload for providers.
type ChatRequest struct {
	Model       string
	Messages    []ChatMessage
	Tools       []ToolDefinition
	Temperature float64
}

// ChatResponse is the normalized response from providers.
type ChatResponse struct {
	Content   string
	ToolCalls []ToolCall
}

// Client sends chat requests to an LLM provider.
type Client interface {
	Chat(ctx context.Context, req ChatRequest) (ChatResponse, error)
}
