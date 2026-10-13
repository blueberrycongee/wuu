package pluginhost

import pluginapi "github.com/blueberrycongee/wuu/packages/plugin-go"

const (
	KernelModelInvokeService        = pluginapi.KernelModelInvokeService
	CapabilityModelRequestTransform = pluginapi.CapabilityModelRequestTransform
	CapabilityModelRequestObserve   = pluginapi.CapabilityModelRequestObserve
	CapabilityModelMiddleware       = pluginapi.CapabilityModelMiddleware
)

// The public SDK owns these wire DTOs; provider and runtime state stay internal.
type LogicalModelRequestV1 = pluginapi.LogicalModelRequestV1
type ModelRequestV1 = pluginapi.ModelRequestV1
type ModelCacheHintV1 = pluginapi.ModelCacheHintV1
type ModelMediaInputV1 = pluginapi.ModelMediaInputV1
type ModelImageV1 = pluginapi.ModelImageV1
type ModelFileV1 = pluginapi.ModelFileV1
type ModelReasoningBlockV1 = pluginapi.ModelReasoningBlockV1
type ModelProviderItemV1 = pluginapi.ModelProviderItemV1
type ModelToolCallV1 = pluginapi.ModelToolCallV1
type ModelToolV1 = pluginapi.ModelToolV1
type ModelLoadableToolV1 = pluginapi.ModelLoadableToolV1
type ModelToolResultV1 = pluginapi.ModelToolResultV1
type ModelMessageV1 = pluginapi.ModelMessageV1
type ModelInvocationOriginV1 = pluginapi.ModelInvocationOriginV1
type ModelResponseV1 = pluginapi.ModelResponseV1
type ModelErrorV1 = pluginapi.ModelErrorV1
type ModelStreamEventV1 = pluginapi.ModelStreamEventV1
type ModelRequestMiddlewareInput = pluginapi.ModelRequestMiddlewareInput
type ModelRequestMiddlewareOutput = pluginapi.ModelRequestMiddlewareOutput
type ModelResponseObserveInput = pluginapi.ModelResponseObserveInput
type ModelRequestObserveInput = pluginapi.ModelRequestObserveInput
type ModelMiddlewareInput = pluginapi.ModelMiddlewareInput
type ModelMiddlewareOutput = pluginapi.ModelMiddlewareOutput
type ModelInvokeParams = pluginapi.ModelInvokeParams
type ModelInvokeResult = pluginapi.ModelInvokeResult
type ModelInvocationHandle = pluginapi.ModelInvocationHandle
type ModelInvocationReadParams = pluginapi.ModelInvocationReadParams
type ModelInvocationReadResult = pluginapi.ModelInvocationReadResult
type ModelAdapterInput = pluginapi.ModelAdapterInput
type ModelAdapterOutput = pluginapi.ModelAdapterOutput

func KernelModelInvokeDescriptor() ServiceDescriptor {
	return ServiceDescriptor{
		Name: KernelModelInvokeService, Version: "1.0.0",
		Methods: []ServiceMethodDescriptor{
			{Name: "call", InputSchema: "host.model.invoke.input.v1", OutputSchema: "host.model.invoke.output.v1"},
			{Name: "start", InputSchema: "host.model.invoke.input.v1", OutputSchema: "host.model.invoke.handle.v1"},
			{Name: "read", InputSchema: "host.model.invoke.read.input.v1", OutputSchema: "host.model.invoke.read.output.v1"},
			{Name: "cancel", InputSchema: "host.model.invoke.handle.v1", OutputSchema: "host.model.invoke.cancel.output.v1"},
		},
	}
}
