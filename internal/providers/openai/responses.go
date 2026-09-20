package openai

import (
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

	"github.com/blueberrycongee/wuu/internal/provideroptions"
	"github.com/blueberrycongee/wuu/internal/providers"
)

const responsesFinalAnswerTailGrace = 2 * time.Second

func (c *Client) responsesChat(ctx context.Context, req providers.ChatRequest) (providers.ChatResponse, error) {
	payload, err := c.buildResponsesRequest(req, false)
	if err != nil {
		return providers.ChatResponse{}, err
	}

	body, err := marshalResponsesRequest(payload)
	if err != nil {
		return providers.ChatResponse{}, fmt.Errorf("marshal request: %w", err)
	}

	httpResp, lease, err := c.doSingleResponsesRequest(ctx, c.httpClient, body, false, payload.ExtraHeaders, payload.Attempt, payload.SubmissionReason)
	if err != nil {
		return providers.ChatResponse{}, err
	}
	defer httpResp.Body.Close()
	defer lease.Release()

	respBody, err := io.ReadAll(httpResp.Body)
	if err != nil {
		err = fmt.Errorf("read response body: %w", err)
		failOpenAIResponseLease(lease, httpResp, err)
		return providers.ChatResponse{}, err
	}

	var parsed responsesResponse
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		err = fmt.Errorf("parse response JSON: %w", err)
		lease.FailError(err)
		return providers.ChatResponse{}, err
	}
	if parsed.Error != nil {
		err := parsed.Error.asError()
		lease.FailError(err)
		return providers.ChatResponse{}, err
	}
	response, err := parsed.asChatResponse(req.Model)
	if err != nil {
		lease.FailError(err)
		return providers.ChatResponse{}, err
	}
	lease.SucceedWithUsage(response.Usage)
	return response, nil
}

func (c *Client) responsesStreamChat(ctx context.Context, req providers.ChatRequest) (<-chan providers.StreamEvent, error) {
	payload, err := c.buildResponsesRequest(req, true)
	if err != nil {
		return nil, err
	}

	if responsesTransportUsesWebSocket(c.responsesTransport) {
		submissionsBefore := len(payload.Attempt.SubmissionSnapshot())
		ch, err := c.responsesStreamChatWebSocket(ctx, payload, c.responsesTransport)
		if err == nil {
			return ch, nil
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if len(payload.Attempt.SubmissionSnapshot()) > submissionsBefore {
			return nil, err
		}
		providers.DebugLogf("Responses websocket unavailable before stream start, falling back to SSE: %v", err)
		reason := responsesWebSocketFallbackReason(err)
		payload.SubmissionReason = reason
		return c.responsesStreamChatSSE(ctx, payload, responsesSSEProviderState(payload, c.responsesTransport, reason, responsesWebSocketFallbackMetaFromError(err)))
	}

	return c.responsesStreamChatSSE(ctx, payload, responsesSSEProviderState(payload, c.responsesTransport, "", responsesWebSocketFallbackMeta{}))
}

func (c *Client) responsesStreamChatSSE(ctx context.Context, payload responsesRequest, state *providers.ProviderStateSummary) (<-chan providers.StreamEvent, error) {
	body, err := marshalResponsesRequest(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	streamClient := newStreamingHTTPClient(c.httpClient, c.streamConfig)
	resp, lease, err := c.doSingleResponsesRequest(ctx, streamClient, body, true, payload.ExtraHeaders, payload.Attempt, payload.SubmissionReason)
	if err != nil {
		return nil, err
	}

	ch := make(chan providers.StreamEvent, 64)
	go c.readResponsesSSE(ctx, resp, lease, ch, state)
	return ch, nil
}

// CompactResponsesV2 invokes provider-native remote compaction over the normal
// streaming Responses endpoint. The request differs from an inference request
// only by its trailing compaction_trigger item.
func (c *Client) CompactResponsesV2(ctx context.Context, req providers.ChatRequest) (providers.ProviderItem, *providers.TokenUsage, error) {
	payload, err := c.buildResponsesRequest(req, true)
	if err != nil {
		return providers.ProviderItem{}, nil, err
	}
	payload.Input = append(payload.Input, responsesInputItem{Raw: json.RawMessage(`{"type":"compaction_trigger"}`)})
	payload.MaxOutputTokens = 0

	ch, err := c.responsesStreamChatSSE(ctx, payload, nil)
	if err != nil {
		return providers.ProviderItem{}, nil, err
	}
	var item providers.ProviderItem
	var count int
	for event := range ch {
		switch event.Type {
		case providers.EventProviderItem:
			if event.ProviderItem == nil || event.ProviderItem.Type != "compaction" {
				continue
			}
			item = *event.ProviderItem
			count++
		case providers.EventError:
			if event.Error == nil {
				event.Error = errors.New("remote compaction failed")
			}
			return providers.ProviderItem{}, nil, event.Error
		case providers.EventDone:
			if event.ProviderEventType != "response.completed" || event.Truncated {
				return providers.ProviderItem{}, event.Usage, fmt.Errorf("remote compaction did not complete: %s", event.ProviderEventType)
			}
			if count != 1 {
				return providers.ProviderItem{}, event.Usage, fmt.Errorf("remote compaction returned %d compaction items", count)
			}
			item.Provider = strings.TrimSpace(req.Provider)
			item.Scope = strings.TrimSpace(req.ProviderStateScope)
			return item, event.Usage, nil
		}
	}
	return providers.ProviderItem{}, nil, errors.New("remote compaction stream ended before response.completed")
}

func responsesSSEProviderState(payload responsesRequest, configuredTransport providers.StreamTransportMode, fallbackReason string, fallback responsesWebSocketFallbackMeta) *providers.ProviderStateSummary {
	fallbackReason = strings.TrimSpace(fallbackReason)
	state := &providers.ProviderStateSummary{
		Provider:            "openai",
		Protocol:            "responses_sse",
		Transport:           "http",
		ConfiguredTransport: string(configuredTransport),
		ReplayMode:          "full_request",
		FallbackActive:      fallbackReason != "",
		FallbackReason:      fallbackReason,
		InputItems:          len(payload.Input),
		FullInputItems:      len(payload.Input),
		DeltaInputItems:     0,
		ConnectionReused:    false,
	}
	if fallbackReason != "" {
		state.Diagnostic = "provider_transport_failure"
		state.TransportFailurePhase = responsesWebSocketFallbackPhase(fallbackReason)
		state.FailedTransport = "websocket"
		state.FallbackTransport = "http"
		state.EventsEmitted = state.TransportFailurePhase == "after_message_stream_start"
		applyResponsesWebSocketFallbackMeta(state, fallback)
	}
	return state
}

func (c *Client) buildResponsesRequest(req providers.ChatRequest, stream bool) (responsesRequest, error) {
	if strings.TrimSpace(req.Model) == "" {
		return responsesRequest{}, errors.New("model is required")
	}
	if len(req.Messages) == 0 {
		return responsesRequest{}, errors.New("messages is required")
	}

	var videoErr error
	req, videoErr = providers.PrepareVideoInput(req, false)
	if videoErr != nil {
		return responsesRequest{}, videoErr
	}
	resolved := providers.ResolveProviderHistory(req.Messages, req.Provider, req.ProviderStateScope)
	prepared, err := providers.PrepareMessagesForProviderRequestWithPolicy(req.Provider, req.Model, resolved, req.MediaInput)
	if err != nil {
		return responsesRequest{}, err
	}

	instructions, messages := splitResponsesInstructions(prepared)
	nativeDeferred := req.NativeDeferredToolDiscovery
	input := make([]responsesInputItem, 0, len(messages))
	if nativeDeferred {
		if tools := responsesCompactedDiscoveredTools(req.Model, prepared); len(tools) > 0 {
			input = append(input, responsesAdditionalToolsInputItem(tools))
		}
	}
	for _, msg := range messages {
		input = appendResponsesInputItem(input, msg, req.Provider, req.ProviderStateScope, req.Model, nativeDeferred)
	}

	payload := responsesRequest{
		Model:           req.Model,
		Instructions:    instructions,
		Input:           input,
		Temperature:     effectiveTemperature(req),
		MaxOutputTokens: req.MaxTokens,
		Stream:          stream,
		Options:         provideroptions.Clone(req.ProviderOptions),
		Attempt:         req.Attempt,
	}
	if !providerOptionEnabled(req.ProviderOptions, "omitStore") {
		if c.responsesStore != nil {
			payload.Store = c.responsesStore
		} else {
			store := false
			payload.Store = &store
		}
	}
	if !providerOptionEnabled(req.ProviderOptions, "omitPromptCacheKey") {
		if key := responsePromptCacheKey(req.CacheHint); key != "" {
			payload.PromptCacheKey = key
		}
	}
	if strings.TrimSpace(req.Effort) != "" {
		payload.Reasoning = &responsesReasoning{Effort: req.Effort}
	}
	if len(req.Tools) > 0 {
		discoveredToolNames := map[string]struct{}{}
		if nativeDeferred {
			discoveredToolNames = providers.DiscoveredToolNamesFromMessages(prepared)
		}
		tools := make([]responsesToolDefinition, 0, len(req.Tools))
		for _, tool := range req.Tools {
			if shouldOmitResponsesTopLevelTool(tool, discoveredToolNames) {
				continue
			}
			tools = append(tools, responsesToolDefinitionFromProvider(req.Model, tool, nativeDeferred))
		}
		if len(tools) > 0 {
			payload.ToolChoice = "auto"
			if req.ForceToolName != "" {
				// Forced tool choice for mechanical closing turns. The
				// Responses wire form pins a function by name and is flatter
				// than Chat Completions (no nested "function" object).
				payload.ToolChoice = map[string]any{
					"type": "function",
					"name": req.ForceToolName,
				}
			}
			payload.Tools = tools
		}
	}

	// Sticky-routing headers for Responses-compatible backends. Some backends
	// key turn-local state off session-id / x-client-request-id. Public OpenAI
	// ignores these fields, so it is safe to attach them whenever a cache hint
	// exists.
	if req.CacheHint != nil && !providerOptionEnabled(req.ProviderOptions, "omitPromptCacheKey") {
		if key := strings.TrimSpace(req.CacheHint.PromptCacheKey); key != "" {
			payload.ExtraHeaders = map[string]string{
				"session-id":          key,
				"x-client-request-id": key,
			}
		}
	}

	return payload, nil
}

func marshalResponsesRequest(payload responsesRequest) ([]byte, error) {
	// Always serialize through the sorted-key map form so the body's key
	// order cannot flip between requests when options appear or disappear —
	// automatic prefix caching matches raw bytes.
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	var object map[string]any
	if err := json.Unmarshal(raw, &object); err != nil {
		return nil, err
	}
	mergeResponsesProviderOptions(object, payload.Options)
	return json.Marshal(object)
}

func mergeResponsesProviderOptions(object map[string]any, options map[string]any) {
	if len(object) == 0 || len(options) == 0 {
		return
	}
	for key, value := range options {
		switch key {
		case "reasoningEffort":
			if effort, ok := value.(string); ok && strings.TrimSpace(effort) != "" {
				reasoning := ensureResponseObject(object, "reasoning")
				reasoning["effort"] = strings.TrimSpace(effort)
			}
		case "reasoningSummary":
			reasoning := ensureResponseObject(object, "reasoning")
			reasoning["summary"] = value
		case "textVerbosity":
			text := ensureResponseObject(object, "text")
			text["verbosity"] = value
		case "serviceTier":
			object["service_tier"] = value
		case "maxOutputTokens":
			object["max_output_tokens"] = value
		case "promptCacheKey":
			object["prompt_cache_key"] = value
		case "include":
			// Accept either []string or []any so callers don't have to
			// round-trip through a concrete slice type.
			object["include"] = value
		case "parallelToolCalls":
			object["parallel_tool_calls"] = value
		case "temperature":
			if _, exists := object["temperature"]; !exists {
				object["temperature"] = value
			}
		case "topP":
			object["top_p"] = value
		case "topK":
			object["top_k"] = value
		default:
			if responsesProviderOptionUnsupported(key) {
				continue
			}
			mergeProviderOptionValue(object, key, value)
		}
	}
}

func responsesProviderOptionUnsupported(key string) bool {
	switch key {
	case "toolStreaming", "toolChoiceAutoOnly", "omitStore", "omitPromptCacheKey", "thinkingConfig", "reasoningConfig", "modelParams", "gateway",
		"usage", "chat_template_args", "enable_thinking", "thinking",
		"temperatureSupported", "temperature_supported", "promptCacheKeySupported":
		return true
	default:
		return false
	}
}

func ensureResponseObject(object map[string]any, key string) map[string]any {
	if existing, ok := object[key].(map[string]any); ok {
		return existing
	}
	next := make(map[string]any)
	object[key] = next
	return next
}

func splitResponsesInstructions(messages []providers.ChatMessage) (string, []providers.ChatMessage) {
	instructions := make([]string, 0, 1)
	input := make([]providers.ChatMessage, 0, len(messages))
	var lastInstructionMessage providers.ChatMessage
	for _, msg := range messages {
		if strings.EqualFold(msg.Role, "system") {
			if text := strings.TrimSpace(msg.Content); text != "" {
				instructions = append(instructions, text)
				lastInstructionMessage = msg
			}
			continue
		}
		input = append(input, msg)
	}
	// The Responses API does not accept instructions as the sole request
	// context. A compaction pass can legitimately replace the entire transcript
	// with one or more system messages, so retain the final one as an input item
	// instead of emitting input=[] without a previous_response_id.
	if len(input) == 0 && len(instructions) > 0 {
		input = append(input, lastInstructionMessage)
		instructions = instructions[:len(instructions)-1]
	}
	return strings.Join(instructions, "\n\n"), input
}

func appendResponsesInputItem(input []responsesInputItem, msg providers.ChatMessage, provider, providerStateScope, model string, nativeDeferred bool) []responsesInputItem {
	if msg.Role == "tool" {
		if nativeDeferred && isResponsesToolSearchResult(msg) {
			return append(input, responsesInputItem{
				Type:      "tool_search_output",
				CallID:    msg.ToolCallID,
				Status:    "completed",
				Execution: "client",
				Tools:     responsesToolSearchOutputTools(model, msg.Content),
			})
		}
		input = append(input, responsesInputItem{
			Type:   "function_call_output",
			CallID: msg.ToolCallID,
			Output: msg.Content,
		})
		if nativeDeferred {
			if tools := responsesToolDefinitionsFromLoadable(model, msg.DiscoveredTools); len(tools) > 0 {
				input = append(input, responsesAdditionalToolsInputItem(tools))
			}
		}
		return input
	}

	if msg.Role == "assistant" {
		for _, providerItem := range msg.ProviderItems {
			if item, ok := responsesProviderInputItem(providerItem, provider, providerStateScope); ok {
				input = append(input, item)
			}
		}
		for _, block := range msg.ReasoningBlocks {
			if item, ok := responsesReasoningInputItem(block); ok {
				input = append(input, item)
			}
		}
		if strings.TrimSpace(msg.Content) != "" {
			input = append(input, responsesInputItem{
				Type:    "message",
				ID:      responsesProviderItemIDForModel(msg.ProviderItemID, msg.ProviderItemModel, model),
				Role:    "assistant",
				Phase:   string(msg.Phase),
				Content: []responsesInputContentPart{{Type: "output_text", Text: msg.Content}},
			})
		}
		for _, call := range msg.ToolCalls {
			if nativeDeferred && isResponsesToolSearchCall(call) {
				input = append(input, responsesInputItem{
					Type:      "tool_search_call",
					ID:        responsesProviderItemIDForModel(call.ProviderItemID, call.ProviderItemModel, model),
					CallID:    call.ID,
					Status:    "completed",
					Execution: "client",
					Arguments: responsesToolSearchArguments(call.Arguments),
				})
				continue
			}
			input = append(input, responsesInputItem{
				Type:      "function_call",
				ID:        responsesFunctionCallItemID(responsesProviderItemIDForModel(call.ProviderItemID, call.ProviderItemModel, model)),
				CallID:    call.ID,
				Name:      call.Name,
				Arguments: call.Arguments,
			})
		}
		if strings.TrimSpace(msg.Content) == "" && len(msg.ToolCalls) == 0 && len(msg.ProviderItems) == 0 {
			input = append(input, responsesInputItem{Role: "assistant", Content: ""})
		}
		return input
	}

	return append(input, responsesInputItem{
		Role:    msg.Role,
		Content: responsesMessageContent(msg),
	})
}

func responsesProviderInputItem(item providers.ProviderItem, provider, providerStateScope string) (responsesInputItem, bool) {
	if item.Provider != "" && strings.TrimSpace(item.Provider) != strings.TrimSpace(provider) {
		return responsesInputItem{}, false
	}
	if item.Scope != "" && strings.TrimSpace(item.Scope) != strings.TrimSpace(providerStateScope) {
		return responsesInputItem{}, false
	}
	raw := json.RawMessage(strings.TrimSpace(item.Data))
	if len(raw) == 0 || !json.Valid(raw) {
		return responsesInputItem{}, false
	}
	var probe struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil || probe.Type != item.Type || probe.Type != "compaction" {
		return responsesInputItem{}, false
	}
	return responsesInputItem{Raw: append(json.RawMessage(nil), raw...)}, true
}

func responsesReasoningInputItem(block providers.ReasoningBlock) (responsesInputItem, bool) {
	raw := json.RawMessage(strings.TrimSpace(block.Data))
	if len(raw) == 0 || !json.Valid(raw) {
		return responsesInputItem{}, false
	}
	var probe struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil || probe.Type != "reasoning" {
		return responsesInputItem{}, false
	}
	replay := stripResponsesReasoningStatus(raw)
	return responsesInputItem{Raw: append(json.RawMessage(nil), replay...)}, true
}

// stripResponsesReasoningStatus removes the output-only `status` field from a
// reasoning item before replaying it as input. The Responses wire accepts
// `status` on output items but some model endpoints reject it on input items,
// and the upstream codex-rs Reasoning shape has no such field.
func stripResponsesReasoningStatus(raw json.RawMessage) json.RawMessage {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return raw
	}
	if _, ok := obj["status"]; !ok {
		return raw
	}
	delete(obj, "status")
	stripped, err := json.Marshal(obj)
	if err != nil {
		return raw
	}
	return stripped
}

func responsesProviderItemIDForModel(itemID, itemModel, targetModel string) string {
	itemID = strings.TrimSpace(itemID)
	if itemID == "" {
		return ""
	}
	itemModel = strings.TrimSpace(itemModel)
	if itemModel == "" || itemModel == strings.TrimSpace(targetModel) {
		return clampResponsesItemID(itemID)
	}
	return ""
}

// clampResponsesItemID enforces the Responses API 64-character cap on item
// ids. Truncation is done on Unicode code points so multi-byte characters are
// never split mid-sequence.
func clampResponsesItemID(id string) string {
	runes := []rune(id)
	if len(runes) <= 64 {
		return id
	}
	return string(runes[:64])
}

// responsesFunctionCallItemID sanitizes a function_call item id for input
// replay. The Responses wire requires function_call item ids to use the fc_
// prefix; ids that do not (for example custom-tool ids replayed from another
// provider) are dropped instead of being sent through and rejected upstream.
func responsesFunctionCallItemID(itemID string) string {
	itemID = clampResponsesItemID(itemID)
	if itemID == "" || strings.HasPrefix(itemID, "fc_") {
		return itemID
	}
	return ""
}

func responsesMessageContent(msg providers.ChatMessage) any {
	if (len(msg.Images) == 0 && len(msg.Files) == 0) || !strings.EqualFold(msg.Role, "user") {
		return msg.Content
	}

	parts := make([]responsesInputContentPart, 0, len(msg.Images)+len(msg.Files)+1)
	if strings.TrimSpace(msg.Content) != "" {
		parts = append(parts, responsesInputContentPart{
			Type: "input_text",
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
		parts = append(parts, responsesInputContentPart{
			Type:     "input_image",
			ImageURL: "data:" + mediaType + ";base64," + data,
		})
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
		filename := strings.TrimSpace(file.Filename)
		if filename == "" {
			filename = "attachment"
		}
		parts = append(parts, responsesInputContentPart{
			Type:     "input_file",
			Filename: filename,
			FileData: "data:" + mediaType + ";base64," + data,
		})
	}
	return parts
}

// openAIPromptCacheKeyMaxLength matches the OpenAI Responses / Chat
// Completions spec cap of 64 characters on the prompt_cache_key field.
// Truncating client-side avoids the backend's 400 for over-long keys.
const openAIPromptCacheKeyMaxLength = 64

// clampPromptCacheKey enforces the 64-char Responses spec limit. Returns "" for empty or
// whitespace-only input so callers can treat the result as a presence check.
// Truncation is done on Unicode code points so we never split a multi-byte
// character mid-sequence.
func clampPromptCacheKey(key string) string {
	key = strings.TrimSpace(key)
	if key == "" {
		return ""
	}
	runes := []rune(key)
	if len(runes) <= openAIPromptCacheKeyMaxLength {
		return key
	}
	return string(runes[:openAIPromptCacheKeyMaxLength])
}

func responsePromptCacheKey(hint *providers.CacheHint) string {
	if hint == nil {
		return ""
	}
	return clampPromptCacheKey(hint.PromptCacheKey)
}

func responsesToolParameters(schema map[string]any) map[string]any {
	if schema == nil {
		return nil
	}
	out := make(map[string]any, len(schema)+1)
	for k, v := range schema {
		out[k] = v
	}
	if typ, ok := out["type"].(string); ok && typ == "object" {
		if _, exists := out["properties"]; !exists {
			out["properties"] = map[string]any{}
		}
	}
	return out
}

func responsesToolDefinitionFromProvider(model string, tool providers.ToolDefinition, nativeDeferred bool) responsesToolDefinition {
	if nativeDeferred && strings.EqualFold(tool.Name, "tool_search") {
		return responsesToolDefinition{
			Type:        "tool_search",
			Description: tool.Description,
			Execution:   "client",
			Parameters:  responsesToolParameters(providers.ToolInputSchemaForModel(model, tool.InputSchema)),
		}
	}
	strict := false
	return responsesToolDefinition{
		Type:         "function",
		Name:         tool.Name,
		Description:  tool.Description,
		Strict:       &strict,
		DeferLoading: nativeDeferred && tool.DeferLoading,
		Parameters:   responsesToolParameters(providers.ToolInputSchemaForModel(model, tool.InputSchema)),
	}
}

func responsesToolDefinitionFromLoadable(model string, tool providers.LoadableToolDefinition, deferLoading bool) responsesToolDefinition {
	strict := false
	typ := strings.TrimSpace(tool.Type)
	if typ == "" {
		typ = "function"
	}
	return responsesToolDefinition{
		Type:         typ,
		Name:         tool.Name,
		Description:  tool.Description,
		Strict:       &strict,
		DeferLoading: deferLoading,
		Parameters:   responsesToolParameters(providers.ToolInputSchemaForModel(model, tool.InputSchema)),
	}
}

func responsesToolSearchOutputTools(model string, content string) []responsesToolDefinition {
	return responsesToolDefinitionsFromLoadableForToolSearch(model, providers.LoadableToolsFromToolSearchResult(content))
}

func responsesCompactedDiscoveredTools(model string, messages []providers.ChatMessage) []responsesToolDefinition {
	attached := providers.CompactedDiscoveredToolsFromMessages(messages)
	if len(attached) == 0 {
		return nil
	}
	rawNames := providers.ToolSearchResultToolNamesFromMessages(messages)
	filtered := make([]providers.LoadableToolDefinition, 0, len(attached))
	for _, tool := range attached {
		name := strings.TrimSpace(tool.Name)
		if name == "" {
			continue
		}
		if _, exists := rawNames[name]; exists {
			continue
		}
		filtered = append(filtered, tool)
	}
	return responsesToolDefinitionsFromLoadable(model, filtered)
}

func responsesToolDefinitionsFromLoadable(model string, loadable []providers.LoadableToolDefinition) []responsesToolDefinition {
	return responsesToolDefinitionsFromLoadableWithDefer(model, loadable, false)
}

func responsesToolDefinitionsFromLoadableForToolSearch(model string, loadable []providers.LoadableToolDefinition) []responsesToolDefinition {
	return responsesToolDefinitionsFromLoadableWithDefer(model, loadable, true)
}

func responsesToolDefinitionsFromLoadableWithDefer(model string, loadable []providers.LoadableToolDefinition, deferLoading bool) []responsesToolDefinition {
	tools := make([]responsesToolDefinition, 0, len(loadable))
	for _, tool := range loadable {
		if strings.TrimSpace(tool.Name) == "" {
			continue
		}
		tools = append(tools, responsesToolDefinitionFromLoadable(model, tool, deferLoading))
	}
	return tools
}

func responsesAdditionalToolsInputItem(tools []responsesToolDefinition) responsesInputItem {
	return responsesInputItem{
		Type:  "additional_tools",
		Role:  "developer",
		Tools: tools,
	}
}

func shouldOmitResponsesTopLevelTool(tool providers.ToolDefinition, discovered map[string]struct{}) bool {
	if len(discovered) == 0 {
		return false
	}
	if strings.EqualFold(tool.Name, "tool_search") {
		return false
	}
	_, ok := discovered[tool.Name]
	return ok
}

func isResponsesToolSearchCall(call providers.ToolCall) bool {
	return call.Kind == providers.ToolCallKindToolSearch || strings.EqualFold(call.Name, "tool_search")
}

func isResponsesToolSearchResult(msg providers.ChatMessage) bool {
	return msg.ToolResultKind == providers.ToolCallKindToolSearch || strings.EqualFold(msg.Name, "tool_search")
}

func responsesToolSearchArguments(arguments string) any {
	trimmed := strings.TrimSpace(arguments)
	if trimmed == "" {
		return map[string]any{}
	}
	var decoded any
	if err := json.Unmarshal([]byte(trimmed), &decoded); err != nil {
		return trimmed
	}
	return decoded
}

func (c *Client) doSingleResponsesRequest(
	ctx context.Context,
	httpClient *http.Client,
	body []byte,
	acceptStream bool,
	extraHeaders map[string]string,
	attempt providers.InferenceAttempt,
	submissionReason string,
) (*http.Response, *providers.ProviderLease, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/responses", bytes.NewReader(body))
	if err != nil {
		return nil, nil, fmt.Errorf("build request: %w", err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
	httpReq.Header.Set("Content-Type", "application/json")
	if acceptStream {
		httpReq.Header.Set("Accept", "text/event-stream")
	}
	for k, v := range c.headers {
		httpReq.Header.Set(k, v)
	}
	for k, v := range extraHeaders {
		httpReq.Header.Set(k, v)
	}

	mode := "unary"
	if acceptStream {
		mode = "stream"
	}
	if strings.TrimSpace(submissionReason) != "" {
		mode = "fallback"
	}
	lease, err := c.coordinator.AcquireForAttempt(ctx, c.providerScope, attempt)
	if err != nil {
		return nil, nil, err
	}
	if _, err := lease.RecordSubmission(attempt, providers.InferenceSubmissionMeta{
		Provider:     "openai",
		Protocol:     "responses",
		Transport:    "http",
		Mode:         mode,
		Reason:       submissionReason,
		RequestBytes: len(body),
	}); err != nil {
		lease.Release()
		return nil, nil, fmt.Errorf("journal responses submission: %w", err)
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
			ProviderFamily:  "openai",
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

func (c *Client) readResponsesSSE(ctx context.Context, resp *http.Response, lease *providers.ProviderLease, ch chan<- providers.StreamEvent, state *providers.ProviderStateSummary) {
	defer close(ch)
	defer resp.Body.Close()
	defer lease.Release()

	emit := providers.NewStreamEmitter(ctx, ch)

	if state != nil {
		if !emit.Send(providers.StreamEvent{
			Type:          providers.EventProviderState,
			ProviderState: state,
		}) {
			return
		}
	}

	idleTimeout := c.streamConfig.IdleTimeout
	var idleFired atomic.Bool
	idleTimer := time.AfterFunc(idleTimeout, func() {
		idleFired.Store(true)
		_ = resp.Body.Close()
	})
	defer idleTimer.Stop()
	resetIdle := func() { idleTimer.Reset(idleTimeout) }
	var finalAnswerTailFired atomic.Bool
	var finalAnswerTailTimer *time.Timer
	defer func() {
		if finalAnswerTailTimer != nil {
			finalAnswerTailTimer.Stop()
		}
	}()
	armFinalAnswerTail := func() {
		if finalAnswerTailTimer != nil {
			return
		}
		finalAnswerTailTimer = time.AfterFunc(responsesFinalAnswerTailTimeout(idleTimeout), func() {
			finalAnswerTailFired.Store(true)
			_ = resp.Body.Close()
		})
	}
	disarmFinalAnswerTail := func() {
		if finalAnswerTailTimer != nil {
			finalAnswerTailTimer.Stop()
		}
	}

	pending := newResponsesPendingTools()
	pendingReasoning := newResponsesPendingReasoning()
	var sawToolCall bool
	var currentTextPhase providers.MessagePhase
	var currentTextItemID string

	scanner := providers.NewSSEReader(resp.Body, resetIdle)
	for scanner.Scan() {
		data := scanner.Event().Data
		if data == "[DONE]" {
			pending.emitEnds(emit)
			lease.Succeed()
			emit.Send(providers.StreamEvent{Type: providers.EventDone})
			return
		}

		var event responsesStreamEvent
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			providers.DebugLogfWire("Responses SSE parse error: %v, data: %s", err, data)
			err = fmt.Errorf("parse chunk: %w", err)
			failOpenAIResponseLease(lease, resp, err)
			emit.Send(providers.StreamEvent{Type: providers.EventError, Error: err})
			return
		}

		switch event.Type {
		case "response.reasoning_summary_text.delta", "response.reasoning_text.delta":
			pendingReasoning.appendDelta(event, event.Delta, emit)

		case "response.reasoning_summary_part.done":
			pendingReasoning.appendDelta(event, "\n\n", emit)

		case "response.output_text.delta":
			if event.Delta != "" {
				if event.ItemID != "" {
					currentTextItemID = event.ItemID
				}
				emit.Send(providers.StreamEvent{Type: providers.EventContentDelta, Content: event.Delta, Phase: currentTextPhase, ProviderItemID: currentTextItemID})
			}

		case "response.output_item.added":
			switch event.Item.Type {
			case "reasoning":
				pendingReasoning.start(event.Item, event.outputIndex())
			case "message":
				if event.Item.ID != "" {
					currentTextItemID = event.Item.ID
				}
				if phase := providers.NormalizeMessagePhase(event.Item.Phase); phase != "" {
					currentTextPhase = phase
					emit.Send(providers.StreamEvent{Type: providers.EventContentDelta, Phase: currentTextPhase, ProviderItemID: currentTextItemID})
				}
			case "function_call", "tool_search_call":
				sawToolCall = true
				disarmFinalAnswerTail()
				pending.start(event.Item, event.outputIndex(), emit)
			}

		case "response.function_call_arguments.delta", "response.tool_search_call.arguments.delta":
			if event.Delta != "" {
				pending.appendDelta(event, emit)
			}

		case "response.function_call_arguments.done", "response.tool_search_call.arguments.done":
			pending.setArguments(event)

		case "response.output_item.done":
			switch event.Item.Type {
			case "reasoning":
				pendingReasoning.emitDone(event, emit)
			case "message":
				if event.Item.ID != "" {
					currentTextItemID = event.Item.ID
				}
				if phase := providers.NormalizeMessagePhase(event.Item.Phase); phase != "" {
					currentTextPhase = phase
					emit.Send(providers.StreamEvent{Type: providers.EventContentDelta, Phase: currentTextPhase, ProviderItemID: currentTextItemID})
				}
				if responsesFinalAnswerItemDone(event, sawToolCall) {
					armFinalAnswerTail()
				}
			case "function_call", "tool_search_call":
				sawToolCall = true
				disarmFinalAnswerTail()
				pt := pending.start(event.Item, event.outputIndex(), emit)
				pending.emitEnd(pt, event.Item.argumentsString(), emit)
			case "compaction":
				item := providers.ProviderItem{Type: "compaction", Data: string(event.Item.Raw)}
				emit.Send(providers.StreamEvent{Type: providers.EventProviderItem, ProviderItem: &item})
			}

		case "response.completed", "response.done", "response.incomplete":
			pending.emitEnds(emit)
			usage, stopReason, finishReason, truncated := responsesDoneMetadata(event.Response, sawToolCall, event.Type)
			lease.SucceedWithUsage(usage)
			emit.Send(providers.StreamEvent{
				Type:              providers.EventDone,
				Usage:             usage,
				StopReason:        stopReason,
				FinishReason:      finishReason,
				Truncated:         truncated,
				ProviderEventType: event.Type,
			})
			return

		case "response.failed", "error":
			err := event.asError()
			lease.FailError(err)
			emit.Send(providers.StreamEvent{Type: providers.EventError, Error: err})
			return
		}
		if emit.Aborted() {
			return
		}
	}
	if finalAnswerTailFired.Load() && !sawToolCall && ctx.Err() == nil {
		providers.DebugLogf("Responses SSE inferred completion after final-answer tail grace")
		lease.Succeed()
		emit.Send(responsesInferredFinalAnswerDoneEvent())
		return
	}

	if err := scanner.Err(); err != nil {
		if idleFired.Load() {
			err := fmt.Errorf("stream idle timeout after %s: %w", idleTimeout, context.DeadlineExceeded)
			failOpenAIResponseLease(lease, resp, err)
			emit.Send(providers.StreamEvent{
				Type:  providers.EventError,
				Error: err,
			})
			return
		}
		err = fmt.Errorf("read stream: %w", err)
		failOpenAIResponseLease(lease, resp, err)
		emit.Send(providers.StreamEvent{Type: providers.EventError, Error: err})
		return
	}
	if idleFired.Load() {
		err := fmt.Errorf("stream idle timeout after %s: %w", idleTimeout, context.DeadlineExceeded)
		failOpenAIResponseLease(lease, resp, err)
		emit.Send(providers.StreamEvent{
			Type:  providers.EventError,
			Error: err,
		})
		return
	}
	err := providers.NewIncompleteStreamError("stream closed before response.completed")
	failOpenAIResponseLease(lease, resp, err)
	emit.Send(providers.StreamEvent{
		Type:  providers.EventError,
		Error: err,
	})
}

func responsesFinalAnswerTailTimeout(idleTimeout time.Duration) time.Duration {
	if idleTimeout > 0 && idleTimeout <= responsesFinalAnswerTailGrace {
		if shortened := idleTimeout / 2; shortened > 0 {
			return shortened
		}
		return idleTimeout
	}
	return responsesFinalAnswerTailGrace
}

func responsesFinalAnswerItemDone(event responsesStreamEvent, sawToolCall bool) bool {
	return !sawToolCall &&
		event.Type == "response.output_item.done" &&
		event.Item.Type == "message" &&
		providers.NormalizeMessagePhase(event.Item.Phase) == providers.MessagePhaseFinalAnswer &&
		strings.EqualFold(strings.TrimSpace(event.Item.Status), "completed")
}

func responsesInferredFinalAnswerDoneEvent() providers.StreamEvent {
	return providers.StreamEvent{
		Type:              providers.EventDone,
		StopReason:        "completed",
		FinishReason:      providers.FinishReasonStop,
		ProviderEventType: "response.output_item.done",
	}
}

func responsesDoneMetadata(resp *responsesResponse, sawToolCall bool, eventType string) (*providers.TokenUsage, string, providers.FinishReason, bool) {
	if resp == nil {
		if sawToolCall {
			return nil, "tool_calls", providers.FinishReasonToolCalls, false
		}
		return nil, "", "", false
	}

	usage := resp.Usage.asTokenUsage()
	stopReason := strings.ToLower(strings.TrimSpace(resp.Status))
	if stopReason == "" && eventType == "response.completed" {
		stopReason = "completed"
	}
	truncated := false
	if resp.IncompleteDetails != nil && strings.TrimSpace(resp.IncompleteDetails.Reason) != "" {
		stopReason = strings.ToLower(strings.TrimSpace(resp.IncompleteDetails.Reason))
		truncated = stopReason == "max_output_tokens"
	}
	if sawToolCall && !truncated {
		stopReason = "tool_calls"
	}
	finishReason := providers.NormalizeFinishReason(stopReason, truncated, sawToolCall)
	// end_turn is an optional Responses extension, not a Chat Completions
	// finish_reason. Missing/null must not turn ordinary BYOK completions into
	// extra billable requests. Incomplete/error evidence always wins over it.
	if stopReason == "completed" && eventType != "response.incomplete" &&
		resp.Error == nil && resp.IncompleteDetails == nil && resp.EndTurn != nil && !*resp.EndTurn {
		finishReason = providers.FinishReasonContinue
	}
	return usage, stopReason, finishReason, truncated
}

type responsesPendingTool struct {
	itemID      string
	outputIndex int
	id          string
	name        string
	kind        providers.ToolCallKind
	args        strings.Builder
	ended       bool
}

type responsesPendingReasoning struct {
	items    []*strings.Builder
	byItemID map[string]*strings.Builder
	byIndex  map[int]*strings.Builder
}

func newResponsesPendingReasoning() *responsesPendingReasoning {
	return &responsesPendingReasoning{
		byItemID: make(map[string]*strings.Builder),
		byIndex:  make(map[int]*strings.Builder),
	}
}

func (p *responsesPendingReasoning) start(item responsesOutputItem, outputIndex int) *strings.Builder {
	if item.Type != "reasoning" {
		return nil
	}
	builder := &strings.Builder{}
	p.items = append(p.items, builder)
	if item.ID != "" {
		p.byItemID[item.ID] = builder
	}
	if outputIndex >= 0 {
		p.byIndex[outputIndex] = builder
	}
	return builder
}

func (p *responsesPendingReasoning) appendDelta(event responsesStreamEvent, delta string, emit *providers.StreamEmitter) {
	if delta == "" {
		return
	}
	builder := p.find(event)
	if builder == nil {
		builder = p.start(responsesOutputItem{Type: "reasoning", ID: event.ItemID}, event.outputIndex())
	}
	if builder != nil {
		builder.WriteString(delta)
	}
	emit.Send(providers.StreamEvent{Type: providers.EventThinkingDelta, Content: delta})
}

func (p *responsesPendingReasoning) emitDone(event responsesStreamEvent, emit *providers.StreamEmitter) {
	if event.Item.Type != "reasoning" {
		return
	}
	block := responsesReasoningBlock(event.Item)
	if strings.TrimSpace(block.Thinking) == "" {
		if builder := p.find(event); builder != nil {
			block.Thinking = builder.String()
		}
	}
	if strings.TrimSpace(block.Thinking) != "" {
		if builder := p.find(event); builder == nil || builder.String() == "" {
			if !emit.Send(providers.StreamEvent{Type: providers.EventThinkingDelta, Content: block.Thinking}) {
				return
			}
		}
	}
	emit.Send(providers.StreamEvent{Type: providers.EventThinkingDone, ReasoningBlock: &block})
}

func (p *responsesPendingReasoning) find(event responsesStreamEvent) *strings.Builder {
	if event.ItemID != "" {
		if builder := p.byItemID[event.ItemID]; builder != nil {
			return builder
		}
	}
	if event.Item.ID != "" {
		if builder := p.byItemID[event.Item.ID]; builder != nil {
			return builder
		}
	}
	if idx := event.outputIndex(); idx >= 0 {
		if builder := p.byIndex[idx]; builder != nil {
			return builder
		}
	}
	if len(p.items) == 0 {
		return nil
	}
	return p.items[len(p.items)-1]
}

type responsesPendingTools struct {
	items    []*responsesPendingTool
	byItemID map[string]*responsesPendingTool
	byIndex  map[int]*responsesPendingTool
}

func newResponsesPendingTools() *responsesPendingTools {
	return &responsesPendingTools{
		byItemID: make(map[string]*responsesPendingTool),
		byIndex:  make(map[int]*responsesPendingTool),
	}
}

func (p *responsesPendingTools) start(item responsesOutputItem, outputIndex int, emit *providers.StreamEmitter) *responsesPendingTool {
	if item.ID != "" {
		if existing := p.byItemID[item.ID]; existing != nil {
			existing.update(item, outputIndex)
			return existing
		}
	}
	if outputIndex >= 0 {
		if existing := p.byIndex[outputIndex]; existing != nil {
			existing.update(item, outputIndex)
			return existing
		}
	}

	pt := &responsesPendingTool{
		itemID:      item.ID,
		outputIndex: outputIndex,
		id:          item.CallID,
		name:        item.toolCallName(),
		kind:        item.toolCallKind(),
	}
	if args := item.argumentsString(); args != "" {
		pt.args.WriteString(args)
	}
	p.items = append(p.items, pt)
	if item.ID != "" {
		p.byItemID[item.ID] = pt
	}
	if outputIndex >= 0 {
		p.byIndex[outputIndex] = pt
	}
	emit.Send(providers.StreamEvent{
		Type: providers.EventToolUseStart,
		ToolCall: &providers.ToolCall{
			ID:             pt.id,
			ProviderItemID: pt.itemID,
			Name:           pt.name,
			Kind:           pt.kind,
		},
	})
	return pt
}

func (p *responsesPendingTools) appendDelta(event responsesStreamEvent, emit *providers.StreamEmitter) {
	pt := p.find(event)
	if pt != nil {
		pt.args.WriteString(event.Delta)
	}
	emit.Send(providers.StreamEvent{Type: providers.EventToolUseDelta, Content: event.Delta})
}

func (p *responsesPendingTools) setArguments(event responsesStreamEvent) {
	pt := p.find(event)
	args := event.argumentsString()
	if pt == nil || args == "" {
		return
	}
	pt.args.Reset()
	pt.args.WriteString(args)
}

func (p *responsesPendingTools) emitEnd(pt *responsesPendingTool, arguments string, emit *providers.StreamEmitter) {
	if pt == nil || pt.ended {
		return
	}
	if arguments != "" {
		pt.args.Reset()
		pt.args.WriteString(arguments)
	}
	pt.ended = true
	emit.Send(providers.StreamEvent{
		Type: providers.EventToolUseEnd,
		ToolCall: &providers.ToolCall{
			ID:             pt.id,
			ProviderItemID: pt.itemID,
			Name:           pt.name,
			Kind:           pt.kind,
			Arguments:      pt.args.String(),
		},
	})
}

func (p *responsesPendingTools) emitEnds(emit *providers.StreamEmitter) {
	for _, pt := range p.items {
		p.emitEnd(pt, "", emit)
	}
}

func (p *responsesPendingTools) find(event responsesStreamEvent) *responsesPendingTool {
	if event.ItemID != "" {
		if pt := p.byItemID[event.ItemID]; pt != nil {
			return pt
		}
	}
	if idx := event.outputIndex(); idx >= 0 {
		if pt := p.byIndex[idx]; pt != nil {
			return pt
		}
	}
	if len(p.items) == 0 {
		return nil
	}
	return p.items[len(p.items)-1]
}

func (p *responsesPendingTool) update(item responsesOutputItem, outputIndex int) {
	if p.itemID == "" {
		p.itemID = item.ID
	}
	if p.outputIndex < 0 {
		p.outputIndex = outputIndex
	}
	if p.id == "" {
		p.id = item.CallID
	}
	if p.name == "" {
		p.name = item.toolCallName()
	}
	if p.kind == "" {
		p.kind = item.toolCallKind()
	}
}

type responsesRequest struct {
	Model              string                    `json:"model"`
	Instructions       string                    `json:"instructions,omitempty"`
	Input              []responsesInputItem      `json:"input"`
	Tools              []responsesToolDefinition `json:"tools,omitempty"`
	ToolChoice         any                       `json:"tool_choice,omitempty"`
	Temperature        float64                   `json:"temperature,omitempty"`
	MaxOutputTokens    int                       `json:"max_output_tokens,omitempty"`
	Stream             bool                      `json:"stream,omitempty"`
	Store              *bool                     `json:"store,omitempty"`
	Reasoning          *responsesReasoning       `json:"reasoning,omitempty"`
	PromptCacheKey     string                    `json:"prompt_cache_key,omitempty"`
	PreviousResponseID string                    `json:"previous_response_id,omitempty"`
	// Include asks the backend to surface specific output items (e.g.
	// "reasoning.encrypted_content") alongside the assistant message.
	// Some Responses backends require this for reasoning replay.
	Include []string `json:"include,omitempty"`
	// ParallelToolCalls lets the backend issue concurrent tool calls when
	// the model emits multiple in one turn.
	ParallelToolCalls *bool                      `json:"parallel_tool_calls,omitempty"`
	Options           map[string]any             `json:"-"`
	Attempt           providers.InferenceAttempt `json:"-"`
	SubmissionReason  string                     `json:"-"`
	// ExtraHeaders carries per-request HTTP headers derived from runtime state
	// (e.g. session-id / x-client-request-id sourced from the prompt cache
	// key). They are not serialized into the request body; the caller is
	// expected to merge them onto the outgoing http.Request right before
	// dispatch. OpenAI ignores them on the public API path; the ChatGPT
	// Codex backend uses them for sticky routing within a turn.
	ExtraHeaders map[string]string `json:"-"`
}

type responsesReasoning struct {
	Effort string `json:"effort,omitempty"`
}

type responsesInputItem struct {
	Raw       json.RawMessage `json:"-"`
	Type      string          `json:"type,omitempty"`
	ID        string          `json:"id,omitempty"`
	Role      string          `json:"role,omitempty"`
	Phase     string          `json:"phase,omitempty"`
	Content   any             `json:"content,omitempty"`
	CallID    string          `json:"call_id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Status    string          `json:"status,omitempty"`
	Execution string          `json:"execution,omitempty"`
	Arguments any             `json:"arguments,omitempty"`
	Output    string          `json:"output,omitempty"`
	Tools     any             `json:"tools,omitempty"`
}

func (i responsesInputItem) MarshalJSON() ([]byte, error) {
	if len(i.Raw) > 0 {
		return i.Raw, nil
	}
	type alias responsesInputItem
	return json.Marshal(alias(i))
}

func (i *responsesInputItem) UnmarshalJSON(data []byte) error {
	type alias responsesInputItem
	var decoded alias
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*i = responsesInputItem(decoded)
	if json.Valid(data) {
		i.Raw = append(i.Raw[:0], data...)
	}
	return nil
}

type responsesInputContentPart struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	ImageURL string `json:"image_url,omitempty"`
	Filename string `json:"filename,omitempty"`
	FileData string `json:"file_data,omitempty"`
}

type responsesToolDefinition struct {
	Type         string         `json:"type"`
	Name         string         `json:"name,omitempty"`
	Description  string         `json:"description,omitempty"`
	Strict       *bool          `json:"strict,omitempty"`
	Execution    string         `json:"execution,omitempty"`
	DeferLoading bool           `json:"defer_loading,omitempty"`
	Parameters   map[string]any `json:"parameters"`
}

type responsesResponse struct {
	ID                string                      `json:"id,omitempty"`
	Status            string                      `json:"status"`
	EndTurn           *bool                       `json:"end_turn,omitempty"`
	Output            []responsesOutputItem       `json:"output"`
	Usage             *responsesUsage             `json:"usage,omitempty"`
	Error             *responsesError             `json:"error,omitempty"`
	IncompleteDetails *responsesIncompleteDetails `json:"incomplete_details,omitempty"`
}

func (r responsesResponse) asChatResponse(model string) (providers.ChatResponse, error) {
	if r.Error != nil {
		return providers.ChatResponse{}, r.Error.asError()
	}

	var contentParts []string
	calls := make([]providers.ToolCall, 0)
	reasoningBlocks := make([]providers.ReasoningBlock, 0)
	var phase providers.MessagePhase
	var providerItemID string
	for _, item := range r.Output {
		switch item.Type {
		case "reasoning":
			reasoningBlocks = append(reasoningBlocks, responsesReasoningBlock(item))
		case "message":
			if providerItemID == "" {
				providerItemID = item.ID
			}
			if itemPhase := providers.NormalizeMessagePhase(item.Phase); itemPhase != "" {
				phase = itemPhase
			}
			content, err := parseResponsesContent(item.Content)
			if err != nil {
				return providers.ChatResponse{}, err
			}
			if content != "" {
				contentParts = append(contentParts, content)
			}
		case "function_call":
			calls = append(calls, providers.ToolCall{
				ID:                item.CallID,
				ProviderItemID:    item.ID,
				ProviderItemModel: model,
				Name:              item.Name,
				Arguments:         item.argumentsString(),
			})
		case "tool_search_call":
			calls = append(calls, providers.ToolCall{
				ID:                item.CallID,
				ProviderItemID:    item.ID,
				ProviderItemModel: model,
				Name:              "tool_search",
				Kind:              providers.ToolCallKindToolSearch,
				Arguments:         item.argumentsString(),
			})
		}
	}

	usage, stopReason, finishReason, truncated := responsesDoneMetadata(&r, len(calls) > 0, "")

	return providers.ChatResponse{
		Content:           strings.Join(contentParts, "\n"),
		Phase:             phase,
		ProviderItemID:    providerItemID,
		ProviderItemModel: model,
		ReasoningBlocks:   reasoningBlocks,
		ToolCalls:         calls,
		Usage:             usage,
		StopReason:        stopReason,
		FinishReason:      finishReason,
		Truncated:         truncated,
	}, nil
}

type responsesOutputItem struct {
	Raw       json.RawMessage `json:"-"`
	ID        string          `json:"id"`
	Type      string          `json:"type"`
	Role      string          `json:"role,omitempty"`
	Phase     string          `json:"phase,omitempty"`
	Status    string          `json:"status,omitempty"`
	Content   json.RawMessage `json:"content,omitempty"`
	Summary   json.RawMessage `json:"summary,omitempty"`
	CallID    string          `json:"call_id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Execution string          `json:"execution,omitempty"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
}

func (i *responsesOutputItem) UnmarshalJSON(data []byte) error {
	type alias responsesOutputItem
	var decoded alias
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*i = responsesOutputItem(decoded)
	if json.Valid(data) {
		i.Raw = append(i.Raw[:0], data...)
	}
	return nil
}

func responsesReasoningBlock(item responsesOutputItem) providers.ReasoningBlock {
	return providers.ReasoningBlock{
		Type:     "reasoning",
		Thinking: firstNonEmptyString(responsesTextFromParts(item.Summary), responsesTextFromParts(item.Content)),
		Data:     string(item.Raw),
	}
}

func responsesTextFromParts(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var parts []struct {
		Text string `json:"text,omitempty"`
	}
	if err := json.Unmarshal(raw, &parts); err == nil {
		out := make([]string, 0, len(parts))
		for _, part := range parts {
			if part.Text != "" {
				out = append(out, part.Text)
			}
		}
		return strings.Join(out, "\n\n")
	}
	var part struct {
		Text string `json:"text,omitempty"`
	}
	if err := json.Unmarshal(raw, &part); err == nil {
		return part.Text
	}
	return ""
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func (i responsesOutputItem) toolCallName() string {
	if i.Type == "tool_search_call" {
		return "tool_search"
	}
	return i.Name
}

func (i responsesOutputItem) toolCallKind() providers.ToolCallKind {
	if i.Type == "tool_search_call" {
		return providers.ToolCallKindToolSearch
	}
	return ""
}

func (i responsesOutputItem) argumentsString() string {
	return rawResponseArgumentsString(i.Arguments)
}

func (e responsesStreamEvent) argumentsString() string {
	return rawResponseArgumentsString(e.Arguments)
}

func rawResponseArgumentsString(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var asString string
	if err := json.Unmarshal(raw, &asString); err == nil {
		return asString
	}
	var compacted bytes.Buffer
	if err := json.Compact(&compacted, raw); err == nil {
		return compacted.String()
	}
	return string(raw)
}

type responsesContentPart struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

func parseResponsesContent(raw json.RawMessage) (string, error) {
	if len(raw) == 0 {
		return "", nil
	}

	var asString string
	if err := json.Unmarshal(raw, &asString); err == nil {
		return asString, nil
	}

	var parts []responsesContentPart
	if err := json.Unmarshal(raw, &parts); err == nil {
		out := make([]string, 0, len(parts))
		for _, part := range parts {
			switch part.Type {
			case "output_text", "text", "input_text":
				if part.Text != "" {
					out = append(out, part.Text)
				}
			}
		}
		return strings.Join(out, "\n"), nil
	}

	return "", fmt.Errorf("unsupported response content: %s", string(raw))
}

type responsesIncompleteDetails struct {
	Reason string `json:"reason,omitempty"`
}

type responsesUsage struct {
	InputTokens        int `json:"input_tokens"`
	OutputTokens       int `json:"output_tokens"`
	InputTokensDetails *struct {
		CachedTokens int `json:"cached_tokens,omitempty"`
	} `json:"input_tokens_details,omitempty"`
}

func (u *responsesUsage) asTokenUsage() *providers.TokenUsage {
	if u == nil {
		return nil
	}
	cached := 0
	if u.InputTokensDetails != nil {
		cached = u.InputTokensDetails.CachedTokens
	}
	freshInput := u.InputTokens - cached
	if freshInput < 0 {
		freshInput = 0
	}
	return &providers.TokenUsage{
		InputTokens:     freshInput,
		OutputTokens:    u.OutputTokens,
		CacheReadTokens: cached,
	}
}

type responsesError struct {
	Type    string `json:"type,omitempty"`
	Code    string `json:"code,omitempty"`
	Message string `json:"message,omitempty"`
}

func (e *responsesError) asError() error {
	if e == nil {
		return errors.New("response error")
	}
	code := strings.TrimSpace(e.Code)
	if code == "" {
		code = strings.TrimSpace(e.Type)
	}
	if code != "" || e.Message != "" {
		err := providers.NewProviderStreamError(code, e.Message)
		err.ProviderFamily = "openai"
		return err
	}
	if e.Type != "" {
		return fmt.Errorf("response error: %s", e.Type)
	}
	return errors.New("response error")
}

type responsesStreamEvent struct {
	Type        string              `json:"type"`
	Code        string              `json:"code,omitempty"`
	Message     string              `json:"message,omitempty"`
	Delta       string              `json:"delta,omitempty"`
	Arguments   json.RawMessage     `json:"arguments,omitempty"`
	ItemID      string              `json:"item_id,omitempty"`
	OutputIndex *int                `json:"output_index,omitempty"`
	Item        responsesOutputItem `json:"item,omitempty"`
	Response    *responsesResponse  `json:"response,omitempty"`
	Error       *responsesError     `json:"error,omitempty"`
}

// Responses uses top-level code/message for error events. Compatible gateways
// also send error objects, and response.failed carries response.error. Prefer
// those more specific objects, filling missing fields from the top level.
func (e responsesStreamEvent) errorDetail() responsesError {
	detail := responsesError{Code: e.Code, Message: e.Message}
	nested := e.Error
	if e.Type == "response.failed" && e.Response != nil && e.Response.Error != nil {
		nested = e.Response.Error
	}
	if nested != nil {
		detail.Type = nested.Type
		if strings.TrimSpace(nested.Code) != "" {
			detail.Code = nested.Code
		} else if strings.TrimSpace(nested.Type) != "" {
			detail.Code = nested.Type
		}
		if strings.TrimSpace(nested.Message) != "" {
			detail.Message = nested.Message
		}
	}
	return detail
}

func (e responsesStreamEvent) asError() error {
	detail := e.errorDetail()
	if strings.TrimSpace(detail.Code) == "" && strings.TrimSpace(detail.Message) == "" {
		// Retain the protocol identity without copying arbitrary event fields
		// (which can include response content) into durable error messages.
		return &providers.StreamError{ProviderFamily: "openai", Message: "Responses " + e.Type + " event without error code or message"}
	}
	return detail.asError()
}

func (e responsesStreamEvent) outputIndex() int {
	if e.OutputIndex == nil {
		return -1
	}
	return *e.OutputIndex
}
