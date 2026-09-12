// Package host implements the desktop side of wuu remote control. A Host
// owns the machine's remote identity, keeps an outbound websocket to the
// relay (NAT-friendly: the desktop always dials out), runs the pairing
// window that enrolls phones, and gives every paired phone its own
// end-to-end encrypted app-server connection over the shared runtime.
//
// A phone speaks the exact same line-JSON dialect as the desktop app: each
// device session pipes decrypted rpc lines into appserver.RunStdio and seals
// every outbound line back to the phone, numbered and spooled so a flaky
// mobile link can drop, re-handshake, and resume without losing events while
// agents keep running.
package host

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/blueberrycongee/wuu/internal/remote/secure"
	"github.com/blueberrycongee/wuu/internal/remote/wire"
	"github.com/blueberrycongee/wuu/internal/runtime"
	"github.com/blueberrycongee/wuu/internal/version"
)

const (
	// maxAppLineBytes bounds a single app-server protocol line in either
	// direction. The app-server's own stdin scanner caps at 4MB; outbound
	// lines (thread lists, histories) can be larger.
	maxAppLineBytes = 32 << 20

	relayWriteTimeout = 15 * time.Second
	relayPingInterval = 20 * time.Second

	defaultPushMinInterval = 30 * time.Second
	defaultPairingTimeout  = 10 * time.Minute
)

var b64 = base64.RawURLEncoding

// ErrNotConnected is returned when a relay write is attempted while the
// relay link is down.
var ErrNotConnected = errors.New("relay not connected")

// PairingConfig opens a pairing window when the host starts.
type PairingConfig struct {
	// Timeout closes the window after this long. Zero means 10 minutes.
	Timeout time.Duration
	// Once closes the window after the first successful pairing.
	Once bool
	// OnURI receives the pairing URI after the relay confirms registration.
	OnURI func(uri string)
	// OnPaired fires after a phone is enrolled.
	OnPaired func(name string, devicePub []byte)
}

// Options configures a Host.
type Options struct {
	// AppServer connects an authenticated device to an existing execution host.
	// When omitted, standalone hosts create their own app-server connection.
	AppServer func(context.Context, io.Reader, io.Writer) error
	Workdir   string
	Runtime   *runtime.Session
	Store     *Store
	RelayURL  string // overrides the store's relay URL
	HostName  string // overrides the store's host name
	Pairing   *PairingConfig
	Logf      func(format string, args ...any)

	// ReconnectMin/ReconnectMax bound the relay redial backoff. Zero values
	// mean 1s/30s.
	ReconnectMin time.Duration
	ReconnectMax time.Duration
	// PushMinInterval throttles per-device push hints. Zero means 30s.
	PushMinInterval time.Duration
	// Pusher delivers native push notifications to registered devices. Nil
	// means ExpoPusher; tests use LogHostPusher to stay off the network.
	Pusher HostPusher
}

type pairingState struct {
	pairing   *secure.Pairing
	cfg       PairingConfig
	deadline  time.Time
	announced bool
}

// Host runs the desktop side of remote control.
type Host struct {
	appServer       func(context.Context, io.Reader, io.Writer) error
	workdir         string
	rt              *runtime.Session
	store           *Store
	relayURL        string
	hostName        string
	logf            func(format string, args ...any)
	reconnectMin    time.Duration
	reconnectMax    time.Duration
	pushMinInterval time.Duration
	pusher          HostPusher

	baseCtx context.Context

	wsMu    sync.Mutex // guards the ws pointer
	writeMu sync.Mutex // serializes writes on the live socket
	ws      *websocket.Conn

	sessMu   sync.Mutex
	sessions map[string]*deviceSession

	pairMu  sync.Mutex
	pairing *pairingState
}

func New(opts Options) (*Host, error) {
	if opts.Runtime == nil && opts.AppServer == nil {
		return nil, errors.New("remote host requires a runtime")
	}
	if opts.Store == nil {
		return nil, errors.New("remote host requires a store")
	}
	relayURL := opts.RelayURL
	if relayURL == "" {
		relayURL = opts.Store.RelayURL()
	}
	if relayURL == "" {
		return nil, errors.New("no relay url configured (wuu remote init --relay <url>)")
	}
	hostName := opts.HostName
	if hostName == "" {
		hostName = opts.Store.HostName()
	}
	if hostName == "" {
		if hn, err := os.Hostname(); err == nil {
			hostName = hn
		} else {
			hostName = "wuu-host"
		}
	}
	logf := opts.Logf
	if logf == nil {
		logf = func(string, ...any) {}
	}
	pusher := opts.Pusher
	if pusher == nil {
		pusher = NewExpoPusher()
	}
	h := &Host{
		appServer:       opts.AppServer,
		workdir:         opts.Workdir,
		rt:              opts.Runtime,
		store:           opts.Store,
		relayURL:        relayURL,
		hostName:        hostName,
		logf:            logf,
		reconnectMin:    opts.ReconnectMin,
		reconnectMax:    opts.ReconnectMax,
		pushMinInterval: opts.PushMinInterval,
		pusher:          pusher,
		sessions:        map[string]*deviceSession{},
	}
	if h.reconnectMin <= 0 {
		h.reconnectMin = time.Second
	}
	if h.reconnectMax <= 0 {
		h.reconnectMax = 30 * time.Second
	}
	if h.pushMinInterval <= 0 {
		h.pushMinInterval = defaultPushMinInterval
	}
	if opts.Pairing != nil {
		if _, err := h.StartPairing(*opts.Pairing); err != nil {
			return nil, err
		}
	}
	return h, nil
}

// StartPairing opens (or replaces) the pairing window and returns the
// pairing URI to show as a QR code.
func (h *Host) StartPairing(cfg PairingConfig) (string, error) {
	pairing, err := secure.NewPairing()
	if err != nil {
		return "", err
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = defaultPairingTimeout
	}
	st := &pairingState{
		pairing:  pairing,
		cfg:      cfg,
		deadline: time.Now().Add(timeout),
	}
	h.pairMu.Lock()
	h.pairing = st
	h.pairMu.Unlock()

	uri := pairing.URI(h.relayURL, h.store.Identity().Public())
	// Best effort: if the relay link is already up, open the window now.
	_ = h.writeRelay(wire.RelayMsg{Type: wire.TypePairOpen, PairingID: pairing.ID})
	return uri, nil
}

// Run connects to the relay and serves until ctx is cancelled. It reconnects
// with capped exponential backoff; device app-server connections survive
// relay drops.
func (h *Host) Run(ctx context.Context) error {
	h.baseCtx = ctx
	defer h.shutdownSessions()
	syncCtx, stopSync := context.WithCancel(ctx)
	syncDone := make(chan struct{})
	go func() { defer close(syncDone); h.publishConversations(syncCtx) }()
	defer func() { stopSync(); <-syncDone }()

	attempt := 0
	for {
		authed, err := h.runOnce(ctx)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if authed {
			attempt = 0
		}
		delay := h.reconnectMin << attempt
		if delay > h.reconnectMax || delay <= 0 {
			delay = h.reconnectMax
		} else if attempt < 30 {
			attempt++
		}
		if err != nil {
			h.logf("remote host: relay connection lost: %v (retry in %s)", err, delay)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delay):
		}
	}
}

// runOnce performs one relay connection lifecycle: dial, authenticate, pump
// messages until the connection dies. Returns whether authentication
// succeeded (used to reset backoff).
func (h *Host) runOnce(ctx context.Context) (bool, error) {
	dialCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	ws, _, err := websocket.Dial(dialCtx, h.relayURL, nil)
	cancel()
	if err != nil {
		return false, err
	}
	ws.SetReadLimit(maxFrameBytes)
	defer func() {
		h.wsMu.Lock()
		if h.ws == ws {
			h.ws = nil
		}
		h.wsMu.Unlock()
		_ = ws.CloseNow()
		h.detachAllSessions()
	}()

	h.wsMu.Lock()
	h.ws = ws
	h.wsMu.Unlock()

	if err := h.authenticate(ctx, ws); err != nil {
		return false, err
	}
	h.logf("remote host: connected to relay %s as %s", h.relayURL, secure.Fingerprint(h.store.Identity().Public()))

	// Re-open the pairing window on every (re)connection while it lasts.
	h.pairMu.Lock()
	if h.pairing != nil && time.Now().Before(h.pairing.deadline) {
		pid := h.pairing.pairing.ID
		h.pairMu.Unlock()
		_ = h.writeRelay(wire.RelayMsg{Type: wire.TypePairOpen, PairingID: pid})
	} else {
		h.pairMu.Unlock()
	}

	pingCtx, stopPing := context.WithCancel(ctx)
	defer stopPing()
	go func() {
		ticker := time.NewTicker(relayPingInterval)
		defer ticker.Stop()
		for {
			select {
			case <-pingCtx.Done():
				return
			case <-ticker.C:
				pctx, pcancel := context.WithTimeout(pingCtx, 10*time.Second)
				err := ws.Ping(pctx)
				pcancel()
				if err != nil {
					_ = ws.CloseNow()
					return
				}
			}
		}
	}()

	for {
		msg, err := readRelayMsg(ctx, ws)
		if err != nil {
			return true, err
		}
		h.dispatch(msg)
	}
}

func (h *Host) authenticate(ctx context.Context, ws *websocket.Conn) error {
	id := h.store.Identity()
	hello := wire.RelayMsg{
		Type:  wire.TypeHello,
		Proto: wire.ProtoVersion,
		Role:  wire.RoleHost,
		Pub:   secure.EncodeKey(id.Public()),
	}
	if err := h.writeWS(ctx, ws, hello); err != nil {
		return err
	}
	challenge, err := readRelayMsg(ctx, ws)
	if err != nil {
		return err
	}
	if challenge.Type != wire.TypeChallenge {
		return fmt.Errorf("relay: expected challenge, got %s (%s)", challenge.Type, challenge.Msg)
	}
	nonce, err := b64.DecodeString(challenge.Nonce)
	if err != nil {
		return fmt.Errorf("relay: bad challenge nonce: %w", err)
	}
	auth := wire.RelayMsg{
		Type: wire.TypeAuth,
		Sig:  b64.EncodeToString(id.SignRelayAuth(nonce, wire.RoleHost)),
	}
	if err := h.writeWS(ctx, ws, auth); err != nil {
		return err
	}
	ok, err := readRelayMsg(ctx, ws)
	if err != nil {
		return err
	}
	if ok.Type != wire.TypeAuthOK {
		return fmt.Errorf("relay auth failed: %s (%s)", ok.Code, ok.Msg)
	}
	return nil
}

func (h *Host) dispatch(msg wire.RelayMsg) {
	switch msg.Type {
	case wire.TypeFrame:
		h.handleFrame(msg)
	case wire.TypePairOffer:
		h.handlePairOffer(msg)
	case wire.TypePresence:
		if msg.Online != nil && !*msg.Online {
			if sess := h.lookupSession(msg.Pub); sess != nil {
				sess.detach()
			}
		}
	case wire.TypeDeliverErr:
		if sess := h.lookupSession(msg.To); sess != nil {
			sess.detach()
		}
	case wire.TypeOK:
		h.pairMu.Lock()
		st := h.pairing
		ready := st != nil && st.pairing.ID == msg.PairingID && !st.announced && time.Now().Before(st.deadline)
		if ready {
			st.announced = true
		}
		h.pairMu.Unlock()
		if ready && st.cfg.OnURI != nil {
			st.cfg.OnURI(st.pairing.URI(h.relayURL, h.store.Identity().Public()))
		}
	case wire.TypeDevices, "pong":
		// Informational.
	case wire.TypeErr:
		h.logf("remote host: relay error %s: %s", msg.Code, msg.Msg)
	}
}

// handleFrame routes one payload from a phone: handshakes establish a new
// channel, sealed frames go to the device session.
func (h *Host) handleFrame(msg wire.RelayMsg) {
	payload, err := b64.DecodeString(msg.Payload)
	if err != nil {
		return
	}
	kind, body, err := wire.SplitPayload(payload)
	if err != nil {
		return
	}
	switch kind {
	case wire.KindHandshake:
		h.handleHandshake(msg.From, body)
	case wire.KindSealed:
		if sess := h.lookupSession(msg.From); sess != nil {
			sess.handleSealed(body)
		}
	}
}

func (h *Host) handleHandshake(from string, body []byte) {
	e2e, err := wire.DecodeE2E(body)
	if err != nil || e2e.T != wire.E2EHS1 {
		return
	}
	// The relay authenticated `from`; the handshake signature must belong to
	// the same device or someone is replaying another phone's opener.
	if e2e.DevicePub != from {
		h.logf("remote host: hs1 device key mismatch (from %s)", from)
		return
	}
	hs1 := secure.HS1{DevicePub: e2e.DevicePub, Eph: e2e.Eph, Nonce: e2e.Nonce, Sig: e2e.Sig}
	authorize := h.store.IsPaired
	if credentials := h.store.Account(); credentials != nil {
		authorize = func(pub []byte) bool {
			return credentials.Allows(h.baseCtx, secure.EncodeKey(h.store.Identity().Public()), secure.EncodeKey(pub))
		}
	}
	ch, hs2, err := secure.AcceptHandshake(h.store.Identity(), hs1, authorize)
	if err != nil {
		h.logf("remote host: reject handshake from %s: %v", from, err)
		return
	}
	sess := h.ensureSession(from)
	sess.setChannel(ch)

	reply := wire.E2EMsg{T: wire.E2EHS2, Eph: hs2.Eph, Nonce: hs2.Nonce, Sig: hs2.Sig}
	data, err := wire.EncodeE2E(reply)
	if err != nil {
		return
	}
	if err := h.sendFrame(from, wire.WrapPayload(wire.KindHandshake, data)); err != nil {
		sess.detach()
	}
}

// handlePairOffer serves the pairing window: decrypt the offer, enroll the
// device locally and at the relay, and answer the phone.
func (h *Host) handlePairOffer(msg wire.RelayMsg) {
	h.pairMu.Lock()
	st := h.pairing
	h.pairMu.Unlock()
	if st == nil || st.pairing.ID != msg.PairingID {
		return
	}
	if time.Now().After(st.deadline) {
		h.closePairing(st, "expired")
		return
	}
	payload, err := b64.DecodeString(msg.Payload)
	if err != nil {
		return
	}
	offer, hostPairing, err := st.pairing.OpenPairOffer(payload)
	if err != nil {
		h.logf("remote host: bad pairing offer: %v", err)
		_ = h.writeRelay(wire.RelayMsg{Type: wire.TypePairErr, PairingID: msg.PairingID, Code: wire.CodeBadRequest})
		return
	}
	name := offer.Name
	if name == "" {
		name = "phone-" + secure.Fingerprint(offer.DevicePub)
	}
	if err := h.store.AddDevice(offer.DevicePub, name, time.Now()); err != nil {
		h.logf("remote host: persist device: %v", err)
		_ = h.writeRelay(wire.RelayMsg{Type: wire.TypePairErr, PairingID: msg.PairingID, Code: wire.CodeBadRequest})
		return
	}
	// Enroll at the relay before answering: the relay processes this
	// connection's messages in order, so the device exists by the time the
	// phone receives the answer and dials in.
	devPub := secure.EncodeKey(offer.DevicePub)
	if err := h.writeRelay(wire.RelayMsg{Type: wire.TypeDeviceAdd, Pub: devPub, Name: name}); err != nil {
		return
	}
	answer, err := hostPairing.SealPairAnswer(h.store.Identity(), h.hostName, offer.DevicePub)
	if err != nil {
		return
	}
	if err := h.writeRelay(wire.RelayMsg{
		Type:      wire.TypePairAnswer,
		PairingID: msg.PairingID,
		Payload:   b64.EncodeToString(answer),
	}); err != nil {
		return
	}
	h.logf("remote host: paired device %q (%s)", name, secure.Fingerprint(offer.DevicePub))
	if st.cfg.OnPaired != nil {
		st.cfg.OnPaired(name, offer.DevicePub)
	}
	if st.cfg.Once {
		h.closePairing(st, "paired")
	}
}

func (h *Host) closePairing(st *pairingState, reason string) {
	h.pairMu.Lock()
	if h.pairing == st {
		h.pairing = nil
	}
	h.pairMu.Unlock()
	_ = h.writeRelay(wire.RelayMsg{Type: wire.TypePairClose, PairingID: st.pairing.ID})
	h.logf("remote host: pairing window closed (%s)", reason)
}

func (h *Host) ensureSession(devPub string) *deviceSession {
	h.sessMu.Lock()
	defer h.sessMu.Unlock()
	if sess, ok := h.sessions[devPub]; ok {
		return sess
	}
	sess := &deviceSession{h: h, devPub: devPub}
	h.sessions[devPub] = sess
	return sess
}

func (h *Host) lookupSession(devPub string) *deviceSession {
	h.sessMu.Lock()
	defer h.sessMu.Unlock()
	return h.sessions[devPub]
}

func (h *Host) detachAllSessions() {
	h.sessMu.Lock()
	sessions := make([]*deviceSession, 0, len(h.sessions))
	for _, s := range h.sessions {
		sessions = append(sessions, s)
	}
	h.sessMu.Unlock()
	for _, s := range sessions {
		s.detach()
	}
}

func (h *Host) shutdownSessions() {
	h.sessMu.Lock()
	sessions := make([]*deviceSession, 0, len(h.sessions))
	for _, s := range h.sessions {
		sessions = append(sessions, s)
	}
	h.sessions = map[string]*deviceSession{}
	h.sessMu.Unlock()
	for _, s := range sessions {
		s.shutdown()
	}
}

// sendFrame ships an opaque payload to a device through the relay.
func (h *Host) sendFrame(devPub string, payload []byte) error {
	return h.writeRelay(wire.RelayMsg{
		Type:    wire.TypeFrame,
		To:      devPub,
		Payload: b64.EncodeToString(payload),
	})
}

// sendPush nudges a device with a content-free hint, through both legs
// independently: the relay (for relay-native transports) and, when the device
// registered a push token, the host's own pusher — a relay outage must not
// silence native notifications. threadID deep-links turn-bound hints.
func (h *Host) sendPush(devPub, hint, threadID string) {
	err := h.writeRelay(wire.RelayMsg{Type: wire.TypePush, To: devPub, Hint: hint, ThreadID: threadID})
	if err == nil {
		h.logf("remote host: push %s -> %s", hint, devPub[:min(12, len(devPub))])
	}
	token, platform, registered := h.store.DevicePush(devPub)
	if !registered {
		return
	}
	ev := HostPushEvent{
		Device:   devPub,
		Token:    token,
		Platform: platform,
		Hint:     hint,
		ThreadID: threadID,
		At:       time.Now().UTC(),
	}
	// The pusher owns its own HTTP timeout; run it off the session's write
	// path so a slow provider cannot stall app-server output pumping.
	go func() {
		ctx, cancel := context.WithTimeout(h.baseCtx, 15*time.Second)
		defer cancel()
		h.pusher.Push(ctx, ev)
	}()
}

func (h *Host) writeRelay(msg wire.RelayMsg) error {
	h.wsMu.Lock()
	ws := h.ws
	h.wsMu.Unlock()
	if ws == nil {
		return ErrNotConnected
	}
	ctx, cancel := context.WithTimeout(context.Background(), relayWriteTimeout)
	defer cancel()
	return h.writeWS(ctx, ws, msg)
}

// writeWS is the single serialized write path for the relay socket.
func (h *Host) writeWS(ctx context.Context, ws *websocket.Conn, msg wire.RelayMsg) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	h.writeMu.Lock()
	defer h.writeMu.Unlock()
	return ws.Write(ctx, websocket.MessageText, data)
}

func (h *Host) hostInfo() wire.HostInfo {
	info := wire.HostInfo{
		Name:    h.hostName,
		Version: version.Info().Version,
		Workdir: h.workdir,
	}
	if h.rt != nil {
		info.Workdir, info.Provider, info.Model = h.rt.RootDir, h.rt.ProviderName, h.rt.Model
	}
	return info
}

// --- shared websocket helpers ------------------------------------------------

const maxFrameBytes = 16 << 20

func readRelayMsg(ctx context.Context, ws *websocket.Conn) (wire.RelayMsg, error) {
	_, data, err := ws.Read(ctx)
	if err != nil {
		return wire.RelayMsg{}, err
	}
	var msg wire.RelayMsg
	if err := json.Unmarshal(data, &msg); err != nil {
		return wire.RelayMsg{}, err
	}
	return msg, nil
}
