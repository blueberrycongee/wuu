package appserver

import (
	"context"
	"errors"
	"testing"

	"github.com/blueberrycongee/wuu/internal/personalconnectors"
	"github.com/blueberrycongee/wuu/internal/runtime"
	"github.com/blueberrycongee/wuu/internal/session"
)

func TestPersonalConnectorCoordinationExecutesEffectAndRejectsStaleAuthority(t *testing.T) {
	f := newPersonalBoundaryFixture(t, nil)
	state, err := f.rt.RetainState()
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	ctx := runtime.WithInvocationState(f.owner, state)
	actor := personalconnectors.Actor{ProductDomain: "personal", UserSpaceID: f.rt.UserSpaceID, SessionID: f.id}
	snapshot, err := f.host.server.connectorSnapshot(ctx, actor)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	effectErr := errors.New("synthetic effect outcome")
	err = f.host.server.connectorCommit(ctx, actor, snapshot, func(effectCtx context.Context) error {
		calls++
		if !runtime.HasInvocationState(effectCtx) {
			t.Error("effect lost captured generation")
		}
		return effectErr
	})
	if !errors.Is(err, effectErr) || calls != 1 {
		t.Fatalf("effect outcome=%v calls=%d", err, calls)
	}
	if err := f.rt.Sessions.AppendHistoryRecord(f.id, session.HistoryRecord{Role: "user", Origin: "user", Content: "Cancel that request."}); err != nil {
		t.Fatal(err)
	}
	err = f.host.server.connectorCommit(ctx, actor, snapshot, func(context.Context) error { calls++; return nil })
	if err == nil || calls != 1 {
		t.Fatalf("stale authority executed effect: err=%v calls=%d", err, calls)
	}
}
