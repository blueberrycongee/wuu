// Unpadded base64url (RFC 4648 §5) — the canonical byte encoding across the
// entire remote protocol (keys, nonces, payloads, stored credentials).
// Implemented locally so it runs identically in React Native, browsers, and
// Node without Buffer/btoa.

const ALPHABET = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_";

const REVERSE = new Int16Array(128).fill(-1);
for (let i = 0; i < ALPHABET.length; i++) REVERSE[ALPHABET.charCodeAt(i)] = i;

export function b64encode(bytes: Uint8Array): string {
  let out = "";
  let i = 0;
  for (; i + 2 < bytes.length; i += 3) {
    const n = (bytes[i] << 16) | (bytes[i + 1] << 8) | bytes[i + 2];
    out += ALPHABET[(n >> 18) & 63] + ALPHABET[(n >> 12) & 63] + ALPHABET[(n >> 6) & 63] + ALPHABET[n & 63];
  }
  const rem = bytes.length - i;
  if (rem === 1) {
    const n = bytes[i] << 16;
    out += ALPHABET[(n >> 18) & 63] + ALPHABET[(n >> 12) & 63];
  } else if (rem === 2) {
    const n = (bytes[i] << 16) | (bytes[i + 1] << 8);
    out += ALPHABET[(n >> 18) & 63] + ALPHABET[(n >> 12) & 63] + ALPHABET[(n >> 6) & 63];
  }
  return out;
}

/** Strict decode: rejects padding, whitespace, and out-of-alphabet
 *  characters, mirroring Go's RawURLEncoding. */
export function b64decode(s: string): Uint8Array {
  const rem = s.length % 4;
  if (rem === 1) throw new Error("invalid base64url length");
  const outLen = Math.floor(s.length / 4) * 3 + (rem === 2 ? 1 : rem === 3 ? 2 : 0);
  const out = new Uint8Array(outLen);
  let outOff = 0;
  let acc = 0;
  let accBits = 0;
  for (let i = 0; i < s.length; i++) {
    const code = s.charCodeAt(i);
    const v = code < 128 ? REVERSE[code] : -1;
    if (v < 0) throw new Error(`invalid base64url character ${JSON.stringify(s[i])}`);
    acc = (acc << 6) | v;
    accBits += 6;
    if (accBits >= 8) {
      accBits -= 8;
      out[outOff++] = (acc >> accBits) & 0xff;
    }
  }
  // Reject non-canonical trailing bits (Go rejects these too).
  if (accBits > 0 && (acc & ((1 << accBits) - 1)) !== 0) {
    throw new Error("invalid base64url trailing bits");
  }
  return out;
}
