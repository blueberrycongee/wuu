package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/composition"
	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/credentialstore"
	"github.com/blueberrycongee/wuu/internal/loopdriver"
	"github.com/blueberrycongee/wuu/internal/permissionpolicy"
	"github.com/blueberrycongee/wuu/internal/persistence"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/pluginprotocol"
	"github.com/blueberrycongee/wuu/internal/pluginsettings"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/session"
)

// Candidate admission uses the same configured model/capability checks as the
// step resolver, before durable mutation, without touching live tools or I/O.
func TestStepModelCandidateRejectsUnknownDisabledAndPermissions(t *testing.T) {
	s := &Session{Permissions: config.ResolvedPermissions{Mode: config.PermissionModeReadOnly}}
	cfg := config.Config{DefaultProvider: "fixture", Providers: map[string]config.ProviderConfig{"fixture": {Type: "openai-compatible", Model: "one", Models: map[string]config.ProviderModelConfig{"two": {}, "disabled": {Disabled: true}}}}}
	runner := &agent.StreamRunner{ProviderName: "fixture", Model: "one"}
	for _, selection := range []session.RuntimeSelection{
		{Provider: "missing", Model: "one", PermissionMode: config.PermissionModeReadOnly},
		{Provider: "fixture", Model: "unknown", PermissionMode: config.PermissionModeReadOnly},
		{Provider: "fixture", Model: "disabled", PermissionMode: config.PermissionModeReadOnly},
		{Provider: "fixture", Model: "two", PermissionMode: config.PermissionModeUnconfined},
	} {
		if _, err := s.prepareStepModelBinding(context.Background(), cfg, runner, nil, "thread", s.RootDir, nil, selection); err == nil {
			t.Fatalf("accepted invalid selection: %+v", selection)
		}
	}
	runner.Tools = selectionInvalidTools{}
	if _, err := s.prepareStepModelBinding(context.Background(), cfg, runner, nil, "thread", s.RootDir, nil, session.RuntimeSelection{Provider: "fixture", Model: "one", PermissionMode: config.PermissionModeReadOnly}); err == nil || !strings.Contains(err.Error(), "tool") {
		t.Fatalf("incompatible active tool surface accepted: %v", err)
	}
}

type selectionInvalidTools struct{}

func (selectionInvalidTools) Definitions() []providers.ToolDefinition {
	return []providers.ToolDefinition{{Name: "invalid tool name"}}
}
func (selectionInvalidTools) Execute(context.Context, providers.ToolCall) (string, error) {
	return "", errors.New("unexpected tool effect")
}

// The selected service provider (not a private client substitution) receives
// both steps. Committing intent inside the first request cannot redirect it;
// rebuilding the conversation consumes the same durable selection.
func TestSelectedModelProviderNextStepAndRestore(t *testing.T) {
	ctx := context.Background()
	adapter := &stepSelectionAdapter{}
	rt := newStepSelectionRuntime(t, adapter)
	if _, err := rt.Sessions.Create("selection"); err != nil {
		t.Fatal(err)
	}
	if _, err := rt.Sessions.SetRuntimeSelection("selection", session.RuntimeSelection{Provider: "first", Model: "one", PermissionMode: rt.Permissions.Mode}); err != nil {
		t.Fatal(err)
	}
	adapter.selectNext = func() error {
		_, err := rt.Sessions.SetNextStepModelSelection("selection", session.NextStepModelSelection{ExpectedRevision: 1, Selection: session.RuntimeSelection{Provider: "second", Model: "two", PermissionMode: rt.Permissions.Mode}})
		return err
	}
	thread, err := rt.NewThreadRuntime("selection")
	if err != nil {
		t.Fatal(err)
	}
	defer rt.releasePluginGeneration(thread.PluginGeneration)
	if err := thread.PrepareNextStepModel(ctx, session.RuntimeSelection{Provider: "second", Model: "two", PermissionMode: rt.Permissions.Mode}); err != nil {
		t.Fatalf("capable replacement engine rejected next-step intent: %v", err)
	}
	thread.StreamRunner.ModelInputReceiptStore = sessionModelInputReceiptStore{sessions: rt.Sessions, sessionID: "selection"}
	var modelEvents []providers.ModelStepSummary
	result, err := thread.StreamRunner.RunWithCallback(ctx, []providers.ChatMessage{{Role: "system", Content: thread.StreamRunner.SystemPrompt}, {Role: "user", Content: "continue with selected model"}}, func(event providers.StreamEvent) {
		if event.ModelStep != nil {
			decoded, err := engineEventFromWire(engineEventToWire(event))
			if err != nil || decoded.ModelStep == nil {
				t.Fatalf("engine event lost model identity: %+v %v", decoded, err)
			}
			modelEvents = append(modelEvents, *decoded.ModelStep)
		}
	})
	if err != nil || result.Content != "finished" || result.InputTokens != 6 || result.OutputTokens != 4 {
		t.Fatalf("run: %+v %v", result, err)
	}
	if len(adapter.requests) != 2 || adapter.requests[0].Provider != "first" || adapter.requests[0].Model != "one" || adapter.requests[1].Provider != "second" || adapter.requests[1].Model != "two" {
		t.Fatalf("wrong selected model routing: %+v", adapter.requests)
	}
	if len(modelEvents) != 1 || modelEvents[0].Provider != "second" || modelEvents[0].Revision == 0 {
		t.Fatalf("actual-step events: %+v", modelEvents)
	}
	decoded, err := engineResultFromWire(engineResultToWire(result))
	if err != nil || len(decoded.ModelSteps) != 2 || decoded.ModelSteps[0].InputTokens != 3 || decoded.ModelSteps[1].InputTokens != 3 || decoded.ModelSteps[1].Provider != "second" {
		t.Fatalf("mixed engine usage lost attribution: %+v %v", decoded, err)
	}
	receipts, err := rt.Sessions.LoadModelInputReceipts("selection")
	if err != nil || len(receipts) != 2 {
		t.Fatalf("durable receipts: %+v %v", receipts, err)
	}
	for index, record := range receipts {
		var receipt agent.ModelInputReceipt
		if err := json.Unmarshal(record.Payload, &receipt); err != nil {
			t.Fatal(err)
		}
		step := decoded.ModelSteps[index]
		if receipt.Provider != step.Provider || receipt.ModelSelectionRevision != step.Revision || receipt.OperationID != step.OperationID {
			t.Fatalf("receipt disagrees with dispatched step: %+v / %+v", receipt, step)
		}
		if step.Revision != 0 && step.OperationID != modelEvents[0].OperationID {
			t.Fatalf("actual event disagrees with revised receipt: %+v", modelEvents[0])
		}
	}
	saved, _, err := rt.Sessions.Find("selection")
	if err != nil {
		t.Fatal(err)
	}
	restored, err := rt.NewThreadRuntimeForRootModel("selection", rt.RootDir, ThreadModelSelection{Provider: saved.Provider, Model: saved.Model, PermissionMode: saved.PermissionMode})
	if err != nil {
		t.Fatal(err)
	}
	defer rt.releasePluginGeneration(restored.PluginGeneration)
	if _, err := restored.StreamRunner.RunWithCallback(ctx, []providers.ChatMessage{{Role: "user", Content: "resume"}}, nil); err != nil {
		t.Fatal(err)
	}
	if len(adapter.requests) != 3 || adapter.requests[2].Provider != "second" || adapter.requests[2].Model != "two" {
		t.Fatalf("restore lost selection: %+v", adapter.requests)
	}
}

func newStepSelectionRuntime(t *testing.T, adapter *stepSelectionAdapter) *Session {
	t.Helper()
	modules := []string{engineExecutionConsumer, sessionStateConsumer, persistence.JSONLModule, kernelHostModule, loopdriver.DefaultDriverID, sessionExecutionConsumer, toolExecutionConsumer, StateServicesConsumer, credentialstore.FileModule, pluginsettings.Module, config.Module, permissionpolicy.Module, ArtifactModule, DataQueryModule, "wuu.session.default", "wuu.tools.default", "wuu.prompt.default", "wuu.compaction.default", "wuu.agent.policy.default", adapter.ID(), modelExecutionConsumer, capacityPolicyModule, capacityResourceModule}
	cfg := config.Config{Composition: &composition.Config{Version: composition.Version, Modules: &modules, Services: []composition.Binding{{Service: selectedModelService, Major: 1, Provider: adapter.ID()}, {Service: "engine.wuu", Major: 1, Provider: adapter.ID()}}}, DefaultProvider: "first", Providers: map[string]config.ProviderConfig{
		"first":  {Type: "openai-compatible", BaseURL: "https://unused.invalid", APIKey: "synthetic", Model: "one"},
		"second": {Type: "openai-compatible", BaseURL: "https://unused.invalid", APIKey: "synthetic", Model: "two"},
	}}
	rt, err := NewSession(Options{RootDir: t.TempDir(), HomeDir: t.TempDir(), Config: cfg, NoTools: true, NonInteractive: true, NativeProviders: []NativeProviderFactory{{ID: adapter.ID(), Build: func(NativeProviderContext) (pluginhost.Client, error) {
		adapter.state = pluginhost.StatePrepared
		return adapter, nil
	}}}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = rt.Cleanup() })
	return rt
}

// Public session intent is not proof that its selected engine can consume it.
// Both a live boundary and restart must reject before the next model dispatch.
func TestSelectedModelProviderRejectsUnsupportedDurableIntent(t *testing.T) {
	ctx := context.Background()
	adapter := &stepSelectionAdapter{legacyEngine: true}
	rt := newStepSelectionRuntime(t, adapter)
	if _, err := rt.Sessions.Create("selection"); err != nil {
		t.Fatal(err)
	}
	initial := session.RuntimeSelection{Provider: "first", Model: "one", PermissionMode: rt.Permissions.Mode}
	if _, err := rt.Sessions.SetRuntimeSelection("selection", initial); err != nil {
		t.Fatal(err)
	}
	adapter.selectNext = func() error {
		_, err := rt.Sessions.SetNextStepModelSelection("selection", session.NextStepModelSelection{ExpectedRevision: 1, Selection: session.RuntimeSelection{Provider: "second", Model: "two", PermissionMode: rt.Permissions.Mode}})
		return err
	}
	thread, err := rt.NewThreadRuntime("selection")
	if err != nil {
		t.Fatal(err)
	}
	defer rt.releasePluginGeneration(thread.PluginGeneration)
	_, err = thread.StreamRunner.RunWithCallback(ctx, []providers.ChatMessage{{Role: "user", Content: "continue"}}, nil)
	if err == nil || !strings.Contains(err.Error(), "engine") || len(adapter.requests) != 1 {
		t.Fatalf("unsupported engine consumed durable intent: requests=%d err=%v", len(adapter.requests), err)
	}
	saved, _, err := rt.Sessions.Find("selection")
	if err != nil {
		t.Fatal(err)
	}
	restored, err := rt.NewThreadRuntimeForRootModel("selection", rt.RootDir, ThreadModelSelection{Provider: saved.Provider, Model: saved.Model, PermissionMode: saved.PermissionMode})
	if err != nil {
		t.Fatal(err)
	}
	defer rt.releasePluginGeneration(restored.PluginGeneration)
	_, err = restored.StreamRunner.RunWithCallback(ctx, []providers.ChatMessage{{Role: "user", Content: "resume"}}, nil)
	if err == nil || !strings.Contains(err.Error(), "engine") || len(adapter.requests) != 1 {
		t.Fatalf("restore dispatched unsupported intent: requests=%d err=%v", len(adapter.requests), err)
	}
}

type stepSelectionAdapter struct {
	nativeModelProvider
	legacyEngine bool
	requests     []pluginprotocol.LogicalModelRequestV1
	selectNext   func() error
}

func (*stepSelectionAdapter) ID() string                                        { return "test.step-selection-model" }
func (*stepSelectionAdapter) Prepare(context.Context) error                     { return nil }
func (*stepSelectionAdapter) Close(context.Context) error                       { return nil }
func (*stepSelectionAdapter) RequiredServices() []pluginhost.ServiceRequirement { return nil }
func (*stepSelectionAdapter) ModelAdapters() []pluginhost.ModelAdapterDescriptor {
	return []pluginhost.ModelAdapterDescriptor{{ID: nativeModelAdapter, Version: 1, Mode: "direct"}}
}
func (f *stepSelectionAdapter) ProvidedServices() []pluginhost.ServiceDescriptor {
	services := f.nativeModelProvider.ProvidedServices()
	return append(services, engineServiceDescriptor("wuu"))
}
func (f *stepSelectionAdapter) InvokeService(_ context.Context, input pluginhost.ServiceInvokeParams) (json.RawMessage, error) {
	if input.Service == "engine.wuu" {
		capabilities := []string{"host-managed-continuation"}
		if !f.legacyEngine {
			capabilities = append(capabilities, "step-model-selection")
		}
		return json.Marshal(map[string]any{"id": "wuu", "version": "1", "enabled": true, "capabilities": capabilities})
	}
	return json.Marshal(f.ModelAdapters()[0])
}
func (f *stepSelectionAdapter) InvokeCapability(_ context.Context, p pluginhost.CapabilityInvokeParams) (pluginhost.CapabilityInvokeResult, error) {
	var input pluginprotocol.ModelAdapterInput
	if err := json.Unmarshal(p.Input, &input); err != nil {
		return pluginhost.CapabilityInvokeResult{}, err
	}
	f.requests = append(f.requests, input.Request)
	finish, content := "stop", "finished"
	if len(f.requests) == 1 {
		if err := f.selectNext(); err != nil {
			return pluginhost.CapabilityInvokeResult{}, err
		}
		finish, content = "continue", "first step"
	}
	raw, err := json.Marshal(pluginprotocol.ModelAdapterOutput{Response: &pluginprotocol.ModelResponseV1{Content: content, FinishReason: finish, Usage: &pluginprotocol.ModelRequestUsage{InputTokens: 3, OutputTokens: 2}}})
	return pluginhost.CapabilityInvokeResult{Output: raw}, err
}
