package agent

import (
	"context"
	"errors"
	"fmt"
	"math"
	"math/rand"
	"strings"
	"time"

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

	// Stream reconnect policy (more aggressive than codex default 5).
	// Zero values use built-in defaults.
	StreamMaxRetries        int
	StreamRetryInitialDelay time.Duration
	StreamRetryMaxDelay     time.Duration
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

	// MaxSteps == 0 means unlimited (no step cap). Aligned with the
	// non-streaming Runner and with Claude Code's default behavior:
	// the model decides when it's done by emitting a final message.
	// A positive value still acts as a runaway safety net.
	maxSteps := r.MaxSteps

	messages := make([]providers.ChatMessage, len(history))
	copy(messages, history)
	startLen := len(messages)

	for step := 0; maxSteps == 0 || step < maxSteps; step++ {
		req := providers.ChatRequest{
			Model:       r.Model,
			Messages:    messages,
			Temperature: r.Temperature,
		}
		if r.Tools != nil {
			req.Tools = r.Tools.Definitions()
		}

		var contentBuf strings.Builder
		// Map from tool call index to accumulated tool call.
		pendingTools := map[int]*providers.ToolCall{}
		if err := r.runStreamWithReconnect(ctx, req, &contentBuf, pendingTools, onEvent); err != nil {
			return "", nil, fmt.Errorf("stream request failed: %w", err)
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

	return "", nil, fmt.Errorf("max steps exceeded (%d)", maxSteps)
}

func truncateLog(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

func (r *StreamRunner) runStreamWithReconnect(
	ctx context.Context,
	req providers.ChatRequest,
	contentBuf *strings.Builder,
	pendingTools map[int]*providers.ToolCall,
	onEvent StreamCallback,
) error {
	cfg := r.streamRetryConfig()
	attempt := 0
	for {
		// Don't retry if the caller's context is already done.
		if ctx.Err() != nil {
			return ctx.Err()
		}
		ch, err := r.Client.StreamChat(ctx, req)
		if err != nil {
			if ctx.Err() == nil && shouldRetryStreamError(err, attempt, cfg.MaxRetries) {
				delay := streamRetryDelay(attempt, cfg.InitialDelay, cfg.MaxDelay)
				providers.DebugLogf("stream connect failed, reconnecting (%d/%d) in %s: %v", attempt+1, cfg.MaxRetries, delay, err)
				if onEvent != nil {
					onEvent(providers.StreamEvent{
						Type:    providers.EventReconnect,
						Content: fmt.Sprintf("Reconnecting... %d/%d", attempt+1, cfg.MaxRetries),
					})
				}
				if waitErr := waitWithContext(ctx, delay); waitErr != nil {
					return waitErr
				}
				attempt++
				continue
			}
			return err
		}

		var (
			streamErr error
			sawDone   bool
			sawOutput bool
		)

		for event := range ch {
			switch event.Type {
			case providers.EventContentDelta,
				providers.EventToolUseStart,
				providers.EventToolUseDelta,
				providers.EventToolUseEnd,
				providers.EventThinkingDelta,
				providers.EventThinkingDone:
				sawOutput = true
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
					streamErr = event.Error
				} else {
					streamErr = errors.New("stream error")
				}
				continue

			case providers.EventDone:
				sawDone = true
			}

			if onEvent != nil {
				onEvent(event)
			}
		}

		if streamErr == nil && !sawDone {
			streamErr = errors.New("stream closed before done")
		}
		if streamErr == nil {
			return nil
		}

		// Only retry when the stream failed before producing any user-visible
		// output AND the parent context is still alive.
		if !sawOutput && ctx.Err() == nil && shouldRetryStreamError(streamErr, attempt, cfg.MaxRetries) {
			delay := streamRetryDelay(attempt, cfg.InitialDelay, cfg.MaxDelay)
			providers.DebugLogf("stream disconnected early, reconnecting (%d/%d) in %s: %v", attempt+1, cfg.MaxRetries, delay, streamErr)
			if onEvent != nil {
				onEvent(providers.StreamEvent{
					Type:    providers.EventReconnect,
					Content: fmt.Sprintf("Reconnecting... %d/%d", attempt+1, cfg.MaxRetries),
				})
			}
			if waitErr := waitWithContext(ctx, delay); waitErr != nil {
				return waitErr
			}
			attempt++
			continue
		}

		if onEvent != nil {
			onEvent(providers.StreamEvent{
				Type:  providers.EventError,
				Error: streamErr,
			})
		}
		return streamErr
	}
}

func (r *StreamRunner) streamRetryConfig() providers.RetryConfig {
	cfg := providers.DefaultRetryConfig()
	// Codex default is 5; we default to 6 to be slightly more aggressive.
	cfg.MaxRetries = 6
	cfg.InitialDelay = 250 * time.Millisecond
	cfg.MaxDelay = 5 * time.Second
	if r.StreamMaxRetries > 0 {
		cfg.MaxRetries = r.StreamMaxRetries
	}
	if r.StreamRetryInitialDelay > 0 {
		cfg.InitialDelay = r.StreamRetryInitialDelay
	}
	if r.StreamRetryMaxDelay > 0 {
		cfg.MaxDelay = r.StreamRetryMaxDelay
	}
	if cfg.InitialDelay > cfg.MaxDelay {
		cfg.InitialDelay = cfg.MaxDelay
	}
	return cfg
}

func shouldRetryStreamError(err error, attempt, maxRetries int) bool {
	if attempt >= maxRetries {
		return false
	}
	return providers.IsRetryable(err)
}

func streamRetryDelay(attempt int, initial, maxDelay time.Duration) time.Duration {
	base := float64(initial) * math.Pow(2, float64(attempt))
	if base > float64(maxDelay) {
		base = float64(maxDelay)
	}
	// 0-20% jitter to avoid herd retry.
	jitter := base * 0.2 * rand.Float64()
	return time.Duration(base + jitter)
}

func waitWithContext(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
