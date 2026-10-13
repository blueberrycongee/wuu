package pluginhost

import "github.com/blueberrycongee/wuu/internal/pluginprotocol"

const (
	KernelModelInvokeService        = pluginprotocol.KernelModelInvokeService
	CapabilityModelRequestTransform = pluginprotocol.CapabilityModelRequestTransform
	CapabilityModelRequestObserve   = pluginprotocol.CapabilityModelRequestObserve
	CapabilityModelMiddleware       = pluginprotocol.CapabilityModelMiddleware
)

// These aliases keep host callers on the internal wire contract; provider state
// and plugin authoring remain separate.
type LogicalModelRequestV1 = pluginprotocol.LogicalModelRequestV1
type ModelRequestV1 = pluginprotocol.ModelRequestV1
type ModelCacheHintV1 = pluginprotocol.ModelCacheHintV1
type ModelMediaInputV1 = pluginprotocol.ModelMediaInputV1
type ModelImageV1 = pluginprotocol.ModelImageV1
type ModelFileV1 = pluginprotocol.ModelFileV1
type ModelReasoningBlockV1 = pluginprotocol.ModelReasoningBlockV1
type ModelProviderItemV1 = pluginprotocol.ModelProviderItemV1
type ModelToolCallV1 = pluginprotocol.ModelToolCallV1
type ModelToolV1 = pluginprotocol.ModelToolV1
type ModelLoadableToolV1 = pluginprotocol.ModelLoadableToolV1
type ModelToolResultV1 = pluginprotocol.ModelToolResultV1
type ModelMessageV1 = pluginprotocol.ModelMessageV1
type ModelInvocationOriginV1 = pluginprotocol.ModelInvocationOriginV1
type ModelResponseV1 = pluginprotocol.ModelResponseV1
type ModelErrorV1 = pluginprotocol.ModelErrorV1
type ModelStreamEventV1 = pluginprotocol.ModelStreamEventV1
type ModelRequestMiddlewareInput = pluginprotocol.ModelRequestMiddlewareInput
type ModelRequestMiddlewareOutput = pluginprotocol.ModelRequestMiddlewareOutput
type ModelResponseObserveInput = pluginprotocol.ModelResponseObserveInput
type ModelRequestObserveInput = pluginprotocol.ModelRequestObserveInput
type ModelMiddlewareInput = pluginprotocol.ModelMiddlewareInput
type ModelMiddlewareOutput = pluginprotocol.ModelMiddlewareOutput
type ModelInvokeParams = pluginprotocol.ModelInvokeParams
type ModelInvokeResult = pluginprotocol.ModelInvokeResult
type ModelInvocationHandle = pluginprotocol.ModelInvocationHandle
type ModelInvocationReadParams = pluginprotocol.ModelInvocationReadParams
type ModelInvocationReadResult = pluginprotocol.ModelInvocationReadResult
type ModelAdapterInput = pluginprotocol.ModelAdapterInput
type ModelAdapterOutput = pluginprotocol.ModelAdapterOutput

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
