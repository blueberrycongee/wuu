package principal

import (
	"context"
	"encoding/json"
	"testing"
)

// These cases protect transport provenance, not just the identity wire shape.
func TestUntrustedJSONCannotMintOwnerOrVerifiedIdentity(t *testing.T) {
	var verified Verified
	if err := json.Unmarshal([]byte(`{"Kind":"user","Issuer":"local","Tenant":"local","Subject":"owner","owner":true}`), &verified); err == nil && verified.Valid() {
		t.Fatal("wire claims minted a verified identity")
	}
	identity := Identity{Kind: "user", Issuer: "fixture", Tenant: "tenant", Subject: "owner"}
	caller, err := Authenticated(identity)
	if err != nil || caller.IsOwner() || !caller.Valid() {
		t.Fatalf("authenticated identity: %#v %v", caller, err)
	}
	got, ok := FromContext(WithContext(context.Background(), caller))
	if !ok || got.Identity() != identity || got.IsOwner() {
		t.Fatal("context lost provenance")
	}
	if _, err := Authenticated(Identity{Kind: "user", Subject: "missing issuer"}); err == nil {
		t.Fatal("ambiguous identity accepted")
	}
}
