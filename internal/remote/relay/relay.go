// Package relay implements the wuu remote-control relay: a blind switchboard
// that lets phones reach hosts across NATs. The relay authenticates devices
// by Ed25519 challenge-response, routes opaque end-to-end encrypted frames
// between a host and its enrolled phones, carries the (equally opaque)
// pairing exchange, tracks presence, and forwards content-free push hints to
// a pluggable Pusher. It can decrypt nothing: all application content is
// sealed by internal/remote/secure before it ever reaches a socket.
package relay

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/blueberrycongee/wuu/internal/remote/account"
	"github.com/blueberrycongee/wuu/internal/remote/secure"
	"github.com/blueberrycongee/wuu/internal/remote/wire"
)

const (
	// maxFrameBytes bounds a single relay message. App-server lines are
	// capped at 4MB; base64 plus envelope overhead fits comfortably below
	// this.
	maxFrameBytes = 16 << 20

	writeTimeout   = 15 * time.Second
	pairingTimeout = 90 * time.Second
)

// Options configures a relay server.
type Options struct {
	Registry          *Registry
	Accounts          *account.Store
	AllowRegistration bool
	PushPlatforms     []string
	Pusher            Pusher
	Logf              func(format string, args ...any)
	// PushMinInterval throttles push events per device. Zero means the
	// default of 30 seconds.
	PushMinInterval time.Duration
}

// Server routes relay traffic. Wire it into an http.Server via Handler.
type Server struct {
	reg             *Registry
	accounts        *account.Store
	accountHTTP     *account.HTTP
	pusher          Pusher
	logf            func(format string, args ...any)
	pushMinInterval time.Duration

	mu          sync.Mutex
	conns       map[string]*conn // authenticated devices by public key
	pairings    map[string]*conn // open pairing windows by pairing id -> host conn
	pairWaiters map[string]*conn // pairing guests by pairing id
	pushLast    map[string]time.Time
}

func New(opts Options) *Server {
	reg := opts.Registry
	if reg == nil {
		reg, _ = OpenRegistry("")
	}
	logf := opts.Logf
	if logf == nil {
		logf = log.Printf
	}
	pusher := opts.Pusher
	if pusher == nil {
		pusher = LogPusher{Logf: logf}
	}
	interval := opts.PushMinInterval
	if interval == 0 {
		interval = 30 * time.Second
	}
	s := &Server{
		reg:             reg,
		pusher:          pusher,
		logf:            logf,
		pushMinInterval: interval,
		conns:           map[string]*conn{},
		pairings:        map[string]*conn{},
		pairWaiters:     map[string]*conn{},
		pushLast:        map[string]time.Time{},
	}
	if opts.Accounts != nil {
		s.accounts = opts.Accounts
		s.accountHTTP = account.NewHTTP(opts.Accounts, opts.AllowRegistration)
		s.accountHTTP.PushPlatforms = opts.PushPlatforms
		s.accountHTTP.Online = func(pub string) bool { s.mu.Lock(); defer s.mu.Unlock(); return s.conns[pub] != nil }
		s.accountHTTP.Changed = s.accountChanged
	}
	return s
}

// Handler returns the HTTP mux: GET /healthz and the websocket endpoint at
// /v1/connect.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	if s.accountHTTP != nil {
		mux.Handle("/v1/account/", s.accountHTTP)
	}
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("/v1/connect", s.handleConnect)
	return mux
}

type conn struct {
	ws      *websocket.Conn
	writeMu sync.Mutex

	// Set once authentication completes.
	pub     string
	role    string
	account string
}

func (c *conn) send(msg wire.RelayMsg) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), writeTimeout)
	defer cancel()
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return c.ws.Write(ctx, websocket.MessageText, data)
}

func (s *Server) handleConnect(w http.ResponseWriter, r *http.Request) {
	ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		// Non-browser clients today; a future web client terminates TLS at
		// the relay operator's proxy and shares its origin policy.
		OriginPatterns: []string{"*"},
	})
	if err != nil {
		return
	}
	ws.SetReadLimit(maxFrameBytes)
	c := &conn{ws: ws}
	defer s.dropConn(c)

	ctx := r.Context()
	first, err := s.read(ctx, c)
	if err != nil {
		return
	}
	if s.accounts != nil && first.Type != wire.TypeHello {
		return
	}
	switch first.Type {
	case wire.TypeHello:
		if !s.authenticate(ctx, c, first) {
			return
		}
		s.serveAuthed(ctx, c)
	case wire.TypePairOffer:
		s.servePairingGuest(ctx, c, first)
	default:
		_ = c.send(wire.RelayMsg{Type: wire.TypeErr, Code: wire.CodeBadRequest, Msg: "expected hello or pair_offer"})
	}
}

func (s *Server) read(ctx context.Context, c *conn) (wire.RelayMsg, error) {
	_, data, err := c.ws.Read(ctx)
	if err != nil {
		return wire.RelayMsg{}, err
	}
	var msg wire.RelayMsg
	if err := json.Unmarshal(data, &msg); err != nil {
		return wire.RelayMsg{}, fmt.Errorf("decode relay message: %w", err)
	}
	return msg, nil
}

// authenticate runs the challenge-response flow for a hello message and, on
// success, registers the connection. A host authenticates into its own
// account; a phone must already be enrolled under some account.
func (s *Server) authenticate(ctx context.Context, c *conn, hello wire.RelayMsg) bool {
	fail := func(code, msg string) bool {
		_ = c.send(wire.RelayMsg{Type: wire.TypeAuthErr, Code: code, Msg: msg})
		return false
	}
	if hello.Proto != wire.ProtoVersion {
		return fail(wire.CodeBadRequest, fmt.Sprintf("unsupported proto %d", hello.Proto))
	}
	if hello.Role != wire.RoleHost && hello.Role != wire.RolePhone {
		return fail(wire.CodeBadRequest, "role must be host or phone")
	}
	pub, err := secure.DecodeKey(hello.Pub)
	if err != nil {
		return fail(wire.CodeBadRequest, "bad public key")
	}

	nonce := make([]byte, 32)
	if _, err := rand.Read(nonce); err != nil {
		return fail(wire.CodeBadRequest, "nonce generation failed")
	}
	if err := c.send(wire.RelayMsg{Type: wire.TypeChallenge, Nonce: base64.RawURLEncoding.EncodeToString(nonce)}); err != nil {
		return false
	}
	authMsg, err := s.read(ctx, c)
	if err != nil {
		return false
	}
	if authMsg.Type != wire.TypeAuth {
		return fail(wire.CodeBadRequest, "expected auth")
	}
	sig, err := base64.RawURLEncoding.DecodeString(authMsg.Sig)
	if err != nil {
		return fail(wire.CodeBadRequest, "bad signature encoding")
	}
	if !secure.VerifyRelayAuth(pub, nonce, sig, hello.Role) {
		return fail(wire.CodeBadSignature, "challenge signature invalid")
	}

	pubKey := hello.Pub
	var account string
	if s.accounts != nil {
		d, ok := s.accounts.Device(pubKey)
		if !ok || d.Role != hello.Role {
			return fail(wire.CodeUnauthorized, "login required or device revoked")
		}
		account = d.Account
	} else {
		switch hello.Role {
		case wire.RoleHost:
			account = pubKey
			if err := s.reg.EnsureAccount(account); err != nil {
				s.logf("relay: ensure account: %v", err)
				return fail(wire.CodeBadRequest, "registry unavailable")
			}
		case wire.RolePhone:
			acct, ok := s.reg.AccountByDevice(pubKey)
			if !ok {
				return fail(wire.CodeUnknownDevice, "device not enrolled; pair first")
			}
			account = acct
		}

	}
	c.pub = pubKey
	c.role = hello.Role
	c.account = account

	// Register, superseding any previous connection for the same device.
	s.mu.Lock()
	if old := s.conns[pubKey]; old != nil {
		_ = old.ws.Close(websocket.StatusPolicyViolation, wire.CodeSuperseded)
	}
	s.conns[pubKey] = c
	var peerOnline bool
	if hello.Role == wire.RolePhone {
		target := account
		if s.accounts != nil {
			target = hello.To
			d, ok := s.accounts.Device(target)
			if !ok || d.Account != account || d.Role != wire.RoleHost {
				s.mu.Unlock()
				return fail(wire.CodeUnauthorized, "unknown account computer")
			}
		}
		_, peerOnline = s.conns[target]
	}
	s.mu.Unlock()

	if err := c.send(wire.RelayMsg{Type: wire.TypeAuthOK, Pub: pubKey, Online: &peerOnline}); err != nil {
		return false
	}
	s.broadcastPresence(c, true)
	return true
}

// serveAuthed is the main read loop for an authenticated device.
func (s *Server) serveAuthed(ctx context.Context, c *conn) {
	for {
		msg, err := s.read(ctx, c)
		if err != nil {
			return
		}
		if s.accounts != nil && msg.Type != wire.TypeFrame && msg.Type != wire.TypePush && msg.Type != "ping" {
			_ = c.send(wire.RelayMsg{Type: wire.TypeErr, Code: wire.CodeUnauthorized, Msg: "use the account API for device management"})
			continue
		}
		switch msg.Type {
		case wire.TypeFrame:
			s.routeFrame(c, msg)
		case wire.TypePairOpen:
			s.handlePairOpen(c, msg)
		case wire.TypePairClose:
			s.handlePairClose(c, msg, wire.CodeNoSuchPairing)
		case wire.TypePairAnswer, wire.TypePairErr:
			s.handlePairAnswer(c, msg)
		case wire.TypeDeviceAdd:
			s.handleDeviceAdd(c, msg)
		case wire.TypeDeviceRm:
			s.handleDeviceRemove(c, msg)
		case wire.TypeDeviceList:
			s.handleDeviceList(c)
		case wire.TypePush:
			s.handlePush(c, msg)
		case "ping":
			_ = c.send(wire.RelayMsg{Type: "pong"})
		default:
			_ = c.send(wire.RelayMsg{Type: wire.TypeErr, Code: wire.CodeBadRequest, Msg: "unknown message " + msg.Type})
		}
	}
}

// routeFrame forwards an opaque payload between a host and one of its
// enrolled phones. Any other pairing of endpoints is refused.
func (s *Server) routeFrame(c *conn, msg wire.RelayMsg) {
	target := msg.To
	if s.accounts != nil {
		from, ok := s.accounts.Device(c.pub)
		dest, found := s.accounts.Device(target)
		if !ok || !found || from.Account != dest.Account || from.Role == dest.Role {
			_ = c.send(wire.RelayMsg{Type: wire.TypeDeliverErr, To: target, Code: wire.CodeUnauthorized})
			return
		}
	} else {
		switch c.role {
		case wire.RoleHost:
			if !s.reg.HasDevice(c.account, target) {
				_ = c.send(wire.RelayMsg{Type: wire.TypeDeliverErr, To: target, Code: wire.CodeUnknownDevice})
				return
			}
		case wire.RolePhone:
			if target == "" {
				target = c.account
			}
			if target != c.account {
				_ = c.send(wire.RelayMsg{Type: wire.TypeDeliverErr, To: target, Code: wire.CodeUnauthorized})
				return
			}
		}
	}
	s.mu.Lock()
	dest := s.conns[target]
	s.mu.Unlock()
	if dest == nil {
		_ = c.send(wire.RelayMsg{Type: wire.TypeDeliverErr, To: target, Code: wire.CodeOffline})
		return
	}
	err := dest.send(wire.RelayMsg{Type: wire.TypeFrame, From: c.pub, Payload: msg.Payload})
	if err != nil {
		_ = c.send(wire.RelayMsg{Type: wire.TypeDeliverErr, To: target, Code: wire.CodeOffline})
	}
}

func (s *Server) handlePairOpen(c *conn, msg wire.RelayMsg) {
	if c.role != wire.RoleHost || msg.PairingID == "" {
		_ = c.send(wire.RelayMsg{Type: wire.TypeErr, Code: wire.CodeUnauthorized, Msg: "pair_open requires host role"})
		return
	}
	s.mu.Lock()
	s.pairings[msg.PairingID] = c
	s.mu.Unlock()
	_ = c.send(wire.RelayMsg{Type: wire.TypeOK, PairingID: msg.PairingID})
}

func (s *Server) handlePairClose(c *conn, msg wire.RelayMsg, waiterCode string) {
	s.mu.Lock()
	if s.pairings[msg.PairingID] == c {
		delete(s.pairings, msg.PairingID)
	}
	waiter := s.pairWaiters[msg.PairingID]
	delete(s.pairWaiters, msg.PairingID)
	s.mu.Unlock()
	if waiter != nil {
		_ = waiter.send(wire.RelayMsg{Type: wire.TypePairErr, PairingID: msg.PairingID, Code: waiterCode})
		_ = waiter.ws.Close(websocket.StatusNormalClosure, "pairing closed")
	}
}

// handlePairAnswer forwards the host's pairing answer (or error) to the
// waiting guest and completes the pairing exchange.
func (s *Server) handlePairAnswer(c *conn, msg wire.RelayMsg) {
	if c.role != wire.RoleHost {
		return
	}
	s.mu.Lock()
	waiter := s.pairWaiters[msg.PairingID]
	delete(s.pairWaiters, msg.PairingID)
	s.mu.Unlock()
	if waiter == nil {
		return
	}
	_ = waiter.send(msg)
	_ = waiter.ws.Close(websocket.StatusNormalClosure, "pairing complete")
}

// servePairingGuest handles an unauthenticated connection whose first message
// is a pairing offer. The guest gets exactly one exchange: offer in, answer
// (or error) out, then the connection closes.
func (s *Server) servePairingGuest(ctx context.Context, c *conn, offer wire.RelayMsg) {
	pid := offer.PairingID
	s.mu.Lock()
	host := s.pairings[pid]
	var prev *conn
	if host != nil {
		prev = s.pairWaiters[pid]
		s.pairWaiters[pid] = c
	}
	s.mu.Unlock()

	if host == nil {
		_ = c.send(wire.RelayMsg{Type: wire.TypePairErr, PairingID: pid, Code: wire.CodeNoSuchPairing})
		return
	}
	if prev != nil {
		_ = prev.ws.Close(websocket.StatusPolicyViolation, wire.CodeSuperseded)
	}
	if err := host.send(wire.RelayMsg{Type: wire.TypePairOffer, PairingID: pid, Payload: offer.Payload}); err != nil {
		s.mu.Lock()
		if s.pairWaiters[pid] == c {
			delete(s.pairWaiters, pid)
		}
		s.mu.Unlock()
		_ = c.send(wire.RelayMsg{Type: wire.TypePairErr, PairingID: pid, Code: wire.CodeOffline})
		return
	}

	// Hold the connection open until the host answers (handlePairAnswer
	// writes to us and closes the socket) or the window times out.
	waitCtx, cancel := context.WithTimeout(ctx, pairingTimeout)
	defer cancel()
	for {
		if _, err := s.read(waitCtx, c); err != nil {
			s.mu.Lock()
			if s.pairWaiters[pid] == c {
				delete(s.pairWaiters, pid)
			}
			s.mu.Unlock()
			return
		}
		// Guests have nothing else to say; ignore further messages.
	}
}

func (s *Server) handleDeviceAdd(c *conn, msg wire.RelayMsg) {
	if c.role != wire.RoleHost {
		_ = c.send(wire.RelayMsg{Type: wire.TypeErr, Code: wire.CodeUnauthorized, Msg: "device_add requires host role"})
		return
	}
	if _, err := secure.DecodeKey(msg.Pub); err != nil {
		_ = c.send(wire.RelayMsg{Type: wire.TypeErr, Code: wire.CodeBadRequest, Msg: "bad device key"})
		return
	}
	if err := s.reg.AddDevice(c.account, msg.Pub, msg.Name, time.Now()); err != nil {
		s.logf("relay: add device: %v", err)
		_ = c.send(wire.RelayMsg{Type: wire.TypeErr, Code: wire.CodeBadRequest, Msg: "registry write failed"})
		return
	}
	_ = c.send(wire.RelayMsg{Type: wire.TypeOK, Pub: msg.Pub})
}

func (s *Server) handleDeviceRemove(c *conn, msg wire.RelayMsg) {
	if c.role != wire.RoleHost {
		_ = c.send(wire.RelayMsg{Type: wire.TypeErr, Code: wire.CodeUnauthorized, Msg: "device_remove requires host role"})
		return
	}
	if err := s.reg.RemoveDevice(c.account, msg.Pub); err != nil {
		s.logf("relay: remove device: %v", err)
	}
	s.mu.Lock()
	dev := s.conns[msg.Pub]
	s.mu.Unlock()
	if dev != nil && dev.account == c.account {
		_ = dev.ws.Close(websocket.StatusPolicyViolation, "device revoked")
	}
	_ = c.send(wire.RelayMsg{Type: wire.TypeOK, Pub: msg.Pub})
}

func (s *Server) handleDeviceList(c *conn) {
	if c.role != wire.RoleHost {
		_ = c.send(wire.RelayMsg{Type: wire.TypeErr, Code: wire.CodeUnauthorized, Msg: "device_list requires host role"})
		return
	}
	devices := s.reg.Devices(c.account)
	out := make([]wire.RelayDevice, 0, len(devices))
	s.mu.Lock()
	for _, d := range devices {
		_, online := s.conns[d.Pub]
		out = append(out, wire.RelayDevice{
			Pub:     d.Pub,
			Name:    d.Info.Name,
			AddedAt: d.Info.AddedAt.Format(time.RFC3339),
			Online:  online,
		})
	}
	s.mu.Unlock()
	_ = c.send(wire.RelayMsg{Type: wire.TypeDevices, Devices: out})
}

// handlePush forwards a content-free push hint to the configured Pusher,
// throttled per target device.
func (s *Server) handlePush(c *conn, msg wire.RelayMsg) {
	if c.role != wire.RoleHost {
		return
	}
	if msg.Hint != wire.PushAgentDone && msg.Hint != wire.PushNeedsInput {
		return
	}
	targets := []string{}
	if s.accounts != nil {
		source, ok := s.accounts.Device(c.pub)
		if !ok || source.Account != c.account || source.Role != "host" {
			return
		}
		devices, err := s.accounts.Devices(c.account)
		if err != nil {
			return
		}
		for _, device := range devices {
			if device.Role == "phone" && (msg.To == "" || msg.To == device.Pub) {
				targets = append(targets, device.Pub)
			}
		}
	} else if msg.To != "" {
		if s.reg.HasDevice(c.account, msg.To) {
			targets = append(targets, msg.To)
		}
	} else {
		for _, d := range s.reg.Devices(c.account) {
			targets = append(targets, d.Pub)
		}
	}
	now := time.Now()
	for _, target := range targets {
		s.mu.Lock()
		last := s.pushLast[target]
		throttled := now.Sub(last) < s.pushMinInterval
		if !throttled {
			s.pushLast[target] = now
		}
		s.mu.Unlock()
		if throttled {
			continue
		}
		s.pusher.Push(context.Background(), PushEvent{
			Account: c.account,
			Device:  target,
			Hint:    msg.Hint,
			At:      now.UTC(),
			Host:    c.pub,
		})
	}
}

// broadcastPresence tells the other side of the account that a device came or
// went: hosts learn about their phones, phones learn about their host.
func (s *Server) broadcastPresence(c *conn, online bool) {
	if c.pub == "" {
		return
	}
	s.mu.Lock()
	var recipients []*conn
	if s.accounts != nil {
		for _, other := range s.conns {
			if other.account == c.account && other.role != c.role {
				recipients = append(recipients, other)
			}
		}
	} else {
		switch c.role {
		case wire.RolePhone:
			if host := s.conns[c.account]; host != nil {
				recipients = append(recipients, host)
			}
		case wire.RoleHost:
			for pub, other := range s.conns {
				if other.role == wire.RolePhone && other.account == c.account && pub != c.pub {
					recipients = append(recipients, other)
				}
			}
		}
	}
	s.mu.Unlock()
	for _, r := range recipients {
		_ = r.send(wire.RelayMsg{Type: wire.TypePresence, Pub: c.pub, Online: &online})
	}
}

// dropConn cleans up when a connection ends for any reason.
func (s *Server) dropConn(c *conn) {
	_ = c.ws.CloseNow()
	if c.pub == "" {
		return
	}
	s.mu.Lock()
	if s.conns[c.pub] == c {
		delete(s.conns, c.pub)
	} else {
		// Superseded by a newer connection; the newer one owns presence now.
		s.mu.Unlock()
		return
	}
	var orphanWaiters []*conn
	if c.role == wire.RoleHost {
		for pid, host := range s.pairings {
			if host == c {
				delete(s.pairings, pid)
				if w := s.pairWaiters[pid]; w != nil {
					orphanWaiters = append(orphanWaiters, w)
					delete(s.pairWaiters, pid)
				}
			}
		}
	}
	s.mu.Unlock()
	for _, w := range orphanWaiters {
		_ = w.send(wire.RelayMsg{Type: wire.TypePairErr, Code: wire.CodeOffline})
		_ = w.ws.Close(websocket.StatusNormalClosure, "host disconnected")
	}
	s.broadcastPresence(c, false)
}

// Disconnect both endpoints when membership changes. This also invalidates the
// host's current channel; a fresh handshake checks the authoritative directory.
func (s *Server) accountChanged(account string) {
	s.mu.Lock()
	var conns []*conn
	for _, c := range s.conns {
		if c.account == account {
			conns = append(conns, c)
		}
	}
	s.mu.Unlock()
	for _, c := range conns {
		_ = c.ws.CloseNow()
	}
}
