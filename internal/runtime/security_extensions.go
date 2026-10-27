package runtime

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

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
	input := authorizationInput(request)
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
		Policy: pluginhost.ProcessSandboxPolicy{ProtectedPaths: append([]string(nil), policy.ProtectedPaths...), Mode: string(policy.Mode), WritableRoots: append([]string(nil), policy.WritableRoots...)},
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
		ProtectedPathsEnforced: confined.ProtectedPathsEnforced,
		Argv:                   confined.Argv, Enforcement: processsandbox.Enforcement(confined.Enforcement),
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

func authorizationInput(request tools.AuthorizationRequest) pluginhost.AuthorizationRequest {
	return pluginhost.AuthorizationRequest{
		SessionID: request.SessionID, ActorID: request.ActorID, CWD: request.CWD, PermissionMode: request.PermissionMode,
		Tool: pluginhost.AuthorizationTool{
			Name: request.Tool.Name, Kind: string(request.Tool.Kind), Arguments: request.Arguments,
			ReadOnly: request.Tool.ReadOnly, ConcurrencySafe: request.Tool.ConcurrencySafe,
			Destructive: request.Tool.Destructive, Risk: string(request.Tool.Risk), Reason: request.Tool.Reason,
		},
	}
}

// WithAuthorization keeps the selected provider's exact decision and execution
// participation alive through the effect and its physical cleanup.
func (a *pluginAuthorizer) WithAuthorization(ctx context.Context, request tools.AuthorizationRequest, use func(context.Context) error) (err error) {
	if !a.SupportsAtomicAuthorization() || use == nil {
		return errors.New("authorization provider is unavailable")
	}
	scope, err := a.host.OpenServiceScope(ctx, toolExecutionConsumer, pluginhost.SecurityAuthorizeService)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, scope.Close()) }()
	var nonce [32]byte
	if _, err = rand.Read(nonce[:]); err != nil {
		return err
	}
	id := hex.EncodeToString(nonce[:])
	// Release even an unknown acquisition outcome. Providers tombstone the ID so
	// a late response/retry cannot create a detached authorized effect.
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(scope.Context), 5*time.Second)
		defer cancel()
		raw, _ := json.Marshal(pluginhost.AuthorizationReleaseRequest{ID: id})
		_, releaseErr := scope.CallCleanup(cleanup, pluginhost.SecurityAuthorizeService, "release-authorization", raw)
		err = errors.Join(err, releaseErr)
	}()
	raw, err := json.Marshal(pluginhost.AuthorizationAcquireRequest{ID: id, Request: authorizationInput(request)})
	if err != nil {
		return err
	}
	result, callErr := a.host.CallService(scope.Context, toolExecutionConsumer, pluginhost.SecurityAuthorizeService, "acquire-authorization", raw)
	if callErr != nil {
		return callErr
	}
	var lease pluginhost.AuthorizationLease
	if err = json.Unmarshal(result, &lease); err != nil {
		return err
	}
	if lease.ID != id || lease.Decision.Outcome != "allow" {
		return fmt.Errorf("authorization_denied: %s", lease.Decision.Reason)
	}
	if err = scope.Context.Err(); err != nil {
		return err
	}
	return use(scope.Context)
}

func (a *pluginAuthorizer) SupportsAtomicAuthorization() bool {
	if a == nil || a.host == nil || a.host.ServiceRegistry() == nil {
		return false
	}
	descriptor, ok := a.host.ServiceRegistry().ServiceDescriptorFor(toolExecutionConsumer, pluginhost.SecurityAuthorizeService, 1)
	if !ok {
		return false
	}
	acquire, release := false, false
	for _, m := range descriptor.Methods {
		if m.InputSchema != "security.authorize."+m.Name+".input.v1" || m.OutputSchema != "security.authorize."+m.Name+".output.v1" {
			continue
		}
		if m.Name == "acquire-authorization" {
			acquire = true
		}
		if m.Name == "release-authorization" && m.Cleanup {
			release = true
		}
	}
	return acquire && release
}
