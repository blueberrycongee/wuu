package agent

import (
	"context"
	"errors"
	"fmt"
	"math"
	"math/rand"
	"strings"
	"sync"
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

	// OnUsage, when non-nil, is invoked once per LLM round-trip with
	// the per-call token counts reported by the provider. This mirrors
	// the field of the same name on the non-streaming Runner so that
	// callers driving long-lived background runs (e.g. sub-agents) can
	// surface live token accumulation while the run is still going.
	OnUsage func(input, output int)

	// ContextWindowOverride lets the caller pin a specific context
	// window for this model instead of consulting the built-in
	// registry. Use it when a user has configured an unknown or
	// proxied model that wuu wouldn't otherwise recognize. Zero
	// means "ask providers.ContextWindowFor(Model)".
	ContextWindowOverride int

	// DisableAutoCompact turns off the proactive fill-rate trigger.
	// The reactive context-overflow recovery still runs. Off by default.
	DisableAutoCompact bool

	// BeforeStep, when set, is called at the start of each model
	// round right before building the provider request. Any returned
	// messages are appended to history for that round.
	BeforeStep func() []providers.ChatMessage

	// Stream reconnect policy. Zero values use the Codex-aligned defaults.
	StreamMaxRetries        int
	StreamRetryInitialDelay time.Duration
	StreamRetryMaxDelay     time.Duration

	usageMu           sync.Mutex
	conversationUsage *UsageTracker
	trackedHistoryLen int
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
	res, err := r.RunWithCallback(ctx, history, r.OnEvent)
	if err != nil {
		return "", err
	}
	return res.Content, nil
}

// RunWithCallback executes a conversation turn with a per-call event callback.
// It accepts the full message history and returns the loop result, including
// any new messages produced during this turn and whether history was rewritten
// by auto-compaction.
func (r *StreamRunner) RunWithCallback(ctx context.Context, history []providers.ChatMessage, onEvent StreamCallback) (LoopResult, error) {
	if r.Client == nil {
		return LoopResult{}, errors.New("client is required")
	}
	if strings.TrimSpace(r.Model) == "" {
		return LoopResult{}, errors.New("model is required")
	}
	runUsage, baseHistoryLen := r.prepareUsageTracker(history)

	step := &streamStep{
		client:  r.Client,
		onEvent: onEvent,
		retry:   r.streamRetryConfig(),
	}

	maxCtx := r.ContextWindowOverride
	if maxCtx <= 0 {
		maxCtx = providers.ContextWindowFor(r.Model)
	}
	if r.DisableAutoCompact {
		maxCtx = 0 // disables the proactive trigger inside RunToolLoop
	}
	cfg := LoopConfig{
		Tools:            r.Tools,
		Model:            r.Model,
		Temperature:      r.Temperature,
		MaxSteps:         r.MaxSteps,
		MaxContextTokens: maxCtx,
		BeforeStep:       r.BeforeStep,
		OnUsage:          r.OnUsage,
		OnMessage: func(msg providers.ChatMessage) {
			if onEvent == nil {
				return
			}
			copyMsg := msg
			onEvent(providers.StreamEvent{
				Type:    providers.EventMessage,
				Message: &copyMsg,
			})
		},
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
		// Surface auto-compact events as a stream event so the TUI
		// can render a system line like "✦ Compacted history: 18 → 5
		// messages (~12k tokens)". The loop fires this for both the
		// proactive and the reactive overflow path.
		OnCompact: func(info CompactInfo) {
			if onEvent == nil {
				return
			}
			onEvent(providers.StreamEvent{
				Type:    providers.EventCompact,
				Content: formatCompactNotice(info),
			})
		},
		UsageTracker: runUsage,
	}

	res, err := RunToolLoop(ctx, history, cfg, step)
	if err != nil {
		r.commitUsageTracker(runUsage, baseHistoryLen)
		return res, err
	}
	finalHistoryLen := baseHistoryLen + len(res.NewMessages)
	if res.HistoryRewritten {
		finalHistoryLen = len(res.NewMessages)
	}
	r.commitUsageTracker(runUsage, finalHistoryLen)
	return res, nil
}

// prepareUsageTracker snapshots the runner's shared conversation
// usage state and advances it to the history passed for this turn.
// The returned tracker is run-local; callers must commit it
// explicitly after deciding which history actually persists.
func (r *StreamRunner) prepareUsageTracker(history []providers.ChatMessage) (*UsageTracker, int) {
	r.usageMu.Lock()
	defer r.usageMu.Unlock()

	if r.conversationUsage == nil {
		r.conversationUsage = NewUsageTracker()
	}

	tracker := r.conversationUsage.Clone()
	trackedLen := r.trackedHistoryLen
	if trackedLen < 0 {
		trackedLen = 0
	}
	if trackedLen > len(history) {
		tracker.Reset()
		trackedLen = 0
	}
	if trackedLen == 0 {
		tracker.RecordPendingMessages(history)
		return tracker, len(history)
	}
	if len(history) > trackedLen {
		tracker.RecordPendingMessages(history[trackedLen:])
		trackedLen = len(history)
	}
	return tracker, trackedLen
}

// commitUsageTracker publishes a run-local usage snapshot as the new
// shared baseline for future turns.
func (r *StreamRunner) commitUsageTracker(tracker *UsageTracker, historyLen int) {
	if tracker == nil {
		return
	}
	r.usageMu.Lock()
	defer r.usageMu.Unlock()
	r.conversationUsage = tracker.Clone()
	r.trackedHistoryLen = historyLen
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
		thinkingBuf  strings.Builder
		pendingTools = map[int]*providers.ToolCall{}
		usage        *providers.TokenUsage
		stopReason   string
		truncated    bool
	)
	if err := s.runStreamWithReconnect(ctx, req, &contentBuf, &thinkingBuf, pendingTools, &usage, &stopReason, &truncated); err != nil {
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
		Content:          contentBuf.String(),
		ReasoningContent: thinkingBuf.String(),
		ToolCalls:        toolCalls,
		Usage:            usage,
		StopReason:       stopReason,
		Truncated:        truncated,
	}, nil
}

func truncateLog(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// formatCompactNotice produces the human-readable string surfaced via
// EventCompact. Kept short — it shows up as a single system line in
// the chat viewport.
func formatCompactNotice(info CompactInfo) string {
	verb := "Compacted"
	if info.Reason == CompactReasonOverflow {
		verb = "Recovered from context overflow — compacted"
	}
	if info.TokensBefore > 0 {
		return fmt.Sprintf("✦ %s history: %d → %d messages (was ~%s)",
			verb, info.MessagesBefore, info.MessagesAfter,
			formatTokenCount(info.TokensBefore))
	}
	return fmt.Sprintf("✦ %s history: %d → %d messages",
		verb, info.MessagesBefore, info.MessagesAfter)
}

// formatTokenCount renders a token count in a compact form: 1234 →
// "1.2k", 12_345 → "12k", 1_234_567 → "1.2M".
func formatTokenCount(n int) string {
	switch {
	case n < 1000:
		return fmt.Sprintf("%d tokens", n)
	case n < 10_000:
		return fmt.Sprintf("%.1fk tokens", float64(n)/1000)
	case n < 1_000_000:
		return fmt.Sprintf("%dk tokens", n/1000)
	default:
		return fmt.Sprintf("%.1fM tokens", float64(n)/1_000_000)
	}
}

func (s *streamStep) runStreamWithReconnect(
	ctx context.Context,
	req providers.ChatRequest,
	contentBuf *strings.Builder,
	thinkingBuf *strings.Builder,
	pendingTools map[int]*providers.ToolCall,
	usage **providers.TokenUsage,
	stopReason *string,
	truncated *bool,
) error {
	cfg := s.retry
	onEvent := s.onEvent
	attempt := 0
	emitLifecycle := func(phase providers.StreamLifecyclePhase, retryCount int, reason error, retryIn time.Duration) {
		if onEvent == nil {
			return
		}
		details := &providers.StreamLifecycle{
			Phase:       phase,
			Attempt:     retryCount + 1,
			MaxAttempts: cfg.MaxRetries + 1,
			RetryCount:  retryCount,
			MaxRetries:  cfg.MaxRetries,
			RetryIn:     retryIn,
		}
		if reason != nil {
			details.Reason = providers.StreamErrorSummary(reason)
		}
		onEvent(providers.StreamEvent{
			Type:      providers.EventLifecycle,
			Lifecycle: details,
		})
	}
	for {
		// Don't retry if the caller's context is already done.
		if ctx.Err() != nil {
			return ctx.Err()
		}
		emitLifecycle(providers.StreamPhaseConnecting, attempt, nil, 0)
		ch, err := s.client.StreamChat(ctx, req)
		if err != nil {
			if ctx.Err() == nil && shouldRetryStreamError(err, attempt, cfg.MaxRetries) {
				delay := streamRetryDelay(attempt, cfg.InitialDelay, cfg.MaxDelay)
				providers.DebugLogf("stream connect failed, reconnecting (%d/%d) in %s: %v", attempt+1, cfg.MaxRetries, delay, err)
				emitLifecycle(providers.StreamPhaseReconnecting, attempt+1, err, delay)
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
			emitLifecycle(providers.StreamPhaseFailed, attempt, err, 0)
			return err
		}
		emitLifecycle(providers.StreamPhaseConnected, attempt, nil, 0)

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

			case providers.EventThinkingDelta:
				thinkingBuf.WriteString(event.Content)

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
				if event.StopReason != "" {
					*stopReason = event.StopReason
				}
				if event.Truncated {
					*truncated = true
				}
			}

			if onEvent != nil {
				onEvent(event)
			}
		}

		if streamErr == nil && !sawDone {
			streamErr = providers.NewIncompleteStreamError("stream closed before done")
		}
		if streamErr == nil {
			return nil
		}

		// Only retry when the stream failed before producing any user-visible
		// output AND the parent context is still alive.
		if !sawOutput && ctx.Err() == nil && shouldRetryStreamError(streamErr, attempt, cfg.MaxRetries) {
			delay := streamRetryDelay(attempt, cfg.InitialDelay, cfg.MaxDelay)
			providers.DebugLogf("stream disconnected early, reconnecting (%d/%d) in %s: %v", attempt+1, cfg.MaxRetries, delay, streamErr)
			emitLifecycle(providers.StreamPhaseReconnecting, attempt+1, streamErr, delay)
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

		emitLifecycle(providers.StreamPhaseFailed, attempt, streamErr, 0)
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
	cfg.MaxRetries = 5
	cfg.InitialDelay = 200 * time.Millisecond
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
