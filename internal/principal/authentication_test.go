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
