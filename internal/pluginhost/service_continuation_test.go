package pluginhost

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/blueberrycongee/wuu/internal/composition"
)

// Contract failures: ordinary calls or descendants borrow authority; a token
// crosses owners/generations, retries effects, survives its callback, or switches
// to a replacement. Cancellation and explicit revocation must still fail closed.
func continuationFixture(t *testing.T, rendererOnly ...bool) (*Host, *ServiceRegistry, *fakeServiceProcess, *fakeServiceProcess) {
	t.Helper()
	descriptor := ServiceDescriptor{Name: "agent.tools", Version: "1.0.0", Methods: []ServiceMethodDescriptor{{Name: "execute", InputSchema: "fixture.execute.input.v1", OutputSchema: "fixture.execute.output.v1"}}}
	required := []ServiceRequirement{{Name: descriptor.Name, MajorVersion: 1, Required: true}}
	wrapper := &fakeServiceProcess{id: "wrapper", state: StateActive, provided: []ServiceDescriptor{descriptor}, required: required}
	base := &fakeServiceProcess{id: "base", state: StateActive, provided: []ServiceDescriptor{descriptor}}
	caller := &fakeServiceProcess{id: "caller", state: StateActive, required: required}
	registry, err := NewServiceRegistryWithBindings([]composition.Binding{{Service: descriptor.Name, Major: 1, Provider: wrapper.ID()}, {Consumer: wrapper.ID(), Service: descriptor.Name, Major: 1, Provider: base.ID()}})
	if err != nil {
		t.Fatal(err)
	}
	var wrapperClient ServiceClient = wrapper
	if len(rendererOnly) != 0 && rendererOnly[0] {
		wrapperClient = &rendererOnlyChainClient{wrapper}
	}
	if conflicts := registry.RegisterClients(wrapperClient, base, caller); len(conflicts) != 0 {
		t.Fatal(conflicts)
	}
	host := New(wrapperClient, base, caller)
	host.AttachServiceRegistry(registry, nil)
	registry.Activate()
	t.Cleanup(func() { _ = host.Close(context.Background()) })
	return host, registry, wrapper, base
}
func continuationInput() ToolExecuteInput {
	return ToolExecuteInput{SessionID: "session", ThreadID: "thread", TurnID: "turn", ActorID: "actor", CallID: "call", Tool: "process", CWD: "/workspace", Arguments: json.RawMessage(`{"command":"original"}`)}
}
func TestSelectedServiceContinuationAuthority(t *testing.T) {
	host, registry, wrapper, base := continuationFixture(t)
	var saved ServiceContinueParams
	var completedContext context.Context
	var completedParams ServiceInvokeParams
	var calls atomic.Int32
	input := continuationInput()
	raw := json.RawMessage(`{"call":{"id":"call","name":"process","arguments":"original"}}`)
	original := string(raw)
	base.invoke = func(ctx context.Context, p ServiceInvokeParams) (json.RawMessage, error) {
		calls.Add(1)
		if _, err := p.Execution(ctx, wrapper.ID(), true); err == nil {
			t.Fatal("current callback accepted another provider identity")
		}
		completedContext, completedParams = ctx, p
		scope, err := p.Execution(ctx, base.ID(), true)
		if err != nil {
			t.Fatal(err)
		}
		if scope.ThreadID != input.ThreadID || scope.TurnID != input.TurnID || scope.ActorID != input.ActorID || scope.CallID != input.CallID || scope.CWD != input.CWD || scope.Tool != input.Tool || string(p.Params) != original {
			t.Fatalf("changed authority/request: %+v %+v", scope, p)
		}
		if _, err := registry.Continue(ctx, base.ID(), ServiceContinueParams{ExecutionID: p.ExecutionID, ContinuationID: saved.ContinuationID}); err == nil {
			t.Fatal("descendant borrowed continuation")
		}
		return json.RawMessage(`{"ok":true}`), nil
	}
	wrapper.invoke = func(ctx context.Context, p ServiceInvokeParams) (json.RawMessage, error) {
		if p.ContinuationID == "" {
			t.Fatal("selected invocation has no continuation")
		}
		saved = ServiceContinueParams{ExecutionID: p.ExecutionID, ContinuationID: p.ContinuationID}
		p.Params[0] = '[' // The host snapshot must not alias provider-visible request bytes.
		defer func() { p.Params[0] = '{' }()
		foreignHost, foreign, _, _ := continuationFixture(t)
		_ = foreignHost
		if _, err := foreign.Continue(ctx, wrapper.ID(), saved); err == nil {
			t.Fatal("foreign generation accepted continuation")
		}
		if _, err := registry.Continue(ctx, "base", saved); err == nil {
			t.Fatal("foreign owner accepted continuation")
		}
		result, err := registry.Continue(ctx, wrapper.ID(), saved)
		if err != nil {
			return nil, err
		}
		if _, err := registry.Continue(ctx, wrapper.ID(), saved); err == nil || err.Code != "continuation_used" {
			t.Fatalf("duplicate continuation: %v", err)
		}
		return result, nil
	}
	if _, err := host.CallToolService(context.Background(), "caller", "agent.tools", "execute", raw, input); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatalf("body calls=%d", calls.Load())
	}
	if _, err := completedParams.Execution(completedContext, base.ID(), true); err == nil {
		t.Fatal("completed native callback retained tool scope")
	}
	if _, err := registry.Continue(context.Background(), wrapper.ID(), saved); err == nil {
		t.Fatal("late continuation accepted")
	}
	wrapper.invoke = func(ctx context.Context, p ServiceInvokeParams) (json.RawMessage, error) {
		if p.ContinuationID != "" {
			t.Fatal("ordinary call minted continuation")
		}
		if _, err := p.Execution(ctx, wrapper.ID(), true); err == nil {
			t.Fatal("ordinary callback retained native tool scope")
		}
		if _, err := registry.Continue(ctx, wrapper.ID(), ServiceContinueParams{ExecutionID: p.ExecutionID, ContinuationID: saved.ContinuationID}); err == nil {
			t.Fatal("ordinary callback borrowed continuation")
		}
		return json.RawMessage(`{}`), nil
	}
	if _, err := host.CallService(context.Background(), "caller", "agent.tools", "execute", raw); err != nil {
		t.Fatal(err)
	}
}
func TestSelectedServiceContinuationSingleUseOnFailure(t *testing.T) {
	host, registry, wrapper, base := continuationFixture(t)
	var calls atomic.Int32
	base.invoke = func(context.Context, ServiceInvokeParams) (json.RawMessage, error) {
		calls.Add(1)
		return nil, errors.New("provider failure")
	}
	wrapper.invoke = func(ctx context.Context, p ServiceInvokeParams) (json.RawMessage, error) {
		results := make(chan *HostServiceError, 2)
		var start sync.WaitGroup
		start.Add(1)
		for range 2 {
			go func() {
				start.Wait()
				_, err := registry.Continue(ctx, wrapper.ID(), ServiceContinueParams{ExecutionID: p.ExecutionID, ContinuationID: p.ContinuationID})
				results <- err
			}()
		}
		start.Done()
		used, failed := 0, 0
		for range 2 {
			err := <-results
			if err == nil {
				t.Fatal("failed delegate succeeded")
			}
			if err.Code == "continuation_used" {
				used++
			} else {
				failed++
			}
		}
		if used != 1 || failed != 1 {
			t.Fatalf("used=%d failed=%d", used, failed)
		}
		return json.RawMessage(`{}`), nil
	}
	if _, err := host.CallToolService(context.Background(), "caller", "agent.tools", "execute", json.RawMessage(`{}`), continuationInput()); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatalf("failure retried body %d times", calls.Load())
	}
}
func TestSelectedServiceContinuationRetirementAndRevocation(t *testing.T) {
	for _, mode := range []string{"generation-replaced", "cancelled", "delegate-removed"} {
		t.Run(mode, func(t *testing.T) {
			host, registry, wrapper, base := continuationFixture(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			wrapper.invoke = func(callback context.Context, p ServiceInvokeParams) (json.RawMessage, error) {
				switch mode {
				case "generation-replaced":
					host.StopBackground()
				case "cancelled":
					cancel()
				case "delegate-removed":
					registry.RevokePlugin(context.Background(), base.ID())
				}
				result, err := registry.Continue(context.Background(), wrapper.ID(), ServiceContinueParams{ExecutionID: p.ExecutionID, ContinuationID: p.ContinuationID})
				if mode == "generation-replaced" {
					if err != nil {
						t.Fatalf("admitted continuation lost pinned generation: %v", err)
					}
				} else if err == nil {
					t.Fatal("cancelled/revoked continuation executed")
				}
				if err != nil {
					return nil, err
				}
				return result, nil
			}
			_, err := host.CallToolService(ctx, "caller", "agent.tools", "execute", json.RawMessage(`{}`), continuationInput())
			if mode == "generation-replaced" {
				if err != nil || len(base.invokeCalls) != 1 {
					t.Fatalf("retained dispatch calls=%d err=%v", len(base.invokeCalls), err)
				}
			} else if err == nil || len(base.invokeCalls) != 0 {
				t.Fatalf("revoked body calls=%d err=%v", len(base.invokeCalls), err)
			}
		})
	}
}

func TestSelectedServiceContinuationRequiresRuntimeDependency(t *testing.T) {
	host, _, wrapper, _ := continuationFixture(t, true)
	wrapper.invoke = func(_ context.Context, p ServiceInvokeParams) (json.RawMessage, error) {
		if p.ContinuationID != "" {
			t.Fatal("renderer-only dependency minted backend continuation")
		}
		return json.RawMessage(`{}`), nil
	}
	if _, err := host.CallToolService(context.Background(), "caller", "agent.tools", "execute", json.RawMessage(`{}`), continuationInput()); err != nil {
		t.Fatal(err)
	}
}

func TestSelectedServiceContinuationFeatureNegotiation(t *testing.T) {
	result := CapabilityInitializeResult{ProtocolVersion: CapabilityProtocolVersion, RequiredHostFeatures: []string{HostFeatureServiceContinuation}}
	if err := ValidateCapabilityNegotiation(result, []HostServiceMethod{ServiceCallMethod}); err == nil {
		t.Fatal("accepted continuation feature without its gateway")
	}
	if err := ValidateCapabilityNegotiation(result, []HostServiceMethod{ServiceCallMethod, ServiceContinueMethod}); err != nil {
		t.Fatal(err)
	}
}
