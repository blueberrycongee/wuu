package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/credentialstore"
	"github.com/blueberrycongee/wuu/internal/modelcatalog"
	"github.com/blueberrycongee/wuu/internal/modelwire"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/pluginprotocol"
	"github.com/blueberrycongee/wuu/internal/providerfactory"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/siwc"
)

const nativeModelModule = "wuu.model.native"
const selectedModelService = "model.adapter"
const nativeModelAdapter = "wuu.native"

type nativeModelProvider struct {
	mu       sync.Mutex
	clientMu sync.Mutex
	registry *pluginhost.ServiceRegistry
	state    pluginhost.State
	home     string
	manager  *siwc.Manager
	clients  map[string]providers.StreamClient
	config   config.Config
}

func nativeModelProviderFactory() NativeProviderFactory {
	return NativeProviderFactory{ID: nativeModelModule, DefaultEnabled: true, Build: func(input NativeProviderContext) (pluginhost.Client, error) {
		return &nativeModelProvider{registry: input.Registry, home: input.WuuHome, state: pluginhost.StatePrepared, clients: map[string]providers.StreamClient{}}, nil
	}}
}
func (n *nativeModelProvider) ID() string { return nativeModelModule }
func (n *nativeModelProvider) Status() pluginhost.Status {
	n.mu.Lock()
	defer n.mu.Unlock()
	return pluginhost.Status{ID: n.ID(), State: n.state}
}
func (n *nativeModelProvider) Prepare(ctx context.Context) error {
	call := func(ctx context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
		result, err := n.registry.Call(ctx, n.ID(), pluginhost.ServiceCallParams{Service: config.ServiceName, Method: method, Params: raw})
		if err != nil {
			return nil, err
		}
		return result, nil
	}
	view, err := config.NewClient(call).Read(ctx)
	if err != nil {
		return err
	}
	n.config = view.Config
	return nil
}
func (n *nativeModelProvider) Activate(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	n.state = pluginhost.StateActive
	return nil
}
func (n *nativeModelProvider) Close(context.Context) error {
	n.mu.Lock()
	n.state = pluginhost.StateStopped
	n.mu.Unlock()
	n.clientMu.Lock()
	defer n.clientMu.Unlock()
	var result error
	for _, client := range n.clients {
		if closer, ok := client.(interface{ Close() error }); ok {
			result = errors.Join(result, closer.Close())
		}
	}
	n.clients = nil
	if n.manager != nil {
		result = errors.Join(result, n.manager.Close())
	}
	return result
}
func (n *nativeModelProvider) ProtocolVersion() int { return 2 }
func (n *nativeModelProvider) Capabilities() []pluginhost.CapabilityDescriptor {
	return []pluginhost.CapabilityDescriptor{{ID: pluginhost.CapabilityModelAdapter, Version: 1, ErrorPolicy: pluginhost.ErrorPolicyPropagate}}
}
func (n *nativeModelProvider) ModelAdapters() []pluginhost.ModelAdapterDescriptor {
	return []pluginhost.ModelAdapterDescriptor{{ID: nativeModelAdapter, Version: 1, Mode: "receipt", Operations: []string{"prepare", "media", "compact", "compact.supported", "quota", "validate", "prewarm", "recover"}}}
}
func (n *nativeModelProvider) ProvidedServices() []pluginhost.ServiceDescriptor {
	return []pluginhost.ServiceDescriptor{{Name: selectedModelService, Version: "1.0.0", Methods: []pluginhost.ServiceMethodDescriptor{{Name: "describe", InputSchema: "model.adapter.describe.input.v1", OutputSchema: "model.adapter.describe.output.v1"}}}}
}
func (n *nativeModelProvider) RequiredServices() []pluginhost.ServiceRequirement {
	return []pluginhost.ServiceRequirement{{Name: config.ServiceName, MajorVersion: 1, Required: true}, {Name: credentialstore.ServiceName, MajorVersion: 1, Required: true}, {Name: pluginhost.KernelModelReceiptService, MajorVersion: 1, Required: true}, {Name: pluginhost.KernelModelEmitService, MajorVersion: 1, Required: true}}
}
func (n *nativeModelProvider) InvokeService(ctx context.Context, p pluginhost.ServiceInvokeParams) (json.RawMessage, error) {
	if p.Method != "describe" {
		return nil, serviceError("method_not_found", "unknown model adapter method")
	}
	return json.Marshal(n.ModelAdapters()[0])
}
func (n *nativeModelProvider) call(ctx context.Context, service, method string, input any, output any) error {
	raw, err := json.Marshal(input)
	if err != nil {
		return err
	}
	result, callErr := n.registry.Call(ctx, n.ID(), pluginhost.ServiceCallParams{Service: service, Method: method, Params: raw})
	if callErr != nil {
		return callErr
	}
	if output != nil {
		return json.Unmarshal(result, output)
	}
	return nil
}
func (n *nativeModelProvider) client(ctx context.Context, request providers.ChatRequest, configuredModel string) (providers.StreamClient, error) {
	n.clientMu.Lock()
	defer n.clientMu.Unlock()
	if n.Status().State != pluginhost.StateActive {
		return nil, errors.New("native model provider is unavailable")
	}
	cfg, name, err := n.effectiveProvider(request, configuredModel)
	if err != nil {
		return nil, err
	}
	keyBytes, _ := json.Marshal(struct {
		Provider string
		Config   config.ProviderConfig
	}{name, cfg})
	key := string(keyBytes)
	if client := n.clients[key]; client != nil {
		return client, nil
	}
	client, err := n.buildClient(ctx, cfg, name)
	if err != nil {
		return nil, err
	}
	n.clients[key] = client
	return client, nil
}

// Caller holds clientMu; candidate validation never enters the transport cache.
func (n *nativeModelProvider) buildClient(ctx context.Context, cfg config.ProviderConfig, name string) (providers.StreamClient, error) {
	var err error
	credentials := credentialstore.NewClient(func(ctx context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
		result, err := n.registry.Call(ctx, n.ID(), pluginhost.ServiceCallParams{Service: credentialstore.ServiceName, Method: method, Params: raw})
		if err != nil {
			return nil, err
		}
		return result, nil
	})
	if n.manager == nil {
		n.manager, err = siwc.NewBoundManager(n.home, credentials)
		if err != nil {
			return nil, err
		}
	}
	client, err := providerfactory.BuildRuntimeStreamClient(cfg, name, providerfactory.Bindings{Context: ctx, Credentials: credentials, ChatGPT: n.manager})
	if err != nil {
		return nil, err
	}
	return client, nil
}
func (n *nativeModelProvider) InvokeCapability(ctx context.Context, p pluginhost.CapabilityInvokeParams) (pluginhost.CapabilityInvokeResult, error) {
	var input pluginprotocol.ModelAdapterInput
	if err := decodeServiceParams(p.Input, &input); err != nil {
		return pluginhost.CapabilityInvokeResult{}, err
	}
	if input.Operation == "validate" {
		var cfg config.ProviderConfig
		if err := json.Unmarshal(input.Configuration, &cfg); err != nil {
			return pluginhost.CapabilityInvokeResult{}, errors.New("invalid provider configuration")
		}
		_, cfg = modelcatalog.EnrichProvider(input.Request.Provider, cfg, cfg.Model)
		n.clientMu.Lock()
		client, err := n.buildClient(ctx, cfg, input.Request.Provider)
		n.clientMu.Unlock()
		if client != nil {
			if closer, ok := client.(interface{ Close() error }); ok {
				err = errors.Join(err, closer.Close())
			}
		}
		if err != nil {
			return pluginhost.CapabilityInvokeResult{}, err
		}
		raw, err := json.Marshal(pluginprotocol.ModelAdapterOutput{Supported: true})
		return pluginhost.CapabilityInvokeResult{Output: raw}, err
	}
	var request providers.ChatRequest
	var err error
	if input.Operation == "quota" {
		request = providers.ChatRequest{Provider: input.Request.Provider, Model: input.Request.Model}
	} else {
		request, err = modelwire.ApplyRequest(providers.ChatRequest{}, input.Request)
		if err != nil {
			return pluginhost.CapabilityInvokeResult{}, err
		}
	}
	if input.Operation == "compact.supported" {
		cfg, name, err := n.config.ResolveProvider(request.Provider)
		if err != nil {
			return pluginhost.CapabilityInvokeResult{}, err
		}
		model := input.ConfiguredModel
		if model == "" {
			model = request.Model
		}
		cfg.Model = model
		_, cfg = modelcatalog.EnrichProvider(name, cfg, model)
		raw, err := json.Marshal(pluginprotocol.ModelAdapterOutput{Supported: providerfactory.NativeCompactionSupported(cfg)})
		return pluginhost.CapabilityInvokeResult{Output: raw}, err
	}
	client, err := n.client(ctx, request, input.ConfiguredModel)
	if err != nil {
		return pluginhost.CapabilityInvokeResult{}, err
	}
	output := pluginprotocol.ModelAdapterOutput{}
	result := func() (pluginhost.CapabilityInvokeResult, error) {
		raw, err := json.Marshal(output)
		return pluginhost.CapabilityInvokeResult{Output: raw}, err
	}
	switch input.Operation {
	case "quota":
		reader, ok := client.(providers.QuotaReader)
		if !ok {
			output.QuotaError = providers.QuotaErrorUnsupported
			return result()
		}
		quota, err := reader.ReadQuota(ctx)
		output.Supported = true
		if err != nil {
			output.QuotaError = providers.QuotaErrorCode(err)
		}
		output.Quota, _ = json.Marshal(quota)
		return result()

	case "prepare":
		if preparer, ok := client.(providers.InferenceRequestPreparer); ok {
			request, err = preparer.PrepareInferenceRequest(ctx, request)
			if err != nil {
				return pluginhost.CapabilityInvokeResult{}, err
			}
		}
		wire := modelwire.RequestToWire(request)
		output.Request = &wire
		return result()
	case "media":
		policy := providers.ResolveMediaInput(client, request.MediaInput, request.ProviderOptions)
		wire := modelwire.RequestToWire(providers.ChatRequest{MediaInput: policy})
		output.Media = &wire.MediaInput
		return result()
	case "prewarm":
		if warmer, ok := client.(providers.SessionPrewarmer); ok {
			err = warmer.PrewarmSession(ctx, input.SessionID)
		}
		if err != nil {
			return pluginhost.CapabilityInvokeResult{}, err
		}
		return result()
	case "recover":
		recovery, ok := client.(providers.InferenceRecoveryApplier)
		if !ok {
			return pluginhost.CapabilityInvokeResult{}, errors.New("selected model adapter does not support recovery")
		}
		err = recovery.ApplyInferenceRecovery(ctx, providers.RecoveryPlan{Action: providers.RecoveryActionKind(input.RecoveryAction)})
		if err != nil {
			return pluginhost.CapabilityInvokeResult{}, err
		}
		return result()
	case "compact":
		compactor, ok := client.(providers.NativeCompactor)
		if !ok || !compactor.NativeCompactionAvailable() {
			return result()
		}
	}
	if input.Receipt == nil {
		return pluginhost.CapabilityInvokeResult{}, errors.New("model transport requires a physical receipt")
	}
	receipt := input.Receipt
	request.Operation = providers.InferenceOperation{ID: receipt.OperationID, WorkloadProfile: providers.InferenceWorkloadProfile(receipt.Profile)}
	request.Attempt = providers.NewRemoteInferenceAttempt(request.Operation, receipt.AttemptID, receipt.AttemptOrdinal, func(method string, in, out any) error {
		raw, err := json.Marshal(modelReceiptTransportInput(method, in))
		if err != nil {
			return err
		}
		// The owner-bound receipt permits only physical settlement after cancellation.
		receiptBase := context.Background()
		if method == "create" {
			receiptBase = ctx
		}
		receiptCtx, cancel := context.WithTimeout(receiptBase, 3*time.Second)
		defer cancel()
		params := modelReceiptParams{Receipt: receipt.ID, Input: raw}
		switch method {
		case "create":
			var wire pluginprotocol.ModelSubmission
			err := n.call(receiptCtx, pluginhost.KernelModelReceiptService, method, params, &wire)
			if err == nil {
				*out.(*providers.InferenceSubmission) = modelSubmissionFromWire(wire)
			}
			return err
		case "snapshot":
			var wire []pluginprotocol.ModelSubmission
			err := n.call(receiptCtx, pluginhost.KernelModelReceiptService, method, params, &wire)
			if err == nil {
				for _, entry := range wire {
					*out.(*[]providers.InferenceSubmission) = append(*out.(*[]providers.InferenceSubmission), modelSubmissionFromWire(entry))
				}
			}
			return err
		default:
			return n.call(receiptCtx, pluginhost.KernelModelReceiptService, method, params, out)
		}
	})
	if input.Operation == "compact" {
		compacted, err := client.(providers.NativeCompactor).NativeCompact(ctx, request)
		if err != nil {
			return pluginhost.CapabilityInvokeResult{}, err
		}
		output.Supported = true
		for _, message := range compacted.Replacement {
			output.Replacement = append(output.Replacement, modelwire.ChatMessageToWire(message))
		}
		if compacted.Usage != nil {
			usage := modelwire.TokenUsageToWire(*compacted.Usage)
			output.Usage = &usage
		}
		output.ProviderStateScope = compacted.ProviderStateScope
		return result()
	}
	if !input.Stream {
		var response providers.ChatResponse
		// Logical unary calls do not dictate the provider's wire transport.
		// Keep this compatibility decision inside the selected provider.
		if transport, ok := client.(interface{ RequiresStreamingChat() bool }); ok && transport.RequiresStreamingChat() {
			var stream <-chan providers.StreamEvent
			stream, err = client.StreamChat(ctx, request)
			if err == nil {
				response, err = collectNativeModelStream(ctx, stream, request.Model)
			}
		} else {
			response, err = client.Chat(ctx, request)
		}
		if err != nil {
			output.Error = modelErrorView(err)
			return result()
		}
		if err = request.Attempt.JournalError(); err != nil {
			return pluginhost.CapabilityInvokeResult{}, err
		}
		wire := logicalModelResponse(response)
		output.Response = &wire
		return result()
	}
	stream, err := client.StreamChat(ctx, request)
	if err != nil {
		output.Error = modelErrorView(err)
		return result()
	}
	var emitErr error
	for event := range stream {
		if emitErr != nil {
			continue
		}
		wire := logicalModelStreamEvent(event)
		raw, _ := json.Marshal(wire)
		_, callErr := n.registry.Call(ctx, n.ID(), pluginhost.ServiceCallParams{Service: pluginhost.KernelModelEmitService, Method: pluginhost.KernelServiceMethod, Params: raw, ExecutionID: p.ExecutionID})
		if callErr != nil {
			emitErr = callErr
		}
	}
	if err = errors.Join(emitErr, request.Attempt.JournalError()); err != nil {
		return pluginhost.CapabilityInvokeResult{}, err
	}
	return result()
}

func (n *nativeModelProvider) effectiveProvider(request providers.ChatRequest, configuredModel string) (config.ProviderConfig, string, error) {
	cfg, name, err := n.config.ResolveProvider(request.Provider)
	if err != nil && request.Provider == "grok-build" {
		// The built-in CLI-linked account is a named native configuration, not a
		// fallback transport. Its selected credential source still controls access.
		cfg, name, err = config.ApplyGrokBuildProviderDefaults(config.ProviderConfig{Type: "grok-build"}), "grok-build", nil
	}
	if err != nil {
		return config.ProviderConfig{}, name, err
	}
	if configuredModel == "" {
		configuredModel = request.Model
	}
	cfg.Model = configuredModel
	_, cfg = modelcatalog.EnrichProvider(name, cfg, configuredModel)
	return cfg, name, nil
}
