package clientauthority

import (
	"context"
	"github.com/blueberrycongee/wuu/internal/principal"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/blueberrycongee/wuu/internal/userspace"
)

// Admission is a separate desktop-parent pipe, never a field from JSONL.
func TestDesktopAdmissionRequiresSeparateMatchingBootstrap(t *testing.T) {
	binding := userspace.Binding{UserSpaceID: "local", ProductDomain: "personal"}
	for _, input := range []string{"", `{}`, `{"version":1,"user_space_id":"other","product_domain":"personal"}`, `{"version":1,"user_space_id":"local","product_domain":"coding"}`, `{"version":1,"user_space_id":"local","product_domain":"personal","owner":true}`} {
		if _, err := AdmitDesktop(context.Background(), binding, strings.NewReader(input)); err == nil {
			t.Fatalf("invalid bootstrap admitted: %s", input)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	owner, err := AdmitDesktop(ctx, binding, strings.NewReader(`{"version":1,"user_space_id":"local","product_domain":"personal"}`))
	if err != nil {
		t.Fatal(err)
	}
	if err := RequireOwner(owner, binding); err != nil {
		t.Fatal(err)
	}
	if err := RequireOwner(context.Background(), binding); err == nil {
		t.Fatal("plain transport became owner")
	}
	if err := RequireOwner(owner, userspace.Binding{UserSpaceID: "other", ProductDomain: "personal"}); err == nil {
		t.Fatal("cross-space owner admitted")
	}
	if err := RequireOwner(owner, userspace.Binding{}); err == nil {
		t.Fatal("unbound transport admitted")
	}
	cancel()
	if err := RequireOwner(context.WithoutCancel(owner), binding); err == nil {
		t.Fatal("detached callback retained closed connection authority")
	}
}

// A host-minted owner proof identifies the exact user binding; it does not grant
// product-specific action policy or authorize another domain.
func TestDesktopOwnerProofHasNoProductPrivilege(t *testing.T) {
	binding := userspace.Binding{UserSpaceID: "local", ProductDomain: "coding"}
	owner, err := AdmitDesktop(context.Background(), binding, strings.NewReader(`{"version":1,"user_space_id":"local","product_domain":"coding"}`))
	if err != nil {
		t.Fatal(err)
	}
	if err := RequireOwner(owner, binding); err != nil {
		t.Fatal(err)
	}
	if err := RequireOwner(owner, userspace.Binding{UserSpaceID: "local", ProductDomain: "personal"}); err == nil {
		t.Fatal("proof crossed product binding")
	}
}

func TestEnrolledOwnerTransportAdmissionRechecksRevocation(t *testing.T) {
	binding := userspace.Binding{UserSpaceID: "local", ProductDomain: "personal"}
	identity := principal.Identity{Kind: "user", Issuer: "slack", Tenant: "team", Subject: "owner"}
	guest, _ := principal.Authenticated(identity)
	if _, err := AdmitOwnerTransport(context.Background(), binding, guest); err == nil {
		t.Fatal("authenticated but unpaired user became owner")
	}
	var live atomic.Bool
	live.Store(true)
	owner, _ := principal.OwnerConnection(identity, live.Load)
	ctx, cancel := context.WithCancel(context.Background())
	admitted, err := AdmitOwnerTransport(ctx, binding, owner)
	if err != nil {
		t.Fatal(err)
	}
	if err := RequireOwner(admitted, binding); err != nil {
		t.Fatal(err)
	}
	if err := RequireOwner(admitted, userspace.Binding{UserSpaceID: "other", ProductDomain: "personal"}); err == nil {
		t.Fatal("cross-space owner admitted")
	}
	live.Store(false)
	if err := RequireOwner(admitted, binding); err == nil {
		t.Fatal("revoked enrollment retained authority")
	}
	live.Store(true)
	cancel()
	if err := RequireOwner(context.WithoutCancel(admitted), binding); err == nil {
		t.Fatal("detached callback retained authority")
	}
}
