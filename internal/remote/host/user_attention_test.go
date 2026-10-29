package host

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"github.com/coder/websocket"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/appserver"
	"github.com/blueberrycongee/wuu/internal/principal"
	"github.com/blueberrycongee/wuu/internal/remote/account"
	"github.com/blueberrycongee/wuu/internal/remote/secure"
	"github.com/blueberrycongee/wuu/internal/remote/wire"
)

func admittedAttention() appserver.UserAttentionNotification {
	return appserver.UserAttentionNotification{UserSpaceID: "local", ProductDomain: "personal", TurnID: "turn-1", Outcome: "completed", UserLocation: appserver.UserLocation{Version: 1, UserSpaceID: "local", ProductDomain: "personal", AgentID: "main", MessageSeq: 17}}
}

func TestHostLifetimeAttentionReachesNeverAttachedDevice(t *testing.T) {
	store, pub := makeStore(t, "never-attached")
	if err := store.SetDevicePushToken(pub, "ExponentPushToken[synthetic-personal]", "ios", time.Now()); err != nil {
		t.Fatal(err)
	}
	events := make(personalHostPusher, 4)
	h := &Host{authorizeUserAttention: func(context.Context, appserver.UserAttentionNotification) error { return nil }, store: store, baseCtx: context.Background(), pusher: events, logf: func(string, ...any) {}, pushMinInterval: time.Minute, sessions: map[string]*deviceSession{}, subscribeUserAttention: func(context.Context, func(appserver.UserAttentionNotification)) error { return nil }}
	if err := h.pushUserAttention(context.Background(), admittedAttention()); err != nil {
		t.Fatal(err)
	}
	select {
	case event := <-events:
		if event.Device != pub || event.UserLocation == nil || event.ThreadID != "" {
			t.Fatalf("invalid lifetime attention: %+v", event)
		}
	case <-time.After(time.Second):
		t.Fatal("never-attached phone lost admitted attention")
	}
	s := h.lookupSession(pub)
	if s == nil || s.app != nil {
		t.Fatal("attention required or created a device app connection")
	}
	// A live per-device stream must not independently emit the same push.
	app := &appConn{spool: newSpool(), running: map[string]wire.RunningTurn{}}
	s.app = app
	raw, _ := json.Marshal(map[string]any{"method": "user/attention", "params": admittedAttention()})
	s.onAppLine(app, raw)
	if err := h.pushUserAttention(context.Background(), admittedAttention()); err != nil {
		t.Fatal(err)
	}
	if len(events) != 0 {
		t.Fatal("shared throttle or per-device deduplication failed")
	}
	// Foreground attachment suppresses the global route without starting a turn.
	s.mu.Lock()
	s.lastThreadPush = nil
	s.attached = true
	s.channel = &secure.Channel{}
	s.mu.Unlock()
	if err := h.pushUserAttention(context.Background(), admittedAttention()); err != nil {
		t.Fatal(err)
	}
	if len(events) != 0 {
		t.Fatal("foreground phone received a redundant push")
	}
}

func TestUserAttentionSubscriptionWaitsForRelayAndCancels(t *testing.T) {
	for _, cancelBeforeReady := range []bool{true, false} {
		t.Run(map[bool]string{true: "cancel before relay", false: "cancel subscriber"}[cancelBeforeReady], func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			ready := make(chan struct{})
			subscribed := make(chan struct{})
			done := make(chan error, 1)
			h := &Host{subscribeUserAttention: func(ctx context.Context, _ func(appserver.UserAttentionNotification)) error {
				close(subscribed)
				<-ctx.Done()
				return ctx.Err()
			}}
			go func() { done <- h.runUserAttention(ctx, ready) }()
			if !cancelBeforeReady {
				close(ready)
				<-subscribed
			}
			cancel()
			if err := <-done; !errors.Is(err, context.Canceled) {
				t.Fatalf("subscription did not stop with host: %v", err)
			}
			if cancelBeforeReady {
				select {
				case <-subscribed:
					t.Fatal("subscription consumed replay before relay readiness")
				default:
				}
			}
		})
	}
}

func TestAccountAttentionLooksUpCurrentAuthorizedRecipients(t *testing.T) {
	store, phone := makeStore(t, "account-phone")
	if err := store.RemoveDevice(phone); err != nil {
		t.Fatal(err)
	}
	if err := store.SetAccount(&account.Credentials{Server: "https://synthetic.invalid", Token: "synthetic", Username: "alice"}); err != nil {
		t.Fatal(err)
	}
	host := secure.EncodeKey(store.Identity().Public())
	client, server := personalRelayPair(t)
	events := make(personalHostPusher, 1)
	h := &Host{authorizeUserAttention: func(context.Context, appserver.UserAttentionNotification) error { return nil }, store: store, ws: client, baseCtx: context.Background(), pusher: events, logf: func(string, ...any) {}, pushMinInterval: time.Minute, sessions: map[string]*deviceSession{}}
	devices := []account.Device{{Pub: host, Role: "host", Account: "alice"}, {Pub: phone, Role: "phone", Account: "alice"}, {Pub: phone, Role: "phone", Account: "alice"}, {Pub: "foreign", Role: "phone", Account: "bobby"}, {Pub: "wrong-role", Role: "host", Account: "alice"}}
	calls := 0
	previous := http.DefaultTransport
	http.DefaultTransport = personalPushTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.String() != "https://synthetic.invalid/v1/account/devices" || r.Method != "GET" || r.Header.Get("Authorization") != "Bearer synthetic" {
			t.Fatalf("unexpected account lookup: %s %s", r.Method, r.URL)
		}
		raw, _ := json.Marshal(map[string]any{"devices": devices})
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(raw))), Header: http.Header{}}, nil
	})
	defer func() { http.DefaultTransport = previous }()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	received := make(chan []byte, 1)
	go func() { _, raw, _ := server.Read(ctx); received <- raw }()
	if err := h.pushUserAttention(ctx, admittedAttention()); err != nil {
		t.Fatal(err)
	}
	var msg wire.RelayMsg
	if err := json.Unmarshal(<-received, &msg); err != nil {
		t.Fatal(err)
	}
	if msg.To != phone || msg.UserLocation == nil || msg.ThreadID != "" || calls != 1 || len(h.sessions) != 1 {
		t.Fatalf("incorrect authorized account recipients: %+v", msg)
	}
	// A current directory with the host revoked must not route at all.
	devices = devices[1:]
	if err := h.pushUserAttention(ctx, admittedAttention()); err == nil {
		t.Fatal("revoked host was allowed to route attention")
	}
	// Reject an oversized directory without allocating unbounded sessions.
	devices = make([]account.Device, 257)
	if err := h.pushUserAttention(ctx, admittedAttention()); err == nil {
		t.Fatal("unbounded account directory accepted")
	}
	if len(events) != 0 {
		t.Fatal("account attention bypassed relay with local provider")
	}
}

func TestAccountAttentionLookupCancelsWithHost(t *testing.T) {
	store, _ := makeStore(t, "phone")
	if err := store.SetAccount(&account.Credentials{Server: "https://synthetic.invalid", Token: "synthetic", Username: "alice"}); err != nil {
		t.Fatal(err)
	}
	h := &Host{authorizeUserAttention: func(context.Context, appserver.UserAttentionNotification) error { return nil }, store: store}
	previous := http.DefaultTransport
	requested := make(chan struct{})
	http.DefaultTransport = personalPushTransport(func(r *http.Request) (*http.Response, error) {
		close(requested)
		<-r.Context().Done()
		return nil, r.Context().Err()
	})
	defer func() { http.DefaultTransport = previous }()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- h.pushUserAttention(ctx, admittedAttention()) }()
	<-requested
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("account lookup did not stop: %v", err)
	}
}

func TestHostRunSubscribesBeforeAnyPhoneAttach(t *testing.T) {
	store, phone := makeStore(t, "offline-phone")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	received := make(chan wire.RelayMsg, 1)
	relayDone := make(chan struct{})
	previous := http.DefaultTransport
	http.DefaultTransport = personalPushTransport(func(r *http.Request) (*http.Response, error) {
		clientPipe, serverPipe := net.Pipe()
		recorder := httptest.NewRecorder()
		server, err := websocket.Accept(personalUpgradeWriter{recorder, serverPipe}, r, nil)
		if err != nil {
			return nil, err
		}
		go func() {
			defer close(relayDone)
			defer server.CloseNow()
			read := func() (wire.RelayMsg, error) {
				_, raw, err := server.Read(ctx)
				var msg wire.RelayMsg
				if err == nil {
					err = json.Unmarshal(raw, &msg)
				}
				return msg, err
			}
			write := func(msg wire.RelayMsg) error {
				raw, _ := json.Marshal(msg)
				return server.Write(ctx, websocket.MessageText, raw)
			}
			if msg, err := read(); err != nil || msg.Type != wire.TypeHello {
				return
			}
			if err := write(wire.RelayMsg{Type: wire.TypeChallenge, Nonce: base64.RawURLEncoding.EncodeToString(make([]byte, 32))}); err != nil {
				return
			}
			if msg, err := read(); err != nil || msg.Type != wire.TypeAuth {
				return
			}
			if err := write(wire.RelayMsg{Type: wire.TypeAuthOK}); err != nil {
				return
			}
			if msg, err := read(); err == nil {
				received <- msg
			}
			<-ctx.Done()
		}()
		return &http.Response{StatusCode: recorder.Code, Header: recorder.Header(), Body: clientPipe}, nil
	})
	defer func() { http.DefaultTransport = previous }()
	subscriberDone := make(chan struct{})
	h, err := New(Options{Store: store, RelayURL: "ws://synthetic.invalid/relay", Pusher: LogHostPusher{Logf: func(string, ...any) {}},
		AppServer: func(context.Context, io.Reader, io.Writer) error {
			t.Error("push incorrectly needed a device app connection")
			return nil
		},
		AuthorizeUserAttention: func(context.Context, appserver.UserAttentionNotification) error { return nil },
		SubscribeUserAttention: func(ctx context.Context, receive func(appserver.UserAttentionNotification)) error {
			defer close(subscriberDone)
			receive(admittedAttention())
			<-ctx.Done()
			return ctx.Err()
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- h.Run(ctx) }()
	select {
	case msg := <-received:
		if msg.Type != wire.TypePush || msg.To != phone || msg.UserLocation == nil {
			t.Fatalf("host-lifetime attention did not reach relay: %+v", msg)
		}
	case <-ctx.Done():
		t.Fatal("host did not subscribe and deliver before phone attachment")
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("host shutdown: %v", err)
	}
	<-subscriberDone
	<-relayDone
}

// A host-owned subscription is only an event source. Directory membership and
// local observer authority cannot bypass the selected recipient resource policy.
func TestHostLifetimeAttentionUsesRecipientPolicy(t *testing.T) {
	store, pub := makeStore(t, "policy-phone")
	if err := store.SetDevicePushToken(pub, "ExponentPushToken[policy]", "ios", time.Now()); err != nil {
		t.Fatal(err)
	}
	events := make(personalHostPusher, 4)
	h := &Host{store: store, baseCtx: context.Background(), pusher: events, logf: func(string, ...any) {}, sessions: map[string]*deviceSession{}}
	if err := h.pushUserAttention(context.Background(), admittedAttention()); err == nil || len(events) != 0 {
		t.Fatal("missing resource policy delivered attention")
	}
	var allow atomic.Bool
	var checks atomic.Int32
	h.authorizeUserAttention = func(ctx context.Context, event appserver.UserAttentionNotification) error {
		checks.Add(1)
		caller, ok := principal.FromContext(ctx)
		if !ok || caller.Identity().Subject != pub || event.TurnID != "turn-1" {
			t.Fatal("resource policy did not receive the authenticated recipient and event")
		}
		if !allow.Load() {
			return errors.New("recipient denied")
		}
		return nil
	}
	if err := h.pushUserAttention(context.Background(), admittedAttention()); err != nil || len(events) != 0 || len(h.sessions) != 0 {
		t.Fatalf("denied recipient consumed push state: %v", err)
	}
	allow.Store(true)
	if err := h.pushUserAttention(context.Background(), admittedAttention()); err != nil || checks.Load() < 2 {
		t.Fatalf("authorized recipient missing attention: %v, checks=%d", err, checks.Load())
	}
	select {
	case <-events:
	case <-time.After(time.Second):
		t.Fatal("authorized recipient missing attention")
	}
	allow.Store(false)
	if err := h.pushUserAttention(context.Background(), admittedAttention()); err != nil || len(events) != 0 || checks.Load() < 4 {
		t.Fatalf("changed policy was not rechecked: %v, checks=%d", err, checks.Load())
	}
}

func TestHostAttentionRechecksPolicyBeforeExternalDelivery(t *testing.T) {
	store, pub := makeStore(t, "revoked-push")
	if err := store.SetDevicePushToken(pub, "ExponentPushToken[policy]", "ios", time.Now()); err != nil {
		t.Fatal(err)
	}
	events := make(personalHostPusher, 1)
	checks := 0
	h := &Host{store: store, baseCtx: context.Background(), pusher: events, logf: func(string, ...any) {}, sessions: map[string]*deviceSession{}, authorizeUserAttention: func(context.Context, appserver.UserAttentionNotification) error {
		checks++
		if checks > 1 {
			return errors.New("recipient revoked before delivery")
		}
		return nil
	}}
	if err := h.pushUserAttention(context.Background(), admittedAttention()); err != nil || checks != 2 || len(events) != 0 {
		t.Fatalf("external delivery did not recheck admitted recipient: %v, checks=%d", err, checks)
	}
}
