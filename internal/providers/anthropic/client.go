package anthropic

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"time"

	wuucontext "github.com/blueberrycongee/wuu/internal/context"
	"github.com/blueberrycongee/wuu/internal/modelvariant"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/version"
)

func streamTransportConfig(cfg *providers.StreamTransportConfig) providers.StreamTransportConfig {
	return providers.ResolveStreamTransportConfig(cfg)
}

func streamIdleTimeout() time.Duration {
	return streamTransportConfig(nil).IdleTimeout
}

const (
	defaultTimeout          = 120 * time.Second
	defaultAnthropicVersion = "2023-06-01"
	defaultMaxTokens        = 16384
	toolSearchBetaHeader1P  = "advanced-tool-use-2025-11-20"
)

func anthropicToolSurfaceValidationTarget(model string) providers.ToolSurfaceValidationTarget {
	return providers.ToolSurfaceValidationTarget{
		ProviderKind: "anthropic",
		ProviderName: "anthropic",
		Model:        model,
	}
}

// resolveMaxTokens picks the per-request override if positive,
// then the client-level configured value, then falls back to a
// model-aware default from the registry.
func resolveMaxTokens(perRequest, clientDefault int, model string) int {
	if perRequest > 0 {
		return perRequest
	}
	if clientDefault > 0 {
		return clientDefault
	}
	return providers.MaxOutputTokensFor(model)
}

// ClientConfig configures an Anthropic messages endpoint.
type ClientConfig struct {
	BaseURL       string
	APIKey        string
	AuthToken     string // Bearer token (ANTHROPIC_AUTH_TOKEN). Used instead of APIKey when set.
	Headers       map[string]string
	HTTPClient    *http.Client
	MaxTokens     int
	StreamConfig  *providers.StreamTransportConfig
	Coordinator   *providers.ProviderCoordinator
	ProviderScope providers.ProviderScope
	// CacheCreationInputTokensOmitted marks compatible endpoints that omit
	// cache_creation_input_tokens from usage payloads.
	CacheCreationInputTokensOmitted bool
	// InputTokensIncludeCacheRead marks compatible endpoints whose
	// input_tokens are inclusive of cache_read_input_tokens (e.g. MiniMax).
	// When set, normalizeInclusiveInput subtracts cache_read from input.
	InputTokensIncludeCacheRead bool
}

// stampCacheCreationFlag sets CacheCreationUnknown on usage when the
// configured endpoint is known to omit the cache_creation field.
// Callers should invoke this once after the final usage values are
// populated, before constructing a StreamEvent or returning a
// non-streaming response. Safe to call repeatedly.
func (c *Client) stampCacheCreationFlag(usage *providers.TokenUsage) {
	if usage == nil {
		return
	}
	if c.cacheCreationInputTokensOmitted {
		usage.CacheCreationUnknown = true
	}
}

// normalizeInclusiveInput adjusts usage for endpoints whose input_tokens are
// inclusive of cache_read_input_tokens (e.g. MiniMax), unlike native Anthropic
// where input_tokens exclude cached tokens. It subtracts cache_read from input
// (floored at 0) and preserves cache_read so hit-rate and composition displays
// stay intact. This mirrors the OpenAI client's "fresh input = prompt_tokens -
// cached" handling (internal/providers/openai/client.go). Equivalent occupancy
// becomes input+output because cache_read cancels in TotalContextTokens.
//
// NOT idempotent: subtracting cache_read twice over-reduces input. Both call
// sites are terminal (one per response), so a single paired call beside
// stampCacheCreationFlag is correct. Do not re-invoke on already-normalized
// usage. No-op when the endpoint flag is off (native Anthropic unaffected).
func (c *Client) normalizeInclusiveInput(usage *providers.TokenUsage) {
	if usage == nil || !c.inputTokensIncludeCacheRead {
		return
	}
	fresh := usage.InputTokens - usage.CacheReadTokens
	if fresh < 0 {
		fresh = 0
	}
	usage.InputTokens = fresh
}

// Client sends tool-enabled chat requests to Anthropic APIs.
type Client struct {
	baseURL                         string
	messagesURL                     string
	apiKey                          string
	authToken                       string
	headers                         map[string]string
	httpClient                      *http.Client
	maxTokens                       int
	streamConfig                    providers.StreamTransportConfig
	coordinator                     *providers.ProviderCoordinator
	providerScope                   providers.ProviderScope
	cacheCreationInputTokensOmitted bool
	inputTokensIncludeCacheRead     bool
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
	// When 0, resolveMaxTokens falls back to model-aware defaults.
	providerScope := cfg.ProviderScope
	if providerScope == "" && cfg.Coordinator != nil {
		credential := cfg.APIKey
		if strings.TrimSpace(cfg.AuthToken) != "" {
			credential = cfg.AuthToken
		}
		providerScope = providers.NewProviderScope(cfg.BaseURL, credential, "")
	}

	return &Client{
		baseURL:                         strings.TrimRight(cfg.BaseURL, "/"),
		messagesURL:                     anthropicMessagesURL(cfg.BaseURL),
		apiKey:                          cfg.APIKey,
		authToken:                       cfg.AuthToken,
		headers:                         cloneHeaders(cfg.Headers),
		httpClient:                      hc,
		maxTokens:                       maxTokens,
		streamConfig:                    streamTransportConfig(cfg.StreamConfig),
		coordinator:                     cfg.Coordinator,
		providerScope:                   providerScope,
		cacheCreationInputTokensOmitted: cfg.CacheCreationInputTokensOmitted,
		// Explicit config only — never base_url auto-detection. Usage
		// semantics are vendor behavior that changes over time (MiniMax was
		// live-probed exclusive on 2026-07-06, contradicting the assumption
		// a former "minimaxi" substring auto-detect baked in), so the flag
		// must stay a deliberate per-provider config decision, verified with
		// two identical cache_control requests before enabling.
		inputTokensIncludeCacheRead: cfg.InputTokensIncludeCacheRead,
	}, nil
}

func anthropicMessagesURL(baseURL string) string {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	switch {
	case strings.HasSuffix(baseURL, "/v1/messages"):
		return baseURL
	case strings.HasSuffix(baseURL, "/v1"):
		return baseURL + "/messages"
	default:
		return baseURL + "/v1/messages"
	}
}

// Chat performs one Anthropic messages round.
func (c *Client) Chat(ctx context.Context, req providers.ChatRequest) (providers.ChatResponse, error) {
	if strings.TrimSpace(req.Model) == "" {
		return providers.ChatResponse{}, errors.New("model is required")
	}
	if len(req.Messages) == 0 {
		return providers.ChatResponse{}, errors.New("messages is required")
	}
	if err := providers.ValidateToolDefinitionsForProvider(anthropicToolSurfaceValidationTarget(req.Model), req.Tools); err != nil {
		return providers.ChatResponse{}, err
	}
	var err error
	req, err = providers.EnsureInferenceAttemptContext(ctx, req, providers.InferenceOperationAuxiliary, providers.InferenceProfileInteractive)
	if err != nil {
		return providers.ChatResponse{}, err
	}

	maxTok := resolveMaxTokens(req.MaxTokens, c.maxTokens, req.Model)
	payload, err := c.buildAnthropicRequest(req, maxTok, false)
	if err != nil {
		return providers.ChatResponse{}, err
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return providers.ChatResponse{}, fmt.Errorf("marshal request: %w", err)
	}

	httpResp, lease, err := c.doSingleMessagesRequest(ctx, c.httpClient, body, payload.UseDefaultBetas, payload.Betas, req.Attempt, "unary")
	if err != nil {
		return providers.ChatResponse{}, err
	}
	defer httpResp.Body.Close()
	defer lease.Release()

	respBody, err := io.ReadAll(httpResp.Body)
	if err != nil {
		err = fmt.Errorf("read response body: %w", err)
		failAnthropicResponseLease(lease, httpResp, err)
		return providers.ChatResponse{}, err
	}
	if httpResp.StatusCode < 200 || httpResp.StatusCode >= 300 {
		snippet := string(respBody)
		if len(snippet) > 400 {
			snippet = snippet[:400]
		}
		body := fmt.Sprintf("%s: %s", httpResp.Status, snippet)
		err := &providers.HTTPError{
			ProviderFamily:  "anthropic",
			StatusCode:      httpResp.StatusCode,
			Body:            body,
			RetryAfter:      providers.ParseRetryAfter(httpResp),
			ContextOverflow: providers.DetectContextOverflow(body),
		}
		lease.FailError(err)
		return providers.ChatResponse{}, err
	}

	var parsed anthropicResponse
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		err = fmt.Errorf("parse response JSON: %w", err)
		lease.FailError(err)
		return providers.ChatResponse{}, err
	}
	if len(parsed.Content) == 0 {
		err := errors.New("provider returned empty content")
		lease.FailError(err)
		return providers.ChatResponse{}, err
	}

	var textParts []string
	reasoningBlocks := make([]providers.ReasoningBlock, 0, 2)
	toolCalls := make([]providers.ToolCall, 0, 2)
	for _, block := range parsed.Content {
		switch block.Type {
		case "text":
			if strings.TrimSpace(block.Text) != "" {
				textParts = append(textParts, block.Text)
			}
		case "thinking", "redacted_thinking":
			reasoningBlocks = append(reasoningBlocks, anthropicReasoningBlock(block))
		case "tool_use":
			args, err := json.Marshal(block.Input)
			if err != nil {
				err = fmt.Errorf("marshal tool_use input: %w", err)
				lease.FailError(err)
				return providers.ChatResponse{}, err
			}
			toolCalls = append(toolCalls, providers.ToolCall{
				ID:        block.ID,
				Name:      block.Name,
				Kind:      anthropicToolCallKind(block.Name),
				Arguments: string(args),
			})
		}
	}

	resp := providers.ChatResponse{
		Content:          strings.TrimSpace(strings.Join(textParts, "\n")),
		ReasoningContent: joinReasoningText(reasoningBlocks),
		ReasoningBlocks:  reasoningBlocks,
		ToolCalls:        toolCalls,
		StopReason:       strings.ToLower(parsed.StopReason),
	}
	if resp.StopReason == "max_tokens" {
		resp.Truncated = true
	}
	resp.FinishReason = anthropicFinishReason(resp.StopReason, resp.Truncated, len(toolCalls) > 0)
	if parsed.Usage != nil {
		resp.Usage = &providers.TokenUsage{
			InputTokens:         parsed.Usage.InputTokens,
			OutputTokens:        parsed.Usage.OutputTokens,
			CacheCreationTokens: parsed.Usage.CacheCreationTokens,
			CacheReadTokens:     parsed.Usage.CacheReadTokens,
		}
		c.normalizeInclusiveInput(resp.Usage)
		c.stampCacheCreationFlag(resp.Usage)
	}
	lease.SucceedWithUsage(resp.Usage)
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
	if err := providers.ValidateToolDefinitionsForProvider(anthropicToolSurfaceValidationTarget(req.Model), req.Tools); err != nil {
		return nil, err
	}
	var err error
	req, err = providers.EnsureInferenceAttemptContext(ctx, req, providers.InferenceOperationAuxiliary, providers.InferenceProfileInteractive)
	if err != nil {
		return nil, err
	}

	maxTok := resolveMaxTokens(req.MaxTokens, c.maxTokens, req.Model)
	payload, err := c.buildAnthropicRequest(req, maxTok, true)
	if err != nil {
		return nil, err
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	sseClient := providers.BuildStreamingHTTPClient(c.httpClient, c.streamConfig)
	// Use the single-attempt request — ReliableStreamClient handles retries
	// with proper UI feedback at the caller layer.
	providers.DebugLogf("StreamChat: sending %d bytes to %s/v1/messages (max_tokens=%d, model=%s, msgs=%d)",
		len(body), c.baseURL, maxTok, req.Model, len(req.Messages))
	// Dump request body for debugging 503 issues.
	if dumpPath := os.Getenv("WUU_DUMP_REQUEST"); dumpPath != "" {
		_ = os.WriteFile(dumpPath, body, 0o644)
		providers.DebugLogf("StreamChat: dumped request body to %s", dumpPath)
	}
	resp, lease, err := c.doSingleMessagesRequest(ctx, sseClient, body, payload.UseDefaultBetas, payload.Betas, req.Attempt, "stream")
	if err != nil {
		providers.DebugLogf("StreamChat: error: %v", err)
		return nil, err
	}

	ch := make(chan providers.StreamEvent, 64)
	go c.readSSEStream(ctx, resp, lease, ch)
	return ch, nil
}

type anthropicToolSearchSupport struct {
	BaseURL string
}

func (c *Client) buildAnthropicRequest(req providers.ChatRequest, maxTokens int, stream bool) (anthropicRequest, error) {
	return buildAnthropicRequestWithSupport(req, maxTokens, stream, anthropicToolSearchSupport{BaseURL: c.baseURL})
}

func buildAnthropicRequest(req providers.ChatRequest, maxTokens int, stream bool) (anthropicRequest, error) {
	return buildAnthropicRequestWithSupport(req, maxTokens, stream, anthropicToolSearchSupport{})
}

func buildAnthropicRequestWithSupport(req providers.ChatRequest, maxTokens int, stream bool, support anthropicToolSearchSupport) (anthropicRequest, error) {
	var videoErr error
	req, videoErr = providers.PrepareVideoInput(req, false)
	if videoErr != nil {
		return anthropicRequest{}, videoErr
	}
	prepared, err := providers.PrepareMessagesForProviderRequestWithPolicy(req.Provider, req.Model, req.Messages, req.MediaInput)
	if err != nil {
		return anthropicRequest{}, err
	}
	req.Messages = prepared
	toolSearchEnabled := shouldEnableAnthropicToolSearch(req, support)

	// Parse cache control options from provider options.
	// anthropicCache: "auto" (default) or "off" (disable all cache_control markers)
	// cacheTTL: "5m" (default) or "1h"
	cacheDisabled := false
	if cacheVal, ok := req.ProviderOptions["anthropicCache"].(string); ok {
		cacheVal = strings.TrimSpace(strings.ToLower(cacheVal))
		switch cacheVal {
		case "off":
			cacheDisabled = true
		case "auto", "":
			// Default: cache enabled
		default:
			// Invalid value, log and use default
			providers.DebugLogf("buildAnthropicRequest: invalid anthropicCache %q, using default", cacheVal)
		}
	}

	// thinkingReplay controls how historical assistant reasoning blocks are
	// replayed to the endpoint (models.<m>.options.thinking_replay):
	//   "full" (default) — replay thinking/redacted_thinking with signatures,
	//     the native Anthropic contract (and required by it for tool loops);
	//   "text" — degrade reasoning to a plain text block, for compatible
	//     endpoints that reject thinking blocks or validate signatures they
	//     did not issue (redacted blocks are dropped: their payload is opaque);
	//   "off"  — drop historical reasoning entirely.
	// Vendor tolerance varies and changes; this stays explicit per-model
	// config, never URL-detected.
	thinkingReplay := thinkingReplayFull
	for _, key := range []string{"thinking_replay", "thinkingReplay"} {
		val, ok := req.ProviderOptions[key].(string)
		if !ok {
			continue
		}
		switch mode := strings.TrimSpace(strings.ToLower(val)); mode {
		case thinkingReplayFull, thinkingReplayText, thinkingReplayOff:
			thinkingReplay = mode
		case "":
		default:
			providers.DebugLogf("buildAnthropicRequest: invalid %s %q, using %q", key, val, thinkingReplayFull)
		}
		break
	}
	allowEmptySignature := false
	for _, key := range []string{"allow_empty_signature", "allowEmptySignature"} {
		if enabled, ok := providerOptionBool(req.ProviderOptions[key]); ok {
			allowEmptySignature = enabled
			break
		}
	}
	useDefaultBetas := true
	for _, key := range []string{"anthropic_default_betas", "anthropicDefaultBetas"} {
		if enabled, ok := providerOptionBool(req.ProviderOptions[key]); ok {
			useDefaultBetas = enabled
			break
		}
	}

	cacheTTL := ""
	if ttlVal, ok := req.ProviderOptions["cacheTTL"].(string); ok {
		ttlVal = strings.TrimSpace(ttlVal)
		switch ttlVal {
		case "5m", "1h":
			cacheTTL = ttlVal
		case "":
			// Empty is valid, use default
		default:
			// Invalid TTL, log and ignore
			providers.DebugLogf("buildAnthropicRequest: invalid cacheTTL %q, ignoring", ttlVal)
		}
	}

	payload := anthropicRequest{
		Model:           req.Model,
		MaxTokens:       maxTokens,
		Messages:        make([]anthropicMessage, 0, len(req.Messages)),
		Stream:          stream,
		UseDefaultBetas: useDefaultBetas,
	}
	if toolSearchEnabled {
		payload.Betas = append(payload.Betas, toolSearchBetaHeader1P)
	}
	if req.Temperature > 0 {
		t := req.Temperature
		payload.Temperature = &t
	}

	systemTexts := make([]string, 0, 1)
	// stableTail tracks the payload coordinates of the last cacheable block
	// that came from a non-hidden source message. Hidden messages are
	// per-request volatile context (system reminders, injected state): a
	// cache marker placed on or after them can never be read back, so the
	// sliding tail marker must land on this boundary instead of the raw
	// payload tail.
	var stableTail *anthropicStableTail
	for _, msg := range req.Messages {
		if strings.EqualFold(msg.Role, "system") {
			systemTexts = append(systemTexts, msg.Content)
			continue
		}

		mapped, err := mapMessage(msg, toolSearchEnabled, thinkingReplay, allowEmptySignature)
		if err != nil {
			return anthropicRequest{}, err
		}
		destIdx := len(payload.Messages)
		blockBase := 0
		if n := len(payload.Messages); n > 0 && payload.Messages[n-1].Role == mapped.Role {
			// Anthropic's Messages API accepts mixed text + tool_result
			// blocks in a single user message.  Always merge consecutive
			// same-role messages instead of silently dropping when
			// canMergeBlocks returned false (the previous behaviour
			// caused tool results to vanish when a worker mailbox text
			// block landed between an assistant tool_call and its
			// tool_result).
			destIdx = n - 1
			blockBase = len(payload.Messages[n-1].Content)
			payload.Messages[n-1].Content = append(payload.Messages[n-1].Content, mapped.Content...)
		} else {
			payload.Messages = append(payload.Messages, mapped)
		}
		// Volatile context is flagged either by Hidden or by being a
		// system-reminder message (some call sites set only the name).
		if !msg.Hidden && !wuucontext.IsSystemReminder(msg.Name, msg.Content) {
			if last := lastCacheableBlockIndex(mapped.Content); last >= 0 {
				stableTail = &anthropicStableTail{Message: destIdx, Block: blockBase + last}
			}
		}
	}
	providers.DebugLogf("buildAnthropicRequest: %d input msgs → %d merged msgs", len(req.Messages), len(payload.Messages))
	if !cacheDisabled {
		applyAnthropicCacheHint(&payload, req.CacheHint, cacheTTL, stableTail)
	}
	for i := range payload.Messages {
		payload.Messages[i] = smooshSystemReminderBlocks(payload.Messages[i])
	}
	// Log the role sequence to diagnose alternation violations.
	if len(payload.Messages) > 0 {
		var roles strings.Builder
		for i, m := range payload.Messages {
			if i > 0 {
				roles.WriteString(",")
			}
			roles.WriteString(m.Role)
		}
		providers.DebugLogf("buildAnthropicRequest: role sequence: [%s]", roles.String())
	}
	payload.System = buildAnthropicSystem(systemTexts, req.CacheHint, cacheTTL, cacheDisabled)

	if len(req.Tools) > 0 {
		payload.Tools = buildAnthropicTools(
			req.Model,
			req.Tools,
			providers.DiscoveredToolNamesFromMessages(req.Messages),
			anthropicCompactedDiscoveredToolNames(req.Messages),
			toolSearchEnabled,
		)
		if req.ForceToolName != "" {
			// Forced tool choice for mechanical closing turns: the Anthropic
			// wire form pins a specific tool by name.
			payload.ToolChoice = map[string]any{"type": "tool", "name": req.ForceToolName}
		}
	}

	// Effort level: maps to output_config.effort. Empty = API default (high).
	if req.Effort != "" {
		payload.OutputConfig = &anthropicOutputConfig{Effort: req.Effort}
		// Adaptive thinking: when effort is set, enable thinking so the model
		// can use its reasoning budget.
		payload.Thinking = &anthropicThinking{Type: "adaptive"}
	}
	applyAnthropicProviderOptions(&payload, req.ProviderOptions)

	if modelvariant.AnthropicRequiresBoundThinking(req.Model) {
		// Wuu can compact history and change system reminders or discovered
		// tools between requests. Let the API drop only invalidated reasoning
		// instead of rejecting the whole continuation, while preserving valid
		// signed blocks unchanged. This also applies to newly created accounts.
		if payload.Thinking == nil {
			payload.Thinking = &anthropicThinking{}
		}
		payload.Thinking.Type = "adaptive"
		payload.Thinking.BudgetTokens = 0
		if payload.Thinking.Display == "" {
			payload.Thinking.Display = "summarized"
		}
		payload.Thinking.BlockBinding = map[string]string{"prefix_mismatch_behavior": "drop_block"}
		payload.Betas = append(payload.Betas, "thinking-binding-controls-2026-08-01")
		if payload.OutputConfig != nil && payload.OutputConfig.Effort == "none" {
			payload.OutputConfig.Effort = "low"
		}
		payload.Temperature = nil
		payload.TopP = nil
		payload.TopK = nil
		if payload.ToolChoice != nil {
			// These models reject forced tool_choice. Keep the closing-turn
			// requirement explicit in a supported mid-conversation instruction.
			payload.ToolChoice = map[string]any{"type": "auto"}
			payload.Messages = append(payload.Messages, anthropicMessage{
				Role:    "system",
				Content: []anthropicBlock{{Type: "text", Text: fmt.Sprintf("Call the %q tool for this turn. Begin your response with that tool call.", req.ForceToolName)}},
			})
		}
	}

	// Temperature gating, applied after all option sources:
	// - models that reject the field (per-model "temperature": false, arriving
	//   as the temperatureSupported option) never receive it — absolute;
	// - with thinking active the global sampling default is dropped (the API
	//   only accepts the default alongside thinking), but an explicit
	//   per-model options.temperature is a deliberate user override and wins.
	if !temperatureSupported(req.ProviderOptions) {
		payload.Temperature = nil
	} else if payload.Thinking != nil {
		if _, explicit := providerOptionFloat(req.ProviderOptions["temperature"]); !explicit {
			payload.Temperature = nil
		}
	}

	return payload, nil
}

// temperatureSupported reports whether the model accepts the temperature
// field. Defaults to true; per-model "temperature": false flips it via the
// temperatureSupported provider option (modelvariant.BaseOptionsForProvider).
func temperatureSupported(options map[string]any) bool {
	for _, key := range []string{"temperatureSupported", "temperature_supported"} {
		if value, exists := options[key]; exists {
			if enabled, ok := providerOptionBool(value); ok {
				return enabled
			}
		}
	}
	return true
}

func applyAnthropicProviderOptions(payload *anthropicRequest, options map[string]any) {
	if payload == nil || len(options) == 0 {
		return
	}
	if effort, ok := options["effort"].(string); ok && strings.TrimSpace(effort) != "" {
		if payload.OutputConfig == nil {
			payload.OutputConfig = &anthropicOutputConfig{}
		}
		payload.OutputConfig.Effort = strings.TrimSpace(effort)
	}
	if thinking, ok := providerOptionMap(options["thinking"]); ok {
		applyAnthropicThinkingOption(payload, thinking)
	}
	if speed, ok := options["speed"].(string); ok && strings.TrimSpace(speed) != "" {
		payload.Speed = strings.TrimSpace(speed)
	}
	if payload.Temperature == nil {
		if temperature, ok := providerOptionFloat(options["temperature"]); ok {
			payload.Temperature = &temperature
		}
	}
	if topP, ok := providerOptionFloat(options["topP"]); ok {
		payload.TopP = &topP
	}
	if topK, ok := providerOptionInt(options["topK"]); ok {
		payload.TopK = &topK
	}
}

func applyAnthropicThinkingOption(payload *anthropicRequest, option map[string]any) {
	thinkingType, _ := option["type"].(string)
	thinkingType = strings.TrimSpace(thinkingType)
	if thinkingType == "" {
		return
	}

	next := &anthropicThinking{Type: thinkingType}
	if display, ok := option["display"].(string); ok && strings.TrimSpace(display) != "" {
		next.Display = strings.TrimSpace(display)
	}
	if budget, ok := providerOptionInt(option["budgetTokens"]); ok {
		next.BudgetTokens = budget
	} else if budget, ok := providerOptionInt(option["budget_tokens"]); ok {
		next.BudgetTokens = budget
	}
	payload.Thinking = next
}

func providerOptionMap(value any) (map[string]any, bool) {
	out, ok := value.(map[string]any)
	return out, ok
}

func providerOptionInt(value any) (int, bool) {
	switch typed := value.(type) {
	case int:
		return typed, true
	case int64:
		return int(typed), true
	case float64:
		return int(typed), typed > 0
	case json.Number:
		parsed, err := typed.Int64()
		if err != nil {
			return 0, false
		}
		return int(parsed), true
	default:
		return 0, false
	}
}

func providerOptionFloat(value any) (float64, bool) {
	switch typed := value.(type) {
	case float64:
		return typed, true
	case float32:
		return float64(typed), true
	case int:
		return float64(typed), true
	case int64:
		return float64(typed), true
	case json.Number:
		n, err := typed.Float64()
		if err != nil {
			return 0, false
		}
		return n, true
	default:
		return 0, false
	}
}

func providerOptionBool(value any) (bool, bool) {
	switch typed := value.(type) {
	case bool:
		return typed, true
	case string:
		switch strings.ToLower(strings.TrimSpace(typed)) {
		case "1", "true", "yes", "on", "enabled":
			return true, true
		case "0", "false", "no", "off", "disabled":
			return false, true
		}
	}
	return false, false
}

func shouldEnableAnthropicToolSearch(req providers.ChatRequest, support anthropicToolSearchSupport) bool {
	if !hasToolSearchTool(req.Tools) {
		return false
	}
	if !req.NativeDeferredToolDiscovery {
		return false
	}
	return SupportsNativeToolSearchWhenExplicitlyEnabled(req.Model, req.ProviderOptions)
}

func anthropicToolSearchOption(options map[string]any) (bool, bool) {
	if len(options) == 0 {
		return false, false
	}
	// native_tool_search is the wire-neutral spelling shared with the OpenAI
	// Responses path; the anthropic-flavored aliases stay for back-compat.
	for _, key := range []string{"native_tool_search", "nativeToolSearch", "anthropicToolSearch", "toolSearch", "tool_search"} {
		value, exists := options[key]
		if !exists {
			continue
		}
		enabled, ok := providerOptionBool(value)
		if !ok {
			continue
		}
		return enabled, true
	}
	return false, false
}

func SupportsNativeToolSearchWhenExplicitlyEnabled(model string, options map[string]any) bool {
	if !modelSupportsAnthropicToolReference(model) {
		return false
	}
	if enabled, ok := anthropicToolSearchOption(options); ok && !enabled {
		return false
	}
	return true
}

// SupportsNativeToolSearchByDefault is the auto-mode gate. It keeps unknown
// compatible endpoints on ordinary flattened tools unless the provider options
// explicitly opt in.
func SupportsNativeToolSearchByDefault(baseURL, model string, options map[string]any) bool {
	if !modelSupportsAnthropicToolReference(model) {
		return false
	}
	if enabled, ok := anthropicToolSearchOption(options); ok {
		return enabled
	}
	return isFirstPartyAnthropicBaseURL(baseURL)
}

func hasToolSearchTool(defs []providers.ToolDefinition) bool {
	for _, def := range defs {
		if strings.EqualFold(def.Name, "tool_search") {
			return true
		}
	}
	return false
}

func modelSupportsAnthropicToolReference(model string) bool {
	normalized := strings.ToLower(strings.TrimSpace(model))
	if normalized == "" {
		return false
	}
	return !strings.Contains(normalized, "haiku")
}

func isFirstPartyAnthropicBaseURL(raw string) bool {
	u := strings.TrimRight(strings.ToLower(strings.TrimSpace(raw)), "/")
	return u == "https://api.anthropic.com" || u == "https://api.anthropic.com/v1"
}

func buildAnthropicSystem(systemTexts []string, hint *providers.CacheHint, cacheTTL string, cacheDisabled bool) any {
	if len(systemTexts) == 0 {
		return nil
	}
	if !shouldCacheAnthropicSystem(hint) || cacheDisabled {
		return strings.Join(systemTexts, "\n")
	}
	blocks := make([]anthropicSystemBlock, 0, len(systemTexts))
	for _, text := range systemTexts {
		blocks = append(blocks, anthropicSystemBlock{Type: "text", Text: text})
	}
	// The single breakpoint sits on the LAST system block, so Anthropic caches
	// the entire system prefix up to and including it. wuu assembles statics
	// plus the dynamic sections (memory / memdir index / skills) into one joined
	// string sent as a single system message (prompt.Builder.Build ->
	// stream_runner system message), so those dynamic segments live inside the
	// cached prefix rather than being excluded after the breakpoint. Even if a
	// caller ever splits system into multiple blocks, the end-of-system marker
	// still caches all earlier blocks. This block plus the optional compact
	// anchor and the sliding history tail stay within Anthropic's 4-breakpoint
	// budget. Byte-stability across turns is provided upstream: the assembled
	// system is frozen on the thread runner (recomputed only on thread rebuild /
	// clear, not per turn), so the cached prefix bytes do not drift mid-session.
	blocks[len(blocks)-1].CacheControl = ephemeralCacheControl(cacheTTL)
	return blocks
}

func shouldCacheAnthropicSystem(hint *providers.CacheHint) bool {
	return hint != nil && hint.StableSystem
}

func buildAnthropicTools(model string, defs []providers.ToolDefinition, discovered, compacted map[string]struct{}, toolSearchEnabled bool) []anthropicTool {
	if len(defs) == 0 {
		return nil
	}
	// Note: ToolDefinition.CacheStable is preserved for other call sites,
	// but we no longer emit per-tool cache markers. Instead, we apply
	// a single sliding tail marker at the request level (in applyAnthropicCacheHint),
	// which handles intra-turn tool loops more efficiently than per-tool prefixes.
	out := make([]anthropicTool, 0, len(defs))
	for _, tool := range defs {
		mapped := anthropicTool{
			Name:        tool.Name,
			Description: tool.Description,
			InputSchema: providers.ToolInputSchemaForModel(model, tool.InputSchema),
		}
		if toolSearchEnabled && shouldDeferAnthropicTool(tool, discovered, compacted) {
			mapped.DeferLoading = true
		}
		out = append(out, mapped)
	}
	return out
}

func shouldDeferAnthropicTool(tool providers.ToolDefinition, discovered, compacted map[string]struct{}) bool {
	if strings.EqualFold(tool.Name, "tool_search") {
		return false
	}
	if _, ok := compacted[tool.Name]; ok {
		return false
	}
	if tool.DeferLoading {
		return true
	}
	_, ok := discovered[tool.Name]
	return ok
}

func anthropicCompactedDiscoveredToolNames(messages []providers.ChatMessage) map[string]struct{} {
	compacted := providers.CompactedDiscoveredToolNamesFromMessages(messages)
	if len(compacted) == 0 {
		return compacted
	}
	raw := providers.ToolSearchResultToolNamesFromMessages(messages)
	for name := range raw {
		delete(compacted, name)
	}
	return compacted
}

// anthropicStableTail is the payload position (message index, absolute block
// index within that message's merged content) of the last cacheable block
// that originated from a non-hidden source message. It is captured during
// message mapping in buildAnthropicRequest, where source-message hiddenness
// is still known.
type anthropicStableTail struct {
	Message int
	Block   int
}

func applyAnthropicCacheHint(payload *anthropicRequest, hint *providers.CacheHint, cacheTTL string, stableTail *anthropicStableTail) {
	if payload == nil || hint == nil {
		return
	}
	if len(payload.Messages) == 0 {
		return
	}

	// Cache marker strategy for Anthropic:
	// 1. System block: mark the last system block when hint.StableSystem is set.
	//    This is done separately in buildAnthropicSystem.
	// 2. Compact anchor: when hint.HasCompactSummary, mark the first message
	//    as a cache anchor close to the rewritten history root.
	// 3. Sliding tail marker: mark the last cacheable block that came from a
	//    non-hidden source message. The single marker moves with every request
	//    round in an intra-turn tool loop, eliminating O(n²) re-caching and
	//    staying inside Anthropic's 20-block breakpoint lookback window.
	//    Hidden trailing context (per-request system reminders and similar)
	//    stays after the marker: those bytes never repeat across requests, so
	//    a marker beyond them could never produce a cache hit — and the
	//    smoosh pass below would delete a marker riding on a reminder block.
	// Budget: system (1) + optional compact anchor (1) + tail (1) = 2-3 of
	// the API maximum of 4 cache_control blocks per request.

	if hint.HasCompactSummary {
		markAnthropicMessageForCache(&payload.Messages[0], cacheTTL)
	}

	markAnthropicStableTailForCache(payload, cacheTTL, stableTail)
}

// markAnthropicStableTailForCache places the sliding tail marker. It prefers
// the tracked stable-tail position; when none was captured (e.g. every
// message is hidden, or the caller bypassed mapping in tests) it falls back
// to walking backward from the end of the payload.
func markAnthropicStableTailForCache(payload *anthropicRequest, cacheTTL string, stableTail *anthropicStableTail) bool {
	if payload == nil || len(payload.Messages) == 0 {
		return false
	}
	if stableTail != nil && stableTail.Message >= 0 && stableTail.Message < len(payload.Messages) {
		msg := &payload.Messages[stableTail.Message]
		if stableTail.Block >= 0 && stableTail.Block < len(msg.Content) {
			block := &msg.Content[stableTail.Block]
			if anthropicBlockSupportsCache(block) {
				block.CacheControl = ephemeralCacheControl(cacheTTL)
				return true
			}
		}
	}
	// Fallback: walk backward from the last message to find the last
	// cacheable block anywhere in the payload.
	for i := len(payload.Messages) - 1; i >= 0; i-- {
		if markAnthropicMessageForCache(&payload.Messages[i], cacheTTL) {
			return true
		}
	}
	return false
}

// lastCacheableBlockIndex returns the index of the last block in blocks that
// anthropicBlockSupportsCache accepts, or -1 when none qualify.
func lastCacheableBlockIndex(blocks []anthropicBlock) int {
	for i := len(blocks) - 1; i >= 0; i-- {
		if anthropicBlockSupportsCache(&blocks[i]) {
			return i
		}
	}
	return -1
}

func markAnthropicMessageForCache(msg *anthropicMessage, cacheTTL string) bool {
	if msg == nil {
		return false
	}
	for i := len(msg.Content) - 1; i >= 0; i-- {
		block := &msg.Content[i]
		if !anthropicBlockSupportsCache(block) {
			continue
		}
		block.CacheControl = ephemeralCacheControl(cacheTTL)
		return true
	}
	return false
}

func anthropicBlockSupportsCache(block *anthropicBlock) bool {
	if block == nil {
		return false
	}
	switch block.Type {
	case "text", "tool_use", "tool_result":
		return true
	default:
		return false
	}
}

func ephemeralCacheControl(ttl string) *anthropicCacheControl {
	cc := &anthropicCacheControl{Type: "ephemeral"}
	if ttl != "" {
		cc.TTL = ttl
	}
	return cc
}

// doSingleMessagesRequest is the protocol driver's only wire-send boundary.
// Recovery belongs to providers.ExecuteChat or ReliableStreamClient.
func (c *Client) doSingleMessagesRequest(
	ctx context.Context,
	httpClient *http.Client,
	body []byte,
	useDefaultBetas bool,
	extraBetas []string,
	attempt providers.InferenceAttempt,
	mode string,
) (*http.Response, *providers.ProviderLease, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.messagesURL, bytes.NewReader(body))
	if err != nil {
		return nil, nil, fmt.Errorf("build request: %w", err)
	}
	httpReq.Header.Set("content-type", "application/json")
	if c.apiKey != "" {
		httpReq.Header.Set("x-api-key", c.apiKey)
	}
	if c.authToken != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.authToken)
	}
	httpReq.Header.Set("anthropic-version", defaultAnthropicVersion)
	// Keep the established Anthropic feature betas by default. Compatible
	// endpoints can disable this first-party set through model metadata while
	// retaining OAuth, request-specific, and explicitly configured betas below.
	var betas []string
	if useDefaultBetas {
		betas = []string{
			"interleaved-thinking-2025-05-14",
			"prompt-caching-2024-07-31",
			"token-efficient-tools-2026-03-28",
		}
	}
	if c.authToken != "" {
		betas = append(betas, "oauth-2025-04-20")
	}
	betas = appendUniqueStrings(betas, extraBetas...)
	httpReq.Header.Set("User-Agent", "wuu/"+version.Version)
	httpReq.Header.Set("x-app", "wuu")
	httpReq.Header.Set("Accept", "text/event-stream")
	for k, v := range c.headers {
		if strings.EqualFold(k, "anthropic-beta") {
			betas = appendUniqueStrings(betas, splitAnthropicBetas(v)...)
			continue
		}
		httpReq.Header.Set(k, v)
	}
	if len(betas) > 0 {
		httpReq.Header.Set("anthropic-beta", strings.Join(betas, ","))
	}

	lease, err := c.coordinator.AcquireForAttempt(ctx, c.providerScope, attempt)
	if err != nil {
		return nil, nil, err
	}
	if _, err := lease.RecordSubmission(attempt, providers.InferenceSubmissionMeta{
		Provider:     "anthropic",
		Protocol:     "messages",
		Transport:    "http",
		Mode:         mode,
		RequestBytes: len(body),
	}); err != nil {
		lease.Release()
		return nil, nil, fmt.Errorf("journal messages submission: %w", err)
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
			ProviderFamily:  "anthropic",
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

func failAnthropicResponseLease(lease *providers.ProviderLease, resp *http.Response, err error) {
	if resp != nil && resp.Request != nil {
		if ctxErr := resp.Request.Context().Err(); ctxErr != nil {
			lease.FailError(ctxErr)
			return
		}
	}
	lease.FailError(err)
}

// blockState tracks an active content block during SSE streaming.
type blockState struct {
	blockType string // "text", "tool_use", or "thinking"
	toolID    string
	toolName  string
	argsJSON  strings.Builder
	reasoning providers.ReasoningBlock
}

func (c *Client) readSSEStream(ctx context.Context, resp *http.Response, lease *providers.ProviderLease, ch chan<- providers.StreamEvent) {
	defer close(ch)
	defer resp.Body.Close()
	defer lease.Release()

	emit := providers.NewStreamEmitter(ctx, ch)

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
		sawMessageStop bool
		sawStreamError bool
	)

	scanner := providers.NewSSEReader(resp.Body, resetIdle)
	for scanner.Scan() {
		if c.handleSSEEvent(scanner.Event(), &usage, &stopReason, blocks, lease, emit, &sawMessageStop, &sawStreamError) {
			return
		}
	}

	if err := scanner.Err(); err != nil {
		if idleFired.Load() {
			providers.DebugLogf("readSSEStream: idle timeout after %s", idleTimeout)
			err := fmt.Errorf("stream idle timeout after %s: %w", idleTimeout, context.DeadlineExceeded)
			failAnthropicResponseLease(lease, resp, err)
			emit.Send(providers.StreamEvent{Type: providers.EventError, Error: err})
			return
		}
		providers.DebugLogf("readSSEStream: scanner error: %v", err)
		err = fmt.Errorf("read SSE stream: %w", err)
		failAnthropicResponseLease(lease, resp, err)
		emit.Send(providers.StreamEvent{Type: providers.EventError, Error: err})
		return
	}
	if idleFired.Load() {
		providers.DebugLogf("readSSEStream: idle timeout (post-scan) after %s", idleTimeout)
		err := fmt.Errorf("stream idle timeout after %s: %w", idleTimeout, context.DeadlineExceeded)
		failAnthropicResponseLease(lease, resp, err)
		emit.Send(providers.StreamEvent{Type: providers.EventError, Error: err})
		return
	}
	if !sawMessageStop && !sawStreamError {
		providers.DebugLogf("readSSEStream: incomplete stream (no message_stop)")
		err := providers.NewIncompleteStreamError("stream closed before message_stop")
		failAnthropicResponseLease(lease, resp, err)
		emit.Send(providers.StreamEvent{
			Type:  providers.EventError,
			Error: err,
		})
	}
}

// handleSSEEvent processes one SSE event and reports whether the reader must
// stop: after the terminal message_stop/error events, or once the request
// context ended and no further event can be delivered.
func (c *Client) handleSSEEvent(
	raw providers.SSEEvent,
	usage *providers.TokenUsage,
	stopReason *string,
	blocks map[int]*blockState,
	lease *providers.ProviderLease,
	emit *providers.StreamEmitter,
	sawMessageStop *bool,
	sawStreamError *bool,
) bool {
	switch raw.Event {
	case "message_start":
		var p messageStartPayload
		if json.Unmarshal([]byte(raw.Data), &p) == nil {
			usage.InputTokens = p.Message.Usage.InputTokens
			usage.CacheCreationTokens = p.Message.Usage.CacheCreationTokens
			usage.CacheReadTokens = p.Message.Usage.CacheReadTokens
			c.normalizeInclusiveInput(usage)
			c.stampCacheCreationFlag(usage)
		}
	case "content_block_start":
		var p contentBlockStartPayload
		if json.Unmarshal([]byte(raw.Data), &p) == nil {
			bs := &blockState{
				blockType: p.ContentBlock.Type,
				reasoning: providers.ReasoningBlock{
					Type:      p.ContentBlock.Type,
					Thinking:  p.ContentBlock.Thinking,
					Signature: p.ContentBlock.Signature,
					Data:      p.ContentBlock.Data,
				},
			}
			if p.ContentBlock.Type == "tool_use" {
				bs.toolID = p.ContentBlock.ID
				bs.toolName = p.ContentBlock.Name
				if !emit.Send(providers.StreamEvent{Type: providers.EventToolUseStart, ToolCall: &providers.ToolCall{ID: p.ContentBlock.ID, Name: p.ContentBlock.Name, Kind: anthropicToolCallKind(p.ContentBlock.Name)}}) {
					return true
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
				if !emit.Send(providers.StreamEvent{Type: providers.EventContentDelta, Content: p.Delta.Text}) {
					return true
				}
			case "input_json_delta":
				if bs != nil {
					bs.argsJSON.WriteString(p.Delta.PartialJSON)
				}
				if !emit.Send(providers.StreamEvent{Type: providers.EventToolUseDelta, Content: p.Delta.PartialJSON}) {
					return true
				}
			case "thinking_delta":
				if bs != nil {
					bs.reasoning.Thinking += p.Delta.Thinking
				}
				if !emit.Send(providers.StreamEvent{Type: providers.EventThinkingDelta, Content: p.Delta.Thinking}) {
					return true
				}
			case "signature_delta":
				if bs != nil {
					bs.reasoning.Signature += p.Delta.Signature
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
					if !emit.Send(providers.StreamEvent{Type: providers.EventToolUseEnd, ToolCall: &providers.ToolCall{ID: bs.toolID, Name: bs.toolName, Kind: anthropicToolCallKind(bs.toolName), Arguments: bs.argsJSON.String()}}) {
						return true
					}
				}
				if bs.blockType == "thinking" || bs.blockType == "redacted_thinking" {
					reasoningBlock := bs.reasoning
					if !emit.Send(providers.StreamEvent{Type: providers.EventThinkingDone, ReasoningBlock: &reasoningBlock}) {
						return true
					}
				}
			}
			delete(blocks, idx.Index)
		}
	case "message_delta":
		var p messageDeltaPayload
		if json.Unmarshal([]byte(raw.Data), &p) == nil {
			usageUpdated := false
			if p.Usage.InputTokens != nil {
				usage.InputTokens = *p.Usage.InputTokens
				usageUpdated = true
			}
			if p.Usage.OutputTokens != nil {
				usage.OutputTokens = *p.Usage.OutputTokens
				usageUpdated = true
			}
			if p.Usage.CacheCreationTokens != nil {
				usage.CacheCreationTokens = *p.Usage.CacheCreationTokens
				usageUpdated = true
			}
			if p.Usage.CacheReadTokens != nil {
				usage.CacheReadTokens = *p.Usage.CacheReadTokens
				usageUpdated = true
			}
			// Some compatible endpoints (MiniMax among them) report the real
			// usage only in message_delta, overwriting whatever message_start
			// carried. Re-normalize exactly when this event re-reported
			// input_tokens: the overwrite above reverted InputTokens to the
			// raw endpoint value, so one paired subtraction is correct. When
			// the delta carries no input_tokens the previously normalized
			// value is still in place and normalizing again would
			// double-subtract (the helper is not idempotent).
			if p.Usage.InputTokens != nil {
				c.normalizeInclusiveInput(usage)
			}
			if usageUpdated {
				if !emit.Send(providers.StreamEvent{Type: providers.EventUsage, Usage: &providers.TokenUsage{InputTokens: usage.InputTokens, OutputTokens: usage.OutputTokens, CacheCreationTokens: usage.CacheCreationTokens, CacheReadTokens: usage.CacheReadTokens, CacheCreationUnknown: usage.CacheCreationUnknown}}) {
					return true
				}
			}
			if p.Delta.StopReason != "" {
				*stopReason = strings.ToLower(p.Delta.StopReason)
			}
		}
	case "message_stop":
		if sawMessageStop != nil {
			*sawMessageStop = true
		}
		truncated := *stopReason == "max_tokens"
		terminalUsage := &providers.TokenUsage{InputTokens: usage.InputTokens, OutputTokens: usage.OutputTokens, CacheCreationTokens: usage.CacheCreationTokens, CacheReadTokens: usage.CacheReadTokens, CacheCreationUnknown: usage.CacheCreationUnknown}
		lease.SucceedWithUsage(terminalUsage)
		emit.Send(providers.StreamEvent{
			Type:         providers.EventDone,
			Usage:        terminalUsage,
			StopReason:   *stopReason,
			FinishReason: anthropicFinishReason(*stopReason, truncated, false),
			Truncated:    truncated,
		})
		return true
	case "error":
		if sawStreamError != nil {
			*sawStreamError = true
		}
		providers.DebugLogfWire("readSSEStream: error event: %s", raw.Data)
		var p anthropicErrorPayload
		if err := json.Unmarshal([]byte(raw.Data), &p); err == nil {
			// Native Anthropic carries the machine-readable class in
			// error.type ("overloaded_error", "api_error", ...); compatible
			// endpoints such as MiniMax use error.code instead. Feed whichever
			// is present into classification so a retryable in-stream failure
			// is not demoted to message-substring matching.
			code := strings.TrimSpace(p.Error.Type)
			if code == "" {
				code = strings.TrimSpace(p.Error.Code)
			}
			providers.DebugLogf("readSSEStream: error type=%s code=%s msg=%s", p.Error.Type, p.Error.Code, p.Error.Message)
			streamErr := providers.NewProviderStreamError(code, p.Error.Message)
			streamErr.ProviderFamily = "anthropic"
			lease.FailError(streamErr)
			emit.Send(providers.StreamEvent{
				Type:  providers.EventError,
				Error: streamErr,
			})
			return true
		}
		streamErr := providers.NewProviderStreamError("", raw.Data)
		streamErr.ProviderFamily = "anthropic"
		lease.FailError(streamErr)
		emit.Send(providers.StreamEvent{
			Type:  providers.EventError,
			Error: streamErr,
		})
		return true
	}
	return emit.Aborted()
}

// Thinking replay modes for historical assistant reasoning blocks. See the
// thinking_replay option parsing in buildAnthropicRequest.
const (
	thinkingReplayFull = "full"
	thinkingReplayText = "text"
	thinkingReplayOff  = "off"
)

func mapMessage(msg providers.ChatMessage, toolSearchEnabled bool, thinkingReplay string, allowEmptySignature bool) (anthropicMessage, error) {
	switch msg.Role {
	case "user":
		blocks := make([]anthropicBlock, 0, len(msg.Images)+len(msg.Files)+1)
		text := msg.Content
		if strings.TrimSpace(text) == "" {
			text = " "
		}
		blocks = append(blocks, anthropicBlock{Type: "text", Text: text})
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
		for _, file := range msg.Files {
			data := strings.TrimSpace(file.Data)
			if data == "" {
				continue
			}
			mediaType := strings.TrimSpace(file.MediaType)
			if mediaType == "" {
				mediaType = "application/octet-stream"
			}
			blocks = append(blocks, anthropicBlock{Type: "document", Source: &anthropicImageSource{Type: "base64", MediaType: mediaType, Data: data}})
		}
		return anthropicMessage{Role: msg.Role, Content: blocks}, nil
	case "assistant":
		reasoningBlocks := msg.ReasoningBlocks
		if len(reasoningBlocks) == 0 && strings.TrimSpace(msg.ReasoningContent) != "" {
			// Backward compatibility: sessions persisted before native
			// thinking-block support only have ReasoningContent text.
			reasoningBlocks = []providers.ReasoningBlock{{
				Type:     "thinking",
				Thinking: msg.ReasoningContent,
			}}
		}
		blocks := make([]anthropicBlock, 0, len(reasoningBlocks)+len(msg.ToolCalls)+1)
		switch thinkingReplay {
		case thinkingReplayOff:
			// Historical reasoning dropped entirely.
		case thinkingReplayText:
			// Degrade to a plain text block for endpoints that reject
			// thinking blocks or foreign signatures. Redacted blocks carry
			// only an opaque payload and cannot be textified — dropped.
			if reasoning := joinReasoningText(reasoningBlocks); reasoning != "" {
				blocks = append(blocks, anthropicBlock{Type: "text", Text: "<thinking>\n" + reasoning + "\n</thinking>"})
			}
		default:
			blocks = append(blocks, mapReasoningBlocks(reasoningBlocks, allowEmptySignature)...)
		}
		// Assistant tool-use turns should replay provider-native
		// thinking blocks before tool_use. Only synthesize a text
		// block when there is visible assistant text, or when this
		// would otherwise become an empty message.
		if strings.TrimSpace(msg.Content) != "" || (len(blocks) == 0 && len(msg.ToolCalls) == 0) {
			text := msg.Content
			if strings.TrimSpace(text) == "" {
				text = " "
			}
			blocks = append(blocks, anthropicBlock{Type: "text", Text: text})
		}
		for _, call := range msg.ToolCalls {
			var input any
			raw := strings.TrimSpace(call.Arguments)
			if raw == "" {
				input = map[string]any{}
			} else if err := json.Unmarshal([]byte(raw), &input); err != nil {
				// PrepareMessagesForModelRequest only allows invalid JSON
				// here when the paired tool_result tells the model the
				// arguments were invalid. Anthropic still requires an
				// object-shaped tool_use input for replay.
				input = map[string]any{}
			}
			blocks = append(blocks, anthropicBlock{Type: "tool_use", ID: call.ID, Name: call.Name, Input: input})
		}
		return anthropicMessage{Role: msg.Role, Content: blocks}, nil
	case "tool":
		return anthropicMessage{Role: "user", Content: []anthropicBlock{anthropicToolResultBlock(msg, toolSearchEnabled)}}, nil
	default:
		return anthropicMessage{}, fmt.Errorf("unsupported message role %q", msg.Role)
	}
}

func anthropicToolResultBlock(msg providers.ChatMessage, toolSearchEnabled bool) anthropicBlock {
	if toolSearchEnabled && isAnthropicToolSearchResult(msg) {
		refs := anthropicToolReferencesFromResult(msg.Content)
		if len(refs) > 0 {
			return anthropicBlock{Type: "tool_result", ToolUseID: msg.ToolCallID, Content: refs}
		}
		return anthropicBlock{Type: "tool_result", ToolUseID: msg.ToolCallID, Content: "No matching deferred tools found"}
	}
	if toolSearchEnabled && len(msg.DiscoveredTools) > 0 {
		refs := anthropicToolReferencesFromLoadable(msg.DiscoveredTools)
		if len(refs) > 0 {
			content := make([]anthropicBlock, 0, len(refs)+1)
			if strings.TrimSpace(msg.Content) != "" {
				content = append(content, anthropicBlock{Type: "text", Text: msg.Content})
			}
			content = append(content, refs...)
			return anthropicBlock{Type: "tool_result", ToolUseID: msg.ToolCallID, Content: content}
		}
	}
	return anthropicBlock{Type: "tool_result", ToolUseID: msg.ToolCallID, Content: msg.Content}
}

func isAnthropicToolSearchResult(msg providers.ChatMessage) bool {
	return providers.IsToolSearchResultMessage(msg)
}

func anthropicToolReferencesFromResult(content string) []anthropicBlock {
	tools := anthropicLoadableToolsFromResult(content)
	return anthropicToolReferencesFromLoadable(tools)
}

func anthropicToolReferencesFromLoadable(tools []providers.LoadableToolDefinition) []anthropicBlock {
	if len(tools) == 0 {
		return nil
	}
	refs := make([]anthropicBlock, 0, len(tools))
	for _, tool := range tools {
		name := strings.TrimSpace(tool.Name)
		if name == "" {
			continue
		}
		refs = append(refs, anthropicBlock{Type: "tool_reference", ToolName: name})
	}
	return refs
}

func anthropicLoadableToolsFromResult(content string) []providers.LoadableToolDefinition {
	return providers.LoadableToolsFromToolSearchResult(content)
}

func anthropicToolCallKind(name string) providers.ToolCallKind {
	if strings.EqualFold(name, "tool_search") {
		return providers.ToolCallKindToolSearch
	}
	return ""
}

func mapReasoningBlocks(blocks []providers.ReasoningBlock, allowEmptySignature bool) []anthropicBlock {
	if len(blocks) == 0 {
		return nil
	}
	out := make([]anthropicBlock, 0, len(blocks))
	for _, block := range blocks {
		switch block.Type {
		case "thinking":
			thinking := block.Thinking
			var signature *string
			if block.Signature != "" || allowEmptySignature {
				signatureValue := block.Signature
				signature = &signatureValue
			}
			out = append(out, anthropicBlock{
				Type:      "thinking",
				Thinking:  &thinking,
				Signature: signature,
			})
		case "redacted_thinking":
			out = append(out, anthropicBlock{Type: "redacted_thinking", Data: block.Data})
		}
	}
	return out
}

func anthropicReasoningBlock(block anthropicBlock) providers.ReasoningBlock {
	thinking := ""
	if block.Thinking != nil {
		thinking = *block.Thinking
	}
	signature := ""
	if block.Signature != nil {
		signature = *block.Signature
	}
	return providers.ReasoningBlock{
		Type:      block.Type,
		Thinking:  thinking,
		Signature: signature,
		Data:      block.Data,
	}
}

func joinReasoningText(blocks []providers.ReasoningBlock) string {
	if len(blocks) == 0 {
		return ""
	}
	parts := make([]string, 0, len(blocks))
	for _, block := range blocks {
		if strings.TrimSpace(block.Thinking) != "" {
			parts = append(parts, block.Thinking)
		}
	}
	return strings.TrimSpace(strings.Join(parts, "\n"))
}

func smooshSystemReminderBlocks(msg anthropicMessage) anthropicMessage {
	if msg.Role != "user" || len(msg.Content) == 0 {
		return msg
	}

	lastToolResultIdx := -1
	reminders := make([]string, 0, 1)
	kept := make([]anthropicBlock, 0, len(msg.Content))
	for _, block := range msg.Content {
		if block.Type == "text" && wuucontext.IsSystemReminder("", block.Text) {
			reminders = append(reminders, strings.TrimSpace(block.Text))
			continue
		}
		kept = append(kept, block)
		if block.Type == "tool_result" {
			lastToolResultIdx = len(kept) - 1
		}
	}
	if lastToolResultIdx < 0 || len(reminders) == 0 {
		return msg
	}

	toolResult := kept[lastToolResultIdx]
	if toolResult.CacheControl != nil {
		return msg
	}
	parts := make([]string, 0, 1+len(reminders))
	existingContent, ok := toolResult.Content.(string)
	if !ok {
		return msg
	}
	if existing := strings.TrimSpace(existingContent); existing != "" {
		parts = append(parts, existing)
	}
	parts = append(parts, reminders...)
	toolResult.Content = strings.Join(parts, "\n\n")
	kept[lastToolResultIdx] = toolResult
	msg.Content = kept
	return msg
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

func appendUniqueStrings(base []string, values ...string) []string {
	seen := make(map[string]struct{}, len(base)+len(values))
	for _, value := range base {
		seen[value] = struct{}{}
	}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		base = append(base, value)
		seen[value] = struct{}{}
	}
	return base
}

func splitAnthropicBetas(header string) []string {
	parts := strings.Split(header, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

type anthropicRequest struct {
	Model           string                 `json:"model"`
	System          any                    `json:"system,omitempty"`
	MaxTokens       int                    `json:"max_tokens"`
	Temperature     *float64               `json:"temperature,omitempty"`
	TopP            *float64               `json:"top_p,omitempty"`
	TopK            *int                   `json:"top_k,omitempty"`
	Messages        []anthropicMessage     `json:"messages"`
	Tools           []anthropicTool        `json:"tools,omitempty"`
	ToolChoice      any                    `json:"tool_choice,omitempty"`
	Stream          bool                   `json:"stream,omitempty"`
	Speed           string                 `json:"speed,omitempty"`
	Thinking        *anthropicThinking     `json:"thinking,omitempty"`
	OutputConfig    *anthropicOutputConfig `json:"output_config,omitempty"`
	UseDefaultBetas bool                   `json:"-"`
	Betas           []string               `json:"-"`
}

// anthropicThinking configures extended thinking.
type anthropicThinking struct {
	Type         string            `json:"type"`                    // "adaptive" or "enabled"
	BudgetTokens int               `json:"budget_tokens,omitempty"` // only for type=enabled
	Display      string            `json:"display,omitempty"`
	BlockBinding map[string]string `json:"block_binding,omitempty"`
}

// anthropicOutputConfig controls output behavior.
type anthropicOutputConfig struct {
	Effort string `json:"effort,omitempty"` // "low", "medium", "high", "max"
}

type anthropicMessage struct {
	Role    string           `json:"role"`
	Content []anthropicBlock `json:"content"`
}

type anthropicBlock struct {
	Type         string                 `json:"type"`
	Text         string                 `json:"text,omitempty"`
	Thinking     *string                `json:"thinking,omitempty"`
	Signature    *string                `json:"signature,omitempty"`
	Data         string                 `json:"data,omitempty"`
	Source       *anthropicImageSource  `json:"source,omitempty"`
	ID           string                 `json:"id,omitempty"`
	Name         string                 `json:"name,omitempty"`
	Input        any                    `json:"input,omitempty"`
	ToolUseID    string                 `json:"tool_use_id,omitempty"`
	Content      any                    `json:"content,omitempty"`
	ToolName     string                 `json:"tool_name,omitempty"`
	CacheControl *anthropicCacheControl `json:"cache_control,omitempty"`
}

type anthropicSystemBlock struct {
	Type         string                 `json:"type"`
	Text         string                 `json:"text"`
	CacheControl *anthropicCacheControl `json:"cache_control,omitempty"`
}

type anthropicCacheControl struct {
	Type string `json:"type"`
	TTL  string `json:"ttl,omitempty"`
}

type anthropicImageSource struct {
	Type      string `json:"type"`
	MediaType string `json:"media_type"`
	Data      string `json:"data"`
}

type anthropicTool struct {
	Name         string                 `json:"name"`
	Description  string                 `json:"description,omitempty"`
	InputSchema  map[string]any         `json:"input_schema"`
	DeferLoading bool                   `json:"defer_loading,omitempty"`
	CacheControl *anthropicCacheControl `json:"cache_control,omitempty"`
}

func anthropicFinishReason(stopReason string, truncated, hasToolCalls bool) providers.FinishReason {
	if strings.EqualFold(strings.TrimSpace(stopReason), "pause_turn") && !truncated && !hasToolCalls {
		return providers.FinishReasonContinue
	}
	return providers.NormalizeFinishReason(stopReason, truncated, hasToolCalls)
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

type anthropicErrorPayload struct {
	Error struct {
		// Type is the native Anthropic error class (e.g. "overloaded_error");
		// Code is the numeric class used by compatible endpoints (e.g.
		// MiniMax's "1305"). Native streams leave Code empty and vice versa.
		Type    string `json:"type"`
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
		Type      string `json:"type"`
		ID        string `json:"id,omitempty"`
		Name      string `json:"name,omitempty"`
		Input     any    `json:"input,omitempty"`
		Thinking  string `json:"thinking,omitempty"`
		Signature string `json:"signature,omitempty"`
		Data      string `json:"data,omitempty"`
	} `json:"content_block"`
}

type contentBlockDeltaPayload struct {
	Index int `json:"index"`
	Delta struct {
		Type        string `json:"type"`
		Text        string `json:"text,omitempty"`
		PartialJSON string `json:"partial_json,omitempty"`
		Thinking    string `json:"thinking,omitempty"`
		Signature   string `json:"signature,omitempty"`
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
