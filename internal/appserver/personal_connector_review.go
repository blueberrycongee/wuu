package appserver

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/blueberrycongee/wuu/internal/approvefor"
	"github.com/blueberrycongee/wuu/internal/clientauthority"
	"github.com/blueberrycongee/wuu/internal/personalconnectors"
	"github.com/blueberrycongee/wuu/internal/personalreview"
)

const connectorReviewVersion = "personal-account-action.v1"

func (s *Server) connectorReview(ctx context.Context, actor personalconnectors.Actor, action personalconnectors.Result) (personalconnectors.ReviewDecision, error) {
	raw, err := json.Marshal(personalConnectorCoordination{Actor: actor, Action: action})
	if err != nil {
		return personalconnectors.ReviewDecision{}, err
	}
	raw, err = s.invokePersonalCoordination(ctx, "connector-review", personalCoordinationRequest{Input: raw}, nil)
	if err != nil {
		return personalconnectors.ReviewDecision{}, err
	}
	var decision personalconnectors.ReviewDecision
	err = json.Unmarshal(raw, &decision)
	return decision, err
}
func (s *Server) connectorRules(ctx context.Context, operation string, action personalconnectors.Result, input json.RawMessage) (json.RawMessage, error) {
	raw, err := json.Marshal(personalConnectorCoordination{Operation: operation, Action: action, RuleInput: input})
	if err != nil {
		return nil, err
	}
	return s.invokePersonalCoordination(ctx, "connector-rule", personalCoordinationRequest{Input: raw}, nil)
}

func connectorRuleScope(action personalconnectors.Result) (personalreview.RuleScope, error) {
	if action.Digest == "" || action.ConnectionID == "" || action.Operation == "" || action.AccountID == "" || action.ArgumentsJSON == "" {
		return personalreview.RuleScope{}, errors.New("incomplete connector action")
	}
	raw, err := json.Marshal(struct {
		Connection, Account, Domain, Operation, Resource string
		Arguments                                        json.RawMessage
	}{action.ConnectionID, action.AccountID, action.Domain, action.Operation, action.Resource, json.RawMessage(action.ArgumentsJSON)})
	if err != nil {
		return personalreview.RuleScope{}, err
	}
	return personalreview.ExactRuleScope(raw, "仅对账号 "+action.AccountID+"，重复这项操作及完全相同的内容、收件人与附件："+action.Summary)
}
func (p *personalActions) connectorRule(ctx context.Context, operation string, action personalconnectors.Result, raw json.RawMessage) (json.RawMessage, error) {
	binding := p.server.rt.UserBinding()
	if err := clientauthority.RequireOwner(ctx, binding); err != nil {
		return nil, err
	}
	scope, err := connectorRuleScope(action)
	if err != nil {
		return nil, err
	}
	var input struct {
		ActionID         string                      `json:"action_id"`
		Digest           string                      `json:"digest"`
		ExpectedRevision int64                       `json:"expected_revision,omitempty"`
		Decision         personalreview.RuleDecision `json:"decision,omitempty"`
	}
	if err = decodePersonalAction(raw, &input); err != nil {
		return nil, err
	}
	if input.ActionID != action.ActionID || input.Digest != action.Digest || action.State != "awaiting_confirmation" {
		return nil, errors.New("connector action changed")
	}
	var view personalreview.RulesView
	if operation == "connectors.rule.preview" {
		view, err = p.store.GetRules(ctx, binding.UserSpaceID, p.ownerID())
		view.Preview = &personalreview.RulePreview{Description: scope.Description, Operation: "connector." + action.Operation, Target: action.AccountID, Decision: personalreview.RuleAllow}
	} else if operation == "connectors.rule.save" {
		view, err = p.store.SaveRule(ctx, binding.UserSpaceID, p.ownerID(), input.ExpectedRevision, "connector."+action.Operation, connectorReviewVersion, action.AccountID, scope, input.Decision)
	} else {
		return nil, errors.New("unknown connector rule operation")
	}
	if err != nil {
		return nil, err
	}
	return json.Marshal(view)
}

func (p *personalActions) reviewConnector(ctx context.Context, actor personalconnectors.Actor, action personalconnectors.Result) (personalconnectors.ReviewDecision, error) {
	decision := personalconnectors.ReviewDecision{Outcome: "ask", Reason: "请确认这项账号操作。"}
	if _, err := p.connectorAuthority(ctx, actor); err != nil {
		return decision, err
	}
	scope, err := connectorRuleScope(action)
	if err != nil {
		return decision, err
	}
	settings, err := p.store.GetSettings(ctx, actor.UserSpaceID)
	if err != nil {
		return decision, err
	}
	rules, err := p.store.GetRules(ctx, actor.UserSpaceID, p.ownerID())
	if err != nil {
		return decision, err
	}
	decision.PolicyRevision, decision.RulesRevision = settings.Revision, rules.Revision
	rule, err := p.store.MatchRules(ctx, actor.UserSpaceID, p.ownerID(), "connector."+action.Operation, connectorReviewVersion, scope)
	if err != nil {
		return decision, err
	}
	if rule != nil {
		decision.Reason = rule.Description
		if rule.Decision == personalreview.RuleAllow {
			decision.Outcome = "allow"
			decision.RuleID = rule.ID
		}
		return decision, nil
	}
	if !settings.AutoReviewEnabled {
		return decision, nil
	}
	taskScope, err := p.scope(ctx, actor.SessionID)
	if err != nil {
		return decision, err
	}
	var evidence personalreview.Authority
	if err = p.withAuthority(ctx, taskScope, func(a personalreview.Authority) error { evidence = a; return nil }); err != nil {
		return decision, err
	}
	if !evidence.Active || len(evidence.UserMessages) == 0 {
		return decision, nil
	}
	decision.Source, err = json.Marshal(evidence.Scope)
	if err != nil {
		return decision, err
	}
	raw, err := json.Marshal(action)
	if err != nil {
		return decision, err
	}
	reviewed, err := p.reviewDecision(ctx, personalreview.ReviewInput{Action: personalreview.Action{Proposal: personalreview.Proposal{ID: action.ActionID, Scope: taskScope, Operation: "connector." + action.Operation, Arguments: raw}, OwnerID: p.ownerID(), PolicyRevision: settings.Revision, HandlerVersion: connectorReviewVersion, Effect: personalreview.ExternalWrite, Digest: action.Digest, Summary: personalreview.Summary{Title: action.Summary, Account: action.AccountID, Target: action.Resource, Consequence: "执行所展示的账号操作，可能发送内容或修改外部资料。"}}, UserMessages: evidence.UserMessages})
	if err != nil {
		return decision, nil
	}
	decision.Reason = reviewed.Reason
	switch reviewed.Outcome {
	case approvefor.OutcomeAllow:
		decision.Outcome = "allow"
	case approvefor.OutcomeDeny:
		decision.Outcome = "deny"
	}
	return decision, nil
}

// Called inside the task, session and policy inspection, immediately before the
// broker claims the effect. A concurrent rule/settings change invalidates review.
func (p *personalActions) checkConnectorReview(ctx context.Context, actor personalconnectors.Actor, action personalconnectors.Result, decision *personalconnectors.ReviewDecision) error {
	if decision == nil || decision.Outcome != "allow" || action.Digest == "" {
		return errors.New("automatic connector approval missing")
	}
	settings, err := p.store.GetSettings(ctx, actor.UserSpaceID)
	if err != nil {
		return err
	}
	rules, err := p.store.GetRules(ctx, actor.UserSpaceID, p.ownerID())
	if err != nil {
		return err
	}
	if settings.Revision != decision.PolicyRevision || rules.Revision != decision.RulesRevision {
		return errors.New("approval rules changed; confirm this action again")
	}
	if decision.RuleID != "" {
		scope, err := connectorRuleScope(action)
		if err != nil {
			return err
		}
		rule, err := p.store.MatchRules(ctx, actor.UserSpaceID, p.ownerID(), "connector."+action.Operation, connectorReviewVersion, scope)
		if err != nil {
			return err
		}
		if rule == nil || rule.ID != decision.RuleID || rule.Decision != personalreview.RuleAllow {
			return errors.New("saved connector permission changed")
		}
		return nil
	}
	var source personalreview.Scope
	if err := decodePersonalAction(decision.Source, &source); err != nil {
		return errors.New("automatic connector review source is unavailable")
	}
	if source.SpaceID != actor.UserSpaceID || source.SessionID != actor.SessionID || source.TaskID == "" || source.ContextDigest == "" {
		return errors.New("automatic connector review source does not match this execution")
	}
	return p.withAuthority(ctx, source, func(current personalreview.Authority) error {
		if !current.Active || current.BoundaryReason != "" || !current.AllowIntentReview || len(current.UserMessages) == 0 || current.OwnerID != p.ownerID() || current.Scope != source || current.PolicyRevision != decision.PolicyRevision {
			return errors.New("original user request changed; confirm this action again")
		}
		return nil
	})
}
