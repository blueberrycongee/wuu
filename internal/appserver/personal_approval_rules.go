package appserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/blueberrycongee/wuu/internal/personalreview"
	"github.com/blueberrycongee/wuu/internal/tools"
)

func (p *personalActions) fileRuleScope(raw json.RawMessage) (personalreview.RuleScope, error) {
	var action tools.FileAction
	if err := decodePersonalAction(raw, &action); err != nil {
		return personalreview.RuleScope{}, err
	}
	if _, err := p.describe(raw); err != nil {
		return personalreview.RuleScope{}, err
	}
	if err := p.safeFile(action); err != nil {
		return personalreview.RuleScope{}, err
	}
	key, err := json.Marshal(struct{ Operation, CWD, Target string }{action.Operation, action.CWD, action.Target})
	if err != nil {
		return personalreview.RuleScope{}, err
	}
	return personalreview.RuleScope{Key: personalHash(key), Description: fmt.Sprintf("Allow future changes to %q in %q. New contents may differ; other files are not included.", filepath.Base(action.Target), filepath.Dir(action.Target))}, nil
}

type personalRuleSource struct {
	SpaceID          string `json:"space_id"`
	ID               string `json:"id"`
	Digest           string `json:"digest"`
	ExpectedRevision int64  `json:"expected_revision"`
}

func (s personalRuleSource) key() personalreview.Key {
	return personalreview.Key{SpaceID: s.SpaceID, ID: s.ID}
}
func (s personalRuleSource) binding() personalreview.ActionBinding {
	return personalreview.ActionBinding{Digest: s.Digest, ExpectedRevision: s.ExpectedRevision}
}

func (p *personalActions) callRules(ctx context.Context, operation string, raw json.RawMessage) (json.RawMessage, error) {
	identity, err := p.authenticate(ctx)
	if err != nil {
		return nil, err
	}
	if operation == "review.rule.preview" {
		var source personalRuleSource
		if err := decodePersonalAction(raw, &source); err != nil {
			return nil, err
		}
		if source.SpaceID != identity.SpaceID {
			return nil, personalreview.ErrUnauthorized
		}
		view, err := p.engine.PreviewRule(ctx, source.key(), source.binding())
		if err != nil {
			return nil, err
		}
		return json.Marshal(view)
	}
	var input struct {
		ExpectedRevision int64                       `json:"expected_revision,omitempty"`
		Action           string                      `json:"action,omitempty"`
		Source           *personalRuleSource         `json:"source,omitempty"`
		ID               string                      `json:"id,omitempty"`
		Decision         personalreview.RuleDecision `json:"decision,omitempty"`
		Enabled          *bool                       `json:"enabled,omitempty"`
	}
	if err := decodePersonalAction(raw, &input); err != nil {
		return nil, err
	}
	var view personalreview.RulesView
	switch input.Action {
	case "":
		if input.ExpectedRevision != 0 || input.Source != nil || input.ID != "" || input.Decision != "" || input.Enabled != nil {
			return nil, errors.New("rule listing does not accept mutation fields")
		}
		view, err = p.store.GetRules(ctx, identity.SpaceID, identity.UserID)
	case "save":
		if input.Source == nil || input.Source.SpaceID != identity.SpaceID || input.ID != "" || input.Enabled != nil {
			return nil, personalreview.ErrUnauthorized
		}
		view, err = p.engine.SaveRule(ctx, input.Source.key(), input.Source.binding(), input.ExpectedRevision, input.Decision)
	case "update":
		if input.Enabled == nil || input.Source != nil || input.ID == "" {
			return nil, errors.New("rule update requires its ID, decision and enabled state")
		}
		view, err = p.store.UpdateRule(ctx, identity.SpaceID, identity.UserID, input.ExpectedRevision, input.ID, input.Decision, *input.Enabled)
	case "delete":
		if input.Source != nil || input.ID == "" || input.Decision != "" || input.Enabled != nil {
			return nil, errors.New("rule deletion requires its ID")
		}
		view, err = p.store.DeleteRule(ctx, identity.SpaceID, identity.UserID, input.ExpectedRevision, input.ID)
	default:
		return nil, errors.New("unknown approval rule action")
	}
	if err != nil {
		return nil, err
	}
	return json.Marshal(view)
}
