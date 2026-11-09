// The cryptographic core of wuu remote control, ported byte-for-byte from
// internal/remote/secure (the reference implementation). Every output of this
// module is pinned by internal/remote/secure/testdata/vectors.json; run the
// conformance suite after any change here or in the Go reference.
//
// Trust model recap: the relay is untrusted and sees only ciphertext and
// routing metadata. The pairing URI travels out-of-band (QR on the host
// screen) and carries a one-time X25519 key that never touches the relay;
// after pairing, every connection runs a signed ephemeral Diffie-Hellman
// handshake yielding per-direction AES-256-GCM keys with forward secrecy.

import { ed25519, x25519 } from "@noble/curves/ed25519.js";
import { sha256 } from "@noble/hashes/sha2.js";
import { hkdf } from "@noble/hashes/hkdf.js";
import { gcm } from "@noble/ciphers/aes.js";

import { b64decode, b64encode } from "./b64";
import {
  bytesEqual,
  concatBytes,
  hexEncode,
  putU64be,
  randomBytes,
  readU64be,
  u32be,
  utf8Decode,
  utf8Encode,
} from "./bytes";

export const PAIR_URI_SCHEME = "wuu";

const LABEL_RELAY_AUTH = "wuu/relay/auth/v1";
const LABEL_PAIR_CONFIRM = "wuu/pair/confirm/v1";
const LABEL_HS1 = "wuu/hs1/v1";
const LABEL_HS2 = "wuu/hs2/v1";

const INFO_PAIR_OFFER = "wuu pair offer v1";
const INFO_PAIR_ANSWER = "wuu pair answer v1";
const INFO_SEND_PHONE = "wuu e2e phone->host v1";
const INFO_SEND_HOST = "wuu e2e host->phone v1";

const X25519_KEY_LEN = 32;
const GCM_NONCE_LEN = 12;

export function encodeKey(pub: Uint8Array): string {
  return b64encode(pub);
}

export function decodeKey(s: string): Uint8Array {
  const raw = b64decode(s.trim());
  if (raw.length !== 32) throw new Error(`decode key: got ${raw.length} bytes, want 32`);
  return raw;
}

/** Short display-only identifier for a public key (first 6 hash bytes, hex). */
export function fingerprint(pub: Uint8Array): string {
  return hexEncode(sha256(pub).subarray(0, 6));
}

// --- Identity ----------------------------------------------------------------

/** Long-term Ed25519 device identity; the public key doubles as the device
 *  address at the relay. */
export class Identity {
  private constructor(
    private readonly seedBytes: Uint8Array,
    private readonly pub: Uint8Array,
  ) {}

  static fromSeed(seed: Uint8Array): Identity {
    if (seed.length !== 32) throw new Error(`identity seed must be 32 bytes, got ${seed.length}`);
    return new Identity(new Uint8Array(seed), ed25519.getPublicKey(seed));
  }

  static generate(): Identity {
    return Identity.fromSeed(randomBytes(32));
  }

  seed(): Uint8Array {
    return new Uint8Array(this.seedBytes);
  }

  public_(): Uint8Array {
    return new Uint8Array(this.pub);
  }

  sign(label: string, ...parts: Uint8Array[]): Uint8Array {
    return ed25519.sign(signingBuffer(label, parts), this.seedBytes);
  }

  signRelayAuth(nonce: Uint8Array, role: string): Uint8Array {
    return this.sign(LABEL_RELAY_AUTH, nonce, this.pub, utf8Encode(role));
  }
}

/** Domain-separated signing buffer: label || 0x00 || length-prefixed parts. */
export function signingBuffer(label: string, parts: Uint8Array[]): Uint8Array {
  const pieces: Uint8Array[] = [utf8Encode(label), new Uint8Array([0])];
  for (const p of parts) {
    pieces.push(u32be(p.length), p);
  }
  return concatBytes(...pieces);
}

export function verify(pub: Uint8Array, label: string, sig: Uint8Array, ...parts: Uint8Array[]): boolean {
  if (pub.length !== 32 || sig.length !== 64) return false;
  try {
    return ed25519.verify(sig, signingBuffer(label, parts), pub);
  } catch {
    return false;
  }
}

export function verifyRelayAuth(pub: Uint8Array, nonce: Uint8Array, sig: Uint8Array, role: string): boolean {
  return verify(pub, LABEL_RELAY_AUTH, sig, nonce, pub, utf8Encode(role));
}

// --- X25519 helpers ----------------------------------------------------------

function x25519Shared(priv: Uint8Array, peerPub: Uint8Array): Uint8Array {
  const shared = x25519.getSharedSecret(priv, peerPub);
  // Mirror Go crypto/ecdh, which rejects the all-zero (low-order) result.
  let acc = 0;
  for (const b of shared) acc |= b;
  if (acc === 0) throw new Error("x25519: low-order shared secret");
  return shared;
}

// --- Pairing -----------------------------------------------------------------

/** Parsed form of a pairing URI (QR content). */
export interface PairLink {
  relayUrl: string;
  pairingId: string;
  ephPub: Uint8Array; // host pairing ephemeral X25519 public key
  hostPub: Uint8Array; // host long-term Ed25519 public key (pinned)
}

export interface PairOffer {
  devicePub: Uint8Array;
  name: string;
  platform?: string;
}

export interface PairAnswer {
  hostPub: Uint8Array;
  hostName: string;
  sig: Uint8Array;
}

// Mirrors Go url.QueryEscape: unreserved = A-Za-z0-9 - _ . ~ ; space → '+'.
function queryEscape(s: string): string {
  let out = "";
  for (const b of utf8Encode(s)) {
    if (
      (b >= 0x41 && b <= 0x5a) || // A-Z
      (b >= 0x61 && b <= 0x7a) || // a-z
      (b >= 0x30 && b <= 0x39) || // 0-9
      b === 0x2d || b === 0x5f || b === 0x2e || b === 0x7e // - _ . ~
    ) {
      out += String.fromCharCode(b);
    } else if (b === 0x20) {
      out += "+";
    } else {
      out += "%" + b.toString(16).toUpperCase().padStart(2, "0");
    }
  }
  return out;
}

function queryUnescape(s: string): string {
  const bytes: number[] = [];
  for (let i = 0; i < s.length; i++) {
    const c = s[i];
    if (c === "+") {
      bytes.push(0x20);
    } else if (c === "%") {
      if (i + 3 > s.length) throw new Error("truncated percent escape");
      const hex = s.slice(i + 1, i + 3);
      if (!/^[0-9a-fA-F]{2}$/.test(hex)) throw new Error("invalid percent escape");
      bytes.push(parseInt(hex, 16));
      i += 2;
    } else {
      const code = c.charCodeAt(0);
      if (code > 127) throw new Error("non-ascii character in query");
      bytes.push(code);
    }
  }
  return utf8Decode(new Uint8Array(bytes));
}

/** Renders the pairing deep link with Go-identical query encoding
 *  (alphabetical key order, url.QueryEscape escaping). */
export function buildPairURI(relayUrl: string, pairingId: string, ephPub: Uint8Array, hostPub: Uint8Array): string {
  const pairs: Array<[string, string]> = [
    ["h", b64encode(hostPub)],
    ["k", b64encode(ephPub)],
    ["p", pairingId],
    ["r", relayUrl],
    ["v", "1"],
  ];
  const query = pairs.map(([k, v]) => `${k}=${queryEscape(v)}`).join("&");
  return `${PAIR_URI_SCHEME}://pair?${query}`;
}

export function parsePairURI(s: string): PairLink {
  const trimmed = s.trim();
  const prefix = `${PAIR_URI_SCHEME}://pair`;
  if (!trimmed.startsWith(prefix)) throw new Error(`not a wuu pairing uri: ${JSON.stringify(s)}`);
  let rest = trimmed.slice(prefix.length);
  if (rest.startsWith("/")) rest = rest.slice(1);
  if (rest !== "" && !rest.startsWith("?")) throw new Error(`not a wuu pairing uri: ${JSON.stringify(s)}`);
  const query = rest.startsWith("?") ? rest.slice(1) : "";
  const params = new Map<string, string>();
  for (const part of query.split("&")) {
    if (part === "") continue;
    const eq = part.indexOf("=");
    const key = eq < 0 ? part : part.slice(0, eq);
    const value = eq < 0 ? "" : part.slice(eq + 1);
    if (!params.has(key)) params.set(key, queryUnescape(value));
  }
  if (params.get("v") !== "1") {
    throw new Error(`unsupported pairing uri version ${JSON.stringify(params.get("v") ?? "")}`);
  }
  const ephPub = decodeKey(params.get("k") ?? "");
  const hostPub = decodeKey(params.get("h") ?? "");
  const link: PairLink = {
    relayUrl: params.get("r") ?? "",
    pairingId: params.get("p") ?? "",
    ephPub,
    hostPub,
  };
  if (link.relayUrl === "" || link.pairingId === "") {
    throw new Error("pairing uri missing relay or pairing id");
  }
  return link;
}

function pairTranscript(hostEph: Uint8Array, phoneEph: Uint8Array, pairingId: string): Uint8Array {
  return sha256(concatBytes(utf8Encode("wuu/pair/v1"), hostEph, phoneEph, utf8Encode(pairingId)));
}

/** Phone-side state between sealing the offer and opening the answer. */
export class PhonePairing {
  constructor(
    readonly link: PairLink,
    readonly ephPub: Uint8Array,
    private readonly shared: Uint8Array,
    private readonly transcript: Uint8Array,
  ) {}

  openPairAnswer(payload: Uint8Array, devicePub: Uint8Array): PairAnswer {
    const plain = aeadOpen(this.shared, this.transcript, INFO_PAIR_ANSWER, payload);
    const w = JSON.parse(utf8Decode(plain)) as { host_pub?: string; host_name?: string; sig?: string };
    const hostPub = decodeKey(w.host_pub ?? "");
    const sig = b64decode(w.sig ?? "");
    if (!bytesEqual(hostPub, this.link.hostPub)) {
      throw new Error("pairing answer host key does not match QR pin");
    }
    if (!verify(hostPub, LABEL_PAIR_CONFIRM, sig, this.transcript, devicePub)) {
      throw new Error("pairing answer signature invalid");
    }
    return { hostPub, hostName: w.host_name ?? "", sig };
  }
}

/** Builds the encrypted pairing offer. Options pin the ephemeral key and
 *  nonce for conformance tests; production omits them for fresh randomness. */
export function sealPairOffer(
  link: PairLink,
  offer: PairOffer,
  opts: { ephPriv?: Uint8Array; nonce?: Uint8Array } = {},
): { payload: Uint8Array; pairing: PhonePairing } {
  const ephPriv = opts.ephPriv ?? randomBytes(32);
  const nonce = opts.nonce ?? randomBytes(GCM_NONCE_LEN);
  const ephPub = x25519.getPublicKey(ephPriv);
  const shared = x25519Shared(ephPriv, link.ephPub);
  const transcript = pairTranscript(link.ephPub, ephPub, link.pairingId);

  const wire: Record<string, string> = { device_pub: b64encode(offer.devicePub), name: offer.name };
  if (offer.platform) wire.platform = offer.platform;
  const plain = utf8Encode(JSON.stringify(wire));
  const sealed = aeadSealWithNonce(shared, transcript, INFO_PAIR_OFFER, nonce, plain);
  return {
    payload: concatBytes(ephPub, sealed),
    pairing: new PhonePairing(link, ephPub, shared, transcript),
  };
}

// --- Host-side pairing (used by tests and the conformance suite; the
// production host is the Go implementation) ----------------------------------

export class HostPairing {
  constructor(
    private readonly shared: Uint8Array,
    private readonly transcript: Uint8Array,
  ) {}

  sealPairAnswer(host: Identity, hostName: string, devicePub: Uint8Array, nonce?: Uint8Array): Uint8Array {
    const sig = host.sign(LABEL_PAIR_CONFIRM, this.transcript, devicePub);
    const wire: Record<string, string> = { host_pub: b64encode(host.public_()) };
    if (hostName) wire.host_name = hostName;
    wire.sig = b64encode(sig);
    const plain = utf8Encode(JSON.stringify(wire));
    return aeadSealWithNonce(this.shared, this.transcript, INFO_PAIR_ANSWER, nonce ?? randomBytes(GCM_NONCE_LEN), plain);
  }
}

export class Pairing {
  constructor(
    readonly id: string,
    private readonly priv: Uint8Array,
  ) {}

  static generate(): Pairing {
    return new Pairing(b64encode(randomBytes(16)), randomBytes(32));
  }

  ephPub(): Uint8Array {
    return x25519.getPublicKey(this.priv);
  }

  uri(relayUrl: string, hostPub: Uint8Array): string {
    return buildPairURI(relayUrl, this.id, this.ephPub(), hostPub);
  }

  openPairOffer(payload: Uint8Array): { offer: PairOffer; hostPairing: HostPairing } {
    if (payload.length < X25519_KEY_LEN + GCM_NONCE_LEN) throw new Error("pairing offer too short");
    const phoneEph = payload.subarray(0, X25519_KEY_LEN);
    const shared = x25519Shared(this.priv, phoneEph);
    const transcript = pairTranscript(this.ephPub(), phoneEph, this.id);
    const plain = aeadOpen(shared, transcript, INFO_PAIR_OFFER, payload.subarray(X25519_KEY_LEN));
    const w = JSON.parse(utf8Decode(plain)) as { device_pub?: string; name?: string; platform?: string };
    return {
      offer: { devicePub: decodeKey(w.device_pub ?? ""), name: w.name ?? "", platform: w.platform },
      hostPairing: new HostPairing(shared, transcript),
    };
  }
}

// --- Connection handshake ------------------------------------------------------

/** HS1/HS2 travel as plaintext JSON through the relay (public values +
 *  signatures only); field values are base64url strings. */
export interface HS1 {
  device_pub: string;
  eph: string;
  nonce: string;
  sig: string;
}

export interface HS2 {
  eph: string;
  nonce: string;
  sig: string;
}

function sessionTranscript(
  hostPub: Uint8Array,
  devPub: Uint8Array,
  phoneEph: Uint8Array,
  phoneNonce: Uint8Array,
  hostEph: Uint8Array,
  hostNonce: Uint8Array,
): Uint8Array {
  const pieces: Uint8Array[] = [utf8Encode("wuu/session/v1")];
  for (const p of [hostPub, devPub, phoneEph, phoneNonce, hostEph, hostNonce]) {
    pieces.push(u32be(p.length), p);
  }
  return sha256(concatBytes(...pieces));
}

/** Phone-side in-flight handshake state. */
export class Handshake {
  constructor(
    private readonly phone: Identity,
    private readonly hostPub: Uint8Array,
    private readonly ephPriv: Uint8Array,
    private readonly nonce: Uint8Array,
  ) {}

  complete(hs2: HS2): Channel {
    const hostEph = decodeKey(hs2.eph);
    const hostNonce = b64decode(hs2.nonce);
    const sig = b64decode(hs2.sig);
    const phoneEphPub = x25519.getPublicKey(this.ephPriv);
    if (
      !verify(this.hostPub, LABEL_HS2, sig, this.hostPub, this.phone.public_(), phoneEphPub, this.nonce, hostEph, hostNonce)
    ) {
      throw new Error("hs2 signature invalid");
    }
    const shared = x25519Shared(this.ephPriv, hostEph);
    const transcript = sessionTranscript(this.hostPub, this.phone.public_(), phoneEphPub, this.nonce, hostEph, hostNonce);
    return new Channel(shared, transcript, this.hostPub, true);
  }
}

export function newHandshake(
  phone: Identity,
  hostPub: Uint8Array,
  opts: { ephPriv?: Uint8Array; nonce?: Uint8Array } = {},
): { handshake: Handshake; hs1: HS1 } {
  const ephPriv = opts.ephPriv ?? randomBytes(32);
  const nonce = opts.nonce ?? randomBytes(16);
  const ephPub = x25519.getPublicKey(ephPriv);
  const sig = phone.sign(LABEL_HS1, hostPub, phone.public_(), ephPub, nonce);
  return {
    handshake: new Handshake(phone, hostPub, ephPriv, nonce),
    hs1: {
      device_pub: b64encode(phone.public_()),
      eph: b64encode(ephPub),
      nonce: b64encode(nonce),
      sig: b64encode(sig),
    },
  };
}

/** Host side of the handshake (tests and conformance only in this port). */
export function acceptHandshake(
  host: Identity,
  hs1: HS1,
  isPaired: (devicePub: Uint8Array) => boolean,
  opts: { ephPriv?: Uint8Array; nonce?: Uint8Array } = {},
): { channel: Channel; hs2: HS2 } {
  const devPub = decodeKey(hs1.device_pub);
  const phoneEph = decodeKey(hs1.eph);
  const phoneNonce = b64decode(hs1.nonce);
  const sig = b64decode(hs1.sig);
  if (!isPaired(devPub)) throw new Error("handshake from unpaired device");
  if (!verify(devPub, LABEL_HS1, sig, host.public_(), devPub, phoneEph, phoneNonce)) {
    throw new Error("hs1 signature invalid");
  }
  const ephPriv = opts.ephPriv ?? randomBytes(32);
  const hostNonce = opts.nonce ?? randomBytes(16);
  const hostEphPub = x25519.getPublicKey(ephPriv);
  const shared = x25519Shared(ephPriv, phoneEph);
  const sig2 = host.sign(LABEL_HS2, host.public_(), devPub, phoneEph, phoneNonce, hostEphPub, hostNonce);
  const transcript = sessionTranscript(host.public_(), devPub, phoneEph, phoneNonce, hostEphPub, hostNonce);
  return {
    channel: new Channel(shared, transcript, devPub, false),
    hs2: { eph: b64encode(hostEphPub), nonce: b64encode(hostNonce), sig: b64encode(sig2) },
  };
}

// --- Sealed channel ------------------------------------------------------------

/** One established end-to-end session. Each direction has its own AES-256-GCM
 *  key and a strictly monotonic counter nonce; open() rejects any frame whose
 *  counter does not advance (dedupe + replay protection in one). */
export class Channel {
  private readonly sendKey: Uint8Array;
  private readonly recvKey: Uint8Array;
  private sendCounter = 0;
  private recvLast = 0;
  private readonly transcript: Uint8Array;
  private readonly peerPub: Uint8Array;

  constructor(shared: Uint8Array, transcript: Uint8Array, peer: Uint8Array, phoneSide: boolean) {
    const phoneKey = hkdf(sha256, shared, transcript, utf8Encode(INFO_SEND_PHONE), 32);
    const hostKey = hkdf(sha256, shared, transcript, utf8Encode(INFO_SEND_HOST), 32);
    this.sendKey = phoneSide ? phoneKey : hostKey;
    this.recvKey = phoneSide ? hostKey : phoneKey;
    this.transcript = new Uint8Array(transcript);
    this.peerPub = new Uint8Array(peer);
  }

  peer(): Uint8Array {
    return new Uint8Array(this.peerPub);
  }

  /** Encrypts one frame: nonce(12, counter-based) || ciphertext+tag. */
  seal(plaintext: Uint8Array): Uint8Array {
    this.sendCounter++;
    const nonce = new Uint8Array(GCM_NONCE_LEN);
    putU64be(nonce, 4, this.sendCounter);
    const ct = gcm(this.sendKey, nonce, this.transcript).encrypt(plaintext);
    return concatBytes(nonce, ct);
  }

  /** Decrypts one frame, enforcing counter monotonicity. */
  open(frame: Uint8Array): Uint8Array {
    if (frame.length < GCM_NONCE_LEN) throw new Error("sealed frame too short");
    const nonce = frame.subarray(0, GCM_NONCE_LEN);
    const counter = readU64be(nonce, 4);
    if (counter <= this.recvLast) {
      throw new Error(`sealed frame counter ${counter} replayed (last ${this.recvLast})`);
    }
    const plain = gcm(this.recvKey, nonce, this.transcript).decrypt(frame.subarray(GCM_NONCE_LEN));
    this.recvLast = counter;
    return plain;
  }
}

// --- One-shot AEAD helpers (pairing exchange) --------------------------------

function oneShotKey(shared: Uint8Array, transcript: Uint8Array, info: string): Uint8Array {
  return hkdf(sha256, shared, transcript, utf8Encode(info), 32);
}

function aeadSealWithNonce(
  shared: Uint8Array,
  transcript: Uint8Array,
  info: string,
  nonce: Uint8Array,
  plaintext: Uint8Array,
): Uint8Array {
  const key = oneShotKey(shared, transcript, info);
  return concatBytes(nonce, gcm(key, nonce, transcript).encrypt(plaintext));
}

function aeadOpen(shared: Uint8Array, transcript: Uint8Array, info: string, payload: Uint8Array): Uint8Array {
  if (payload.length < GCM_NONCE_LEN) throw new Error("sealed payload too short");
  const key = oneShotKey(shared, transcript, info);
  return gcm(key, payload.subarray(0, GCM_NONCE_LEN), transcript).decrypt(payload.subarray(GCM_NONCE_LEN));
}
