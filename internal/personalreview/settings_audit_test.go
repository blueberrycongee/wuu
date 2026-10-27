package personalreview

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/approvefor"
)

// Settings changes and their attributable audit must commit together; failed
// CAS, audit persistence faults and restart must never invent an owner choice.
func TestSettingsAuditIsAttributableAtomicAndDurable(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "settings-audit.sqlite")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	before, err := store.GetSettings(ctx, "space")
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now().UTC()
	after, err := store.SetSettings(ctx, "space", true, before.Revision, "stable-owner")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.SetSettings(ctx, "space", false, before.Revision, "another-owner"); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale settings CAS: %v", err)
	}
	events, err := store.SettingsEvents(ctx, "space")
	if err != nil || len(events) != 1 {
		t.Fatalf("events=%+v err=%v", events, err)
	}
	event := events[0]
	if event.Actor != "stable-owner" || event.OldEnabled || !event.NewEnabled || event.Revision != after.Revision || event.PolicyVersion != after.PolicyVersion || event.At.Before(started) || event.At.After(time.Now().UTC()) {
		t.Fatalf("incorrect settings provenance: %+v", event)
	}
	if _, err := store.db.Exec(`CREATE TRIGGER synthetic_settings_audit_failure BEFORE INSERT ON personal_review_settings_events BEGIN SELECT RAISE(ABORT,'synthetic persistence fault'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SetSettings(ctx, "space", false, after.Revision, "stable-owner"); err == nil {
		t.Fatal("audit failure was ignored")
	}
	retained, err := store.GetSettings(ctx, "space")
	if err != nil || !retained.AutoReviewEnabled || retained.Revision != after.Revision {
		t.Fatalf("audit failure changed settings: %+v %v", retained, err)
	}
	events, err = store.SettingsEvents(ctx, "space")
	if err != nil || len(events) != 1 {
		t.Fatalf("failed change left phantom event: %+v %v", events, err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	events, err = store.SettingsEvents(ctx, "space")
	if err != nil || len(events) != 1 || events[0] != event {
		t.Fatalf("settings audit did not survive restart: %+v %v", events, err)
	}
	other, err := store.SettingsEvents(ctx, "other-space")
	if err != nil || len(other) != 0 {
		t.Fatalf("settings audit crossed space: %+v %v", other, err)
	}
}

func TestDeniedReviewerDecisionCannotBecomeManualApproval(t *testing.T) {
	for _, stage := range []string{"assessment", "execution"} {
		t.Run(stage, func(t *testing.T) {
			f := newFixture(t)
			var r Record
			if stage == "assessment" {
				f.decision.Outcome = approvefor.OutcomeDeny
				r = f.propose("synthetic.save")
			} else {
				r = f.approve(f.propose("synthetic.save"))
				f.decision.Outcome = approvefor.OutcomeDeny
				r = f.run(r)
			}
			if r.Status != Denied {
				t.Fatalf("reviewer denial was not terminal: %+v", r)
			}
			if _, err := f.resolve(r, ApproveOnce); !errors.Is(err, ErrConflict) {
				t.Fatalf("manual approval overrode deny: %v", err)
			}
			f.decision.Outcome = approvefor.OutcomeAllow
			reassessed, err := f.engine.Assess(context.Background(), r.Action.Key())
			if err != nil || reassessed.Status != Denied {
				t.Fatalf("fresh reviewer revived deny: %+v %v", reassessed, err)
			}
			f.run(reassessed)
			if f.calls != 0 {
				t.Fatal("denied action executed")
			}
		})
	}
}
