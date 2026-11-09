package personalreview

import (
	"context"
	"testing"
	"time"
)

// An interactive confirmation remains available until its action changes. A
// background deadline still prevents unattended work from resuming much later.
func TestHostControlsInteractiveAndBackgroundExpiry(t *testing.T) {
	for _, interactive := range []bool{true, false} {
		t.Run(map[bool]string{true: "interactive", false: "background"}[interactive], func(t *testing.T) {
			f := newFixture(t)
			f.engine.actionExpiry = func(scope Scope, at time.Time) time.Time {
				if interactive {
					return time.Time{}
				}
				return at.Add(30 * time.Minute)
			}
			r := f.propose("synthetic.save")
			f.now = f.now.Add(31 * time.Minute)
			if interactive {
				view, err := f.engine.OwnerSnapshot(context.Background())
				if err != nil || view.Requests[0].Action.ExpiresAt != nil {
					t.Fatalf("interactive expiry presentation: %+v %v", view, err)
				}
				r = f.approve(r)
				r = f.run(r)
				if r.Status != Succeeded || f.calls != 1 {
					t.Fatalf("interactive approval expired: %+v", r)
				}
			} else {
				_, err := f.engine.Resolve(context.Background(), r.Action.Key(), Resolution{Digest: r.Action.Digest, ExpectedRevision: r.Revision, Choice: ApproveOnce})
				if err == nil {
					t.Fatal("background approval remained valid after deadline")
				}
				r, err = f.engine.GetExecution(context.Background(), r.Action.Key())
				if err != nil || r.Status != Expired || f.calls != 0 {
					t.Fatalf("background expiry: %+v %v", r, err)
				}
			}
		})
	}
}
