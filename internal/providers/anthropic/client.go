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
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/blueberrycongee/wuu/internal/providers"
)

// defaultStreamIdleTimeout is the maximum silence between SSE chunks before
// the watchdog aborts the stream. Aligned with Claude Code's 90s default.
const defaultStreamIdleTimeout = 90 * time.Second

func streamIdleTimeout() time.Duration {
	if v := os.Getenv("WUU_STREAM_IDLE_TIMEOUT_MS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return time.Duration(n) * time.Millisecond
		}
	}
	return defaultStreamIdleTimeout
}

const (
	defaultTimeout          = 120 * time.Second
	defaultAnthropicVersion = "2023-06-01"
	defaultMaxTokens        = 4096
)

// ClientConfig configures an Anthropic messages endpoint.
type ClientConfig struct {
	BaseURL     string
	APIKey      string
	Headers     map[string]string
	HTTPClient  *http.Client
	MaxTokens   int
	RetryConfig *providers.RetryConfig
}

// Client sends tool-enabled chat requests to Anthropic APIs.
type Client struct {
	baseURL     string
	apiKey      string
	headers     map[string]string
	httpClient  *http.Client
	maxTokens   int
	retryConfig providers.RetryConfig
}

// New creates an Anthropic client.
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
		baseURL:     strings.TrimRight(cfg.BaseURL, "/"),
		apiKey:      cfg.APIKey,
		headers:     cloneHeaders(cfg.Headers),
		httpClient:  hc,
		maxTokens:   maxTokens,
		retryConfig: rc,
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

	payload := anthropicRequest{
		Model:     req.Model,
		MaxTokens: c.maxTokens,
		Messages:  make([]anthropicMessage, 0, len(req.Messages)),
	}
	if req.Temperature > 0 {
		t := req.Temperature
		payload.Temperature = &t
	}

	for _, msg := range req.Messages {
		if strings.EqualFold(msg.Role, "system") {
			if payload.System != "" {
				payload.System += "\n"
			}
			payload.System += msg.Content
			continue
		}

		mapped, err := mapMessage(msg)
		if err != nil {
			return providers.ChatResponse{}, err
		}
		payload.Messages = append(payload.Messages, mapped)
	}

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
	// Anthropic signals output truncation with stop_reason="max_tokens".
	if resp.StopReason == "max_tokens" {
		resp.Truncated = true
	}
	if parsed.Usage != nil {
		resp.Usage = &providers.TokenUsage{
			InputTokens:  parsed.Usage.InputTokens,
			OutputTokens: parsed.Usage.OutputTokens,
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

	payload := anthropicRequest{
		Model:     req.Model,
		MaxTokens: c.maxTokens,
		Messages:  make([]anthropicMessage, 0, len(req.Messages)),
		Stream:    true,
	}
	if req.Temperature > 0 {
		t := req.Temperature
		payload.Temperature = &t
	}

	for _, msg := range req.Messages {
		if strings.EqualFold(msg.Role, "system") {
			if payload.System != "" {
				payload.System += "\n"
			}
			payload.System += msg.Content
			continue
		}
		mapped, err := mapMessage(msg)
		if err != nil {
			return nil, err
		}
		payload.Messages = append(payload.Messages, mapped)
	}

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

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	// Use a separate client without timeout for long-lived SSE connections.
	sseClient := &http.Client{Timeout: 0}
	resp, err := c.doMessagesRequest(ctx, sseClient, body)
	if err != nil {
		return nil, err
	}

	ch := make(chan providers.StreamEvent, 64)
	go c.readSSEStream(resp, ch)
	return ch, nil
}

func (c *Client) doMessagesRequest(
	ctx context.Context,
	httpClient *http.Client,
	body []byte,
) (*http.Response, error) {
	var httpResp *http.Response
	err := providers.WithRetry(ctx, c.retryConfig, func() error {
		httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/messages", bytes.NewReader(body))
		if err != nil {
			return fmt.Errorf("build request: %w", err)
		}
		httpReq.Header.Set("content-type", "application/json")
		httpReq.Header.Set("x-api-key", c.apiKey)
		httpReq.Header.Set("anthropic-version", defaultAnthropicVersion)
		for k, v := range c.headers {
			httpReq.Header.Set(k, v)
		}

		resp, err := httpClient.Do(httpReq)
		if err != nil {
			return fmt.Errorf("request failed: %w", err)
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 400))
			_ = resp.Body.Close()
			body := fmt.Sprintf("%s: %s", resp.Status, string(snippet))
			return &providers.HTTPError{
				StatusCode:      resp.StatusCode,
				Body:            body,
				RetryAfter:      providers.ParseRetryAfter(resp),
				ContextOverflow: providers.DetectContextOverflow(body),
			}
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

	// Idle watchdog: abort the body if no chunk arrives within the timeout.
	idleTimeout := streamIdleTimeout()
	var idleFired atomic.Bool
	idleTimer := time.AfterFunc(idleTimeout, func() {
		idleFired.Store(true)
		_ = resp.Body.Close()
	})
	defer idleTimer.Stop()
	resetIdle := func() { idleTimer.Reset(idleTimeout) }

	var (
		usage  providers.TokenUsage
		blocks = make(map[int]*blockState)
		cur    sseRawEvent
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

		// Empty line signals end of an SSE frame.
		if line == "" && cur.Event != "" {
			c.handleSSEEvent(cur, &usage, blocks, ch)
			cur = sseRawEvent{}
		}
	}

	// Process any trailing event without a final blank line.
	if cur.Event != "" {
		c.handleSSEEvent(cur, &usage, blocks, ch)
	}

	if err := scanner.Err(); err != nil {
		if idleFired.Load() {
			ch <- providers.StreamEvent{
				Type:  providers.EventError,
				Error: fmt.Errorf("stream idle timeout after %s: %w", idleTimeout, context.DeadlineExceeded),
			}
			return
		}
		ch <- providers.StreamEvent{Type: providers.EventError, Error: fmt.Errorf("read SSE stream: %w", err)}
		return
	}
	if idleFired.Load() {
		ch <- providers.StreamEvent{
			Type:  providers.EventError,
			Error: fmt.Errorf("stream idle timeout after %s: %w", idleTimeout, context.DeadlineExceeded),
		}
	}
}

func (c *Client) handleSSEEvent(
	raw sseRawEvent,
	usage *providers.TokenUsage,
	blocks map[int]*blockState,
	ch chan<- providers.StreamEvent,
) {
	switch raw.Event {
	case "message_start":
		var p messageStartPayload
		if json.Unmarshal([]byte(raw.Data), &p) == nil {
			usage.InputTokens = p.Message.Usage.InputTokens
		}

	case "content_block_start":
		var p contentBlockStartPayload
		if json.Unmarshal([]byte(raw.Data), &p) == nil {
			bs := &blockState{blockType: p.ContentBlock.Type}
			if p.ContentBlock.Type == "tool_use" {
				bs.toolID = p.ContentBlock.ID
				bs.toolName = p.ContentBlock.Name
				ch <- providers.StreamEvent{
					Type: providers.EventToolUseStart,
					ToolCall: &providers.ToolCall{
						ID:   p.ContentBlock.ID,
						Name: p.ContentBlock.Name,
					},
				}
			}
			blocks[p.Index] = bs
		}

	case "content_block_delta":
		var p contentBlockDeltaPayload
		if json.Unmarshal([]byte(raw.Data), &p) == nil {
			bs := blocks[p.Index]
			switch p.Delta.Type {
			case "text_delta":
				ch <- providers.StreamEvent{
					Type:    providers.EventContentDelta,
					Content: p.Delta.Text,
				}
			case "input_json_delta":
				if bs != nil {
					bs.argsJSON.WriteString(p.Delta.PartialJSON)
				}
				ch <- providers.StreamEvent{
					Type:    providers.EventToolUseDelta,
					Content: p.Delta.PartialJSON,
				}
			case "thinking_delta":
				ch <- providers.StreamEvent{
					Type:    providers.EventThinkingDelta,
					Content: p.Delta.Thinking,
				}
			}
		}

	case "content_block_stop":
		var idx struct {
			Index int `json:"index"`
		}
		if json.Unmarshal([]byte(raw.Data), &idx) == nil {
			if bs, ok := blocks[idx.Index]; ok {
				if bs.blockType == "tool_use" {
					ch <- providers.StreamEvent{
						Type: providers.EventToolUseEnd,
						ToolCall: &providers.ToolCall{
							ID:        bs.toolID,
							Name:      bs.toolName,
							Arguments: bs.argsJSON.String(),
						},
					}
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
			usage.OutputTokens = p.Usage.OutputTokens
		}

	case "message_stop":
		ch <- providers.StreamEvent{
			Type:  providers.EventDone,
			Usage: &providers.TokenUsage{InputTokens: usage.InputTokens, OutputTokens: usage.OutputTokens},
		}
	}
}

func mapMessage(msg providers.ChatMessage) (anthropicMessage, error) {
	switch msg.Role {
	case "user", "assistant":
		blocks := make([]anthropicBlock, 0, len(msg.ToolCalls)+len(msg.Images)+1)
		if strings.TrimSpace(msg.Content) != "" {
			blocks = append(blocks, anthropicBlock{
				Type: "text",
				Text: msg.Content,
			})
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
				blocks = append(blocks, anthropicBlock{
					Type: "image",
					Source: &anthropicImageSource{
						Type:      "base64",
						MediaType: mediaType,
						Data:      data,
					},
				})
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
			blocks = append(blocks, anthropicBlock{
				Type:  "tool_use",
				ID:    call.ID,
				Name:  call.Name,
				Input: input,
			})
		}
		return anthropicMessage{
			Role:    msg.Role,
			Content: blocks,
		}, nil
	case "tool":
		return anthropicMessage{
			Role: "user",
			Content: []anthropicBlock{{
				Type:      "tool_result",
				ToolUseID: msg.ToolCallID,
				Content:   msg.Content,
			}},
		}, nil
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
	System      string             `json:"system,omitempty"`
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
	Type      string                `json:"type"`
	Text      string                `json:"text,omitempty"`
	Source    *anthropicImageSource `json:"source,omitempty"`
	ID        string                `json:"id,omitempty"`
	Name      string                `json:"name,omitempty"`
	Input     any                   `json:"input,omitempty"`
	ToolUseID string                `json:"tool_use_id,omitempty"`
	Content   string                `json:"content,omitempty"`
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
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage,omitempty"`
}

// SSE streaming types.

type sseRawEvent struct {
	Event string
	Data  string
}

type messageStartPayload struct {
	Message struct {
		Usage struct {
			InputTokens int `json:"input_tokens"`
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
	Usage struct {
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
}
