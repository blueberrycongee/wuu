package appserver

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/blueberrycongee/wuu/internal/principal"
)

// TrustedLocalTransportVersion is the authenticated embedding-hop protocol. It
// is not a public remote RPC: ingress adapters must strip/reject transport/*.
const TrustedLocalTransportVersion = 1
const maxTransportReceipts = 65536

type trustedTransportReceipt struct {
	peer  string
	guard func() error
}
type trustedTransportBinding struct {
	envelope  trustedTransportEnvelope
	principal principal.Verified
	revoked   atomic.Bool
}
type trustedTransportValidator struct {
	Address string `json:"address"`
	Token   string `json:"token"`
}
type trustedTransportState struct {
	generation   string
	bindingOrder []string
	bindings     map[string]*trustedTransportBinding
	enabled      bool
	mu           sync.Mutex
	pending      map[string]string
	receipts     map[string]trustedTransportReceipt
	order        []string
}
type trustedTransportEnvelope struct {
	Validation trustedTransportValidator `json:"validation"`
	Version    int                       `json:"version"`
	PeerID     string                    `json:"peer_id"`
	Principal  principal.Identity        `json:"principal"`
	Owner      bool                      `json:"owner"`
	Method     string                    `json:"method"`
	Params     json.RawMessage           `json:"params"`
	Receipt    string                    `json:"receipt"`
}
type trustedTransportFrame struct {
	Generation string   `json:"generation"`
	Version    int      `json:"version"`
	Message    Response `json:"message"`
	Receipt    string   `json:"receipt"`
}

// WithTrustedLocalTransport enables provenance forwarding only on an embedding's
// private, local-owner stdio. Never enable it on a network or authenticated user
// connection. The receiver checks both this opt-in and the original principal.
func WithTrustedLocalTransport() ServerOption {
	return func(s *Server) error {
		s.trustedTransport.enabled = true
		return nil
	}
}
func (s *Server) handleTrustedTransport(ctx context.Context, req Request) error {
	deny := func(err error) error { return s.writeResponse(req.ID, nil, err) }
	local := principal.LocalOwner()
	caller := s.ConnectionPrincipal()
	if !s.trustedTransport.enabled || !caller.IsOwner() || caller.Identity() != local.Identity() {
		return deny(errors.New("trusted local transport required"))
	}
	// A nested envelope must never regain the outer pipe's owner authority.
	if forwarded, ok := principal.FromContext(ctx); ok && (forwarded.Identity() != local.Identity() || !forwarded.IsOwner()) {
		return deny(errors.New("nested transport forbidden"))
	}
	var input trustedTransportEnvelope
	if json.Unmarshal(req.Params, &input) != nil || input.Version != TrustedLocalTransportVersion || input.PeerID == "" || len(input.PeerID) > 256 {
		return deny(errors.New("invalid trusted transport envelope"))
	}
	switch req.Method {
	case "transport/check":
		s.trustedTransport.mu.Lock()
		receipt, ok := s.trustedTransport.receipts[input.Receipt]
		s.trustedTransport.mu.Unlock()
		if !ok || receipt.peer != input.PeerID {
			return deny(errors.New("transport receipt unavailable"))
		}
		if err := receipt.guard(); err != nil {
			return deny(err)
		}
		return s.writeResponse(req.ID, map[string]bool{"allowed": true}, nil)
	case "transport/release":
		s.trustedTransport.mu.Lock()
		if binding := s.trustedTransport.bindings[input.PeerID]; binding != nil {
			binding.revoked.Store(true)
			delete(s.trustedTransport.bindings, input.PeerID)
		}
		for token, receipt := range s.trustedTransport.receipts {
			if receipt.peer == input.PeerID {
				delete(s.trustedTransport.receipts, token)
			}
		}
		s.trustedTransport.mu.Unlock()
		return s.writeResponse(req.ID, map[string]bool{"released": true}, nil)
	case "transport/dispatch", "transport/event":
	default:
		return deny(errors.New("unknown trusted transport method"))
	}
	if input.Method == "" || strings.HasPrefix(input.Method, "transport/") || input.Method == "shutdown" {
		return deny(errors.New("nested or lifecycle transport operation forbidden"))
	}
	identity, err := s.bindTrustedTransport(input)
	if err != nil {
		return deny(err)
	}
	ctx = principal.WithContext(ctx, identity)
	if req.Method == "transport/event" {
		notification := Notification{Method: input.Method, Params: input.Params}
		_, guard, allowed := s.authorizeOutbound(ctx, notification)
		if !allowed {
			return deny(errors.New("event access denied"))
		}
		receipt, err := s.retainTransportReceipt(input.PeerID, guard)
		if err != nil {
			return deny(err)
		}
		return s.writeResponse(req.ID, map[string]any{"version": 1, "receipt": receipt, "generation": s.transportGeneration()}, nil)
	}
	if len(req.ID) == 0 {
		return deny(errors.New("transport dispatch requires request id"))
	}
	s.trustedTransport.mu.Lock()
	if s.trustedTransport.pending == nil {
		s.trustedTransport.pending = make(map[string]string)
	}
	if _, ok := s.trustedTransport.pending[string(req.ID)]; ok {
		s.trustedTransport.mu.Unlock()
		return deny(errors.New("transport request already pending"))
	}
	if len(s.trustedTransport.pending) >= 4096 {
		s.trustedTransport.mu.Unlock()
		return deny(errors.New("too many delegated requests"))
	}
	s.trustedTransport.pending[string(req.ID)] = input.PeerID
	s.trustedTransport.mu.Unlock()
	nested, err := json.Marshal(Request{ID: req.ID, Method: input.Method, Params: input.Params})
	if err != nil {
		return deny(err)
	}
	err = s.handleLine(ctx, nested)
	if err != nil {
		s.trustedTransport.mu.Lock()
		delete(s.trustedTransport.pending, string(req.ID))
		s.trustedTransport.mu.Unlock()
	}
	return err
}

func (s *Server) bindTrustedTransport(input trustedTransportEnvelope) (principal.Verified, error) {
	host, _, err := net.SplitHostPort(input.Validation.Address)
	if err != nil || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() || len(input.Validation.Token) < 32 {
		return principal.Verified{}, errors.New("authenticated live-validation capability required")
	}
	s.trustedTransport.mu.Lock()
	defer s.trustedTransport.mu.Unlock()
	if s.trustedTransport.bindings == nil {
		s.trustedTransport.bindings = make(map[string]*trustedTransportBinding)
	}
	if binding := s.trustedTransport.bindings[input.PeerID]; binding != nil {
		original := binding.envelope
		if original.Principal != input.Principal || original.Owner != input.Owner || original.Validation != input.Validation {
			return principal.Verified{}, errors.New("transport peer provenance cannot change")
		}
		return binding.principal, nil
	}
	// Forgotten/dead transports cannot permanently exhaust the core. Expiring
	// the oldest binding revokes its in-flight/replay closures; never promote it.
	for len(s.trustedTransport.bindingOrder) >= 256 {
		oldest := s.trustedTransport.bindingOrder[0]
		s.trustedTransport.bindingOrder = s.trustedTransport.bindingOrder[1:]
		if old := s.trustedTransport.bindings[oldest]; old != nil {
			old.revoked.Store(true)
			delete(s.trustedTransport.bindings, oldest)
		}
	}
	binding := &trustedTransportBinding{envelope: trustedTransportEnvelope{Principal: input.Principal, Owner: input.Owner, Validation: input.Validation}}
	transport := &http.Transport{Proxy: nil, DisableKeepAlives: true}
	client := &http.Client{Timeout: 5 * time.Second, Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("validation redirects forbidden") }}
	valid := func() bool {
		if binding.revoked.Load() {
			return false
		}
		request, err := http.NewRequest(http.MethodPost, "http://"+input.Validation.Address+"/v1/validate", nil)
		if err != nil {
			return false
		}
		request.Header.Set("Authorization", "Bearer "+input.Validation.Token)
		response, err := client.Do(request)
		if err != nil {
			return false
		}
		defer response.Body.Close()
		return response.StatusCode == http.StatusNoContent && !binding.revoked.Load()
	}
	var identity principal.Verified
	if input.Owner {
		identity, err = principal.OwnerConnection(input.Principal, valid)
	} else {
		identity, err = principal.AuthenticatedWithValidity(input.Principal, valid)
	}
	if err != nil {
		return principal.Verified{}, err
	}
	binding.principal = identity
	s.trustedTransport.bindings[input.PeerID] = binding
	s.trustedTransport.bindingOrder = append(s.trustedTransport.bindingOrder, input.PeerID)
	return identity, nil
}

func (s *Server) retainTransportReceipt(peer string, guard func() error) (string, error) {
	var secret [24]byte
	if _, err := rand.Read(secret[:]); err != nil {
		return "", err
	}
	token := hex.EncodeToString(secret[:])
	s.trustedTransport.mu.Lock()
	defer s.trustedTransport.mu.Unlock()
	if s.trustedTransport.receipts == nil {
		s.trustedTransport.receipts = make(map[string]trustedTransportReceipt)
	}
	if s.trustedTransport.generation == "" {
		s.trustedTransport.generation = token
	}
	s.trustedTransport.receipts[token] = trustedTransportReceipt{peer: peer, guard: guard}
	s.trustedTransport.order = append(s.trustedTransport.order, token)
	// Exhaustion expires old receipts, never weakens their policy. Reconnect then
	// obtains fresh snapshots rather than replaying unverifiable cached content.
	for len(s.trustedTransport.order) > maxTransportReceipts {
		delete(s.trustedTransport.receipts, s.trustedTransport.order[0])
		s.trustedTransport.order = s.trustedTransport.order[1:]
	}
	return token, nil
}

func (s *Server) wrapTrustedTransportResponse(value any, guard func() error) (any, error) {
	response, ok := value.(Response)
	if !ok {
		return value, nil
	}
	s.trustedTransport.mu.Lock()
	peer, ok := s.trustedTransport.pending[string(response.ID)]
	delete(s.trustedTransport.pending, string(response.ID))
	s.trustedTransport.mu.Unlock()
	if !ok {
		return value, nil
	}
	token, err := s.retainTransportReceipt(peer, guard)
	if err != nil {
		return nil, err
	}
	return Response{ID: response.ID, Result: trustedTransportFrame{Generation: s.transportGeneration(), Version: 1, Message: response, Receipt: token}}, nil
}

func (s *Server) transportGeneration() string {
	s.trustedTransport.mu.Lock()
	defer s.trustedTransport.mu.Unlock()
	return s.trustedTransport.generation
}
