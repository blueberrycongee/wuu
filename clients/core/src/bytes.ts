// Byte helpers shared across the remote-core. Pure JS with no environment
// dependencies so the same code runs in React Native, browsers, and Node.

export function concatBytes(...parts: Uint8Array[]): Uint8Array {
  let total = 0;
  for (const p of parts) total += p.length;
  const out = new Uint8Array(total);
  let off = 0;
  for (const p of parts) {
    out.set(p, off);
    off += p.length;
  }
  return out;
}

/** Constant-time byte comparison (mirrors the Go side's bytesEqual). */
export function bytesEqual(a: Uint8Array, b: Uint8Array): boolean {
  if (a.length !== b.length) return false;
  let diff = 0;
  for (let i = 0; i < a.length; i++) diff |= a[i] ^ b[i];
  return diff === 0;
}

/** Big-endian uint32, used for length-prefixing signature parts. */
export function u32be(n: number): Uint8Array {
  const out = new Uint8Array(4);
  out[0] = (n >>> 24) & 0xff;
  out[1] = (n >>> 16) & 0xff;
  out[2] = (n >>> 8) & 0xff;
  out[3] = n & 0xff;
  return out;
}

/** Writes a big-endian uint64 into buf at off (counter values fit in 2^53). */
export function putU64be(buf: Uint8Array, off: number, n: number): void {
  let v = BigInt(n);
  for (let i = 7; i >= 0; i--) {
    buf[off + i] = Number(v & 0xffn);
    v >>= 8n;
  }
}

export function readU64be(buf: Uint8Array, off: number): number {
  let v = 0n;
  for (let i = 0; i < 8; i++) v = (v << 8n) | BigInt(buf[off + i]);
  if (v > BigInt(Number.MAX_SAFE_INTEGER)) {
    throw new Error("u64 value exceeds safe integer range");
  }
  return Number(v);
}

// UTF-8 encode/decode implemented locally so the core does not depend on
// TextEncoder/TextDecoder being present (React Native engines vary).

export function utf8Encode(s: string): Uint8Array {
  const out: number[] = [];
  for (let i = 0; i < s.length; i++) {
    let cp = s.codePointAt(i)!;
    if (cp > 0xffff) i++; // surrogate pair consumed
    if (cp < 0x80) {
      out.push(cp);
    } else if (cp < 0x800) {
      out.push(0xc0 | (cp >> 6), 0x80 | (cp & 0x3f));
    } else if (cp < 0x10000) {
      out.push(0xe0 | (cp >> 12), 0x80 | ((cp >> 6) & 0x3f), 0x80 | (cp & 0x3f));
    } else {
      out.push(
        0xf0 | (cp >> 18),
        0x80 | ((cp >> 12) & 0x3f),
        0x80 | ((cp >> 6) & 0x3f),
        0x80 | (cp & 0x3f),
      );
    }
  }
  return new Uint8Array(out);
}

export function utf8Decode(bytes: Uint8Array): string {
  let out = "";
  let i = 0;
  while (i < bytes.length) {
    const b0 = bytes[i];
    let cp: number;
    let extra: number;
    if (b0 < 0x80) {
      cp = b0;
      extra = 0;
    } else if ((b0 & 0xe0) === 0xc0) {
      cp = b0 & 0x1f;
      extra = 1;
    } else if ((b0 & 0xf0) === 0xe0) {
      cp = b0 & 0x0f;
      extra = 2;
    } else if ((b0 & 0xf8) === 0xf0) {
      cp = b0 & 0x07;
      extra = 3;
    } else {
      throw new Error("invalid utf-8 leading byte");
    }
    if (extra > 0 && i + extra >= bytes.length) {
      throw new Error("truncated utf-8 sequence");
    }
    for (let j = 1; j <= extra; j++) {
      const bx = bytes[i + j];
      if ((bx & 0xc0) !== 0x80) throw new Error("invalid utf-8 continuation byte");
      cp = (cp << 6) | (bx & 0x3f);
    }
    i += extra + 1;
    out += String.fromCodePoint(cp);
  }
  return out;
}

export function hexEncode(bytes: Uint8Array): string {
  let out = "";
  for (const b of bytes) out += b.toString(16).padStart(2, "0");
  return out;
}

/** Cryptographically secure random bytes. React Native needs a
 *  crypto.getRandomValues polyfill (react-native-get-random-values). */
export function randomBytes(n: number): Uint8Array {
  const c = (globalThis as { crypto?: { getRandomValues?: (b: Uint8Array) => Uint8Array } }).crypto;
  if (!c || typeof c.getRandomValues !== "function") {
    throw new Error("crypto.getRandomValues is unavailable; install a polyfill (react-native-get-random-values)");
  }
  const out = new Uint8Array(n);
  c.getRandomValues(out);
  return out;
}
