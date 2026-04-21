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

	"github.com/blueberrycongee/wuu/internal/providers"
)

const defaultTimeout = 120 * time.Second

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

// ClientConfig configures an OpenAI-compatible chat completions endpoint.
type ClientConfig struct {
	BaseURL      string
	APIKey       string
	Headers      map[string]string
	HTTPClient   *http.Client
	RetryConfig  *providers.RetryConfig
	StreamConfig *providers.StreamTransportConfig
}

// Client sends tool-enabled chat requests to OpenAI-compatible APIs.
type Client struct {
	baseURL              string
	apiKey               string
	headers              map[string]string
	httpClient           *http.Client
	retryConfig          providers.RetryConfig
	promptCacheKeyFormat promptCacheKeyFormat
	streamConfig         providers.StreamTransportConfig
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
	rc := providers.DefaultRetryConfig()
	if cfg.RetryConfig != nil {
		rc = *cfg.RetryConfig
	}
	rc = providers.NormalizeRetryConfig(rc)

	return &Client{
		baseURL:              strings.TrimRight(cfg.BaseURL, "/"),
		apiKey:               cfg.APIKey,
		headers:              cloneHeaders(cfg.Headers),
		httpClient:           hc,
		retryConfig:          rc,
		promptCacheKeyFormat: detectPromptCacheKeyFormat(cfg.BaseURL, cfg.Headers),
		streamConfig:         streamTransportConfig(cfg.StreamConfig),
	}, nil
}

// Chat performs one chat-completions round.
func (c *Client) Chat(ctx context.Context, req providers.ChatRequest) (providers.ChatResponse, error) {
	if strings.TrimSpace(req.Model) == "" {
		return providers.ChatResponse{}, errors.New("model is required")
	}
	if len(req.Messages) == 0 {
		return providers.ChatResponse{}, errors.New("messages is required")
	}

	payload := chatCompletionsRequest{
		Model:           req.Model,
		Messages:        make([]chatMessage, 0, len(req.Messages)),
		Temperature:     req.Temperature,
		MaxTokens:       req.MaxTokens,
		ReasoningEffort: req.Effort,
	}
	applyPromptCacheKey(&payload, req.CacheHint, c.promptCacheKeyFormat)

	req.Messages = providers.NormalizeMessages(req.Messages)
	for _, msg := range req.Messages {
		mapped := mapMessage(msg)
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
		payload.Tools = make([]toolDefinition, 0, len(req.Tools))
		for _, tool := range req.Tools {
			payload.Tools = append(payload.Tools, toolDefinition{
				Type: "function",
				Function: toolFunctionDefinition{
					Name:        tool.Name,
					Description: tool.Description,
					Parameters:  tool.InputSchema,
				},
			})
		}
	}

	body, err := marshalChatCompletionsRequest(payload)
	if err != nil {
		return providers.ChatResponse{}, fmt.Errorf("marshal request: %w", err)
	}

	httpResp, err := c.doChatCompletionsRequest(ctx, c.httpClient, body, false)
	if err != nil {
		return providers.ChatResponse{}, err
	}
	defer httpResp.Body.Close()

	respBody, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return providers.ChatResponse{}, fmt.Errorf("read response body: %w", err)
	}

	if httpResp.StatusCode < 200 || httpResp.StatusCode >= 300 {
		snippet := string(respBody)
		if len(snippet) > 400 {
			snippet = snippet[:400]
		}
		body := fmt.Sprintf("%s: %s", httpResp.Status, snippet)
		return providers.ChatResponse{}, &providers.HTTPError{
			StatusCode:      httpResp.StatusCode,
			Body:            body,
			RetryAfter:      providers.ParseRetryAfter(httpResp),
			ContextOverflow: providers.DetectContextOverflow(body),
		}
	}

	var parsed chatCompletionsResponse
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return providers.ChatResponse{}, fmt.Errorf("parse response JSON: %w", err)
	}
	if len(parsed.Choices) == 0 {
		return providers.ChatResponse{}, errors.New("provider returned no choices")
	}

	choice := parsed.Choices[0]
	message := choice.Message
	content, err := parseContent(message.Content)
	if err != nil {
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
		ReasoningContent: message.ReasoningContent,
		ToolCalls:        calls,
		StopReason:       strings.ToLower(choice.FinishReason),
	}
	// OpenAI signals output truncation with finish_reason="length".
	if resp.StopReason == "length" {
		resp.Truncated = true
	}
	resp.Usage = parsed.Usage.asTokenUsage()
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

	payload := chatCompletionsRequest{
		Model:           req.Model,
		Messages:        make([]chatMessage, 0, len(req.Messages)),
		Temperature:     req.Temperature,
		MaxTokens:       req.MaxTokens,
		Stream:          true,
		ReasoningEffort: req.Effort,
	}
	applyPromptCacheKey(&payload, req.CacheHint, c.promptCacheKeyFormat)
	req.Messages = providers.NormalizeMessages(req.Messages)
	for _, msg := range req.Messages {
		mapped := mapMessage(msg)
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
		payload.Tools = make([]toolDefinition, 0, len(req.Tools))
		for _, tool := range req.Tools {
			payload.Tools = append(payload.Tools, toolDefinition{
				Type: "function",
				Function: toolFunctionDefinition{
					Name:        tool.Name,
					Description: tool.Description,
					Parameters:  tool.InputSchema,
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
	// Use the single-attempt request — the stream runner's
	// runStreamWithReconnect handles retries with proper UI feedback.
	streamClient := newStreamingHTTPClient(c.httpClient, c.streamConfig)
	resp, err := c.doSingleChatCompletionsRequest(ctx, streamClient, body, true)
	if err != nil {
		return nil, err
	}

	ch := make(chan providers.StreamEvent, 64)
	go c.readSSE(resp, ch)
	return ch, nil
}

// doSingleChatCompletionsRequest sends one HTTP request without retry.
func (c *Client) doSingleChatCompletionsRequest(
	ctx context.Context,
	httpClient *http.Client,
	body []byte,
	acceptStream bool,
) (*http.Response, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
	httpReq.Header.Set("Content-Type", "application/json")
	if acceptStream {
		httpReq.Header.Set("Accept", "text/event-stream")
	}
	for k, v := range c.headers {
		httpReq.Header.Set(k, v)
	}

	resp, err := httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 400))
		_ = resp.Body.Close()
		body := fmt.Sprintf("%s: %s", resp.Status, string(snippet))
		return nil, &providers.HTTPError{
			StatusCode:      resp.StatusCode,
			Body:            body,
			RetryAfter:      providers.ParseRetryAfter(resp),
			ContextOverflow: providers.DetectContextOverflow(body),
		}
	}
	return resp, nil
}

// doChatCompletionsRequest sends an HTTP request with automatic retries.
// Used by the non-streaming Chat path.
func (c *Client) doChatCompletionsRequest(
	ctx context.Context,
	httpClient *http.Client,
	body []byte,
	acceptStream bool,
) (*http.Response, error) {
	var httpResp *http.Response
	err := providers.WithRetry(ctx, c.retryConfig, func() error {
		resp, err := c.doSingleChatCompletionsRequest(ctx, httpClient, body, acceptStream)
		if err != nil {
			return err
		}
		httpResp = resp
		return nil
	})
	if err != nil {
		return nil, err
	}
	return httpResp, nil
}

func (c *Client) readSSE(resp *http.Response, ch chan<- providers.StreamEvent) {
	defer close(ch)
	defer resp.Body.Close()

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
	)

	emitToolEnds := func() {
		for idx, pt := range pending {
			ch <- providers.StreamEvent{
				Type: providers.EventToolUseEnd,
				ToolCall: &providers.ToolCall{
					ID:        pt.id,
					Name:      pt.name,
					Arguments: pt.args.String(),
				},
			}
			delete(pending, idx)
		}
	}

	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		resetIdle()
		line := scanner.Text()
		providers.DebugLogf("SSE raw: %s", line)
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
				ch <- providers.StreamEvent{Type: providers.EventThinkingDone}
				thinkingDone = true
			}
			emitToolEnds()
			ch <- providers.StreamEvent{
				Type:       providers.EventDone,
				Usage:      lastUsage,
				StopReason: lastFinishReason,
				Truncated:  lastFinishReason == "length",
			}
			return
		}

		var chunk chatCompletionsChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			providers.DebugLogf("SSE parse error: %v, data: %s", err, data)
			ch <- providers.StreamEvent{Type: providers.EventError, Error: fmt.Errorf("parse chunk: %w", err)}
			return
		}

		providers.DebugLogf("SSE chunk: choices=%d, content=%q, tool_calls=%d",
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
		}

		if len(chunk.Choices) == 0 {
			continue
		}
		choice := chunk.Choices[0]

		if choice.Delta.ReasoningContent != "" {
			sawThinking = true
			ch <- providers.StreamEvent{
				Type:    providers.EventThinkingDelta,
				Content: choice.Delta.ReasoningContent,
			}
		}

		if choice.Delta.Content != "" {
			if sawThinking && !thinkingDone {
				ch <- providers.StreamEvent{Type: providers.EventThinkingDone}
				thinkingDone = true
			}
			ch <- providers.StreamEvent{
				Type:    providers.EventContentDelta,
				Content: choice.Delta.Content,
			}
		}

		for _, tc := range choice.Delta.ToolCalls {
			if sawThinking && !thinkingDone {
				ch <- providers.StreamEvent{Type: providers.EventThinkingDone}
				thinkingDone = true
			}
			pt, exists := pending[tc.Index]
			if !exists {
				pt = &pendingTool{id: tc.ID, name: tc.Function.Name}
				pending[tc.Index] = pt
				ch <- providers.StreamEvent{
					Type: providers.EventToolUseStart,
					ToolCall: &providers.ToolCall{
						ID:   tc.ID,
						Name: tc.Function.Name,
					},
				}
			} else {
				pt.args.WriteString(tc.Function.Arguments)
				ch <- providers.StreamEvent{
					Type:    providers.EventToolUseDelta,
					Content: tc.Function.Arguments,
				}
			}
		}

		if choice.FinishReason != nil {
			lastFinishReason = strings.ToLower(*choice.FinishReason)
			if lastFinishReason == "tool_calls" {
				emitToolEnds()
			}
		}
	}

	if err := scanner.Err(); err != nil {
		if idleFired.Load() {
			ch <- providers.StreamEvent{
				Type:  providers.EventError,
				Error: fmt.Errorf("stream idle timeout after %s: %w", idleTimeout, context.DeadlineExceeded),
			}
			return
		}
		ch <- providers.StreamEvent{Type: providers.EventError, Error: fmt.Errorf("read stream: %w", err)}
		return
	}
	// Scanner ended cleanly (e.g. body closed) but we never saw [DONE].
	// If the idle watchdog fired, surface it as a retryable timeout.
	if idleFired.Load() {
		ch <- providers.StreamEvent{
			Type:  providers.EventError,
			Error: fmt.Errorf("stream idle timeout after %s: %w", idleTimeout, context.DeadlineExceeded),
		}
		return
	}
	ch <- providers.StreamEvent{
		Type:  providers.EventError,
		Error: providers.NewIncompleteStreamError("stream closed before [DONE]"),
	}
}

func mapMessage(msg providers.ChatMessage) chatMessage {
	mapped := chatMessage{
		Role:             msg.Role,
		Name:             msg.Name,
		ToolCallID:       msg.ToolCallID,
		ReasoningContent: msg.ReasoningContent,
	}

	if len(msg.Images) > 0 && strings.EqualFold(msg.Role, "user") {
		parts := make([]chatContentPart, 0, len(msg.Images)+1)
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

// mergeContent combines two chatMessage Content values. Content can
// be a plain string or a []chatContentPart array; this normalizes
// both to text and concatenates with a newline separator.
func mergeContent(existing, incoming any) any {
	a := contentToString(existing)
	b := contentToString(incoming)
	if a == "" {
		return b
	}
	if b == "" {
		return a
	}
	return a + "\n" + b
}

func contentToString(v any) string {
	switch c := v.(type) {
	case string:
		return c
	case []chatContentPart:
		var parts []string
		for _, p := range c {
			if p.Text != "" {
				parts = append(parts, p.Text)
			}
		}
		return strings.Join(parts, "\n")
	default:
		return fmt.Sprintf("%v", v)
	}
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
		Model          string           `json:"model"`
		Messages       []chatMessage    `json:"messages"`
		Tools          []toolDefinition `json:"tools,omitempty"`
		ToolChoice     string           `json:"tool_choice,omitempty"`
		Temperature    float64          `json:"temperature,omitempty"`
		Stream         bool             `json:"stream,omitempty"`
		PromptCacheKey string           `json:"promptCacheKey,omitempty"`
	}

	base := requestJSON{
		Model:          payload.Model,
		Messages:       payload.Messages,
		Tools:          payload.Tools,
		ToolChoice:     payload.ToolChoice,
		Temperature:    payload.Temperature,
		Stream:         payload.Stream,
		PromptCacheKey: payload.PromptCacheKey,
	}
	if strings.TrimSpace(payload.AltCacheKey) == "" {
		return json.Marshal(base)
	}

	raw, err := json.Marshal(base)
	if err != nil {
		return nil, err
	}
	var object map[string]any
	if err := json.Unmarshal(raw, &object); err != nil {
		return nil, err
	}
	object["prompt_cache_key"] = payload.AltCacheKey
	return json.Marshal(object)
}

func applyPromptCacheKey(payload *chatCompletionsRequest, hint *providers.CacheHint, format promptCacheKeyFormat) {
	if payload == nil || hint == nil {
		return
	}
	key := strings.TrimSpace(hint.PromptCacheKey)
	if key == "" {
		return
	}
	switch format {
	case promptCacheKeySnake:
		payload.AltCacheKey = key
	default:
		payload.PromptCacheKey = key
	}
}

type promptCacheKeyFormat int

const (
	promptCacheKeyCamel promptCacheKeyFormat = iota
	promptCacheKeySnake
)

func detectPromptCacheKeyFormat(baseURL string, headers map[string]string) promptCacheKeyFormat {
	if promptCacheKeyHeaderPrefersSnake(headers) {
		return promptCacheKeySnake
	}
	host := strings.ToLower(strings.TrimSpace(baseURL))
	if strings.Contains(host, "openrouter.ai") {
		return promptCacheKeySnake
	}
	return promptCacheKeyCamel
}

func promptCacheKeyHeaderPrefersSnake(headers map[string]string) bool {
	for k, v := range headers {
		key := strings.ToLower(strings.TrimSpace(k))
		value := strings.ToLower(strings.TrimSpace(v))
		if key == "x-openrouter-provider" || key == "http-referer" || key == "x-title" {
			if value != "" {
				return true
			}
		}
	}
	return false
}

type chatCompletionsRequest struct {
	Model            string           `json:"model"`
	Messages         []chatMessage    `json:"messages"`
	Tools            []toolDefinition `json:"tools,omitempty"`
	ToolChoice       string           `json:"tool_choice,omitempty"`
	Temperature      float64          `json:"temperature,omitempty"`
	MaxTokens        int              `json:"max_tokens,omitempty"`
	Stream           bool             `json:"stream,omitempty"`
	PromptCacheKey   string           `json:"promptCacheKey,omitempty"`
	AltCacheKey      string           `json:"prompt_cache_key,omitempty"`
	ReasoningEffort  string           `json:"reasoning_effort,omitempty"`
}

type chatMessage struct {
	Role             string     `json:"role"`
	Content          any        `json:"content,omitempty"`
	Name             string     `json:"name,omitempty"`
	ToolCallID       string     `json:"tool_call_id,omitempty"`
	ReasoningContent string     `json:"reasoning_content,omitempty"`
	ToolCalls        []toolCall `json:"tool_calls,omitempty"`
}

type chatContentPart struct {
	Type     string        `json:"type"`
	Text     string        `json:"text,omitempty"`
	ImageURL *chatImageURL `json:"image_url,omitempty"`
}

type chatImageURL struct {
	URL string `json:"url"`
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
