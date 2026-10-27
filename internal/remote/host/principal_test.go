package host

import (
	"context"
	"testing"

	"github.com/blueberrycongee/wuu/internal/remote/account"
)

func TestPairedPrincipalCannotCrossAccountAuthoritySwitch(t *testing.T) {
	store, pub := makeStore(t, "owner")
	h := &Host{store: store, baseCtx: context.Background()}
	caller, err := h.devicePrincipal(pub)
	if err != nil || caller.Check(context.Background()) != nil || !caller.IsOwner() {
		t.Fatalf("paired caller: %v", err)
	}
	if err := store.SetAccount(&account.Credentials{Server: "https://identity.invalid", Username: "different-authority", Token: "synthetic-not-used"}); err != nil {
		t.Fatal(err)
	}
	if caller.Check(context.Background()) == nil {
		t.Fatal("old local pairing retained authority after account switch")
	}
}
