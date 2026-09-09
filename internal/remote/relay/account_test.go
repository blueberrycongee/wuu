package relay

import (
	"context"
	"encoding/base64"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/blueberrycongee/wuu/internal/remote/account"
	"github.com/blueberrycongee/wuu/internal/remote/secure"
	"github.com/blueberrycongee/wuu/internal/remote/wire"
)

func TestAccountRelayIsolationAndRevocation(t *testing.T) {
	store, err := account.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	srv := New(Options{Accounts: store, AllowRegistration: true})
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	url := "ws" + strings.TrimPrefix(ts.URL, "http") + "/v1/connect"
	enroll := func(user, role string, register bool) (*secure.Identity, account.Session) {
		id := mustID(t)
		in := account.Login{Username: user, Password: "correct horse battery", Pub: secure.EncodeKey(id.Public()), Role: role, Name: role, Proof: base64.RawURLEncoding.EncodeToString(id.SignRelayAuth([]byte("wuu/account/enroll/v1:"+user), role))}
		path := "/login"
		if register {
			path = "/register"
		}
		var session account.Session
		if err := account.Request(context.Background(), ts.URL, "", "POST", path, in, &session); err != nil {
			t.Fatal(err)
		}
		return id, session
	}
	host, _ := enroll("alice", "host", true)
	second, _ := enroll("alice", "host", false)
	phone, p := enroll("alice", "phone", false)
	foreign, f := enroll("bobby", "host", true)
	hc := dialRelay(t, context.Background(), url)
	defer hc.ws.CloseNow()
	hc.auth(host, wire.RoleHost)
	h2 := dialRelay(t, context.Background(), url)
	defer h2.ws.CloseNow()
	h2.auth(second, wire.RoleHost)
	fc := dialRelay(t, context.Background(), url)
	defer fc.ws.CloseNow()
	fc.auth(foreign, wire.RoleHost)
	pc := dialRelay(t, context.Background(), url)
	defer pc.ws.CloseNow()
	pc.send(wire.RelayMsg{Type: wire.TypeHello, Proto: 1, Role: wire.RolePhone, Pub: p.Pub, To: secure.EncodeKey(host.Public())})
	ch := pc.recv()
	nonce, _ := base64.RawURLEncoding.DecodeString(ch.Nonce)
	pc.send(wire.RelayMsg{Type: wire.TypeAuth, Sig: base64.RawURLEncoding.EncodeToString(phone.SignRelayAuth(nonce, wire.RolePhone))})
	if got := pc.recv(); got.Type != wire.TypeAuthOK || got.Online == nil || !*got.Online {
		t.Fatalf("phone auth: %+v", got)
	}
	for _, dest := range []struct {
		id *secure.Identity
		c  *testClient
	}{{host, hc}, {second, h2}} {
		pc.send(wire.RelayMsg{Type: wire.TypeFrame, To: secure.EncodeKey(dest.id.Public()), Payload: "opaque"})
		if got := dest.c.recvType(wire.TypeFrame); got.From != p.Pub || got.Payload != "opaque" {
			t.Fatalf("routing: %+v", got)
		}
	}
	pc.send(wire.RelayMsg{Type: wire.TypeFrame, To: f.Pub, Payload: "forbidden"})
	if got := pc.recvType(wire.TypeDeliverErr); got.Code != wire.CodeUnauthorized {
		t.Fatalf("cross account: %+v", got)
	}
	if err := account.Request(context.Background(), ts.URL, f.Token, "DELETE", "/devices/"+p.Pub, nil, nil); err == nil {
		t.Fatal("foreign account revoked phone")
	}
	pc.send(wire.RelayMsg{Type: wire.TypeDeviceAdd, Pub: f.Pub})
	if got := pc.recvType(wire.TypeErr); got.Code != wire.CodeUnauthorized {
		t.Fatal("legacy enrollment bypass")
	}
	if err := account.Request(context.Background(), ts.URL, p.Token, "POST", "/logout", nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, ok := store.Device(p.Pub); ok {
		t.Fatal("logout retained device")
	}
	// A previously valid key must fail fresh WebSocket authentication after logout.
	retry := dialRelay(t, context.Background(), url)
	defer retry.ws.CloseNow()
	retry.send(wire.RelayMsg{Type: wire.TypeHello, Proto: 1, Role: wire.RolePhone, Pub: p.Pub, To: secure.EncodeKey(host.Public())})
	ch = retry.recv()
	nonce, _ = base64.RawURLEncoding.DecodeString(ch.Nonce)
	retry.send(wire.RelayMsg{Type: wire.TypeAuth, Sig: base64.RawURLEncoding.EncodeToString(phone.SignRelayAuth(nonce, wire.RolePhone))})
	if got := retry.recv(); got.Type != wire.TypeAuthErr {
		t.Fatalf("revoked key: %+v", got)
	}
}
