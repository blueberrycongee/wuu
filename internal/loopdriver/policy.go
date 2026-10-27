package loopdriver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/blueberrycongee/wuu/internal/providers"
	"sync"
	"time"

	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/pluginprotocol"
)

const NativePolicyModule = "wuu.agent.policy.default"
const DefaultToolConcurrency = 10

type PolicyEvaluator func(context.Context, pluginprotocol.AgentPolicyInput) (pluginprotocol.AgentPolicyDecision, error)

// PolicyClient captures a generation-bound route; failures never select another
// provider or invoke native policy. Validation applies equally to both origins.
type PolicyClient struct {
	call func(context.Context, json.RawMessage) (json.RawMessage, error)
}

func NewPolicyClient(call func(context.Context, json.RawMessage) (json.RawMessage, error)) *PolicyClient {
	return &PolicyClient{call: call}
}
func (c *PolicyClient) Evaluate(ctx context.Context, in pluginprotocol.AgentPolicyInput) (pluginprotocol.AgentPolicyDecision, error) {
	var out pluginprotocol.AgentPolicyDecision
	if err := ctx.Err(); err != nil {
		return out, err
	}
	raw, err := json.Marshal(in)
	if err != nil {
		return out, err
	}
	raw, err = c.call(ctx, raw)
	if err != nil {
		return out, err
	}
	if err = json.Unmarshal(raw, &out); err != nil {
		return out, err
	}
	if err = ctx.Err(); err != nil {
		return out, err
	}
	return out, ValidatePolicyDecision(in, out)
}
func ValidatePolicyDecision(in pluginprotocol.AgentPolicyInput, out pluginprotocol.AgentPolicyDecision) error {
	valid := false
	switch in.Phase {
	case "transport_retry":
		valid = out.Action == "fail" || out.Action == "recover"
		if out.Action == "recover" {
			switch out.RecoveryAction {
			case "replay_same_payload", "wait_then_replay", "refresh_credential", "switch_transport":
			default:
				valid = false
			}
		}
	case "checkpoint_resume":
		valid = out.Action == "fail" || out.Action == "resume"
		if out.Action == "resume" && (in.CompletedMessages < 1 || in.ToolWorkStarted || len(in.ToolCalls) > 0 || in.Failure == nil || in.Failure.Category != "replay_unsafe" || in.Failure.Recovery == nil || in.Failure.Recovery.StopReason != "replay_unsafe" || in.MaxAttempts < 2 || in.Attempt >= in.MaxAttempts-1) {
			return errors.New("checkpoint resume requires completed progress and an unexhausted receipt budget without tool effects")
		}
	case "retry":
		valid = out.Action == "fail" || out.Action == "recover" || out.Action == "retry"
	case "continue":
		valid = out.Action == "stop" || out.Action == "continue"
	case "schedule":
		valid = out.Action == "serial" || out.Action == "parallel_safe"
	}
	if in.Phase == "schedule" && out.Action == "parallel_safe" && (out.MaxParallel < 1 || out.MaxParallel > 64) {
		return fmt.Errorf("parallel schedule requires a capacity between 1 and 64")
	}
	if !valid {
		return fmt.Errorf("invalid %s policy action %q", in.Phase, out.Action)
	}
	if out.DelayMS < 0 || out.DelayMS > 60000 || (out.DelayMS != 0 && out.Action != "retry" && in.Phase != "transport_retry") {
		return fmt.Errorf("invalid policy retry delay")
	}
	if out.Action == "retry" && out.MaxAttempts < 1 {
		return fmt.Errorf("retry policy requires a positive max_attempts budget")
	}
	if in.Phase == "retry" && out.Action == "retry" && in.Failure != nil && in.Failure.Recovery != nil {
		recovery := in.Failure.Recovery
		if recovery.StopReason == "retry_limit" || recovery.StopReason == "workflow_budget_exceeded" || recovery.BudgetDimension != "" {
			return fmt.Errorf("policy cannot renew an exhausted inference budget")
		}
	}
	if out.Action == "retry" && (in.Failure == nil || in.Failure.Recovery == nil || in.Failure.Recovery.SubmissionCount != 0) {
		return fmt.Errorf("policy cannot retry an unknown or submitted model operation")
	}
	if (in.Phase == "retry" || in.Phase == "transport_retry") && out.Action != "fail" && (in.PartialOutput || in.ToolWorkStarted) {
		return fmt.Errorf("policy cannot replay partial output or accepted tool effects")
	}
	if in.Phase == "transport_retry" && out.Action == "recover" {
		if in.Failure == nil || in.Failure.Recovery == nil {
			return fmt.Errorf("transport recovery requires a submission receipt")
		}
	}
	return nil
}

// NativePolicyService is a regular selected provider, not a fallback. It owns
// the product's conservative retry, continuation, and safe parallel scheduling.
type NativePolicyService struct {
	mu    sync.Mutex
	state pluginhost.State
}

func NewNativePolicyService() *NativePolicyService {
	return &NativePolicyService{state: pluginhost.StatePrepared}
}
func (s *NativePolicyService) ID() string { return NativePolicyModule }
func (s *NativePolicyService) Status() pluginhost.Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	return pluginhost.Status{ID: s.ID(), State: s.state}
}
func (s *NativePolicyService) ProvidedServices() []pluginhost.ServiceDescriptor {
	return []pluginhost.ServiceDescriptor{{Name: pluginprotocol.AgentPolicyService, Version: "1.0.0", Methods: []pluginhost.ServiceMethodDescriptor{{Name: "evaluate", InputSchema: "agent.policy.input.v1", OutputSchema: "agent.policy.decision.v1"}}}}
}
func (s *NativePolicyService) RequiredServices() []pluginhost.ServiceRequirement { return nil }
func (s *NativePolicyService) Prepare(ctx context.Context) error                 { return ctx.Err() }
func (s *NativePolicyService) Activate(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state = pluginhost.StateActive
	return nil
}
func (s *NativePolicyService) Close(context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state = pluginhost.StateStopped
	return nil
}
func (s *NativePolicyService) InvokeService(ctx context.Context, p pluginhost.ServiceInvokeParams) (json.RawMessage, error) {
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
			failure := (&FailureError{Failure: *in.Failure}).FailureEvidence()
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
		if ValidatePolicyDecision(in, out) != nil {
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
		out.MaxParallel = DefaultToolConcurrency
	default:
		return nil, fmt.Errorf("unknown agent policy phase %q", in.Phase)
	}
	return json.Marshal(out)
}

// StreamRecoveryPlanner uses this same captured service binding for model transport recovery.
func (c *PolicyClient) StreamRecoveryPlanner() providers.StreamRecoveryPlanner {
	if c == nil {
		return nil
	}
	return func(ctx context.Context, in providers.StreamRecoveryInput) (providers.RecoveryPlan, time.Duration, error) {
		failure := FailureToWire(in.Failure.Cause)
		if failure == nil {
			return providers.RecoveryPlan{}, 0, errors.New("transport policy requires failure evidence")
		}
		failure.Recovery = &pluginprotocol.DriverModelRecovery{AttemptCount: in.Attempt, MaxAttempts: in.MaxAttempts, SubmissionCount: in.Submissions}
		hint := ""
		var hinted providers.InferenceRecoveryHint
		if errors.As(in.Failure.Cause, &hinted) {
			hint = string(hinted.InferenceRecoveryAction())
		}
		input := pluginprotocol.AgentPolicyInput{Phase: "transport_retry", ExecutionID: ExecutionContextFromContext(ctx).ExecutionID, Attempt: in.Attempt, MaxAttempts: in.MaxAttempts, InitialDelayMS: in.Config.InitialDelay.Milliseconds(), MaxDelayMS: in.Config.MaxDelay.Milliseconds(), RecoveryHint: hint, Failure: failure, PartialOutput: in.ForwardedEvents > 0, ToolWorkStarted: len(in.FinalizedToolCalls) > 0}
		decision, err := c.Evaluate(ctx, input)
		if err != nil {
			return providers.RecoveryPlan{}, 0, err
		}
		action := providers.RecoveryStop
		if decision.Action == "recover" {
			action = providers.RecoveryActionKind(decision.RecoveryAction)
		}
		return providers.RecoveryPlan{Action: action, Reason: decision.Reason}, time.Duration(decision.DelayMS) * time.Millisecond, nil
	}
}
