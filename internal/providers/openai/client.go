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
	"time"

	"github.com/blueberrycongee/wuu/internal/providers"
)

const defaultTimeout = 120 * time.Second

// ClientConfig configures an OpenAI-compatible chat completions endpoint.
type ClientConfig struct {
	BaseURL     string
	APIKey      string
	Headers     map[string]string
	HTTPClient  *http.Client
	RetryConfig *providers.RetryConfig
}

// Client sends tool-enabled chat requests to OpenAI-compatible APIs.
type Client struct {
	baseURL     string
	apiKey      string
	headers     map[string]string
	httpClient  *http.Client
	retryConfig providers.RetryConfig
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
		baseURL:     strings.TrimRight(cfg.BaseURL, "/"),
		apiKey:      cfg.APIKey,
		headers:     cloneHeaders(cfg.Headers),
		httpClient:  hc,
		retryConfig: rc,
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
		Model:       req.Model,
		Messages:    make([]chatMessage, 0, len(req.Messages)),
		Temperature: req.Temperature,
	}

	for _, msg := range req.Messages {
		payload.Messages = append(payload.Messages, mapMessage(msg))
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

	body, err := json.Marshal(payload)
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
		return providers.ChatResponse{}, &providers.HTTPError{
			StatusCode: httpResp.StatusCode,
			Body:       fmt.Sprintf("%s: %s", httpResp.Status, snippet),
			RetryAfter: providers.ParseRetryAfter(httpResp),
		}
	}

	var parsed chatCompletionsResponse
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return providers.ChatResponse{}, fmt.Errorf("parse response JSON: %w", err)
	}
	if len(parsed.Choices) == 0 {
		return providers.ChatResponse{}, errors.New("provider returned no choices")
	}

	message := parsed.Choices[0].Message
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
		Content:   content,
		ToolCalls: calls,
	}
	if parsed.Usage != nil {
		resp.Usage = &providers.TokenUsage{
			InputTokens:  parsed.Usage.PromptTokens,
			OutputTokens: parsed.Usage.CompletionTokens,
		}
	}
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
		Model:       req.Model,
		Messages:    make([]chatMessage, 0, len(req.Messages)),
		Temperature: req.Temperature,
		Stream:      true,
	}
	for _, msg := range req.Messages {
		payload.Messages = append(payload.Messages, mapMessage(msg))
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

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	// Use a separate client without short timeout for long-lived SSE connections.
	streamClient := &http.Client{Timeout: 10 * time.Minute}
	resp, err := c.doChatCompletionsRequest(ctx, streamClient, body, true)
	if err != nil {
		return nil, err
	}

	ch := make(chan providers.StreamEvent, 64)
	go c.readSSE(resp, ch)
	return ch, nil
}

func (c *Client) doChatCompletionsRequest(
	ctx context.Context,
	httpClient *http.Client,
	body []byte,
	acceptStream bool,
) (*http.Response, error) {
	var httpResp *http.Response
	err := providers.WithRetry(ctx, c.retryConfig, func() error {
		httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/chat/completions", bytes.NewReader(body))
		if err != nil {
			return fmt.Errorf("build request: %w", err)
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
			return fmt.Errorf("request failed: %w", err)
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 400))
			_ = resp.Body.Close()
			return &providers.HTTPError{
				StatusCode: resp.StatusCode,
				Body:       fmt.Sprintf("%s: %s", resp.Status, string(snippet)),
				RetryAfter: providers.ParseRetryAfter(resp),
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

func (c *Client) readSSE(resp *http.Response, ch chan<- providers.StreamEvent) {
	defer close(ch)
	defer resp.Body.Close()

	type pendingTool struct {
		id   string
		name string
		args strings.Builder
	}
	pending := make(map[int]*pendingTool)
	var lastUsage *providers.TokenUsage

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
			emitToolEnds()
			ch <- providers.StreamEvent{
				Type:  providers.EventDone,
				Usage: lastUsage,
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
			lastUsage = &providers.TokenUsage{
				InputTokens:  chunk.Usage.PromptTokens,
				OutputTokens: chunk.Usage.CompletionTokens,
			}
		}

		if len(chunk.Choices) == 0 {
			continue
		}
		choice := chunk.Choices[0]

		if choice.Delta.Content != "" {
			ch <- providers.StreamEvent{
				Type:    providers.EventContentDelta,
				Content: choice.Delta.Content,
			}
		}

		for _, tc := range choice.Delta.ToolCalls {
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

		if choice.FinishReason != nil && *choice.FinishReason == "tool_calls" {
			emitToolEnds()
		}
	}

	if err := scanner.Err(); err != nil {
		ch <- providers.StreamEvent{Type: providers.EventError, Error: fmt.Errorf("read stream: %w", err)}
	}
}

func mapMessage(msg providers.ChatMessage) chatMessage {
	mapped := chatMessage{
		Role:       msg.Role,
		Name:       msg.Name,
		ToolCallID: msg.ToolCallID,
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

type chatCompletionsRequest struct {
	Model       string           `json:"model"`
	Messages    []chatMessage    `json:"messages"`
	Tools       []toolDefinition `json:"tools,omitempty"`
	ToolChoice  string           `json:"tool_choice,omitempty"`
	Temperature float64          `json:"temperature,omitempty"`
	Stream      bool             `json:"stream,omitempty"`
}

type chatMessage struct {
	Role       string     `json:"role"`
	Content    any        `json:"content,omitempty"`
	Name       string     `json:"name,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	ToolCalls  []toolCall `json:"tool_calls,omitempty"`
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
	Message chatResponseMessage `json:"message"`
}

type chatResponseMessage struct {
	Content   json.RawMessage `json:"content"`
	ToolCalls []toolCall      `json:"tool_calls"`
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
	Content   string          `json:"content,omitempty"`
	ToolCalls []toolCallDelta `json:"tool_calls,omitempty"`
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
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
}
