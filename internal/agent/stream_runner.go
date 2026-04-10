package agent

import (
	"context"
	"errors"
	"fmt"
	"math"
	"math/rand"
	"strings"
	"time"

	"github.com/blueberrycongee/wuu/internal/compact"
	"github.com/blueberrycongee/wuu/internal/providers"
)

// StreamCallback receives streaming events for TUI rendering.
type StreamCallback func(event providers.StreamEvent)

// StreamRunner manages one multi-step coding turn with streaming.
// It is a thin wrapper around RunToolLoop that supplies a streamStep
// adapter (Step → providers.StreamClient.StreamChat with reconnect),
// so the actual loop logic — step counting, truncation recovery,
// context-overflow auto-compact — comes from the same code as Runner.
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

	step := &streamStep{
		client:  r.Client,
		onEvent: onEvent,
		retry:   r.streamRetryConfig(),
	}

	cfg := LoopConfig{
		Tools:       r.Tools,
		Model:       r.Model,
		Temperature: r.Temperature,
		MaxSteps:    r.MaxSteps,
		Compact: func(ctx context.Context, messages []providers.ChatMessage) ([]providers.ChatMessage, error) {
			return compact.Compact(ctx, messages, r.Client, r.Model)
		},
		// Forward each tool result through the streaming callback so
		// the TUI can render the tool card live (the loop itself only
		// records the tool message into the history).
		OnToolResult: func(call providers.ToolCall, result string) {
			if onEvent == nil {
				return
			}
			onEvent(providers.StreamEvent{
				Type:       providers.EventToolUseEnd,
				ToolCall:   &providers.ToolCall{ID: call.ID, Name: call.Name},
				ToolResult: truncateLog(result, 2000),
			})
		},
	}

	res, err := RunToolLoop(ctx, history, cfg, step)
	return res.Content, res.NewMessages, err
}

// streamStep adapts providers.StreamClient (with reconnect) to the
// transport-agnostic Step interface. One Execute call opens an SSE
// stream, consumes it to completion (with mid-attempt reconnect on
// early disconnects), accumulates the assistant content + tool calls
// + usage, and returns a normalized StepResult.
type streamStep struct {
	client  providers.StreamClient
	onEvent StreamCallback
	retry   providers.RetryConfig
}

func (s *streamStep) Execute(ctx context.Context, req providers.ChatRequest) (StepResult, error) {
	var (
		contentBuf   strings.Builder
		pendingTools = map[int]*providers.ToolCall{}
		usage        *providers.TokenUsage
	)
	if err := s.runStreamWithReconnect(ctx, req, &contentBuf, pendingTools, &usage); err != nil {
		return StepResult{}, fmt.Errorf("stream request failed: %w", err)
	}

	// Build ordered tool calls list from the pending map.
	toolCalls := make([]providers.ToolCall, 0, len(pendingTools))
	for i := 0; i < len(pendingTools); i++ {
		if tc, ok := pendingTools[i]; ok {
			toolCalls = append(toolCalls, *tc)
		}
	}

	return StepResult{
		Content:   contentBuf.String(),
		ToolCalls: toolCalls,
		Usage:     usage,
		// NOTE: SSE paths in providers/{openai,anthropic} don't yet
		// surface stop_reason / finish_reason through StreamEvent, so
		// streaming runs can't trigger truncation recovery today. The
		// auto-compact-on-overflow branch DOES fire because StreamChat
		// returns providers.HTTPError with ContextOverflow when the
		// initial connect fails. Plumbing truncation through SSE is a
		// follow-up.
		Truncated: false,
	}, nil
}

func truncateLog(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

func (s *streamStep) runStreamWithReconnect(
	ctx context.Context,
	req providers.ChatRequest,
	contentBuf *strings.Builder,
	pendingTools map[int]*providers.ToolCall,
	usage **providers.TokenUsage,
) error {
	cfg := s.retry
	onEvent := s.onEvent
	attempt := 0
	for {
		// Don't retry if the caller's context is already done.
		if ctx.Err() != nil {
			return ctx.Err()
		}
		ch, err := s.client.StreamChat(ctx, req)
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
				if event.Usage != nil {
					*usage = event.Usage
				}
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
