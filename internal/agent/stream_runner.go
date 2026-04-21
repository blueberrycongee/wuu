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
	wuucontext "github.com/blueberrycongee/wuu/internal/context"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/stringutil"
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

	// StreamingToolExecution, when true, starts executing read-only
	// tools during model streaming (before the full response arrives).
	// Off by default until stabilized. Aligned with Claude Code's
	// StreamingToolExecutor pattern.
	StreamingToolExecution bool

	// BeforeStep, when set, is called at the start of each model
	// round right before building the provider request. Any returned
	// messages are appended to history for that round.
	BeforeStep func() []providers.ChatMessage

	// Effort controls reasoning depth. See ChatRequest.Effort.
	Effort string

	// Stream reconnect policy. Zero values use CC-aligned defaults.
	StreamReconnectBudget   time.Duration // total time for reconnection (default: 2m)
	StreamRetryInitialDelay time.Duration // backoff start (default: 1s)
	StreamRetryMaxDelay     time.Duration // backoff cap (default: 30s)

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
	history = filterEphemeralHistory(history)
	runUsage, baseHistoryLen := r.prepareUsageTracker(history)

	step := &streamStep{
		client:                  r.Client,
		onEvent:                 onEvent,
		retry:                   r.streamReconnectCfg(),
		tools:                   r.Tools,
		enableStreamingToolExec: r.StreamingToolExecution,
	}

	maxCtx := r.ContextWindowOverride
	if maxCtx <= 0 {
		maxCtx = providers.ContextWindowFor(r.Model)
	}
	if r.DisableAutoCompact {
		maxCtx = 0 // disables the proactive trigger inside RunToolLoop
	}
	beforeStep := r.BeforeStep
	if beforeStep != nil {
		beforeStep = func() []providers.ChatMessage {
			return filterEphemeralHistory(r.BeforeStep())
		}
	}
	cfg := LoopConfig{
		Tools:            r.Tools,
		Model:            r.Model,
		Temperature:      r.Temperature,
		MaxSteps:         r.MaxSteps,
		MaxContextTokens: maxCtx,
		BeforeStep:       beforeStep,
		OnUsage:          r.OnUsage,
		OnMessage: func(msg providers.ChatMessage) {
			if onEvent == nil || isEphemeralHistoryMessage(msg) {
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
		Effort:       r.Effort,
	}

	res, err := RunToolLoop(ctx, history, cfg, step)
	res.NewMessages = filterEphemeralHistory(res.NewMessages)
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

func isEphemeralHistoryMessage(msg providers.ChatMessage) bool {
	return msg.Role == "user" && wuucontext.IsSystemReminder(msg.Name, msg.Content)
}

func filterEphemeralHistory(msgs []providers.ChatMessage) []providers.ChatMessage {
	if len(msgs) == 0 {
		return nil
	}
	filtered := make([]providers.ChatMessage, 0, len(msgs))
	for _, msg := range msgs {
		if !isEphemeralHistoryMessage(msg) {
			filtered = append(filtered, msg)
		}
	}
	return filtered
}

// streamStep adapts providers.StreamClient (with reconnect) to the
// transport-agnostic Step interface. One Execute call opens an SSE
// stream, consumes it to completion (with mid-attempt reconnect on
// early disconnects), accumulates the assistant content + tool calls
// + usage, and returns a normalized StepResult.
type streamStep struct {
	client  providers.StreamClient
	onEvent StreamCallback
	retry   streamReconnectConfig
	// Streaming tool execution: when set, read-only tools start
	// executing as soon as their arguments are fully received,
	// overlapping with continued model output.
	tools                   ToolExecutor
	enableStreamingToolExec bool
}

func (s *streamStep) Execute(ctx context.Context, req providers.ChatRequest) (StepResult, error) {
	// Streaming tool execution: collect results from tools started
	// mid-stream. Protected by mu since goroutines write concurrently.
	var precomputeMu sync.Mutex
	precomputed := map[string]string{} // tool call ID → result

	var (
		contentBuf      strings.Builder
		thinkingBuf     strings.Builder
		reasoningBlocks []providers.ReasoningBlock
		pendingTools    = map[int]*providers.ToolCall{}
		usage           *providers.TokenUsage
		stopReason      string
		truncated       bool
	)

	// Wrap the event callback to intercept ToolUseEnd and kick off
	// read-only tool execution during streaming.
	origOnEvent := s.onEvent
	if s.enableStreamingToolExec && s.tools != nil {
		s.onEvent = func(ev providers.StreamEvent) {
			if ev.Type == providers.EventToolUseEnd && ev.ToolCall != nil {
				tc := ev.ToolCall
				if tc.ID != "" && tc.Arguments != "" && isReadOnlyTool(s.tools, tc.Name) {
					go func() {
						result, err := s.tools.Execute(ctx, providers.ToolCall{
							ID:        tc.ID,
							Name:      tc.Name,
							Arguments: tc.Arguments,
						})
						if err != nil {
							return // skip precompute on error
						}
						precomputeMu.Lock()
						precomputed[tc.ID] = result
						precomputeMu.Unlock()
					}()
				}
			}
			if origOnEvent != nil {
				origOnEvent(ev)
			}
		}
	}

	if err := s.runStreamWithReconnect(ctx, req, &contentBuf, &thinkingBuf, &reasoningBlocks, pendingTools, &usage, &stopReason, &truncated); err != nil {
		s.onEvent = origOnEvent // restore
		return StepResult{}, fmt.Errorf("stream request failed: %w", err)
	}
	s.onEvent = origOnEvent // restore

	// Build ordered tool calls list from the pending map.
	toolCalls := make([]providers.ToolCall, 0, len(pendingTools))
	for i := 0; i < len(pendingTools); i++ {
		if tc, ok := pendingTools[i]; ok {
			toolCalls = append(toolCalls, *tc)
		}
	}

	// Non-streaming fallback: when the stream completed without
	// producing any content or tool calls AND there is no normal
	// stop reason, the stream was likely broken by a proxy or
	// provider compatibility issue. Try one non-streaming Chat()
	// call before giving up — this mirrors Claude Code's fallback
	// strategy for empty SSE responses.
	if strings.TrimSpace(contentBuf.String()) == "" && len(toolCalls) == 0 && !isNormalStop(stopReason) {
		providers.DebugLogf("stream returned empty content with stop_reason=%q, attempting non-streaming fallback", stopReason)
		if s.onEvent != nil {
			s.onEvent(providers.StreamEvent{
				Type:    providers.EventReconnect,
				Content: "Empty stream response — trying non-streaming fallback...",
			})
		}
		resp, err := s.client.Chat(ctx, req)
		if err != nil {
			return StepResult{}, fmt.Errorf("non-streaming fallback failed: %w", err)
		}
		fbToolCalls := make([]providers.ToolCall, len(resp.ToolCalls))
		copy(fbToolCalls, resp.ToolCalls)
		// Emit the fallback content through the streaming callback so
		// the TUI can render it.
		if s.onEvent != nil && strings.TrimSpace(resp.Content) != "" {
			s.onEvent(providers.StreamEvent{
				Type:    providers.EventContentDelta,
				Content: resp.Content,
			})
			s.onEvent(providers.StreamEvent{
				Type:       providers.EventDone,
				Usage:      resp.Usage,
				StopReason: resp.StopReason,
			})
		}
		return StepResult{
			Content:          resp.Content,
			ReasoningContent: resp.ReasoningContent,
			ReasoningBlocks:  cloneReasoningBlocks(resp.ReasoningBlocks),
			ToolCalls:        fbToolCalls,
			Usage:            resp.Usage,
			StopReason:       resp.StopReason,
			Truncated:        resp.Truncated,
		}, nil
	}

	// Collect any precomputed results from streaming tool execution.
	precomputeMu.Lock()
	var pc map[string]string
	if len(precomputed) > 0 {
		pc = precomputed
	}
	precomputeMu.Unlock()

	return StepResult{
		Content:            contentBuf.String(),
		ReasoningContent:   thinkingBuf.String(),
		ReasoningBlocks:    cloneReasoningBlocks(reasoningBlocks),
		ToolCalls:          toolCalls,
		Usage:              usage,
		StopReason:         stopReason,
		Truncated:          truncated,
		PrecomputedResults: pc,
	}, nil
}

// isNormalStop reports whether the stop reason indicates the model
// intentionally ended its turn (as opposed to the stream breaking).
func isNormalStop(reason string) bool {
	switch reason {
	case "stop", "end_turn":
		return true
	}
	return false
}

func truncateLog(s string, maxLen int) string {
	return stringutil.Truncate(s, maxLen, "...")
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
	reasoningBlocks *[]providers.ReasoningBlock,
	pendingTools map[int]*providers.ToolCall,
	usage **providers.TokenUsage,
	stopReason *string,
	truncated *bool,
) error {
	cfg := s.retry
	onEvent := s.onEvent
	attempt := 0
	var reconnectStart time.Time

	elapsed := func() time.Duration {
		if reconnectStart.IsZero() {
			return 0
		}
		return time.Since(reconnectStart)
	}

	emitLifecycle := func(phase providers.StreamLifecyclePhase, retryCount int, reason error, retryIn time.Duration) {
		if onEvent == nil {
			return
		}
		details := &providers.StreamLifecycle{
			Phase:      phase,
			Attempt:    retryCount + 1,
			RetryCount: retryCount,
			RetryIn:    retryIn,
			Elapsed:    elapsed(),
			Budget:     cfg.Budget,
		}
		if reason != nil {
			details.Reason = providers.StreamErrorSummary(reason)
		}
		onEvent(providers.StreamEvent{
			Type:      providers.EventLifecycle,
			Lifecycle: details,
		})
	}

	reconnect := func(err error) (delay time.Duration, ok bool) {
		if reconnectStart.IsZero() {
			reconnectStart = time.Now()
		}
		el := elapsed()
		if !shouldRetryStreamError(err, el, cfg.Budget) {
			return 0, false
		}
		delay = streamRetryDelay(attempt, cfg.InitialDelay, cfg.MaxDelay)
		// Clamp delay so we don't overshoot the budget.
		if remaining := cfg.Budget - el; delay > remaining {
			delay = remaining
		}
		attempt++
		providers.DebugLogf("stream reconnecting (%d, %s/%s) in %s: %v",
			attempt, el.Round(time.Second), cfg.Budget, delay, err)
		// Log full error details for post-mortem analysis.
		var httpErr *providers.HTTPError
		if errors.As(err, &httpErr) {
			providers.DebugLogf("  HTTP %d body: %s", httpErr.StatusCode, httpErr.Body)
		}
		var streamErr *providers.StreamError
		if errors.As(err, &streamErr) {
			providers.DebugLogf("  stream error code=%s msg=%s", streamErr.Code, streamErr.Message)
		}
		emitLifecycle(providers.StreamPhaseReconnecting, attempt, err, delay)
		if onEvent != nil {
			onEvent(providers.StreamEvent{
				Type:    providers.EventReconnect,
				Content: fmt.Sprintf("Reconnecting... %s / %s", el.Round(time.Second), cfg.Budget),
			})
		}
		return delay, true
	}

	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		emitLifecycle(providers.StreamPhaseConnecting, attempt, nil, 0)

		// Connection-stage timeouts (dial, TLS, response-header) are
		// already set on the HTTP transport by BuildStreamingHTTPClient.
		// Do NOT wrap ctx in a per-attempt WithTimeout here — the HTTP
		// request is created with the context passed to StreamChat, and
		// canceling that context kills resp.Body reads (especially on
		// HTTP/2), aborting the SSE stream immediately after connect.
		ch, err := s.client.StreamChat(ctx, req)
		if err != nil {
			if ctx.Err() == nil {
				if delay, ok := reconnect(err); ok {
					if waitErr := waitWithContext(ctx, delay); waitErr != nil {
						return waitErr
					}
					continue
				}
			}
			emitLifecycle(providers.StreamPhaseFailed, attempt, err, 0)
			return err
		}
		// Successful connect resets the reconnect clock.
		reconnectStart = time.Time{}
		attempt = 0
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

			case providers.EventThinkingDone:
				if event.ReasoningBlock != nil {
					*reasoningBlocks = append(*reasoningBlocks, *event.ReasoningBlock)
				}

			case providers.EventToolUseStart:
				if event.ToolCall != nil {
					idx := len(pendingTools)
					pendingTools[idx] = &providers.ToolCall{
						ID:   event.ToolCall.ID,
						Name: event.ToolCall.Name,
					}
				}

			case providers.EventToolUseDelta:
				if len(pendingTools) > 0 {
					latest := pendingTools[len(pendingTools)-1]
					latest.Arguments += event.Content
				}

			case providers.EventToolUseEnd:
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
		if !sawOutput && ctx.Err() == nil {
			if delay, ok := reconnect(streamErr); ok {
				if waitErr := waitWithContext(ctx, delay); waitErr != nil {
					return waitErr
				}
				continue
			}
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

// isReadOnlyTool checks if a tool is read-only and concurrency-safe
// via the ToolMetadataProvider interface.
func isReadOnlyTool(executor ToolExecutor, name string) bool {
	mp, ok := executor.(ToolMetadataProvider)
	if !ok {
		return false
	}
	meta, found := mp.ToolMetadata(name)
	return found && meta.ReadOnly && meta.ConcurrencySafe
}

// streamReconnectConfig holds CC-aligned time-budget reconnection parameters.
type streamReconnectConfig struct {
	Budget       time.Duration
	InitialDelay time.Duration
	MaxDelay     time.Duration
}

const (
	defaultReconnectBudget = 10 * time.Minute
	defaultReconnectDelay  = 1 * time.Second
	defaultReconnectMax    = 30 * time.Second
)

func (r *StreamRunner) streamReconnectCfg() streamReconnectConfig {
	cfg := streamReconnectConfig{
		Budget:       defaultReconnectBudget,
		InitialDelay: defaultReconnectDelay,
		MaxDelay:     defaultReconnectMax,
	}
	if r.StreamReconnectBudget > 0 {
		cfg.Budget = r.StreamReconnectBudget
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

func shouldRetryStreamError(err error, elapsed, budget time.Duration) bool {
	if elapsed >= budget {
		return false
	}
	return providers.IsRetryable(err)
}

func streamRetryDelay(attempt int, initial, maxDelay time.Duration) time.Duration {
	base := float64(initial) * math.Pow(2, float64(attempt))
	if base > float64(maxDelay) {
		base = float64(maxDelay)
	}
	// ±25% jitter (CC-aligned) to avoid thundering herd.
	jitter := 0.25 * base * (2*rand.Float64() - 1)
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
