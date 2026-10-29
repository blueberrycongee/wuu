// Package defaultpolicy implements the shipped agent policy. The contract and
// its consumers live in loopdriver and do not depend on this implementation.
// An unselected provider is never constructed or invoked.
package defaultpolicy

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/blueberrycongee/wuu/internal/loopdriver"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/pluginprotocol"
	"github.com/blueberrycongee/wuu/internal/providers"
)

const Module = "wuu.agent.policy.default"
const defaultToolConcurrency = 10

// Service is a regular selected provider, not a fallback. It owns
// the product's conservative retry, continuation, and safe parallel scheduling.
type Service struct {
	mu    sync.Mutex
	state pluginhost.State
}

func NewService() *Service {
	return &Service{state: pluginhost.StatePrepared}
}
func (s *Service) ID() string { return Module }
func (s *Service) Status() pluginhost.Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	return pluginhost.Status{ID: s.ID(), State: s.state}
}
func (s *Service) ProvidedServices() []pluginhost.ServiceDescriptor {
	return []pluginhost.ServiceDescriptor{{Name: pluginprotocol.AgentPolicyService, Version: "1.0.0", Methods: []pluginhost.ServiceMethodDescriptor{{Name: "evaluate", InputSchema: "agent.policy.input.v1", OutputSchema: "agent.policy.decision.v1"}}}}
}
func (s *Service) RequiredServices() []pluginhost.ServiceRequirement { return nil }
func (s *Service) Prepare(ctx context.Context) error                 { return ctx.Err() }
func (s *Service) Activate(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state = pluginhost.StateActive
	return nil
}
func (s *Service) Close(context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state = pluginhost.StateStopped
	return nil
}
func (s *Service) InvokeService(ctx context.Context, p pluginhost.ServiceInvokeParams) (json.RawMessage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s.Status().State != pluginhost.StateActive {
		return nil, fmt.Errorf("agent policy provider is unavailable")
	}
	if p.Service != pluginprotocol.AgentPolicyService || p.Method != "evaluate" {
		return nil, fmt.Errorf("unknown agent policy operation")
	}
	var in pluginprotocol.AgentPolicyInput
	if err := json.Unmarshal(p.Params, &in); err != nil {
		return nil, err
	}
	out := pluginprotocol.AgentPolicyDecision{}
	switch in.Phase {
	case "transport_retry":
		out.Action = "fail"
		if !in.PartialOutput && !in.ToolWorkStarted && in.Failure != nil && in.Failure.Recovery != nil {
			failure := (&loopdriver.FailureError{Failure: *in.Failure}).FailureEvidence()
			if in.RecoveryHint == string(providers.RecoveryRefreshAuth) {
				failure.Cause = providers.MarkAuthRefreshable(failure.Cause)
			}
			plan, delay := providers.DefaultStreamRecoveryPlan(providers.StreamRecoveryInput{Failure: failure, Attempt: in.Attempt, Config: providers.RetryConfig{InitialDelay: time.Duration(in.InitialDelayMS) * time.Millisecond, MaxDelay: time.Duration(in.MaxDelayMS) * time.Millisecond}})
			if plan.Retryable() {
				out.Action = "recover"
				out.RecoveryAction = string(plan.Action)
				out.DelayMS = int(delay.Milliseconds())
			}
		}
	case "checkpoint_resume":
		out.Action = "resume"
		if loopdriver.ValidatePolicyDecision(in, out) != nil {
			out.Action = "fail"
		}
	case "retry":
		out.Action = "fail"
		if !in.PartialOutput && !in.ToolWorkStarted && in.Failure != nil && in.Failure.ContextOverflow {
			out.Action = "recover"
		}
	case "continue":
		out.Action = "stop"
		if in.FinishReason == "continue" && !in.Truncated {
			out.Action = "continue"
		}
	case "schedule":
		out.Action = "parallel_safe"
		out.MaxParallel = defaultToolConcurrency
	default:
		return nil, fmt.Errorf("unknown agent policy phase %q", in.Phase)
	}
	return json.Marshal(out)
}
