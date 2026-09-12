// Package wire defines the two message vocabularies of wuu remote control:
//
//   - The relay leg: JSON text frames exchanged between a device (host or
//     phone) and the relay over a websocket. The relay reads these; they carry
//     routing metadata and opaque payloads only, never plaintext application
//     content.
//   - The end-to-end leg: JSON messages exchanged between a phone and a host
//     inside relay frame payloads. Apart from the two handshake messages
//     (public values + signatures), every end-to-end message travels sealed by
//     secure.Channel, so the relay cannot read or forge it.
//
// A relay frame payload is a byte slice with a one-byte kind prefix:
// KindHandshake carries plaintext handshake JSON, KindSealed carries
// secure.Channel ciphertext.
package wire

import (
	"encoding/json"
	"fmt"
)

// ProtoVersion is the relay protocol version negotiated in the hello message.
const ProtoVersion = 1

// Relay message types (RelayMsg.Type).
const (
	TypeHello      = "hello"
	TypeChallenge  = "challenge"
	TypeAuth       = "auth"
	TypeAuthOK     = "auth_ok"
	TypeAuthErr    = "auth_err"
	TypePairOpen   = "pair_open"
	TypePairClose  = "pair_close"
	TypePairOffer  = "pair_offer"
	TypePairAnswer = "pair_answer"
	TypePairErr    = "pair_err"
	TypeDeviceAdd  = "device_add"
	TypeDeviceRm   = "device_remove"
	TypeDeviceList = "device_list"
	TypeDevices    = "devices"
	TypeFrame      = "frame"
	TypeDeliverErr = "deliver_err"
	TypePresence   = "presence"
	TypePush       = "push"
	TypeErr        = "err"
	TypeOK         = "ok"
)

// Device roles (RelayMsg.Role).
const (
	RoleHost  = "host"
	RolePhone = "phone"
)

// Relay error codes (RelayMsg.Code).
const (
	CodeBadRequest    = "bad_request"
	CodeBadSignature  = "bad_signature"
	CodeUnknownDevice = "unknown_device"
	CodeUnauthorized  = "unauthorized"
	CodeOffline       = "offline"
	CodeNoSuchPairing = "no_such_pairing"
	CodeSuperseded    = "superseded"
)

// Push hints (RelayMsg.Hint). Push notifications are deliberately
// content-free: the hint names a class of event, never what happened inside.
const (
	PushNeedsInput = "needs_input"
	PushAgentDone  = "agent_done"
)

// RelayDevice is one enrolled device in a device listing.
type RelayDevice struct {
	Pub     string `json:"pub"`
	Name    string `json:"name,omitempty"`
	AddedAt string `json:"added_at,omitempty"`
	Online  bool   `json:"online,omitempty"`
}

// RelayMsg is the single envelope for every relay-leg message. Type selects
// which of the optional fields are meaningful.
type RelayMsg struct {
	Type string `json:"type"`

	// hello / auth
	Proto int    `json:"proto,omitempty"`
	Role  string `json:"role,omitempty"`
	Pub   string `json:"pub,omitempty"`   // device public key, base64url
	Nonce string `json:"nonce,omitempty"` // auth challenge nonce, base64url
	Sig   string `json:"sig,omitempty"`   // auth signature, base64url

	// pairing
	PairingID string `json:"pairing_id,omitempty"`

	// device management
	Name    string        `json:"name,omitempty"`
	Devices []RelayDevice `json:"devices,omitempty"`

	// data frames: To/From are device public keys, Payload is the opaque
	// end-to-end payload (kind prefix + bytes), base64url.
	To      string `json:"to,omitempty"`
	From    string `json:"from,omitempty"`
	Payload string `json:"payload,omitempty"`

	// presence
	Online *bool `json:"online,omitempty"`

	// push
	Hint string `json:"hint,omitempty"`
	// ThreadID deep-links a turn-bound push hint to its thread; empty for
	// process-wide hints (needs_input).
	ThreadID string `json:"thread_id,omitempty"`

	// errors
	Code string `json:"code,omitempty"`
	Msg  string `json:"msg,omitempty"`
}

// Payload kind prefixes for relay frame payloads.
const (
	KindHandshake byte = 0x01
	KindSealed    byte = 0x02
)

// WrapPayload prefixes body with a payload kind byte.
func WrapPayload(kind byte, body []byte) []byte {
	out := make([]byte, 0, len(body)+1)
	out = append(out, kind)
	out = append(out, body...)
	return out
}

// SplitPayload separates the kind prefix from the payload body.
func SplitPayload(payload []byte) (byte, []byte, error) {
	if len(payload) < 1 {
		return 0, nil, fmt.Errorf("empty frame payload")
	}
	return payload[0], payload[1:], nil
}

// --- End-to-end messages -----------------------------------------------------

// E2E message types (E2EMsg.T).
const (
	E2EHS1      = "hs1"
	E2EHS2      = "hs2"
	E2EAttach   = "attach"
	E2EAttached = "attached"
	E2ERPC      = "rpc"
	E2EAck      = "ack"
	E2EState    = "state"
	E2EPing     = "ping"
	E2EPong     = "pong"
	E2EBye      = "bye"
)

// Client profiles tune the app-server event stream for a controller.
const (
	ClientProfileMobileChat = "mobile_chat"
	// MobileActivity adds bounded tool start/completion items, without output deltas.
	ClientProfileMobileActivity = "mobile_activity"
)

// HostInfo describes the host to an attached phone.
type HostInfo struct {
	Name     string `json:"name,omitempty"`
	Version  string `json:"version,omitempty"`
	Workdir  string `json:"workdir,omitempty"`
	Provider string `json:"provider,omitempty"`
	Model    string `json:"model,omitempty"`
}

// RunningTurn is one live turn in the host state snapshot.
type RunningTurn struct {
	ThreadID  string `json:"thread_id"`
	TurnID    string `json:"turn_id,omitempty"`
	StartedAt string `json:"started_at,omitempty"`
}

// E2EMsg is the envelope for every end-to-end message. T selects the
// meaningful fields.
//
// Reliability model: every host-to-phone rpc line carries a monotonically
// increasing Seq. The host keeps unacknowledged lines in a spool; the phone
// acknowledges with ack{Recv}. After a transport drop the phone re-handshakes
// and sends attach{Prev, Recv}; when the host still holds that session's
// spool it replays everything after Recv (attached{Resumed:true}), otherwise
// the phone gets a fresh app-server connection (attached{Resumed:false}) and
// refetches state over RPC. Phone-to-host rpc lines are deliberately
// unnumbered: requests are at-most-once, callers see missing responses fail.
type E2EMsg struct {
	T string `json:"t"`

	// hs1/hs2: handshake bodies (plaintext leg, signed).
	DevicePub string `json:"device_pub,omitempty"`
	Eph       string `json:"eph,omitempty"`
	Nonce     string `json:"nonce,omitempty"`
	Sig       string `json:"sig,omitempty"`

	// attach / attached
	Prev          string `json:"prev,omitempty"` // previous session id the phone wants to resume
	Recv          uint64 `json:"recv,omitempty"` // highest host seq the phone has applied
	ClientProfile string `json:"client_profile,omitempty"`
	// AcceptLineCompression advertises support for independently compressed
	// host-to-controller RPC lines. Missing or unknown values keep plain JSON.
	AcceptLineCompression string `json:"accept_line_compression,omitempty"`
	Session               string `json:"session,omitempty"` // current session id (attached)
	Resumed               bool   `json:"resumed,omitempty"`
	ReplayFrom            uint64 `json:"replay_from,omitempty"`

	// rpc
	Seq  uint64          `json:"seq,omitempty"`
	Line json.RawMessage `json:"line,omitempty"`
	// LineGzip is raw base64url gzip of Line, inside the authenticated encrypted
	// envelope. At most one representation is present; decoded lines are <=32MiB.
	LineGzip string `json:"line_gzip,omitempty"`

	// state
	Ver     uint64        `json:"ver,omitempty"`
	Host    *HostInfo     `json:"host,omitempty"`
	Running []RunningTurn `json:"running,omitempty"`

	// bye
	Reason string `json:"reason,omitempty"`
}

// EncodeE2E marshals an end-to-end message.
func EncodeE2E(m E2EMsg) ([]byte, error) { return json.Marshal(m) }

// DecodeE2E unmarshals an end-to-end message.
func DecodeE2E(data []byte) (E2EMsg, error) {
	var m E2EMsg
	if err := json.Unmarshal(data, &m); err != nil {
		return E2EMsg{}, fmt.Errorf("decode e2e message: %w", err)
	}
	if m.T == "" {
		return E2EMsg{}, fmt.Errorf("e2e message missing type")
	}
	return m, nil
}
