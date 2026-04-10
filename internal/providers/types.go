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

// InputImage carries one user-provided image in base64 form.
type InputImage struct {
	MediaType string
	Data      string
}

// ChatMessage is a generic multi-provider chat message.
type ChatMessage struct {
	Role       string
	Name       string
	Content    string
	Images     []InputImage
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
	Usage     *TokenUsage // optional; nil when the provider didn't return usage
	// StopReason is the raw provider stop signal, normalized to lowercase.
	// Common values: "stop" / "end_turn" (natural finish), "length" /
	// "max_tokens" (output truncation), "tool_calls" / "tool_use".
	StopReason string
	// Truncated is true when the model hit its output token cap mid-response.
	// Callers (e.g. agent.Runner) can use this to issue a "continue" prompt.
	Truncated bool
}

// Client sends chat requests to an LLM provider.
type Client interface {
	Chat(ctx context.Context, req ChatRequest) (ChatResponse, error)
}

// StreamEventType classifies events emitted during streaming.
type StreamEventType string

const (
	EventContentDelta  StreamEventType = "content_delta"
	EventThinkingDelta StreamEventType = "thinking_delta"
	EventThinkingDone  StreamEventType = "thinking_done"
	EventToolUseStart  StreamEventType = "tool_use_start"
	EventToolUseDelta  StreamEventType = "tool_use_delta"
	EventToolUseEnd    StreamEventType = "tool_use_end"
	EventReconnect     StreamEventType = "reconnect"
	EventDone          StreamEventType = "done"
	EventError         StreamEventType = "error"
)

// TokenUsage reports token consumption for a streaming response.
type TokenUsage struct {
	InputTokens  int
	OutputTokens int
}

// StreamEvent is a single event from a streaming chat response.
type StreamEvent struct {
	Type       StreamEventType
	Content    string
	ToolCall   *ToolCall
	ToolResult string
	Error      error
	Usage      *TokenUsage
}

// StreamClient extends Client with streaming support.
type StreamClient interface {
	Client
	StreamChat(ctx context.Context, req ChatRequest) (<-chan StreamEvent, error)
}
