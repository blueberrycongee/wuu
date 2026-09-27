// Conformance: this port must reproduce every byte of the Go reference
// implementation's vectors (internal/remote/secure/testdata/vectors.json).
// Regenerate the file on the Go side with:
//
//	go test ./internal/remote/secure -run TestVectors -update
//
// Both sides of every exchange are checked: the phone side is the production
// path, the host side proves the port agrees with the reference in both
// directions.

import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

import { b64decode, b64encode } from "../src/b64.js";
import {
  Channel,
  HS1,
  HS2,
  Identity,
  Pairing,
  acceptHandshake,
  buildPairURI,
  encodeKey,
  fingerprint,
  newHandshake,
  parsePairURI,
  sealPairOffer,
  signingBuffer,
  verifyRelayAuth,
} from "../src/secure.js";

interface VectorIdentity {
  seed: string;
  public: string;
  fingerprint: string;
}

interface Vectors {
  format: { version: number };
  identities: { host: VectorIdentity; phone: VectorIdentity };
  relay_auth: Array<{ signer: string; role: string; nonce: string; signing_buffer: string; sig: string }>;
  pairing: {
    relay_url: string;
    pairing_id: string;
    host_eph_priv: string;
    host_eph_pub: string;
    uri: string;
    phone_eph_priv: string;
    phone_eph_pub: string;
    shared: string;
    transcript: string;
    offer_name: string;
    offer_platform: string;
    offer_nonce: string;
    offer_plaintext: string;
    offer_key: string;
    sealed_offer: string;
    host_name: string;
    confirm_signing_buffer: string;
    confirm_sig: string;
    answer_nonce: string;
    answer_plaintext: string;
    answer_key: string;
    sealed_answer: string;
  };
  handshake: {
    phone_eph_priv: string;
    phone_eph_pub: string;
    phone_nonce: string;
    hs1_signing_buffer: string;
    hs1: HS1;
    host_eph_priv: string;
    host_eph_pub: string;
    host_nonce: string;
    hs2_signing_buffer: string;
    hs2: HS2;
    shared: string;
    transcript: string;
    key_phone_to_host: string;
    key_host_to_phone: string;
  };
  channel: {
    phone_to_host: Array<{ counter: number; plaintext: string; sealed: string }>;
    host_to_phone: Array<{ counter: number; plaintext: string; sealed: string }>;
  };
}

const vectorsPath = fileURLToPath(
  new URL("../../../internal/remote/secure/testdata/vectors.json", import.meta.url),
);
const vectors = JSON.parse(readFileSync(vectorsPath, "utf8")) as Vectors;

const host = Identity.fromSeed(b64decode(vectors.identities.host.seed));
const phone = Identity.fromSeed(b64decode(vectors.identities.phone.seed));

describe("format", () => {
  it("is the vector version this port implements", () => {
    expect(vectors.format.version).toBe(1);
  });
});

describe("identities", () => {
  it("derives the pinned public keys and fingerprints", () => {
    expect(encodeKey(host.public_())).toBe(vectors.identities.host.public);
    expect(encodeKey(phone.public_())).toBe(vectors.identities.phone.public);
    expect(fingerprint(host.public_())).toBe(vectors.identities.host.fingerprint);
    expect(fingerprint(phone.public_())).toBe(vectors.identities.phone.fingerprint);
  });
});

describe("relay auth", () => {
  for (const ra of vectors.relay_auth) {
    it(`reproduces the ${ra.signer} signature`, () => {
      const id = ra.signer === "host" ? host : phone;
      const nonce = b64decode(ra.nonce);
      expect(b64encode(signingBuffer("wuu/relay/auth/v1", [nonce, id.public_(), new TextEncoder().encode(ra.role)]))).toBe(
        ra.signing_buffer,
      );
      expect(b64encode(id.signRelayAuth(nonce, ra.role))).toBe(ra.sig);
      expect(verifyRelayAuth(id.public_(), nonce, b64decode(ra.sig), ra.role)).toBe(true);
      expect(verifyRelayAuth(id.public_(), nonce, b64decode(ra.sig), ra.role === "host" ? "phone" : "host")).toBe(false);
    });
  }
});

describe("pairing", () => {
  const p = vectors.pairing;

  it("host side renders the pinned URI", () => {
    const pairing = new Pairing(p.pairing_id, b64decode(p.host_eph_priv));
    expect(b64encode(pairing.ephPub())).toBe(p.host_eph_pub);
    expect(pairing.uri(p.relay_url, host.public_())).toBe(p.uri);
    expect(buildPairURI(p.relay_url, p.pairing_id, pairing.ephPub(), host.public_())).toBe(p.uri);
  });

  it("phone side parses the URI back to the pinned link", () => {
    const link = parsePairURI(p.uri);
    expect(link.relayUrl).toBe(p.relay_url);
    expect(link.pairingId).toBe(p.pairing_id);
    expect(b64encode(link.ephPub)).toBe(p.host_eph_pub);
    expect(b64encode(link.hostPub)).toBe(vectors.identities.host.public);
  });

  it("phone side seals the pinned offer byte-for-byte", () => {
    const link = parsePairURI(p.uri);
    const { payload } = sealPairOffer(
      link,
      { devicePub: phone.public_(), name: p.offer_name, platform: p.offer_platform },
      { ephPriv: b64decode(p.phone_eph_priv), nonce: b64decode(p.offer_nonce) },
    );
    expect(b64encode(payload)).toBe(p.sealed_offer);
  });

  it("host side opens the offer and seals the pinned answer byte-for-byte", () => {
    const pairing = new Pairing(p.pairing_id, b64decode(p.host_eph_priv));
    const { offer, hostPairing } = pairing.openPairOffer(b64decode(p.sealed_offer));
    expect(encodeKey(offer.devicePub)).toBe(vectors.identities.phone.public);
    expect(offer.name).toBe(p.offer_name);
    expect(offer.platform).toBe(p.offer_platform);
    const sealed = hostPairing.sealPairAnswer(host, p.host_name, phone.public_(), b64decode(p.answer_nonce));
    expect(b64encode(sealed)).toBe(p.sealed_answer);
  });

  it("phone side opens and verifies the pinned answer", () => {
    const link = parsePairURI(p.uri);
    const { pairing } = sealPairOffer(
      link,
      { devicePub: phone.public_(), name: p.offer_name, platform: p.offer_platform },
      { ephPriv: b64decode(p.phone_eph_priv), nonce: b64decode(p.offer_nonce) },
    );
    const answer = pairing.openPairAnswer(b64decode(p.sealed_answer), phone.public_());
    expect(encodeKey(answer.hostPub)).toBe(vectors.identities.host.public);
    expect(answer.hostName).toBe(p.host_name);
    expect(b64encode(answer.sig)).toBe(p.confirm_sig);
  });
});

describe("handshake", () => {
  const h = vectors.handshake;

  it("phone side produces the pinned HS1", () => {
    const { hs1 } = newHandshake(phone, host.public_(), {
      ephPriv: b64decode(h.phone_eph_priv),
      nonce: b64decode(h.phone_nonce),
    });
    expect(hs1).toEqual(h.hs1);
  });

  it("host side accepts HS1 and produces the pinned HS2", () => {
    const { hs2 } = acceptHandshake(host, h.hs1, (dev) => encodeKey(dev) === vectors.identities.phone.public, {
      ephPriv: b64decode(h.host_eph_priv),
      nonce: b64decode(h.host_nonce),
    });
    expect(hs2).toEqual(h.hs2);
  });
});

describe("sealed channel", () => {
  const h = vectors.handshake;

  function phoneChannel(): Channel {
    const { handshake } = newHandshake(phone, host.public_(), {
      ephPriv: b64decode(h.phone_eph_priv),
      nonce: b64decode(h.phone_nonce),
    });
    return handshake.complete(h.hs2);
  }

  function hostChannel(): Channel {
    const { channel } = acceptHandshake(host, h.hs1, () => true, {
      ephPriv: b64decode(h.host_eph_priv),
      nonce: b64decode(h.host_nonce),
    });
    return channel;
  }

  it("phone→host frames match byte-for-byte and open on the host side", () => {
    const sender = phoneChannel();
    const receiver = hostChannel();
    for (const frame of vectors.channel.phone_to_host) {
      const sealed = sender.seal(b64decode(frame.plaintext));
      expect(b64encode(sealed)).toBe(frame.sealed);
      expect(b64encode(receiver.open(sealed))).toBe(frame.plaintext);
    }
  });

  it("host→phone frames match byte-for-byte and open on the phone side", () => {
    const sender = hostChannel();
    const receiver = phoneChannel();
    for (const frame of vectors.channel.host_to_phone) {
      const sealed = sender.seal(b64decode(frame.plaintext));
      expect(b64encode(sealed)).toBe(frame.sealed);
      expect(b64encode(receiver.open(sealed))).toBe(frame.plaintext);
    }
  });
});
