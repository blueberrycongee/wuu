package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/agentcontrol"
	"github.com/blueberrycongee/wuu/internal/composition"
	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/executionauth"
	"github.com/blueberrycongee/wuu/internal/persistence"
	pluginpkg "github.com/blueberrycongee/wuu/internal/plugin"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
)

type compositionActivationCapture struct {
	fakeServicePluginClient
	retain       func() (*State, error)
	origin       string
	admissionErr error
}

func (c *compositionActivationCapture) Activate(context.Context) error {
	state, err := c.retain()
	if err != nil {
		return err
	}
	defer state.Close()
	c.origin = state.ExecutionCompositionID()
	execution, err := state.RetainExecution()
	c.admissionErr = err
	if execution != nil {
		_ = execution.Close()
	}
	c.active = true
	return nil
}

// Activation can capture state before NewSession returns. A candidate must
// already have its own identity, and cannot admit work before publication.
func TestCompositionActivationHasOriginBeforePublication(t *testing.T) {
	const consumerID = "example.activation-origin"
	var captures []*compositionActivationCapture
	factory := NativeProviderFactory{ID: consumerID, Build: func(input NativeProviderContext) (pluginhost.Client, error) {
		consumer := &compositionActivationCapture{fakeServicePluginClient: fakeServicePluginClient{id: consumerID, required: []pluginhost.ServiceRequirement{{Name: pluginhost.KernelSessionListService, MajorVersion: 1, Required: true}}}, retain: input.RetainState}
		captures = append(captures, consumer)
		return consumer, nil
	}}
	cfg := config.Default()
	cfg.DefaultProvider = "fixture"
	cfg.Providers = map[string]config.ProviderConfig{"fixture": {Type: "openai-compatible", BaseURL: "http://127.0.0.1:9/v1", APIKey: "synthetic", Model: "fixture"}}
	cfg.Composition = &composition.Config{Version: composition.Version, Profile: "headless", Patches: []composition.Patch{{Modules: map[string]bool{consumerID: true}}}}
	catalog := []pluginpkg.Plugin{}
	owner, err := NewSession(Options{RootDir: t.TempDir(), WuuHome: t.TempDir(), HomeDir: t.TempDir(), Config: cfg, DiscoveredPlugins: &catalog, NonInteractive: true, NativeProviders: []NativeProviderFactory{factory}})
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Cleanup()
	kernel := newKernelHostServices(nil, nil)
	kernel.bindStateGeneration(owner.pluginGeneration, owner)
	defer kernel.compositions.close()
	raw, err := kernel.compositions.InvokeService(context.Background(), pluginhost.ServiceInvokeParams{Caller: "controller", Method: "create", Params: json.RawMessage(`{"inherit":true,"state":"parent"}`)})
	if err != nil {
		t.Fatal(err)
	}
	var scope pluginhost.CompositionView
	if err := json.Unmarshal(raw, &scope); err != nil {
		t.Fatal(err)
	}
	if scope.Services.GraphGeneration == "" || scope.Services.GraphGeneration == owner.pluginGeneration.id {
		t.Fatalf("candidate discovery did not preserve isolated graph identity: %+v", scope.Services)
	}
	if scope.Services.GraphGeneration != kernel.compositions.scopes[scope.ID].runtime.pluginGeneration.id {
		t.Fatal("candidate discovery does not identify its running implementation")
	}
	if len(scope.Services.Dependencies) == 0 || len(scope.Services.Requirements) == 0 {
		t.Fatal("candidate discovery omitted authoritative dependency graph")
	}
	if len(captures) != 2 {
		t.Fatalf("expected root and candidate activations, got %d", len(captures))
	}
	if captures[1].origin != scope.ID {
		t.Fatalf("candidate activation used origin %q, want %q", captures[1].origin, scope.ID)
	}
	if captures[1].admissionErr == nil {
		t.Fatal("candidate admitted execution before publication")
	}
	state, err := kernel.compositions.scopes[scope.ID].runtime.RetainState()
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	execution, err := state.RetainExecution()
	if err != nil {
		t.Fatalf("published candidate rejected execution: %v", err)
	}
	if err := execution.Close(); err != nil {
		t.Fatal(err)
	}
}

// Owner retirement must reach workers whose lifetime is independent of the
// parent turn. The HTTP endpoint waits for physical transport cancellation.
func TestCompositionOwnerRetirementCancelsNativeWorker(t *testing.T) {
	entered, canceled, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		close(entered)
		select {
		case <-r.Context().Done():
			close(canceled)
		case <-release:
		}
	}))
	defer endpoint.Close()
	defer close(release)
	cfg := config.Default()
	cfg.DefaultProvider = "fixture"
	cfg.Providers = map[string]config.ProviderConfig{"fixture": {Type: "openai-compatible", BaseURL: endpoint.URL + "/v1", APIKey: "synthetic", Model: "fixture"}}
	cfg.Composition = &composition.Config{Version: composition.Version, Profile: "headless"}
	catalog := []pluginpkg.Plugin{}
	owner, err := NewSession(Options{RootDir: t.TempDir(), WuuHome: t.TempDir(), HomeDir: t.TempDir(), Config: cfg, DiscoveredPlugins: &catalog, NonInteractive: true})
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Cleanup()
	kernel := newKernelHostServices(nil, nil)
	kernel.bindStateGeneration(owner.pluginGeneration, owner)
	scopes := kernel.compositions
	defer scopes.close()
	invoke := func(method, input string) json.RawMessage {
		t.Helper()
		output, err := scopes.InvokeService(context.Background(), pluginhost.ServiceInvokeParams{Caller: "controller", Method: method, Params: json.RawMessage(input)})
		if err != nil {
			t.Fatal(err)
		}
		return output
	}
	var scope pluginhost.CompositionView
	if err := json.Unmarshal(invoke("create", `{"inherit":true,"state":"parent"}`), &scope); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Sessions.Create("retiring-parent"); err != nil {
		t.Fatal(err)
	}
	invoke("bind", fmt.Sprintf(`{"session":"retiring-parent","id":%q,"expected":""}`, scope.ID))
	binding, err := owner.RetainState()
	if err != nil {
		t.Fatal(err)
	}
	defer binding.Close()
	thread, err := owner.NewThreadRuntimeForState(binding, "retiring-parent", owner.RootDir, ThreadModelSelection{})
	if err != nil {
		t.Fatal(err)
	}
	defer thread.CloseComposition()
	defer thread.AgentControl.Close()
	defer thread.AgentControl.StopAll()
	thread.AgentControl.StartWorkerTerminalRecovery()
	if _, err := thread.AgentControl.Spawn(context.Background(), agentcontrol.SpawnRequest{Type: agentcontrol.DefaultSubagentType, TaskName: "hold", Prompt: "Wait for an answer."}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("worker did not reach HTTP inference")
	}
	if err := scopes.stopCaller("controller"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-canceled:
	case <-time.After(3 * time.Second):
		t.Fatal("owner retirement left the native worker's HTTP inference running")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := thread.AgentControl.WaitOwnedWorkerExecutions(ctx); err != nil {
		t.Fatalf("retired worker did not drain: %v", err)
	}
}

// Inheritance captures the active graph, not discovery defaults. Candidates
// must own distinct state and model choices; a failed preparation must leave
// already-created scopes and the parent's selection usable.
func TestCompositionInheritance(t *testing.T) {
	cfg := config.Default()
	cfg.DefaultProvider = "first"
	cfg.Providers = map[string]config.ProviderConfig{
		"first":  {Type: "openai-compatible", BaseURL: "http://127.0.0.1:9/v1", APIKey: "synthetic", Model: "model-one"},
		"second": {Type: "openai-compatible", BaseURL: "http://127.0.0.1:9/v1", APIKey: "synthetic", Model: "model-two"},
	}
	cfg.Composition = composition.DefaultConfig("headless", executionauth.ConsumerModule, executionauth.NativeModule)
	catalog := []pluginpkg.Plugin{}
	opts := Options{RootDir: t.TempDir(), WuuHome: t.TempDir(), HomeDir: t.TempDir(), Config: cfg, DiscoveredPlugins: &catalog, NonInteractive: true}
	owner, err := NewSession(opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = owner.Cleanup() })
	kernel := newKernelHostServices(nil, nil)
	kernel.bindStateGeneration(owner.pluginGeneration, owner)
	scopes := kernel.compositions
	t.Cleanup(func() {
		if err := scopes.close(); err != nil {
			t.Error(err)
		}
	})
	invoke := func(method, input string) (json.RawMessage, error) {
		return scopes.InvokeService(context.Background(), pluginhost.ServiceInvokeParams{Caller: "controller", Method: method, Params: json.RawMessage(input)})
	}
	create := func(input string) pluginhost.CompositionView {
		t.Helper()
		raw, err := invoke("create", input)
		if err != nil {
			t.Fatal(err)
		}
		var view pluginhost.CompositionView
		if err := json.Unmarshal(raw, &view); err != nil {
			t.Fatal(err)
		}
		return view
	}
	first := create(`{"inherit":true}`)
	second := create(`{"inherit":true,"model":{"provider":"second","model":"model-two"}}`)
	if first.ParentGeneration != owner.pluginGeneration.id || second.ParentGeneration != first.ParentGeneration {
		t.Fatal("scope lost its captured parent identity")
	}
	if first.Model.Provider != "first" || first.Model.Model != "model-one" || second.Model.Provider != "second" || second.Model.Model != "model-two" {
		t.Fatalf("scope model choices crossed: first=%+v second=%+v", first.Model, second.Model)
	}
	if owner.ProviderName != "first" || owner.Model != "model-one" {
		t.Fatal("candidate changed the parent's model selection")
	}
	parentGraph := owner.PluginHost.ServiceRegistry().Snapshot(0)
	for _, selected := range parentGraph.Services {
		major, _ := pluginhost.ServiceVersionMajor(selected.Version)
		provider, found := scopes.scopes[first.ID].runtime.PluginHost.ServiceRegistry().ProviderPluginID(selected.Service, major)
		if !found || provider != selected.Provider {
			t.Fatalf("inherited service %s = %q, want %q", selected.Service, provider, selected.Provider)
		}
	}
	firstRuntime, secondRuntime := scopes.scopes[first.ID].runtime, scopes.scopes[second.ID].runtime
	if _, err := firstRuntime.Sessions.Create("scope-only"); err != nil {
		t.Fatal(err)
	}
	for _, other := range []*Session{owner, secondRuntime} {
		if _, found, err := other.Sessions.Find("scope-only"); err != nil || found {
			t.Fatalf("isolated state leaked: found=%v err=%v", found, err)
		}
	}
	for _, invalid := range []string{
		`{"inherit":true,"selection":{"version":2,"patches":[{"modules":{"wuu.tools.default":false}}]}}`,
		`{"inherit":true,"model":{"provider":"absent","model":"model-two"}}`,
		`{"inherit":true,"selection":{"version":2,"profile":"headless"}}`,
		`{}`,
	} {
		if _, err := invoke("create", invalid); err == nil {
			t.Fatalf("invalid candidate was accepted: %s", invalid)
		}
	}
	if len(scopes.scopes) != 2 {
		t.Fatalf("failed preparation published a scope: %d", len(scopes.scopes))
	}
	if _, found, err := firstRuntime.Sessions.Find("scope-only"); err != nil || !found {
		t.Fatalf("failed candidate disturbed active state: found=%v err=%v", found, err)
	}

	// Product bindings must share durable state explicitly while retaining
	// independent model choices. Disposal fences new turns without redirecting
	// admitted work, and unbinding requires the last selected identity.
	shared := create(`{"inherit":true,"state":"parent","model":{"provider":"second","model":"model-two"}}`)
	if _, err := owner.Sessions.Create("product-main"); err != nil {
		t.Fatal(err)
	}
	sharedRuntime := scopes.scopes[shared.ID].runtime
	if provider, ok := sharedRuntime.PluginHost.ServiceRegistry().ProviderPluginIDFor(executionauth.ConsumerModule, executionauth.ServiceName, 1); !ok || provider != parentStateModule {
		t.Fatalf("shared product created a second grant authority: %q, %v", provider, ok)
	}
	if _, err := sharedRuntime.PluginHost.CallService(context.Background(), executionauth.ConsumerModule, executionauth.ServiceName, "describe", json.RawMessage(`{}`)); err != nil {
		t.Fatalf("imported selected grant service unavailable: %v", err)
	}
	if _, err := sharedRuntime.Documents.Read(context.Background(), persistence.Query{Namespace: "wuu.execution_grants.v1"}); err == nil {
		t.Fatal("general imported documents gained access to owned grant records")
	}
	if _, found, err := sharedRuntime.Sessions.Find("product-main"); err != nil || !found {
		t.Fatalf("product scope lost parent history: found=%v err=%v", found, err)
	}
	// A composition can itself own scopes. Parent imports must resolve through
	// that graph, while an isolated descendant reconstructs the selected state
	// providers in its own directory instead of importing the grandparent.
	nestedKernel := newKernelHostServices(nil, nil)
	nestedKernel.bindStateGeneration(sharedRuntime.pluginGeneration, sharedRuntime)
	nested := nestedKernel.compositions
	defer nested.close()
	nestedInvoke := func(method, input string) (json.RawMessage, error) {
		return nested.InvokeService(context.Background(), pluginhost.ServiceInvokeParams{Caller: "nested-controller", Method: method, Params: json.RawMessage(input)})
	}
	for _, source := range []string{"parent", "isolated"} {
		raw, err := nestedInvoke("create", fmt.Sprintf(`{"inherit":true,"state":%q}`, source))
		if err != nil {
			t.Fatalf("create nested %s state: %v", source, err)
		}
		var view pluginhost.CompositionView
		if err := json.Unmarshal(raw, &view); err != nil {
			t.Fatal(err)
		}
		descendant := nested.scopes[view.ID].runtime
		if _, found, err := descendant.Sessions.Find("product-main"); err != nil || found != (source == "parent") {
			t.Fatalf("nested %s state visibility: found=%v err=%v", source, found, err)
		}
		if source == "parent" {
			if _, err := nestedInvoke("bind", fmt.Sprintf(`{"session":"product-main","id":%q,"expected":""}`, view.ID)); err != nil {
				t.Fatal(err)
			}
			binding, err := owner.RetainState()
			if err != nil {
				t.Fatal(err)
			}
			admitted, err := owner.NewThreadRuntimeForState(binding, "product-main", owner.RootDir, ThreadModelSelection{})
			_ = binding.Close()
			if err != nil {
				t.Fatalf("root could not admit nested product binding: %v", err)
			}
			if admitted.CompositionID != view.ID || admitted.StreamRunner.Model != "model-two" {
				t.Fatal("nested execution lost its graph or model")
			}
			if err := admitted.CloseComposition(); err != nil {
				t.Fatal(err)
			}
			if _, err := nestedInvoke("bind", fmt.Sprintf(`{"session":"product-main","id":"","expected":%q}`, view.ID)); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := nestedInvoke("dispose", fmt.Sprintf(`{"id":%q}`, view.ID)); err != nil {
			t.Fatal(err)
		}
	}
	if err := nested.close(); err != nil {
		t.Fatal(err)
	}
	if _, err := invoke("bind", fmt.Sprintf(`{"session":"product-main","id":%q,"expected":""}`, first.ID)); err == nil {
		t.Fatal("isolated state was bound to a product session")
	}
	if _, err := owner.Sessions.Create("external-session"); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Sessions.SetEngine("external-session", "codex"); err != nil {
		t.Fatal(err)
	}
	if _, err := invoke("bind", fmt.Sprintf(`{"session":"external-session","id":%q,"expected":""}`, shared.ID)); err != nil {
		t.Fatal(err)
	}
	externalState, err := owner.RetainState()
	if err != nil {
		t.Fatal(err)
	}
	externalRuntime, err := owner.NewThreadCapabilityRuntimeForState(externalState, "external-session", owner.RootDir, ThreadModelSelection{})
	_ = externalState.Close()
	if err != nil {
		t.Fatal(err)
	}
	if externalRuntime.CompositionID != shared.ID || externalRuntime.StreamRunner != nil {
		t.Fatal("external composition lost its selected graph or constructed a native loop")
	}
	if err := externalRuntime.CloseComposition(); err != nil {
		t.Fatal(err)
	}
	if _, err := invoke("bind", fmt.Sprintf(`{"session":"external-session","id":"","expected":%q}`, shared.ID)); err != nil {
		t.Fatal(err)
	}

	if _, err := invoke("bind", fmt.Sprintf(`{"session":"product-main","id":%q,"expected":""}`, shared.ID)); err != nil {
		t.Fatal(err)
	}
	if _, err := invoke("bind", `{"session":"product-main","id":"","expected":""}`); err == nil {
		t.Fatal("conflicting product binding was accepted")
	}
	state, err := owner.RetainState()
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	thread, err := owner.NewThreadRuntimeForState(state, "product-main", owner.RootDir, ThreadModelSelection{})
	if err != nil {
		t.Fatal(err)
	}
	if thread.CompositionID != shared.ID || thread.StreamRunner.Model != "model-two" {
		t.Fatalf("product execution did not capture composition: id=%q model=%q", thread.CompositionID, thread.StreamRunner.Model)
	}
	if _, err := invoke("bind", fmt.Sprintf(`{"session":"product-main","id":"","expected":%q}`, shared.ID)); err != nil {
		t.Fatal(err)
	}
	if err := owner.InheritSessionComposition(context.Background(), state, "product-main", "product-child", thread); err != nil {
		t.Fatal(err)
	}
	if got, err := owner.SessionCompositionID(context.Background(), state, "product-child"); err != nil || got != shared.ID {
		t.Fatalf("child inherited a later parent selection: %q, %v", got, err)
	}
	if _, err := invoke("bind", fmt.Sprintf(`{"session":"product-main","id":%q,"expected":""}`, shared.ID)); err != nil {
		t.Fatal(err)
	}
	if _, err := invoke("dispose", fmt.Sprintf(`{"id":%q}`, shared.ID)); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.NewThreadRuntimeForState(state, "product-main", owner.RootDir, ThreadModelSelection{}); err == nil {
		t.Fatal("disposed binding silently admitted another composition")
	}
	if thread.StreamRunner.Model != "model-two" {
		t.Fatal("disposal redirected the admitted execution")
	}
	if err := thread.CloseComposition(); err != nil {
		t.Fatal(err)
	}
	// Selection survives resource disposal and process restart. A missing scope
	// must be discoverable and must fence admission until explicitly rebound.
	if err := state.Close(); err != nil {
		t.Fatal(err)
	}
	if err := scopes.close(); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Cleanup(); err != nil {
		t.Fatal(err)
	}
	restarted, err := NewSession(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Cleanup()
	restartedState, err := restarted.RetainState()
	if err != nil {
		t.Fatal(err)
	}
	defer restartedState.Close()
	if got, err := restarted.SessionCompositionID(context.Background(), restartedState, "product-child"); err != nil || got != shared.ID {
		t.Fatalf("restart lost inherited selection: %q, %v", got, err)
	}
	if _, err := restarted.NewThreadRuntimeForState(restartedState, "product-main", restarted.RootDir, ThreadModelSelection{}); err == nil {
		t.Fatal("restart silently substituted workspace defaults")
	}
	restartedKernel := newKernelHostServices(nil, nil)
	restartedKernel.bindStateGeneration(restarted.pluginGeneration, restarted)
	defer restartedKernel.compositions.close()
	invoke = func(method, input string) (json.RawMessage, error) {
		return restartedKernel.compositions.InvokeService(context.Background(), pluginhost.ServiceInvokeParams{Caller: "controller", Method: method, Params: json.RawMessage(input)})
	}
	raw, err := invoke("inspectSession", `{"session":"product-main"}`)
	if err != nil {
		t.Fatal(err)
	}
	var inspection pluginhost.CompositionSessionView
	if err := json.Unmarshal(raw, &inspection); err != nil {
		t.Fatal(err)
	}
	if inspection.ID != shared.ID || inspection.State != "unavailable" || inspection.Composition == nil || inspection.Composition.Model.Model != "model-two" {
		t.Fatalf("restart diagnostics lost selection: %+v", inspection)
	}
	if _, err := invoke("bind", fmt.Sprintf(`{"session":"product-main","id":"","expected":%q}`, shared.ID)); err != nil {
		t.Fatal(err)
	}
}

// Child compositions cannot erase the owner's user-domain context boundary,
// including when their selected state provider imports the parent dataset.
func TestCompositionInheritsUserHostBoundary(t *testing.T) {
	cfg := config.Default()
	cfg.DefaultProvider = "test"
	cfg.Providers = map[string]config.ProviderConfig{"test": {Type: "openai-compatible", BaseURL: "http://127.0.0.1:9/v1", APIKey: "synthetic", Model: "model"}}
	cfg.Composition = &composition.Config{Version: composition.Version, Profile: "headless"}
	catalog := []pluginpkg.Plugin{}
	owner, err := NewSession(Options{RootDir: t.TempDir(), WuuHome: t.TempDir(), HomeDir: t.TempDir(), Config: cfg, DiscoveredPlugins: &catalog, NonInteractive: true, UserSpaceID: "alice", ProductDomain: "personal"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = owner.Cleanup() })
	kernel := newKernelHostServices(nil, nil)
	kernel.bindStateGeneration(owner.pluginGeneration, owner)
	scopes := kernel.compositions
	t.Cleanup(func() {
		if err := scopes.close(); err != nil {
			t.Error(err)
		}
	})
	for _, state := range []string{"isolated", "parent"} {
		raw, err := scopes.InvokeService(context.Background(), pluginhost.ServiceInvokeParams{Caller: "controller", Method: "create", Params: json.RawMessage(`{"inherit":true,"state":"` + state + `"}`)})
		if err != nil {
			t.Fatal(err)
		}
		var view pluginhost.CompositionView
		if err := json.Unmarshal(raw, &view); err != nil {
			t.Fatal(err)
		}
		child := scopes.scopes[view.ID].runtime
		if !owner.UserBinding().Matches(child.UserBinding()) {
			t.Fatal("composition discarded domain binding")
		}
		if len(child.InstructionFiles) != 0 || len(child.Skills) != 0 {
			t.Fatal("composition rediscovered ambient context")
		}
		if child.ProductDataRoot() == child.WuuHome {
			t.Fatal("composition discarded isolated product state root")
		}
	}
}
