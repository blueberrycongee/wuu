package appserver

import (
	"context"
	"io"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/blueberrycongee/wuu/internal/access"
	"github.com/blueberrycongee/wuu/internal/principal"
)

// Shared execution ownership must never become a connection's read authority;
// queued fanout must recheck both recipient policy and transport revocation.
func TestUserHostRequiresPrincipalAndRechecksFanout(t *testing.T) {
	h := &UserHost{}
	if err := h.Serve(context.Background(), strings.NewReader(""), io.Discard, nil); err == nil {
		t.Fatal("unauthenticated user-host transport admitted")
	}
	if err := h.SubscribeUserAttention(context.Background(), func(UserAttentionNotification) {}); err == nil {
		t.Fatal("unauthenticated attention subscriber admitted")
	}
	var revoked atomic.Bool
	caller, err := principal.Authenticated(principal.Identity{Kind: "device", Issuer: "fixture", Tenant: "fixture", Subject: "reader"})
	if err != nil {
		t.Fatal(err)
	}
	var ownerChecks atomic.Int32
	policy := access.PolicyFunc(func(_ context.Context, request access.Request) (access.Decision, error) {
		if request.Owner {
			ownerChecks.Add(1)
		}
		return access.Decision{Allow: !revoked.Load() && request.Principal == caller.Identity() && len(request.Resources) == 1 && request.Resources[0].ID == "visible"}, nil
	})
	ctx, cancel := context.WithCancel(principal.WithContext(context.Background(), caller))
	defer cancel()
	c := &userHostConnection{ctx: ctx, cancel: cancel, output: make(chan userHostOutput, 4)}
	h.server = &Server{out: h, access: connectionAccess{principal: principal.LocalOwner(), policy: policy}}
	h.connections = map[string]*userHostConnection{"reader": c}
	for _, id := range []string{"hidden", "visible"} {
		if err := h.server.writeNotification(NotificationThreadUpdated, ThreadUpdatedNotification{Thread: Thread{ID: id}}); err != nil {
			t.Fatal(err)
		}
	}
	if ownerChecks.Load() != 0 {
		t.Fatal("internal fanout manufactured owner authorization")
	}
	if len(c.output) != 1 {
		t.Fatalf("recipient scope leaked: %d frames", len(c.output))
	}
	frame := <-c.output
	if frame.guard == nil || frame.guard() != nil {
		t.Fatal("authorized frame missing guard")
	}
	revoked.Store(true)
	if frame.guard() == nil {
		t.Fatal("queued frame survived policy revocation")
	}
}
