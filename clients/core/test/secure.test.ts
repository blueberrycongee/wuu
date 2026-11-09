// Behavioral requirements that vectors cannot capture, mirrored from the Go
// reference's secure_test.go: replay rejection, signature substitution, key
// pinning, tamper rejection.

import { describe, expect, it } from "vitest";

import { b64decode, b64encode } from "../src/b64.js";
import { randomBytes, utf8Encode } from "../src/bytes.js";
import {
  Identity,
  Pairing,
  acceptHandshake,
  buildPairURI,
  newHandshake,
  parsePairURI,
  sealPairOffer,
  verifyRelayAuth,
} from "../src/secure.js";

function pairOfChannels() {
  const host = Identity.generate();
  const phone = Identity.generate();
  const { handshake, hs1 } = newHandshake(phone, host.public_());
  const { channel: hostCh, hs2 } = acceptHandshake(host, hs1, () => true);
  const phoneCh = handshake.complete(hs2);
  return { hostCh, phoneCh };
}

describe("relay auth", () => {
  it("rejects role substitution and wrong keys", () => {
    const id = Identity.generate();
    const nonce = randomBytes(32);
    const sig = id.signRelayAuth(nonce, "phone");
    expect(verifyRelayAuth(id.public_(), nonce, sig, "phone")).toBe(true);
    expect(verifyRelayAuth(id.public_(), nonce, sig, "host")).toBe(false);
    expect(verifyRelayAuth(Identity.generate().public_(), nonce, sig, "phone")).toBe(false);
  });
});

describe("pairing", () => {
  it("rejects an answer whose host key does not match the QR pin", () => {
    const host = Identity.generate();
    const imposter = Identity.generate();
    const phone = Identity.generate();
    const pairing = Pairing.generate();
    // The QR pins the real host, but the answer is confirmed by an imposter.
    const link = parsePairURI(pairing.uri("ws://relay", host.public_()));
    const { payload, pairing: pp } = sealPairOffer(link, { devicePub: phone.public_(), name: "p" });
    const { hostPairing } = pairing.openPairOffer(payload);
    const answer = hostPairing.sealPairAnswer(imposter, "evil", phone.public_());
    expect(() => pp.openPairAnswer(answer, phone.public_())).toThrow(/does not match QR pin/);
  });

  it("rejects an answer signed over a different device key", () => {
    const host = Identity.generate();
    const phone = Identity.generate();
    const other = Identity.generate();
    const pairing = Pairing.generate();
    const link = parsePairURI(pairing.uri("ws://relay", host.public_()));
    const { payload, pairing: pp } = sealPairOffer(link, { devicePub: phone.public_(), name: "p" });
    const { hostPairing } = pairing.openPairOffer(payload);
    const answer = hostPairing.sealPairAnswer(host, "h", other.public_());
    expect(() => pp.openPairAnswer(answer, phone.public_())).toThrow(/signature invalid/);
  });

  it("rejects tampered offers", () => {
    const host = Identity.generate();
    const phone = Identity.generate();
    const pairing = Pairing.generate();
    const link = parsePairURI(pairing.uri("ws://relay", host.public_()));
    const { payload } = sealPairOffer(link, { devicePub: phone.public_(), name: "p" });
    payload[payload.length - 1] ^= 0x01;
    expect(() => pairing.openPairOffer(payload)).toThrow();
  });

  it("parses URIs with escaped relay urls and rejects foreign schemes", () => {
    const host = Identity.generate();
    const eph = randomBytes(32);
    const uri = buildPairURI("ws://relay.example.com/v1/connect?region=eu", "pid", eph, host.public_());
    const link = parsePairURI(uri);
    expect(link.relayUrl).toBe("ws://relay.example.com/v1/connect?region=eu");
    expect(() => parsePairURI("https://pair?v=1")).toThrow(/not a wuu pairing uri/);
    expect(() => parsePairURI("wuu://pair?v=2")).toThrow(/unsupported pairing uri version/);
  });
});

describe("handshake", () => {
  it("rejects unpaired devices", () => {
    const host = Identity.generate();
    const phone = Identity.generate();
    const { hs1 } = newHandshake(phone, host.public_());
    expect(() => acceptHandshake(host, hs1, () => false)).toThrow(/unpaired/);
  });

  it("rejects hs1 signed for a different host", () => {
    const host = Identity.generate();
    const otherHost = Identity.generate();
    const phone = Identity.generate();
    // Signed toward otherHost, presented to host.
    const { hs1 } = newHandshake(phone, otherHost.public_());
    expect(() => acceptHandshake(host, hs1, () => true)).toThrow(/signature invalid/);
  });

  it("rejects hs2 from an imposter host", () => {
    const host = Identity.generate();
    const imposter = Identity.generate();
    const phone = Identity.generate();
    // The phone pins the real host; the imposter answers a handshake that was
    // addressed to itself and replays that hs2 into the pinned exchange.
    const { handshake } = newHandshake(phone, host.public_());
    const { hs1: towardImposter } = newHandshake(phone, imposter.public_());
    const { hs2 } = acceptHandshake(imposter, towardImposter, () => true);
    expect(() => handshake.complete(hs2)).toThrow(/signature invalid/);
  });
});

describe("sealed channel", () => {
  it("rejects replayed and stale frames", () => {
    const { hostCh, phoneCh } = pairOfChannels();
    const f1 = phoneCh.seal(utf8Encode("one"));
    const f2 = phoneCh.seal(utf8Encode("two"));
    hostCh.open(f1);
    hostCh.open(f2);
    expect(() => hostCh.open(f1)).toThrow(/replayed/);
    expect(() => hostCh.open(f2)).toThrow(/replayed/);
  });

  it("rejects tampered frames and cross-direction frames", () => {
    const { hostCh, phoneCh } = pairOfChannels();
    const frame = phoneCh.seal(utf8Encode("payload"));
    const tampered = new Uint8Array(frame);
    tampered[tampered.length - 1] ^= 0x01;
    expect(() => hostCh.open(tampered)).toThrow();
    // A frame sealed by the phone must not open on the phone side (direction
    // keys differ).
    const frame2 = phoneCh.seal(utf8Encode("payload"));
    expect(() => phoneCh.open(frame2)).toThrow();
  });

  it("rejects frames from a different session", () => {
    const { phoneCh } = pairOfChannels();
    const { hostCh: otherHostCh } = pairOfChannels();
    const frame = phoneCh.seal(utf8Encode("cross-session"));
    expect(() => otherHostCh.open(frame)).toThrow();
  });
});

describe("base64url", () => {
  it("round-trips and rejects invalid input", () => {
    for (const n of [0, 1, 2, 3, 31, 32, 33, 100]) {
      const bytes = randomBytes(n);
      expect(b64decode(b64encode(bytes))).toEqual(bytes);
    }
    expect(() => b64decode("a")).toThrow();
    expect(() => b64decode("ab=c")).toThrow();
    expect(() => b64decode("a+b")).toThrow();
  });
});
