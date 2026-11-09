package secure

// Conformance vectors for non-Go implementations of this package.
//
// testdata/vectors.json pins every byte of the pairing exchange, the
// connection handshake, and the sealed channel for a fixed set of inputs. A
// port must reproduce each output byte-for-byte from the recorded inputs
// before it can be trusted to interoperate. The Go implementation is the
// reference: regenerate with
//
//	go test ./internal/remote/secure -run TestVectors -update
//
// and update every port whenever the file changes. Behavioral requirements
// that vectors cannot capture (counter replay rejection, signature
// substitution, key pinning) live in secure_test.go and must be mirrored as
// behavioral tests in each port.

import (
	"bytes"
	"crypto/ecdh"
	"crypto/hkdf"
	"crypto/sha256"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

var updateVectors = flag.Bool("update", false, "rewrite testdata/vectors.json from the reference implementation")

type vectorFile struct {
	Format     vectorFormat      `json:"format"`
	Identities vectorIdentities  `json:"identities"`
	RelayAuth  []vectorRelayAuth `json:"relay_auth"`
	Pairing    vectorPairing     `json:"pairing"`
	Handshake  vectorHandshake   `json:"handshake"`
	Channel    vectorChannel     `json:"channel"`
}

type vectorFormat struct {
	Version      int    `json:"version"`
	Encoding     string `json:"encoding"`
	SigningBuf   string `json:"signing_buffer"`
	SealedLayout string `json:"sealed_layout"`
	ChannelNonce string `json:"channel_nonce"`
	HKDF         string `json:"hkdf"`
	Generator    string `json:"generator"`
}

type vectorIdentities struct {
	Host  vectorIdentity `json:"host"`
	Phone vectorIdentity `json:"phone"`
}

type vectorIdentity struct {
	Seed        string `json:"seed"`
	Public      string `json:"public"`
	Fingerprint string `json:"fingerprint"`
}

type vectorRelayAuth struct {
	Signer        string `json:"signer"`
	Role          string `json:"role"`
	Nonce         string `json:"nonce"`
	SigningBuffer string `json:"signing_buffer"`
	Sig           string `json:"sig"`
}

type vectorPairing struct {
	RelayURL             string `json:"relay_url"`
	PairingID            string `json:"pairing_id"`
	HostEphPriv          string `json:"host_eph_priv"`
	HostEphPub           string `json:"host_eph_pub"`
	URI                  string `json:"uri"`
	PhoneEphPriv         string `json:"phone_eph_priv"`
	PhoneEphPub          string `json:"phone_eph_pub"`
	Shared               string `json:"shared"`
	Transcript           string `json:"transcript"`
	OfferName            string `json:"offer_name"`
	OfferPlatform        string `json:"offer_platform"`
	OfferNonce           string `json:"offer_nonce"`
	OfferPlaintext       string `json:"offer_plaintext"`
	OfferKey             string `json:"offer_key"`
	SealedOffer          string `json:"sealed_offer"`
	HostName             string `json:"host_name"`
	ConfirmSigningBuffer string `json:"confirm_signing_buffer"`
	ConfirmSig           string `json:"confirm_sig"`
	AnswerNonce          string `json:"answer_nonce"`
	AnswerPlaintext      string `json:"answer_plaintext"`
	AnswerKey            string `json:"answer_key"`
	SealedAnswer         string `json:"sealed_answer"`
}

type vectorHandshake struct {
	PhoneEphPriv     string `json:"phone_eph_priv"`
	PhoneEphPub      string `json:"phone_eph_pub"`
	PhoneNonce       string `json:"phone_nonce"`
	HS1SigningBuffer string `json:"hs1_signing_buffer"`
	HS1              HS1    `json:"hs1"`
	HostEphPriv      string `json:"host_eph_priv"`
	HostEphPub       string `json:"host_eph_pub"`
	HostNonce        string `json:"host_nonce"`
	HS2SigningBuffer string `json:"hs2_signing_buffer"`
	HS2              HS2    `json:"hs2"`
	Shared           string `json:"shared"`
	Transcript       string `json:"transcript"`
	KeyPhoneToHost   string `json:"key_phone_to_host"`
	KeyHostToPhone   string `json:"key_host_to_phone"`
}

type vectorChannel struct {
	Note        string        `json:"note"`
	PhoneToHost []vectorFrame `json:"phone_to_host"`
	HostToPhone []vectorFrame `json:"host_to_phone"`
}

type vectorFrame struct {
	Counter   uint64 `json:"counter"`
	Plaintext string `json:"plaintext"`
	Sealed    string `json:"sealed"`
}

// vecBytes derives deterministic pseudo-random input material from a label so
// the vector inputs are reproducible from source alone; the JSON file still
// records every byte explicitly.
func vecBytes(label string, n int) []byte {
	out := make([]byte, 0, n+sha256.Size)
	for i := 0; len(out) < n; i++ {
		sum := sha256.Sum256(fmt.Appendf(nil, "wuu-vectors/v1/%s/%d", label, i))
		out = append(out, sum[:]...)
	}
	return out[:n]
}

func vecIdentity(t *testing.T, label string) *Identity {
	t.Helper()
	id, err := IdentityFromSeed(vecBytes(label, 32))
	if err != nil {
		t.Fatalf("IdentityFromSeed(%s): %v", label, err)
	}
	return id
}

func vecX25519(t *testing.T, label string) *ecdh.PrivateKey {
	t.Helper()
	priv, err := ecdh.X25519().NewPrivateKey(vecBytes(label, 32))
	if err != nil {
		t.Fatalf("NewPrivateKey(%s): %v", label, err)
	}
	return priv
}

func vecHKDF(t *testing.T, shared []byte, transcript [32]byte, info string) []byte {
	t.Helper()
	key, err := hkdf.Key(sha256.New, shared, transcript[:], info, 32)
	if err != nil {
		t.Fatalf("hkdf(%s): %v", info, err)
	}
	return key
}

// buildVectors computes the canonical vector set. Every flow is also
// round-tripped through the opposing side, so a passing build proves the
// recorded bytes describe a working exchange, not just internally consistent
// output.
func buildVectors(t *testing.T) vectorFile {
	t.Helper()
	host := vecIdentity(t, "identity/host")
	phone := vecIdentity(t, "identity/phone")

	vf := vectorFile{
		Format: vectorFormat{
			Version:      1,
			Encoding:     "all byte fields are unpadded base64url (RFC 4648 §5)",
			SigningBuf:   "label || 0x00 || (len(part) as uint32 BE || part)... ; signed with Ed25519",
			SealedLayout: "nonce(12) || AES-256-GCM ciphertext+tag, aad = transcript",
			ChannelNonce: "12 bytes: 4 zero bytes || uint64 BE counter; counter starts at 1 per direction",
			HKDF:         "HKDF-SHA256(secret=shared, salt=transcript, info=<info string>) -> 32-byte key",
			Generator:    "go test ./internal/remote/secure -run TestVectors -update",
		},
		Identities: vectorIdentities{
			Host: vectorIdentity{
				Seed:        b64.EncodeToString(host.Seed()),
				Public:      EncodeKey(host.Public()),
				Fingerprint: Fingerprint(host.Public()),
			},
			Phone: vectorIdentity{
				Seed:        b64.EncodeToString(phone.Seed()),
				Public:      EncodeKey(phone.Public()),
				Fingerprint: Fingerprint(phone.Public()),
			},
		},
	}

	// Relay authentication: challenge-response signatures for both roles.
	for _, ra := range []struct {
		signer string
		id     *Identity
		role   string
	}{
		{"phone", phone, "phone"},
		{"host", host, "host"},
	} {
		nonce := vecBytes("relay-auth/"+ra.signer, 32)
		sig := ra.id.SignRelayAuth(nonce, ra.role)
		if !VerifyRelayAuth(ra.id.Public(), nonce, sig, ra.role) {
			t.Fatalf("relay auth vector for %s does not verify", ra.signer)
		}
		vf.RelayAuth = append(vf.RelayAuth, vectorRelayAuth{
			Signer:        ra.signer,
			Role:          ra.role,
			Nonce:         b64.EncodeToString(nonce),
			SigningBuffer: b64.EncodeToString(signingBuffer(labelRelayAuth, [][]byte{nonce, ra.id.Public(), []byte(ra.role)})),
			Sig:           b64.EncodeToString(sig),
		})
	}

	// Pairing: QR URI -> sealed offer -> sealed answer, with the host-side
	// ephemeral, phone-side ephemeral, and both AEAD nonces pinned.
	const relayURL = "ws://127.0.0.1:8787/v1/connect"
	pairingID := b64.EncodeToString(vecBytes("pairing/id", 16))
	hostEph := vecX25519(t, "pairing/host-eph")
	pairing := &Pairing{ID: pairingID, priv: hostEph}
	uri := pairing.URI(relayURL, host.Public())

	link, err := ParsePairURI(uri)
	if err != nil {
		t.Fatalf("ParsePairURI: %v", err)
	}
	if link.RelayURL != relayURL || link.PairingID != pairingID {
		t.Fatalf("pairing uri round trip mismatch: %+v", link)
	}

	phoneEph := vecX25519(t, "pairing/phone-eph")
	offerNonce := vecBytes("pairing/offer-nonce", gcmNonceLen)
	offer := PairOffer{DevicePub: phone.Public(), Name: "vector phone", Platform: "android"}
	sealedOffer, pp, err := sealPairOffer(link, offer, phoneEph, offerNonce)
	if err != nil {
		t.Fatalf("sealPairOffer: %v", err)
	}

	gotOffer, hp, err := pairing.OpenPairOffer(sealedOffer)
	if err != nil {
		t.Fatalf("OpenPairOffer: %v", err)
	}
	if EncodeKey(gotOffer.DevicePub) != EncodeKey(phone.Public()) || gotOffer.Name != offer.Name || gotOffer.Platform != offer.Platform {
		t.Fatalf("pairing offer round trip mismatch: %+v", gotOffer)
	}

	answerNonce := vecBytes("pairing/answer-nonce", gcmNonceLen)
	const hostName = "vector host"
	sealedAnswer, err := hp.sealPairAnswer(host, hostName, phone.Public(), answerNonce)
	if err != nil {
		t.Fatalf("sealPairAnswer: %v", err)
	}
	answer, err := pp.OpenPairAnswer(sealedAnswer, phone.Public())
	if err != nil {
		t.Fatalf("OpenPairAnswer: %v", err)
	}
	if answer.HostName != hostName || EncodeKey(answer.HostPub) != EncodeKey(host.Public()) {
		t.Fatalf("pairing answer round trip mismatch: %+v", answer)
	}

	offerPlain, err := json.Marshal(pairOfferWire{
		DevicePub: b64.EncodeToString(offer.DevicePub),
		Name:      offer.Name,
		Platform:  offer.Platform,
	})
	if err != nil {
		t.Fatalf("marshal offer plaintext: %v", err)
	}
	confirmSig := host.sign(labelPairConfirm, hp.transcript[:], phone.Public())
	if !bytes.Equal(confirmSig, answer.Sig) {
		t.Fatalf("recomputed confirm signature differs from sealed answer")
	}
	answerPlain, err := json.Marshal(pairAnswerWire{
		HostPub:  b64.EncodeToString(host.Public()),
		HostName: hostName,
		Sig:      b64.EncodeToString(confirmSig),
	})
	if err != nil {
		t.Fatalf("marshal answer plaintext: %v", err)
	}

	vf.Pairing = vectorPairing{
		RelayURL:             relayURL,
		PairingID:            pairingID,
		HostEphPriv:          b64.EncodeToString(hostEph.Bytes()),
		HostEphPub:           b64.EncodeToString(hostEph.PublicKey().Bytes()),
		URI:                  uri,
		PhoneEphPriv:         b64.EncodeToString(phoneEph.Bytes()),
		PhoneEphPub:          b64.EncodeToString(phoneEph.PublicKey().Bytes()),
		Shared:               b64.EncodeToString(pp.shared),
		Transcript:           b64.EncodeToString(pp.transcript[:]),
		OfferName:            offer.Name,
		OfferPlatform:        offer.Platform,
		OfferNonce:           b64.EncodeToString(offerNonce),
		OfferPlaintext:       b64.EncodeToString(offerPlain),
		OfferKey:             b64.EncodeToString(vecHKDF(t, pp.shared, pp.transcript, infoPairOffer)),
		SealedOffer:          b64.EncodeToString(sealedOffer),
		HostName:             hostName,
		ConfirmSigningBuffer: b64.EncodeToString(signingBuffer(labelPairConfirm, [][]byte{hp.transcript[:], phone.Public()})),
		ConfirmSig:           b64.EncodeToString(confirmSig),
		AnswerNonce:          b64.EncodeToString(answerNonce),
		AnswerPlaintext:      b64.EncodeToString(answerPlain),
		AnswerKey:            b64.EncodeToString(vecHKDF(t, pp.shared, pp.transcript, infoPairAnswer)),
		SealedAnswer:         b64.EncodeToString(sealedAnswer),
	}

	// Connection handshake: signed ephemeral DH with both ephemerals and both
	// 16-byte nonces pinned.
	hsPhoneEph := vecX25519(t, "handshake/phone-eph")
	hsPhoneNonce := vecBytes("handshake/phone-nonce", 16)
	hs, hs1, err := newHandshake(phone, host.Public(), hsPhoneEph, hsPhoneNonce)
	if err != nil {
		t.Fatalf("newHandshake: %v", err)
	}

	hsHostEph := vecX25519(t, "handshake/host-eph")
	hsHostNonce := vecBytes("handshake/host-nonce", 16)
	isPaired := func(devicePub []byte) bool { return EncodeKey(devicePub) == EncodeKey(phone.Public()) }
	chHost, hs2, err := acceptHandshake(host, hs1, isPaired, hsHostEph, hsHostNonce)
	if err != nil {
		t.Fatalf("acceptHandshake: %v", err)
	}
	chPhone, err := hs.Complete(hs2)
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}

	shared, err := hsPhoneEph.ECDH(hsHostEph.PublicKey())
	if err != nil {
		t.Fatalf("handshake ecdh: %v", err)
	}
	phoneEphPub := hsPhoneEph.PublicKey().Bytes()
	hostEphPub := hsHostEph.PublicKey().Bytes()
	transcript := sessionTranscript(host.Public(), phone.Public(), phoneEphPub, hsPhoneNonce, hostEphPub, hsHostNonce)

	vf.Handshake = vectorHandshake{
		PhoneEphPriv:     b64.EncodeToString(hsPhoneEph.Bytes()),
		PhoneEphPub:      b64.EncodeToString(phoneEphPub),
		PhoneNonce:       b64.EncodeToString(hsPhoneNonce),
		HS1SigningBuffer: b64.EncodeToString(signingBuffer(labelHS1, [][]byte{host.Public(), phone.Public(), phoneEphPub, hsPhoneNonce})),
		HS1:              hs1,
		HostEphPriv:      b64.EncodeToString(hsHostEph.Bytes()),
		HostEphPub:       b64.EncodeToString(hostEphPub),
		HostNonce:        b64.EncodeToString(hsHostNonce),
		HS2SigningBuffer: b64.EncodeToString(signingBuffer(labelHS2, [][]byte{host.Public(), phone.Public(), phoneEphPub, hsPhoneNonce, hostEphPub, hsHostNonce})),
		HS2:              hs2,
		Shared:           b64.EncodeToString(shared),
		Transcript:       b64.EncodeToString(transcript[:]),
		KeyPhoneToHost:   b64.EncodeToString(vecHKDF(t, shared, transcript, infoSendPhone)),
		KeyHostToPhone:   b64.EncodeToString(vecHKDF(t, shared, transcript, infoSendHost)),
	}

	// Sealed channel: frames in both directions over the handshake's channel,
	// including an empty frame and a non-UTF8 binary frame.
	vf.Channel.Note = "keys and transcript come from the handshake section; aad = transcript"
	seal := func(src, dst *Channel, plaintexts [][]byte) []vectorFrame {
		frames := make([]vectorFrame, 0, len(plaintexts))
		for i, plain := range plaintexts {
			sealed := src.Seal(plain)
			opened, err := dst.Open(sealed)
			if err != nil {
				t.Fatalf("channel frame %d round trip: %v", i, err)
			}
			if !bytes.Equal(opened, plain) {
				t.Fatalf("channel frame %d plaintext mismatch", i)
			}
			frames = append(frames, vectorFrame{
				Counter:   src.sendCounter,
				Plaintext: b64.EncodeToString(plain),
				Sealed:    b64.EncodeToString(sealed),
			})
		}
		return frames
	}
	vf.Channel.PhoneToHost = seal(chPhone, chHost, [][]byte{
		[]byte(`{"id":"1","method":"initialize","params":{}}` + "\n"),
		{},
		vecBytes("channel/binary", 33),
	})
	vf.Channel.HostToPhone = seal(chHost, chPhone, [][]byte{
		[]byte(`{"id":"1","result":{"protocolVersion":"wuu-app-server/v0.1"}}` + "\n"),
		[]byte("修一下登录页的报错 🚀"),
	})

	return vf
}

func TestVectors(t *testing.T) {
	vf := buildVectors(t)
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(vf); err != nil {
		t.Fatalf("encode vectors: %v", err)
	}
	path := filepath.Join("testdata", "vectors.json")
	if *updateVectors {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir testdata: %v", err)
		}
		if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v (generate with: go test ./internal/remote/secure -run TestVectors -update)", path, err)
	}
	if !bytes.Equal(buf.Bytes(), want) {
		t.Fatalf("conformance vectors drifted from %s; if the wire format change is intentional, regenerate with -update and update every port", path)
	}
}
