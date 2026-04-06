package anthropic

import (
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

const (
	defaultTimeout          = 120 * time.Second
	defaultAnthropicVersion = "2023-06-01"
	defaultMaxTokens        = 4096
)

// ClientConfig configures an Anthropic messages endpoint.
type ClientConfig struct {
	BaseURL    string
	APIKey     string
	Headers    map[string]string
	HTTPClient *http.Client
	MaxTokens  int
}

// Client sends tool-enabled chat requests to Anthropic APIs.
type Client struct {
	baseURL    string
	apiKey     string
	headers    map[string]string
	httpClient *http.Client
	maxTokens  int
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

	return &Client{
		baseURL:    strings.TrimRight(cfg.BaseURL, "/"),
		apiKey:     cfg.APIKey,
		headers:    cloneHeaders(cfg.Headers),
		httpClient: hc,
		maxTokens:  maxTokens,
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

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/messages", bytes.NewReader(body))
	if err != nil {
		return providers.ChatResponse{}, fmt.Errorf("build request: %w", err)
	}
	httpReq.Header.Set("content-type", "application/json")
	httpReq.Header.Set("x-api-key", c.apiKey)
	httpReq.Header.Set("anthropic-version", defaultAnthropicVersion)
	for k, v := range c.headers {
		httpReq.Header.Set(k, v)
	}

	httpResp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return providers.ChatResponse{}, fmt.Errorf("request failed: %w", err)
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
		return providers.ChatResponse{}, fmt.Errorf("provider returned %s: %s", httpResp.Status, snippet)
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

	return providers.ChatResponse{
		Content:   strings.TrimSpace(strings.Join(textParts, "\n")),
		ToolCalls: toolCalls,
	}, nil
}

func mapMessage(msg providers.ChatMessage) (anthropicMessage, error) {
	switch msg.Role {
	case "user", "assistant":
		blocks := make([]anthropicBlock, 0, len(msg.ToolCalls)+1)
		if strings.TrimSpace(msg.Content) != "" {
			blocks = append(blocks, anthropicBlock{
				Type: "text",
				Text: msg.Content,
			})
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
}

type anthropicMessage struct {
	Role    string           `json:"role"`
	Content []anthropicBlock `json:"content"`
}

type anthropicBlock struct {
	Type      string `json:"type"`
	Text      string `json:"text,omitempty"`
	ID        string `json:"id,omitempty"`
	Name      string `json:"name,omitempty"`
	Input     any    `json:"input,omitempty"`
	ToolUseID string `json:"tool_use_id,omitempty"`
	Content   string `json:"content,omitempty"`
}

type anthropicTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	InputSchema map[string]any `json:"input_schema"`
}

type anthropicResponse struct {
	Content []anthropicBlock `json:"content"`
}
