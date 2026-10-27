package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/processsandbox"
	"github.com/blueberrycongee/wuu/internal/tools"
)

type pluginAuthorizer struct {
	host *pluginhost.Host
}

func (a *pluginAuthorizer) Authorize(ctx context.Context, request tools.AuthorizationRequest) (tools.AuthorizationDecision, error) {
	if a == nil || a.host == nil {
		return tools.AuthorizationDecision{}, errors.New("authorization provider is unavailable")
	}
	input := pluginhost.AuthorizationRequest{
		SessionID: request.SessionID, ActorID: request.ActorID, CWD: request.CWD, PermissionMode: request.PermissionMode,
		Tool: pluginhost.AuthorizationTool{
			Name: request.Tool.Name, Kind: string(request.Tool.Kind), Arguments: request.Arguments,
			ReadOnly: request.Tool.ReadOnly, ConcurrencySafe: request.Tool.ConcurrencySafe,
			Destructive: request.Tool.Destructive, Risk: string(request.Tool.Risk), Reason: request.Tool.Reason,
		},
	}
	raw, err := json.Marshal(input)
	if err != nil {
		return tools.AuthorizationDecision{}, err
	}
	result, callErr := a.host.CallService(ctx, toolExecutionConsumer, pluginhost.SecurityAuthorizeService, pluginhost.SecurityAuthorizeMethod, raw)
	if callErr != nil {
		return tools.AuthorizationDecision{}, callErr
	}
	var decision pluginhost.AuthorizationDecision
	if err := json.Unmarshal(result, &decision); err != nil {
		return tools.AuthorizationDecision{}, fmt.Errorf("decode authorization decision: %w", err)
	}
	decision.Outcome = strings.TrimSpace(decision.Outcome)
	if decision.Outcome != "allow" && decision.Outcome != "deny" {
		return tools.AuthorizationDecision{}, fmt.Errorf("authorization provider returned unknown outcome %q", decision.Outcome)
	}
	return tools.AuthorizationDecision{Outcome: decision.Outcome, Reason: decision.Reason}, nil
}

type pluginSandboxProvider struct {
	host *pluginhost.Host
}

func (p *pluginSandboxProvider) Confine(ctx context.Context, argv []string, policy processsandbox.Policy) (processsandbox.ConfinedCommand, error) {
	if p == nil || p.host == nil {
		return processsandbox.ConfinedCommand{}, errors.New("sandbox provider is unavailable")
	}
	input := pluginhost.ProcessSandboxRequest{
		Argv:   append([]string(nil), argv...),
		Policy: pluginhost.ProcessSandboxPolicy{Mode: string(policy.Mode), WritableRoots: append([]string(nil), policy.WritableRoots...)},
	}
	raw, err := json.Marshal(input)
	if err != nil {
		return processsandbox.ConfinedCommand{}, err
	}
	result, callErr := p.host.CallService(ctx, toolExecutionConsumer, pluginhost.ProcessSandboxService, pluginhost.ProcessSandboxMethod, raw)
	if callErr != nil {
		return processsandbox.ConfinedCommand{}, callErr
	}
	var confined pluginhost.ProcessSandboxResult
	if err := json.Unmarshal(result, &confined); err != nil {
		return processsandbox.ConfinedCommand{}, fmt.Errorf("decode sandbox result: %w", err)
	}
	return processsandbox.ConfinedCommand{
		Argv: confined.Argv, Enforcement: processsandbox.Enforcement(confined.Enforcement),
		DenialSignatures: confined.DenialSignatures, RunnerFailureSignatures: confined.RunnerFailureSignatures,
	}, nil
}

func configureToolkitSecurityExtensions(kit *tools.Toolkit, host *pluginhost.Host) {
	if kit == nil {
		return
	}
	// Capture this generation even when its selected authority is unavailable.
	// Clearing the binding would turn missing or retired policy into local allow.
	kit.SetAuthorizer(&pluginAuthorizer{host: host})
	kit.SetProcessSandboxProvider(nil)
	if host == nil || host.ServiceRegistry() == nil {
		return
	}
	registry := host.ServiceRegistry()
	if registry.HasProviderFor(toolExecutionConsumer, pluginhost.ProcessSandboxService, pluginhost.SecurityServiceMajor) {
		kit.SetProcessSandboxProvider(&pluginSandboxProvider{host: host})
	}
}
