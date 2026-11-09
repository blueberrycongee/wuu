import { gzipSync } from "node:zlib";
import { expect, it } from "vitest";
import { b64encode } from "../src/b64";
import { decodeCompressedLine } from "../src/compression";

it("rejects damaged and oversized decompressed RPC lines", async () => {
  await expect(decodeCompressedLine(b64encode(new Uint8Array([1, 2, 3])))).rejects.toThrow();
  const bomb = gzipSync(JSON.stringify({ text: "x".repeat(32 * 1024 * 1024) }));
  await expect(decodeCompressedLine(b64encode(bomb))).rejects.toThrow("decompressed RPC line too large");
});
