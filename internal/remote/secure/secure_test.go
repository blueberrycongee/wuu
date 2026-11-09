package secure

import (
	"strings"
	"testing"
)

func mustIdentity(t *testing.T) *Identity {
	t.Helper()
	id, err := NewIdentity()
	if err != nil {
		t.Fatalf("NewIdentity: %v", err)
	}
	return id
}

func TestIdentitySeedRoundTrip(t *testing.T) {
	id := mustIdentity(t)
	clone, err := IdentityFromSeed(id.Seed())
	if err != nil {
		t.Fatalf("IdentityFromSeed: %v", err)
	}
	if EncodeKey(clone.Public()) != EncodeKey(id.Public()) {
		t.Fatalf("seed round trip changed public key")
	}
}

func TestRelayAuthSignature(t *testing.T) {
	id := mustIdentity(t)
	nonce := []byte("0123456789abcdef0123456789abcdef")
	sig := id.SignRelayAuth(nonce, "phone")
	if !VerifyRelayAuth(id.Public(), nonce, sig, "phone") {
		t.Fatalf("valid relay auth rejected")
	}
	if VerifyRelayAuth(id.Public(), nonce, sig, "host") {
		t.Fatalf("role substitution accepted")
	}
	other := mustIdentity(t)
	if VerifyRelayAuth(other.Public(), nonce, sig, "phone") {
		t.Fatalf("signature accepted under wrong key")
	}
}

func TestPairURIRoundTrip(t *testing.T) {
	host := mustIdentity(t)
	pairing, err := NewPairing()
	if err != nil {
		t.Fatalf("NewPairing: %v", err)
	}
	uri := pairing.URI("ws://127.0.0.1:8787/v1/connect", host.Public())
	if !strings.HasPrefix(uri, "wuu://pair?") {
		t.Fatalf("unexpected uri %q", uri)
	}
	link, err := ParsePairURI(uri)
	if err != nil {
		t.Fatalf("ParsePairURI: %v", err)
	}
	if link.RelayURL != "ws://127.0.0.1:8787/v1/connect" {
		t.Fatalf("relay url = %q", link.RelayURL)
	}
	if link.PairingID != pairing.ID {
		t.Fatalf("pairing id = %q, want %q", link.PairingID, pairing.ID)
	}
	if EncodeKey(link.HostPub) != EncodeKey(host.Public()) {
		t.Fatalf("host pub mismatch")
	}
	if _, err := ParsePairURI("https://example.com/pair?v=1"); err == nil {
		t.Fatalf("foreign uri accepted")
	}
}

func TestPairingExchange(t *testing.T) {
	host := mustIdentity(t)
	phone := mustIdentity(t)
	pairing, err := NewPairing()
	if err != nil {
		t.Fatalf("NewPairing: %v", err)
	}
	link, err := ParsePairURI(pairing.URI("ws://relay/v1/connect", host.Public()))
	if err != nil {
		t.Fatalf("ParsePairURI: %v", err)
	}

	offerPayload, phonePairing, err := SealPairOffer(link, PairOffer{
		DevicePub: phone.Public(),
		Name:      "pixel",
		Platform:  "android",
	})
	if err != nil {
		t.Fatalf("SealPairOffer: %v", err)
	}

	offer, hostPairing, err := pairing.OpenPairOffer(offerPayload)
	if err != nil {
		t.Fatalf("OpenPairOffer: %v", err)
	}
	if offer.Name != "pixel" || EncodeKey(offer.DevicePub) != EncodeKey(phone.Public()) {
		t.Fatalf("offer mismatch: %+v", offer)
	}

	answerPayload, err := hostPairing.SealPairAnswer(host, "workstation", offer.DevicePub)
	if err != nil {
		t.Fatalf("SealPairAnswer: %v", err)
	}
	answer, err := phonePairing.OpenPairAnswer(answerPayload, phone.Public())
	if err != nil {
		t.Fatalf("OpenPairAnswer: %v", err)
	}
	if answer.HostName != "workstation" {
		t.Fatalf("answer host name = %q", answer.HostName)
	}

	// Tampered offers must not decrypt.
	bad := append([]byte(nil), offerPayload...)
	bad[len(bad)-1] ^= 0x01
	if _, _, err := pairing.OpenPairOffer(bad); err == nil {
		t.Fatalf("tampered offer accepted")
	}

	// An answer signed by an impostor host must be rejected even when the
	// payload decrypts (same pairing key, wrong long-term identity).
	impostor := mustIdentity(t)
	forged, err := hostPairing.SealPairAnswer(impostor, "evil", offer.DevicePub)
	if err != nil {
		t.Fatalf("SealPairAnswer(impostor): %v", err)
	}
	if _, err := phonePairing.OpenPairAnswer(forged, phone.Public()); err == nil {
		t.Fatalf("answer from impostor host accepted")
	}
}

func TestHandshakeAndChannel(t *testing.T) {
	host := mustIdentity(t)
	phone := mustIdentity(t)

	hs, hs1, err := NewHandshake(phone, host.Public())
	if err != nil {
		t.Fatalf("NewHandshake: %v", err)
	}
	hostCh, hs2, err := AcceptHandshake(host, hs1, func(devPub []byte) bool {
		return EncodeKey(devPub) == EncodeKey(phone.Public())
	})
	if err != nil {
		t.Fatalf("AcceptHandshake: %v", err)
	}
	phoneCh, err := hs.Complete(hs2)
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}

	// Both directions round trip.
	frame := phoneCh.Seal([]byte(`{"t":"ping"}`))
	plain, err := hostCh.Open(frame)
	if err != nil {
		t.Fatalf("host Open: %v", err)
	}
	if string(plain) != `{"t":"ping"}` {
		t.Fatalf("host got %q", plain)
	}
	frame2 := hostCh.Seal([]byte(`{"t":"pong"}`))
	plain2, err := phoneCh.Open(frame2)
	if err != nil {
		t.Fatalf("phone Open: %v", err)
	}
	if string(plain2) != `{"t":"pong"}` {
		t.Fatalf("phone got %q", plain2)
	}

	// Replayed frames are rejected.
	if _, err := hostCh.Open(frame); err == nil {
		t.Fatalf("replayed frame accepted")
	}
	// Later frames still work after a rejected replay.
	frame3 := phoneCh.Seal([]byte("x"))
	if _, err := hostCh.Open(frame3); err != nil {
		t.Fatalf("post-replay frame rejected: %v", err)
	}

	// Tampered ciphertext is rejected.
	frame4 := phoneCh.Seal([]byte("y"))
	frame4[len(frame4)-1] ^= 0x01
	if _, err := hostCh.Open(frame4); err == nil {
		t.Fatalf("tampered frame accepted")
	}
}

func TestHandshakeRejections(t *testing.T) {
	host := mustIdentity(t)
	phone := mustIdentity(t)
	stranger := mustIdentity(t)

	// Unpaired device.
	_, hs1, err := NewHandshake(stranger, host.Public())
	if err != nil {
		t.Fatalf("NewHandshake: %v", err)
	}
	if _, _, err := AcceptHandshake(host, hs1, func(devPub []byte) bool {
		return EncodeKey(devPub) == EncodeKey(phone.Public())
	}); err == nil {
		t.Fatalf("unpaired device accepted")
	}

	// A phone must reject an HS2 signed by anyone but its pinned host.
	hs, hs1, err := NewHandshake(phone, host.Public())
	if err != nil {
		t.Fatalf("NewHandshake: %v", err)
	}
	imposter := mustIdentity(t)
	_, forgedHS2, err := AcceptHandshake(imposter, HS1{
		DevicePub: hs1.DevicePub,
		Eph:       hs1.Eph,
		Nonce:     hs1.Nonce,
		// Re-sign hs1 toward the imposter so its AcceptHandshake passes; the
		// phone must still refuse the resulting hs2.
		Sig: resign(t, phone, imposter.Public(), hs1),
	}, func([]byte) bool { return true })
	if err != nil {
		t.Fatalf("imposter AcceptHandshake: %v", err)
	}
	if _, err := hs.Complete(forgedHS2); err == nil {
		t.Fatalf("hs2 from imposter host accepted")
	}
}

// resign rebuilds the hs1 signature toward a different host key, simulating a
// phone that talks to the wrong host. Only used to construct test fixtures.
func resign(t *testing.T, phone *Identity, hostPub []byte, hs1 HS1) string {
	t.Helper()
	eph, err := DecodeKey(hs1.Eph)
	if err != nil {
		t.Fatalf("decode eph: %v", err)
	}
	nonce, err := b64.DecodeString(hs1.Nonce)
	if err != nil {
		t.Fatalf("decode nonce: %v", err)
	}
	return b64.EncodeToString(phone.sign(labelHS1, hostPub, phone.Public(), eph, nonce))
}
