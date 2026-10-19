// Package chatgpt implements official Sign in with ChatGPT plan inference.
package chatgpt

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/blueberrycongee/wuu/internal/provideroptions"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/providers/openai"
)

const DefaultBaseURL = "https://api.openai.com/v1"

type ClientConfig struct {
	AccountID string
	// AccessToken refreshes the selected account through its credential owner.
	AccessToken func(context.Context, string) (string, error)
	// BindRequest cancels active HTTP work when the account signs out. Account
	// managers supply it; protocol-only fixtures may omit it.
	BindRequest  func(context.Context, string) (context.Context, func(), error)
	HTTPClient   *http.Client
	StreamConfig *providers.StreamTransportConfig
	Coordinator  *providers.ProviderCoordinator
}

// Client resolves the selected account's current token for every request. It
// cannot be redirected to a compatible endpoint or reuse a local Codex login.
type Client struct {
	accountID        string
	accessToken      func(context.Context, string) (string, error)
	bindRequest      func(context.Context, string) (context.Context, func(), error)
	httpClient       *http.Client
	streamHTTPClient *http.Client
	streamConfig     providers.StreamTransportConfig
	coordinator      *providers.ProviderCoordinator
	scope            providers.ProviderScope
}

func New(cfg ClientConfig) (*Client, error) {
	accountID := strings.TrimSpace(cfg.AccountID)
	if accountID == "" || cfg.AccessToken == nil {
		return nil, errors.New("no ChatGPT account credentials; sign in with ChatGPT")
	}
	hc := &http.Client{Timeout: 30 * time.Second}
	if cfg.HTTPClient != nil {
		copy := *cfg.HTTPClient
		hc = &copy
	}
	// An OAuth bearer belongs only to this API origin. Never follow redirects,
	// including same-host redirects to a route outside the documented API.
	hc.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	streamConfig := providers.ResolveStreamTransportConfig(cfg.StreamConfig)
	return &Client{
		accountID:        accountID,
		accessToken:      cfg.AccessToken,
		bindRequest:      cfg.BindRequest,
		httpClient:       hc,
		streamHTTPClient: providers.BuildStreamingHTTPClient(hc, streamConfig),
		streamConfig:     streamConfig,
		coordinator:      cfg.Coordinator,
		scope:            providers.NewProviderScope(DefaultBaseURL, accountID, ""),
	}, nil
}

func (*Client) RequiresStreamingChat() bool { return true }

func (*Client) ResolveMediaInput(policy providers.MediaInputPolicy, options map[string]any) providers.MediaInputPolicy {
	return providers.ResolveVideoInputPolicy(policy, options, false)
}

func (c *Client) PrepareInferenceRequest(ctx context.Context, req providers.ChatRequest) (providers.ChatRequest, error) {
	if err := ctx.Err(); err != nil {
		return req, err
	}
	// Make omitted limits visible to strict one-shot callers before journaling.
	req.MaxTokens = 0
	req.Temperature = 0
	req.NativeDeferredToolDiscovery = false
	req.ProviderStateScope = string(c.scope)
	req.ProviderOptions = provideroptions.Clone(req.ProviderOptions)
	return req, nil
}

func (c *Client) StreamChat(ctx context.Context, req providers.ChatRequest) (<-chan providers.StreamEvent, error) {
	cleanup := func() {}
	if c.bindRequest != nil {
		var err error
		ctx, cleanup, err = c.bindRequest(ctx, c.accountID)
		if err != nil {
			return nil, err
		}
	}
	started := false
	defer func() {
		if !started {
			cleanup()
		}
	}()
	req, err := c.PrepareInferenceRequest(ctx, req)
	if err != nil {
		return nil, err
	}
	token, err := c.accessToken(ctx, c.accountID)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(token) == "" {
		return nil, errors.New("no ChatGPT account credentials; sign in with ChatGPT")
	}
	client, err := openai.New(openai.ClientConfig{
		BaseURL:             DefaultBaseURL,
		WireAPI:             "responses",
		APIKey:              token,
		HTTPClient:          c.httpClient,
		StreamingHTTPClient: c.streamHTTPClient,
		StreamConfig:        &c.streamConfig,
		Coordinator:         c.coordinator,
		ProviderScope:       c.scope,
		ResponsesChatGPT:    true,
		ResponsesTransport:  providers.StreamTransportSSE,
	})
	if err != nil {
		return nil, err
	}
	events, err := client.StreamChat(ctx, req)
	if err != nil {
		return nil, err
	}
	out := make(chan providers.StreamEvent, 64)
	started = true
	go func() {
		defer cleanup()
		defer close(out)
		for event := range events {
			select {
			case out <- event:
			case <-ctx.Done():
				return
			}
		}
	}()
	return out, nil
}

// Chat collects the mandatory SSE response, preserving the same completion and
// failure semantics as StreamChat rather than issuing an unsupported unary call.
func (c *Client) Chat(ctx context.Context, req providers.ChatRequest) (providers.ChatResponse, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	events, err := c.StreamChat(ctx, req)
	if err != nil {
		return providers.ChatResponse{}, err
	}
	var response providers.ChatResponse
	response.ProviderItemModel = req.Model
	for {
		select {
		case <-ctx.Done():
			return response, ctx.Err()
		case event, ok := <-events:
			if !ok {
				return response, providers.NewIncompleteStreamError("ChatGPT stream ended before response.completed")
			}
			switch event.Type {
			case providers.EventContentDelta:
				response.Content += event.Content
				response.Phase = event.Phase
				response.ProviderItemID = event.ProviderItemID
			case providers.EventContentReplace:
				response.Content = event.Content
				response.Phase = event.Phase
				response.ProviderItemID = event.ProviderItemID
			case providers.EventThinkingDelta:
				response.ReasoningContent += event.Content
			case providers.EventThinkingReplace:
				response.ReasoningContent = event.Content
			case providers.EventThinkingDone:
				if event.ReasoningBlock != nil {
					response.ReasoningBlocks = append(response.ReasoningBlocks, *event.ReasoningBlock)
				}
			case providers.EventToolUseEnd:
				if event.ToolCall != nil {
					response.ToolCalls = append(response.ToolCalls, *event.ToolCall)
				}
			case providers.EventProviderItem:
				if event.ProviderItem != nil {
					response.ProviderItems = append(response.ProviderItems, *event.ProviderItem)
				}
			case providers.EventUsage:
				response.Usage = event.Usage
			case providers.EventError:
				if event.Error == nil {
					event.Error = errors.New("ChatGPT response failed")
				}
				return response, event.Error
			case providers.EventDone:
				if event.ProviderEventType != "response.completed" {
					return response, providers.NewIncompleteStreamError("ChatGPT response did not complete")
				}
				if event.Usage != nil {
					response.Usage = event.Usage
				}
				response.StopReason, response.FinishReason, response.Truncated = event.StopReason, event.FinishReason, event.Truncated
				return response, nil
			}
		}
	}
}

// ModelInfo retains the original descriptor so callers can use capabilities
// supplied by the account catalog without inventing model-specific defaults.
type ModelInfo struct {
	Slug        string          `json:"slug"`
	DisplayName string          `json:"display_name"`
	Raw         json.RawMessage `json:"-"`
}

func (c *Client) ListModels(ctx context.Context) ([]ModelInfo, error) {
	if c.bindRequest != nil {
		bound, cleanup, err := c.bindRequest(ctx, c.accountID)
		if err != nil {
			return nil, err
		}
		defer cleanup()
		ctx = bound
	}
	token, err := c.accessToken(ctx, c.accountID)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(token) == "" {
		return nil, errors.New("no ChatGPT account credentials; sign in with ChatGPT")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, DefaultBaseURL+"/models", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("list ChatGPT models: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<10))
		return nil, providers.NewHTTPError("openai", resp, body)
	}
	var envelope struct {
		Models []json.RawMessage `json:"models"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&envelope); err != nil {
		return nil, fmt.Errorf("parse ChatGPT model catalog: %w", err)
	}
	if envelope.Models == nil {
		return nil, errors.New("ChatGPT model catalog did not contain a models array")
	}
	models := make([]ModelInfo, 0, len(envelope.Models))
	for _, raw := range envelope.Models {
		var descriptor struct {
			Slug        string `json:"slug"`
			DisplayName string `json:"display_name"`
			Visibility  string `json:"visibility"`
		}
		if err := json.Unmarshal(raw, &descriptor); err != nil {
			return nil, fmt.Errorf("parse ChatGPT model: %w", err)
		}
		if descriptor.Visibility != "list" {
			continue
		}
		if strings.TrimSpace(descriptor.Slug) == "" || strings.TrimSpace(descriptor.DisplayName) == "" {
			return nil, errors.New("ChatGPT model catalog contains an invalid visible model")
		}
		models = append(models, ModelInfo{Slug: descriptor.Slug, DisplayName: descriptor.DisplayName, Raw: append(json.RawMessage(nil), raw...)})
	}
	return models, nil
}
