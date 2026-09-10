package account

import (
	"testing"

	"github.com/blueberrycongee/wuu/internal/remote/pgtest"

	"github.com/blueberrycongee/wuu/internal/remote/secure"
)

func loginInput(t *testing.T, user, role string) Login {
	t.Helper()
	id, err := secure.NewIdentity()
	if err != nil {
		t.Fatal(err)
	}
	return Login{Username: user, Password: "correct horse battery", Pub: secure.EncodeKey(id.Public()), Role: role, Name: role, Proof: enc.EncodeToString(id.SignRelayAuth([]byte("wuu/account/enroll/v1:"+user), role))}
}
func TestAccountIsolationRevocationAndRecovery(t *testing.T) {
	path := pgtest.URL(t)
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { s.Close() }()
	alice := loginInput(t, "alice", "host")
	a, err := s.Login(alice, true)
	if err != nil {
		t.Fatal(err)
	}
	bob := loginInput(t, "bobby", "host")
	b, err := s.Login(bob, true)
	if err != nil {
		t.Fatal(err)
	}
	phone := loginInput(t, "alice", "phone")
	p, err := s.Login(phone, false)
	if err != nil {
		t.Fatal(err)
	}
	list, err := s.Devices("alice")
	if err != nil || len(list) != 2 {
		t.Fatalf("devices %v %v", list, err)
	}
	if err = s.Revoke("bobby", p.Pub); err != ErrUnauthorized {
		t.Fatalf("cross-account revoke: %v", err)
	}
	if err = s.Revoke("alice", p.Pub); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Authenticate(p.Token); err != ErrUnauthorized {
		t.Fatalf("revoked token accepted: %v", err)
	}
	if _, ok := s.Device(p.Pub); ok {
		t.Fatal("revoked key remains enrolled")
	}
	// Database reopening preserves identities and token hashes.
	s.Close()
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if d, err := s.Authenticate(a.Token); err != nil || d.Account != "alice" {
		t.Fatalf("persisted session: %v %v", d, err)
	}
	next, err := s.Reset("alice", a.Recovery, "a different safe password", true)
	if err != nil || next == a.Recovery || next == "" {
		t.Fatalf("recovery: %v", err)
	}
	if _, err = s.Authenticate(a.Token); err != ErrUnauthorized {
		t.Fatal("recovery preserved old session")
	}
	if _, ok := s.Device(a.Pub); ok {
		t.Fatal("recovery preserved old device")
	}
	if _, err = s.Authenticate(b.Token); err != nil {
		t.Fatal("recovery affected another account")
	}
	if _, err = s.Reset("alice", a.Recovery, "another good password", true); err != ErrUnauthorized {
		t.Fatal("recovery code reused")
	}
	if _, err = s.Login(alice, false); err != ErrUnauthorized {
		t.Fatal("old password accepted")
	}
	alice.Password = "a different safe password"
	if _, err = s.Login(alice, false); err != nil {
		t.Fatal(err)
	}
}
func TestEnrollmentRequiresKeyPossessionAndUniqueOwnership(t *testing.T) {
	s, err := Open(pgtest.URL(t))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	in := loginInput(t, "alice", "host")
	bad := in
	bad.Proof = "bad"
	if _, err = s.Login(bad, true); err != ErrUnauthorized {
		t.Fatal("invalid key proof accepted")
	}
	if _, err = s.Login(in, true); err != nil {
		t.Fatal(err)
	}
	in.Role = "phone"
	if _, err = s.Login(in, false); err != ErrUnauthorized {
		t.Fatal("role altered without proof")
	}
}
