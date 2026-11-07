package personalreview

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/blueberrycongee/wuu/internal/persistence"
)

type RuleDecision string

const (
	RuleAllow RuleDecision = "allow"
	RuleAsk   RuleDecision = "ask"
)

// RuleScope is derived by a trusted operation from its frozen arguments. Key
// must bind every execution input except the exact variance described to users.
// It is an equality key, never a shell prefix, glob, or model-written pattern.
type RuleScope struct {
	Key         string
	Description string
}

type Rule struct {
	ID          string       `json:"id"`
	Description string       `json:"description"`
	Operation   string       `json:"operation"`
	Target      string       `json:"target"`
	Decision    RuleDecision `json:"decision"`
	Enabled     bool         `json:"enabled"`
	CreatedAt   time.Time    `json:"created_at"`
	UpdatedAt   time.Time    `json:"updated_at"`
	Revision    int64        `json:"-"`
}

type RulePreview struct {
	Description string       `json:"description"`
	Operation   string       `json:"operation"`
	Target      string       `json:"target"`
	Decision    RuleDecision `json:"decision"`
}

type RulesView struct {
	Schema   string       `json:"schema"`
	Revision int64        `json:"revision"`
	Rules    []Rule       `json:"rules"`
	Preview  *RulePreview `json:"preview,omitempty"`
}

type storedRule struct {
	Rule           Rule   `json:"rule"`
	MatchKey       string `json:"match_key"`
	HandlerVersion string `json:"handler_version"`
	Revision       int64  `json:"revision"`
}

type storedRules struct {
	Revision int64        `json:"revision"`
	Rules    []storedRule `json:"rules"`
}

type RuleEvent struct {
	Actor    string    `json:"actor"`
	At       time.Time `json:"at"`
	Revision int64     `json:"revision"`
	Action   string    `json:"action"`
	Rule     Rule      `json:"rule"`
}

func rulesKey(spaceID, ownerID string) (string, error) {
	if strings.TrimSpace(spaceID) == "" || strings.TrimSpace(ownerID) == "" {
		return "", ErrUnauthorized
	}
	return "rules/" + reviewPart(spaceID) + "/" + reviewPart(ownerID), nil
}

func rulesView(rules storedRules) RulesView {
	view := RulesView{Schema: "personal-review-rules.v1", Revision: rules.Revision, Rules: make([]Rule, 0, len(rules.Rules))}
	for _, entry := range rules.Rules {
		rule := entry.Rule
		rule.Revision = entry.Revision
		view.Rules = append(view.Rules, rule)
	}
	return view
}

func (s *Store) readRules(ctx context.Context, spaceID, ownerID string) (storedRules, error) {
	key, err := rulesKey(spaceID, ownerID)
	if err != nil {
		return storedRules{}, err
	}
	rules := storedRules{Revision: 1, Rules: []storedRule{}}
	// Legacy SQLite ledgers remain readable for migration. New policy belongs to
	// selected document storage, never a second local configuration database.
	if s.documents == nil {
		return rules, nil
	}
	err = s.documentTransaction(ctx, func(tx *persistence.Tx) error {
		_, err := tx.Get(reviewSettings, key, &rules)
		return err
	})
	return rules, err
}

func (s *Store) GetRules(ctx context.Context, spaceID, ownerID string) (RulesView, error) {
	rules, err := s.readRules(ctx, spaceID, ownerID)
	return rulesView(rules), err
}

func (s *Store) MatchRules(ctx context.Context, spaceID, ownerID, operation, handlerVersion string, scope RuleScope) (*Rule, error) {
	rules, err := s.readRules(ctx, spaceID, ownerID)
	if err != nil {
		return nil, err
	}
	var selected *Rule
	for _, entry := range rules.Rules {
		if !entry.Rule.Enabled || entry.Rule.Operation != operation || entry.HandlerVersion != handlerVersion || entry.MatchKey != scope.Key {
			continue
		}
		rule := entry.Rule
		rule.Revision = entry.Revision
		if rule.Decision == RuleAsk {
			return &rule, nil
		}
		if rule.Decision == RuleAllow {
			selected = &rule
		}
	}
	return selected, nil
}

func (s *Store) SaveRule(ctx context.Context, spaceID, ownerID string, expected int64, operation, handlerVersion, target string, scope RuleScope, decision RuleDecision) (RulesView, error) {
	if operation == "" || handlerVersion == "" || target == "" || scope.Key == "" || strings.TrimSpace(scope.Description) == "" || len(scope.Key) > 128*1024 || len(scope.Description) > 4096 || !validRuleDecision(decision) {
		return RulesView{}, errors.New("invalid bounded approval rule")
	}
	id := "rule-" + digest(struct{ Operation, HandlerVersion, Key string }{operation, handlerVersion, scope.Key})
	return s.changeRules(ctx, spaceID, ownerID, expected, "save", func(rules *storedRules, at time.Time) (Rule, error) {
		entry := storedRule{Rule: Rule{ID: id, Description: scope.Description, Operation: operation, Target: target, Decision: decision, Enabled: true, CreatedAt: at, UpdatedAt: at}, MatchKey: scope.Key, HandlerVersion: handlerVersion, Revision: expected + 1}
		for i, old := range rules.Rules {
			if old.Rule.ID == id {
				entry.Rule.CreatedAt = old.Rule.CreatedAt
				rules.Rules[i] = entry
				return entry.Rule, nil
			}
		}
		if len(rules.Rules) >= 256 {
			return Rule{}, errors.New("approval rule limit reached")
		}
		rules.Rules = append(rules.Rules, entry)
		return entry.Rule, nil
	})
}

func (s *Store) UpdateRule(ctx context.Context, spaceID, ownerID string, expected int64, id string, decision RuleDecision, enabled bool) (RulesView, error) {
	if !validRuleDecision(decision) {
		return RulesView{}, errors.New("invalid approval rule decision")
	}
	return s.changeRules(ctx, spaceID, ownerID, expected, "update", func(rules *storedRules, at time.Time) (Rule, error) {
		for i := range rules.Rules {
			entry := &rules.Rules[i]
			if entry.Rule.ID == id {
				entry.Rule.Decision = decision
				entry.Rule.Enabled = enabled
				entry.Rule.UpdatedAt = at
				entry.Revision = expected + 1
				return entry.Rule, nil
			}
		}
		return Rule{}, ErrNotFound
	})
}

func (s *Store) DeleteRule(ctx context.Context, spaceID, ownerID string, expected int64, id string) (RulesView, error) {
	return s.changeRules(ctx, spaceID, ownerID, expected, "delete", func(rules *storedRules, at time.Time) (Rule, error) {
		for i, entry := range rules.Rules {
			if entry.Rule.ID == id {
				rules.Rules = append(rules.Rules[:i], rules.Rules[i+1:]...)
				return entry.Rule, nil
			}
		}
		return Rule{}, ErrNotFound
	})
}
func validRuleDecision(decision RuleDecision) bool {
	return decision == RuleAllow || decision == RuleAsk
}

func (s *Store) changeRules(ctx context.Context, spaceID, ownerID string, expected int64, action string, change func(*storedRules, time.Time) (Rule, error)) (RulesView, error) {
	key, err := rulesKey(spaceID, ownerID)
	if err != nil {
		return RulesView{}, err
	}
	if s.documents == nil {
		return RulesView{}, errors.New("approval rules require selected document storage")
	}
	if expected < 1 || expected >= 1<<53-1 {
		return RulesView{}, ErrConflict
	}
	var rules storedRules
	err = s.documentTransaction(ctx, func(tx *persistence.Tx) error {
		rules = storedRules{Revision: 1, Rules: []storedRule{}}
		if _, err := tx.Get(reviewSettings, key, &rules); err != nil {
			return err
		}
		if rules.Revision != expected {
			return ErrConflict
		}
		at := time.Now().UTC()
		rule, err := change(&rules, at)
		if err != nil {
			return err
		}
		rules.Revision++
		if err = tx.Put(reviewSettings, key, rules); err != nil {
			return err
		}
		return tx.Put(reviewSettingsEvents, reviewRevisionKey(key, rules.Revision), RuleEvent{Actor: ownerID, At: at, Revision: rules.Revision, Action: action, Rule: rule})
	})
	if err != nil {
		return RulesView{}, err
	}
	return rulesView(rules), nil
}

func (s *Store) RuleEvents(ctx context.Context, spaceID, ownerID string) ([]RuleEvent, error) {
	key, err := rulesKey(spaceID, ownerID)
	if err != nil {
		return nil, err
	}
	if s.documents == nil {
		return []RuleEvent{}, nil
	}
	return documentRows[RuleEvent](ctx, s, reviewSettingsEvents, key+"/")
}

// ExactRuleScope is the conservative default for operations without a broader
// trusted scope. A changed argument always requires a new decision.
func ExactRuleScope(arguments json.RawMessage, description string) (RuleScope, error) {
	canonical, err := canonicalArguments(arguments)
	if err != nil {
		return RuleScope{}, err
	}
	return RuleScope{Key: ArgumentsDigest(canonical), Description: description}, nil
}

func (e *Engine) actionRule(ctx context.Context, r Record, dispatch bool) (*Rule, string, error) {
	if validApproval(r) || !needsConfirmation(r.Action.Effect) {
		return nil, "", nil
	}
	scope, err := e.ruleScope(r.Action)
	if err != nil {
		return nil, "", err
	}
	rule, err := e.store.MatchRules(ctx, r.Action.Scope.SpaceID, r.Action.OwnerID, r.Action.Operation, r.Action.HandlerVersion, scope)
	if err != nil {
		return nil, "", err
	}
	if rule != nil && rule.Decision == RuleAsk {
		return rule, "Your saved rule requires confirmation for this action", nil
	}
	if dispatch && r.RuleID != "" && (rule == nil || rule.ID != r.RuleID || rule.Revision != r.RuleRevision) {
		return nil, "The saved permission changed. Confirm this action again", nil
	}
	return rule, "", nil
}
func (e *Engine) ruleScope(action Action) (RuleScope, error) {
	operation, ok := e.operations[action.Operation]
	if !ok {
		return RuleScope{}, errors.New("unknown operation cannot create an approval rule")
	}
	if operation.RuleScope != nil {
		return operation.RuleScope(action.Arguments)
	}
	return ExactRuleScope(action.Arguments, "Repeat this exact operation with the same inputs: "+action.Summary.Title)
}

func (e *Engine) PreviewRule(ctx context.Context, key Key, binding ActionBinding) (RulesView, error) {
	record, identity, err := e.ruleSource(ctx, key, binding)
	if err != nil {
		return RulesView{}, err
	}
	scope, err := e.ruleScope(record.Action)
	if err != nil {
		return RulesView{}, err
	}
	view, err := e.store.GetRules(ctx, identity.SpaceID, identity.UserID)
	if err != nil {
		return RulesView{}, err
	}
	view.Preview = &RulePreview{Description: scope.Description, Operation: record.Action.Operation, Target: record.Action.Summary.Target, Decision: RuleAllow}
	return view, nil
}
func (e *Engine) SaveRule(ctx context.Context, key Key, binding ActionBinding, expected int64, decision RuleDecision) (RulesView, error) {
	record, identity, err := e.ruleSource(ctx, key, binding)
	if err != nil {
		return RulesView{}, err
	}
	scope, err := e.ruleScope(record.Action)
	if err != nil {
		return RulesView{}, err
	}
	return e.store.SaveRule(ctx, identity.SpaceID, identity.UserID, expected, record.Action.Operation, record.Action.HandlerVersion, record.Action.Summary.Target, scope, decision)
}
func (e *Engine) ruleSource(ctx context.Context, key Key, binding ActionBinding) (Record, Identity, error) {
	r, err := e.store.get(ctx, key)
	if err != nil {
		return Record{}, Identity{}, err
	}
	identity, err := e.owner(ctx, r)
	if err != nil {
		return Record{}, Identity{}, err
	}
	if r.Action.Digest != binding.Digest || r.Revision != binding.ExpectedRevision {
		return Record{}, Identity{}, ErrConflict
	}
	if r.Status != AwaitingConfirmation && r.Status != Blocked && r.Status != Ready && r.Status != Approved {
		return Record{}, Identity{}, ErrConflict
	}
	_, stopped, err := e.inspect(ctx, r)
	if err != nil {
		return Record{}, Identity{}, err
	}
	if stopped.Status != "" {
		return Record{}, Identity{}, ErrConflict
	}
	if !needsConfirmation(r.Action.Effect) {
		return Record{}, Identity{}, errors.New("operation does not support reusable approval")
	}
	return r, identity, nil
}
