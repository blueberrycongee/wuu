package appserver

import (
	"context"
	"testing"

	"github.com/blueberrycongee/wuu/internal/personalconnectors"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/session"
)

// A running turn can propose an action after an unrelated newer human message.
// Its automatic decision must still depend on the original source it reviewed.
func TestConnectorAutomaticReviewBindsOriginalSourcePastNewerMessage(t *testing.T) {
	for _, retract := range []bool{false, true} {
		name := "unchanged"
		if retract {
			name = "retracted-original"
		}
		t.Run(name, func(t *testing.T) {
			f := newPersonalBoundaryFixture(t, nil)
			if _, err := f.rt.Sessions.AppendHistoryRecordReturningSeq(f.id, session.HistoryRecord{Role: "user", Origin: "user", Content: "An unrelated newer question."}); err != nil {
				t.Fatal(err)
			}
			actor := personalconnectors.Actor{ProductDomain: "personal", UserSpaceID: f.rt.UserSpaceID, SessionID: f.id}
			p := f.host.actionsForTest(t)
			ctx := f.sourceContext()
			authority, err := p.connectorAuthority(ctx, actor)
			if err != nil {
				t.Fatal(err)
			}
			modelCalls := 0
			f.client.mu.Lock()
			f.client.onChat = func(_ int, _ providers.ChatRequest) {
				modelCalls++
				if retract {
					if err := f.rt.Sessions.RewriteHistoryRecordsForEdit(f.id, nil, f.sourceSeq, f.sourceSeq); err != nil {
						t.Fatal(err)
					}
				}
			}
			f.client.mu.Unlock()
			action := personalconnectors.Result{State: "awaiting_confirmation", ActionID: "source-action", Digest: "exact-action", ConnectionID: "account", AccountID: "me", Domain: "personal", Operation: "gmail.send", Resource: "me", Summary: "Send the requested message", ArgumentsJSON: `{"to":["test@example.invalid"],"body":"Requested message"}`}
			decision, err := p.reviewConnector(ctx, actor, action)
			if err != nil || decision.Outcome != "allow" || modelCalls != 1 {
				t.Fatalf("review: %+v %v calls=%d", decision, err, modelCalls)
			}
			effects := 0
			err = p.withConnectorAuthorization(ctx, actor, authority, func(guarded context.Context) error {
				if err := p.checkConnectorReview(guarded, actor, action, &decision); err != nil {
					return err
				}
				effects++
				return nil
			})
			if retract && (err == nil || effects != 0) {
				t.Fatalf("retracted original source dispatched: effects=%d err=%v", effects, err)
			}
			if !retract && (err != nil || effects != 1) {
				t.Fatalf("unchanged original source blocked: effects=%d err=%v", effects, err)
			}
		})
	}
}
