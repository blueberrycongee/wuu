package host

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/blueberrycongee/wuu/internal/remote/secure"
	"github.com/blueberrycongee/wuu/internal/remote/wire"
)

// Exercise the existing encrypted websocket path so the ephemeral lane cannot
// accidentally enter the resumable RPC spool or bypass channel authentication.
func previewTestSession(t *testing.T, desktop bool) (*deviceSession, func() wire.E2EMsg) {
	t.Helper()
	accepted := make(chan *websocket.Conn, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		accepted <- ws
	}))
	t.Cleanup(server.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	ws, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ws.CloseNow() })
	peer := <-accepted
	t.Cleanup(func() { _ = peer.CloseNow() })
	hostIdentity, err := secure.NewIdentity()
	if err != nil {
		t.Fatal(err)
	}
	phoneIdentity, err := secure.NewIdentity()
	if err != nil {
		t.Fatal(err)
	}
	handshake, hs1, err := secure.NewHandshake(phoneIdentity, hostIdentity.Public())
	if err != nil {
		t.Fatal(err)
	}
	hostChannel, hs2, err := secure.AcceptHandshake(hostIdentity, hs1, func(pub []byte) bool {
		return bytes.Equal(pub, phoneIdentity.Public())
	})
	if err != nil {
		t.Fatal(err)
	}
	phoneChannel, err := handshake.Complete(hs2)
	if err != nil {
		t.Fatal(err)
	}
	h := &Host{ws: ws, hostName: "preview-test", logf: t.Logf}
	if desktop {
		h.appServer = func(context.Context, io.Reader, io.Writer) error { return nil }
	}
	s := &deviceSession{h: h, devPub: secure.EncodeKey(phoneIdentity.Public()), channel: hostChannel,
		app: &appConn{contID: "continuity", profile: wire.ClientProfileMobileActivity,
			spool: newSpool(), running: map[string]wire.RunningTurn{}},
	}
	return s, func() wire.E2EMsg {
		t.Helper()
		message, err := readRelayMsg(ctx, peer)
		if err != nil {
			t.Fatal(err)
		}
		if message.Type != wire.TypeFrame || message.To != s.devPub {
			t.Fatalf("preview escaped its paired-device route: %+v", message)
		}
		payload, err := b64.DecodeString(message.Payload)
		if err != nil {
			t.Fatal(err)
		}
		kind, sealed, err := wire.SplitPayload(payload)
		if err != nil || kind != wire.KindSealed {
			t.Fatalf("preview was not sealed: kind=%d err=%v", kind, err)
		}
		plain, err := phoneChannel.Open(sealed)
		if err != nil {
			t.Fatal(err)
		}
		value, err := wire.DecodeE2E(plain)
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
}

func TestPreviewNegotiationAndEphemeralDelivery(t *testing.T) {
	frame := []byte(`{"method":"desktop/activity/preview/frame","params":{"stream_id":"stream","sequence":1,"data":"aGVsbG8="}}`)
	state := []byte(`{"method":"desktop/activity/preview/state","params":{"stream_id":"stream","state":"ended"}}`)
	for _, test := range []struct {
		name                     string
		desktop, accept, enabled bool
	}{
		{"desktop preview", true, true, true},
		{"legacy client", true, false, false},
		{"standalone host", false, true, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			s, receive := previewTestSession(t, test.desktop)
			s.handleAttachLocked(wire.E2EMsg{T: wire.E2EAttach, Prev: s.app.contID,
				ClientProfile: wire.ClientProfileMobileActivity, PreviewV1: test.accept})
			attached := receive()
			if attached.T != wire.E2EAttached || attached.PreviewV1 != test.enabled {
				t.Fatalf("wrong negotiated preview capability: %+v", attached)
			}
			if receive().T != wire.E2EState {
				t.Fatal("missing host state")
			}
			for _, line := range [][]byte{frame, state} {
				s.onAppLine(s.app, line)
				if test.enabled {
					message := receive()
					if message.T != wire.E2EPreview || message.Seq != 0 || !bytes.Equal(message.Line, line) || message.LineGzip != "" {
						t.Fatalf("preview must be ephemeral, uncompressed, and unchanged: %+v", message)
					}
				}
			}
			if s.app.seq != 0 || s.app.spool.bytes != 0 {
				t.Fatal("preview consumed RPC sequence or replay budget")
			}
			// The next ordinary reply proves legacy peers received no preview.
			s.onAppLine(s.app, []byte(`{"id":"after-preview","result":{}}`))
			message := receive()
			if message.T != wire.E2ERPC || message.Seq != 1 {
				t.Fatalf("preview interfered with ordinary RPC: %+v", message)
			}
		})
	}
}

func TestPreviewDropsAfterDetachAndNeverReplays(t *testing.T) {
	s, receive := previewTestSession(t, true)
	s.handleAttachLocked(wire.E2EMsg{T: wire.E2EAttach, Prev: s.app.contID,
		ClientProfile: wire.ClientProfileMobileActivity, PreviewV1: true})
	receive()
	receive()
	channel := s.channel
	s.detach()
	s.onAppLine(s.app, []byte(`{"method":"desktop/activity/preview/frame","params":{"stream_id":"old","data":"YQ=="}}`))
	s.onAppLine(s.app, []byte(`{"method":"desktop/activity/preview/state","params":{"stream_id":"old","state":"ended"}}`))
	s.onAppLine(s.app, []byte(`{"id":"missed-reply","result":{}}`))
	if s.app.seq != 1 || len(s.app.spool.entries) != 1 {
		t.Fatal("detached preview entered durable replay")
	}
	s.setChannel(channel)
	s.handleAttachLocked(wire.E2EMsg{T: wire.E2EAttach, Prev: s.app.contID,
		ClientProfile: wire.ClientProfileMobileActivity, PreviewV1: true})
	if message := receive(); message.T != wire.E2EAttached || !message.Resumed {
		t.Fatalf("preview altered resumable task lifetime: %+v", message)
	}
	receive()
	message := receive()
	if message.T != wire.E2ERPC || message.Seq != 1 || !strings.Contains(string(message.Line), "missed-reply") {
		t.Fatalf("reconnect replayed pixels or lost ordinary response: %+v", message)
	}
}

func TestPreviewBoundsDoNotDiscardOrdinaryRequests(t *testing.T) {
	s, receive := previewTestSession(t, true)
	s.handleAttachLocked(wire.E2EMsg{T: wire.E2EAttach, Prev: s.app.contID,
		ClientProfile: wire.ClientProfileMobileActivity, PreviewV1: true})
	receive()
	receive()
	oversized, _ := json.Marshal(map[string]any{"method": "desktop/activity/preview/frame", "params": map[string]string{
		"stream_id": "oversized", "data": strings.Repeat("A", maxPreviewLineBytes),
	}})
	s.onAppLine(s.app, oversized)
	request := []byte(`{"id":"approval","method":"desktop/activity/preview/state","params":{}}`)
	s.onAppLine(s.app, request)
	message := receive()
	if message.T != wire.E2ERPC || message.Seq != 1 || !bytes.Equal(message.Line, request) || len(s.app.spool.entries) != 1 {
		t.Fatalf("size bound or notification classification corrupted RPC: %+v", message)
	}
}

func TestClassifyPushHintTurnCompleted(t *testing.T) {
	line := []byte(`{"method":"turn/completed","params":{"thread_id":"thread-1","turn":{"id":"turn-1"}}}`)
	hint, threadID := classifyPushHint(line)
	if hint != "agent_done" {
		t.Errorf("hint: want agent_done, got %q", hint)
	}
	if threadID != "thread-1" {
		t.Errorf("threadID: want thread-1, got %q", threadID)
	}
}

func TestClassifyPushHintTurnError(t *testing.T) {
	line := []byte(`{"method":"turn/error","params":{"thread_id":"thread-2","turn_id":"turn-2","error":"boom"}}`)
	hint, threadID := classifyPushHint(line)
	if hint != "agent_done" {
		t.Errorf("hint: want agent_done, got %q", hint)
	}
	if threadID != "thread-2" {
		t.Errorf("threadID: want thread-2, got %q", threadID)
	}
}

func TestClassifyPushHintServerRequest(t *testing.T) {
	// Server requests (the app-server asking its client something) carry
	// both id and method; the agent is blocked on input. Thread is empty
	// because the request is process-wide, not thread-scoped.
	line := []byte(`{"id":"42","method":"tool/approval","params":{}}`)
	hint, threadID := classifyPushHint(line)
	if hint != "needs_input" {
		t.Errorf("hint: want needs_input, got %q", hint)
	}
	if threadID != "" {
		t.Errorf("threadID: want empty for needs_input, got %q", threadID)
	}
}

func TestClassifyPushHintIrrelevant(t *testing.T) {
	cases := []string{
		`{"method":"thread/started","params":{"thread_id":"t-1"}}`,
		`{"method":"item/started","params":{}}`,
		`not json`,
		``,
	}
	for _, raw := range cases {
		hint, threadID := classifyPushHint([]byte(raw))
		if hint != "" || threadID != "" {
			t.Errorf("classifyPushHint(%q): want empty, got hint=%q thread=%q", raw, hint, threadID)
		}
	}
}

// tryConsumePushSlot needs a session with s.h.pushMinInterval set. We
// construct a minimal Host with just the field the throttle reads so the
// test stays focused.
func newThrottleTestSession(interval time.Duration) *deviceSession {
	return &deviceSession{
		h: &Host{pushMinInterval: interval},
	}
}

func TestTryConsumePushSlotPerThread(t *testing.T) {
	s := newThrottleTestSession(30 * time.Second)
	// First push for t-1 consumes the per-thread slot.
	if !s.tryConsumePushSlot("agent_done", "t-1") {
		t.Fatalf("first push t-1: want true, got false")
	}
	// A different thread can fire independently (per-(device, thread) is
	// the rule, not a per-device coarse cap).
	if !s.tryConsumePushSlot("agent_done", "t-2") {
		t.Errorf("push t-2 after t-1: want true, got false")
	}
	if !s.tryConsumePushSlot("agent_done", "t-3") {
		t.Errorf("push t-3 after t-1+t-2: want true, got false")
	}
	// But a second push on t-1 within its window is blocked.
	if s.tryConsumePushSlot("agent_done", "t-1") {
		t.Errorf("push t-1 second time within per-thread window: want false, got true")
	}
	// And a second push on t-2 within its window is blocked too.
	if s.tryConsumePushSlot("agent_done", "t-2") {
		t.Errorf("push t-2 second time within per-thread window: want false, got true")
	}
}

func TestTryConsumePushSlotPerDeviceCoarseThrottle(t *testing.T) {
	// Use a 1ms interval so we can wait it out without slowing the test.
	s := newThrottleTestSession(1 * time.Millisecond)
	if !s.tryConsumePushSlot("agent_done", "t-1") {
		t.Fatalf("first push: want true, got false")
	}
	// Wait past the interval.
	time.Sleep(10 * time.Millisecond)
	// Per-device window has elapsed; the same thread can fire again.
	if !s.tryConsumePushSlot("agent_done", "t-1") {
		t.Errorf("push t-1 after window: want true, got false")
	}
}

func TestTryConsumePushSlotEmptyThreadSkipsPerThreadThrottle(t *testing.T) {
	s := newThrottleTestSession(30 * time.Second)
	// needs_input has no thread_id; only per-device throttle applies.
	if !s.tryConsumePushSlot("needs_input", "") {
		t.Fatalf("first needs_input: want true, got false")
	}
	if s.tryConsumePushSlot("needs_input", "") {
		t.Errorf("second needs_input within window: want false, got true")
	}
}

func TestTryConsumePushSlotPrunesOnGrowth(t *testing.T) {
	s := newThrottleTestSession(1 * time.Millisecond)
	// Seed 300 distinct thread slots so the cap kicks in. Each push needs
	// to be spaced at least 1ms apart, so the test is slow but bounded.
	for i := 0; i < 300; i++ {
		// Wait just over the interval between each push to keep the
		// per-device throttle from blocking.
		time.Sleep(2 * time.Millisecond)
		threadID := "t-" + itoa(i)
		s.tryConsumePushSlot("agent_done", threadID)
	}
	// The map should have been pruned at least once.
	if len(s.lastThreadPush) > 256 {
		t.Errorf("lastThreadPush: want pruned to <= 256, got %d", len(s.lastThreadPush))
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	var buf [20]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}
