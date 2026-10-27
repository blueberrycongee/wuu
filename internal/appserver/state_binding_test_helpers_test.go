package appserver

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/blueberrycongee/wuu/internal/access"
	"github.com/blueberrycongee/wuu/internal/agentengine"
	"github.com/blueberrycongee/wuu/internal/compactionservice"
	"github.com/blueberrycongee/wuu/internal/modelwire"
	"github.com/blueberrycongee/wuu/internal/persistence"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/pluginprotocol"
	"github.com/blueberrycongee/wuu/internal/principal"
	"github.com/blueberrycongee/wuu/internal/promptservice"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/runtime"
	"github.com/blueberrycongee/wuu/internal/session"
	"github.com/blueberrycongee/wuu/internal/titleservice"
	"github.com/blueberrycongee/wuu/internal/toolservice"
)

// Direct handler fixtures do not boot a session runtime. Select their trusted
// embedding identity and owner-only policy explicitly instead of relying on a
// zero-value Server to bypass the outbound resource boundary.
func newOwnerHandlerFixture(rt *runtime.Session, out io.Writer) *Server {
	return &Server{rt: rt, out: out, access: localOwnerTestAccess()}
}

func localOwnerTestAccess() connectionAccess {
	return connectionAccess{principal: principal.LocalOwner(), policy: access.OwnerOnly{}}
}

// Embedded model fixtures still need a captured generation for receipt ownership.
func attachTestModelGeneration(t *testing.T, rt *runtime.Session, additional ...pluginhost.Client) {
	t.Helper()
	// Capture the synthetic transport before publishing the declared graph; all
	// inference participates in selected model, capacity and context services.
	model := &embeddedModelStreamFixture{}
	if rt.StreamRunner != nil {
		model.client = rt.StreamRunner.Client
	}
	var toolProvider pluginhost.Client = toolservice.NewService()
	if rt.Toolkit == nil && rt.StreamRunner != nil && rt.StreamRunner.Tools != nil {
		executor := rt.StreamRunner.Tools
		toolProvider = &embeddedToolFixture{Service: toolservice.NewService(), scope: toolservice.NewScope(func(context.Context) (toolservice.Backend, error) {
			return toolservice.Backend{Executor: executor}, nil
		})}
	}
	engineConsumer := &embeddedEngineConsumer{}
	for _, client := range additional {
		if provider, ok := client.(interface {
			ProvidedServices() []pluginhost.ServiceDescriptor
		}); ok {
			for _, service := range provider.ProvidedServices() {
				if strings.HasPrefix(service.Name, "engine.") {
					engineConsumer.extra = append(engineConsumer.extra, pluginhost.ServiceRequirement{Name: service.Name, MajorVersion: 1, Required: true})
				}
			}
		}
	}
	clients := []pluginhost.Client{access.NewService(access.NativeModule, access.OwnerOnly{}), &embeddedAccessConsumer{}, promptservice.NewService(), titleservice.NewService(), &embeddedCompactionFixture{compactionservice.NewService()}, &embeddedModelConsumer{}, model, &embeddedModelConfigurationConsumer{}, &embeddedCapacityFixture{leases: map[string]string{}}, toolProvider, &embeddedToolsConsumer{}, engineConsumer, &embeddedEngineMetadata{factory: rt.WuuEngine()}}
	clients = append(clients, additional...)
	// User-host fixtures may bind their user space after runtime construction.
	// Select their review ledger through the same declared consumer as production.
	hasReview, hasDocuments := false, false
	for _, client := range clients {
		hasReview = hasReview || client.ID() == personalActionsConsumerID
		if provider, ok := client.(interface {
			ProvidedServices() []pluginhost.ServiceDescriptor
		}); ok {
			for _, service := range provider.ProvidedServices() {
				hasDocuments = hasDocuments || service.Name == persistence.ServiceName
			}
		}
	}
	if !hasReview {
		clients = append(clients, NewPersonalActionsConsumer())
	}
	if !hasDocuments {
		documentDir := filepath.Join(rt.SessionDir, "documents")
		if strings.TrimSpace(rt.SessionDir) == "" {
			documentDir = t.TempDir()
		}
		clients = append(clients, persistence.NewService("fixture.documents", documentDir, "sqlite"))
	}
	registry, conflicts := pluginhost.BuildServiceRegistry(clients...)
	if len(conflicts) != 0 {
		t.Fatal(conflicts)
	}
	rt.PluginHost = pluginhost.New(clients...)
	model.host = rt.PluginHost
	rt.PluginHost.AttachServiceRegistry(registry, conflicts)
	if err := rt.PluginHost.Activate(context.Background()); err != nil {
		t.Fatal(err)
	}
	host := rt.PluginHost
	t.Cleanup(func() {
		if _, err := rt.Cleanup(); err != nil {
			t.Error(err)
		}
		if err := host.Close(context.Background()); err != nil {
			t.Error(err)
		}
	})
}

func attachTestHookGeneration(t *testing.T, rt *runtime.Session) {
	t.Helper()
	journal, err := rt.Sessions.NewInferenceJournalRuntime("appserver-hook-test")
	if err != nil {
		t.Fatal(err)
	}
	rt.InferenceJournalRuntime = journal
	t.Cleanup(func() { _ = journal.Close() })
	attachTestModelGeneration(t, rt)
}

func testRuntimeState(t *testing.T, rt *runtime.Session) *runtime.State {
	t.Helper()
	binding, err := rt.RetainState()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := binding.Close(); err != nil {
			t.Error(err)
		}
	})
	return binding
}

func newAppserverTestStore(t *testing.T, dir string) *session.Store {
	t.Helper()
	documents, err := persistence.Open(context.Background(), dir, "sqlite")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := documents.Close(); err != nil {
			t.Error(err)
		}
	})
	return session.NewStore(context.Background(), documents)
}

// Model transport comes from the embedded runner, rather than a host gateway.
type embeddedCompactionFixture struct{ *compactionservice.Service }

func (*embeddedCompactionFixture) RequiredServices() []pluginhost.ServiceRequirement { return nil }

type embeddedModelConsumer struct{ active atomic.Bool }

func (*embeddedModelConsumer) ID() string { return "wuu.session-execution" }
func (c *embeddedModelConsumer) Status() pluginhost.Status {
	state := pluginhost.StatePrepared
	if c.active.Load() {
		state = pluginhost.StateActive
	}
	return pluginhost.Status{ID: c.ID(), State: state}
}
func (*embeddedModelConsumer) ProvidedServices() []pluginhost.ServiceDescriptor { return nil }
func (*embeddedModelConsumer) RequiredServices() []pluginhost.ServiceRequirement {
	return []pluginhost.ServiceRequirement{
		{Name: promptservice.ServiceName, MajorVersion: 1, Required: true}, {Name: compactionservice.ServiceName, MajorVersion: 1, Required: true}, {Name: titleservice.ServiceName, MajorVersion: 1, Required: true},
	}
}
func (c *embeddedModelConsumer) Activate(context.Context) error { c.active.Store(true); return nil }
func (c *embeddedModelConsumer) Close(context.Context) error    { c.active.Store(false); return nil }

type embeddedToolFixture struct {
	*toolservice.Service
	scope *toolservice.Scope
}

func (c *embeddedToolFixture) InvokeService(ctx context.Context, p pluginhost.ServiceInvokeParams) (json.RawMessage, error) {
	return c.Service.InvokeService(toolservice.WithScope(ctx, c.scope), p)
}

// These fixtures provide their own toolkit and permission authority.
type embeddedToolsConsumer struct{ embeddedModelConsumer }

func (*embeddedToolsConsumer) ID() string { return "wuu.tool-execution" }
func (c *embeddedToolsConsumer) Status() pluginhost.Status {
	status := c.embeddedModelConsumer.Status()
	status.ID = c.ID()
	return status
}
func (*embeddedToolsConsumer) RequiredServices() []pluginhost.ServiceRequirement {
	return []pluginhost.ServiceRequirement{{Name: toolservice.ServiceName, MajorVersion: 1, Required: true}, {Name: pluginhost.SecurityAuthorizeService, MajorVersion: 1}}
}

// Configuration UI fixtures use the public adapter validation contract. Native
// transport/credential validation is exercised by runtime provider tests; these
// fixtures must never initialize a real account or submit model traffic.
type embeddedModelConfigurationFixture struct{ embeddedModelConsumer }

func (*embeddedModelConfigurationFixture) ID() string { return "fixture.model-configuration" }
func (c *embeddedModelConfigurationFixture) Status() pluginhost.Status {
	status := c.embeddedModelConsumer.Status()
	status.ID = c.ID()
	return status
}
func (*embeddedModelConfigurationFixture) RequiredServices() []pluginhost.ServiceRequirement {
	return nil
}
func (*embeddedModelConfigurationFixture) ProvidedServices() []pluginhost.ServiceDescriptor {
	return []pluginhost.ServiceDescriptor{{Name: "model.adapter", Version: "1.0.0", Methods: []pluginhost.ServiceMethodDescriptor{{Name: "describe", InputSchema: "model.adapter.describe.input.v1", OutputSchema: "model.adapter.describe.output.v1"}}}}
}
func (*embeddedModelConfigurationFixture) ProtocolVersion() int { return 2 }
func (*embeddedModelConfigurationFixture) Capabilities() []pluginhost.CapabilityDescriptor {
	return []pluginhost.CapabilityDescriptor{{ID: pluginhost.CapabilityModelAdapter, Version: 1, ErrorPolicy: pluginhost.ErrorPolicyPropagate}}
}
func (*embeddedModelConfigurationFixture) ModelAdapters() []pluginhost.ModelAdapterDescriptor {
	return []pluginhost.ModelAdapterDescriptor{{ID: "fixture.configuration", Version: 1, Mode: "direct", Operations: []string{"validate"}}}
}
func (c *embeddedModelConfigurationFixture) InvokeService(context.Context, pluginhost.ServiceInvokeParams) (json.RawMessage, error) {
	return json.Marshal(c.ModelAdapters()[0])
}
func (*embeddedModelConfigurationFixture) InvokeCapability(_ context.Context, p pluginhost.CapabilityInvokeParams) (pluginhost.CapabilityInvokeResult, error) {
	var in pluginprotocol.ModelAdapterInput
	if err := json.Unmarshal(p.Input, &in); err != nil {
		return pluginhost.CapabilityInvokeResult{}, err
	}
	if in.Operation != "validate" {
		return pluginhost.CapabilityInvokeResult{}, errors.New("configuration fixture cannot submit inference")
	}
	raw, err := json.Marshal(pluginprotocol.ModelAdapterOutput{Supported: true})
	return pluginhost.CapabilityInvokeResult{Output: raw}, err
}

type embeddedModelConfigurationConsumer struct{ embeddedModelConsumer }

func (*embeddedModelConfigurationConsumer) ID() string { return "wuu.model-execution" }
func (c *embeddedModelConfigurationConsumer) Status() pluginhost.Status {
	status := c.embeddedModelConsumer.Status()
	status.ID = c.ID()
	return status
}
func (*embeddedModelConfigurationConsumer) RequiredServices() []pluginhost.ServiceRequirement {
	return []pluginhost.ServiceRequirement{{Name: "model.adapter", MajorVersion: 1, Required: true}, {Name: "model.capacity", MajorVersion: 1, Required: true}, {Name: "host.model.capacity", MajorVersion: 1, Required: true}}
}

// Embedded runtimes retain their explicitly supplied engine; metadata calls use
// the same declared service boundary as production instead of an absent graph.
type embeddedEngineConsumer struct {
	embeddedModelConsumer
	extra []pluginhost.ServiceRequirement
}

func (*embeddedEngineConsumer) ID() string { return "wuu.engine-execution" }
func (c *embeddedEngineConsumer) Status() pluginhost.Status {
	s := c.embeddedModelConsumer.Status()
	s.ID = c.ID()
	return s
}
func (c *embeddedEngineConsumer) RequiredServices() []pluginhost.ServiceRequirement {
	return append([]pluginhost.ServiceRequirement{{Name: "engine.wuu", MajorVersion: 1, Required: true}}, c.extra...)
}

type embeddedEngineMetadata struct {
	embeddedModelConsumer
	factory agentengine.Factory
}

func (*embeddedEngineMetadata) ID() string { return "fixture.engine-metadata" }
func (c *embeddedEngineMetadata) Status() pluginhost.Status {
	s := c.embeddedModelConsumer.Status()
	s.ID = c.ID()
	return s
}
func (*embeddedEngineMetadata) RequiredServices() []pluginhost.ServiceRequirement { return nil }
func (*embeddedEngineMetadata) ProvidedServices() []pluginhost.ServiceDescriptor {
	return []pluginhost.ServiceDescriptor{{Name: "engine.wuu", Version: "1.0.0", Methods: []pluginhost.ServiceMethodDescriptor{{Name: "descriptor", InputSchema: "engine.descriptor.input.v1", OutputSchema: "engine.descriptor.output.v1"}, {Name: "history_fork", InputSchema: "engine.history-fork.input.v1", OutputSchema: "engine.history-fork.output.v1"}}}}
}
func (c *embeddedEngineMetadata) InvokeService(ctx context.Context, p pluginhost.ServiceInvokeParams) (json.RawMessage, error) {
	if p.Method == "history_fork" {
		return json.Marshal(pluginprotocol.EngineHistoryForkResult{})
	}
	if p.Method != "descriptor" || c.factory == nil {
		return nil, errors.New("embedded engine metadata unavailable")
	}
	d, err := c.factory.Descriptor(ctx)
	if err != nil {
		return nil, err
	}
	return json.Marshal(struct {
		ID           agentengine.EngineID `json:"id"`
		Version      string               `json:"version"`
		Capabilities []string             `json:"capabilities"`
		Enabled      bool                 `json:"enabled"`
	}{d.ID, d.Version, d.Capabilities, true})
}

// Purely synthetic model transport participates in public admission and event
// delivery. It cannot fall through to a provider endpoint.
type embeddedModelStreamFixture struct {
	embeddedModelConfigurationFixture
	client providers.StreamClient
	host   *pluginhost.Host
}

func (c *embeddedModelStreamFixture) InvokeCapability(ctx context.Context, p pluginhost.CapabilityInvokeParams) (pluginhost.CapabilityInvokeResult, error) {
	var in pluginprotocol.ModelAdapterInput
	if err := json.Unmarshal(p.Input, &in); err != nil {
		return pluginhost.CapabilityInvokeResult{}, err
	}
	if in.Operation == "validate" {
		return c.embeddedModelConfigurationFixture.InvokeCapability(ctx, p)
	}
	req, err := modelwire.ApplyRequest(providers.ChatRequest{}, in.Request)
	if err != nil {
		return pluginhost.CapabilityInvokeResult{}, err
	}
	if c.client == nil {
		return pluginhost.CapabilityInvokeResult{}, errors.New("fixture model is unavailable")
	}
	if !in.Stream {
		response, err := c.client.Chat(ctx, req)
		if err != nil {
			return pluginhost.CapabilityInvokeResult{}, err
		}
		wire := modelwire.ChatResponseToWire(response)
		raw, err := json.Marshal(pluginprotocol.ModelAdapterOutput{Response: &wire})
		return pluginhost.CapabilityInvokeResult{Output: raw}, err
	}
	stream, err := c.client.StreamChat(ctx, req)
	if err != nil {
		return pluginhost.CapabilityInvokeResult{}, err
	}
	for event := range stream {
		if err := c.host.EmitModelEvent(ctx, c.ID(), p.ExecutionID, embeddedModelEvent(event)); err != nil {
			return pluginhost.CapabilityInvokeResult{}, err
		}
	}
	return pluginhost.CapabilityInvokeResult{Output: json.RawMessage(`{}`)}, nil
}

type embeddedCapacityFixture struct {
	embeddedModelConsumer
	mu     sync.Mutex
	leases map[string]string
}

func (*embeddedCapacityFixture) ID() string { return "fixture.model-capacity" }
func (c *embeddedCapacityFixture) Status() pluginhost.Status {
	s := c.embeddedModelConsumer.Status()
	s.ID = c.ID()
	return s
}
func (*embeddedCapacityFixture) RequiredServices() []pluginhost.ServiceRequirement { return nil }
func (*embeddedCapacityFixture) ProvidedServices() []pluginhost.ServiceDescriptor {
	var out []pluginhost.ServiceDescriptor
	for _, name := range []string{"model.capacity", "host.model.capacity"} {
		out = append(out, pluginhost.ServiceDescriptor{Name: name, Version: "1.0.0", Methods: []pluginhost.ServiceMethodDescriptor{{Name: "acquire", InputSchema: "model.capacity.acquire.input.v1", OutputSchema: "model.capacity.acquire.output.v1"}, {Name: "settle", InputSchema: "model.capacity.settle.input.v1", OutputSchema: "model.capacity.settle.output.v1", Cleanup: true}}})
	}
	return out
}
func (c *embeddedCapacityFixture) InvokeService(ctx context.Context, p pluginhost.ServiceInvokeParams) (json.RawMessage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	switch p.Method {
	case "acquire":
		var in pluginprotocol.ModelCapacityAcquire
		if err := json.Unmarshal(p.Params, &in); err != nil {
			return nil, err
		}
		if in.Scope == "" {
			return nil, errors.New("missing capacity scope")
		}
		id := rand.Text()
		c.leases[id] = p.Caller + ":" + p.Service
		return json.Marshal(pluginprotocol.ModelCapacityLease{Lease: id})
	case "settle":
		var in pluginprotocol.ModelCapacitySettle
		if err := json.Unmarshal(p.Params, &in); err != nil {
			return nil, err
		}
		if c.leases[in.Lease] != p.Caller+":"+p.Service {
			return nil, errors.New("invalid fixture lease")
		}
		delete(c.leases, in.Lease)
		return json.RawMessage(`{}`), nil
	default:
		return nil, fmt.Errorf("unexpected capacity method %s", p.Method)
	}
}
func (c *embeddedCapacityFixture) Close(context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.leases) != 0 {
		return fmt.Errorf("fixture capacity leaked %d leases", len(c.leases))
	}
	return nil
}

func embeddedModelEvent(in providers.StreamEvent) pluginhost.ModelStreamEventV1 {
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
		failure := providers.NormalizeFailure(in.Error)
		out.Error = &pluginprotocol.ModelErrorV1{Code: "inference_failed", Message: in.Error.Error(), Failure: &pluginprotocol.ModelFailureV1{Origin: string(failure.Origin), Category: string(failure.Category), ProviderFamily: failure.ProviderFamily, ProviderCode: failure.ProviderCode, HTTPStatus: failure.HTTPStatus, RetryAfterMS: failure.RetryAfter.Milliseconds(), ContextOverflow: failure.ContextOverflow, AuthRefreshable: failure.AuthRefreshable, Confidence: string(failure.ClassificationConfidence), SuggestedRecovery: string(failure.SuggestedRecovery)}}
	}
	return out
}

func TestEmbeddedModelFixtureCompletesNonStreamingRequests(t *testing.T) {
	for _, operation := range []string{"", "complete"} {
		t.Run("operation="+operation, func(t *testing.T) {
			client := &fakeClient{response: providers.ChatResponse{Content: "complete response"}}
			fixture := &embeddedModelStreamFixture{client: providers.AdaptStreamClient(client), host: pluginhost.New()}
			input, err := json.Marshal(pluginprotocol.ModelAdapterInput{Operation: operation, Stream: false, Request: modelwire.RequestToWire(providers.ChatRequest{Model: "fixture-model", Messages: []providers.ChatMessage{{Role: "user", Content: "complete"}}})})
			if err != nil {
				t.Fatal(err)
			}
			result, err := fixture.InvokeCapability(context.Background(), pluginhost.CapabilityInvokeParams{Input: input})
			if err != nil {
				t.Fatal(err)
			}
			var output pluginprotocol.ModelAdapterOutput
			if err := json.Unmarshal(result.Output, &output); err != nil {
				t.Fatal(err)
			}
			if output.Response == nil || output.Response.Content != "complete response" {
				t.Fatalf("non-streaming model response = %+v", output.Response)
			}
			if len(client.requests) != 1 {
				t.Fatalf("model calls=%d, want 1", len(client.requests))
			}
		})
	}
}

type embeddedAccessConsumer struct{ embeddedModelConsumer }

func (*embeddedAccessConsumer) ID() string { return access.ConsumerModule }
func (c *embeddedAccessConsumer) Status() pluginhost.Status {
	status := c.embeddedModelConsumer.Status()
	status.ID = c.ID()
	return status
}
func (*embeddedAccessConsumer) RequiredServices() []pluginhost.ServiceRequirement {
	return []pluginhost.ServiceRequirement{{Name: access.ServiceName, MajorVersion: 1, Required: true}}
}
