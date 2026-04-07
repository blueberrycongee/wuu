package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/blueberrycongee/wuu/internal/providers"
)

// StreamCallback receives streaming events for TUI rendering.
type StreamCallback func(event providers.StreamEvent)

// StreamRunner manages one multi-step coding turn with streaming.
type StreamRunner struct {
	Client       providers.StreamClient
	Tools        ToolExecutor
	Model        string
	SystemPrompt string
	MaxSteps     int
	Temperature  float64
	OnEvent      StreamCallback
}

// Run executes one prompt with streaming tool-use loop.
func (r *StreamRunner) Run(ctx context.Context, prompt string) (string, error) {
	if strings.TrimSpace(prompt) == "" {
		return "", errors.New("prompt is required")
	}
	var history []providers.ChatMessage
	if strings.TrimSpace(r.SystemPrompt) != "" {
		history = append(history, providers.ChatMessage{Role: "system", Content: r.SystemPrompt})
	}
	history = append(history, providers.ChatMessage{Role: "user", Content: prompt})
	result, _, err := r.RunWithCallback(ctx, history, r.OnEvent)
	return result, err
}

// RunWithCallback executes a conversation turn with a per-call event callback.
// It accepts the full message history and returns the assistant's text plus
// any new messages produced during this turn (assistant + tool results).
func (r *StreamRunner) RunWithCallback(ctx context.Context, history []providers.ChatMessage, onEvent StreamCallback) (string, []providers.ChatMessage, error) {
	if r.Client == nil {
		return "", nil, errors.New("client is required")
	}
	if strings.TrimSpace(r.Model) == "" {
		return "", nil, errors.New("model is required")
	}

	messages := make([]providers.ChatMessage, len(history))
	copy(messages, history)
	startLen := len(messages)

	for {
		req := providers.ChatRequest{
			Model:       r.Model,
			Messages:    messages,
			Temperature: r.Temperature,
		}
		if r.Tools != nil {
			req.Tools = r.Tools.Definitions()
		}

		ch, err := r.Client.StreamChat(ctx, req)
		if err != nil {
			return "", nil, fmt.Errorf("stream request failed: %w", err)
		}

		var contentBuf strings.Builder
		// Map from tool call index to accumulated tool call.
		pendingTools := map[int]*providers.ToolCall{}

		for event := range ch {
			if onEvent != nil {
				onEvent(event)
			}

			switch event.Type {
			case providers.EventContentDelta:
				contentBuf.WriteString(event.Content)

			case providers.EventToolUseStart:
				if event.ToolCall != nil {
					idx := len(pendingTools)
					pendingTools[idx] = &providers.ToolCall{
						ID:   event.ToolCall.ID,
						Name: event.ToolCall.Name,
					}
				}

			case providers.EventToolUseDelta:
				// Append partial arguments to the most recently started tool call.
				if len(pendingTools) > 0 {
					latest := pendingTools[len(pendingTools)-1]
					latest.Arguments += event.Content
				}

			case providers.EventToolUseEnd:
				// EventToolUseEnd carries the fully accumulated arguments from
				// the provider layer. Use them if present; otherwise keep what
				// we accumulated from deltas.
				if event.ToolCall != nil && event.ToolCall.Arguments != "" {
					for _, tc := range pendingTools {
						if tc.ID == event.ToolCall.ID {
							tc.Arguments = event.ToolCall.Arguments
							break
						}
					}
				}

			case providers.EventError:
				if event.Error != nil {
					return "", nil, event.Error
				}
				return "", nil, errors.New("stream error")

			case providers.EventDone:
				// Stream complete.
			}
		}

		// Build ordered tool calls list from pending map.
		toolCalls := make([]providers.ToolCall, 0, len(pendingTools))
		for i := 0; i < len(pendingTools); i++ {
			if tc, ok := pendingTools[i]; ok {
				toolCalls = append(toolCalls, *tc)
			}
		}

		assistant := providers.ChatMessage{
			Role:      "assistant",
			Content:   contentBuf.String(),
			ToolCalls: toolCalls,
		}
		messages = append(messages, assistant)

		// No tool calls means the model is done.
		if len(toolCalls) == 0 {
			result := strings.TrimSpace(contentBuf.String())
			if result == "" {
				return "", nil, errors.New("model returned empty answer")
			}
			return result, messages[startLen:], nil
		}

		// Execute each requested tool.
		if r.Tools == nil {
			return "", nil, errors.New("model requested tools but none are configured")
		}

		for _, call := range toolCalls {
			providers.DebugLogf("executing tool: %s, id: %s, args: %s", call.Name, call.ID, call.Arguments)
			toolResult, execErr := r.Tools.Execute(ctx, call)
			if execErr != nil {
				providers.DebugLogf("tool error: %s: %v", call.Name, execErr)
				toolResult = errorJSON(execErr)
			}
			providers.DebugLogf("tool result: %s: %s", call.Name, truncateLog(toolResult, 500))
			// Emit tool result to TUI.
			if onEvent != nil {
				onEvent(providers.StreamEvent{
					Type:       providers.EventToolUseEnd,
					ToolCall:   &providers.ToolCall{ID: call.ID, Name: call.Name},
					ToolResult: truncateLog(toolResult, 2000),
				})
			}
			messages = append(messages, providers.ChatMessage{
				Role:       "tool",
				Name:       call.Name,
				ToolCallID: call.ID,
				Content:    toolResult,
			})
		}
	}
}

func truncateLog(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
