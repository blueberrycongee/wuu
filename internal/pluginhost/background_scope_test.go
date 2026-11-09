package pluginhost

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

func TestBackgroundExecutionScopeCannotBeSuppliedByCaller(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	type observation struct{ callback, owner bool }
	observed := make(chan observation, 2)
	probe := &fakeServiceProcess{id: "probe", state: StateActive, provided: []ServiceDescriptor{searchService("1.0.0")},
		invoke: func(ctx context.Context, params ServiceInvokeParams) (json.RawMessage, error) {
			observed <- observation{IsBackgroundExecution(ctx), params.CallerScope != nil && IsBackgroundExecution(params.CallerScope.Context)}
			return json.RawMessage(`{}`), nil
		}}
	worker := &fakeServiceProcess{id: "worker", state: StateActive,
		provided: []ServiceDescriptor{{Name: "worker.jobs", Version: "1.0.0", Methods: []ServiceMethodDescriptor{{Name: "run", InputSchema: "worker.run.input.v1", OutputSchema: "worker.run.output.v1"}}}},
		required: []ServiceRequirement{{Name: "search.provider", MajorVersion: 1, Required: true}}}
	registry, conflicts := BuildServiceRegistry(worker, probe)
	if len(conflicts) != 0 {
		t.Fatal(conflicts)
	}
	host := New(worker, probe)
	host.AttachServiceRegistry(registry, nil)
	defer host.Close(ctx)
	if err := host.Prepare(ctx); err != nil {
		t.Fatal(err)
	}
	if err := host.Activate(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := host.CallService(ctx, "worker", "search.provider", "query", json.RawMessage(`{"background":true,"background_execution":true}`)); err != nil {
		t.Fatal(err)
	}
	if got := <-observed; got.callback || got.owner {
		t.Fatalf("foreground forged background authority: %+v", got)
	}
	callback := make(chan bool, 1)
	worker.invoke = func(ctx context.Context, params ServiceInvokeParams) (json.RawMessage, error) {
		callback <- IsBackgroundExecution(ctx)
		result, err := registry.Call(context.Background(), "worker", ServiceCallParams{Service: "search.provider", Method: "query", ExecutionID: params.ExecutionID})
		if err != nil {
			return nil, err
		}
		return result, nil
	}
	if _, err := host.StartBackground(ctx, "worker", BackgroundStartParams{Key: "once", Service: "worker.jobs", Method: "run"}); err != nil {
		t.Fatal(err)
	}
	select {
	case marked := <-callback:
		if !marked {
			t.Fatal("admitted background callback lost host provenance")
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	select {
	case got := <-observed:
		if !got.owner {
			t.Fatalf("authenticated nested callback lost background owner: %+v", got)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if IsBackgroundExecution(nil) || IsBackgroundExecution(context.Background()) {
		t.Fatal("unmarked context acquired background authority")
	}
}
