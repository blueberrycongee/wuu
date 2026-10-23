// Package modelwire converts portable model data to the public service protocol.
package modelwire

import (
	"encoding/json"
	"errors"
	"strings"

	"github.com/blueberrycongee/wuu/internal/pluginprotocol"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/toolresult"
)

func CacheHintToWire(in providers.CacheHint) (out pluginprotocol.ModelCacheHintV1) {
	out.PromptCacheKey = in.PromptCacheKey
	out.StableSystem = in.StableSystem
	out.StablePrefixMessages = in.StablePrefixMessages
	out.TurnPrefixMessages = in.TurnPrefixMessages
	out.HasCompactSummary = in.HasCompactSummary
	return
}

func CacheHintFromWire(in pluginprotocol.ModelCacheHintV1) (out providers.CacheHint) {
	out.PromptCacheKey = in.PromptCacheKey
	out.StableSystem = in.StableSystem
	out.StablePrefixMessages = in.StablePrefixMessages
	out.TurnPrefixMessages = in.TurnPrefixMessages
	out.HasCompactSummary = in.HasCompactSummary
	return
}

func MediaInputPolicyToWire(in providers.MediaInputPolicy) (out pluginprotocol.ModelMediaInputV1) {
	out.Video = in.Video
	out.VideoKnown = in.VideoKnown
	out.Image = in.Image
	out.ImageKnown = in.ImageKnown
	out.File = in.File
	out.FileKnown = in.FileKnown
	return
}

func MediaInputPolicyFromWire(in pluginprotocol.ModelMediaInputV1) (out providers.MediaInputPolicy) {
	out.Video = in.Video
	out.VideoKnown = in.VideoKnown
	out.Image = in.Image
	out.File = in.File
	out.ImageKnown = in.ImageKnown
	out.FileKnown = in.FileKnown
	return
}

func InputImageToWire(in providers.InputImage) (out pluginprotocol.ModelImageV1) {
	out.MediaType = in.MediaType
	out.Data = in.Data
	out.Required = in.Required
	out.Width = in.Width
	out.Height = in.Height
	out.ProviderItemID = in.ProviderItemID
	return
}

func InputImageFromWire(in pluginprotocol.ModelImageV1) (out providers.InputImage) {
	out.ProviderItemID = in.ProviderItemID
	out.Required = in.Required
	out.MediaType = in.MediaType
	out.Data = in.Data
	out.Width = in.Width
	out.Height = in.Height
	return
}

func InputFileToWire(in providers.InputFile) (out pluginprotocol.ModelFileV1) {
	out.MediaType = in.MediaType
	out.Data = in.Data
	out.Filename = in.Filename
	out.SizeBytes = in.SizeBytes
	out.Required = in.Required
	return
}

func InputFileFromWire(in pluginprotocol.ModelFileV1) (out providers.InputFile) {
	out.SizeBytes = in.SizeBytes
	out.Required = in.Required
	out.MediaType = in.MediaType
	out.Data = in.Data
	out.Filename = in.Filename
	return
}

func ReasoningBlockToWire(in providers.ReasoningBlock) (out pluginprotocol.ModelReasoningBlockV1) {
	out.Type = in.Type
	out.Thinking = in.Thinking
	out.Signature = in.Signature
	out.Data = in.Data
	return
}

func ReasoningBlockFromWire(in pluginprotocol.ModelReasoningBlockV1) (out providers.ReasoningBlock) {
	out.Type = in.Type
	out.Thinking = in.Thinking
	out.Signature = in.Signature
	out.Data = in.Data
	return
}

func ProviderItemToWire(in providers.ProviderItem) (out pluginprotocol.ModelProviderItemV1) {
	out.Type = in.Type
	out.Data = in.Data
	out.Provider = in.Provider
	out.Scope = in.Scope
	for _, value := range in.Fallback {
		out.Fallback = append(out.Fallback, ChatMessageToWire(value))
	}
	return
}

func ProviderItemFromWire(in pluginprotocol.ModelProviderItemV1) (out providers.ProviderItem) {
	out.Type = in.Type
	out.Data = in.Data
	out.Provider = in.Provider
	out.Scope = in.Scope
	for _, value := range in.Fallback {
		out.Fallback = append(out.Fallback, ChatMessageFromWire(value))
	}
	return
}

func ToolCallToWire(in providers.ToolCall) (out pluginprotocol.ModelToolCallV1) {
	out.ID = in.ID
	out.Name = in.Name
	out.Arguments = in.Arguments
	out.Kind = string(in.Kind)
	out.ProviderItemID = in.ProviderItemID
	out.ProviderItemProvider = in.ProviderItemProvider
	out.ProviderItemModel = in.ProviderItemModel
	return
}

func ToolCallFromWire(in pluginprotocol.ModelToolCallV1) (out providers.ToolCall) {
	out.ID = in.ID
	out.ProviderItemID = in.ProviderItemID
	out.ProviderItemProvider = in.ProviderItemProvider
	out.ProviderItemModel = in.ProviderItemModel
	out.Name = in.Name
	out.Arguments = in.Arguments
	out.Kind = providers.ToolCallKind(in.Kind)
	return
}

func ToolDefinitionToWire(in providers.ToolDefinition) (out pluginprotocol.ModelToolV1) {
	out.Name = in.Name
	out.Description = in.Description
	out.InputSchema = providers.CloneRequestObject(in.InputSchema)
	out.Freeform = in.Freeform
	out.DirectOnly = in.DirectOnly
	out.DeferLoading = in.DeferLoading
	out.CacheStable = in.CacheStable
	return
}

func ToolDefinitionFromWire(in pluginprotocol.ModelToolV1) (out providers.ToolDefinition) {
	out.Name = in.Name
	out.Description = in.Description
	out.InputSchema = providers.CloneRequestObject(in.InputSchema)
	out.Freeform = in.Freeform
	out.DirectOnly = in.DirectOnly
	out.DeferLoading = in.DeferLoading
	out.CacheStable = in.CacheStable
	return
}

func LoadableToolDefinitionToWire(in providers.LoadableToolDefinition) (out pluginprotocol.ModelLoadableToolV1) {
	out.Type = in.Type
	out.Name = in.Name
	out.Description = in.Description
	out.InputSchema = providers.CloneRequestObject(in.InputSchema)
	out.DeferLoading = in.DeferLoading
	return
}

func LoadableToolDefinitionFromWire(in pluginprotocol.ModelLoadableToolV1) (out providers.LoadableToolDefinition) {
	out.Type = in.Type
	out.Name = in.Name
	out.Description = in.Description
	out.InputSchema = providers.CloneRequestObject(in.InputSchema)
	out.DeferLoading = in.DeferLoading
	return
}

func ChatMessageToWire(in providers.ChatMessage) (out pluginprotocol.ModelMessageV1) {
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
		out.ReasoningBlocks = append(out.ReasoningBlocks, ReasoningBlockToWire(value))
	}
	for _, value := range in.ProviderItems {
		out.ProviderItems = append(out.ProviderItems, ProviderItemToWire(value))
	}
	out.ProviderItemID = in.ProviderItemID
	out.ProviderItemProvider = in.ProviderItemProvider
	out.ProviderItemModel = in.ProviderItemModel
	for _, value := range in.Images {
		out.Images = append(out.Images, InputImageToWire(value))
	}
	for _, value := range in.Files {
		out.Files = append(out.Files, InputFileToWire(value))
	}
	out.ToolCallID = in.ToolCallID
	out.ToolResultKind = string(in.ToolResultKind)
	if in.ToolResult != nil {
		raw, _ := json.Marshal(in.ToolResult)
		var value pluginprotocol.ModelToolResultV1
		_ = json.Unmarshal(raw, &value)
		out.ToolResult = &value
	}
	for _, value := range in.ToolCalls {
		out.ToolCalls = append(out.ToolCalls, ToolCallToWire(value))
	}
	for _, value := range in.DiscoveredTools {
		out.DiscoveredTools = append(out.DiscoveredTools, LoadableToolDefinitionToWire(value))
	}
	out.FinishReason = string(in.FinishReason)
	out.StopReason = in.StopReason
	out.Truncated = in.Truncated
	return
}

func ChatMessageFromWire(in pluginprotocol.ModelMessageV1) (out providers.ChatMessage) {
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
		out.ReasoningBlocks = append(out.ReasoningBlocks, ReasoningBlockFromWire(value))
	}
	for _, value := range in.ProviderItems {
		out.ProviderItems = append(out.ProviderItems, ProviderItemFromWire(value))
	}
	for _, value := range in.Images {
		out.Images = append(out.Images, InputImageFromWire(value))
	}
	for _, value := range in.Files {
		out.Files = append(out.Files, InputFileFromWire(value))
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
		out.ToolCalls = append(out.ToolCalls, ToolCallFromWire(value))
	}
	out.FinishReason = providers.FinishReason(in.FinishReason)
	out.StopReason = in.StopReason
	out.Truncated = in.Truncated
	for _, value := range in.DiscoveredTools {
		out.DiscoveredTools = append(out.DiscoveredTools, LoadableToolDefinitionFromWire(value))
	}
	return
}

func TokenUsageToWire(in providers.TokenUsage) (out pluginprotocol.ModelRequestUsage) {
	out.InputTokens = in.InputTokens
	out.OutputTokens = in.OutputTokens
	out.CacheCreationTokens = in.CacheCreationTokens
	out.CacheReadTokens = in.CacheReadTokens
	out.CacheCreationUnknown = in.CacheCreationUnknown
	return
}

func TokenUsageFromWire(in pluginprotocol.ModelRequestUsage) (out providers.TokenUsage) {
	out.InputTokens = in.InputTokens
	out.OutputTokens = in.OutputTokens
	out.CacheCreationTokens = in.CacheCreationTokens
	out.CacheReadTokens = in.CacheReadTokens
	out.CacheCreationUnknown = in.CacheCreationUnknown
	return
}

func ChatResponseToWire(in providers.ChatResponse) (out pluginprotocol.ModelResponseV1) {
	out.Content = in.Content
	out.Phase = string(in.Phase)
	out.ProviderItemID = in.ProviderItemID
	out.ProviderItemModel = in.ProviderItemModel
	out.ReasoningContent = in.ReasoningContent
	for _, value := range in.ReasoningBlocks {
		out.ReasoningBlocks = append(out.ReasoningBlocks, ReasoningBlockToWire(value))
	}
	for _, value := range in.ProviderItems {
		out.ProviderItems = append(out.ProviderItems, ProviderItemToWire(value))
	}
	for _, value := range in.Images {
		out.Images = append(out.Images, InputImageToWire(value))
	}
	for _, value := range in.ToolCalls {
		out.ToolCalls = append(out.ToolCalls, ToolCallToWire(value))
	}
	if in.Usage != nil {
		value := TokenUsageToWire(*in.Usage)
		out.Usage = &value
	}
	out.StopReason = in.StopReason
	out.FinishReason = string(in.FinishReason)
	out.Truncated = in.Truncated
	return
}

func ChatResponseFromWire(in pluginprotocol.ModelResponseV1) (out providers.ChatResponse) {
	for _, value := range in.ProviderItems {
		out.ProviderItems = append(out.ProviderItems, ProviderItemFromWire(value))
	}
	for _, value := range in.Images {
		out.Images = append(out.Images, InputImageFromWire(value))
	}
	out.Content = in.Content
	out.Phase = providers.MessagePhase(in.Phase)
	out.ProviderItemID = in.ProviderItemID
	out.ProviderItemModel = in.ProviderItemModel
	out.ReasoningContent = in.ReasoningContent
	for _, value := range in.ReasoningBlocks {
		out.ReasoningBlocks = append(out.ReasoningBlocks, ReasoningBlockFromWire(value))
	}
	for _, value := range in.ToolCalls {
		out.ToolCalls = append(out.ToolCalls, ToolCallFromWire(value))
	}
	if in.Usage != nil {
		value := TokenUsageFromWire(*in.Usage)
		out.Usage = &value
	}
	out.StopReason = in.StopReason
	out.FinishReason = providers.FinishReason(in.FinishReason)
	out.Truncated = in.Truncated
	return
}

// RequestToWire copies only the versioned model input. Retry authority,
// credentials, routing leases, transcript addresses and local paths stay in Go.
func RequestToWire(in providers.ChatRequest) pluginprotocol.LogicalModelRequestV1 {
	out := pluginprotocol.LogicalModelRequestV1{Messages: []pluginprotocol.ModelMessageV1{}, Tools: []pluginprotocol.ModelToolV1{}, Version: 1, Adapter: in.Adapter, Provider: in.Provider, Model: in.Model, Temperature: in.Temperature, MaxTokens: in.MaxTokens, Effort: in.Effort, ProviderOptions: providers.CloneRequestObject(in.ProviderOptions), ProviderStateScope: in.ProviderStateScope, MediaInput: MediaInputPolicyToWire(in.MediaInput), NativeDeferredToolDiscovery: in.NativeDeferredToolDiscovery, ForceToolName: in.ForceToolName}
	for _, message := range in.Messages {
		out.Messages = append(out.Messages, ChatMessageToWire(message))
	}
	for _, tool := range in.Tools {
		out.Tools = append(out.Tools, ToolDefinitionToWire(tool))
	}
	if in.CacheHint != nil {
		hint := CacheHintToWire(*in.CacheHint)
		out.CacheHint = &hint
	}
	return out
}

func ApplyRequest(base providers.ChatRequest, in pluginprotocol.LogicalModelRequestV1) (providers.ChatRequest, error) {
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
	out := requestFromWire(base, in)
	for _, message := range out.Messages {
		switch message.Role {
		case "system", "user", "assistant", "tool":
		default:
			return base, errors.New("unsupported model message role")
		}
		if message.ToolResult != nil {
			if err := message.ToolResult.Validate(); err != nil {
				return base, err
			}
		}
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

func requestFromWire(base providers.ChatRequest, in pluginprotocol.LogicalModelRequestV1) providers.ChatRequest {
	out := base
	out.Adapter = in.Adapter
	out.Provider = in.Provider
	out.Model = in.Model
	out.Temperature = in.Temperature
	out.MaxTokens = in.MaxTokens
	out.Effort = in.Effort
	out.ProviderOptions = providers.CloneRequestObject(in.ProviderOptions)
	out.ProviderStateScope = in.ProviderStateScope
	out.MediaInput = MediaInputPolicyFromWire(in.MediaInput)
	out.NativeDeferredToolDiscovery = in.NativeDeferredToolDiscovery
	out.ForceToolName = in.ForceToolName
	out.Messages = make([]providers.ChatMessage, 0, len(in.Messages))
	out.Tools = make([]providers.ToolDefinition, 0, len(in.Tools))
	out.CacheHint = nil
	for _, message := range in.Messages {
		out.Messages = append(out.Messages, ChatMessageFromWire(message))
	}
	for _, tool := range in.Tools {
		out.Tools = append(out.Tools, ToolDefinitionFromWire(tool))
	}
	if in.CacheHint != nil {
		hint := CacheHintFromWire(*in.CacheHint)
		out.CacheHint = &hint
	}
	return out
}
