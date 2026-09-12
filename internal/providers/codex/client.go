package codex

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/blueberrycongee/wuu/internal/provideroptions"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/providers/openai"
)

const (
	DefaultBaseURL = "https://chatgpt.com/backend-api/codex"
)

// ClientConfig configures the ChatGPT-backed Codex provider.
type ClientConfig struct {
	BaseURL               string
	APIKey                string
	Headers               map[string]string
	Home                  string
	HTTPClient            *http.Client
	StreamConfig          *providers.StreamTransportConfig
	StreamTransport       providers.StreamTransportMode
	Coordinator           *providers.ProviderCoordinator
	ReuseCodexCredentials bool
	NativeCompaction      *bool
}

// Client uses a local Codex OAuth session as an OpenAI Responses-compatible
// provider while leaving the agent loop in wuu.
type Client struct {
	baseURL          string
	auth             *OAuthSource
	headers          map[string]string
	httpClient       *http.Client
	streamConfig     *providers.StreamTransportConfig
	streamTransport  providers.StreamTransportMode
	coordinator      *providers.ProviderCoordinator
	wsCache          *openai.ResponsesWebSocketCache
	nativeCompaction bool
}

// ModelInfo describes one model advertised by the OAuth-backed Codex backend.
type ModelInfo struct {
	Slug                  string   `json:"slug"`
	DisplayName           string   `json:"display_name,omitempty"`
	Description           string   `json:"description,omitempty"`
	DefaultReasoningLevel string   `json:"default_reasoning_level,omitempty"`
	SupportedReasoning    []string `json:"supported_reasoning,omitempty"`
	Visibility            string   `json:"visibility,omitempty"`
	SupportedInAPI        bool     `json:"supported_in_api"`
	Priority              int      `json:"priority,omitempty"`
}

// New creates a Codex subscription-backed provider client.
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
		streamTransport = providers.StreamTransportAuto
	}
	nativeCompaction := true
	if cfg.NativeCompaction != nil {
		nativeCompaction = *cfg.NativeCompaction
	}
	return &Client{
		baseURL:          baseURL,
		auth:             NewOAuthSource(OAuthConfig{BaseURL: baseURL, APIKey: cfg.APIKey, Home: home, HTTPClient: cfg.HTTPClient, ReuseCodexCredentials: cfg.ReuseCodexCredentials}),
		headers:          cloneHeaders(cfg.Headers),
		httpClient:       cfg.HTTPClient,
		streamConfig:     cfg.StreamConfig,
		streamTransport:  streamTransport,
		coordinator:      cfg.Coordinator,
		wsCache:          openai.NewResponsesWebSocketCache(),
		nativeCompaction: nativeCompaction,
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

// PrewarmSession resolves/caches OAuth credentials and, when configured,
// establishes the session's reusable Responses WebSocket. It deliberately does
// not submit response.create, so blank threads consume no model capacity.
func (c *Client) PrewarmSession(ctx context.Context, sessionID string) error {
	client, _, err := c.openAIClient(ctx, false)
	if err != nil {
		return err
	}
	switch c.streamTransport {
	case providers.StreamTransportAuto,
		providers.StreamTransportWebSocket,
		providers.StreamTransportWebSocketCached:
		return client.PrewarmResponsesWebSocket(ctx, sessionID)
	default:
		return nil
	}
}

func (c *Client) PrepareInferenceRequest(ctx context.Context, req providers.ChatRequest) (providers.ChatRequest, error) {
	creds, err := c.auth.Credentials(ctx, false)
	if err != nil {
		return providers.ChatRequest{}, err
	}
	req.ProviderStateScope = codexNativeCompactionScope(c.baseURL, req.Model, creds)
	return codexRequest(req), nil
}

// NativeCompact invokes Codex Remote Compaction V2 over the normal Responses
// stream. It never calls the legacy /responses/compact endpoint.
func (c *Client) NativeCompactionAvailable() bool {
	return c != nil && c.nativeCompaction
}

func (c *Client) NativeCompact(ctx context.Context, req providers.ChatRequest) (providers.NativeCompactionResult, error) {
	if !c.nativeCompaction {
		return providers.NativeCompactionResult{}, providers.ErrNativeCompactionUnavailable
	}
	client, creds, err := c.openAIClient(ctx, false)
	if err != nil {
		return providers.NativeCompactionResult{}, err
	}
	req = codexRequest(req)
	req.Messages = providers.ResolveProviderHistory(req.Messages, req.Provider, req.ProviderStateScope)
	item, usage, err := client.CompactResponsesV2(ctx, req)
	if err != nil {
		if providers.IsAuthError(err) && creds.refreshable {
			err = providers.MarkAuthRefreshable(err)
		}
		return providers.NativeCompactionResult{}, err
	}
	item.Fallback = providers.CloneChatMessages(req.Messages)
	return providers.NativeCompactionResult{
		Replacement: []providers.ChatMessage{{
			Role:          "assistant",
			Hidden:        true,
			ProviderItems: []providers.ProviderItem{item},
		}},
		Usage:              usage,
		ProviderStateScope: req.ProviderStateScope,
	}, nil
}

func (c *Client) ApplyInferenceRecovery(ctx context.Context, plan providers.RecoveryPlan) error {
	if plan.Action != providers.RecoveryRefreshAuth {
		return fmt.Errorf("Codex client cannot apply recovery action %q", plan.Action)
	}
	_, _, err := c.openAIClient(ctx, true)
	if err != nil {
		return fmt.Errorf("refresh Codex OAuth credentials: %w", err)
	}
	return nil
}

// Models fetches the live model catalog for the current Codex OAuth account.
func (c *Client) Models(ctx context.Context) ([]ModelInfo, error) {
	creds, err := c.auth.Credentials(ctx, false)
	if err != nil {
		return nil, err
	}
	endpoint := c.baseURL + "/models?client_version=1.0.0"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("build Codex models request: %w", err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+creds.accessToken)
	httpReq.Header.Set("Accept", "application/json")
	for k, v := range c.headers {
		httpReq.Header.Set(k, v)
	}
	for k, v := range codexHeaders(creds.accessToken, creds.accountID) {
		if httpReq.Header.Get(k) == "" {
			httpReq.Header.Set(k, v)
		}
	}

	httpClient := c.httpClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 20 * time.Second}
	}
	resp, err := httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("fetch Codex models: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("fetch Codex models: %s: %s", resp.Status, string(body))
	}

	models, err := parseModels(body)
	if err != nil {
		return nil, err
	}
	return models, nil
}

// codexRequest applies ChatGPT-Codex specific request shaping. The defaults
// ask the backend to surface reasoning encrypted content, allow parallel tool
// calls, and request concise text.
// It also removes request fields that the Codex subscription backend rejects.
// Callers can override supported provider options before sending.
func codexRequest(req providers.ChatRequest) providers.ChatRequest {
	req.Temperature = 0
	req.MaxTokens = 0
	req.ProviderOptions = provideroptions.Clone(req.ProviderOptions)
	if req.ProviderOptions == nil {
		req.ProviderOptions = make(map[string]any)
	}
	delete(req.ProviderOptions, "maxOutputTokens")
	delete(req.ProviderOptions, "max_output_tokens")
	delete(req.ProviderOptions, "temperatureSupported")
	delete(req.ProviderOptions, "temperature_supported")
	if _, ok := req.ProviderOptions["include"]; !ok {
		req.ProviderOptions["include"] = []string{"reasoning.encrypted_content"}
	}
	if _, ok := req.ProviderOptions["parallelToolCalls"]; !ok {
		req.ProviderOptions["parallelToolCalls"] = true
	}
	if _, ok := req.ProviderOptions["textVerbosity"]; !ok {
		req.ProviderOptions["textVerbosity"] = "low"
	}
	return req
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
	for k, v := range codexHeaders(creds.accessToken, creds.accountID) {
		if _, exists := headers[k]; !exists {
			headers[k] = v
		}
	}
	store := false
	providerScope := codexProviderScope(c.baseURL, creds)
	client, err := openai.New(openai.ClientConfig{
		BaseURL:                 c.baseURL,
		WireAPI:                 "responses",
		APIKey:                  creds.accessToken,
		Headers:                 headers,
		HTTPClient:              c.httpClient,
		StreamConfig:            c.streamConfig,
		ResponsesStore:          &store,
		ResponsesTransport:      c.streamTransport,
		ResponsesWebSocketCache: c.wsCache,
		Coordinator:             c.coordinator,
		ProviderScope:           providerScope,
	})
	if err != nil {
		return nil, credentials{}, err
	}
	return client, creds, nil
}

func codexProviderScope(baseURL string, creds credentials) providers.ProviderScope {
	// Account ID survives access-token refresh, so prefer it as the private
	// identity. Explicit opaque tokens without an account claim still get an
	// isolated scope through their keyed credential fingerprint.
	if accountID := strings.TrimSpace(creds.accountID); accountID != "" {
		return providers.NewProviderScope(baseURL, "", accountID)
	}
	return providers.NewProviderScope(baseURL, creds.accessToken, "")
}

func codexNativeCompactionScope(baseURL, model string, creds credentials) string {
	identity := strings.TrimSpace(creds.accountID)
	if identity == "" {
		identity = strings.TrimSpace(creds.accessToken)
	}
	sum := sha256.Sum256([]byte(strings.TrimSpace(baseURL) + "\x00" + identity + "\x00" + strings.TrimSpace(model)))
	return "codex-v2:" + hex.EncodeToString(sum[:16])
}

func parseModels(data []byte) ([]ModelInfo, error) {
	var payload struct {
		Models []struct {
			Slug                  string `json:"slug"`
			DisplayName           string `json:"display_name"`
			Description           string `json:"description"`
			DefaultReasoningLevel string `json:"default_reasoning_level"`
			SupportedReasoning    []struct {
				Effort string `json:"effort"`
			} `json:"supported_reasoning_levels"`
			Visibility     string `json:"visibility"`
			SupportedInAPI bool   `json:"supported_in_api"`
			Priority       *int   `json:"priority"`
		} `json:"models"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, fmt.Errorf("parse Codex models response: %w", err)
	}
	models := make([]ModelInfo, 0, len(payload.Models))
	seen := make(map[string]struct{}, len(payload.Models))
	for _, item := range payload.Models {
		slug := strings.TrimSpace(item.Slug)
		if slug == "" {
			continue
		}
		if _, exists := seen[slug]; exists {
			continue
		}
		visibility := strings.TrimSpace(item.Visibility)
		if strings.EqualFold(visibility, "hide") || strings.EqualFold(visibility, "hidden") {
			continue
		}
		seen[slug] = struct{}{}
		reasoning := make([]string, 0, len(item.SupportedReasoning))
		for _, level := range item.SupportedReasoning {
			if effort := strings.TrimSpace(level.Effort); effort != "" {
				reasoning = append(reasoning, effort)
			}
		}
		priority := 10000
		if item.Priority != nil {
			priority = *item.Priority
		}
		models = append(models, ModelInfo{
			Slug:                  slug,
			DisplayName:           strings.TrimSpace(item.DisplayName),
			Description:           strings.TrimSpace(item.Description),
			DefaultReasoningLevel: strings.TrimSpace(item.DefaultReasoningLevel),
			SupportedReasoning:    reasoning,
			Visibility:            visibility,
			SupportedInAPI:        item.SupportedInAPI,
			Priority:              priority,
		})
	}
	sort.SliceStable(models, func(i, j int) bool {
		if models[i].Priority != models[j].Priority {
			return models[i].Priority < models[j].Priority
		}
		return models[i].Slug < models[j].Slug
	})
	return models, nil
}

func cloneHeaders(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
