package host

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/blueberrycongee/wuu/internal/remote/account"
	"github.com/blueberrycongee/wuu/internal/remote/secure"
	"github.com/blueberrycongee/wuu/internal/remote/wire"
)

type personalPushTransport func(*http.Request) (*http.Response, error)

func (f personalPushTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type personalHostPusher chan HostPushEvent

func (p personalHostPusher) Push(_ context.Context, ev HostPushEvent) { p <- ev }

func TestPersonalPushFromAppPipeThroughExpo(t *testing.T) {
	store, pub := makeStore(t, "personal-phone")
	if err := store.SetDevicePushToken(pub, "ExponentPushToken[synthetic-personal]", "ios", time.Now()); err != nil {
		t.Fatal(err)
	}
	host := secure.EncodeKey(store.Identity().Public())
	events := make(personalHostPusher, 4)
	h := &Host{store: store, baseCtx: context.Background(), pusher: events, logf: func(string, ...any) {}, pushMinInterval: time.Minute}
	app := &appConn{cancel: func() {}, spool: newSpool(), running: map[string]wire.RunningTurn{}, profile: wire.ClientProfileMobileActivity}
	s := &deviceSession{h: h, devPub: pub, app: app}
	r, w := io.Pipe()
	defer r.Close()
	done := make(chan struct{})
	go func() { s.pumpAppOutput(app, r); close(done) }()
	line := `{"method":"user/attention","params":{"turn_id":"turn-1","outcome":"completed","user_space_id":"local","product_domain":"personal","user_location":{"version":1,"user_space_id":"local","product_domain":"personal","agent_id":"main","message_seq":17}}}`
	if _, err := io.WriteString(w, line+"\n"); err != nil {
		t.Fatal(err)
	}
	w.Close()
	<-done
	var event HostPushEvent
	select {
	case event = <-events:
	case <-time.After(5 * time.Second):
		t.Fatal("app pipe did not produce Personal push")
	}
	if event.ThreadID != "" || event.Host != host || event.Device != pub || event.UserLocation == nil {
		t.Fatalf("unbound push: %+v", event)
	}
	calls := 0
	expected := map[string]any{"version": "1", "product_domain": "personal", "user_space_id": "local", "host": host, "recipient": pub, "agent_id": "main", "message_seq": "17"}
	p := &ExpoPusher{HTTPClient: &http.Client{Transport: personalPushTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		var batch []ExpoPushRequest
		if err := json.NewDecoder(r.Body).Decode(&batch); err != nil {
			t.Fatal(err)
		}
		if len(batch) != 1 || !reflect.DeepEqual(batch[0].Data, expected) {
			t.Fatalf("unexpected Expo Personal payload: %+v", batch)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`[{"status":"ok"}]`)), Header: http.Header{}}, nil
	})}}
	p.Push(context.Background(), event)
	if calls != 1 {
		t.Fatal("Expo transport not called")
	}
	invalid := *event.UserLocation
	invalid.Version = 2
	event.UserLocation = &invalid
	p.Push(context.Background(), event)
	if calls != 1 {
		t.Fatal("invalid location reached Expo transport")
	}
}

// The websocket upgrade and frames stay on an in-memory pipe. No listener,
// account service, token endpoint or provider is contacted.
type personalUpgradeWriter struct {
	*httptest.ResponseRecorder
	conn net.Conn
}

func (w personalUpgradeWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return w.conn, bufio.NewReadWriter(bufio.NewReader(w.conn), bufio.NewWriter(w.conn)), nil
}
func personalRelayPair(t *testing.T) (*websocket.Conn, *websocket.Conn) {
	t.Helper()
	clientPipe, serverPipe := net.Pipe()
	var server *websocket.Conn
	tr := personalPushTransport(func(r *http.Request) (*http.Response, error) {
		recorder := httptest.NewRecorder()
		var err error
		server, err = websocket.Accept(personalUpgradeWriter{recorder, serverPipe}, r, nil)
		if err != nil {
			return nil, err
		}
		return &http.Response{StatusCode: recorder.Code, Header: recorder.Header(), Body: clientPipe}, nil
	})
	client, _, err := websocket.Dial(context.Background(), "ws://synthetic.invalid/relay", &websocket.DialOptions{HTTPClient: &http.Client{Transport: tr}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { client.CloseNow(); server.CloseNow() })
	return client, server
}
func TestAccountPersonalPushDoesNotRequireLegacyPairing(t *testing.T) {
	store, pub := makeStore(t, "account-phone")
	if err := store.RemoveDevice(pub); err != nil {
		t.Fatal(err)
	}
	if err := store.SetAccount(&account.Credentials{Server: "https://synthetic.invalid", Token: "synthetic"}); err != nil {
		t.Fatal(err)
	}
	client, server := personalRelayPair(t)
	h := &Host{store: store, ws: client, baseCtx: context.Background(), pusher: make(personalHostPusher, 1), logf: func(string, ...any) {}}
	received := make(chan []byte, 1)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	go func() {
		_, raw, err := server.Read(ctx)
		if err != nil {
			received <- nil
			return
		}
		received <- raw
	}()
	h.sendPush(pub, wire.PushAgentDone, "", &wire.UserLocation{Version: 1, UserSpaceID: "local", ProductDomain: "personal", AgentID: "main"})
	raw := <-received
	if len(raw) == 0 {
		t.Fatal("account-authorized Personal push was suppressed by legacy pairing check")
	}
	var msg wire.RelayMsg
	if err := json.Unmarshal(raw, &msg); err != nil {
		t.Fatal(err)
	}
	if msg.Type != wire.TypePush || msg.To != pub || msg.ThreadID != "" || msg.UserLocation == nil || msg.UserLocation.AgentID != "main" {
		t.Fatalf("invalid relay Personal push: %s", raw)
	}
}
