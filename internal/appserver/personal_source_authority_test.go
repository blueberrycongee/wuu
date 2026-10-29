package appserver

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/blueberrycongee/wuu/internal/personalconnectors"
	"github.com/blueberrycongee/wuu/internal/personalreview"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/runtime"
)

// Logical source edits retain physical audit rows. A different store handle can
// retract the source while this host still retains the admitted implementation.
func TestPersonalReviewRetractedSourceRevokesIntentAndConnectorAuthority(t *testing.T) {
	f := newPersonalBoundaryFixture(t, nil)
	f.enableAuto()
	state, err := f.rt.RetainState()
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	ctx := runtime.WithInvocationState(f.sourceContext(), state)
	scope := personalreview.Scope{SessionID: f.id, TaskID: "conversation:" + f.id + ":" + strconv.Itoa(f.sourceSeq)}
	if err := f.host.actions.withAuthority(ctx, scope, func(a personalreview.Authority) error {
		if !a.AllowIntentReview || len(a.UserMessages) != 1 {
			t.Fatalf("active original source unavailable: %+v", a)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	actor := personalconnectors.Actor{ProductDomain: "personal", UserSpaceID: f.rt.UserSpaceID, SessionID: f.id}
	if _, err := f.host.actions.connectorAuthority(ctx, actor); err != nil {
		t.Fatal(err)
	}
	external := newAppserverTestStore(t, filepath.Join(f.rt.SessionDir, "documents"))
	page, err := external.ReadHistoryPage(f.id, f.sourceSeq, 1)
	if err != nil || len(page.Records) != 1 {
		t.Fatalf("source before edit: %+v %v", page, err)
	}
	if err := external.RewriteHistoryRecordsForEdit(f.id, nil, f.sourceSeq, page.HeadSeq); err != nil {
		t.Fatal(err)
	}
	physical, err := external.ReadHistoryPage(f.id, f.sourceSeq, 1)
	if err != nil || len(physical.Records) != 1 || physical.Records[0].Seq != f.sourceSeq {
		t.Fatalf("retraction must preserve the physical audit row: %+v %v", physical, err)
	}
	if err := f.host.actions.withAuthority(ctx, scope, func(a personalreview.Authority) error {
		if a.AllowIntentReview || len(a.UserMessages) != 0 || a.BoundaryReason == "" {
			t.Errorf("retracted source retained review authority: %+v", a)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.host.actions.connectorAuthority(ctx, actor); err == nil {
		t.Error("retracted latest human source retained connector authority")
	}
	if _, err := f.kit.Execute(ctx, f.call()); err == nil {
		t.Error("retracted source authorized a file effect")
	}
	if err := f.host.actions.drain(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(f.rt.RootDir, "note.txt")); !os.IsNotExist(err) {
		t.Error("retracted source wrote the target file")
	}
	f.client.mu.Lock()
	defer f.client.mu.Unlock()
	if len(f.client.requests) != 0 {
		t.Errorf("retracted source reached the intent reviewer: %d calls", len(f.client.requests))
	}
}

func TestPersonalReviewRetractionAfterModelDecisionPreventsDispatch(t *testing.T) {
	f := newPersonalBoundaryFixture(t, nil)
	f.enableAuto()
	external := newAppserverTestStore(t, filepath.Join(f.rt.SessionDir, "documents"))
	page, err := external.ReadHistoryPage(f.id, f.sourceSeq, 1)
	if err != nil || len(page.Records) != 1 {
		t.Fatalf("source before review: %+v %v", page, err)
	}
	retracted := make(chan error, 1)
	f.client.mu.Lock()
	f.client.onChat = func(call int, _ providers.ChatRequest) {
		if call == 2 {
			// The execution reviewer already sampled the original source. A
			// separate writer retracts it before the final guarded dispatch.
			retracted <- external.RewriteHistoryRecordsForEdit(f.id, nil, f.sourceSeq, page.HeadSeq)
		}
	}
	f.client.mu.Unlock()
	if _, err := f.kit.Execute(f.sourceContext(), f.call()); err == nil {
		t.Error("stale model decision dispatched after source retraction")
	}
	select {
	case err := <-retracted:
		if err != nil {
			t.Fatal(err)
		}
	default:
		t.Fatal("test did not reach the final model decision")
	}
	if _, err := os.Stat(filepath.Join(f.rt.RootDir, "note.txt")); !os.IsNotExist(err) {
		t.Fatal("source retraction did not fence the file effect")
	}
	records, err := f.host.actions.engine.Records(context.Background(), f.rt.UserSpaceID)
	if err != nil || len(records) != 1 || records[0].Status != personalreview.Stale && records[0].Status != personalreview.Denied {
		t.Fatalf("stale source did not terminate the old action: %+v %v", records, err)
	}
}
