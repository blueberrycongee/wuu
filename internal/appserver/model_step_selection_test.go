package appserver

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/agentengine"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/runtime"
	"github.com/blueberrycongee/wuu/internal/session"
	"github.com/blueberrycongee/wuu/internal/sessiontrace"
)

// Explicit next-step updates persist intent without detaching an active runtime.
// The default API and permission changes keep their original busy rejection.
func TestServerNextStepModelSelectionKeepsActiveRuntime(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	writeTwoProviderSelectionConfig(t, rt.ConfigPath)
	bindConfigSourceForTest(t, rt)
	out := &lockedBuffer{}
	srv := New(rt, out)
	t.Cleanup(srv.Close)
	if err := srv.handleLine(context.Background(), []byte(`{"id":"start","method":"thread/start"}`)); err != nil {
		t.Fatal(err)
	}
	threadID := remarshal[ThreadStartResult](t, responseByID(t, parseOutput(t, out.String()), "start")["result"]).Thread.ID
	th := srv.thread(threadID)
	running, err := srv.ensureThreadRuntime(th)
	if err != nil {
		t.Fatal(err)
	}
	th.mu.Lock()
	th.running = true
	th.runningProviderName, th.runningModel = th.ModelProvider, th.Model
	th.mu.Unlock()
	defer func() { th.mu.Lock(); th.running = false; th.mu.Unlock() }()
	permission := "unconfined"
	for _, tc := range []struct {
		id, apply, model string
		permission       *string
		wantError        bool
	}{
		{id: "legacy", model: "alt-model", wantError: true},
		{id: "permission", apply: "next_step", model: "alt-model", permission: &permission, wantError: true},
		{id: "unknown", apply: "next_step", model: "unknown", wantError: true},
		{id: "next", apply: "next_step", model: "alt-model"},
	} {
		params := map[string]any{"thread_id": threadID, "provider": "alt-provider", "model": tc.model, "apply_at": tc.apply}
		if tc.permission != nil {
			params["permission_mode"] = *tc.permission
		}
		raw, _ := json.Marshal(map[string]any{"id": tc.id, "method": "config/model/update", "params": params})
		if err := srv.handleLine(context.Background(), raw); err != nil {
			t.Fatal(err)
		}
		response := responseByID(t, parseOutput(t, out.String()), tc.id)
		if (response["error"] != nil) != tc.wantError {
			t.Fatalf("%s: %+v", tc.id, response)
		}
		th.mu.Lock()
		current := th.execRuntime
		th.mu.Unlock()
		if current != running {
			t.Fatal("selection detached active runtime")
		}
	}
	th.mu.Lock()
	snapshot := th.snapshotLocked()
	th.mu.Unlock()
	if snapshot.Model != "alt-model" || snapshot.RunningModel != "fake-model" || snapshot.RunningModelProvider != "fake-provider" {
		t.Fatalf("pending selection obscured the actual in-flight model: %+v", snapshot)
	}
	selected, found, err := rt.Sessions.Find(threadID)
	if err != nil || !found || selected.Model != "alt-model" || selected.ModelSelectionApplyAt != "next_step" || selected.ModelSelectionRevision == 0 {
		t.Fatalf("selection not durable: %+v %v", selected, err)
	}
}

func TestMixedModelUsagePersistsAndRestoresWithoutInitialModelAttribution(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	if _, err := rt.Sessions.Create("mixed-usage"); err != nil {
		t.Fatal(err)
	}
	result := agent.LoopResult{InputTokens: 30, OutputTokens: 7, ContextTokens: 22, ModelSteps: []providers.ModelStepUsage{
		{ModelStepSummaryV1: providers.ModelStepSummary{Provider: "first", Model: "one", Revision: 1, OperationID: "op-one"}, InputTokens: 10, OutputTokens: 3, ContextTokens: 13},
		{ModelStepSummaryV1: providers.ModelStepSummary{Provider: "second", Model: "two", APIModel: "two-api", Revision: 2, StepIndex: 1, OperationID: "op-two", ContextWindowTokens: 32000}, InputTokens: 20, OutputTokens: 4, ContextTokens: 22},
	}}
	if err := appendLoopTokenUsage(rt.Sessions, "mixed-usage", "first", "one", result); err != nil {
		t.Fatal(err)
	}
	records, err := rt.Sessions.LoadHistoryRecords("mixed-usage", true)
	if err != nil || len(records) != 1 || records[0].Provider != "" || records[0].Model != "" || len(records[0].ModelSteps) != 2 {
		t.Fatalf("mixed usage mislabeled: %+v %v", records, err)
	}
	rows, err := rt.Sessions.ListTokenUsage()
	if err != nil || len(rows) != 2 || rows[0].Provider != "first" || rows[0].InputTokens != 10 || rows[1].Provider != "second" || rows[1].InputTokens != 20 {
		t.Fatalf("per-model accounting lost: %+v %v", rows, err)
	}
	meta, err := persistedMessageFromHistoryRecord(records[0])
	if err != nil {
		t.Fatal(err)
	}
	activity, err := rt.Sessions.LatestSubscriptionActivity([]session.SubscriptionActivityKey{{Provider: "first"}, {Provider: "second"}})
	if err != nil || activity[session.SubscriptionActivityKey{Provider: "first"}].LocalUsage.InputTokens != 10 || activity[session.SubscriptionActivityKey{Provider: "second"}].LocalUsage.InputTokens != 20 {
		t.Fatalf("subscription attribution lost: %+v %v", activity, err)
	}
	srv := New(rt, &lockedBuffer{})
	t.Cleanup(srv.Close)
	composition := srv.contextCompositionFromTrace(testRuntimeState(t, rt), "mixed-usage", "", sessiontrace.ReplaySummary{
		LatestTurn:      &sessiontrace.TurnRecord{ModelSteps: result.ModelSteps},
		ContextRequests: []sessiontrace.RequestContextRecord{{StepIndex: 1}},
	})
	if composition.Provider != "second" || composition.Model != "two-api" || composition.ContextWindowTokens != 32000 {
		t.Fatalf("latest context was labeled with initial model: %+v", composition)
	}
	metas := []persistedMessage{meta}
	turns := applyTokenUsageMetasToTurns([]Turn{{ID: "turn"}}, metas)
	if turns[0].InputTokens != 30 || turns[0].OutputTokens != 7 {
		t.Fatalf("aggregate recovery lost usage: %+v", turns)
	}
}

// A selected host-loop engine owns its identity; Wuu is not a privileged name.
func TestServerNextStepModelSelectionForCustomCapableEngine(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	factory := &customStepSelectionEngine{Factory: runtime.NewWuuEngine(rt)}
	registry := agentengine.NewRegistry()
	if err := registry.Register(runtime.NewWuuEngine(rt)); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(factory); err != nil {
		t.Fatal(err)
	}
	rt.SetEnginesForTest(registry)
	writeTwoProviderSelectionConfig(t, rt.ConfigPath)
	bindConfigSourceForTest(t, rt, &customStepSelectionEngineMetadata{embeddedEngineMetadata{factory: factory}})
	out := &lockedBuffer{}
	srv := New(rt, out)
	t.Cleanup(srv.Close)
	if err := srv.handleLine(context.Background(), []byte(`{"id":"custom-start","method":"thread/start","params":{"engine":"custom-step"}}`)); err != nil {
		t.Fatal(err)
	}
	response := responseByID(t, parseOutput(t, out.String()), "custom-start")
	if response["error"] != nil {
		t.Fatalf("custom thread admission: %+v", response)
	}
	threadID := remarshal[ThreadStartResult](t, response["result"]).Thread.ID
	th := srv.thread(threadID)
	active, err := srv.ensureThreadRuntime(th)
	if err != nil {
		t.Fatal(err)
	}
	if err := active.PrepareNextStepModel(context.Background(), session.RuntimeSelection{Provider: "alt-provider", Model: "alt-model", PermissionMode: th.PermissionMode}); err != nil {
		t.Fatalf("custom retained descriptor does not authorize model selection: %v", err)
	}
	th.mu.Lock()
	th.running = true
	th.mu.Unlock()
	defer func() { th.mu.Lock(); th.running = false; th.mu.Unlock() }()
	raw, _ := json.Marshal(map[string]any{"id": "custom-next", "method": "config/model/update", "params": map[string]any{"thread_id": threadID, "provider": "alt-provider", "model": "alt-model", "apply_at": "next_step"}})
	if err := srv.handleLine(context.Background(), raw); err != nil {
		t.Fatal(err)
	}
	response = responseByID(t, parseOutput(t, out.String()), "custom-next")
	if response["error"] != nil {
		t.Fatalf("capable custom engine rejected: %+v", response)
	}
	selected, found, err := rt.Sessions.Find(threadID)
	if err != nil || !found || selected.EngineID != "custom-step" || selected.ModelSelectionApplyAt != "next_step" || selected.Model != "alt-model" {
		t.Fatalf("custom intent: %+v %v", selected, err)
	}
	binding, err := active.StreamRunner.ResolveModelStep(context.Background(), 0)
	if err != nil || binding == nil || binding.Model != "alt-model" || binding.Provider != "alt-provider" || binding.Revision != selected.ModelSelectionRevision {
		t.Fatalf("capable custom engine could not consume its accepted intent: %+v %v", binding, err)
	}
	th.mu.Lock()
	same := th.execRuntime == active
	th.mu.Unlock()
	if !same {
		t.Fatal("custom engine selection detached its active runtime")
	}
}

type customStepSelectionEngine struct{ agentengine.Factory }

func (f *customStepSelectionEngine) Descriptor(ctx context.Context) (agentengine.Descriptor, error) {
	descriptor, err := f.Factory.Descriptor(ctx)
	descriptor.ID = "custom-step"
	return descriptor, err
}

type customStepSelectionEngineMetadata struct{ embeddedEngineMetadata }

func (*customStepSelectionEngineMetadata) ID() string { return "fixture.custom-step-engine" }
func (c *customStepSelectionEngineMetadata) ProvidedServices() []pluginhost.ServiceDescriptor {
	services := c.embeddedEngineMetadata.ProvidedServices()
	services[0].Name = "engine.custom-step"
	return services
}
