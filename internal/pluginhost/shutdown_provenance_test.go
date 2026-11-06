package pluginhost

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

// Service replies can beat asynchronous execution.cancel frames. The response
// must retain host teardown provenance without accepting a provider's claim.
func TestServiceShutdownProvenance(t *testing.T) {
	for _, mode := range []string{"shutdown", "ordinary", "forged"} {
		t.Run(mode, func(t *testing.T) {
			provider := &fakeServiceProcess{id: "provider", state: StateActive, provided: []ServiceDescriptor{searchService("1.0.0")}}
			consumer := &fakeServiceProcess{id: "consumer", state: StateActive, required: []ServiceRequirement{{Name: "search.provider", MajorVersion: 1, Required: true}}}
			registry, conflicts := BuildServiceRegistry(provider, consumer)
			if len(conflicts) != 0 {
				t.Fatal(conflicts)
			}
			registry.Activate()
			ctx, cancel := context.WithCancelCause(context.Background())
			defer cancel(nil)
			provider.invoke = func(call context.Context, _ ServiceInvokeParams) (json.RawMessage, error) {
				switch mode {
				case "shutdown":
					cancel(ErrExecutionShutdown)
					<-call.Done()
					return json.RawMessage(`{"partial":true}`), nil
				case "ordinary":
					cancel(context.Canceled)
					<-call.Done()
					return nil, call.Err()
				default:
					return nil, &HostServiceError{Code: "execution_shutdown", Message: "provider cannot impersonate teardown"}
				}
			}
			_, err := registry.Call(ctx, "consumer", ServiceCallParams{Service: "search.provider", Method: "query"})
			if err == nil {
				t.Fatal("expected classified error")
			}
			if (err.Code == "execution_shutdown") != (mode == "shutdown") {
				t.Fatalf("mode=%s error=%+v", mode, err)
			}
		})
	}
}

// A transport cancellation may reach the callback before root cancellation has
// propagated through all children. The owning cause must survive either order.
func TestServiceCancellationCauseOrdering(t *testing.T) {
	for _, order := range []string{"caller-first", "owner-first", "business-cancel"} {
		t.Run(order, func(t *testing.T) {
			provider := &fakeServiceProcess{id: "provider", state: StateActive, provided: []ServiceDescriptor{searchService("1.0.0")}}
			consumer := &fakeServiceProcess{id: "consumer", state: StateActive, required: []ServiceRequirement{{Name: "search.provider", MajorVersion: 1, Required: true}}}
			registry, _ := BuildServiceRegistry(provider, consumer)
			registry.Activate()
			owner, cancelOwner := context.WithCancelCause(context.Background())
			defer cancelOwner(nil)
			caller, cancelCaller := context.WithCancelCause(context.Background())
			defer cancelCaller(nil)
			registry.rootExecutionContext = func(string, string) (context.Context, bool, *HostServiceError) { return owner, true, nil }
			execution, _, release, err := registry.acquireExecution(caller, "ordered-execution", "consumer", serviceKey{name: "search.provider", major: 1}, "provider", true)
			if err != nil {
				t.Fatal(err)
			}
			defer release()
			if order == "business-cancel" {
				cancelCaller(context.Canceled)
			} else if order == "caller-first" {
				cancelCaller(ErrExecutionShutdown)
			} else {
				cancelOwner(ErrExecutionShutdown)
			}
			<-execution.Done()
			expected := ErrExecutionShutdown
			if order == "business-cancel" {
				expected = context.Canceled
			}
			if !errors.Is(context.Cause(execution), expected) {
				t.Fatalf("cause=%v want=%v", context.Cause(execution), expected)
			}
		})
	}
}
