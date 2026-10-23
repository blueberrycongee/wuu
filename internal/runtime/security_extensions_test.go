package runtime

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/permissionpolicy"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/processsandbox"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/tools"
)

type securityServiceProvider struct {
	descriptors []pluginhost.ServiceDescriptor
	invocations []pluginhost.ServiceInvokeParams
	decision    json.RawMessage
}

func (p *securityServiceProvider) ID() string { return "security-provider" }
func (p *securityServiceProvider) Status() pluginhost.Status {
	return pluginhost.Status{ID: p.ID(), State: pluginhost.StateActive}
}
func (p *securityServiceProvider) Close(context.Context) error { return nil }
func (p *securityServiceProvider) ProvidedServices() []pluginhost.ServiceDescriptor {
	return p.descriptors
}
func (p *securityServiceProvider) RequiredServices() []pluginhost.ServiceRequirement { return nil }
func (p *securityServiceProvider) InvokeService(_ context.Context, params pluginhost.ServiceInvokeParams) (json.RawMessage, error) {
	p.invocations = append(p.invocations, params)
	switch params.Service {
	case pluginhost.SecurityAuthorizeService:
		if p.decision != nil {
			return p.decision, nil
		}
		return json.RawMessage(`{"outcome":"deny","reason":"protected branch"}`), nil
	case pluginhost.ProcessSandboxService:
		return json.RawMessage(`{"argv":["/usr/bin/env","secure-runner"],"enforcement":"full","denial_signatures":["custom denied"],"runner_failure_signatures":["custom runner failed"]}`), nil
	default:
		return nil, nil
	}
}

func securityTestHost(t *testing.T, providers ...pluginhost.Client) *pluginhost.Host {
	t.Helper()
	clients := append(providers, runtimeServiceConsumers("")[0])
	host := pluginhost.New(clients...)
	registry, conflicts := pluginhost.BuildServiceRegistry(clients...)
	if len(conflicts) != 0 {
		t.Fatalf("conflicts = %+v", conflicts)
	}
	host.AttachServiceRegistry(registry, conflicts)
	if err := host.Activate(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = host.Close(context.Background()) })
	return host
}

// These calls must never reach a file tool when the selected authority is
// missing, retired, or replaced by a denying generation.
func TestToolkitSecurityMissingProviderDeniesExecution(t *testing.T) {
	t.Setenv("WUU_HOME", t.TempDir())
	for _, name := range []string{"no-host", "no-registry", "no-provider"} {
		t.Run(name, func(t *testing.T) {
			var host *pluginhost.Host
			switch name {
			case "no-registry":
				host = pluginhost.New()
			case "no-provider":
				consumer := runtimeServiceConsumers("")[0]
				host = pluginhost.New(consumer)
				registry, conflicts := pluginhost.BuildServiceRegistry(consumer)
				host.AttachServiceRegistry(registry, conflicts)
				_ = host.Activate(context.Background())
				t.Cleanup(func() { _ = host.Close(context.Background()) })
			}
			root := t.TempDir()
			kit, err := tools.New(root)
			if err != nil {
				t.Fatal(err)
			}
			configureToolkitSecurityExtensions(kit, host)
			_, err = kit.Execute(context.Background(), providers.ToolCall{Name: "write_file", Arguments: `{"path":"denied.txt","content":"must not execute"}`})
			if err == nil || !strings.Contains(err.Error(), "authorization_denied") {
				t.Fatalf("missing provider did not deny execution: %v", err)
			}
			if _, err := os.Stat(filepath.Join(root, "denied.txt")); !os.IsNotExist(err) {
				t.Fatalf("denied tool produced a file: %v", err)
			}
		})
	}
}

func TestToolkitSecurityPinsGenerationUntilRevocation(t *testing.T) {
	t.Setenv("WUU_HOME", t.TempDir())
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "source.txt"), []byte("generation-owned read"), 0o600); err != nil {
		t.Fatal(err)
	}
	kit, err := tools.New(root)
	if err != nil {
		t.Fatal(err)
	}
	oldProvider := &securityServiceProvider{descriptors: []pluginhost.ServiceDescriptor{pluginhost.SecurityAuthorizeDescriptor()}, decision: json.RawMessage(`{"outcome":"allow"}`)}
	oldHost := securityTestHost(t, oldProvider)
	old := &PluginGeneration{id: "old-policy", host: oldHost}
	configureToolkitSecurityExtensions(kit, oldHost)
	pinned, err := kit.CloneForRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	newProvider := &securityServiceProvider{descriptors: oldProvider.descriptors, decision: json.RawMessage(`{"outcome":"deny","reason":"new policy"}`)}
	configureToolkitSecurityExtensions(kit, securityTestHost(t, newProvider))
	old.revokeMissingPlugins(map[string]bool{oldProvider.ID(): true, toolExecutionConsumer: true}, nil, "")
	pinned.RefreshAuthorityFrom(kit)
	call := providers.ToolCall{Name: "read_file", Arguments: `{"path":"source.txt"}`}
	if text, err := pinned.Execute(context.Background(), call); err != nil || !strings.Contains(text, "generation-owned read") {
		t.Fatalf("pinned tool lost its original authorization: %s %v", text, err)
	}
	if _, err := kit.Execute(context.Background(), call); err == nil || !strings.Contains(err.Error(), "new policy") {
		t.Fatalf("new tool did not use replacement policy: %v", err)
	}
	old.revokeMissingPlugins(map[string]bool{toolExecutionConsumer: true}, nil, "")
	if _, err := pinned.Execute(context.Background(), call); err == nil || !strings.Contains(err.Error(), "authorization_denied") {
		t.Fatalf("disabled old provider still admitted tools: %v", err)
	}
	configureToolkitSecurityExtensions(pinned, oldHost)
	if _, err := pinned.Execute(context.Background(), call); err == nil || !strings.Contains(err.Error(), "authorization_denied") {
		t.Fatalf("rebinding a retired graph restored native allow: %v", err)
	}
	if len(oldProvider.invocations) != 1 || len(newProvider.invocations) != 1 {
		t.Fatalf("calls crossed generations: old=%d new=%d", len(oldProvider.invocations), len(newProvider.invocations))
	}
}

func TestToolkitSecurityDefaultProviderOwnsReadOnlyDecision(t *testing.T) {
	t.Setenv("WUU_HOME", t.TempDir())
	kit, err := tools.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	configureToolkitSecurityExtensions(kit, securityTestHost(t, permissionpolicy.NewService(permissionpolicy.Module)))
	ConfigureToolkitPermissions(kit, config.ResolvedPermissions{Mode: config.PermissionModeReadOnly})
	_, err = kit.Execute(context.Background(), providers.ToolCall{Name: "write_file", Arguments: `{"path":"denied.txt","content":"must not execute"}`})
	if err == nil || !strings.Contains(err.Error(), "authorization_denied") || !strings.Contains(err.Error(), "read-only") {
		t.Fatalf("default provider did not make the read-only decision: %v", err)
	}
}

func TestPluginSecurityServicesAreRealCoreConsumers(t *testing.T) {
	provider := &securityServiceProvider{descriptors: []pluginhost.ServiceDescriptor{
		pluginhost.SecurityAuthorizeDescriptor(), pluginhost.ProcessSandboxDescriptor(),
	}}
	clients := append([]pluginhost.Client{provider}, runtimeServiceConsumers("")...)
	host := pluginhost.New(clients...)
	registry, conflicts := pluginhost.BuildServiceRegistry(clients...)
	host.AttachServiceRegistry(registry, conflicts)
	if len(conflicts) != 0 {
		t.Fatalf("conflicts = %+v", conflicts)
	}
	if err := host.Activate(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer host.Close(context.Background())

	authorizer := &pluginAuthorizer{host: host}
	decision, err := authorizer.Authorize(context.Background(), tools.AuthorizationRequest{
		SessionID: "thread-1", CWD: "/workspace", PermissionMode: "standard",
		Tool: tools.ToolInfo{Name: "bash", Kind: tools.ToolKindShell}, Arguments: `{"command":"git push"}`,
	})
	if err != nil || decision.Outcome != "deny" || decision.Reason != "protected branch" {
		t.Fatalf("decision = %+v err = %v", decision, err)
	}

	sandbox := &pluginSandboxProvider{host: host}
	confined, err := sandbox.Confine(context.Background(), []string{"/bin/echo", "hello"}, processsandbox.Policy{Mode: processsandbox.ModeReadOnly})
	if err != nil || confined.Enforcement != processsandbox.EnforcementFull || len(confined.Argv) != 2 || len(confined.DenialSignatures) != 1 || len(confined.RunnerFailureSignatures) != 1 {
		t.Fatalf("confined = %+v err = %v", confined, err)
	}
	if len(provider.invocations) != 2 || provider.invocations[0].Caller != toolExecutionConsumer || provider.invocations[1].ExecutionID == "" {
		t.Fatalf("invocations = %+v", provider.invocations)
	}
}
