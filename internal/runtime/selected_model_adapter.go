package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"slices"

	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/modelwire"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/pluginprotocol"
	"github.com/blueberrycongee/wuu/internal/providerfactory"
	"github.com/blueberrycongee/wuu/internal/providers"
)

// A route carries configuration identity, not a private native transport. The
// selected graph provider owns credentials, connection pools and construction.
type modelRouteClient struct {
	provider, model string
	host            *pluginhost.Host
}

func (c *modelRouteClient) pipeline() providers.StreamClient {
	return pluginModelPipelineClient(c.host, c, pluginhost.ModelInvocationOriginV1{Kind: "auxiliary"}, false)
}
func (c *modelRouteClient) Chat(ctx context.Context, req providers.ChatRequest) (providers.ChatResponse, error) {
	return c.pipeline().Chat(ctx, req)
}
func (c *modelRouteClient) StreamChat(ctx context.Context, req providers.ChatRequest) (<-chan providers.StreamEvent, error) {
	return c.pipeline().StreamChat(ctx, req)
}
func (c *modelRouteClient) PrepareInferenceRequest(ctx context.Context, req providers.ChatRequest) (providers.ChatRequest, error) {
	return c.pipeline().(providers.InferenceRequestPreparer).PrepareInferenceRequest(ctx, req)
}
func (c *modelRouteClient) ApplyInferenceRequestRecovery(ctx context.Context, req providers.ChatRequest, plan providers.RecoveryPlan) error {
	return (&modelAdapterClient{host: c.host, inner: c}).ApplyInferenceRequestRecovery(ctx, req, plan)
}
func (c *modelRouteClient) ApplyInferenceRecovery(ctx context.Context, plan providers.RecoveryPlan) error {
	return (&modelAdapterClient{host: c.host, inner: c}).ApplyInferenceRecovery(ctx, plan)
}
func (c *modelRouteClient) PrewarmSession(ctx context.Context, id string) error {
	return (&modelAdapterClient{host: c.host, inner: c}).PrewarmSession(ctx, id)
}
func (c *modelRouteClient) ResolveMediaInput(policy providers.MediaInputPolicy, options map[string]any) providers.MediaInputPolicy {
	return (&modelAdapterClient{host: c.host, inner: c}).ResolveMediaInput(policy, options)
}
func selectedRouteClient(host *pluginhost.Host, cfg config.ProviderConfig, name string, bindings providerfactory.Bindings) (providers.StreamClient, error) {
	if host == nil {
		return providerfactory.BuildRuntimeStreamClient(cfg, name, bindings)
	}
	return &modelRouteClient{provider: name, model: cfg.Model, host: host}, nil
}
func (c *modelAdapterClient) configuredModel() string {
	if route, ok := c.inner.(*modelRouteClient); ok {
		return route.model
	}
	return ""
}
func (c *modelAdapterClient) baseRequest() providers.ChatRequest {
	if route, ok := c.inner.(*modelRouteClient); ok {
		return providers.ChatRequest{Provider: route.provider, Model: route.model}
	}
	return providers.ChatRequest{}
}
func (c *modelAdapterClient) selected(ctx context.Context, request providers.ChatRequest) (providers.ChatRequest, pluginhost.RegisteredModelAdapter, error) {
	if route, ok := c.inner.(*modelRouteClient); ok {
		if request.Provider == "" {
			request.Provider = route.provider
		}
		if request.Model == "" {
			request.Model = route.model
		}
	}
	request, err := selectedModelAdapter(request)
	if err != nil {
		return request, pluginhost.RegisteredModelAdapter{}, err
	}
	if request.Adapter == "" || request.Adapter == "native" {
		raw, callErr := c.host.CallService(ctx, modelExecutionConsumer, selectedModelService, "describe", json.RawMessage(`{}`))
		if callErr != nil {
			return request, pluginhost.RegisteredModelAdapter{}, callErr
		}
		var descriptor pluginhost.ModelAdapterDescriptor
		if err = json.Unmarshal(raw, &descriptor); err != nil {
			return request, pluginhost.RegisteredModelAdapter{}, err
		}
		capability, err := c.host.ModelAdapter(descriptor.ID)
		if err != nil {
			return request, capability, err
		}
		owner, ok := c.host.ServiceRegistry().ProviderPluginID(selectedModelService, 1)
		if !ok || owner != capability.PluginID {
			return request, capability, errors.New("selected model descriptor must name its own adapter capability")
		}
		request.Adapter = descriptor.ID
		return request, capability, nil
	}
	capability, err := c.host.ModelAdapter(request.Adapter)
	return request, capability, err
}
func (c *modelAdapterClient) auxiliary(ctx context.Context, operation string, req providers.ChatRequest, session, action string) (result pluginprotocol.ModelAdapterOutput, resultErr error) {
	req, capability, err := c.selected(ctx, req)
	if err != nil {
		return pluginprotocol.ModelAdapterOutput{}, err
	}
	if !slices.Contains(capability.Adapter.Operations, operation) {
		if operation == "prepare" {
			wire := modelwire.RequestToWire(req)
			return pluginprotocol.ModelAdapterOutput{Request: &wire}, nil
		}
		if operation == "media" {
			wire := modelwire.RequestToWire(req)
			return pluginprotocol.ModelAdapterOutput{Media: &wire.MediaInput}, nil
		}
		if operation == "prewarm" || operation == "compact" || operation == "quota" {
			return pluginprotocol.ModelAdapterOutput{}, nil
		}
		return pluginprotocol.ModelAdapterOutput{}, errors.New("selected model adapter does not support " + operation)
	}
	input := pluginprotocol.ModelAdapterInput{ConfiguredModel: c.configuredModel(), AdapterID: req.Adapter, Operation: operation, Request: modelwire.RequestToWire(req), SessionID: session, RecoveryAction: action}
	release := func() {}
	var directSubmission providers.InferenceSubmission
	var directCapacity *modelCapacityAdmission
	if operation == "compact" {
		if !req.Attempt.Valid() {
			return pluginprotocol.ModelAdapterOutput{}, errors.New("compaction requires an admitted attempt")
		}
		scope, scopeErr := c.host.OpenServiceScope(ctx, modelExecutionConsumer, capacityPolicyService, capacityResourceService)
		if scopeErr != nil {
			return pluginprotocol.ModelAdapterOutput{}, scopeErr
		}
		defer func() { resultErr = errors.Join(resultErr, scope.Close()) }()
		ctx = scope.Context
		directSubmission, directCapacity, err = admitPluginModelSubmission(ctx, c.host, req, capability.Adapter.Mode)
		if err != nil {
			return pluginprotocol.ModelAdapterOutput{}, err
		}
		input.Receipt, release = adapterReceipt(ctx, c.host, capability, req)
	}
	defer release()
	var output pluginprotocol.ModelAdapterOutput
	err = c.host.InvokeCapability(ctx, capability.RegisteredCapability, input, &output)
	if err == nil && output.Error != nil {
		err = modelErrorFromWire(output.Error)
	}
	if operation == "compact" {
		response := modelwire.ChatResponseFromWire(pluginprotocol.ModelResponseV1{Usage: output.Usage})
		err = errors.Join(err, settlePluginModelSubmission(req, directSubmission, response.Usage, err), settleModelCapacityError(directCapacity, err))
	}
	return output, err
}
func adapterReceipt(ctx context.Context, host *pluginhost.Host, capability pluginhost.RegisteredModelAdapter, req providers.ChatRequest) (*pluginprotocol.ModelAdapterReceipt, func()) {
	if capability.Adapter.Mode != "receipt" {
		return nil, func() {}
	}
	id, release := registerModelReceipt(ctx, host, capability.PluginID, req.Attempt, req.Provider)
	operation := req.Attempt.Operation()
	return &pluginprotocol.ModelAdapterReceipt{ID: id, AttemptID: req.Attempt.ID, AttemptOrdinal: req.Attempt.Ordinal, OperationID: operation.ID, Profile: string(operation.WorkloadProfile)}, release
}

// ReadModelQuota is opt-in auxiliary work through the captured model provider.
// It does not allocate an inference attempt or native fallback client.
func (s *State) ReadModelQuota(ctx context.Context, provider string) (providers.Quota, error) {
	if s == nil || s.Host == nil {
		return providers.Quota{}, providers.NewQuotaError(providers.QuotaErrorUnsupported)
	}
	adapter := &modelAdapterClient{host: s.Host, inner: &modelRouteClient{host: s.Host, provider: provider}}
	output, err := adapter.auxiliary(ctx, "quota", providers.ChatRequest{Provider: provider}, "", "")
	if err != nil {
		return providers.Quota{}, err
	}
	if !output.Supported {
		return providers.Quota{}, providers.NewQuotaError(providers.QuotaErrorUnsupported)
	}
	var quota providers.Quota
	if err = json.Unmarshal(output.Quota, &quota); err != nil {
		return quota, providers.NewQuotaError(providers.QuotaErrorInvalidResponse)
	}
	if output.QuotaError != "" {
		return quota, providers.NewQuotaError(output.QuotaError)
	}
	return quota, nil
}

// ModelClient resolves inference through this captured state's model provider.
// Keep State retained until all calls and physical receipt tails finish. The
// selected config provider, not an arbitrary supplied transport, owns routing;
// cfg supplies the already-resolved configured model identity.
func (s *State) ModelClient(cfg config.ProviderConfig, provider string) (providers.StreamClient, error) {
	if s == nil || s.Host == nil {
		return nil, serviceError("service_unavailable", "selected model provider graph is unavailable")
	}
	return selectedRouteClient(s.Host, cfg, provider, providerfactory.Bindings{})
}

// ValidateSelectedModelConfiguration performs opt-in candidate validation in the
// selected provider. It neither installs the candidate nor performs inference.
func ValidateSelectedModelConfiguration(ctx context.Context, host *pluginhost.Host, cfg config.ProviderConfig, provider string) error {
	adapter := &modelAdapterClient{host: host, inner: &modelRouteClient{host: host, provider: provider, model: cfg.Model}}
	request, capability, err := adapter.selected(ctx, providers.ChatRequest{Provider: provider, Model: cfg.Model})
	if err != nil {
		return err
	}
	if !slices.Contains(capability.Adapter.Operations, "validate") {
		return errors.New("selected model provider does not support candidate configuration validation")
	}
	raw, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	defer clear(raw)
	var output pluginprotocol.ModelAdapterOutput
	err = host.InvokeCapability(ctx, capability.RegisteredCapability, pluginprotocol.ModelAdapterInput{AdapterID: request.Adapter, Operation: "validate", Configuration: raw, Request: modelwire.RequestToWire(request)}, &output)
	if err != nil {
		return modelRequestError(err)
	}
	if output.Error != nil {
		return modelRequestError(modelErrorFromWire(output.Error))
	}
	if !output.Supported {
		return errors.New("selected model provider rejected candidate configuration validation")
	}
	return nil
}
