package grokbuild

import (
	"context"
	"net/http"
	"os"
	"strings"

	"github.com/blueberrycongee/wuu/internal/grokbuildspec"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/providers/openai"
)

type ClientConfig struct {
	BaseURL              string
	APIKey               string
	Home                 string
	Headers              map[string]string
	HTTPClient           *http.Client
	StreamConfig         *providers.StreamTransportConfig
	Coordinator          *providers.ProviderCoordinator
	ReuseGrokCredentials bool
}

// Client reuses a local Grok Build login while keeping Wuu's own agent loop.
// Grok Build documents its inference surface as OpenAI Chat Completions plus
// two routing headers.
type Client struct {
	baseURL      string
	auth         credentialSource
	headers      map[string]string
	httpClient   *http.Client
	streamConfig *providers.StreamTransportConfig
	coordinator  *providers.ProviderCoordinator
}

func New(cfg ClientConfig) (*Client, error) {
	baseURL := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if baseURL == "" {
		baseURL = grokbuildspec.DefaultBaseURL
	}
	home := strings.TrimSpace(cfg.Home)
	if home == "" {
		home = os.Getenv("HOME")
	}
	return &Client{
		baseURL: baseURL,
		auth: credentialSource{
			explicitToken: cfg.APIKey,
			home:          home,
			reuseCLI:      cfg.ReuseGrokCredentials,
		},
		headers:      cloneHeaders(cfg.Headers),
		httpClient:   cfg.HTTPClient,
		streamConfig: cfg.StreamConfig,
		coordinator:  cfg.Coordinator,
	}, nil
}

func (c *Client) Chat(ctx context.Context, req providers.ChatRequest) (providers.ChatResponse, error) {
	client, err := c.openAIClient(req.Model)
	if err != nil {
		return providers.ChatResponse{}, err
	}
	resp, err := client.Chat(ctx, req)
	return resp, credentialError(err)
}

func (c *Client) StreamChat(ctx context.Context, req providers.ChatRequest) (<-chan providers.StreamEvent, error) {
	client, err := c.openAIClient(req.Model)
	if err != nil {
		return nil, err
	}
	events, err := client.StreamChat(ctx, req)
	if err != nil {
		return nil, credentialError(err)
	}
	out := make(chan providers.StreamEvent)
	go func() {
		defer close(out)
		for event := range events {
			if event.Error != nil {
				event.Error = credentialError(event.Error)
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

func (c *Client) openAIClient(model string) (*openai.Client, error) {
	token, err := c.auth.token()
	if err != nil {
		return nil, err
	}
	headers := cloneHeaders(c.headers)
	if headers == nil {
		headers = make(map[string]string)
	}
	for key := range headers {
		if strings.EqualFold(key, "Authorization") ||
			strings.EqualFold(key, "X-XAI-Token-Auth") ||
			strings.EqualFold(key, "x-grok-model-override") ||
			strings.EqualFold(key, "x-grok-client-version") ||
			strings.EqualFold(key, "x-grok-client-identifier") ||
			strings.EqualFold(key, "x-grok-client-mode") ||
			strings.EqualFold(key, "x-authenticateresponse") {
			delete(headers, key)
		}
	}
	headers["X-XAI-Token-Auth"] = grokbuildspec.TokenAuthHeaderValue
	headers["x-grok-model-override"] = strings.TrimSpace(model)
	headers["x-grok-client-version"] = grokbuildspec.ClientVersion
	headers["x-grok-client-identifier"] = grokbuildspec.ClientIdentifier
	headers["x-grok-client-mode"] = grokbuildspec.ClientMode
	headers["x-authenticateresponse"] = grokbuildspec.AuthenticateResponseValue
	return openai.New(openai.ClientConfig{
		DisableVideoInput: true,
		BaseURL:           c.baseURL,
		WireAPI:           "chat",
		APIKey:            token,
		Headers:           headers,
		HTTPClient:        c.httpClient,
		StreamConfig:      c.streamConfig,
		Coordinator:       c.coordinator,
		ProviderScope:     providers.NewProviderScope(c.baseURL, token, ""),
	})
}

func cloneHeaders(input map[string]string) map[string]string {
	if len(input) == 0 {
		return nil
	}
	out := make(map[string]string, len(input))
	for key, value := range input {
		out[key] = value
	}
	return out
}
