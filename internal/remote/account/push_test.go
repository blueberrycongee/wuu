package account

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/blueberrycongee/wuu/internal/remote/pgtest"
)

func TestPushRegistrationIsDeviceOwnedAndRevokedWithIdentity(t *testing.T) {
	s, err := Open(pgtest.URL(t))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	phone, err := s.Login(loginInput(t, "alice", "phone"), true)
	if err != nil {
		t.Fatal(err)
	}
	host, err := s.Login(loginInput(t, "alice", "host"), false)
	if err != nil {
		t.Fatal(err)
	}
	h := NewHTTP(s, false)
	h.PushPlatforms = []string{"ios"}
	server := httptest.NewServer(h)
	defer server.Close()
	register := func(token string, body any) error {
		return Request(context.Background(), server.URL, token, "POST", "/push", body, nil)
	}
	if err = register(phone.Token, PushRegistration{Platform: "ios", Token: "test-native-token"}); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Push("bobby", phone.Pub); ok {
		t.Fatal("cross-account push registration exposed")
	}
	if err = register(host.Token, PushRegistration{Platform: "ios", Token: "host-token"}); err == nil {
		t.Fatal("host registered a phone notification")
	}
	if err = register(phone.Token, map[string]string{"platform": "ios", "token": "other", "pub": host.Pub}); err == nil {
		t.Fatal("caller supplied another device id")
	}
	var status map[string]any
	if err = Request(context.Background(), server.URL, phone.Token, "GET", "/push", nil, &status); err != nil || status["enabled"] != true || status["token"] != nil {
		t.Fatal("push status exposed token or lost enrollment")
	}
	if err = register(phone.Token, PushRegistration{Platform: "android", Token: "unconfigured"}); err == nil {
		t.Fatal("unconfigured provider accepted")
	}
	if err = s.Revoke("alice", phone.Pub); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Push("alice", phone.Pub); ok {
		t.Fatal("revocation left a push destination")
	}
}
