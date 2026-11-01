package principal

import (
	"context"
	"encoding/json"
	"errors"
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

// A receiver may retain its genuine connection authority while requiring a
// second live source check. Constraints can revoke, never promote provenance.
func TestConstraintOnlyAttenuatesVerifiedPrincipal(t *testing.T) {
	revoked := false
	check := func(context.Context) error {
		if revoked {
			return errors.New("source revoked")
		}
		return nil
	}
	owner, err := LocalOwner().Constrain(check)
	if err != nil || !owner.IsOwner() || owner.Identity() != LocalOwner().Identity() {
		t.Fatal("genuine owner provenance changed")
	}
	guest, _ := Authenticated(Identity{Kind: "agent", Issuer: "test", Tenant: "local", Subject: "guest"})
	limited, err := guest.Constrain(check)
	if err != nil || limited.IsOwner() {
		t.Fatal("constraint promoted guest")
	}
	if owner.DurableAuthority() != "" {
		t.Fatal("transient source constraint retained independent durable authority")
	}
	if owner.Check(context.Background()) != nil {
		t.Fatal("live source denied")
	}
	revoked = true
	if owner.Check(context.Background()) == nil || limited.Check(context.Background()) == nil {
		t.Fatal("revoked source survived constraint")
	}
	if _, err = owner.Constrain(nil); err == nil {
		t.Fatal("missing check accepted")
	}
}
