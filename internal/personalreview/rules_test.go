package personalreview

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/blueberrycongee/wuu/internal/persistence"
)

// Contracts: scope and owner isolation, exact matching, stale UI writes,
// revocation, audit durability, and preserving an existing manual setting.
func TestRulesPersistExactScopeAndOwnerChoices(t *testing.T) {
	for _, kind := range []string{"sqlite", "jsonl"} {
		t.Run(kind, func(t *testing.T) {
			ctx := context.Background()
			docs, err := persistence.Open(ctx, t.TempDir(), kind)
			if err != nil {
				t.Fatal(err)
			}
			defer docs.Close()
			store, err := NewDocumentStore(docs)
			if err != nil {
				t.Fatal(err)
			}
			settings, err := store.GetSettings(ctx, "space")
			if err != nil || !settings.AutoReviewEnabled {
				t.Fatalf("new default: %+v %v", settings, err)
			}
			settings, err = store.SetSettings(ctx, "space", false, settings.Revision, "owner")
			if err != nil {
				t.Fatal(err)
			}
			scope := RuleScope{Key: "exact args and environment", Description: "Run this operation in this folder"}
			view, err := store.GetRules(ctx, "space", "owner")
			if err != nil {
				t.Fatal(err)
			}
			view, err = store.SaveRule(ctx, "space", "owner", view.Revision, "bash", "1", "folder", scope, RuleAllow)
			if err != nil {
				t.Fatal(err)
			}
			if len(view.Rules) != 1 {
				t.Fatalf("rules: %+v", view)
			}
			id := view.Rules[0].ID
			if _, err = store.UpdateRule(ctx, "space", "owner", 1, id, RuleAsk, true); !errors.Is(err, ErrConflict) {
				t.Fatalf("stale write: %v", err)
			}
			for _, test := range []struct{ space, owner, op, version, key string }{
				{"space2", "owner", "bash", "1", scope.Key}, {"space", "other", "bash", "1", scope.Key},
				{"space", "owner", "bash", "2", scope.Key}, {"space", "owner", "process", "1", scope.Key},
				{"space", "owner", "bash", "1", scope.Key + " ; additional operation"},
			} {
				got, err := store.MatchRules(ctx, test.space, test.owner, test.op, test.version, RuleScope{Key: test.key})
				if err != nil || got != nil {
					t.Fatalf("scope escape: %+v %+v %v", test, got, err)
				}
			}
			got, err := store.MatchRules(ctx, "space", "owner", "bash", "1", scope)
			if err != nil || got == nil || got.Decision != RuleAllow {
				t.Fatalf("allow: %+v %v", got, err)
			}
			view, err = store.UpdateRule(ctx, "space", "owner", view.Revision, id, RuleAsk, true)
			if err != nil {
				t.Fatal(err)
			}
			got, err = store.MatchRules(ctx, "space", "owner", "bash", "1", scope)
			if err != nil || got == nil || got.Decision != RuleAsk {
				t.Fatalf("ask: %+v %v", got, err)
			}
			if err = store.Close(); err != nil {
				t.Fatal(err)
			}
			store, err = NewDocumentStore(docs)
			if err != nil {
				t.Fatal(err)
			}
			got, err = store.MatchRules(ctx, "space", "owner", "bash", "1", scope)
			if err != nil || got == nil || got.Decision != RuleAsk {
				t.Fatalf("restart: %+v %v", got, err)
			}
			current, err := store.GetSettings(ctx, "space")
			if err != nil || current.AutoReviewEnabled || current.Revision != settings.Revision {
				t.Fatalf("rule changed existing setting: %+v %v", current, err)
			}
			view, err = store.DeleteRule(ctx, "space", "owner", view.Revision, id)
			if err != nil {
				t.Fatal(err)
			}
			got, err = store.MatchRules(ctx, "space", "owner", "bash", "1", scope)
			if err != nil || got != nil {
				t.Fatalf("deleted rule matches: %+v %v", got, err)
			}
			events, err := store.RuleEvents(ctx, "space", "owner")
			if err != nil || len(events) != 3 || events[2].Action != "delete" || events[2].Actor != "owner" {
				t.Fatalf("audit: %+v %v", events, err)
			}
		})
	}
}

func TestRuleChangesCannotCrossExecutionInspection(t *testing.T) {
	ctx := context.Background()
	docs, err := persistence.Open(ctx, t.TempDir(), "sqlite")
	if err != nil {
		t.Fatal(err)
	}
	defer docs.Close()
	store, err := NewDocumentStore(docs)
	if err != nil {
		t.Fatal(err)
	}
	view, err := store.GetRules(ctx, "space", "owner")
	if err != nil {
		t.Fatal(err)
	}
	err = store.WithPolicyInspection(ctx, func() error {
		_, err := store.SaveRule(ctx, "space", "owner", view.Revision, "bash", "1", "target", RuleScope{Key: "exact", Description: "Exact operation"}, RuleAllow)
		if err == nil {
			t.Fatal("rule mutation crossed in-flight execution authority")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// Rules are grants for future matching actions, not bulk resolution of pending
// cards. Revocation between assessment and dispatch must never reach the effect.
func TestRulesAuthorizeAndRecheckBeforeDispatch(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	docs, err := persistence.Open(ctx, t.TempDir(), "sqlite")
	if err != nil {
		t.Fatal(err)
	}
	defer docs.Close()
	f.store, err = NewDocumentStore(docs)
	if err != nil {
		t.Fatal(err)
	}
	f.engine = f.makeEngine()
	scope := RuleScope{Key: "synthetic-target", Description: "Save this exact target"}
	op := f.engine.operations["synthetic.save"]
	op.RuleScope = func(json.RawMessage) (RuleScope, error) { return scope, nil }
	f.engine.operations["synthetic.save"] = op
	view, err := f.store.SaveRule(ctx, "personal-space", "owner", 1, "synthetic.save", "1", "synthetic-target", scope, RuleAllow)
	if err != nil {
		t.Fatal(err)
	}
	r := f.propose("synthetic.save")
	if r.Status != Ready || r.RuleID == "" || !f.reviews[len(f.reviews)-1].RuleApproved {
		t.Fatalf("rule did not authorize: %+v", r)
	}
	_, err = f.store.DeleteRule(ctx, "personal-space", "owner", view.Revision, view.Rules[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	r, err = f.engine.Execute(ctx, r.Action.Key())
	if err != nil || r.Status != AwaitingConfirmation || f.calls != 0 {
		t.Fatalf("revoked dispatch: %+v %v effects=%d", r, err, f.calls)
	}
	r = f.approve(r)
	r, err = f.engine.Execute(ctx, r.Action.Key())
	if err != nil || r.Status != Succeeded || f.calls != 1 {
		t.Fatalf("allow once after deletion: %+v %v effects=%d", r, err, f.calls)
	}
}

func TestAskRuleRequiresOwnerEvenWhenAutomaticReviewWouldAllow(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	docs, err := persistence.Open(ctx, t.TempDir(), "sqlite")
	if err != nil {
		t.Fatal(err)
	}
	defer docs.Close()
	f.store, err = NewDocumentStore(docs)
	if err != nil {
		t.Fatal(err)
	}
	f.engine = f.makeEngine()
	f.authority.AllowIntentReview = true
	f.authority.Scope.ContextDigest = "human-source"
	scope := RuleScope{Key: "synthetic-target", Description: "Save this exact target"}
	op := f.engine.operations["synthetic.save"]
	op.RuleScope = func(json.RawMessage) (RuleScope, error) { return scope, nil }
	f.engine.operations["synthetic.save"] = op
	_, err = f.store.SaveRule(ctx, "personal-space", "owner", 1, "synthetic.save", "1", "synthetic-target", scope, RuleAsk)
	if err != nil {
		t.Fatal(err)
	}
	r := f.propose("synthetic.save")
	if r.Status != AwaitingConfirmation || len(f.reviews) != 0 {
		t.Fatalf("ask-first bypassed: %+v reviews=%d", r, len(f.reviews))
	}
	r = f.approve(r)
	r, err = f.engine.Execute(ctx, r.Action.Key())
	if err != nil || r.Status != Succeeded || f.calls != 1 {
		t.Fatalf("manual decision: %+v %v", r, err)
	}
}
