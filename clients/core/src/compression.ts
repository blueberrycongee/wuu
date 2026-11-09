import { b64decode } from "./b64";
import { utf8Decode } from "./bytes";

// Matches the remote host app-server line limit.
const maxLineBytes = 32 * 1024 * 1024;

// Runtimes without the standard streaming decoder retain the original protocol.
export function supportsLineCompression(): boolean {
  return typeof DecompressionStream !== "undefined" && typeof Blob !== "undefined";
}

export async function decodeCompressedLine(encoded: string): Promise<unknown> {
  if (encoded.length > Math.ceil(maxLineBytes * 4 / 3)) throw new Error("compressed RPC line too large");
  const input = Uint8Array.from(b64decode(encoded)).buffer;
  const reader = new Blob([input]).stream().pipeThrough(new DecompressionStream("gzip")).getReader();
  const chunks: Uint8Array[] = [];
  let length = 0;
  try {
    for (;;) {
      const next = await reader.read();
      if (next.done) break;
      length += next.value.length;
      if (length > maxLineBytes) throw new Error("decompressed RPC line too large");
      chunks.push(next.value);
    }
  } finally {
    await reader.cancel().catch(() => {});
  }
  const result = new Uint8Array(length);
  let offset = 0;
  for (const chunk of chunks) { result.set(chunk, offset); offset += chunk.length; }
  return JSON.parse(utf8Decode(result));
}
