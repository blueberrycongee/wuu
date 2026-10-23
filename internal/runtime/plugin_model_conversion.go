package runtime

import (
	"encoding/json"
	"errors"

	"github.com/blueberrycongee/wuu/internal/modelwire"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/providers"
)

func logicalModelResponse(in providers.ChatResponse) pluginhost.ModelResponseV1 {
	return modelwire.ChatResponseToWire(in)
}
func modelResponseFromLogical(in pluginhost.ModelResponseV1) (providers.ChatResponse, error) {
	out := modelwire.ChatResponseFromWire(in)
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
		v := modelwire.InputImageToWire(*in.Image)
		out.Image = &v
	}
	if in.Message != nil {
		v := modelwire.ChatMessageToWire(*in.Message)
		out.Message = &v
	}
	if in.ReasoningBlock != nil {
		v := modelwire.ReasoningBlockToWire(*in.ReasoningBlock)
		out.ReasoningBlock = &v
	}
	if in.ToolCall != nil {
		v := modelwire.ToolCallToWire(*in.ToolCall)
		out.ToolCall = &v
	}
	if in.ProviderItem != nil {
		v := modelwire.ProviderItemToWire(*in.ProviderItem)
		out.ProviderItem = &v
	}
	if in.Usage != nil {
		v := modelwire.TokenUsageToWire(*in.Usage)
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
		v := modelwire.InputImageFromWire(*in.Image)
		out.Image = &v
	}
	if in.Message != nil {
		v := modelwire.ChatMessageFromWire(*in.Message)
		out.Message = &v
	}
	if in.ReasoningBlock != nil {
		v := modelwire.ReasoningBlockFromWire(*in.ReasoningBlock)
		out.ReasoningBlock = &v
	}
	if in.ToolCall != nil {
		v := modelwire.ToolCallFromWire(*in.ToolCall)
		out.ToolCall = &v
	}
	if in.ProviderItem != nil {
		v := modelwire.ProviderItemFromWire(*in.ProviderItem)
		out.ProviderItem = &v
	}
	if in.Usage != nil {
		v := modelwire.TokenUsageFromWire(*in.Usage)
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
