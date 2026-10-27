package personalreview

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/blueberrycongee/wuu/internal/approvefor"
)

// Cancellation must invalidate pending approval in the original execution only;
// a durable dispatch claim may already have caused an effect and is never revoked.
func TestOwningExecutionCancellationInvalidatesPendingActions(t *testing.T) {
	for _, status := range []Status{Proposed, Ready, Approved, AwaitingConfirmation, Blocked} {
		t.Run(string(status), func(t *testing.T) {
			f := newFixture(t)
			r := f.propose("synthetic.save")
			if status == Approved {
				r = f.approve(r)
			} else if r.Status != status {
				r.Status = status
				var err error
				r, err = f.store.save(context.Background(), r, r.Revision, "host", f.now)
				if err != nil {
					t.Fatal(err)
				}
			}
			if err := f.engine.CancelExecution(context.Background(), r.Action.Scope.SpaceID, r.Action.Scope.SessionID); err != nil {
				t.Fatal(err)
			}
			current, err := f.engine.GetExecution(context.Background(), r.Action.Key())
			if err != nil || current.Status != Revoked {
				t.Fatalf("cancellation retained pending action: %+v %v", current, err)
			}
			if _, err := f.resolve(current, ApproveOnce); !errors.Is(err, ErrConflict) {
				t.Fatalf("cancelled action accepted approval: %v", err)
			}
			f.run(current)
			if f.calls != 0 {
				t.Fatal("cancelled action executed")
			}
			if err := f.engine.CancelExecution(context.Background(), r.Action.Scope.SpaceID, r.Action.Scope.SessionID); err != nil {
				t.Fatal(err)
			}
			replay, err := f.engine.GetExecution(context.Background(), r.Action.Key())
			if err != nil || replay.Revision != current.Revision {
				t.Fatalf("duplicate cancellation changed terminal record: %+v %v", replay, err)
			}
		})
	}
}

func TestOwningExecutionCancellationIsScopedAndPreservesClaims(t *testing.T) {
	f := newFixture(t)
	r := f.approve(f.propose("synthetic.save"))
	for _, scope := range [][2]string{{"other-space", r.Action.Scope.SessionID}, {r.Action.Scope.SpaceID, "other-session"}} {
		if err := f.engine.CancelExecution(context.Background(), scope[0], scope[1]); err != nil {
			t.Fatal(err)
		}
	}
	if current, err := f.engine.GetExecution(context.Background(), r.Action.Key()); err != nil || current.Status != Approved {
		t.Fatalf("cancellation crossed scope: %+v %v", current, err)
	}
	started, release := make(chan struct{}), make(chan struct{})
	op := f.engine.operations["synthetic.save"]
	op.Execute = func(context.Context, Action) (json.RawMessage, error) {
		close(started)
		<-release
		return nil, errors.New("synthetic lost receipt")
	}
	f.engine.operations["synthetic.save"] = op
	f.engine.review = func(context.Context, ReviewInput) (approvefor.Decision, error) {
		return approvefor.Decision{Outcome: approvefor.OutcomeAllow, Reason: "synthetic"}, nil
	}
	executed := make(chan Record, 1)
	failed := make(chan error, 1)
	go func() {
		got, err := f.engine.Execute(context.Background(), r.Action.Key())
		executed <- got
		failed <- err
	}()
	<-started
	cancelled := make(chan error, 1)
	go func() {
		cancelled <- f.engine.CancelExecution(context.Background(), r.Action.Scope.SpaceID, r.Action.Scope.SessionID)
	}()
	current, err := f.engine.GetExecution(context.Background(), r.Action.Key())
	if err != nil || current.Status != Executing {
		t.Fatalf("dispatch claim absent: %+v %v", current, err)
	}
	close(release)
	if err := <-failed; err != nil {
		t.Fatal(err)
	}
	if got := <-executed; got.Status != Uncertain {
		t.Fatalf("lost result should be uncertain: %+v", got)
	}
	if err := <-cancelled; err != nil {
		t.Fatal(err)
	}
	if current, err := f.engine.GetExecution(context.Background(), r.Action.Key()); err != nil || current.Status != Uncertain {
		t.Fatalf("cancellation rewrote claimed effect: %+v %v", current, err)
	}
}
