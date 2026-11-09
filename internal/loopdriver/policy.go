package loopdriver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/blueberrycongee/wuu/internal/providers"
	"time"

	"github.com/blueberrycongee/wuu/internal/pluginprotocol"
)

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
	if (in.Phase == "retry" || in.Phase == "transport_retry") && out.Action != "fail" && (in.ToolWorkStarted || (in.PartialOutput && (in.Phase != "transport_retry" || !in.PartialOutputResettable))) {
		return fmt.Errorf("policy cannot replay partial output or accepted tool effects")
	}
	if in.Phase == "transport_retry" && out.Action == "recover" {
		if in.Failure == nil || in.Failure.Recovery == nil {
			return fmt.Errorf("transport recovery requires a submission receipt")
		}
	}
	return nil
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
		input := pluginprotocol.AgentPolicyInput{Phase: "transport_retry", ExecutionID: ExecutionContextFromContext(ctx).ExecutionID, Attempt: in.Attempt, MaxAttempts: in.MaxAttempts, InitialDelayMS: in.Config.InitialDelay.Milliseconds(), MaxDelayMS: in.Config.MaxDelay.Milliseconds(), RecoveryHint: hint, Failure: failure, PartialOutput: in.ForwardedEvents > 0, PartialOutputResettable: in.PartialOutputResettable, ToolWorkStarted: len(in.FinalizedToolCalls) > 0}
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
