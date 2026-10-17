package xaisub

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/blueberrycongee/wuu/internal/provideroptions"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/providers/openai"
)

// ClientConfig configures the SuperGrok subscription provider.
type ClientConfig struct {
	BaseURL         string
	Home            string
	Headers         map[string]string
	HTTPClient      *http.Client
	StreamConfig    *providers.StreamTransportConfig
	StreamTransport providers.StreamTransportMode
	Coordinator     *providers.ProviderCoordinator
}

// Client uses a Wuu-owned SuperGrok OAuth session as an OpenAI Responses
// provider while leaving the agent loop in Wuu.
type Client struct {
	baseURL          string
	auth             *OAuthSource
	headers          map[string]string
	httpClient       *http.Client
	streamHTTPClient *http.Client
	streamConfig     providers.StreamTransportConfig
	streamTransport  providers.StreamTransportMode
	coordinator      *providers.ProviderCoordinator
}

// New creates a SuperGrok subscription-backed provider client.
func New(cfg ClientConfig) (*Client, error) {
	baseURL := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	home := strings.TrimSpace(cfg.Home)
	if home == "" {
		home = os.Getenv("HOME")
	}
	streamTransport := cfg.StreamTransport
	if streamTransport == "" {
		streamTransport = providers.StreamTransportSSE
	}
	streamConfig := providers.ResolveStreamTransportConfig(cfg.StreamConfig)
	return &Client{
		baseURL:          baseURL,
		auth:             NewOAuthSource(OAuthConfig{BaseURL: baseURL, Home: home, HTTPClient: cfg.HTTPClient}),
		headers:          cloneHeaders(cfg.Headers),
		httpClient:       cfg.HTTPClient,
		streamConfig:     streamConfig,
		streamHTTPClient: providers.BuildStreamingHTTPClient(cfg.HTTPClient, streamConfig),
		streamTransport:  streamTransport,
		coordinator:      cfg.Coordinator,
	}, nil
}

// Chat performs one non-streaming Responses API call.
func (c *Client) Chat(ctx context.Context, req providers.ChatRequest) (providers.ChatResponse, error) {
	var err error
	req, err = c.PrepareInferenceRequest(ctx, req)
	if err != nil {
		return providers.ChatResponse{}, err
	}
	req, err = providers.EnsureInferenceAttemptContext(ctx, req, providers.InferenceOperationAuxiliary, providers.InferenceProfileInteractive)
	if err != nil {
		return providers.ChatResponse{}, err
	}
	client, creds, err := c.openAIClient(ctx, false)
	if err != nil {
		return providers.ChatResponse{}, err
	}
	resp, err := client.Chat(ctx, req)
	if err != nil && providers.IsAuthError(err) && creds.refreshable {
		err = providers.MarkAuthRefreshable(err)
	}
	return resp, err
}

// StreamChat opens a streaming Responses API call.
func (c *Client) StreamChat(ctx context.Context, req providers.ChatRequest) (<-chan providers.StreamEvent, error) {
	var err error
	req, err = c.PrepareInferenceRequest(ctx, req)
	if err != nil {
		return nil, err
	}
	req, err = providers.EnsureInferenceAttemptContext(ctx, req, providers.InferenceOperationAuxiliary, providers.InferenceProfileInteractive)
	if err != nil {
		return nil, err
	}
	client, creds, err := c.openAIClient(ctx, false)
	if err != nil {
		return nil, err
	}
	ch, err := client.StreamChat(ctx, req)
	if err != nil && providers.IsAuthError(err) && creds.refreshable {
		err = providers.MarkAuthRefreshable(err)
	}
	if err != nil || !creds.refreshable {
		return ch, err
	}
	out := make(chan providers.StreamEvent, 64)
	go func() {
		defer close(out)
		for event := range ch {
			if event.Type == providers.EventError && providers.IsAuthError(event.Error) {
				event.Error = providers.MarkAuthRefreshable(event.Error)
			}
			select {
			case out <- event:
			case <-ctx.Done():
				return
			}
		}
	}()
	return out, nil
}

func (c *Client) PrepareInferenceRequest(ctx context.Context, req providers.ChatRequest) (providers.ChatRequest, error) {
	if _, err := c.auth.Credentials(ctx, false); err != nil {
		return providers.ChatRequest{}, err
	}
	return xaiRequest(req), nil
}

func (c *Client) ApplyInferenceRecovery(ctx context.Context, plan providers.RecoveryPlan) error {
	if plan.Action != providers.RecoveryRefreshAuth {
		return fmt.Errorf("xAI SuperGrok client cannot apply recovery action %q", plan.Action)
	}
	_, _, err := c.openAIClient(ctx, true)
	if err != nil {
		return fmt.Errorf("refresh xAI SuperGrok OAuth credentials: %w", err)
	}
	return nil
}

func (c *Client) openAIClient(ctx context.Context, forceRefresh bool) (*openai.Client, credentials, error) {
	creds, err := c.auth.Credentials(ctx, forceRefresh)
	if err != nil {
		return nil, credentials{}, err
	}
	headers := cloneHeaders(c.headers)
	if headers == nil {
		headers = make(map[string]string)
	}
	if _, exists := headers["User-Agent"]; !exists {
		headers["User-Agent"] = userAgent()
	}
	client, err := openai.New(openai.ClientConfig{
		BaseURL:             c.baseURL,
		WireAPI:             "responses",
		APIKey:              creds.accessToken,
		Headers:             headers,
		HTTPClient:          c.httpClient,
		StreamConfig:        &c.streamConfig,
		StreamingHTTPClient: c.streamHTTPClient,
		ResponsesTransport:  c.streamTransport,
		Coordinator:         c.coordinator,
		ProviderScope:       providers.NewProviderScope(c.baseURL, creds.accessToken, ""),
	})
	if err != nil {
		return nil, credentials{}, err
	}
	return client, creds, nil
}

func xaiRequest(req providers.ChatRequest) providers.ChatRequest {
	req.ProviderOptions = provideroptions.Clone(req.ProviderOptions)
	if req.ProviderOptions == nil {
		req.ProviderOptions = make(map[string]any)
	}
	if _, ok := req.ProviderOptions["include"]; !ok {
		req.ProviderOptions["include"] = []string{"reasoning.encrypted_content"}
	}
	return req
}

func cloneHeaders(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}
