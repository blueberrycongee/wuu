package agent

import (
	"context"
	"errors"

	"github.com/blueberrycongee/wuu/internal/loopdriver"
	"github.com/blueberrycongee/wuu/internal/pluginprotocol"
)

func (g *streamTurnGateway) EvaluatePolicy(ctx context.Context, in pluginprotocol.AgentPolicyInput) (pluginprotocol.AgentPolicyDecision, error) {
	ctx, leave, err := g.enter(ctx)
	if err != nil {
		return pluginprotocol.AgentPolicyDecision{}, err
	}
	defer leave()
	if err := g.prepare(ctx); err != nil {
		return pluginprotocol.AgentPolicyDecision{}, err
	}
	if g.runner.SelectedPolicy == nil {
		return pluginprotocol.AgentPolicyDecision{}, errors.New("agent policy provider is unavailable")
	}
	in.ExecutionID = loopdriver.ExecutionContextFromContext(ctx).ExecutionID
	in.StepIndex = g.step
	return g.runner.SelectedPolicy.Evaluate(ctx, in)
}
