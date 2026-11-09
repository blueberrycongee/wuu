package relay

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/blueberrycongee/wuu/internal/remote/secure"
	"github.com/blueberrycongee/wuu/internal/remote/wire"
)

type testClient struct {
	t  *testing.T
	ws *websocket.Conn
}

func dialRelay(t *testing.T, ctx context.Context, url string) *testClient {
	t.Helper()
	ws, _, err := websocket.Dial(ctx, url, nil)
	if err != nil {
		t.Fatalf("dial relay: %v", err)
	}
	ws.SetReadLimit(16 << 20)
	return &testClient{t: t, ws: ws}
}

func (c *testClient) send(msg wire.RelayMsg) {
	c.t.Helper()
	data, err := json.Marshal(msg)
	if err != nil {
		c.t.Fatalf("marshal: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.ws.Write(ctx, websocket.MessageText, data); err != nil {
		c.t.Fatalf("write: %v", err)
	}
}

func (c *testClient) recv() wire.RelayMsg {
	c.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, data, err := c.ws.Read(ctx)
	if err != nil {
		c.t.Fatalf("read: %v", err)
	}
	var msg wire.RelayMsg
	if err := json.Unmarshal(data, &msg); err != nil {
		c.t.Fatalf("unmarshal %s: %v", data, err)
	}
	return msg
}

// recvType reads messages until one of the wanted type arrives, skipping
// asynchronous notifications (presence and the like).
func (c *testClient) recvType(want string) wire.RelayMsg {
	c.t.Helper()
	for i := 0; i < 10; i++ {
		msg := c.recv()
		if msg.Type == want {
			return msg
		}
	}
	c.t.Fatalf("no %s message after 10 reads", want)
	return wire.RelayMsg{}
}

func (c *testClient) auth(id *secure.Identity, role string) {
	c.t.Helper()
	c.send(wire.RelayMsg{Type: wire.TypeHello, Proto: wire.ProtoVersion, Role: role, Pub: secure.EncodeKey(id.Public())})
	challenge := c.recv()
	if challenge.Type != wire.TypeChallenge {
		c.t.Fatalf("expected challenge, got %+v", challenge)
	}
	nonce, err := base64.RawURLEncoding.DecodeString(challenge.Nonce)
	if err != nil {
		c.t.Fatalf("decode nonce: %v", err)
	}
	c.send(wire.RelayMsg{Type: wire.TypeAuth, Sig: base64.RawURLEncoding.EncodeToString(id.SignRelayAuth(nonce, role))})
	ok := c.recv()
	if ok.Type != wire.TypeAuthOK {
		c.t.Fatalf("expected auth_ok, got %+v", ok)
	}
}

type recordingPusher struct {
	mu     sync.Mutex
	events []PushEvent
}

func (p *recordingPusher) Push(_ context.Context, ev PushEvent) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.events = append(p.events, ev)
}

func (p *recordingPusher) count() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.events)
}

func newTestRelay(t *testing.T, regPath string, pusher Pusher) (*Server, string) {
	t.Helper()
	reg, err := OpenRegistry(regPath)
	if err != nil {
		t.Fatalf("OpenRegistry: %v", err)
	}
	srv := New(Options{
		Registry:        reg,
		Pusher:          pusher,
		Logf:            t.Logf,
		PushMinInterval: 50 * time.Millisecond,
	})
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return srv, "ws" + strings.TrimPrefix(ts.URL, "http") + "/v1/connect"
}

func TestAuthFrameRoutingAndPresence(t *testing.T) {
	ctx := context.Background()
	regPath := filepath.Join(t.TempDir(), "relay.json")
	srv, url := newTestRelay(t, regPath, nil)

	host := mustID(t)
	phone := mustID(t)
	hostPub := secure.EncodeKey(host.Public())
	phonePub := secure.EncodeKey(phone.Public())

	hc := dialRelay(t, ctx, url)
	hc.auth(host, wire.RoleHost)

	// A phone that has never paired is refused.
	stray := dialRelay(t, ctx, url)
	stray.send(wire.RelayMsg{Type: wire.TypeHello, Proto: wire.ProtoVersion, Role: wire.RolePhone, Pub: phonePub})
	ch := stray.recv()
	nonce, _ := base64.RawURLEncoding.DecodeString(ch.Nonce)
	stray.send(wire.RelayMsg{Type: wire.TypeAuth, Sig: base64.RawURLEncoding.EncodeToString(phone.SignRelayAuth(nonce, wire.RolePhone))})
	if msg := stray.recv(); msg.Type != wire.TypeAuthErr || msg.Code != wire.CodeUnknownDevice {
		t.Fatalf("expected unknown_device auth_err, got %+v", msg)
	}

	// Enroll the phone, then it can authenticate.
	hc.send(wire.RelayMsg{Type: wire.TypeDeviceAdd, Pub: phonePub, Name: "pixel"})
	hc.recvType(wire.TypeOK)

	pc := dialRelay(t, ctx, url)
	pc.auth(phone, wire.RolePhone)

	// Host is told the phone came online.
	presence := hc.recvType(wire.TypePresence)
	if presence.Pub != phonePub || presence.Online == nil || !*presence.Online {
		t.Fatalf("presence = %+v", presence)
	}

	// Frames route phone -> host and host -> phone.
	pc.send(wire.RelayMsg{Type: wire.TypeFrame, Payload: base64.RawURLEncoding.EncodeToString([]byte("up"))})
	got := hc.recvType(wire.TypeFrame)
	if got.From != phonePub || got.Payload != base64.RawURLEncoding.EncodeToString([]byte("up")) {
		t.Fatalf("host frame = %+v", got)
	}
	hc.send(wire.RelayMsg{Type: wire.TypeFrame, To: phonePub, Payload: base64.RawURLEncoding.EncodeToString([]byte("down"))})
	got = pc.recvType(wire.TypeFrame)
	if got.From != hostPub || got.Payload != base64.RawURLEncoding.EncodeToString([]byte("down")) {
		t.Fatalf("phone frame = %+v", got)
	}

	// Sending to an unenrolled device fails cleanly.
	hc.send(wire.RelayMsg{Type: wire.TypeFrame, To: secure.EncodeKey(mustID(t).Public()), Payload: "eA"})
	if msg := hc.recvType(wire.TypeDeliverErr); msg.Code != wire.CodeUnknownDevice {
		t.Fatalf("deliver_err = %+v", msg)
	}

	// Phone disconnect surfaces as presence(offline) at the host.
	_ = pc.ws.Close(websocket.StatusNormalClosure, "bye")
	presence = hc.recvType(wire.TypePresence)
	if presence.Pub != phonePub || presence.Online == nil || *presence.Online {
		t.Fatalf("offline presence = %+v", presence)
	}

	// Now frames to the phone report offline.
	hc.send(wire.RelayMsg{Type: wire.TypeFrame, To: phonePub, Payload: "eA"})
	if msg := hc.recvType(wire.TypeDeliverErr); msg.Code != wire.CodeOffline {
		t.Fatalf("deliver_err = %+v", msg)
	}

	// Registry survives restart.
	reg2, err := OpenRegistry(regPath)
	if err != nil {
		t.Fatalf("reopen registry: %v", err)
	}
	if !reg2.HasDevice(hostPub, phonePub) {
		t.Fatalf("device not persisted")
	}
	_ = srv
}

func TestPairingGuestFlow(t *testing.T) {
	ctx := context.Background()
	_, url := newTestRelay(t, "", nil)

	host := mustID(t)
	hc := dialRelay(t, ctx, url)
	hc.auth(host, wire.RoleHost)

	hc.send(wire.RelayMsg{Type: wire.TypePairOpen, PairingID: "pid-1"})
	hc.recvType(wire.TypeOK)

	// Guest posts an offer without authenticating.
	guest := dialRelay(t, ctx, url)
	guest.send(wire.RelayMsg{Type: wire.TypePairOffer, PairingID: "pid-1", Payload: "b2ZmZXI"})

	offer := hc.recvType(wire.TypePairOffer)
	if offer.PairingID != "pid-1" || offer.Payload != "b2ZmZXI" {
		t.Fatalf("offer = %+v", offer)
	}
	hc.send(wire.RelayMsg{Type: wire.TypePairAnswer, PairingID: "pid-1", Payload: "YW5zd2Vy"})

	answer := guest.recvType(wire.TypePairAnswer)
	if answer.Payload != "YW5zd2Vy" {
		t.Fatalf("answer = %+v", answer)
	}

	// A guest for a pairing id nobody opened is turned away.
	lost := dialRelay(t, ctx, url)
	lost.send(wire.RelayMsg{Type: wire.TypePairOffer, PairingID: "nope", Payload: "eA"})
	if msg := lost.recvType(wire.TypePairErr); msg.Code != wire.CodeNoSuchPairing {
		t.Fatalf("pair_err = %+v", msg)
	}
}

func TestPushForwardingAndThrottle(t *testing.T) {
	ctx := context.Background()
	pusher := &recordingPusher{}
	_, url := newTestRelay(t, "", pusher)

	host := mustID(t)
	phone := mustID(t)
	phonePub := secure.EncodeKey(phone.Public())

	hc := dialRelay(t, ctx, url)
	hc.auth(host, wire.RoleHost)
	hc.send(wire.RelayMsg{Type: wire.TypeDeviceAdd, Pub: phonePub, Name: "pixel"})
	hc.recvType(wire.TypeOK)

	hc.send(wire.RelayMsg{Type: wire.TypePush, To: phonePub, Hint: wire.PushNeedsInput})
	hc.send(wire.RelayMsg{Type: wire.TypePush, To: phonePub, Hint: wire.PushNeedsInput})
	// Device list forces a round trip so both pushes have been processed.
	hc.send(wire.RelayMsg{Type: wire.TypeDeviceList})
	hc.recvType(wire.TypeDevices)
	if got := pusher.count(); got != 1 {
		t.Fatalf("push count after burst = %d, want 1 (throttled)", got)
	}

	time.Sleep(60 * time.Millisecond)
	hc.send(wire.RelayMsg{Type: wire.TypePush, To: phonePub, Hint: wire.PushAgentDone})
	hc.send(wire.RelayMsg{Type: wire.TypeDeviceList})
	hc.recvType(wire.TypeDevices)
	if got := pusher.count(); got != 2 {
		t.Fatalf("push count after interval = %d, want 2", got)
	}
}

func mustID(t *testing.T) *secure.Identity {
	t.Helper()
	id, err := secure.NewIdentity()
	if err != nil {
		t.Fatalf("NewIdentity: %v", err)
	}
	return id
}
