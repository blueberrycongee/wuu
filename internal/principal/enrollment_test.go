package principal

import (
	"context"
	"errors"
	"testing"
)

func TestEnrolledOwnerRevalidatesDurableEnrollment(t *testing.T) {
	revoked := false
	checks := 0
	owner, err := EnrolledOwner(Identity{Kind: "user", Issuer: "fixture:transport", Tenant: "tenant", Subject: "sender"}, "enrollment", func() bool { return true }, func(context.Context) error {
		checks++
		if revoked {
			return errors.New("revoked")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = owner.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	revoked = true
	if err = owner.Check(context.Background()); err == nil {
		t.Fatal("existing owner proof survived durable revocation")
	}
	if checks != 2 {
		t.Fatalf("revalidation checks=%d", checks)
	}
	if owner.DurableAuthority() == LocalOwner().DurableAuthority() {
		t.Fatal("enrollment became installation authority")
	}
}
