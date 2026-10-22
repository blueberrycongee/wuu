package runtime

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/processsandbox"
	"github.com/blueberrycongee/wuu/internal/tools"
)

type securityServiceProvider struct {
	descriptors []pluginhost.ServiceDescriptor
	invocations []pluginhost.ServiceInvokeParams
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
		return json.RawMessage(`{"outcome":"deny","reason":"protected branch"}`), nil
	case pluginhost.ProcessSandboxService:
		return json.RawMessage(`{"argv":["/usr/bin/env","secure-runner"],"enforcement":"full","denial_signatures":["custom denied"],"runner_failure_signatures":["custom runner failed"]}`), nil
	default:
		return nil, nil
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
