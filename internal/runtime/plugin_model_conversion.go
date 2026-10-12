package runtime

import (
	"encoding/json"
	"errors"
	"strings"

	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/toolresult"
)

func modelCacheHintToLogical(in providers.CacheHint) (out pluginhost.ModelCacheHintV1) {
	out.PromptCacheKey = in.PromptCacheKey
	out.StableSystem = in.StableSystem
	out.StablePrefixMessages = in.StablePrefixMessages
	out.TurnPrefixMessages = in.TurnPrefixMessages
	out.HasCompactSummary = in.HasCompactSummary
	return
}

func modelCacheHintFromLogical(in pluginhost.ModelCacheHintV1) (out providers.CacheHint) {
	out.PromptCacheKey = in.PromptCacheKey
	out.StableSystem = in.StableSystem
	out.StablePrefixMessages = in.StablePrefixMessages
	out.TurnPrefixMessages = in.TurnPrefixMessages
	out.HasCompactSummary = in.HasCompactSummary
	return
}

func modelMediaInputPolicyToLogical(in providers.MediaInputPolicy) (out pluginhost.ModelMediaInputV1) {
	out.Video = in.Video
	out.VideoKnown = in.VideoKnown
	out.Image = in.Image
	out.ImageKnown = in.ImageKnown
	out.File = in.File
	out.FileKnown = in.FileKnown
	return
}

func modelMediaInputPolicyFromLogical(in pluginhost.ModelMediaInputV1) (out providers.MediaInputPolicy) {
	out.Video = in.Video
	out.VideoKnown = in.VideoKnown
	out.Image = in.Image
	out.File = in.File
	out.ImageKnown = in.ImageKnown
	out.FileKnown = in.FileKnown
	return
}

func modelInputImageToLogical(in providers.InputImage) (out pluginhost.ModelImageV1) {
	out.MediaType = in.MediaType
	out.Data = in.Data
	out.Required = in.Required
	out.Width = in.Width
	out.Height = in.Height
	out.ProviderItemID = in.ProviderItemID
	return
}

func modelInputImageFromLogical(in pluginhost.ModelImageV1) (out providers.InputImage) {
	out.ProviderItemID = in.ProviderItemID
	out.Required = in.Required
	out.MediaType = in.MediaType
	out.Data = in.Data
	out.Width = in.Width
	out.Height = in.Height
	return
}

func modelInputFileToLogical(in providers.InputFile) (out pluginhost.ModelFileV1) {
	out.MediaType = in.MediaType
	out.Data = in.Data
	out.Filename = in.Filename
	out.SizeBytes = in.SizeBytes
	out.Required = in.Required
	return
}

func modelInputFileFromLogical(in pluginhost.ModelFileV1) (out providers.InputFile) {
	out.SizeBytes = in.SizeBytes
	out.Required = in.Required
	out.MediaType = in.MediaType
	out.Data = in.Data
	out.Filename = in.Filename
	return
}

func modelReasoningBlockToLogical(in providers.ReasoningBlock) (out pluginhost.ModelReasoningBlockV1) {
	out.Type = in.Type
	out.Thinking = in.Thinking
	out.Signature = in.Signature
	out.Data = in.Data
	return
}

func modelReasoningBlockFromLogical(in pluginhost.ModelReasoningBlockV1) (out providers.ReasoningBlock) {
	out.Type = in.Type
	out.Thinking = in.Thinking
	out.Signature = in.Signature
	out.Data = in.Data
	return
}

func modelProviderItemToLogical(in providers.ProviderItem) (out pluginhost.ModelProviderItemV1) {
	out.Type = in.Type
	out.Data = in.Data
	out.Provider = in.Provider
	out.Scope = in.Scope
	for _, value := range in.Fallback {
		out.Fallback = append(out.Fallback, modelChatMessageToLogical(value))
	}
	return
}

func modelProviderItemFromLogical(in pluginhost.ModelProviderItemV1) (out providers.ProviderItem) {
	out.Type = in.Type
	out.Data = in.Data
	out.Provider = in.Provider
	out.Scope = in.Scope
	for _, value := range in.Fallback {
		out.Fallback = append(out.Fallback, modelChatMessageFromLogical(value))
	}
	return
}

func modelToolCallToLogical(in providers.ToolCall) (out pluginhost.ModelToolCallV1) {
	out.ID = in.ID
	out.Name = in.Name
	out.Arguments = in.Arguments
	out.Kind = string(in.Kind)
	out.ProviderItemID = in.ProviderItemID
	out.ProviderItemProvider = in.ProviderItemProvider
	out.ProviderItemModel = in.ProviderItemModel
	return
}

func modelToolCallFromLogical(in pluginhost.ModelToolCallV1) (out providers.ToolCall) {
	out.ID = in.ID
	out.ProviderItemID = in.ProviderItemID
	out.ProviderItemProvider = in.ProviderItemProvider
	out.ProviderItemModel = in.ProviderItemModel
	out.Name = in.Name
	out.Arguments = in.Arguments
	out.Kind = providers.ToolCallKind(in.Kind)
	return
}

func modelToolDefinitionToLogical(in providers.ToolDefinition) (out pluginhost.ModelToolV1) {
	out.Name = in.Name
	out.Description = in.Description
	out.InputSchema = providers.CloneRequestObject(in.InputSchema)
	out.Freeform = in.Freeform
	out.DirectOnly = in.DirectOnly
	out.DeferLoading = in.DeferLoading
	out.CacheStable = in.CacheStable
	return
}

func modelToolDefinitionFromLogical(in pluginhost.ModelToolV1) (out providers.ToolDefinition) {
	out.Name = in.Name
	out.Description = in.Description
	out.InputSchema = providers.CloneRequestObject(in.InputSchema)
	out.Freeform = in.Freeform
	out.DirectOnly = in.DirectOnly
	out.DeferLoading = in.DeferLoading
	out.CacheStable = in.CacheStable
	return
}

func modelLoadableToolDefinitionToLogical(in providers.LoadableToolDefinition) (out pluginhost.ModelLoadableToolV1) {
	out.Type = in.Type
	out.Name = in.Name
	out.Description = in.Description
	out.InputSchema = providers.CloneRequestObject(in.InputSchema)
	out.DeferLoading = in.DeferLoading
	return
}

func modelLoadableToolDefinitionFromLogical(in pluginhost.ModelLoadableToolV1) (out providers.LoadableToolDefinition) {
	out.Type = in.Type
	out.Name = in.Name
	out.Description = in.Description
	out.InputSchema = providers.CloneRequestObject(in.InputSchema)
	out.DeferLoading = in.DeferLoading
	return
}

func modelChatMessageToLogical(in providers.ChatMessage) (out pluginhost.ModelMessageV1) {
	out.Role = in.Role
	out.Name = in.Name
	out.Content = in.Content
	out.Hidden = in.Hidden
	out.Origin = in.Origin
	out.OriginID = in.OriginID
	out.Cause = in.Cause
	out.ReadOnly = in.ReadOnly
	out.Phase = string(in.Phase)
	out.ReasoningContent = in.ReasoningContent
	for _, value := range in.ReasoningBlocks {
		out.ReasoningBlocks = append(out.ReasoningBlocks, modelReasoningBlockToLogical(value))
	}
	for _, value := range in.ProviderItems {
		out.ProviderItems = append(out.ProviderItems, modelProviderItemToLogical(value))
	}
	out.ProviderItemID = in.ProviderItemID
	out.ProviderItemProvider = in.ProviderItemProvider
	out.ProviderItemModel = in.ProviderItemModel
	for _, value := range in.Images {
		out.Images = append(out.Images, modelInputImageToLogical(value))
	}
	for _, value := range in.Files {
		out.Files = append(out.Files, modelInputFileToLogical(value))
	}
	out.ToolCallID = in.ToolCallID
	out.ToolResultKind = string(in.ToolResultKind)
	if in.ToolResult != nil {
		raw, _ := json.Marshal(in.ToolResult)
		var value pluginhost.ModelToolResultV1
		_ = json.Unmarshal(raw, &value)
		out.ToolResult = &value
	}
	for _, value := range in.ToolCalls {
		out.ToolCalls = append(out.ToolCalls, modelToolCallToLogical(value))
	}
	for _, value := range in.DiscoveredTools {
		out.DiscoveredTools = append(out.DiscoveredTools, modelLoadableToolDefinitionToLogical(value))
	}
	out.FinishReason = string(in.FinishReason)
	out.StopReason = in.StopReason
	out.Truncated = in.Truncated
	return
}

func modelChatMessageFromLogical(in pluginhost.ModelMessageV1) (out providers.ChatMessage) {
	out.Role = in.Role
	out.Name = in.Name
	out.Content = in.Content
	out.Origin = in.Origin
	out.OriginID = in.OriginID
	out.Cause = in.Cause
	out.ReadOnly = in.ReadOnly
	out.Phase = providers.MessagePhase(in.Phase)
	out.Hidden = in.Hidden
	out.ProviderItemID = in.ProviderItemID
	out.ProviderItemProvider = in.ProviderItemProvider
	out.ProviderItemModel = in.ProviderItemModel
	out.ReasoningContent = in.ReasoningContent
	for _, value := range in.ReasoningBlocks {
		out.ReasoningBlocks = append(out.ReasoningBlocks, modelReasoningBlockFromLogical(value))
	}
	for _, value := range in.ProviderItems {
		out.ProviderItems = append(out.ProviderItems, modelProviderItemFromLogical(value))
	}
	for _, value := range in.Images {
		out.Images = append(out.Images, modelInputImageFromLogical(value))
	}
	for _, value := range in.Files {
		out.Files = append(out.Files, modelInputFileFromLogical(value))
	}
	out.ToolCallID = in.ToolCallID
	out.ToolResultKind = providers.ToolCallKind(in.ToolResultKind)
	if in.ToolResult != nil {
		raw, _ := json.Marshal(in.ToolResult)
		var value toolresult.Result
		_ = json.Unmarshal(raw, &value)
		out.ToolResult = &value
	}
	for _, value := range in.ToolCalls {
		out.ToolCalls = append(out.ToolCalls, modelToolCallFromLogical(value))
	}
	out.FinishReason = providers.FinishReason(in.FinishReason)
	out.StopReason = in.StopReason
	out.Truncated = in.Truncated
	for _, value := range in.DiscoveredTools {
		out.DiscoveredTools = append(out.DiscoveredTools, modelLoadableToolDefinitionFromLogical(value))
	}
	return
}

func modelTokenUsageToLogical(in providers.TokenUsage) (out pluginhost.ModelRequestUsage) {
	out.InputTokens = in.InputTokens
	out.OutputTokens = in.OutputTokens
	out.CacheCreationTokens = in.CacheCreationTokens
	out.CacheReadTokens = in.CacheReadTokens
	out.CacheCreationUnknown = in.CacheCreationUnknown
	return
}

func modelTokenUsageFromLogical(in pluginhost.ModelRequestUsage) (out providers.TokenUsage) {
	out.InputTokens = in.InputTokens
	out.OutputTokens = in.OutputTokens
	out.CacheCreationTokens = in.CacheCreationTokens
	out.CacheReadTokens = in.CacheReadTokens
	out.CacheCreationUnknown = in.CacheCreationUnknown
	return
}

func modelChatResponseToLogical(in providers.ChatResponse) (out pluginhost.ModelResponseV1) {
	out.Content = in.Content
	out.Phase = string(in.Phase)
	out.ProviderItemID = in.ProviderItemID
	out.ProviderItemModel = in.ProviderItemModel
	out.ReasoningContent = in.ReasoningContent
	for _, value := range in.ReasoningBlocks {
		out.ReasoningBlocks = append(out.ReasoningBlocks, modelReasoningBlockToLogical(value))
	}
	for _, value := range in.ProviderItems {
		out.ProviderItems = append(out.ProviderItems, modelProviderItemToLogical(value))
	}
	for _, value := range in.Images {
		out.Images = append(out.Images, modelInputImageToLogical(value))
	}
	for _, value := range in.ToolCalls {
		out.ToolCalls = append(out.ToolCalls, modelToolCallToLogical(value))
	}
	if in.Usage != nil {
		value := modelTokenUsageToLogical(*in.Usage)
		out.Usage = &value
	}
	out.StopReason = in.StopReason
	out.FinishReason = string(in.FinishReason)
	out.Truncated = in.Truncated
	return
}

func modelChatResponseFromLogical(in pluginhost.ModelResponseV1) (out providers.ChatResponse) {
	for _, value := range in.ProviderItems {
		out.ProviderItems = append(out.ProviderItems, modelProviderItemFromLogical(value))
	}
	for _, value := range in.Images {
		out.Images = append(out.Images, modelInputImageFromLogical(value))
	}
	out.Content = in.Content
	out.Phase = providers.MessagePhase(in.Phase)
	out.ProviderItemID = in.ProviderItemID
	out.ProviderItemModel = in.ProviderItemModel
	out.ReasoningContent = in.ReasoningContent
	for _, value := range in.ReasoningBlocks {
		out.ReasoningBlocks = append(out.ReasoningBlocks, modelReasoningBlockFromLogical(value))
	}
	for _, value := range in.ToolCalls {
		out.ToolCalls = append(out.ToolCalls, modelToolCallFromLogical(value))
	}
	if in.Usage != nil {
		value := modelTokenUsageFromLogical(*in.Usage)
		out.Usage = &value
	}
	out.StopReason = in.StopReason
	out.FinishReason = providers.FinishReason(in.FinishReason)
	out.Truncated = in.Truncated
	return
}

// logicalModelRequest copies only the versioned model input. Retry authority,
// credentials, routing leases, transcript addresses and local paths stay in Go.
func logicalModelRequest(in providers.ChatRequest) pluginhost.LogicalModelRequestV1 {
	out := pluginhost.LogicalModelRequestV1{Messages: []pluginhost.ModelMessageV1{}, Tools: []pluginhost.ModelToolV1{}, Version: 1, Adapter: in.Adapter, Provider: in.Provider, Model: in.Model, Temperature: in.Temperature, MaxTokens: in.MaxTokens, Effort: in.Effort, ProviderOptions: providers.CloneRequestObject(in.ProviderOptions), ProviderStateScope: in.ProviderStateScope, MediaInput: modelMediaInputPolicyToLogical(in.MediaInput), NativeDeferredToolDiscovery: in.NativeDeferredToolDiscovery, ForceToolName: in.ForceToolName}
	for _, message := range in.Messages {
		out.Messages = append(out.Messages, modelChatMessageToLogical(message))
	}
	for _, tool := range in.Tools {
		out.Tools = append(out.Tools, modelToolDefinitionToLogical(tool))
	}
	if in.CacheHint != nil {
		hint := modelCacheHintToLogical(*in.CacheHint)
		out.CacheHint = &hint
	}
	return out
}

func applyLogicalModelRequest(base providers.ChatRequest, in pluginhost.LogicalModelRequestV1) (providers.ChatRequest, error) {
	if in.Version != 1 {
		return base, errors.New("unsupported logical model request version")
	}
	if strings.TrimSpace(in.Model) == "" {
		return base, errors.New("model is required")
	}
	if len(in.Messages) == 0 || len(in.Messages) > 4096 {
		return base, errors.New("messages must contain between 1 and 4096 entries")
	}
	if len(in.Tools) > 1024 {
		return base, errors.New("too many model tools")
	}
	if in.MaxTokens < 0 {
		return base, errors.New("max_tokens cannot be negative")
	}
	out := base
	out.Adapter = in.Adapter
	out.Provider = in.Provider
	out.Model = in.Model
	out.Temperature = in.Temperature
	out.MaxTokens = in.MaxTokens
	out.Effort = in.Effort
	out.ProviderOptions = providers.CloneRequestObject(in.ProviderOptions)
	out.ProviderStateScope = in.ProviderStateScope
	out.MediaInput = modelMediaInputPolicyFromLogical(in.MediaInput)
	out.NativeDeferredToolDiscovery = in.NativeDeferredToolDiscovery
	out.ForceToolName = in.ForceToolName
	out.Messages = make([]providers.ChatMessage, 0, len(in.Messages))
	out.Tools = make([]providers.ToolDefinition, 0, len(in.Tools))
	out.CacheHint = nil
	for _, message := range in.Messages {
		switch message.Role {
		case "system", "user", "assistant", "tool":
		default:
			return base, errors.New("unsupported model message role")
		}
		item := modelChatMessageFromLogical(message)
		if item.ToolResult != nil {
			if err := item.ToolResult.Validate(); err != nil {
				return base, err
			}
		}
		out.Messages = append(out.Messages, item)
	}
	for _, tool := range in.Tools {
		out.Tools = append(out.Tools, modelToolDefinitionFromLogical(tool))
	}
	if in.CacheHint != nil {
		hint := modelCacheHintFromLogical(*in.CacheHint)
		out.CacheHint = &hint
	}
	if err := providers.ValidateToolCallHistory(out.Messages); err != nil {
		return base, err
	}
	names := make(map[string]bool, len(out.Tools))
	for _, tool := range out.Tools {
		if strings.TrimSpace(tool.Name) == "" || names[tool.Name] {
			return base, errors.New("model tool names must be unique and nonempty")
		}
		names[tool.Name] = true
	}
	if out.ForceToolName != "" && !names[out.ForceToolName] {
		return base, errors.New("forced tool is unavailable")
	}
	return out, nil
}

func logicalModelResponse(in providers.ChatResponse) pluginhost.ModelResponseV1 {
	return modelChatResponseToLogical(in)
}
func modelResponseFromLogical(in pluginhost.ModelResponseV1) (providers.ChatResponse, error) {
	out := modelChatResponseFromLogical(in)
	// Failed responses still carry valid physical receipts, but invalid usage
	// must never reach accounting, even when another response field is invalid.
	if err := validateLogicalModelUsage(in.Usage); err != nil {
		out.Usage = nil
		return out, err
	}
	if err := validateLogicalModelOutput(in.Phase, in.FinishReason); err != nil {
		return out, err
	}
	if err := providers.ValidateAssistantToolCalls(out.ToolCalls); err != nil {
		return out, err
	}
	return out, nil
}

func logicalModelStreamEvent(in providers.StreamEvent) pluginhost.ModelStreamEventV1 {
	out := pluginhost.ModelStreamEventV1{Type: string(in.Type), Content: in.Content, Phase: string(in.Phase), ProviderItemID: in.ProviderItemID, ProviderEventType: in.ProviderEventType, StopReason: in.StopReason, FinishReason: string(in.FinishReason), Truncated: in.Truncated}
	if in.ProviderState != nil {
		out.ProviderState, _ = json.Marshal(in.ProviderState)
	}
	if in.Lifecycle != nil {
		lifecycle := *in.Lifecycle
		// Internal lifecycle diagnostics may contain provider response bodies.
		// Keep retry metadata while excluding raw failure text from plugins.
		if lifecycle.Reason != "" {
			lifecycle.Reason = "Model request failed"
		}
		out.Lifecycle, _ = json.Marshal(lifecycle)
		out.ResetPartial = lifecycle.ResetPartial
	}
	if in.Image != nil {
		v := modelInputImageToLogical(*in.Image)
		out.Image = &v
	}
	if in.Message != nil {
		v := modelChatMessageToLogical(*in.Message)
		out.Message = &v
	}
	if in.ReasoningBlock != nil {
		v := modelReasoningBlockToLogical(*in.ReasoningBlock)
		out.ReasoningBlock = &v
	}
	if in.ToolCall != nil {
		v := modelToolCallToLogical(*in.ToolCall)
		out.ToolCall = &v
	}
	if in.ProviderItem != nil {
		v := modelProviderItemToLogical(*in.ProviderItem)
		out.ProviderItem = &v
	}
	if in.Usage != nil {
		v := modelTokenUsageToLogical(*in.Usage)
		out.Usage = &v
	}
	if in.Error != nil {
		out.Error = modelErrorView(in.Error)
	}
	return out
}
func modelStreamEventFromLogical(in pluginhost.ModelStreamEventV1) (providers.StreamEvent, error) {
	out := providers.StreamEvent{Type: providers.StreamEventType(in.Type), Content: in.Content, Phase: providers.MessagePhase(in.Phase), ProviderItemID: in.ProviderItemID, ProviderEventType: in.ProviderEventType, StopReason: in.StopReason, FinishReason: providers.FinishReason(in.FinishReason), Truncated: in.Truncated}
	switch out.Type {
	case providers.EventContentDelta, providers.EventContentReplace, providers.EventThinkingDelta, providers.EventThinkingReplace, providers.EventThinkingDone, providers.EventImage, providers.EventToolUseStart, providers.EventToolUseDelta, providers.EventToolUseEnd, providers.EventProviderItem, providers.EventUsage, providers.EventDone, providers.EventError, providers.EventLifecycle, providers.EventProviderState, providers.EventReconnect:
	default:
		return out, errors.New("unsupported model stream event")
	}
	if err := validateLogicalModelOutput(in.Phase, in.FinishReason); err != nil {
		return out, err
	}
	if err := validateLogicalModelUsage(in.Usage); err != nil {
		return out, err
	}
	if len(in.ProviderState) > 0 {
		var state providers.ProviderStateSummary
		if err := json.Unmarshal(in.ProviderState, &state); err != nil {
			return out, errors.New("invalid provider state metadata")
		}
		out.ProviderState = &state
	}
	if len(in.Lifecycle) > 0 {
		var lifecycle providers.StreamLifecycle
		if err := json.Unmarshal(in.Lifecycle, &lifecycle); err != nil {
			return out, errors.New("invalid stream lifecycle metadata")
		}
		out.Lifecycle = &lifecycle
	}
	if in.ResetPartial {
		if out.Lifecycle == nil {
			out.Lifecycle = &providers.StreamLifecycle{}
		}
		out.Lifecycle.ResetPartial = true
	}
	if in.Image != nil {
		v := modelInputImageFromLogical(*in.Image)
		out.Image = &v
	}
	if in.Message != nil {
		v := modelChatMessageFromLogical(*in.Message)
		out.Message = &v
	}
	if in.ReasoningBlock != nil {
		v := modelReasoningBlockFromLogical(*in.ReasoningBlock)
		out.ReasoningBlock = &v
	}
	if in.ToolCall != nil {
		v := modelToolCallFromLogical(*in.ToolCall)
		out.ToolCall = &v
	}
	if in.ProviderItem != nil {
		v := modelProviderItemFromLogical(*in.ProviderItem)
		out.ProviderItem = &v
	}
	if in.Usage != nil {
		v := modelTokenUsageFromLogical(*in.Usage)
		out.Usage = &v
	}
	if in.Error != nil {
		out.Error = serviceError(in.Error.Code, in.Error.Message)
	}
	switch out.Type {
	case providers.EventImage:
		if out.Image == nil {
			return out, errors.New("image event requires image")
		}
	case providers.EventProviderItem:
		if out.ProviderItem == nil {
			return out, errors.New("provider_item event requires provider_item")
		}
	case providers.EventToolUseStart, providers.EventToolUseEnd:
		if out.ToolCall == nil {
			return out, errors.New("tool event requires tool_call")
		}
		if out.Type == providers.EventToolUseEnd {
			if err := providers.ValidateAssistantToolCalls([]providers.ToolCall{*out.ToolCall}); err != nil {
				return out, err
			}
		}
	}
	if out.Type == providers.EventError && out.Error == nil {
		return out, errors.New("error event requires an error")
	}
	return out, nil
}
func modelErrorView(err error) *pluginhost.ModelErrorV1 {
	if err == nil {
		return nil
	}
	var hostErr *pluginhost.HostServiceError
	if errors.As(err, &hostErr) {
		return &pluginhost.ModelErrorV1{Code: hostErr.Code, Message: hostErr.Message}
	}
	safe := modelRequestError(err)
	if errors.As(safe, &hostErr) {
		return &pluginhost.ModelErrorV1{Code: hostErr.Code, Message: hostErr.Message}
	}
	return &pluginhost.ModelErrorV1{Code: "inference_failed", Message: "model request failed"}
}

func validateLogicalModelOutput(phase, finish string) error {
	if phase != "" && providers.NormalizeMessagePhase(phase) == "" {
		return errors.New("unsupported model output phase")
	}
	switch providers.FinishReason(finish) {
	case "", providers.FinishReasonStop, providers.FinishReasonContinue, providers.FinishReasonLength, providers.FinishReasonToolCalls, providers.FinishReasonContentFilter, providers.FinishReasonError, providers.FinishReasonUnknown:
	default:
		return errors.New("unsupported finish_reason")
	}
	return nil
}

func validateLogicalModelUsage(usage *pluginhost.ModelRequestUsage) error {
	if usage != nil && (usage.InputTokens < 0 || usage.OutputTokens < 0 || usage.CacheCreationTokens < 0 || usage.CacheReadTokens < 0) {
		return errors.New("model usage cannot be negative")
	}
	return nil
}
