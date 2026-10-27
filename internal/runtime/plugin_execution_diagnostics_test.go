package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/pluginhost"
)

func TestPluginExecutionDiagnosticsKeepCollidingGenerationOwners(t *testing.T) {
	// Separate hosts deterministically issue the same first opaque execution ID.
	// The process E2E covers real client cancellation and publication; this case
	// protects diagnostic identity, wrong-host cancellation and late cleanup
	// without guessing how many callbacks the product composition will allocate.
	type heldExecution struct {
		generation *PluginGeneration
		host       *pluginhost.Host
		client     *generationClient
		started    chan string
		done       chan struct{}
		context    context.Context
		err        error
		row        PluginExecutionSnapshot
	}
	newHeld := func(id string) *heldExecution {
		held := &heldExecution{started: make(chan string, 1), done: make(chan struct{})}
		held.client = &generationClient{id: "fixture", capabilities: []pluginhost.CapabilityDescriptor{{
			ID: pluginhost.CapabilityPluginClientRequest, Kind: "decision", Version: 1,
		}}}
		held.generation = testPluginGeneration("fixture", held.client)
		held.generation.id = id
		held.host = held.generation.host
		held.client.invoke = func(params pluginhost.CapabilityInvokeParams) (pluginhost.CapabilityInvokeResult, error) {
			scope, err := held.host.ResolveExecution("fixture", params.ExecutionID)
			if err != nil {
				return pluginhost.CapabilityInvokeResult{}, err
			}
			held.context = scope.Context
			if err := held.host.RecordExecutionUpdate("fixture", pluginhost.ExecutionUpdateParams{
				ExecutionID: params.ExecutionID, Message: id, Detail: json.RawMessage(`{"held":true}`),
			}); err != nil {
				return pluginhost.CapabilityInvokeResult{}, err
			}
			held.started <- params.ExecutionID
			<-scope.Context.Done()
			return pluginhost.CapabilityInvokeResult{}, scope.Context.Err()
		}
		return held
	}
	first, second, third := newHeld("gen-z"), newHeld("gen-a"), newHeld("gen-m")
	session := testGenerationSession(t, first.generation)
	t.Cleanup(func() { session.Cleanup() })
	start := func(held *heldExecution) {
		t.Helper()
		host, generation := session.RetainPluginHost(pluginhost.CapabilityPluginClientRequest)
		if host != held.host || generation != held.generation {
			t.Fatal("admission selected another generation")
		}
		ctx, cancel := context.WithCancel(context.Background())
		t.Cleanup(func() { cancel(); <-held.done })
		go func() {
			held.err = host.InvokeCapability(ctx, host.Capabilities(pluginhost.CapabilityPluginClientRequest)[0],
				pluginhost.PluginClientRequestInput{Method: "hold"}, &pluginhost.PluginClientRequestOutput{})
			session.ReleasePluginGeneration(generation)
			close(held.done)
		}()
		select {
		case id := <-held.started:
			for _, row := range session.PluginExecutionSnapshots() {
				if row.GenerationID == generation.id && row.ID == id {
					held.row = row
					return
				}
			}
			t.Fatal("admitted execution missing from diagnostics")
		case <-held.done:
			t.Fatalf("execution returned before holding: %v", held.err)
		case <-time.After(5 * time.Second):
			t.Fatal("execution did not reach its admission barrier")
		}
	}
	start(first)
	for _, held := range []*heldExecution{second, third} {
		if err := session.ActivatePluginGeneration(held.generation, nil); err != nil {
			t.Fatal(err)
		}
		start(held)
		if held.row.ID != first.row.ID {
			t.Fatalf("fixture did not create colliding host-local IDs: %q != %q", held.row.ID, first.row.ID)
		}
	}
	want := []PluginExecutionSnapshot{first.row, second.row, third.row}
	want[0].Retained, want[1].Retained = true, true
	sort.Slice(want, func(i, j int) bool { return want[i].GenerationID < want[j].GenerationID })
	for range 3 {
		if got := session.PluginExecutionSnapshots(); !reflect.DeepEqual(got, want) {
			t.Fatalf("colliding generation diagnostics = %+v, want %+v", got, want)
		}
	}
	// Cancel by the retained host, not the current host with the same local ID.
	// Waiting for dispatch and generation release also verifies terminal cleanup.
	first.host.CancelExecutions(context.Canceled)
	select {
	case <-first.done:
	case <-time.After(5 * time.Second):
		t.Fatal("retained execution did not settle on its owning host")
	}
	if !errors.Is(first.err, context.Canceled) || !first.client.closed {
		t.Fatalf("retained execution result = %v, process closed = %v", first.err, first.client.closed)
	}
	want = want[:2] // gen-a and gen-m survive; gen-z was the cancelled owner.
	for _, held := range []*heldExecution{second, third} {
		if held.context.Err() != nil {
			t.Fatalf("cancelled another generation with the same execution ID: %s", held.generation.id)
		}
	}
	if got := session.PluginExecutionSnapshots(); !reflect.DeepEqual(got, want) {
		t.Fatalf("surviving executions after cancellation = %+v, want %+v", got, want)
	}
	// Repeated old cleanup and a late progress update cannot resolve the same
	// display ID on the replacement host or keep the retired owner alive.
	first.host.CancelExecutions(context.Canceled)
	session.ReleasePluginGeneration(first.generation)
	if err := first.host.RecordExecutionUpdate("fixture", pluginhost.ExecutionUpdateParams{
		ExecutionID: first.row.ID, Message: "late old progress",
	}); err == nil || err.Code != "execution_not_found" {
		t.Fatalf("late retired update = %v", err)
	}
	if first.generation.host != nil || first.generation.refs.Load() >= 0 {
		t.Fatal("diagnostic reads retained the released generation")
	}
	for _, held := range []*heldExecution{second, third} {
		if held.context.Err() != nil {
			t.Fatalf("late cleanup cancelled another generation: %s", held.generation.id)
		}
	}
	if got := session.PluginExecutionSnapshots(); !reflect.DeepEqual(got, want) {
		t.Fatalf("late cleanup changed another generation: %+v", got)
	}
}
