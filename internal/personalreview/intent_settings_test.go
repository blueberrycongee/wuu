package personalreview

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"sync"
	"testing"

	"github.com/blueberrycongee/wuu/internal/approvefor"
)

// These contracts are specified before integration: opt-in is not itself user
// authorization, missing source evidence cannot authorize, and an old allow is
// not reusable after a policy/task change or a new execution attempt.
func TestIntentReviewRequiresOptInBoundHumanEvidenceAndFreshVerdict(t *testing.T) {
	for _, scenario := range []string{"off", "missing-context", "missing-message", "empty-source-id", "empty-source-text", "enabled"} {
		t.Run(scenario, func(t *testing.T) {
			f := newFixture(t)
			f.authority.AllowIntentReview = true
			f.authority.Scope.ContextDigest = "host-resolved-original-source"
			switch scenario {
			case "off":
				f.authority.AllowIntentReview = false
			case "missing-context":
				f.authority.Scope.ContextDigest = ""
			case "missing-message":
				f.authority.UserMessages = nil
			case "empty-source-id":
				f.authority.UserMessages[0].ID = ""
			case "empty-source-text":
				f.authority.UserMessages[0].Text = " "
			}
			r := f.propose("synthetic.save")
			if scenario != "enabled" {
				if r.Status != AwaitingConfirmation || f.calls != 0 {
					t.Fatalf("missing opt-in/evidence authorized: %+v calls=%d", r, f.calls)
				}
				return
			}
			if r.Status != Ready || r.Approval != nil || r.ConsentID != "" || len(f.reviews) != 1 {
				t.Fatalf("exact host intent should avoid a redundant question: %+v", r)
			}
			r = f.run(r)
			if r.Status != Succeeded || f.calls != 1 || len(f.reviews) != 2 {
				t.Fatalf("dispatch did not freshly review: %+v calls=%d reviews=%d", r, f.calls, len(f.reviews))
			}
			f.run(r)
			if f.calls != 1 || len(f.reviews) != 2 {
				t.Fatal("successful action replayed")
			}
		})
	}
}

func TestIntentReviewRechecksRevocationAndEvidenceAtDispatch(t *testing.T) {
	for _, change := range []string{"opt-out", "context", "source-missing", "task", "deny", "unsure", "error", "late-opt-out", "late-context"} {
		t.Run(change, func(t *testing.T) {
			f := newFixture(t)
			f.authority.AllowIntentReview = true
			f.authority.Scope.ContextDigest = "source-v1"
			r := f.propose("synthetic.save")
			if r.Status != Ready {
				t.Fatalf("not ready: %+v", r)
			}
			switch change {
			case "opt-out":
				f.authority.AllowIntentReview = false
			case "context":
				f.authority.Scope.ContextDigest = "source-v2"
			case "source-missing":
				f.authority.UserMessages = nil
			case "task":
				f.authority.Scope.RequirementsRevision++
			case "deny":
				f.decision.Outcome = approvefor.OutcomeDeny
			case "unsure":
				f.decision.Outcome = approvefor.OutcomeUnsure
			case "error":
				f.reviewErr = errors.New("synthetic outage")
			case "late-opt-out":
				f.beforeReviewReturn = func() { f.authority.AllowIntentReview = false }
			case "late-context":
				f.beforeReviewReturn = func() { f.authority.Scope.ContextDigest = "source-v2" }
			}
			got, err := f.engine.Execute(context.Background(), r.Action.Key())
			if err != nil && !errors.Is(err, ErrUnauthorized) {
				t.Fatal(err)
			}
			if got.Status == Succeeded || got.Status == Executing || f.calls != 0 {
				t.Fatalf("changed authority reached effect: %+v calls=%d", got, f.calls)
			}
		})
	}
}

func TestRevocationSharesDispatchAuthorityBoundary(t *testing.T) {
	f := newFixture(t)
	r := f.approve(f.propose("synthetic.save"))
	entered, release := make(chan struct{}), make(chan struct{})
	base := f.engine.withAuthority
	f.engine.withAuthority = func(ctx context.Context, scope Scope, use func(Authority) error) error {
		close(entered)
		<-release
		return base(ctx, scope, use)
	}
	done := make(chan error, 1)
	go func() {
		_, err := f.engine.Revoke(context.Background(), r.Action.Key(), ActionBinding{Digest: r.Action.Digest, ExpectedRevision: r.Revision})
		done <- err
	}()
	select {
	case <-entered:
	case err := <-done:
		t.Fatalf("revocation skipped shared boundary: %v", err)
	}
	current, err := f.store.get(context.Background(), r.Action.Key())
	if err != nil || current.Status != Approved {
		t.Fatalf("revoked before acquiring authority: %+v %v", current, err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	current, err = f.store.get(context.Background(), r.Action.Key())
	if err != nil || current.Status != Revoked {
		t.Fatalf("revocation not durable: %+v %v", current, err)
	}
}

func TestPersonalSettingsDefaultIsolationCASAndRestart(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "settings.sqlite")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	settings, err := store.GetSettings(ctx, "personal-one")
	if err != nil || settings.AutoReviewEnabled || settings.Revision != 1 {
		t.Fatalf("default must remain manual: %+v %v", settings, err)
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	successes := 0
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := store.SetSettings(ctx, "personal-one", true, settings.Revision)
			if err == nil {
				mu.Lock()
				successes++
				mu.Unlock()
			} else if !errors.Is(err, ErrConflict) {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if successes != 1 {
		t.Fatalf("CAS winners=%d", successes)
	}
	other, err := store.GetSettings(ctx, "personal-two")
	if err != nil || other.AutoReviewEnabled || other.Revision != 1 {
		t.Fatalf("settings crossed spaces: %+v %v", other, err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := store.GetSettings(ctx, "personal-one")
	if err != nil || !loaded.AutoReviewEnabled || loaded.Revision != 2 {
		t.Fatalf("opt-in was not durable: %+v %v", loaded, err)
	}
	if _, err := store.SetSettings(ctx, "personal-one", false, 1); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale update accepted: %v", err)
	}
	if _, err := store.GetSettings(ctx, ""); err == nil {
		t.Fatal("missing space accepted")
	}
	if _, err := store.SetSettings(ctx, "personal-one", false, 0); err == nil {
		t.Fatal("missing revision accepted")
	}
}

func TestHostExecutionReadsAreScopedAndOwnerReceiptIsLossless(t *testing.T) {
	f := newFixture(t)
	op := f.engine.operations["synthetic.save"]
	op.Execute = func(context.Context, Action) (json.RawMessage, error) {
		return json.RawMessage(`{"result_id":9007199254740993}`), nil
	}
	f.engine.operations["synthetic.save"] = op
	r := f.run(f.approve(f.propose("synthetic.save")))
	f.identity.Channel = "worker"
	if _, err := f.engine.Get(context.Background(), r.Action.Key()); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("owner route accepted worker: %v", err)
	}
	got, err := f.engine.GetExecution(context.Background(), r.Action.Key())
	if err != nil || got.Status != Succeeded {
		t.Fatalf("host recovery read: %+v %v", got, err)
	}
	other, err := f.engine.Records(context.Background(), "other-space")
	if err != nil || len(other) != 0 {
		t.Fatalf("cross-space recovery listing: %+v %v", other, err)
	}
	f.identity.Channel = OwnerUI
	view, err := f.engine.OwnerSnapshot(context.Background())
	if err != nil || len(view.Requests) != 1 || view.Requests[0].ReceiptJSON != string(got.Result) {
		t.Fatalf("receipt changed: %+v %v", view, err)
	}
}

func TestIntentReviewOptOutDuringAssessmentDoesNotRemainReady(t *testing.T) {
	f := newFixture(t)
	f.authority.AllowIntentReview = true
	f.authority.Scope.ContextDigest = "host-source"
	f.beforeReviewReturn = func() { f.authority.AllowIntentReview = false }
	r := f.propose("synthetic.save")
	if r.Status != AwaitingConfirmation || f.calls != 0 {
		t.Fatalf("late opt-out retained automatic readiness: %+v calls=%d", r, f.calls)
	}
}

func TestSettingsRejectUnrecognizedPersistedPolicy(t *testing.T) {
	f := newFixture(t)
	settings, err := f.store.GetSettings(context.Background(), "personal-space")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.SetSettings(context.Background(), "personal-space", true, settings.Revision); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.db.Exec(`UPDATE personal_review_settings SET policy_version='unknown-next-policy' WHERE space='personal-space'`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.GetSettings(context.Background(), "personal-space"); err == nil {
		t.Fatal("old opt-in silently applied to a different policy version")
	}
}

// Authority inspection can keep durable requests readable without a provider
// capable of fencing execution. A snapshot must never silently become a lease.
func TestInspectionWithoutExecutionAuthorityPreservesPendingDecision(t *testing.T) {
	f := newFixture(t)
	r := f.approve(f.propose("synthetic.send"))
	f.engine.withExecutionAuthority = nil
	if _, err := f.engine.Execute(context.Background(), r.Action.Key()); !errors.Is(err, ErrExecutionAuthorityUnavailable) {
		t.Fatalf("snapshot substituted execution lease: %v", err)
	}
	current, err := f.engine.Get(context.Background(), r.Action.Key())
	if err != nil || current.Status != Approved || current.Revision != r.Revision || f.calls != 0 {
		t.Fatalf("unsupported dispatch changed durable action: %+v %v calls=%d", current, err, f.calls)
	}
	if _, err = f.engine.Revoke(context.Background(), r.Action.Key(), ActionBinding{Digest: r.Action.Digest, ExpectedRevision: r.Revision}); err != nil {
		t.Fatal("unsupported dispatch blocked safe revocation", err)
	}
}

func TestExecutionAuthorityMustActuallyEnterBoundary(t *testing.T) {
	f := newFixture(t)
	r := f.approve(f.propose("synthetic.send"))
	f.engine.withExecutionAuthority = func(context.Context, Scope, func(context.Context, Authority) error) error { return nil }
	if _, err := f.engine.Execute(context.Background(), r.Action.Key()); !errors.Is(err, ErrExecutionAuthorityUnavailable) {
		t.Fatalf("empty authority implementation claimed success: %v", err)
	}
	current, err := f.store.get(context.Background(), r.Action.Key())
	if err != nil || current.Status != Approved || f.calls != 0 {
		t.Fatalf("noop boundary changed action: %+v %v", current, err)
	}
}

func TestExecutionUsesBoundaryCancellationContextBeforeClaim(t *testing.T) {
	f := newFixture(t)
	r := f.approve(f.propose("synthetic.send"))
	f.engine.withExecutionAuthority = func(ctx context.Context, _ Scope, use func(context.Context, Authority) error) error {
		scoped, cancel := context.WithCancel(ctx)
		cancel()
		return use(scoped, f.authority)
	}
	if _, err := f.engine.Execute(context.Background(), r.Action.Key()); !errors.Is(err, context.Canceled) {
		t.Fatalf("retired boundary reached claim: %v", err)
	}
	saved, err := f.store.get(context.Background(), r.Action.Key())
	if err != nil {
		t.Fatal(err)
	}
	if saved.Status != Approved || f.calls != 0 {
		t.Fatalf("canceled boundary changed action: %s calls=%d", saved.Status, f.calls)
	}
}

func TestExecutionBoundaryCleanupFailurePersistsUncertainWithoutReplay(t *testing.T) {
	f := newFixture(t)
	r := f.approve(f.propose("synthetic.send"))
	f.engine.withExecutionAuthority = func(ctx context.Context, _ Scope, use func(context.Context, Authority) error) error {
		if err := use(ctx, f.authority); err != nil {
			return err
		}
		return errors.New("selected cleanup outcome unknown")
	}
	got, err := f.engine.Execute(context.Background(), r.Action.Key())
	if err != nil || got.Status != Uncertain || f.calls != 1 {
		t.Fatalf("cleanup uncertainty lost: %+v %v calls=%d", got, err, f.calls)
	}
	got, err = f.engine.Execute(context.Background(), r.Action.Key())
	if err != nil || got.Status != Uncertain || f.calls != 1 {
		t.Fatalf("uncertain effect replayed: %+v %v", got, err)
	}
}
