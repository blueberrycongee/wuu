package relay

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/remote/account"
	"github.com/blueberrycongee/wuu/internal/remote/secure"
	"github.com/blueberrycongee/wuu/internal/remote/wire"
)

func TestPersonalRelayPushRequiresOwnedExplicitRecipient(t *testing.T) {
	pusher := &recordingPusher{}
	reg, err := OpenRegistry("")
	if err != nil {
		t.Fatal(err)
	}
	srv := New(Options{Registry: reg, Pusher: pusher})
	host := secure.EncodeKey(mustID(t).Public())
	recipient := secure.EncodeKey(mustID(t).Public())
	foreign := secure.EncodeKey(mustID(t).Public())
	if err := reg.AddDevice(host, recipient, "phone", time.Now()); err != nil {
		t.Fatal(err)
	}
	source := &conn{pub: host, account: host, role: wire.RoleHost}
	location := &wire.UserLocation{Version: 1, UserSpaceID: "local", ProductDomain: "personal", AgentID: "main"}
	for _, msg := range []wire.RelayMsg{
		{Type: wire.TypePush, Hint: wire.PushAgentDone, UserLocation: location},
		{Type: wire.TypePush, To: foreign, Hint: wire.PushAgentDone, UserLocation: location},
		{Type: wire.TypePush, To: recipient, Hint: wire.PushAgentDone, ThreadID: "private", UserLocation: location},
		{Type: wire.TypePush, To: recipient, Hint: wire.PushNeedsInput, UserLocation: location},
		{Type: wire.TypePush, To: recipient, Hint: wire.PushAgentDone, UserLocation: &wire.UserLocation{Version: 2, UserSpaceID: "local", ProductDomain: "personal", AgentID: "main"}},
	} {
		srv.handlePush(source, msg)
	}
	if pusher.count() != 0 {
		t.Fatal("invalid Personal push reached provider")
	}
	srv.handlePush(&conn{pub: foreign, account: foreign, role: wire.RoleHost}, wire.RelayMsg{To: recipient, Hint: wire.PushAgentDone, UserLocation: location})
	srv.handlePush(&conn{pub: recipient, account: host, role: wire.RolePhone}, wire.RelayMsg{To: recipient, Hint: wire.PushAgentDone, UserLocation: location})
	if pusher.count() != 0 {
		t.Fatal("foreign or phone source reached provider")
	}
	srv.handlePush(source, wire.RelayMsg{Type: wire.TypePush, To: recipient, From: "spoof", Pub: "spoof", Hint: wire.PushAgentDone, UserLocation: location})
	if pusher.count() != 1 {
		t.Fatal("authorized push missing")
	}
	ev := pusher.events[0]
	if ev.Host != host || ev.Device != recipient || ev.Account != host || ev.UserLocation == nil || ev.UserLocation.AgentID != "main" {
		t.Fatalf("transport identity lost: %+v", ev)
	}
	if err := reg.RemoveDevice(host, recipient); err != nil {
		t.Fatal(err)
	}
	srv.mu.Lock()
	delete(srv.pushLast, recipient)
	srv.mu.Unlock()
	srv.handlePush(source, wire.RelayMsg{Type: wire.TypePush, To: recipient, Hint: wire.PushAgentDone, UserLocation: location})
	if pusher.count() != 1 {
		t.Fatal("revoked recipient received notification")
	}
}

func TestNativePersonalPayloadsAreContentFreeAndRecipientBound(t *testing.T) {
	ec, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	p := &NativePusher{apnsKey: ec, fcmKey: rsaKey, fcmProject: "test-project", apnsJWT: "injected-test-token", fcmToken: "injected-test-token", apnsExpiry: time.Now().Add(time.Hour), fcmExpiry: time.Now().Add(time.Hour)}
	if err := json.Unmarshal([]byte(`{"apns":{"topic":"ai.wuu.test"}}`), &p.config); err != nil {
		t.Fatal(err)
	}
	host := secure.EncodeKey(mustID(t).Public())
	recipient := secure.EncodeKey(mustID(t).Public())
	seq := int64(17)
	location := &wire.UserLocation{Version: 1, UserSpaceID: "local", ProductDomain: "personal", AgentID: "main", MessageSeq: &seq}
	expected := map[string]string{"version": "1", "product_domain": "personal", "user_space_id": "local", "host": host, "recipient": recipient, "agent_id": "main", "message_seq": "17"}
	calls := 0
	p.client = &http.Client{Transport: pushTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		var body map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		var got map[string]string
		switch r.URL.Host {
		case "api.push.apple.com":
			delete(body, "aps")
			raw, _ := json.Marshal(body)
			if err := json.Unmarshal(raw, &got); err != nil {
				t.Fatal(err)
			}
		case "fcm.googleapis.com":
			var message struct {
				Data map[string]string `json:"data"`
			}
			if err := json.Unmarshal(body["message"], &message); err != nil {
				t.Fatal(err)
			}
			got = message.Data
		default:
			t.Fatalf("unexpected auth/provider request: %s", r.URL.Host)
		}
		if !reflect.DeepEqual(got, expected) {
			t.Fatalf("unexpected Personal payload: %+v", got)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{}`)), Header: http.Header{}}, nil
	})}
	for _, platform := range []string{"ios", "android"} {
		if err := p.deliver(context.Background(), PushEvent{Host: host, Device: recipient, Hint: wire.PushAgentDone, UserLocation: location}, account.PushRegistration{Platform: platform, Token: strings.Repeat("ab", 32)}); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 2 {
		t.Fatal("missing provider requests")
	}
	invalid := *location
	invalid.Version = 2
	for _, ev := range []PushEvent{{Host: host, Device: recipient, Hint: wire.PushAgentDone, UserLocation: &invalid}, {Host: "spoof", Device: recipient, Hint: wire.PushAgentDone, UserLocation: location}, {Host: host, Hint: wire.PushAgentDone, UserLocation: location}} {
		if err := p.deliver(context.Background(), ev, account.PushRegistration{Platform: "ios", Token: strings.Repeat("ab", 32)}); err == nil {
			t.Fatal("invalid Personal event was accepted")
		}
	}
	if calls != 2 {
		t.Fatal("invalid payload reached provider")
	}
}
