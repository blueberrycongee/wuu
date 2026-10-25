package runtime

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/composition"
	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/permissionpolicy"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/processsandbox"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/tools"
	"github.com/blueberrycongee/wuu/internal/toolservice"
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
	clients := append(providers, toolservice.NewService(), runtimeServiceConsumers("")[0])
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
	for _, name := range []string{"no-host", "no-registry", "no-provider", "selected-provider-missing"} {
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
			case "selected-provider-missing":
				clients := []pluginhost.Client{permissionpolicy.NewService(permissionpolicy.Module), runtimeServiceConsumers("")[0]}
				registry, err := pluginhost.NewServiceRegistryWithBindings([]composition.Binding{{Service: pluginhost.SecurityAuthorizeService, Major: 1, Provider: "missing-policy"}})
				if err != nil {
					t.Fatal(err)
				}
				host = pluginhost.New(clients...)
				host.AttachServiceRegistry(registry, registry.RegisterClients(clients...))
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

// A process replacement must decide real file effects through the declared
// service route, and retirement must not select the available default policy.
func TestToolkitSecurityProcessProvider(t *testing.T) {
	if os.Getenv("WUU_PERMISSION_PROCESS_HELPER") == "1" {
		scanner := bufio.NewScanner(os.Stdin)
		encoder := json.NewEncoder(os.Stdout)
		for scanner.Scan() {
			var request struct {
				ID     string          `json:"id"`
				Method string          `json:"method"`
				Params json.RawMessage `json:"params"`
			}
			if err := json.Unmarshal(scanner.Bytes(), &request); err != nil {
				os.Exit(2)
			}
			var result any = struct{}{}
			switch request.Method {
			case "initialize":
				result = pluginhost.CapabilityInitializeResult{ProtocolVersion: pluginhost.CapabilityProtocolVersion, LifecycleVersion: pluginhost.RuntimeLifecycleVersion, ProvidedServices: []pluginhost.ServiceDescriptor{pluginhost.SecurityAuthorizeDescriptor()}}
			case "prepare", "activate", "shutdown":
			case pluginhost.ExecutionCancelMethod:
				continue
			case pluginhost.ServiceInvokeMethod:
				var call pluginhost.ServiceInvokeParams
				var input pluginhost.AuthorizationRequest
				if json.Unmarshal(request.Params, &call) != nil || json.Unmarshal(call.Params, &input) != nil || call.Caller != toolExecutionConsumer || call.ExecutionID == "" || call.Service != pluginhost.SecurityAuthorizeService || call.Method != pluginhost.SecurityAuthorizeMethod {
					os.Exit(3)
				}
				decision := pluginhost.AuthorizationDecision{Outcome: "allow"}
				if strings.Contains(input.Tool.Arguments, "blocked.txt") {
					decision = pluginhost.AuthorizationDecision{Outcome: "deny", Reason: "external protected file"}
				}
				result = decision
			default:
				os.Exit(4)
			}
			if encoder.Encode(map[string]any{"id": request.ID, "result": result}) != nil {
				os.Exit(5)
			}
			if request.Method == "shutdown" {
				return
			}
		}
		return
	}
	ctx := context.Background()
	t.Setenv("WUU_HOME", t.TempDir())
	root := t.TempDir()
	provider, err := pluginhost.Start(ctx, pluginhost.ProcessConfig{
		ID: "external-permissions", Command: os.Args[0], Args: []string{"-test.run=^TestToolkitSecurityProcessProvider$"},
		Env: map[string]string{"WUU_PERMISSION_PROCESS_HELPER": "1"}, PluginRoot: t.TempDir(), ProjectRoot: root,
		WuuHome: t.TempDir(), Timeout: 5 * time.Second, PrepareOnly: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = provider.Close(ctx) })
	clients := []pluginhost.Client{permissionpolicy.NewService(permissionpolicy.Module), provider, toolservice.NewService(), runtimeServiceConsumers("")[0]}
	registry, err := pluginhost.NewServiceRegistryWithBindings([]composition.Binding{{Service: pluginhost.SecurityAuthorizeService, Major: 1, Provider: provider.ID()}})
	if err != nil {
		t.Fatal(err)
	}
	host := pluginhost.New(clients...)
	conflicts := registry.RegisterClients(clients...)
	if len(conflicts) != 0 {
		t.Fatalf("conflicts = %+v", conflicts)
	}
	host.AttachServiceRegistry(registry, conflicts)
	t.Cleanup(func() { _ = host.Close(ctx) })
	if err := host.Activate(ctx); err != nil {
		t.Fatal(err)
	}
	kit, err := tools.New(root)
	if err != nil {
		t.Fatal(err)
	}
	configureToolkitSecurityExtensions(kit, host)
	_, err = kit.Execute(ctx, providers.ToolCall{Name: "write_file", Arguments: `{"path":"blocked.txt","content":"denied"}`})
	if err == nil || !strings.Contains(err.Error(), "external protected file") {
		t.Fatalf("external policy did not deny file mutation: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "blocked.txt")); !os.IsNotExist(err) {
		t.Fatalf("denied file exists: %v", err)
	}
	if _, err := kit.Execute(ctx, providers.ToolCall{Name: "write_file", Arguments: `{"path":"allowed.txt","content":"external authorization"}`}); err != nil {
		t.Fatal(err)
	}
	if content, err := os.ReadFile(filepath.Join(root, "allowed.txt")); err != nil || string(content) != "external authorization" {
		t.Fatalf("allowed file = %q, %v", content, err)
	}
	if outcome, retired := host.RetirePlugin(ctx, provider.ID(), nil); !retired || outcome.Err != nil {
		t.Fatalf("retire provider: %+v retired=%v", outcome, retired)
	}
	configureToolkitSecurityExtensions(kit, host)
	if _, err := kit.Execute(ctx, providers.ToolCall{Name: "write_file", Arguments: `{"path":"after-retirement.txt","content":"denied"}`}); err == nil || !strings.Contains(err.Error(), "authorization_denied") {
		t.Fatalf("retirement fell back to default: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "after-retirement.txt")); !os.IsNotExist(err) {
		t.Fatalf("retired provider admitted a file mutation: %v", err)
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
	old.revokeMissingPlugins(map[string]bool{oldProvider.ID(): true, toolExecutionConsumer: true, toolservice.DefaultModule: true}, nil, "")
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
	clients := []pluginhost.Client{provider, toolservice.NewService(), runtimeServiceConsumers("")[0]}
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
