package runtime

import (
	"context"
	"errors"
	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/providers"
	"testing"
	"time"
)

func TestCapacityResourcesSharedOwnerAndCancellation(t *testing.T) {
	owner := NewCapacityResources(1)
	first, err := owner.acquire(context.Background(), "account")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if release, err := owner.acquire(ctx, "account"); err == nil {
		release()
		t.Fatal("shared owner multiplied capacity")
	}
	independent := NewCapacityResources(1)
	release, err := independent.acquire(context.Background(), "account")
	if err != nil {
		t.Fatal(err)
	}
	release()
	first()
	first()
	release, err = owner.acquire(context.Background(), "account")
	if err != nil {
		t.Fatal(err)
	}
	release()
}

func capacityTestHost(t *testing.T, policy, physical *CapacityResources) *pluginhost.Host {
	t.Helper()
	policyClient, err := capacityProviderFactory(policy, false).Build(NativeProviderContext{})
	if err != nil {
		t.Fatal(err)
	}
	physicalClient, err := capacityProviderFactory(physical, true).Build(NativeProviderContext{})
	if err != nil {
		t.Fatal(err)
	}
	consumer := &runtimeServiceConsumer{id: modelExecutionConsumer, state: pluginhost.StatePrepared, requirements: []pluginhost.ServiceRequirement{{Name: capacityPolicyService, MajorVersion: 1, Required: true}, {Name: capacityResourceService, MajorVersion: 1, Required: true}}}
	clients := []pluginhost.Client{policyClient, physicalClient, consumer}
	host := pluginhost.New(clients...)
	registry, conflicts := pluginhost.BuildServiceRegistry(clients...)
	host.AttachServiceRegistry(registry, conflicts)
	if err := host.Activate(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = host.Close(ctx)
	})
	return host
}

func TestCapacityRetirementPreservesPhysicalTailAcrossPolicyReplacement(t *testing.T) {
	shared := NewCapacityResources(1)
	old := capacityTestHost(t, shared, shared)
	next := capacityTestHost(t, NewCapacityResources(1), shared)
	scope, err := old.OpenServiceScope(context.Background(), modelExecutionConsumer, capacityPolicyService, capacityResourceService)
	if err != nil {
		t.Fatal(err)
	}
	admission, err := acquireModelCapacity(scope.Context, old, "account", providers.InferenceAttempt{}, "provider:route")
	if err != nil {
		t.Fatal(err)
	}
	retired := make(chan error, 1)
	go func() {
		outcome, _ := old.RetirePlugin(context.Background(), capacityPolicyModule, errors.New("disabled"))
		retired <- outcome.Err
	}()
	select {
	case <-scope.Context.Done():
	case <-time.After(time.Second):
		t.Fatal("retirement did not cancel physical callback")
	}
	select {
	case err := <-retired:
		t.Fatalf("retired before transport tail: %v", err)
	default:
	}
	queued, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	if lease, err := acquireModelCapacity(queued, next, "account", providers.InferenceAttempt{}, "provider:route"); err == nil {
		_ = lease.settle("abandoned", nil)
		t.Fatal("policy replacement multiplied physical quota")
	}
	cancel()
	if err := admission.settle("succeeded", nil); err != nil {
		t.Fatal(err)
	}
	if err := scope.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-retired:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("retirement did not finish after physical settlement")
	}
	lease, err := acquireModelCapacity(context.Background(), next, "account", providers.InferenceAttempt{}, "provider:route")
	if err != nil {
		t.Fatal(err)
	}
	if err := lease.settle("succeeded", nil); err != nil {
		t.Fatal(err)
	}
}

func TestCapacityRetirementDeadlineDoesNotAcknowledgeIgnoredCancellation(t *testing.T) {
	owner := NewCapacityResources(1)
	host := capacityTestHost(t, owner, owner)
	replacement := capacityTestHost(t, NewCapacityResources(1), owner)
	scope, err := host.OpenServiceScope(context.Background(), modelExecutionConsumer, capacityPolicyService, capacityResourceService)
	if err != nil {
		t.Fatal(err)
	}
	admission, err := acquireModelCapacity(scope.Context, host, "account", providers.InferenceAttempt{})
	if err != nil {
		t.Fatal(err)
	}
	deadline, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	outcome, found := host.RetirePlugin(deadline, capacityPolicyModule, errors.New("disable"))
	cancel()
	if !found || !errors.Is(outcome.Err, context.DeadlineExceeded) {
		t.Fatalf("untruthful retirement: %+v %v", outcome, found)
	}
	// A transport ignoring cancellation is still physically admitted.
	blocked, stop := context.WithTimeout(context.Background(), 20*time.Millisecond)
	if lease, err := acquireModelCapacity(blocked, replacement, "account", providers.InferenceAttempt{}); err == nil {
		_ = lease.settle("abandoned", nil)
		t.Fatal("deadline released physical slot")
	}
	stop()
	if err := admission.settle("abandoned", nil); err != nil {
		t.Fatal(err)
	}
	if err := scope.Close(); err != nil {
		t.Fatal(err)
	}
	lease, err := acquireModelCapacity(context.Background(), replacement, "account", providers.InferenceAttempt{})
	if err != nil {
		t.Fatal(err)
	}
	if err := lease.settle("succeeded", nil); err != nil {
		t.Fatal(err)
	}
}

func TestCapacityOwnerSurvivesSessionGenerationPublication(t *testing.T) {
	shared := NewCapacityResources(1)
	cfg := config.Config{DefaultProvider: "test", Providers: map[string]config.ProviderConfig{"test": {Type: "openai-compatible", BaseURL: "http://127.0.0.1:1", APIKey: "fixture", Model: "fixture"}}}
	makeSession := func(resources *CapacityResources) *Session {
		owner, err := NewSession(Options{RootDir: t.TempDir(), HomeDir: t.TempDir(), Config: cfg, NoTools: true, NonInteractive: true, CapacityResources: resources})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _, _ = owner.Cleanup() })
		return owner
	}
	owner, peer := makeSession(shared), makeSession(shared)
	captured, err := owner.RetainState()
	if err != nil {
		t.Fatal(err)
	}
	defer captured.Close()
	scope, err := captured.Host.OpenServiceScope(context.Background(), modelExecutionConsumer, capacityPolicyService, capacityResourceService)
	if err != nil {
		t.Fatal(err)
	}
	first, err := acquireModelCapacity(scope.Context, captured.Host, "account", providers.InferenceAttempt{})
	if err != nil {
		t.Fatal(err)
	}
	candidate, err := owner.PreflightExtensions(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := owner.ActivatePluginGeneration(candidate, nil); err != nil {
		t.Fatal(err)
	}
	for _, host := range []*pluginhost.Host{owner.PluginHost, peer.PluginHost} {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		if lease, err := acquireModelCapacity(ctx, host, "account", providers.InferenceAttempt{}); err == nil {
			_ = lease.settle("abandoned", nil)
			t.Error("session/generation multiplied shared owner")
		}
		cancel()
	}
	if err := first.settle("succeeded", nil); err != nil {
		t.Fatal(err)
	}
	if err := scope.Close(); err != nil {
		t.Fatal(err)
	}
	sharedLease, err := acquireModelCapacity(context.Background(), owner.PluginHost, "account", providers.InferenceAttempt{})
	if err != nil {
		t.Fatal(err)
	}
	isolated := makeSession(NewCapacityResources(1))
	independent, err := acquireModelCapacity(context.Background(), isolated.PluginHost, "account", providers.InferenceAttempt{})
	if err != nil {
		t.Fatal(err)
	}
	if err := independent.settle("succeeded", nil); err != nil {
		t.Fatal(err)
	}
	if err := sharedLease.settle("succeeded", nil); err != nil {
		t.Fatal(err)
	}

}

func TestCapacityOwnerFinalDisposalAndExplicitLifetime(t *testing.T) {
	implicit := NewCapacityResources(1)
	implicit.autoClose = true
	first := capacityTestHost(t, implicit, implicit)
	second := capacityTestHost(t, implicit, implicit)
	if err := first.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-implicit.done:
		t.Fatal("first generation closed shared owner")
	default:
	}
	if err := second.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-implicit.done:
	case <-time.After(time.Second):
		t.Fatal("implicit owner not disposed after final graph")
	}
	if _, err := implicit.policyV1.Acquire(context.Background(), "account", providers.ProviderPriorityInteractive); err == nil {
		t.Fatal("disposed policy still admits")
	}
	explicit := NewCapacityResources(1)
	host := capacityTestHost(t, explicit, explicit)
	deadline, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	if err := explicit.Close(deadline); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("owner acknowledged live graph: %v", err)
	}
	cancel()
	if err := host.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := explicit.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestCapacityPreparedCandidateRetainsOwnerBeforeActivation(t *testing.T) {
	owner := NewCapacityResources(1)
	owner.autoClose = true
	old := capacityTestHost(t, owner, owner)
	candidate, err := capacityProviderFactory(owner, true).Build(NativeProviderContext{})
	if err != nil {
		t.Fatal(err)
	}
	prepared := candidate.(*capacityProvider)
	if err := prepared.Prepare(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := old.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-owner.done:
		t.Fatal("prepared candidate lost owner")
	default:
	}
	if err := prepared.Activate(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := prepared.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-owner.done:
	case <-time.After(time.Second):
		t.Fatal("final prepared owner not disposed")
	}
}

func TestCapacitySessionCanRepairAfterAllNativeCapacityModulesDisabled(t *testing.T) {
	cfg := config.Config{DefaultProvider: "test", Providers: map[string]config.ProviderConfig{"test": {Type: "openai-compatible", BaseURL: "http://127.0.0.1:1", APIKey: "fixture", Model: "fixture"}}}
	session, err := NewSession(Options{RootDir: t.TempDir(), HomeDir: t.TempDir(), Config: cfg, NoTools: true, NonInteractive: true})
	if err != nil {
		t.Fatal(err)
	}
	defer session.Cleanup()
	var owner *CapacityResources
	for _, factory := range session.nativeProviders {
		if factory.capacityOwner != nil {
			owner = factory.capacityOwner
			break
		}
	}
	if owner == nil {
		t.Fatal("session did not retain explicit capacity factory owner")
	}
	for _, module := range []string{capacityPolicyModule, capacityResourceModule} {
		outcome, found := session.PluginHost.RetirePlugin(context.Background(), module, errors.New("disabled"))
		if !found || outcome.Err != nil {
			t.Fatalf("disable %s: %+v %v", module, outcome, found)
		}
	}
	select {
	case <-owner.done:
		t.Fatal("live session lost repair authority")
	default:
	}
	candidate, err := session.PreflightExtensions(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := session.ActivatePluginGeneration(candidate, nil); err != nil {
		t.Fatal(err)
	}
	lease, err := acquireModelCapacity(context.Background(), session.PluginHost, "account", providers.InferenceAttempt{})
	if err != nil {
		t.Fatal(err)
	}
	if err := lease.settle("succeeded", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Cleanup(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-owner.done:
	case <-time.After(time.Second):
		t.Fatal("session root owner leaked after final cleanup")
	}
}
