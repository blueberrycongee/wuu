package appserver

import (
	"context"
	"errors"
	"testing"

	"github.com/blueberrycongee/wuu/internal/personalconnectors"
	"github.com/blueberrycongee/wuu/internal/session"
)

// A saved connector confirmation cannot outlive its task's host-owned authority.
func TestConnectorAuthorityRejectsForeignAndMissingSession(t *testing.T) {
	f := newPersonalBoundaryFixture(t, nil)
	for _, actor := range []personalconnectors.Actor{{ProductDomain: "coding", UserSpaceID: "local", SessionID: "s"}, {ProductDomain: "personal", UserSpaceID: "other", SessionID: "s"}, {ProductDomain: "personal", UserSpaceID: "local", SessionID: "missing"}} {
		if _, err := f.host.actions.connectorAuthority(f.owner, actor); err == nil {
			t.Fatalf("invalid actor accepted: %+v", actor)
		}
	}
}

func TestConnectorAuthorityChangesOnlyOnHumanRevision(t *testing.T) {
	f := newPersonalBoundaryFixture(t, nil)
	actor := personalconnectors.Actor{ProductDomain: "personal", UserSpaceID: f.rt.UserSpaceID, SessionID: f.id}
	first, err := f.host.actions.connectorAuthority(f.owner, actor)
	if err != nil || first == "" {
		t.Fatalf("valid authority unavailable %v", err)
	}
	if err := f.rt.Sessions.AppendHistoryRecord(f.id, session.HistoryRecord{Role: "assistant", Content: "Waiting for confirmation."}); err != nil {
		t.Fatal(err)
	}
	same, err := f.host.actions.connectorAuthority(f.owner, actor)
	if err != nil || same != first {
		t.Fatal("assistant output invalidated unchanged human authority", err)
	}
	if err := f.rt.Sessions.AppendHistoryRecord(f.id, session.HistoryRecord{Role: "user", Origin: "user", Content: "Cancel that request."}); err != nil {
		t.Fatal(err)
	}
	changed, err := f.host.actions.connectorAuthority(f.owner, actor)
	if err != nil || changed == first {
		t.Fatal("new human turn retained old authority", err)
	}
	f.rt.ActivePlugins = nil
	f.rt.PluginHost.RetirePlugin(context.Background(), "personal", errors.New("fixture provider disabled"))
	if _, err := f.host.actions.connectorAuthority(f.owner, actor); err == nil {
		t.Fatal("disabled plugin retained authority")
	}
}
