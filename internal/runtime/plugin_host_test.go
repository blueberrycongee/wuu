package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/blueberrycongee/wuu/internal/agent"
	pluginpkg "github.com/blueberrycongee/wuu/internal/plugin"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/providers"
)

type runtimeCapabilityClient struct {
	id       string
	input    pluginhost.RequestTransformInput
	mutate   func(*pluginhost.RequestTransformOutput)
	err      error
	policy   pluginhost.ErrorPolicy
	priority int
	invoked  int
}

func (c *runtimeCapabilityClient) ID() string { return c.id }
func (c *runtimeCapabilityClient) Status() pluginhost.Status {
	return pluginhost.Status{ID: c.id, State: pluginhost.StateActive}
}
func (c *runtimeCapabilityClient) Close(context.Context) error { return nil }
func (c *runtimeCapabilityClient) ProtocolVersion() int        { return pluginhost.CapabilityProtocolVersion }
func (c *runtimeCapabilityClient) Capabilities() []pluginhost.CapabilityDescriptor {
	return []pluginhost.CapabilityDescriptor{{
		ID: pluginhost.CapabilityAgentRequestTransform, Kind: "transform", ErrorPolicy: c.policy, Version: 1, Priority: c.priority,
	}}
}
func (c *runtimeCapabilityClient) InvokeCapability(_ context.Context, params pluginhost.CapabilityInvokeParams) (pluginhost.CapabilityInvokeResult, error) {
	c.invoked++
	if c.err != nil {
		return pluginhost.CapabilityInvokeResult{}, c.err
	}
	if err := json.Unmarshal(params.Input, &c.input); err != nil {
		return pluginhost.CapabilityInvokeResult{}, err
	}
	var output pluginhost.RequestTransformOutput
	if err := json.Unmarshal(params.Output, &output); err != nil {
		return pluginhost.CapabilityInvokeResult{}, err
	}
	if c.mutate != nil {
		c.mutate(&output)
	}
	data, err := json.Marshal(output)
	return pluginhost.CapabilityInvokeResult{Output: data}, err
}

type runtimeTestStep struct{ called bool }

func (s *runtimeTestStep) Execute(context.Context, providers.ChatRequest) (agent.StepResult, error) {
	s.called = true
	return agent.StepResult{Content: "done"}, nil
}

type activationOrderClient struct {
	kernel *kernelHostServices
	state  pluginhost.State
}

func (c *activationOrderClient) ID() string { return "activation-order" }
func (c *activationOrderClient) Status() pluginhost.Status {
	return pluginhost.Status{ID: c.ID(), State: c.state}
}
func (c *activationOrderClient) Close(context.Context) error { return nil }
func (c *activationOrderClient) Activate(context.Context) error {
	if c.kernel.Status().State != pluginhost.StateActive {
		return errors.New("kernel services were not active")
	}
	c.state = pluginhost.StateActive
	return nil
}

func TestActivatePluginHostOpensKernelServicesBeforePluginCallbacks(t *testing.T) {
	kernel := newKernelHostServices(nil, nil)
	registry, conflicts := pluginhost.BuildServiceRegistry(kernel)
	client := &activationOrderClient{kernel: kernel, state: pluginhost.StatePrepared}
	host := pluginhost.New(client)
	host.AttachServiceRegistry(registry, conflicts)

	if err := activatePluginHost(context.Background(), host); err != nil {
		t.Fatal(err)
	}
	if client.state != pluginhost.StateActive {
		t.Fatalf("client state = %q", client.state)
	}
}

func TestPluginCapabilityUsesRequestTransformRegistry(t *testing.T) {
	client := &runtimeCapabilityClient{id: "capability", priority: 7, mutate: func(output *pluginhost.RequestTransformOutput) {
		output.PrependSystemMessages = []string{"capability context"}
	}}
	intercept := pluginRequestInterceptor(pluginhost.New(client), "openai", "thread-2", "/workspace")
	request := providers.ChatRequest{Model: "before", StepIndex: 3, Messages: []providers.ChatMessage{{Role: "user", Content: "hello"}}}
	if err := intercept(context.Background(), &request); err != nil {
		t.Fatal(err)
	}
	if len(request.Messages) != 2 || request.Messages[0].Content != "capability context" || client.invoked != 1 {
		t.Fatalf("request=%+v invoked=%d", request, client.invoked)
	}
	if client.input.ThreadID != "thread-2" || client.input.Provider != "openai" || client.input.StepIndex != 3 || client.input.Request.Model != "before" || len(client.input.Request.Messages) != 1 {
		t.Fatalf("input = %+v", client.input)
	}
}

func TestPluginRequestTransformErrorPolicy(t *testing.T) {
	for _, policy := range []pluginhost.ErrorPolicy{pluginhost.ErrorPolicyPropagate, pluginhost.ErrorPolicyIsolate} {
		t.Run(string(policy), func(t *testing.T) {
			broken := &runtimeCapabilityClient{id: "broken", priority: 10, policy: policy, err: errors.New("transform boom")}
			next := &runtimeCapabilityClient{id: "next", priority: 5, mutate: func(output *pluginhost.RequestTransformOutput) {
				output.PrependSystemMessages = []string{"next context"}
			}}
			host := pluginhost.New(broken, next)
			intercept := pluginRequestInterceptor(host, "openai", "thread", "/workspace")
			request := providers.ChatRequest{Model: "original", Messages: []providers.ChatMessage{{Role: "user", Content: "hello"}}}
			err := intercept(context.Background(), &request)
			if policy == pluginhost.ErrorPolicyPropagate {
				if err == nil || !strings.Contains(err.Error(), "transform boom") {
					t.Fatalf("propagate error = %v", err)
				}
				if next.invoked != 0 {
					t.Fatalf("next transform invoked %d times after propagated failure", next.invoked)
				}
				return
			}
			if err != nil {
				t.Fatalf("isolated transform failed the request: %v", err)
			}
			if next.invoked != 1 || len(request.Messages) != 2 || request.Messages[0].Content != "next context" {
				t.Fatalf("isolated chain did not continue: invoked=%d request=%+v", next.invoked, request)
			}
			diagnostics := host.ContributionDiagnostics("broken")
			if len(diagnostics) != 1 || diagnostics[0].Contribution != pluginhost.CapabilityAgentRequestTransform || !strings.Contains(diagnostics[0].Message, "transform boom") {
				t.Fatalf("isolated diagnostics = %+v", diagnostics)
			}
		})
	}
}

func TestPluginCapabilityRejectsInvalidRequestPatch(t *testing.T) {
	client := &runtimeCapabilityClient{id: "unsafe", priority: 7, mutate: func(output *pluginhost.RequestTransformOutput) {
		output.PrependSystemMessages = []string{""}
	}}
	step := &runtimeTestStep{}
	history := []providers.ChatMessage{
		{Role: "user", Content: "run"},
		{Role: "assistant", ToolCalls: []providers.ToolCall{{ID: "call-1", Name: "read"}}},
		{Role: "tool", ToolCallID: "call-1", Name: "read", Content: "ok"},
	}
	_, err := agent.RunToolLoop(context.Background(), history, agent.LoopConfig{
		Model: "model", MaxSteps: 1,
		BeforeRequest: pluginRequestInterceptor(pluginhost.New(client), "openai", "thread-3", "/workspace"),
	}, step)
	if err == nil || !strings.Contains(err.Error(), "empty message") {
		t.Fatalf("error = %v", err)
	}
	if step.called {
		t.Fatal("provider step was called with an invalid request patch")
	}
}

func TestStartPluginHostPreservesRuntimeFailure(t *testing.T) {
	host, _ := startPluginHost([]pluginpkg.Plugin{{
		Manifest: pluginpkg.Manifest{ID: "broken", Runtime: &pluginpkg.RuntimeSpec{
			Protocol: pluginhost.ProtocolName,
			Command:  "/definitely/not/a/wuu-plugin",
		}},
		Root: t.TempDir(),
	}}, t.TempDir(), "workspace-one", t.TempDir(), t.TempDir(), nil, nil)
	statuses := host.Statuses()
	if len(statuses) != 1 || statuses[0].State != pluginhost.StateFailed || !strings.Contains(statuses[0].Error, "start plugin") {
		t.Fatalf("statuses = %+v", statuses)
	}
}
