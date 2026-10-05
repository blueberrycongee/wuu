// Package phone is the controller-side client of wuu remote control: the
// library a mobile app (or today: tests and the dev CLI) uses to pair with a
// host, keep an end-to-end encrypted session to it through the relay, and
// drive the host's app-server with the exact same line-JSON protocol the
// desktop uses.
//
// The client owns reconnection: on every transport drop it re-handshakes and
// attaches with the last applied host seq, so the host either replays the
// missed lines (Resumed) or hands out a fresh app-server connection the
// caller re-initializes against (not Resumed; watch AttachEvents).
package phone

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	goruntime "runtime"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/blueberrycongee/wuu/internal/exec"
	"github.com/blueberrycongee/wuu/internal/remote/secure"
	"github.com/blueberrycongee/wuu/internal/remote/wire"
	"github.com/blueberrycongee/wuu/internal/securefs"
)

const maxFrameBytes = 16 << 20

var b64 = base64.RawURLEncoding

// Credentials is everything a phone needs to reach its paired host. Stored
// chmod 0600.
type Credentials struct {
	V          int    `json:"v"`
	DeviceSeed string `json:"device_seed"` // base64url Ed25519 seed
	DeviceName string `json:"device_name,omitempty"`
	HostPub    string `json:"host_pub"` // base64url, pinned at pairing
	HostName   string `json:"host_name,omitempty"`
	RelayURL   string `json:"relay_url"`
}

func LoadCredentials(path string) (*Credentials, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var c Credentials
	if err := json.Unmarshal(raw, &c); err != nil {
		return nil, fmt.Errorf("decode %s: %w", path, err)
	}
	return &c, nil
}

func (c *Credentials) Save(path string) error {
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), securefs.DirMode); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Pair scans a pairing URI (the QR content shown on the host) and completes
// the pairing exchange through the relay, returning ready-to-save
// credentials. The relay never sees anything it could use: the offer and
// answer are sealed under keys derived from the QR's ephemeral key.
func Pair(ctx context.Context, uri, deviceName string) (*Credentials, error) {
	link, err := secure.ParsePairURI(uri)
	if err != nil {
		return nil, err
	}
	id, err := secure.NewIdentity()
	if err != nil {
		return nil, err
	}
	offerPayload, pp, err := secure.SealPairOffer(link, secure.PairOffer{
		DevicePub: id.Public(),
		Name:      deviceName,
		Platform:  goruntime.GOOS,
	})
	if err != nil {
		return nil, err
	}

	ws, _, err := websocket.Dial(ctx, link.RelayURL, nil)
	if err != nil {
		return nil, fmt.Errorf("dial relay: %w", err)
	}
	ws.SetReadLimit(maxFrameBytes)
	defer ws.CloseNow()

	offer := wire.RelayMsg{
		Type:      wire.TypePairOffer,
		PairingID: link.PairingID,
		Payload:   b64.EncodeToString(offerPayload),
	}
	if err := writeMsg(ctx, ws, offer); err != nil {
		return nil, err
	}
	for {
		msg, err := readMsg(ctx, ws)
		if err != nil {
			return nil, fmt.Errorf("waiting for pairing answer: %w", err)
		}
		switch msg.Type {
		case wire.TypePairAnswer:
			payload, err := b64.DecodeString(msg.Payload)
			if err != nil {
				return nil, fmt.Errorf("pairing answer payload: %w", err)
			}
			answer, err := pp.OpenPairAnswer(payload, id.Public())
			if err != nil {
				return nil, err
			}
			return &Credentials{
				V:          1,
				DeviceSeed: b64.EncodeToString(id.Seed()),
				DeviceName: deviceName,
				HostPub:    secure.EncodeKey(answer.HostPub),
				HostName:   answer.HostName,
				RelayURL:   link.RelayURL,
			}, nil
		case wire.TypePairErr:
			return nil, fmt.Errorf("pairing rejected: %s", msg.Code)
		default:
			// Ignore anything else.
		}
	}
}

// HostState is the host's live snapshot as seen by the phone.
type HostState struct {
	Ver     uint64
	Host    wire.HostInfo
	Running []wire.RunningTurn
}

// AttachEvent reports a completed attach. When Resumed is false the phone
// got a fresh app-server connection: Protocol() returns a new client and any
// previously fetched state must be refetched.
type AttachEvent struct {
	Session string
	Resumed bool
}

// Options tunes a Client.
type Options struct {
	Logf         func(format string, args ...any)
	ReconnectMin time.Duration
	ReconnectMax time.Duration
}

// Client keeps one phone connected to its paired host.
type Client struct {
	id       *secure.Identity
	hostPub  []byte
	relayURL string
	name     string
	logf     func(format string, args ...any)

	reconnectMin time.Duration
	reconnectMax time.Duration

	mu        sync.Mutex
	ws        *websocket.Conn
	channel   *secure.Channel
	pendingHS *secure.Handshake
	attached  bool
	contID    string
	lastRecv  uint64
	ackDirty  bool
	proto     *exec.ProtocolClient
	protoFeed *io.PipeWriter
	state     *HostState
	waiters   []chan struct{}
	stateCh   chan HostState
	attachCh  chan AttachEvent
	writeMu   sync.Mutex
	runCtx    context.Context
}

func NewClient(creds *Credentials, opts Options) (*Client, error) {
	seed, err := b64.DecodeString(creds.DeviceSeed)
	if err != nil {
		return nil, fmt.Errorf("device seed: %w", err)
	}
	id, err := secure.IdentityFromSeed(seed)
	if err != nil {
		return nil, err
	}
	hostPub, err := secure.DecodeKey(creds.HostPub)
	if err != nil {
		return nil, fmt.Errorf("host pub: %w", err)
	}
	logf := opts.Logf
	if logf == nil {
		logf = func(string, ...any) {}
	}
	c := &Client{
		id:           id,
		hostPub:      hostPub,
		relayURL:     creds.RelayURL,
		name:         creds.DeviceName,
		logf:         logf,
		reconnectMin: opts.ReconnectMin,
		reconnectMax: opts.ReconnectMax,
		stateCh:      make(chan HostState, 16),
		attachCh:     make(chan AttachEvent, 16),
	}
	if c.reconnectMin <= 0 {
		c.reconnectMin = time.Second
	}
	if c.reconnectMax <= 0 {
		c.reconnectMax = 30 * time.Second
	}
	return c, nil
}

// Run maintains the connection until ctx is cancelled.
func (c *Client) Run(ctx context.Context) error {
	c.runCtx = ctx
	attempt := 0
	for {
		authed, err := c.runOnce(ctx)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if authed {
			attempt = 0
		}
		delay := c.reconnectMin << attempt
		if delay > c.reconnectMax || delay <= 0 {
			delay = c.reconnectMax
		} else if attempt < 30 {
			attempt++
		}
		if err != nil {
			c.logf("remote phone: connection lost: %v (retry in %s)", err, delay)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delay):
		}
	}
}

func (c *Client) runOnce(ctx context.Context) (bool, error) {
	dialCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	ws, _, err := websocket.Dial(dialCtx, c.relayURL, nil)
	cancel()
	if err != nil {
		return false, err
	}
	ws.SetReadLimit(maxFrameBytes)
	defer func() {
		c.mu.Lock()
		if c.ws == ws {
			c.ws = nil
		}
		c.channel = nil
		c.pendingHS = nil
		c.attached = false
		c.mu.Unlock()
		_ = ws.CloseNow()
	}()

	hostOnline, err := c.authenticate(ctx, ws)
	if err != nil {
		return false, err
	}
	c.mu.Lock()
	c.ws = ws
	c.mu.Unlock()

	if hostOnline {
		c.startHandshake()
	}

	loopCtx, stopLoops := context.WithCancel(ctx)
	defer stopLoops()
	go c.ackLoop(loopCtx)
	go c.pingLoop(loopCtx, ws)

	for {
		msg, err := readMsg(ctx, ws)
		if err != nil {
			return true, err
		}
		c.dispatch(msg)
	}
}

func (c *Client) authenticate(ctx context.Context, ws *websocket.Conn) (bool, error) {
	hello := wire.RelayMsg{
		Type:  wire.TypeHello,
		Proto: wire.ProtoVersion,
		Role:  wire.RolePhone,
		Pub:   secure.EncodeKey(c.id.Public()),
	}
	if err := writeMsg(ctx, ws, hello); err != nil {
		return false, err
	}
	challenge, err := readMsg(ctx, ws)
	if err != nil {
		return false, err
	}
	if challenge.Type != wire.TypeChallenge {
		return false, fmt.Errorf("relay: expected challenge, got %s (%s)", challenge.Type, challenge.Msg)
	}
	nonce, err := b64.DecodeString(challenge.Nonce)
	if err != nil {
		return false, err
	}
	auth := wire.RelayMsg{Type: wire.TypeAuth, Sig: b64.EncodeToString(c.id.SignRelayAuth(nonce, wire.RolePhone))}
	if err := writeMsg(ctx, ws, auth); err != nil {
		return false, err
	}
	ok, err := readMsg(ctx, ws)
	if err != nil {
		return false, err
	}
	if ok.Type != wire.TypeAuthOK {
		return false, fmt.Errorf("relay auth failed: %s (%s)", ok.Code, ok.Msg)
	}
	return ok.Online != nil && *ok.Online, nil
}

func (c *Client) dispatch(msg wire.RelayMsg) {
	switch msg.Type {
	case wire.TypeFrame:
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
			c.handleHS2(body)
		case wire.KindSealed:
			c.handleSealed(body)
		}
	case wire.TypePresence:
		if msg.Pub == secure.EncodeKey(c.hostPub) && msg.Online != nil {
			if *msg.Online {
				c.startHandshake()
			} else {
				c.markDetached()
			}
		}
	case wire.TypeDeliverErr:
		c.markDetached()
	}
}

// startHandshake sends a fresh HS1 toward the host.
func (c *Client) startHandshake() {
	c.mu.Lock()
	defer c.mu.Unlock()
	hs, hs1, err := secure.NewHandshake(c.id, c.hostPub)
	if err != nil {
		return
	}
	c.pendingHS = hs
	c.channel = nil
	c.attached = false
	body, err := wire.EncodeE2E(wire.E2EMsg{
		T:         wire.E2EHS1,
		DevicePub: hs1.DevicePub,
		Eph:       hs1.Eph,
		Nonce:     hs1.Nonce,
		Sig:       hs1.Sig,
	})
	if err != nil {
		return
	}
	_ = c.sendFrameLocked(wire.WrapPayload(wire.KindHandshake, body))
}

func (c *Client) handleHS2(body []byte) {
	msg, err := wire.DecodeE2E(body)
	if err != nil || msg.T != wire.E2EHS2 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.pendingHS == nil {
		return
	}
	ch, err := c.pendingHS.Complete(secure.HS2{Eph: msg.Eph, Nonce: msg.Nonce, Sig: msg.Sig})
	if err != nil {
		c.logf("remote phone: handshake failed: %v", err)
		c.pendingHS = nil
		return
	}
	c.pendingHS = nil
	c.channel = ch
	// Attach immediately: quote the previous continuity id and the highest
	// applied seq so the host can replay exactly what we missed.
	c.sendSealedLocked(wire.E2EMsg{T: wire.E2EAttach, Prev: c.contID, Recv: c.lastRecv})
}

func (c *Client) handleSealed(body []byte) {
	c.mu.Lock()
	ch := c.channel
	if ch == nil {
		c.mu.Unlock()
		return
	}
	plain, err := ch.Open(body)
	if err != nil {
		c.mu.Unlock()
		return
	}
	msg, err := wire.DecodeE2E(plain)
	if err != nil {
		c.mu.Unlock()
		return
	}

	switch msg.T {
	case wire.E2EAttached:
		c.handleAttachedLocked(msg)
		c.mu.Unlock()
	case wire.E2ERPC:
		if !c.attached || msg.Seq <= c.lastRecv {
			c.mu.Unlock()
			return
		}
		c.lastRecv = msg.Seq
		c.ackDirty = true
		feed := c.protoFeed
		c.mu.Unlock()
		if feed != nil && len(msg.Line) > 0 {
			line := append(bytes.TrimSpace(msg.Line), '\n')
			_, _ = feed.Write(line)
		}
	case wire.E2EState:
		st := HostState{Ver: msg.Ver, Running: msg.Running}
		if msg.Host != nil {
			st.Host = *msg.Host
		}
		c.state = &st
		c.mu.Unlock()
		select {
		case c.stateCh <- st:
		default:
		}
	case wire.E2EPong:
		c.mu.Unlock()
	case wire.E2EBye:
		c.channel = nil
		c.attached = false
		c.mu.Unlock()
	default:
		c.mu.Unlock()
	}
}

// handleAttachedLocked finalizes an attach. Called with c.mu held.
func (c *Client) handleAttachedLocked(msg wire.E2EMsg) {
	fresh := !msg.Resumed || c.proto == nil || msg.Session != c.contID
	c.contID = msg.Session
	c.attached = true
	c.ackDirty = false
	if fresh {
		if c.protoFeed != nil {
			_ = c.protoFeed.Close()
		}
		c.lastRecv = 0
		feedR, feedW := io.Pipe()
		c.protoFeed = feedW
		c.proto = exec.NewProtocolClient(feedR, &protoWriter{c: c})
	}
	for _, w := range c.waiters {
		close(w)
	}
	c.waiters = nil
	select {
	case c.attachCh <- AttachEvent{Session: msg.Session, Resumed: msg.Resumed}:
	default:
	}
	c.logf("remote phone: attached (session=%s resumed=%v)", msg.Session, msg.Resumed)
}

func (c *Client) markDetached() {
	c.mu.Lock()
	c.channel = nil
	c.attached = false
	c.mu.Unlock()
}

// ackLoop flushes cumulative acks so the host can trim its spool.
func (c *Client) ackLoop(ctx context.Context) {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.mu.Lock()
			if c.attached && c.ackDirty && c.channel != nil {
				c.ackDirty = false
				c.sendSealedLocked(wire.E2EMsg{T: wire.E2EAck, Recv: c.lastRecv})
			}
			c.mu.Unlock()
		}
	}
}

func (c *Client) pingLoop(ctx context.Context, ws *websocket.Conn) {
	ticker := time.NewTicker(20 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			pctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			err := ws.Ping(pctx)
			cancel()
			if err != nil {
				_ = ws.CloseNow()
				return
			}
		}
	}
}

// sendSealedLocked seals and sends one e2e message. Called with c.mu held.
func (c *Client) sendSealedLocked(msg wire.E2EMsg) {
	if c.channel == nil {
		return
	}
	plain, err := wire.EncodeE2E(msg)
	if err != nil {
		return
	}
	sealed := c.channel.Seal(plain)
	if err := c.sendFrameLocked(wire.WrapPayload(wire.KindSealed, sealed)); err != nil {
		c.channel = nil
		c.attached = false
	}
}

// sendFrameLocked ships a payload to the host. Called with c.mu held.
func (c *Client) sendFrameLocked(payload []byte) error {
	ws := c.ws
	if ws == nil {
		return errors.New("relay not connected")
	}
	msg := wire.RelayMsg{
		Type:    wire.TypeFrame,
		To:      secure.EncodeKey(c.hostPub),
		Payload: b64.EncodeToString(payload),
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return ws.Write(ctx, websocket.MessageText, data)
}

// protoWriter adapts the ProtocolClient's output into sealed rpc frames.
type protoWriter struct {
	c *Client
}

func (w *protoWriter) Write(p []byte) (int, error) {
	line := bytes.TrimSpace(p)
	if len(line) == 0 {
		return len(p), nil
	}
	w.c.mu.Lock()
	defer w.c.mu.Unlock()
	if !w.c.attached || w.c.channel == nil {
		return 0, errors.New("not attached to host")
	}
	w.c.sendSealedLocked(wire.E2EMsg{T: wire.E2ERPC, Line: json.RawMessage(append([]byte(nil), line...))})
	return len(p), nil
}

// WaitAttached blocks until the client is attached to the host.
func (c *Client) WaitAttached(ctx context.Context) error {
	c.mu.Lock()
	if c.attached {
		c.mu.Unlock()
		return nil
	}
	w := make(chan struct{})
	c.waiters = append(c.waiters, w)
	c.mu.Unlock()
	select {
	case <-w:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Protocol returns the app-server protocol client for the current
// connection, or nil before the first attach. After an AttachEvent with
// Resumed=false, call this again: the previous client is dead.
func (c *Client) Protocol() *exec.ProtocolClient {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.proto
}

// LatestState returns the most recent host state snapshot.
func (c *Client) LatestState() (HostState, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.state == nil {
		return HostState{}, false
	}
	return *c.state, true
}

// States streams host state snapshots (latest-wins buffer).
func (c *Client) States() <-chan HostState { return c.stateCh }

// AttachEvents streams attach completions.
func (c *Client) AttachEvents() <-chan AttachEvent { return c.attachCh }

// --- websocket helpers -------------------------------------------------------

func writeMsg(ctx context.Context, ws *websocket.Conn, msg wire.RelayMsg) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	return ws.Write(ctx, websocket.MessageText, data)
}

func readMsg(ctx context.Context, ws *websocket.Conn) (wire.RelayMsg, error) {
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
