package principal

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

type controlledAuthenticator struct {
	revoked  atomic.Bool
	identity Identity
	expires  time.Time
}

func (a *controlledAuthenticator) Authenticate(context.Context, Credential) (Authentication, error) {
	if a.revoked.Load() {
		return Authentication{}, errors.New("revoked")
	}
	return Authentication{Identity: a.identity, ExpiresAt: a.expires}, nil
}
func TestAuthenticatedConnectionRechecksExpiryRevocationAndIdentity(t *testing.T) {
	adapter := &controlledAuthenticator{identity: Identity{Kind: "user", Issuer: "issuer", Tenant: "tenant", Subject: "subject"}, expires: time.Now().Add(time.Minute)}
	verified, err := Authenticate(context.Background(), adapter, Credential{Scheme: "fixture", Token: "signed", Audience: "host"})
	if err != nil {
		t.Fatal(err)
	}
	if err := verified.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	adapter.revoked.Store(true)
	if err := verified.Check(context.Background()); err == nil {
		t.Fatal("revoked session retained authority")
	}
	adapter.revoked.Store(false)
	adapter.identity.Subject = "other"
	if err := verified.Check(context.Background()); err == nil {
		t.Fatal("identity changed without reconnect")
	}
	adapter.expires = time.Now().Add(-time.Second)
	if _, err := Authenticate(context.Background(), adapter, Credential{}); err == nil {
		t.Fatal("expired identity accepted")
	}
	adapter.expires = time.Time{}
	if _, err := Authenticate(context.Background(), adapter, Credential{}); err == nil {
		t.Fatal("unbounded provider session accepted")
	}
}

// A matching identity or a live owner connection is not evidence of durable
// installation authority. Only the embedding's trusted local pipe supplies it.
func TestDurableAuthorityCannotBeReconstructedFromIdentity(t *testing.T) {
	local := LocalOwner()
	if local.DurableAuthority() != "local-installation" {
		t.Fatal("local installation provenance missing")
	}
	reconstructed, err := Authenticated(local.Identity())
	if err != nil {
		t.Fatal(err)
	}
	if reconstructed.DurableAuthority() != "" {
		t.Fatal("identity reconstructed durable authority")
	}
	owner, err := OwnerConnection(local.Identity(), func() bool { return true })
	if err != nil {
		t.Fatal(err)
	}
	if owner.DurableAuthority() != "" {
		t.Fatal("owner flag reconstructed durable account validity")
	}
}
