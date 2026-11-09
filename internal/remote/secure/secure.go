// Package secure implements the cryptographic core of wuu remote control:
// device identities, the QR-code pairing exchange, the per-connection
// authenticated key agreement, and the sealed channel that carries every
// application byte between a phone and a host.
//
// Trust model: the relay is untrusted. It sees only routing metadata (which
// device talks to which host, timings, sizes) and ciphertext. Authenticity
// roots:
//
//   - The pairing URI travels out-of-band (QR on the host screen). It carries
//     the host's long-term Ed25519 public key (so the phone pins the host from
//     the very first byte) and a fresh X25519 ephemeral public key that never
//     touches the relay. Possession of that ephemeral key is the proof that
//     the phone actually saw the host's screen; the relay cannot forge or
//     intercept a pairing without it.
//   - After pairing both sides hold each other's Ed25519 keys. Every
//     connection then runs a signed ephemeral Diffie-Hellman handshake
//     (SIGMA-style), yielding fresh AES-256-GCM keys per direction with
//     forward secrecy. Frame nonces are strictly monotonic counters, so
//     replayed or reordered ciphertext is rejected at the channel layer.
//
// Everything here is standard library only: ed25519, crypto/ecdh (X25519),
// crypto/hkdf, and AES-GCM.
package secure

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
)

const (
	// PairURIScheme is the deep-link scheme encoded into pairing QR codes.
	PairURIScheme = "wuu"

	labelRelayAuth   = "wuu/relay/auth/v1"
	labelPairConfirm = "wuu/pair/confirm/v1"
	labelHS1         = "wuu/hs1/v1"
	labelHS2         = "wuu/hs2/v1"

	infoPairOffer  = "wuu pair offer v1"
	infoPairAnswer = "wuu pair answer v1"
	infoSendPhone  = "wuu e2e phone->host v1"
	infoSendHost   = "wuu e2e host->phone v1"

	x25519KeyLen = 32
	gcmNonceLen  = 12
)

var b64 = base64.RawURLEncoding

// EncodeKey renders a raw public key in the canonical wire encoding
// (unpadded base64url) used across the relay protocol and stored files.
func EncodeKey(pub []byte) string { return b64.EncodeToString(pub) }

// DecodeKey reverses EncodeKey and enforces the 32-byte key length shared by
// Ed25519 and X25519 public keys.
func DecodeKey(s string) ([]byte, error) {
	raw, err := b64.DecodeString(strings.TrimSpace(s))
	if err != nil {
		return nil, fmt.Errorf("decode key: %w", err)
	}
	if len(raw) != 32 {
		return nil, fmt.Errorf("decode key: got %d bytes, want 32", len(raw))
	}
	return raw, nil
}

// Fingerprint returns a short human-readable identifier for a public key,
// used in logs and CLI output. It is display-only; routing always uses the
// full key.
func Fingerprint(pub []byte) string {
	sum := sha256.Sum256(pub)
	return hex.EncodeToString(sum[:6])
}

// Identity is a long-term Ed25519 device identity. Hosts and phones each hold
// exactly one; the public key doubles as the device address at the relay.
type Identity struct {
	priv ed25519.PrivateKey
}

func NewIdentity() (*Identity, error) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate identity: %w", err)
	}
	return &Identity{priv: priv}, nil
}

// IdentityFromSeed rebuilds an identity from the 32-byte seed produced by
// Seed. It is how identities are loaded from disk.
func IdentityFromSeed(seed []byte) (*Identity, error) {
	if len(seed) != ed25519.SeedSize {
		return nil, fmt.Errorf("identity seed must be %d bytes, got %d", ed25519.SeedSize, len(seed))
	}
	return &Identity{priv: ed25519.NewKeyFromSeed(seed)}, nil
}

func (id *Identity) Seed() []byte   { return id.priv.Seed() }
func (id *Identity) Public() []byte { return []byte(id.priv.Public().(ed25519.PublicKey)) }

// sign produces a domain-separated signature over the given parts. Each part
// is length-prefixed so distinct part boundaries can never collide.
func (id *Identity) sign(label string, parts ...[]byte) []byte {
	return ed25519.Sign(id.priv, signingBuffer(label, parts))
}

// verify checks a signature produced by sign under the same label and parts.
func verify(pub []byte, label string, sig []byte, parts ...[]byte) bool {
	if len(pub) != ed25519.PublicKeySize || len(sig) != ed25519.SignatureSize {
		return false
	}
	return ed25519.Verify(ed25519.PublicKey(pub), signingBuffer(label, parts), sig)
}

func signingBuffer(label string, parts [][]byte) []byte {
	buf := make([]byte, 0, 64)
	buf = append(buf, []byte(label)...)
	buf = append(buf, 0)
	var lp [4]byte
	for _, p := range parts {
		binary.BigEndian.PutUint32(lp[:], uint32(len(p)))
		buf = append(buf, lp[:]...)
		buf = append(buf, p...)
	}
	return buf
}

// SignRelayAuth signs a relay authentication challenge. The role is bound
// into the signature so a phone credential can never be replayed as a host
// (or vice versa).
func (id *Identity) SignRelayAuth(nonce []byte, role string) []byte {
	return id.sign(labelRelayAuth, nonce, id.Public(), []byte(role))
}

// VerifyRelayAuth checks a relay authentication signature.
func VerifyRelayAuth(pub, nonce, sig []byte, role string) bool {
	return verify(pub, labelRelayAuth, sig, nonce, pub, []byte(role))
}

// --- Pairing -----------------------------------------------------------------

// Pairing is the host-side state of one open pairing window: a fresh X25519
// ephemeral key plus a random pairing ID. The ephemeral public key is encoded
// only into the QR URI; the relay learns just the pairing ID.
type Pairing struct {
	ID   string
	priv *ecdh.PrivateKey
}

func NewPairing() (*Pairing, error) {
	priv, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate pairing key: %w", err)
	}
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return nil, err
	}
	return &Pairing{ID: b64.EncodeToString(id[:]), priv: priv}, nil
}

// URI renders the pairing deep link shown as a QR code:
//
//	wuu://pair?v=1&r=<relay ws url>&p=<pairing id>&k=<ephemeral X25519 pub>&h=<host Ed25519 pub>
func (p *Pairing) URI(relayURL string, hostPub []byte) string {
	q := url.Values{}
	q.Set("v", "1")
	q.Set("r", relayURL)
	q.Set("p", p.ID)
	q.Set("k", b64.EncodeToString(p.priv.PublicKey().Bytes()))
	q.Set("h", b64.EncodeToString(hostPub))
	return PairURIScheme + "://pair?" + q.Encode()
}

// PairLink is the parsed form of a pairing URI.
type PairLink struct {
	RelayURL  string
	PairingID string
	EphPub    []byte // host pairing ephemeral X25519 public key
	HostPub   []byte // host long-term Ed25519 public key (pinned)
}

func ParsePairURI(s string) (PairLink, error) {
	u, err := url.Parse(strings.TrimSpace(s))
	if err != nil {
		return PairLink{}, fmt.Errorf("parse pairing uri: %w", err)
	}
	if u.Scheme != PairURIScheme || u.Host != "pair" {
		return PairLink{}, fmt.Errorf("not a wuu pairing uri: %q", s)
	}
	q := u.Query()
	if q.Get("v") != "1" {
		return PairLink{}, fmt.Errorf("unsupported pairing uri version %q", q.Get("v"))
	}
	eph, err := DecodeKey(q.Get("k"))
	if err != nil {
		return PairLink{}, fmt.Errorf("pairing uri ephemeral key: %w", err)
	}
	host, err := DecodeKey(q.Get("h"))
	if err != nil {
		return PairLink{}, fmt.Errorf("pairing uri host key: %w", err)
	}
	link := PairLink{
		RelayURL:  q.Get("r"),
		PairingID: q.Get("p"),
		EphPub:    eph,
		HostPub:   host,
	}
	if link.RelayURL == "" || link.PairingID == "" {
		return PairLink{}, errors.New("pairing uri missing relay or pairing id")
	}
	return link, nil
}

// PairOffer is the phone's half of the pairing exchange (sent encrypted).
type PairOffer struct {
	DevicePub []byte `json:"device_pub"`
	Name      string `json:"name"`
	Platform  string `json:"platform,omitempty"`
}

// PairAnswer is the host's confirmation (sent encrypted). Sig binds the
// phone's device key into the exchange under the host's long-term identity.
type PairAnswer struct {
	HostPub  []byte `json:"host_pub"`
	HostName string `json:"host_name,omitempty"`
	Sig      []byte `json:"sig"`
}

type pairOfferWire struct {
	DevicePub string `json:"device_pub"`
	Name      string `json:"name"`
	Platform  string `json:"platform,omitempty"`
}

type pairAnswerWire struct {
	HostPub  string `json:"host_pub"`
	HostName string `json:"host_name,omitempty"`
	Sig      string `json:"sig"`
}

// PhonePairing is the phone-side state between sealing the offer and opening
// the answer.
type PhonePairing struct {
	link       PairLink
	ephPub     []byte
	shared     []byte
	transcript [32]byte
}

// SealPairOffer builds the encrypted pairing offer for the host described by
// link. Only a party that saw the QR (and therefore link.EphPub) can produce
// or read pairing traffic: the offer key is derived from ECDH with that
// ephemeral key, which the relay never learns.
func SealPairOffer(link PairLink, offer PairOffer) ([]byte, *PhonePairing, error) {
	ephPriv, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	nonce, err := randomNonce()
	if err != nil {
		return nil, nil, err
	}
	return sealPairOffer(link, offer, ephPriv, nonce)
}

// sealPairOffer is the deterministic core of SealPairOffer: the caller
// supplies the ephemeral key and AEAD nonce, so conformance tests can pin
// every byte of the exchange.
func sealPairOffer(link PairLink, offer PairOffer, ephPriv *ecdh.PrivateKey, nonce []byte) ([]byte, *PhonePairing, error) {
	hostEph, err := ecdh.X25519().NewPublicKey(link.EphPub)
	if err != nil {
		return nil, nil, fmt.Errorf("host pairing key: %w", err)
	}
	shared, err := ephPriv.ECDH(hostEph)
	if err != nil {
		return nil, nil, fmt.Errorf("pairing ecdh: %w", err)
	}
	pp := &PhonePairing{
		link:   link,
		ephPub: ephPriv.PublicKey().Bytes(),
		shared: shared,
	}
	pp.transcript = pairTranscript(link.EphPub, pp.ephPub, link.PairingID)

	plain, err := json.Marshal(pairOfferWire{
		DevicePub: b64.EncodeToString(offer.DevicePub),
		Name:      offer.Name,
		Platform:  offer.Platform,
	})
	if err != nil {
		return nil, nil, err
	}
	sealed, err := aeadSealWithNonce(pp.shared, pp.transcript, infoPairOffer, nonce, plain)
	if err != nil {
		return nil, nil, err
	}
	payload := make([]byte, 0, x25519KeyLen+len(sealed))
	payload = append(payload, pp.ephPub...)
	payload = append(payload, sealed...)
	return payload, pp, nil
}

// OpenPairOffer decrypts a pairing offer on the host side.
func (p *Pairing) OpenPairOffer(payload []byte) (PairOffer, *HostPairing, error) {
	if len(payload) < x25519KeyLen+gcmNonceLen {
		return PairOffer{}, nil, errors.New("pairing offer too short")
	}
	phoneEphBytes := payload[:x25519KeyLen]
	phoneEph, err := ecdh.X25519().NewPublicKey(phoneEphBytes)
	if err != nil {
		return PairOffer{}, nil, fmt.Errorf("phone pairing key: %w", err)
	}
	shared, err := p.priv.ECDH(phoneEph)
	if err != nil {
		return PairOffer{}, nil, fmt.Errorf("pairing ecdh: %w", err)
	}
	transcript := pairTranscript(p.priv.PublicKey().Bytes(), phoneEphBytes, p.ID)
	plain, err := aeadOpen(shared, transcript, infoPairOffer, payload[x25519KeyLen:])
	if err != nil {
		return PairOffer{}, nil, fmt.Errorf("open pairing offer: %w", err)
	}
	var w pairOfferWire
	if err := json.Unmarshal(plain, &w); err != nil {
		return PairOffer{}, nil, fmt.Errorf("decode pairing offer: %w", err)
	}
	devPub, err := DecodeKey(w.DevicePub)
	if err != nil {
		return PairOffer{}, nil, fmt.Errorf("pairing offer device key: %w", err)
	}
	offer := PairOffer{DevicePub: devPub, Name: w.Name, Platform: w.Platform}
	hp := &HostPairing{shared: shared, transcript: transcript}
	return offer, hp, nil
}

// HostPairing is the host-side state between opening an offer and sealing the
// answer.
type HostPairing struct {
	shared     []byte
	transcript [32]byte
}

// SealPairAnswer builds the host's encrypted confirmation for a device it has
// just registered.
func (hp *HostPairing) SealPairAnswer(host *Identity, hostName string, devicePub []byte) ([]byte, error) {
	nonce, err := randomNonce()
	if err != nil {
		return nil, err
	}
	return hp.sealPairAnswer(host, hostName, devicePub, nonce)
}

// sealPairAnswer is the deterministic core of SealPairAnswer.
func (hp *HostPairing) sealPairAnswer(host *Identity, hostName string, devicePub, nonce []byte) ([]byte, error) {
	sig := host.sign(labelPairConfirm, hp.transcript[:], devicePub)
	plain, err := json.Marshal(pairAnswerWire{
		HostPub:  b64.EncodeToString(host.Public()),
		HostName: hostName,
		Sig:      b64.EncodeToString(sig),
	})
	if err != nil {
		return nil, err
	}
	return aeadSealWithNonce(hp.shared, hp.transcript, infoPairAnswer, nonce, plain)
}

// OpenPairAnswer decrypts and verifies the host's confirmation on the phone.
// It enforces that the confirming identity matches the key pinned from the QR
// and that the signature covers this phone's device key.
func (pp *PhonePairing) OpenPairAnswer(payload, devicePub []byte) (PairAnswer, error) {
	plain, err := aeadOpen(pp.shared, pp.transcript, infoPairAnswer, payload)
	if err != nil {
		return PairAnswer{}, fmt.Errorf("open pairing answer: %w", err)
	}
	var w pairAnswerWire
	if err := json.Unmarshal(plain, &w); err != nil {
		return PairAnswer{}, fmt.Errorf("decode pairing answer: %w", err)
	}
	hostPub, err := DecodeKey(w.HostPub)
	if err != nil {
		return PairAnswer{}, fmt.Errorf("pairing answer host key: %w", err)
	}
	sig, err := b64.DecodeString(w.Sig)
	if err != nil {
		return PairAnswer{}, fmt.Errorf("pairing answer sig: %w", err)
	}
	if !bytesEqual(hostPub, pp.link.HostPub) {
		return PairAnswer{}, errors.New("pairing answer host key does not match QR pin")
	}
	if !verify(hostPub, labelPairConfirm, sig, pp.transcript[:], devicePub) {
		return PairAnswer{}, errors.New("pairing answer signature invalid")
	}
	return PairAnswer{HostPub: hostPub, HostName: w.HostName, Sig: sig}, nil
}

func pairTranscript(hostEph, phoneEph []byte, pairingID string) [32]byte {
	h := sha256.New()
	h.Write([]byte("wuu/pair/v1"))
	h.Write(hostEph)
	h.Write(phoneEph)
	h.Write([]byte(pairingID))
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out
}

// --- Connection handshake ------------------------------------------------------

// HS1 is the phone's handshake opener, sent in the clear through the relay
// (it contains only public values and a signature).
type HS1 struct {
	DevicePub string `json:"device_pub"`
	Eph       string `json:"eph"`
	Nonce     string `json:"nonce"`
	Sig       string `json:"sig"`
}

// HS2 is the host's handshake reply.
type HS2 struct {
	Eph   string `json:"eph"`
	Nonce string `json:"nonce"`
	Sig   string `json:"sig"`
}

// Handshake is the phone-side in-flight handshake state.
type Handshake struct {
	phone   *Identity
	hostPub []byte
	ephPriv *ecdh.PrivateKey
	nonce   []byte
}

// NewHandshake starts a connection handshake from the phone toward its paired
// host. The returned HS1 travels through the relay as plaintext; its
// signature proves the phone's paired identity and binds the fresh ephemeral
// key.
func NewHandshake(phone *Identity, hostPub []byte) (*Handshake, HS1, error) {
	ephPriv, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return nil, HS1{}, err
	}
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		return nil, HS1{}, err
	}
	return newHandshake(phone, hostPub, ephPriv, nonce)
}

// newHandshake is the deterministic core of NewHandshake.
func newHandshake(phone *Identity, hostPub []byte, ephPriv *ecdh.PrivateKey, nonce []byte) (*Handshake, HS1, error) {
	ephPub := ephPriv.PublicKey().Bytes()
	sig := phone.sign(labelHS1, hostPub, phone.Public(), ephPub, nonce)
	hs1 := HS1{
		DevicePub: b64.EncodeToString(phone.Public()),
		Eph:       b64.EncodeToString(ephPub),
		Nonce:     b64.EncodeToString(nonce),
		Sig:       b64.EncodeToString(sig),
	}
	return &Handshake{phone: phone, hostPub: hostPub, ephPriv: ephPriv, nonce: nonce}, hs1, nil
}

// AcceptHandshake runs the host side of the handshake. isPaired gates which
// device keys are allowed; the returned channel is ready as soon as HS2 has
// been delivered to the phone.
func AcceptHandshake(host *Identity, hs1 HS1, isPaired func(devicePub []byte) bool) (*Channel, HS2, error) {
	ephPriv, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return nil, HS2{}, err
	}
	hostNonce := make([]byte, 16)
	if _, err := rand.Read(hostNonce); err != nil {
		return nil, HS2{}, err
	}
	return acceptHandshake(host, hs1, isPaired, ephPriv, hostNonce)
}

// acceptHandshake is the deterministic core of AcceptHandshake.
func acceptHandshake(host *Identity, hs1 HS1, isPaired func(devicePub []byte) bool, ephPriv *ecdh.PrivateKey, hostNonce []byte) (*Channel, HS2, error) {
	devPub, err := DecodeKey(hs1.DevicePub)
	if err != nil {
		return nil, HS2{}, fmt.Errorf("hs1 device key: %w", err)
	}
	phoneEphBytes, err := DecodeKey(hs1.Eph)
	if err != nil {
		return nil, HS2{}, fmt.Errorf("hs1 ephemeral key: %w", err)
	}
	phoneNonce, err := b64.DecodeString(hs1.Nonce)
	if err != nil {
		return nil, HS2{}, fmt.Errorf("hs1 nonce: %w", err)
	}
	sig, err := b64.DecodeString(hs1.Sig)
	if err != nil {
		return nil, HS2{}, fmt.Errorf("hs1 sig: %w", err)
	}
	if isPaired == nil || !isPaired(devPub) {
		return nil, HS2{}, errors.New("handshake from unpaired device")
	}
	if !verify(devPub, labelHS1, sig, host.Public(), devPub, phoneEphBytes, phoneNonce) {
		return nil, HS2{}, errors.New("hs1 signature invalid")
	}

	phoneEph, err := ecdh.X25519().NewPublicKey(phoneEphBytes)
	if err != nil {
		return nil, HS2{}, fmt.Errorf("hs1 ephemeral key: %w", err)
	}
	shared, err := ephPriv.ECDH(phoneEph)
	if err != nil {
		return nil, HS2{}, fmt.Errorf("handshake ecdh: %w", err)
	}
	hostEphPub := ephPriv.PublicKey().Bytes()
	sig2 := host.sign(labelHS2, host.Public(), devPub, phoneEphBytes, phoneNonce, hostEphPub, hostNonce)
	hs2 := HS2{
		Eph:   b64.EncodeToString(hostEphPub),
		Nonce: b64.EncodeToString(hostNonce),
		Sig:   b64.EncodeToString(sig2),
	}

	transcript := sessionTranscript(host.Public(), devPub, phoneEphBytes, phoneNonce, hostEphPub, hostNonce)
	ch, err := newChannel(shared, transcript, devPub, false)
	if err != nil {
		return nil, HS2{}, err
	}
	return ch, hs2, nil
}

// Complete finishes the handshake on the phone after receiving HS2.
func (h *Handshake) Complete(hs2 HS2) (*Channel, error) {
	hostEphBytes, err := DecodeKey(hs2.Eph)
	if err != nil {
		return nil, fmt.Errorf("hs2 ephemeral key: %w", err)
	}
	hostNonce, err := b64.DecodeString(hs2.Nonce)
	if err != nil {
		return nil, fmt.Errorf("hs2 nonce: %w", err)
	}
	sig, err := b64.DecodeString(hs2.Sig)
	if err != nil {
		return nil, fmt.Errorf("hs2 sig: %w", err)
	}
	phoneEphPub := h.ephPriv.PublicKey().Bytes()
	if !verify(h.hostPub, labelHS2, sig, h.hostPub, h.phone.Public(), phoneEphPub, h.nonce, hostEphBytes, hostNonce) {
		return nil, errors.New("hs2 signature invalid")
	}
	hostEph, err := ecdh.X25519().NewPublicKey(hostEphBytes)
	if err != nil {
		return nil, fmt.Errorf("hs2 ephemeral key: %w", err)
	}
	shared, err := h.ephPriv.ECDH(hostEph)
	if err != nil {
		return nil, fmt.Errorf("handshake ecdh: %w", err)
	}
	transcript := sessionTranscript(h.hostPub, h.phone.Public(), phoneEphPub, h.nonce, hostEphBytes, hostNonce)
	return newChannel(shared, transcript, h.hostPub, true)
}

func sessionTranscript(hostPub, devPub, phoneEph, phoneNonce, hostEph, hostNonce []byte) [32]byte {
	h := sha256.New()
	h.Write([]byte("wuu/session/v1"))
	for _, p := range [][]byte{hostPub, devPub, phoneEph, phoneNonce, hostEph, hostNonce} {
		var lp [4]byte
		binary.BigEndian.PutUint32(lp[:], uint32(len(p)))
		h.Write(lp[:])
		h.Write(p)
	}
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out
}

// --- Sealed channel ------------------------------------------------------------

// Channel is one established end-to-end session between a phone and a host.
// Each direction has its own AES-256-GCM key and a strictly monotonic counter
// nonce; Open rejects any frame whose counter does not advance, which both
// deduplicates redelivered frames and blocks replay.
type Channel struct {
	send        cipher.AEAD
	recv        cipher.AEAD
	sendCounter uint64
	recvLast    uint64
	transcript  [32]byte
	peer        []byte
}

func newChannel(shared []byte, transcript [32]byte, peer []byte, phoneSide bool) (*Channel, error) {
	phoneKey, err := hkdf.Key(sha256.New, shared, transcript[:], infoSendPhone, 32)
	if err != nil {
		return nil, err
	}
	hostKey, err := hkdf.Key(sha256.New, shared, transcript[:], infoSendHost, 32)
	if err != nil {
		return nil, err
	}
	phoneAEAD, err := newAEAD(phoneKey)
	if err != nil {
		return nil, err
	}
	hostAEAD, err := newAEAD(hostKey)
	if err != nil {
		return nil, err
	}
	ch := &Channel{transcript: transcript, peer: append([]byte(nil), peer...)}
	if phoneSide {
		ch.send, ch.recv = phoneAEAD, hostAEAD
	} else {
		ch.send, ch.recv = hostAEAD, phoneAEAD
	}
	return ch, nil
}

// Peer returns the remote party's long-term public key.
func (c *Channel) Peer() []byte { return c.peer }

// Seal encrypts one frame. The returned buffer is nonce || ciphertext.
func (c *Channel) Seal(plaintext []byte) []byte {
	c.sendCounter++
	nonce := make([]byte, gcmNonceLen)
	binary.BigEndian.PutUint64(nonce[4:], c.sendCounter)
	out := c.send.Seal(nonce, nonce, plaintext, c.transcript[:])
	return out
}

// Open decrypts one frame and enforces counter monotonicity.
func (c *Channel) Open(frame []byte) ([]byte, error) {
	if len(frame) < gcmNonceLen {
		return nil, errors.New("sealed frame too short")
	}
	nonce := frame[:gcmNonceLen]
	counter := binary.BigEndian.Uint64(nonce[4:])
	if counter <= c.recvLast {
		return nil, fmt.Errorf("sealed frame counter %d replayed (last %d)", counter, c.recvLast)
	}
	plain, err := c.recv.Open(nil, nonce, frame[gcmNonceLen:], c.transcript[:])
	if err != nil {
		return nil, fmt.Errorf("open sealed frame: %w", err)
	}
	c.recvLast = counter
	return plain, nil
}

// --- shared AEAD helpers ---------------------------------------------------

func newAEAD(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// aeadSealWithNonce derives a one-shot key via HKDF and seals plaintext under
// the caller's nonce (nonce || ciphertext). Used by the pairing exchange,
// where each key encrypts exactly one message; production callers pass a
// fresh randomNonce, conformance tests pin the nonce explicitly.
func aeadSealWithNonce(shared []byte, transcript [32]byte, info string, nonce, plaintext []byte) ([]byte, error) {
	key, err := hkdf.Key(sha256.New, shared, transcript[:], info, 32)
	if err != nil {
		return nil, err
	}
	aead, err := newAEAD(key)
	if err != nil {
		return nil, err
	}
	return aead.Seal(append([]byte(nil), nonce...), nonce, plaintext, transcript[:]), nil
}

func randomNonce() ([]byte, error) {
	nonce := make([]byte, gcmNonceLen)
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return nonce, nil
}

func aeadOpen(shared []byte, transcript [32]byte, info string, payload []byte) ([]byte, error) {
	if len(payload) < gcmNonceLen {
		return nil, errors.New("sealed payload too short")
	}
	key, err := hkdf.Key(sha256.New, shared, transcript[:], info, 32)
	if err != nil {
		return nil, err
	}
	aead, err := newAEAD(key)
	if err != nil {
		return nil, err
	}
	return aead.Open(nil, payload[:gcmNonceLen], payload[gcmNonceLen:], transcript[:])
}

func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	var diff byte
	for i := range a {
		diff |= a[i] ^ b[i]
	}
	return diff == 0
}
