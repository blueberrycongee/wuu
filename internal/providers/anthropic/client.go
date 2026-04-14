package anthropic

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

func streamTransportConfig(cfg *providers.StreamTransportConfig) providers.StreamTransportConfig {
	return providers.ResolveStreamTransportConfig(cfg)
}

func streamIdleTimeout() time.Duration {
	return streamTransportConfig(nil).IdleTimeout
}

func streamConnectTimeout() time.Duration {
	return streamTransportConfig(nil).ConnectTimeout
}

const (
	defaultTimeout          = 120 * time.Second
	defaultAnthropicVersion = "2023-06-01"
	defaultMaxTokens        = 4096
)

// ClientConfig configures an Anthropic messages endpoint.
type ClientConfig struct {
	BaseURL      string
	APIKey       string
	AuthToken    string // Bearer token (ANTHROPIC_AUTH_TOKEN). Used instead of APIKey when set.
	Headers      map[string]string
	HTTPClient   *http.Client
	MaxTokens    int
	RetryConfig  *providers.RetryConfig
	StreamConfig *providers.StreamTransportConfig
}

// Client sends tool-enabled chat requests to Anthropic APIs.
type Client struct {
	baseURL      string
	apiKey       string
	authToken    string
	headers      map[string]string
	httpClient   *http.Client
	maxTokens    int
	retryConfig  providers.RetryConfig
	streamConfig providers.StreamTransportConfig
}

// New creates an Anthropic client.
func New(cfg ClientConfig) (*Client, error) {
	if strings.TrimSpace(cfg.BaseURL) == "" {
		return nil, errors.New("base URL is required")
	}
	if strings.TrimSpace(cfg.APIKey) == "" && strings.TrimSpace(cfg.AuthToken) == "" {
		return nil, errors.New("either API key or auth token is required")
	}
	hc := cfg.HTTPClient
	if hc == nil {
		hc = &http.Client{Timeout: defaultTimeout}
	}
	maxTokens := cfg.MaxTokens
	if maxTokens <= 0 {
		maxTokens = defaultMaxTokens
	}
	rc := providers.DefaultRetryConfig()
	if cfg.RetryConfig != nil {
		rc = *cfg.RetryConfig
	}
	rc = providers.NormalizeRetryConfig(rc)

	return &Client{
		baseURL:      strings.TrimRight(cfg.BaseURL, "/"),
		apiKey:       cfg.APIKey,
		authToken:    cfg.AuthToken,
		headers:      cloneHeaders(cfg.Headers),
		httpClient:   hc,
		maxTokens:    maxTokens,
		retryConfig:  rc,
		streamConfig: streamTransportConfig(cfg.StreamConfig),
	}, nil
}

// Chat performs one Anthropic messages round.
func (c *Client) Chat(ctx context.Context, req providers.ChatRequest) (providers.ChatResponse, error) {
	if strings.TrimSpace(req.Model) == "" {
		return providers.ChatResponse{}, errors.New("model is required")
	}
	if len(req.Messages) == 0 {
		return providers.ChatResponse{}, errors.New("messages is required")
	}

	payload, err := buildAnthropicRequest(req, c.maxTokens, false)
	if err != nil {
		return providers.ChatResponse{}, err
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return providers.ChatResponse{}, fmt.Errorf("marshal request: %w", err)
	}

	httpResp, err := c.doMessagesRequest(ctx, c.httpClient, body)
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

	var parsed anthropicResponse
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return providers.ChatResponse{}, fmt.Errorf("parse response JSON: %w", err)
	}
	if len(parsed.Content) == 0 {
		return providers.ChatResponse{}, errors.New("provider returned empty content")
	}

	var textParts []string
	toolCalls := make([]providers.ToolCall, 0, 2)
	for _, block := range parsed.Content {
		switch block.Type {
		case "text":
			if strings.TrimSpace(block.Text) != "" {
				textParts = append(textParts, block.Text)
			}
		case "tool_use":
			args, err := json.Marshal(block.Input)
			if err != nil {
				return providers.ChatResponse{}, fmt.Errorf("marshal tool_use input: %w", err)
			}
			toolCalls = append(toolCalls, providers.ToolCall{
				ID:        block.ID,
				Name:      block.Name,
				Arguments: string(args),
			})
		}
	}

	resp := providers.ChatResponse{
		Content:    strings.TrimSpace(strings.Join(textParts, "\n")),
		ToolCalls:  toolCalls,
		StopReason: strings.ToLower(parsed.StopReason),
	}
	if resp.StopReason == "max_tokens" {
		resp.Truncated = true
	}
	if parsed.Usage != nil {
		resp.Usage = &providers.TokenUsage{
			InputTokens:         parsed.Usage.InputTokens,
			OutputTokens:        parsed.Usage.OutputTokens,
			CacheCreationTokens: parsed.Usage.CacheCreationTokens,
			CacheReadTokens:     parsed.Usage.CacheReadTokens,
		}
	}
	return resp, nil
}

// StreamChat opens an SSE stream to the Anthropic messages endpoint and
// returns a channel of StreamEvent values. The channel is closed when the
// stream ends or an error occurs.
func (c *Client) StreamChat(ctx context.Context, req providers.ChatRequest) (<-chan providers.StreamEvent, error) {
	if strings.TrimSpace(req.Model) == "" {
		return nil, errors.New("model is required")
	}
	if len(req.Messages) == 0 {
		return nil, errors.New("messages is required")
	}

	payload, err := buildAnthropicRequest(req, c.maxTokens, true)
	if err != nil {
		return nil, err
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	sseClient := providers.BuildStreamingHTTPClient(c.httpClient, c.streamConfig)
	// Use the single-attempt request — the stream runner's
	// runStreamWithReconnect handles retries with proper UI feedback.
	resp, err := c.doSingleMessagesRequest(ctx, sseClient, body)
	if err != nil {
		return nil, err
	}

	ch := make(chan providers.StreamEvent, 64)
	go c.readSSEStream(resp, ch)
	return ch, nil
}

func buildAnthropicRequest(req providers.ChatRequest, maxTokens int, stream bool) (anthropicRequest, error) {
	payload := anthropicRequest{
		Model:     req.Model,
		MaxTokens: maxTokens,
		Messages:  make([]anthropicMessage, 0, len(req.Messages)),
		Stream:    stream,
	}
	if req.Temperature > 0 {
		t := req.Temperature
		payload.Temperature = &t
	}

	systemTexts := make([]string, 0, 1)
	for _, msg := range req.Messages {
		if strings.EqualFold(msg.Role, "system") {
			systemTexts = append(systemTexts, msg.Content)
			continue
		}

		mapped, err := mapMessage(msg)
		if err != nil {
			return anthropicRequest{}, err
		}
		payload.Messages = append(payload.Messages, mapped)
	}
	payload.System = buildAnthropicSystem(systemTexts, req.CacheHint)
	applyAnthropicCacheHint(&payload, req.CacheHint)

	if len(req.Tools) > 0 {
		payload.Tools = make([]anthropicTool, 0, len(req.Tools))
		for _, tool := range req.Tools {
			payload.Tools = append(payload.Tools, anthropicTool{
				Name:        tool.Name,
				Description: tool.Description,
				InputSchema: tool.InputSchema,
			})
		}
	}
	return payload, nil
}

func buildAnthropicSystem(systemTexts []string, hint *providers.CacheHint) any {
	if len(systemTexts) == 0 {
		return nil
	}
	joined := strings.Join(systemTexts, "\n")
	if !shouldCacheAnthropicSystem(hint) {
		return joined
	}
	return []anthropicSystemBlock{{
		Type:         "text",
		Text:         joined,
		CacheControl: ephemeralCacheControl(),
	}}
}

func shouldCacheAnthropicSystem(hint *providers.CacheHint) bool {
	return hint != nil && hint.StableSystem
}

func applyAnthropicCacheHint(payload *anthropicRequest, hint *providers.CacheHint) {
	if payload == nil || hint == nil || hint.StablePrefixMessages <= 0 {
		return
	}
	stable := hint.StablePrefixMessages
	if stable > len(payload.Messages) {
		stable = len(payload.Messages)
	}
	if stable == 0 {
		return
	}

	if hint.HasCompactSummary && markAnthropicMessageForCache(&payload.Messages[0]) {
		return
	}
	for i := stable - 1; i >= 0; i-- {
		if markAnthropicMessageForCache(&payload.Messages[i]) {
			return
		}
	}
}

func markAnthropicMessageForCache(msg *anthropicMessage) bool {
	if msg == nil {
		return false
	}
	for i := len(msg.Content) - 1; i >= 0; i-- {
		block := &msg.Content[i]
		if !anthropicBlockSupportsCache(block) {
			continue
		}
		block.CacheControl = ephemeralCacheControl()
		return true
	}
	return false
}

func anthropicBlockSupportsCache(block *anthropicBlock) bool {
	if block == nil {
		return false
	}
	switch block.Type {
	case "text", "tool_result":
		return true
	default:
		return false
	}
}

func ephemeralCacheControl() *anthropicCacheControl {
	return &anthropicCacheControl{Type: "ephemeral"}
}

// doSingleMessagesRequest sends one HTTP request to the messages endpoint
// without any retry logic. Callers that need retries should wrap this with
// providers.WithRetry.
func (c *Client) doSingleMessagesRequest(
	ctx context.Context,
	httpClient *http.Client,
	body []byte,
) (*http.Response, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/messages", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	httpReq.Header.Set("content-type", "application/json")
	if c.apiKey != "" {
		httpReq.Header.Set("x-api-key", c.apiKey)
	}
	if c.authToken != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.authToken)
	}
	httpReq.Header.Set("anthropic-version", defaultAnthropicVersion)
	if c.authToken != "" {
		httpReq.Header.Set("anthropic-beta", "oauth-2025-04-20")
	}
	httpReq.Header.Set("User-Agent", "claude-cli/2.1.96")
	httpReq.Header.Set("x-app", "cli")
	httpReq.Header.Set("Accept", "text/event-stream")
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

// doMessagesRequest sends an HTTP request with automatic retries.
// Used by the non-streaming Chat path.
func (c *Client) doMessagesRequest(
	ctx context.Context,
	httpClient *http.Client,
	body []byte,
) (*http.Response, error) {
	var httpResp *http.Response
	err := providers.WithRetry(ctx, c.retryConfig, func() error {
		resp, err := c.doSingleMessagesRequest(ctx, httpClient, body)
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

// blockState tracks an active content block during SSE streaming.
type blockState struct {
	blockType string // "text", "tool_use", or "thinking"
	toolID    string
	toolName  string
	argsJSON  strings.Builder
}

func (c *Client) readSSEStream(resp *http.Response, ch chan<- providers.StreamEvent) {
	defer close(ch)
	defer resp.Body.Close()

	idleTimeout := c.streamConfig.IdleTimeout
	var idleFired atomic.Bool
	idleTimer := time.AfterFunc(idleTimeout, func() {
		idleFired.Store(true)
		_ = resp.Body.Close()
	})
	defer idleTimer.Stop()
	resetIdle := func() { idleTimer.Reset(idleTimeout) }

	var (
		usage          providers.TokenUsage
		stopReason     string
		blocks         = make(map[int]*blockState)
		cur            sseRawEvent
		sawMessageStop bool
		sawStreamError bool
	)

	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		resetIdle()
		line := scanner.Text()

		if strings.HasPrefix(line, "event:") {
			cur.Event = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			continue
		}
		if strings.HasPrefix(line, "data:") {
			cur.Data = strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			continue
		}
		if line == "" && cur.Event != "" {
			c.handleSSEEvent(cur, &usage, &stopReason, blocks, ch, &sawMessageStop, &sawStreamError)
			cur = sseRawEvent{}
		}
	}

	if cur.Event != "" {
		c.handleSSEEvent(cur, &usage, &stopReason, blocks, ch, &sawMessageStop, &sawStreamError)
	}

	if err := scanner.Err(); err != nil {
		if idleFired.Load() {
			ch <- providers.StreamEvent{Type: providers.EventError, Error: fmt.Errorf("stream idle timeout after %s: %w", idleTimeout, context.DeadlineExceeded)}
			return
		}
		ch <- providers.StreamEvent{Type: providers.EventError, Error: fmt.Errorf("read SSE stream: %w", err)}
		return
	}
	if idleFired.Load() {
		ch <- providers.StreamEvent{Type: providers.EventError, Error: fmt.Errorf("stream idle timeout after %s: %w", idleTimeout, context.DeadlineExceeded)}
		return
	}
	if !sawMessageStop && !sawStreamError {
		ch <- providers.StreamEvent{
			Type:  providers.EventError,
			Error: providers.NewIncompleteStreamError("stream closed before message_stop"),
		}
	}
}

func (c *Client) handleSSEEvent(
	raw sseRawEvent,
	usage *providers.TokenUsage,
	stopReason *string,
	blocks map[int]*blockState,
	ch chan<- providers.StreamEvent,
	sawMessageStop *bool,
	sawStreamError *bool,
) {
	switch raw.Event {
	case "message_start":
		var p messageStartPayload
		if json.Unmarshal([]byte(raw.Data), &p) == nil {
			usage.InputTokens = p.Message.Usage.InputTokens
			usage.CacheCreationTokens = p.Message.Usage.CacheCreationTokens
			usage.CacheReadTokens = p.Message.Usage.CacheReadTokens
		}
	case "content_block_start":
		var p contentBlockStartPayload
		if json.Unmarshal([]byte(raw.Data), &p) == nil {
			bs := &blockState{blockType: p.ContentBlock.Type}
			if p.ContentBlock.Type == "tool_use" {
				bs.toolID = p.ContentBlock.ID
				bs.toolName = p.ContentBlock.Name
				ch <- providers.StreamEvent{Type: providers.EventToolUseStart, ToolCall: &providers.ToolCall{ID: p.ContentBlock.ID, Name: p.ContentBlock.Name}}
			}
			blocks[p.Index] = bs
		}
	case "content_block_delta":
		var p contentBlockDeltaPayload
		if json.Unmarshal([]byte(raw.Data), &p) == nil {
			bs := blocks[p.Index]
			switch p.Delta.Type {
			case "text_delta":
				ch <- providers.StreamEvent{Type: providers.EventContentDelta, Content: p.Delta.Text}
			case "input_json_delta":
				if bs != nil {
					bs.argsJSON.WriteString(p.Delta.PartialJSON)
				}
				ch <- providers.StreamEvent{Type: providers.EventToolUseDelta, Content: p.Delta.PartialJSON}
			case "thinking_delta":
				ch <- providers.StreamEvent{Type: providers.EventThinkingDelta, Content: p.Delta.Thinking}
			}
		}
	case "content_block_stop":
		var idx struct {
			Index int `json:"index"`
		}
		if json.Unmarshal([]byte(raw.Data), &idx) == nil {
			if bs, ok := blocks[idx.Index]; ok {
				if bs.blockType == "tool_use" {
					ch <- providers.StreamEvent{Type: providers.EventToolUseEnd, ToolCall: &providers.ToolCall{ID: bs.toolID, Name: bs.toolName, Arguments: bs.argsJSON.String()}}
				}
				if bs.blockType == "thinking" {
					ch <- providers.StreamEvent{Type: providers.EventThinkingDone}
				}
			}
			delete(blocks, idx.Index)
		}
	case "message_delta":
		var p messageDeltaPayload
		if json.Unmarshal([]byte(raw.Data), &p) == nil {
			if p.Usage.InputTokens != nil {
				usage.InputTokens = *p.Usage.InputTokens
			}
			if p.Usage.OutputTokens != nil {
				usage.OutputTokens = *p.Usage.OutputTokens
			}
			if p.Usage.CacheCreationTokens != nil {
				usage.CacheCreationTokens = *p.Usage.CacheCreationTokens
			}
			if p.Usage.CacheReadTokens != nil {
				usage.CacheReadTokens = *p.Usage.CacheReadTokens
			}
			if p.Delta.StopReason != "" {
				*stopReason = strings.ToLower(p.Delta.StopReason)
			}
		}
	case "message_stop":
		if sawMessageStop != nil {
			*sawMessageStop = true
		}
		ch <- providers.StreamEvent{Type: providers.EventDone, Usage: &providers.TokenUsage{InputTokens: usage.InputTokens, OutputTokens: usage.OutputTokens, CacheCreationTokens: usage.CacheCreationTokens, CacheReadTokens: usage.CacheReadTokens}, StopReason: *stopReason, Truncated: *stopReason == "max_tokens"}
	case "error":
		if sawStreamError != nil {
			*sawStreamError = true
		}
		var p anthropicErrorPayload
		if err := json.Unmarshal([]byte(raw.Data), &p); err == nil {
			ch <- providers.StreamEvent{
				Type:  providers.EventError,
				Error: providers.NewProviderStreamError(p.Error.Code, p.Error.Message),
			}
			return
		}
		ch <- providers.StreamEvent{
			Type:  providers.EventError,
			Error: providers.NewProviderStreamError("", raw.Data),
		}
	}
}

func mapMessage(msg providers.ChatMessage) (anthropicMessage, error) {
	switch msg.Role {
	case "user", "assistant":
		blocks := make([]anthropicBlock, 0, len(msg.ToolCalls)+len(msg.Images)+1)
		if strings.TrimSpace(msg.Content) != "" {
			blocks = append(blocks, anthropicBlock{Type: "text", Text: msg.Content})
		}
		if msg.Role == "user" {
			for _, image := range msg.Images {
				data := strings.TrimSpace(image.Data)
				if data == "" {
					continue
				}
				mediaType := strings.TrimSpace(image.MediaType)
				if mediaType == "" {
					mediaType = "image/png"
				}
				blocks = append(blocks, anthropicBlock{Type: "image", Source: &anthropicImageSource{Type: "base64", MediaType: mediaType, Data: data}})
			}
		}
		for _, call := range msg.ToolCalls {
			var input any
			raw := strings.TrimSpace(call.Arguments)
			if raw == "" {
				input = map[string]any{}
			} else if err := json.Unmarshal([]byte(raw), &input); err != nil {
				return anthropicMessage{}, fmt.Errorf("parse tool call arguments for %s: %w", call.Name, err)
			}
			blocks = append(blocks, anthropicBlock{Type: "tool_use", ID: call.ID, Name: call.Name, Input: input})
		}
		return anthropicMessage{Role: msg.Role, Content: blocks}, nil
	case "tool":
		return anthropicMessage{Role: "user", Content: []anthropicBlock{{Type: "tool_result", ToolUseID: msg.ToolCallID, Content: msg.Content}}}, nil
	default:
		return anthropicMessage{}, fmt.Errorf("unsupported message role %q", msg.Role)
	}
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

type anthropicRequest struct {
	Model       string             `json:"model"`
	System      any                `json:"system,omitempty"`
	MaxTokens   int                `json:"max_tokens"`
	Temperature *float64           `json:"temperature,omitempty"`
	Messages    []anthropicMessage `json:"messages"`
	Tools       []anthropicTool    `json:"tools,omitempty"`
	Stream      bool               `json:"stream,omitempty"`
}

type anthropicMessage struct {
	Role    string           `json:"role"`
	Content []anthropicBlock `json:"content"`
}

type anthropicBlock struct {
	Type         string                 `json:"type"`
	Text         string                 `json:"text,omitempty"`
	Source       *anthropicImageSource  `json:"source,omitempty"`
	ID           string                 `json:"id,omitempty"`
	Name         string                 `json:"name,omitempty"`
	Input        any                    `json:"input,omitempty"`
	ToolUseID    string                 `json:"tool_use_id,omitempty"`
	Content      string                 `json:"content,omitempty"`
	CacheControl *anthropicCacheControl `json:"cache_control,omitempty"`
}

type anthropicSystemBlock struct {
	Type         string                 `json:"type"`
	Text         string                 `json:"text"`
	CacheControl *anthropicCacheControl `json:"cache_control,omitempty"`
}

type anthropicCacheControl struct {
	Type string `json:"type"`
}

type anthropicImageSource struct {
	Type      string `json:"type"`
	MediaType string `json:"media_type"`
	Data      string `json:"data"`
}

type anthropicTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	InputSchema map[string]any `json:"input_schema"`
}

type anthropicResponse struct {
	Content    []anthropicBlock `json:"content"`
	StopReason string           `json:"stop_reason,omitempty"`
	Usage      *struct {
		InputTokens         int `json:"input_tokens"`
		OutputTokens        int `json:"output_tokens"`
		CacheCreationTokens int `json:"cache_creation_input_tokens,omitempty"`
		CacheReadTokens     int `json:"cache_read_input_tokens,omitempty"`
	} `json:"usage,omitempty"`
}

type sseRawEvent struct {
	Event string
	Data  string
}

type anthropicErrorPayload struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

type messageStartPayload struct {
	Message struct {
		Usage struct {
			InputTokens         int `json:"input_tokens"`
			CacheCreationTokens int `json:"cache_creation_input_tokens,omitempty"`
			CacheReadTokens     int `json:"cache_read_input_tokens,omitempty"`
		} `json:"usage"`
	} `json:"message"`
}

type contentBlockStartPayload struct {
	Index        int `json:"index"`
	ContentBlock struct {
		Type  string `json:"type"`
		ID    string `json:"id,omitempty"`
		Name  string `json:"name,omitempty"`
		Input any    `json:"input,omitempty"`
	} `json:"content_block"`
}

type contentBlockDeltaPayload struct {
	Index int `json:"index"`
	Delta struct {
		Type        string `json:"type"`
		Text        string `json:"text,omitempty"`
		PartialJSON string `json:"partial_json,omitempty"`
		Thinking    string `json:"thinking,omitempty"`
	} `json:"delta"`
}

type messageDeltaPayload struct {
	Delta struct {
		StopReason string `json:"stop_reason,omitempty"`
	} `json:"delta"`
	Usage struct {
		InputTokens         *int `json:"input_tokens,omitempty"`
		OutputTokens        *int `json:"output_tokens,omitempty"`
		CacheCreationTokens *int `json:"cache_creation_input_tokens,omitempty"`
		CacheReadTokens     *int `json:"cache_read_input_tokens,omitempty"`
	} `json:"usage"`
}
