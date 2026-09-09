import { b64encode, parsePairURI } from "@wuu/remote-core";

export function pairingMatchesHost(uri: string, hostPub: string): boolean {
  try {
    return b64encode(parsePairURI(uri).hostPub) === hostPub;
  } catch {
    return false;
  }
}

/** Accept both the camera link and the underlying pairing URI. */
export function pairingURI(input: string): string {
  const value = input.trim();
  if (value.startsWith("wuu://pair?")) return value;
  try {
    const url = new URL(value);
    const uri = new URLSearchParams(url.hash.slice(1)).get("pair");
    if ((url.protocol === "http:" || url.protocol === "https:") && uri?.startsWith("wuu://pair?")) return uri;
  } catch { /* Report a user-facing validation error below. */ }
  throw new Error("请粘贴电脑上复制的完整配对链接，或重新扫描二维码。");
}

export function pairingExpired(error: unknown): boolean {
  const message = error instanceof Error ? error.message : String(error);
  return /\b(no_such_pairing|pairing_expired)\b/.test(message);
}
