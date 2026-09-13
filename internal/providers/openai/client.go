package openai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/blueberrycongee/wuu/internal/provideroptions"
	"github.com/blueberrycongee/wuu/internal/providers"
)

const defaultTimeout = 120 * time.Second

const (
	wireAPIChat      = "chat"
	wireAPIResponses = "responses"
)

func streamTransportConfig(cfg *providers.StreamTransportConfig) providers.StreamTransportConfig {
	return providers.ResolveStreamTransportConfig(cfg)
}

func streamIdleTimeout() time.Duration {
	return streamTransportConfig(nil).IdleTimeout
}

func streamConnectTimeout() time.Duration {
	return streamTransportConfig(nil).ConnectTimeout
}

func newStreamingHTTPClient(base *http.Client, cfg providers.StreamTransportConfig) *http.Client {
	return providers.BuildStreamingHTTPClient(base, cfg)
}

func normalizeWireAPI(value string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", wireAPIChat:
		return wireAPIChat, nil
	case wireAPIResponses:
		return wireAPIResponses, nil
	default:
		return "", fmt.Errorf("unsupported OpenAI wire API %q", value)
	}
}

func normalizeResponsesTransport(mode providers.StreamTransportMode) (providers.StreamTransportMode, error) {
	if mode == "" {
		return providers.StreamTransportSSE, nil
	}
	normalized, ok := providers.NormalizeStreamTransportMode(string(mode))
	if !ok || normalized == "" {
		return "", fmt.Errorf("unsupported responses stream transport %q", mode)
	}
	return normalized, nil
}

func responsesTransportUsesWebSocket(mode providers.StreamTransportMode) bool {
	return mode == providers.StreamTransportAuto ||
		mode == providers.StreamTransportWebSocket ||
		mode == providers.StreamTransportWebSocketCached
}

func responsesTransportUsesCachedContext(mode providers.StreamTransportMode) bool {
	return mode == providers.StreamTransportAuto ||
		mode == providers.StreamTransportWebSocketCached
}

func openAIToolSurfaceValidationTarget(baseURL, model string) providers.ToolSurfaceValidationTarget {
	target := providers.ToolSurfaceValidationTarget{
		ProviderKind: "openai-compatible",
		ProviderName: "openai",
		Model:        model,
	}
	if isFirstPartyOpenAIBaseURL(baseURL) {
		target.ReservedToolNames = []string{"browser"}
	}
	return target
}

func isFirstPartyOpenAIBaseURL(raw string) bool {
	baseURL := strings.TrimRight(strings.ToLower(strings.TrimSpace(raw)), "/")
	return baseURL == "https://api.openai.com" ||
		baseURL == "https://api.openai.com/v1" ||
		baseURL == "https://chatgpt.com/backend-api/codex"
}

// ClientConfig configures an OpenAI-compatible endpoint.
type ClientConfig struct {
	DisableVideoInput       bool
	BaseURL                 string
	WireAPI                 string
	APIKey                  string
	Headers                 map[string]string
	HTTPClient              *http.Client
	StreamConfig            *providers.StreamTransportConfig
	Coordinator             *providers.ProviderCoordinator
	ProviderScope           providers.ProviderScope
	ResponsesStore          *bool
	ResponsesTransport      providers.StreamTransportMode
	ResponsesWebSocketCache *ResponsesWebSocketCache
}

// Client sends tool-enabled chat requests to OpenAI-compatible APIs.
type Client struct {
	disableVideoInput  bool
	baseURL            string
	wireAPI            string
	apiKey             string
	headers            map[string]string
	httpClient         *http.Client
	reasoningFormat    reasoningEffortFormat
	streamConfig       providers.StreamTransportConfig
	coordinator        *providers.ProviderCoordinator
	providerScope      providers.ProviderScope
	responsesStore     *bool
	responsesTransport providers.StreamTransportMode
	responsesWSCache   *ResponsesWebSocketCache
}

// New creates an OpenAI-compatible client.
func New(cfg ClientConfig) (*Client, error) {
	if strings.TrimSpace(cfg.BaseURL) == "" {
		return nil, errors.New("base URL is required")
	}
	if strings.TrimSpace(cfg.APIKey) == "" {
		return nil, errors.New("API key is required")
	}

	hc := cfg.HTTPClient
	if hc == nil {
		hc = &http.Client{Timeout: defaultTimeout}
	}
	wireAPI, err := normalizeWireAPI(cfg.WireAPI)
	if err != nil {
		return nil, err
	}

	responsesTransport, err := normalizeResponsesTransport(cfg.ResponsesTransport)
	if err != nil {
		return nil, err
	}
	if cfg.ResponsesWebSocketCache == nil && responsesTransportUsesWebSocket(responsesTransport) {
		cfg.ResponsesWebSocketCache = NewResponsesWebSocketCache()
	}
	providerScope := cfg.ProviderScope
	if providerScope == "" && cfg.Coordinator != nil {
		providerScope = providers.NewProviderScope(cfg.BaseURL, cfg.APIKey, openAIOrganization(cfg.Headers))
	}

	return &Client{
		disableVideoInput:  cfg.DisableVideoInput,
		baseURL:            strings.TrimRight(cfg.BaseURL, "/"),
		wireAPI:            wireAPI,
		apiKey:             cfg.APIKey,
		headers:            cloneHeaders(cfg.Headers),
		httpClient:         hc,
		reasoningFormat:    detectReasoningEffortFormat(cfg.BaseURL),
		streamConfig:       streamTransportConfig(cfg.StreamConfig),
		coordinator:        cfg.Coordinator,
		providerScope:      providerScope,
		responsesStore:     cfg.ResponsesStore,
		responsesTransport: responsesTransport,
		responsesWSCache:   cfg.ResponsesWebSocketCache,
	}, nil
}

func openAIOrganization(headers map[string]string) string {
	for key, value := range headers {
		if strings.EqualFold(key, "OpenAI-Organization") {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

// Chat performs one chat-completions round.
func (c *Client) Chat(ctx context.Context, req providers.ChatRequest) (providers.ChatResponse, error) {
	if strings.TrimSpace(req.Model) == "" {
		return providers.ChatResponse{}, errors.New("model is required")
	}
	if len(req.Messages) == 0 {
		return providers.ChatResponse{}, errors.New("messages is required")
	}
	if err := providers.ValidateToolDefinitionsForProvider(openAIToolSurfaceValidationTarget(c.baseURL, req.Model), req.Tools); err != nil {
		return providers.ChatResponse{}, err
	}
	var err error
	req, err = providers.EnsureInferenceAttemptContext(ctx, req, providers.InferenceOperationAuxiliary, providers.InferenceProfileInteractive)
	if err != nil {
		return providers.ChatResponse{}, err
	}
	if c.wireAPI == wireAPIResponses {
		return c.responsesChat(ctx, req)
	}
	req, err = providers.PrepareVideoInput(req, !c.disableVideoInput && providers.VideoURLTransport(c.baseURL, req.ProviderOptions))
	if err != nil {
		return providers.ChatResponse{}, err
	}
	if autoToolChoiceOnly(req.ProviderOptions) {
		req.ForceToolName = ""
	}

	payload := chatCompletionsRequest{
		Model:           req.Model,
		Messages:        make([]chatMessage, 0, len(req.Messages)),
		Temperature:     effectiveTemperature(req),
		MaxTokens:       req.MaxTokens,
		ReasoningFormat: c.reasoningFormat,
		Options:         provideroptions.Clone(req.ProviderOptions),
	}
	applyReasoningEffort(&payload, req.Effort, c.reasoningFormat)
	applyPromptCacheKey(&payload, req.CacheHint, req.ProviderOptions)

	prepared, err := providers.PrepareMessagesForProviderRequestWithPolicy(req.Provider, req.Model, req.Messages, req.MediaInput)
	if err != nil {
		return providers.ChatResponse{}, err
	}
	req.Messages = providers.VideoMessagesForEndpoint(prepared, c.baseURL)
	for _, msg := range req.Messages {
		mapped := mapMessage(req.Model, msg)
		if mapped.Role != "tool" && mapped.ToolCallID == "" {
			if n := len(payload.Messages); n > 0 && payload.Messages[n-1].Role == mapped.Role && payload.Messages[n-1].ToolCallID == "" {
				payload.Messages[n-1].Content = mergeContent(payload.Messages[n-1].Content, mapped.Content)
				continue
			}
		}
		payload.Messages = append(payload.Messages, mapped)
	}
	if len(req.Tools) > 0 {
		payload.ToolChoice = "auto"
		if req.ForceToolName != "" {
			// Forced tool choice for mechanical closing turns: the OpenAI
			// wire form pins a specific function by name.
			payload.ToolChoice = map[string]any{
				"type":     "function",
				"function": map[string]any{"name": req.ForceToolName},
			}
		}
		payload.Tools = make([]toolDefinition, 0, len(req.Tools))
		for _, tool := range req.Tools {
			payload.Tools = append(payload.Tools, toolDefinition{
				Type: "function",
				Function: toolFunctionDefinition{
					Name:        tool.Name,
					Description: tool.Description,
					Parameters:  providers.ToolInputSchemaForModel(req.Model, tool.InputSchema),
				},
			})
		}
	}

	body, err := marshalChatCompletionsRequest(payload)
	if err != nil {
		return providers.ChatResponse{}, fmt.Errorf("marshal request: %w", err)
	}

	httpResp, lease, err := c.doSingleChatCompletionsRequest(ctx, c.httpClient, body, false, req.Attempt)
	if err != nil {
		return providers.ChatResponse{}, err
	}
	defer httpResp.Body.Close()
	defer lease.Release()

	respBody, err := io.ReadAll(httpResp.Body)
	if err != nil {
		err = fmt.Errorf("read response body: %w", err)
		failOpenAIResponseLease(lease, httpResp, err)
		return providers.ChatResponse{}, err
	}

	if httpResp.StatusCode < 200 || httpResp.StatusCode >= 300 {
		snippet := string(respBody)
		if len(snippet) > 400 {
			snippet = snippet[:400]
		}
		body := fmt.Sprintf("%s: %s", httpResp.Status, snippet)
		err := &providers.HTTPError{
			ProviderFamily:  "openai",
			StatusCode:      httpResp.StatusCode,
			Body:            body,
			RetryAfter:      providers.ParseRetryAfter(httpResp),
			ContextOverflow: providers.DetectContextOverflow(body),
		}
		lease.FailError(err)
		return providers.ChatResponse{}, err
	}

	var parsed chatCompletionsResponse
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		err = fmt.Errorf("parse response JSON: %w", err)
		lease.FailError(err)
		return providers.ChatResponse{}, err
	}
	if len(parsed.Choices) == 0 {
		err := errors.New("provider returned no choices")
		lease.FailError(err)
		return providers.ChatResponse{}, err
	}

	choice := parsed.Choices[0]
	message := choice.Message
	content, err := parseContent(message.Content)
	if err != nil {
		lease.FailError(err)
		return providers.ChatResponse{}, err
	}

	calls := make([]providers.ToolCall, 0, len(message.ToolCalls))
	for _, call := range message.ToolCalls {
		calls = append(calls, providers.ToolCall{
			ID:        call.ID,
			Name:      call.Function.Name,
			Arguments: call.Function.Arguments,
		})
	}

	resp := providers.ChatResponse{
		Content:          content,
		Phase:            providers.NormalizeMessagePhase(message.Phase),
		ReasoningContent: message.ReasoningContent,
		ToolCalls:        calls,
		StopReason:       strings.ToLower(choice.FinishReason),
	}
	// OpenAI signals output truncation with finish_reason="length".
	if resp.StopReason == "length" {
		resp.Truncated = true
	}
	resp.FinishReason = providers.NormalizeFinishReason(resp.StopReason, resp.Truncated, len(calls) > 0)
	resp.Usage = parsed.Usage.asTokenUsage()
	lease.SucceedWithUsage(resp.Usage)
	return resp, nil
}

// StreamChat opens an SSE stream and returns a channel of streaming events.
func (c *Client) StreamChat(ctx context.Context, req providers.ChatRequest) (<-chan providers.StreamEvent, error) {
	if strings.TrimSpace(req.Model) == "" {
		return nil, errors.New("model is required")
	}
	if len(req.Messages) == 0 {
		return nil, errors.New("messages is required")
	}
	if err := providers.ValidateToolDefinitionsForProvider(openAIToolSurfaceValidationTarget(c.baseURL, req.Model), req.Tools); err != nil {
		return nil, err
	}
	var err error
	req, err = providers.EnsureInferenceAttemptContext(ctx, req, providers.InferenceOperationAuxiliary, providers.InferenceProfileInteractive)
	if err != nil {
		return nil, err
	}
	if c.wireAPI == wireAPIResponses {
		return c.responsesStreamChat(ctx, req)
	}
	req, err = providers.PrepareVideoInput(req, !c.disableVideoInput && providers.VideoURLTransport(c.baseURL, req.ProviderOptions))
	if err != nil {
		return nil, err
	}
	if autoToolChoiceOnly(req.ProviderOptions) {
		req.ForceToolName = ""
	}

	payload := chatCompletionsRequest{
		Model:       req.Model,
		Messages:    make([]chatMessage, 0, len(req.Messages)),
		Temperature: effectiveTemperature(req),
		MaxTokens:   req.MaxTokens,
		Stream:      true,
		// Without include_usage many endpoints never report usage on streams;
		// each such submission then stays unknown-billable and the cumulative
		// per-workflow cap kills the turn after a handful of steps.
		StreamOptions:   &streamOptions{IncludeUsage: true},
		ReasoningFormat: c.reasoningFormat,
		Options:         provideroptions.Clone(req.ProviderOptions),
	}
	applyReasoningEffort(&payload, req.Effort, c.reasoningFormat)
	applyPromptCacheKey(&payload, req.CacheHint, req.ProviderOptions)
	prepared, err := providers.PrepareMessagesForProviderRequestWithPolicy(req.Provider, req.Model, req.Messages, req.MediaInput)
	if err != nil {
		return nil, err
	}
	req.Messages = providers.VideoMessagesForEndpoint(prepared, c.baseURL)
	for _, msg := range req.Messages {
		mapped := mapMessage(req.Model, msg)
		if mapped.Role != "tool" && mapped.ToolCallID == "" {
			if n := len(payload.Messages); n > 0 && payload.Messages[n-1].Role == mapped.Role && payload.Messages[n-1].ToolCallID == "" {
				payload.Messages[n-1].Content = mergeContent(payload.Messages[n-1].Content, mapped.Content)
				continue
			}
		}
		payload.Messages = append(payload.Messages, mapped)
	}
	if len(req.Tools) > 0 {
		payload.ToolChoice = "auto"
		if req.ForceToolName != "" {
			// Forced tool choice for mechanical closing turns: the OpenAI
			// wire form pins a specific function by name.
			payload.ToolChoice = map[string]any{
				"type":     "function",
				"function": map[string]any{"name": req.ForceToolName},
			}
		}
		payload.Tools = make([]toolDefinition, 0, len(req.Tools))
		for _, tool := range req.Tools {
			payload.Tools = append(payload.Tools, toolDefinition{
				Type: "function",
				Function: toolFunctionDefinition{
					Name:        tool.Name,
					Description: tool.Description,
					Parameters:  providers.ToolInputSchemaForModel(req.Model, tool.InputSchema),
				},
			})
		}
	}

	body, err := marshalChatCompletionsRequest(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	// Streaming turns can legitimately outlive the buffered request timeout.
	// Let the caller's ctx and the idle watchdog own cancellation instead.
	// Use the single-attempt request — ReliableStreamClient handles retries
	// with proper UI feedback at the caller layer.
	streamClient := newStreamingHTTPClient(c.httpClient, c.streamConfig)
	resp, lease, err := c.doSingleChatCompletionsRequest(ctx, streamClient, body, true, req.Attempt)
	if err != nil {
		return nil, err
	}

	ch := make(chan providers.StreamEvent, 64)
	go c.readSSE(ctx, resp, lease, ch)
	return ch, nil
}

// doSingleChatCompletionsRequest sends one HTTP request without retry.
func (c *Client) doSingleChatCompletionsRequest(
	ctx context.Context,
	httpClient *http.Client,
	body []byte,
	acceptStream bool,
	attempt providers.InferenceAttempt,
) (*http.Response, *providers.ProviderLease, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, nil, fmt.Errorf("build request: %w", err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
	httpReq.Header.Set("Content-Type", "application/json")
	if acceptStream {
		httpReq.Header.Set("Accept", "text/event-stream")
	}
	for k, v := range c.headers {
		httpReq.Header.Set(k, v)
	}

	mode := "unary"
	if acceptStream {
		mode = "stream"
	}
	lease, err := c.coordinator.AcquireForAttempt(ctx, c.providerScope, attempt)
	if err != nil {
		return nil, nil, err
	}
	if _, err := lease.RecordSubmission(attempt, providers.InferenceSubmissionMeta{
		Provider:     "openai",
		Protocol:     "chat_completions",
		Transport:    "http",
		Mode:         mode,
		RequestBytes: len(body),
	}); err != nil {
		lease.Release()
		return nil, nil, fmt.Errorf("journal chat completion submission: %w", err)
	}
	resp, err := httpClient.Do(httpReq)
	if err != nil {
		err = fmt.Errorf("request failed: %w", err)
		lease.FailError(err)
		return nil, nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 400))
		_ = resp.Body.Close()
		body := fmt.Sprintf("%s: %s", resp.Status, string(snippet))
		err := &providers.HTTPError{
			ProviderFamily:  "openai",
			StatusCode:      resp.StatusCode,
			Body:            body,
			RetryAfter:      providers.ParseRetryAfter(resp),
			ContextOverflow: providers.DetectContextOverflow(body),
		}
		lease.FailError(err)
		return nil, nil, err
	}
	return resp, lease, nil
}

func failOpenAIResponseLease(lease *providers.ProviderLease, resp *http.Response, err error) {
	if resp != nil && resp.Request != nil {
		if ctxErr := resp.Request.Context().Err(); ctxErr != nil {
			lease.FailError(ctxErr)
			return
		}
	}
	lease.FailError(err)
}

func (c *Client) readSSE(ctx context.Context, resp *http.Response, lease *providers.ProviderLease, ch chan<- providers.StreamEvent) {
	defer close(ch)
	defer resp.Body.Close()
	defer lease.Release()

	emit := providers.NewStreamEmitter(ctx, ch)

	// Idle watchdog: if no chunk arrives within streamIdleTimeout(),
	// close the body to abort the scanner. Wrap the surfaced error in
	// context.DeadlineExceeded so the retry classifier picks it up.
	idleTimeout := c.streamConfig.IdleTimeout
	var idleFired atomic.Bool
	idleTimer := time.AfterFunc(idleTimeout, func() {
		idleFired.Store(true)
		_ = resp.Body.Close()
	})
	defer idleTimer.Stop()
	resetIdle := func() { idleTimer.Reset(idleTimeout) }

	type pendingTool struct {
		id   string
		name string
		args strings.Builder
	}
	pending := make(map[int]*pendingTool)
	var (
		lastUsage        *providers.TokenUsage
		lastFinishReason string
		sawThinking      bool
		thinkingDone     bool
		sawToolCall      bool
		currentPhase     providers.MessagePhase
	)

	emitToolEnds := func() bool {
		for idx, pt := range pending {
			if !emit.Send(providers.StreamEvent{
				Type: providers.EventToolUseEnd,
				ToolCall: &providers.ToolCall{
					ID:        pt.id,
					Name:      pt.name,
					Arguments: pt.args.String(),
				},
			}) {
				return false
			}
			delete(pending, idx)
		}
		return true
	}

	scanner := bufio.NewScanner(resp.Body)
	// Match the Responses SSE reader: one oversized data: line must not turn
	// into a non-retryable bufio.ErrTooLong failure.
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		resetIdle()
		line := scanner.Text()
		providers.DebugLogfWire("SSE raw: %s", line)
		if line == "" || strings.HasPrefix(line, "event:") {
			continue
		}
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			providers.DebugLogf("SSE [DONE]")
			if sawThinking && !thinkingDone {
				if !emit.Send(providers.StreamEvent{Type: providers.EventThinkingDone}) {
					return
				}
				thinkingDone = true
			}
			if !emitToolEnds() {
				return
			}
			truncated := lastFinishReason == "length"
			lease.SucceedWithUsage(lastUsage)
			emit.Send(providers.StreamEvent{
				Type:         providers.EventDone,
				Usage:        lastUsage,
				StopReason:   lastFinishReason,
				FinishReason: providers.NormalizeFinishReason(lastFinishReason, truncated, sawToolCall),
				Truncated:    truncated,
			})
			return
		}

		var chunk chatCompletionsChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			providers.DebugLogf("SSE parse error: %v", err)
			providers.DebugLogfWire("SSE parse error data: %s", data)
			err = fmt.Errorf("parse chunk: %w", err)
			failOpenAIResponseLease(lease, resp, err)
			emit.Send(providers.StreamEvent{Type: providers.EventError, Error: err})
			return
		}

		providers.DebugLogfWire("SSE chunk: choices=%d, content=%q, tool_calls=%d",
			len(chunk.Choices),
			func() string {
				if len(chunk.Choices) > 0 {
					return chunk.Choices[0].Delta.Content
				}
				return ""
			}(),
			func() int {
				if len(chunk.Choices) > 0 {
					return len(chunk.Choices[0].Delta.ToolCalls)
				}
				return 0
			}(),
		)

		if chunk.Usage != nil {
			lastUsage = chunk.Usage.asTokenUsage()
			if !emit.Send(providers.StreamEvent{Type: providers.EventUsage, Usage: lastUsage}) {
				return
			}
		}

		if len(chunk.Choices) == 0 {
			continue
		}
		choice := chunk.Choices[0]

		if choice.Delta.ReasoningContent != "" {
			sawThinking = true
			if !emit.Send(providers.StreamEvent{
				Type:    providers.EventThinkingDelta,
				Content: choice.Delta.ReasoningContent,
			}) {
				return
			}
		}

		deltaPhase := providers.NormalizeMessagePhase(choice.Delta.Phase)
		if deltaPhase != "" {
			currentPhase = deltaPhase
			if choice.Delta.Content == "" {
				if !emit.Send(providers.StreamEvent{
					Type:  providers.EventContentDelta,
					Phase: currentPhase,
				}) {
					return
				}
			}
		}

		if choice.Delta.Content != "" {
			if sawThinking && !thinkingDone {
				if !emit.Send(providers.StreamEvent{Type: providers.EventThinkingDone}) {
					return
				}
				thinkingDone = true
			}
			if !emit.Send(providers.StreamEvent{
				Type:    providers.EventContentDelta,
				Content: choice.Delta.Content,
				Phase:   currentPhase,
			}) {
				return
			}
		}

		for _, tc := range choice.Delta.ToolCalls {
			sawToolCall = true
			if sawThinking && !thinkingDone {
				if !emit.Send(providers.StreamEvent{Type: providers.EventThinkingDone}) {
					return
				}
				thinkingDone = true
			}
			pt, exists := pending[tc.Index]
			if !exists {
				pt = &pendingTool{id: tc.ID, name: tc.Function.Name}
				pending[tc.Index] = pt
				if !emit.Send(providers.StreamEvent{
					Type: providers.EventToolUseStart,
					ToolCall: &providers.ToolCall{
						ID:   tc.ID,
						Name: tc.Function.Name,
					},
				}) {
					return
				}
			}
			if tc.Function.Arguments != "" {
				pt.args.WriteString(tc.Function.Arguments)
				if !emit.Send(providers.StreamEvent{
					Type:    providers.EventToolUseDelta,
					Content: tc.Function.Arguments,
				}) {
					return
				}
			}
		}

		if choice.FinishReason != nil {
			lastFinishReason = strings.ToLower(*choice.FinishReason)
			if lastFinishReason == "tool_calls" {
				if !emitToolEnds() {
					return
				}
			}
		}
	}

	if err := scanner.Err(); err != nil {
		if idleFired.Load() {
			err := fmt.Errorf("stream idle timeout after %s: %w", idleTimeout, context.DeadlineExceeded)
			failOpenAIResponseLease(lease, resp, err)
			emit.Send(providers.StreamEvent{
				Type:  providers.EventError,
				Error: err,
			})
			return
		}
		err = fmt.Errorf("read stream: %w", err)
		failOpenAIResponseLease(lease, resp, err)
		emit.Send(providers.StreamEvent{Type: providers.EventError, Error: err})
		return
	}
	// Scanner ended cleanly (e.g. body closed) but we never saw [DONE].
	// If the idle watchdog fired, surface it as a retryable timeout.
	if idleFired.Load() {
		err := fmt.Errorf("stream idle timeout after %s: %w", idleTimeout, context.DeadlineExceeded)
		failOpenAIResponseLease(lease, resp, err)
		emit.Send(providers.StreamEvent{
			Type:  providers.EventError,
			Error: err,
		})
		return
	}
	// Some OpenAI-compatible endpoints close the SSE body immediately after
	// an explicit terminal finish_reason instead of sending a trailing
	// data: [DONE] sentinel. The finish reason is sufficient proof that the
	// logical response completed; a clean EOF without one remains incomplete.
	if lastFinishReason != "" {
		if sawThinking && !thinkingDone {
			if !emit.Send(providers.StreamEvent{Type: providers.EventThinkingDone}) {
				return
			}
		}
		if !emitToolEnds() {
			return
		}
		truncated := lastFinishReason == "length"
		lease.SucceedWithUsage(lastUsage)
		emit.Send(providers.StreamEvent{
			Type:         providers.EventDone,
			Usage:        lastUsage,
			StopReason:   lastFinishReason,
			FinishReason: providers.NormalizeFinishReason(lastFinishReason, truncated, sawToolCall),
			Truncated:    truncated,
		})
		return
	}
	err := providers.NewIncompleteStreamError("stream closed before [DONE]")
	failOpenAIResponseLease(lease, resp, err)
	emit.Send(providers.StreamEvent{
		Type:  providers.EventError,
		Error: err,
	})
}

func mapMessage(model string, msg providers.ChatMessage) chatMessage {
	mapped := chatMessage{
		Role:       msg.Role,
		Name:       msg.Name,
		ToolCallID: msg.ToolCallID,
	}
	if strings.EqualFold(msg.Role, "assistant") && (msg.ReasoningContent != "" || providers.IsDeepSeekModel(model)) {
		reasoningContent := msg.ReasoningContent
		mapped.ReasoningContent = &reasoningContent
	}

	if (len(msg.Images) > 0 || len(msg.Files) > 0) && strings.EqualFold(msg.Role, "user") {
		parts := make([]chatContentPart, 0, len(msg.Images)+len(msg.Files)+1)
		if strings.TrimSpace(msg.Content) != "" {
			parts = append(parts, chatContentPart{
				Type: "text",
				Text: msg.Content,
			})
		}
		for _, image := range msg.Images {
			data := strings.TrimSpace(image.Data)
			if data == "" {
				continue
			}
			mediaType := strings.TrimSpace(image.MediaType)
			if mediaType == "" {
				mediaType = "image/png"
			}
			parts = append(parts, chatContentPart{
				Type: "image_url",
				ImageURL: &chatImageURL{
					URL: "data:" + mediaType + ";base64," + data,
				},
			})
		}
		for _, file := range msg.Files {
			data := strings.TrimSpace(file.Data)
			if data == "" {
				continue
			}
			mediaType := strings.TrimSpace(file.MediaType)
			if mediaType == "" {
				mediaType = "application/octet-stream"
			}
			if providers.IsVideoMediaType(mediaType) {
				parts = append(parts, chatContentPart{Type: "video_url", VideoURL: &chatImageURL{URL: "data:" + mediaType + ";base64," + data}})
				continue
			}
			filename := strings.TrimSpace(file.Filename)
			if filename == "" {
				filename = "attachment"
			}
			parts = append(parts, chatContentPart{
				Type: "file",
				File: &chatFilePart{
					Filename: filename,
					FileData: "data:" + mediaType + ";base64," + data,
				},
			})
		}
		mapped.Content = parts
	} else {
		mapped.Content = msg.Content
	}

	if len(msg.ToolCalls) > 0 {
		mapped.ToolCalls = make([]toolCall, 0, len(msg.ToolCalls))
		for _, call := range msg.ToolCalls {
			mapped.ToolCalls = append(mapped.ToolCalls, toolCall{
				ID:   call.ID,
				Type: "function",
				Function: toolFunctionCall{
					Name:      call.Name,
					Arguments: call.Arguments,
				},
			})
		}
	}
	return mapped
}

// mergeContent combines two chatMessage Content values. Adjacent same-role
// messages are collapsed because some Chat Completions endpoints reject
// consecutive user turns. Text-only payloads stay strings. If either side
// already has image_url or file parts, keep a part list so those media
// parts are not flattened away.
func mergeContent(existing, incoming any) any {
	left := contentParts(existing)
	right := contentParts(incoming)
	if len(left) == 0 {
		return incoming
	}
	if len(right) == 0 {
		return existing
	}
	if !contentHasMedia(left) && !contentHasMedia(right) {
		a := contentText(left)
		b := contentText(right)
		if a == "" {
			return b
		}
		if b == "" {
			return a
		}
		return a + "\n" + b
	}
	return append(left, right...)
}

func contentParts(v any) []chatContentPart {
	switch c := v.(type) {
	case nil:
		return nil
	case string:
		if c == "" {
			return nil
		}
		return []chatContentPart{{Type: "text", Text: c}}
	case []chatContentPart:
		out := make([]chatContentPart, 0, len(c))
		for _, part := range c {
			if part.Type == "" && part.Text == "" && part.ImageURL == nil && part.File == nil {
				continue
			}
			if part.Type == "text" && part.Text == "" {
				continue
			}
			out = append(out, part)
		}
		return out
	default:
		text := fmt.Sprintf("%v", v)
		if text == "" {
			return nil
		}
		return []chatContentPart{{Type: "text", Text: text}}
	}
}

func contentHasMedia(parts []chatContentPart) bool {
	for _, part := range parts {
		if part.ImageURL != nil || part.File != nil || part.Type == "image_url" || part.Type == "file" {
			return true
		}
	}
	return false
}

func contentText(parts []chatContentPart) string {
	texts := make([]string, 0, len(parts))
	for _, part := range parts {
		if part.Text != "" {
			texts = append(texts, part.Text)
		}
	}
	return strings.Join(texts, "\n")
}

func parseContent(raw json.RawMessage) (string, error) {
	if len(raw) == 0 {
		return "", nil
	}

	var asString string
	if err := json.Unmarshal(raw, &asString); err == nil {
		return asString, nil
	}

	var asParts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &asParts); err == nil {
		parts := make([]string, 0, len(asParts))
		for _, part := range asParts {
			if part.Text != "" {
				parts = append(parts, part.Text)
			}
		}
		return strings.Join(parts, "\n"), nil
	}

	return "", fmt.Errorf("unsupported message content: %s", string(raw))
}

func cloneHeaders(input map[string]string) map[string]string {
	if len(input) == 0 {
		return nil
	}
	out := make(map[string]string, len(input))
	for k, v := range input {
		out[k] = v
	}
	return out
}

func marshalChatCompletionsRequest(payload chatCompletionsRequest) ([]byte, error) {
	type requestJSON struct {
		Model           string           `json:"model"`
		Messages        []chatMessage    `json:"messages"`
		Tools           []toolDefinition `json:"tools,omitempty"`
		ToolChoice      any              `json:"tool_choice,omitempty"`
		Temperature     float64          `json:"temperature,omitempty"`
		MaxTokens       int              `json:"max_tokens,omitempty"`
		Stream          bool             `json:"stream,omitempty"`
		PromptCacheKey  string           `json:"prompt_cache_key,omitempty"`
		ReasoningEffort string           `json:"reasoning_effort,omitempty"`
		Reasoning       *reasoningConfig `json:"reasoning,omitempty"`
	}

	base := requestJSON{
		Model:           payload.Model,
		Messages:        payload.Messages,
		Tools:           payload.Tools,
		ToolChoice:      payload.ToolChoice,
		Temperature:     payload.Temperature,
		MaxTokens:       payload.MaxTokens,
		Stream:          payload.Stream,
		PromptCacheKey:  payload.PromptCacheKey,
		ReasoningEffort: payload.ReasoningEffort,
		Reasoning:       payload.Reasoning,
	}
	// Always serialize through the sorted-key map form. Splitting between
	// struct-order (no options) and map-order (options present) bodies would
	// flip the key order of the entire request whenever options appear or
	// disappear, and automatic prefix caching matches raw bytes.
	raw, err := json.Marshal(base)
	if err != nil {
		return nil, err
	}
	var object map[string]any
	if err := json.Unmarshal(raw, &object); err != nil {
		return nil, err
	}
	mergeChatProviderOptions(object, payload.Options, payload.ReasoningFormat)
	return json.Marshal(object)
}

func applyPromptCacheKey(payload *chatCompletionsRequest, hint *providers.CacheHint, options map[string]any) {
	if payload == nil || hint == nil {
		return
	}
	supported, _ := options["promptCacheKeySupported"].(bool)
	if !supported {
		return
	}
	key := clampPromptCacheKey(hint.PromptCacheKey)
	if key == "" {
		return
	}
	payload.PromptCacheKey = key
}

func applyReasoningEffort(payload *chatCompletionsRequest, effort string, format reasoningEffortFormat) {
	effort = strings.TrimSpace(effort)
	if payload == nil || effort == "" {
		return
	}
	switch format {
	case reasoningEffortNested:
		payload.Reasoning = &reasoningConfig{Effort: effort}
	default:
		payload.ReasoningEffort = effort
	}
}

func mergeChatProviderOptions(object map[string]any, options map[string]any, format reasoningEffortFormat) {
	if len(object) == 0 || len(options) == 0 {
		return
	}
	for key, value := range options {
		if key == "video_input" {
			continue
		}
		switch key {
		case "reasoningEffort":
			if effort, ok := value.(string); ok && strings.TrimSpace(effort) != "" {
				effort = strings.TrimSpace(effort)
				if format == reasoningEffortNested {
					reasoning, ok := object["reasoning"].(map[string]any)
					if !ok {
						reasoning = make(map[string]any)
						object["reasoning"] = reasoning
					}
					reasoning["effort"] = effort
				} else {
					object["reasoning_effort"] = effort
				}
			}
		case "reasoningSummary":
			object["reasoning_summary"] = value
		case "textVerbosity":
			object["verbosity"] = value
		case "serviceTier":
			object["service_tier"] = value
		case "maxOutputTokens":
			object["max_tokens"] = value
		case "promptCacheKey":
			object["prompt_cache_key"] = value
		case "temperature":
			if _, exists := object["temperature"]; !exists {
				object["temperature"] = value
			}
		case "topP":
			object["top_p"] = value
		case "topK":
			object["top_k"] = value
		case "tool_stream":
			stream, _ := object["stream"].(bool)
			tools, _ := object["tools"].([]any)
			if stream && len(tools) > 0 {
				object["tool_stream"] = value
			}
		case "include":
			// OpenAI Responses uses include for encrypted reasoning replay.
			// Chat Completions endpoints commonly reject it as an unknown field.
			continue
		default:
			if chatProviderOptionIsAISDKOnly(key) {
				continue
			}
			mergeProviderOptionValue(object, key, value)
		}
	}
}

func chatProviderOptionIsAISDKOnly(key string) bool {
	switch key {
	case "toolStreaming", "toolChoiceAutoOnly", "omitStore", "omitPromptCacheKey", "thinkingConfig", "reasoningConfig", "modelParams", "gateway",
		"temperatureSupported", "temperature_supported", "promptCacheKeySupported":
		return true
	default:
		return false
	}
}

func autoToolChoiceOnly(options map[string]any) bool {
	value, _ := options["toolChoiceAutoOnly"].(bool)
	return value
}

func providerOptionEnabled(options map[string]any, key string) bool {
	value, _ := options[key].(bool)
	return value
}

func mergeProviderOptionValue(object map[string]any, key string, value any) {
	provideroptions.MergeValue(object, key, value)
}

type reasoningEffortFormat int

const (
	reasoningEffortOpenAI reasoningEffortFormat = iota
	reasoningEffortNested
)

func detectReasoningEffortFormat(baseURL string) reasoningEffortFormat {
	host := strings.ToLower(strings.TrimSpace(baseURL))
	if strings.Contains(host, "openrouter.ai") {
		return reasoningEffortNested
	}
	return reasoningEffortOpenAI
}

type reasoningConfig struct {
	Effort string `json:"effort,omitempty"`
}

type chatCompletionsRequest struct {
	Model           string                `json:"model"`
	Messages        []chatMessage         `json:"messages"`
	Tools           []toolDefinition      `json:"tools,omitempty"`
	ToolChoice      any                   `json:"tool_choice,omitempty"`
	Temperature     float64               `json:"temperature,omitempty"`
	MaxTokens       int                   `json:"max_tokens,omitempty"`
	Stream          bool                  `json:"stream,omitempty"`
	StreamOptions   *streamOptions        `json:"stream_options,omitempty"`
	PromptCacheKey  string                `json:"prompt_cache_key,omitempty"`
	ReasoningEffort string                `json:"reasoning_effort,omitempty"`
	Reasoning       *reasoningConfig      `json:"reasoning,omitempty"`
	ReasoningFormat reasoningEffortFormat `json:"-"`
	Options         map[string]any        `json:"-"`
}

type streamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

type chatMessage struct {
	Role             string     `json:"role"`
	Content          any        `json:"content,omitempty"`
	Name             string     `json:"name,omitempty"`
	ToolCallID       string     `json:"tool_call_id,omitempty"`
	ReasoningContent *string    `json:"reasoning_content,omitempty"`
	ToolCalls        []toolCall `json:"tool_calls,omitempty"`
}

type chatContentPart struct {
	VideoURL *chatImageURL `json:"video_url,omitempty"`
	Type     string        `json:"type"`
	Text     string        `json:"text,omitempty"`
	ImageURL *chatImageURL `json:"image_url,omitempty"`
	File     *chatFilePart `json:"file,omitempty"`
}

type chatImageURL struct {
	URL string `json:"url"`
}

type chatFilePart struct {
	Filename string `json:"filename,omitempty"`
	FileData string `json:"file_data,omitempty"`
}

type toolDefinition struct {
	Type     string                 `json:"type"`
	Function toolFunctionDefinition `json:"function"`
}

type toolFunctionDefinition struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters"`
}

type toolCall struct {
	ID       string           `json:"id"`
	Type     string           `json:"type"`
	Function toolFunctionCall `json:"function"`
}

type toolFunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type chatCompletionsResponse struct {
	Choices []chatChoice `json:"choices"`
	Usage   *chunkUsage  `json:"usage,omitempty"`
}

type chatChoice struct {
	Message      chatResponseMessage `json:"message"`
	FinishReason string              `json:"finish_reason,omitempty"`
}

type chatResponseMessage struct {
	Content          json.RawMessage `json:"content"`
	Phase            string          `json:"phase,omitempty"`
	ReasoningContent string          `json:"reasoning_content,omitempty"`
	ToolCalls        []toolCall      `json:"tool_calls"`
}

type chatCompletionsChunk struct {
	Choices []chatChunkChoice `json:"choices"`
	Usage   *chunkUsage       `json:"usage,omitempty"`
}

type chatChunkChoice struct {
	Delta        chatChunkDelta `json:"delta"`
	FinishReason *string        `json:"finish_reason"`
}

type chatChunkDelta struct {
	Content          string          `json:"content,omitempty"`
	Phase            string          `json:"phase,omitempty"`
	ReasoningContent string          `json:"reasoning_content,omitempty"`
	ToolCalls        []toolCallDelta `json:"tool_calls,omitempty"`
}

type toolCallDelta struct {
	Index    int               `json:"index"`
	ID       string            `json:"id,omitempty"`
	Type     string            `json:"type,omitempty"`
	Function toolFunctionDelta `json:"function,omitempty"`
}

type toolFunctionDelta struct {
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
}

type chunkUsage struct {
	PromptTokens        int `json:"prompt_tokens"`
	CompletionTokens    int `json:"completion_tokens"`
	PromptTokensDetails *struct {
		// CachedTokens is the subset of PromptTokens served from
		// OpenAI's prompt cache. Returned by gpt-4o and later.
		CachedTokens int `json:"cached_tokens,omitempty"`
	} `json:"prompt_tokens_details,omitempty"`
}

// asTokenUsage converts the OpenAI usage shape to the provider-
// agnostic providers.TokenUsage. Cached prompt tokens are split out
// of PromptTokens so wuu's auto-compact accounts for them correctly.
func (u *chunkUsage) asTokenUsage() *providers.TokenUsage {
	if u == nil {
		return nil
	}
	cached := 0
	if u.PromptTokensDetails != nil {
		cached = u.PromptTokensDetails.CachedTokens
	}
	// OpenAI's prompt_tokens already includes cached tokens, so the
	// "fresh input" portion is the difference.
	freshInput := u.PromptTokens - cached
	if freshInput < 0 {
		freshInput = 0
	}
	return &providers.TokenUsage{
		InputTokens:     freshInput,
		OutputTokens:    u.CompletionTokens,
		CacheReadTokens: cached,
	}
}

// effectiveTemperature drops the sampling temperature for models that reject
// the field (per-model config "temperature": false arrives as the
// temperatureSupported provider option via modelvariant). Zero is omitted on
// the wire by omitempty, matching the "do not send" semantics.
func effectiveTemperature(req providers.ChatRequest) float64 {
	for _, key := range []string{"temperatureSupported", "temperature_supported"} {
		value, exists := req.ProviderOptions[key]
		if !exists {
			continue
		}
		switch v := value.(type) {
		case bool:
			if !v {
				return 0
			}
		case string:
			switch strings.ToLower(strings.TrimSpace(v)) {
			case "false", "0", "no", "off":
				return 0
			}
		}
	}
	return req.Temperature
}
