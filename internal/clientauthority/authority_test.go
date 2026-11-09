package clientauthority

import (
	"context"
	"strings"
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

// Sharing is explicitly attested on the inherited parent channel for one owner.
func TestDesktopMCPSharingBindsOwnerAndLifetime(t *testing.T) {
	binding := userspace.Binding{UserSpaceID: "owner", ProductDomain: "personal"}
	for _, tc := range []struct {
		account string
		shared  bool
	}{{"", false}, {"another", false}, {"owner", true}} {
		ctx, cancel := context.WithCancel(context.Background())
		admitted, err := AdmitDesktop(ctx, binding, strings.NewReader(`{"version":1,"user_space_id":"owner","product_domain":"personal","mcp_account_user_space_id":"`+tc.account+`"}`))
		if err != nil {
			t.Fatal(err)
		}
		if got := SharesMCPConnectors(admitted, binding); got != tc.shared {
			t.Fatalf("account %q: sharing=%v", tc.account, got)
		}
		if SharesMCPConnectors(admitted, userspace.Binding{UserSpaceID: "another", ProductDomain: "personal"}) {
			t.Fatal("cross-owner sharing")
		}
		connection := BindConnection(admitted)
		if SharesMCPConnectors(connection, binding) != tc.shared {
			t.Fatal("connection lost sharing binding")
		}
		cancel()
		if SharesMCPConnectors(context.WithoutCancel(connection), binding) {
			t.Fatal("expired owner retained sharing")
		}
	}
	if SharesMCPConnectors(context.Background(), binding) {
		t.Fatal("unadmitted host shares accounts")
	}
}
