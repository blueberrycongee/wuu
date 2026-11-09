import { closeSync, openSync, readSync } from "node:fs";

export function readFilePreviewBuffer(
  filePath: string,
  readLimit: number,
): Buffer {
  const buffer = Buffer.alloc(readLimit);
  const descriptor = openSync(filePath, "r");
  let bytesRead = 0;
  try {
    bytesRead = readSync(descriptor, buffer, 0, readLimit, 0);
  } finally {
    closeSync(descriptor);
  }
  return buffer.subarray(0, bytesRead);
}

export function truncateTextBytes(
  text: string,
  maxBytes: number,
): { text: string; truncated: boolean } {
  const buffer = Buffer.from(text, "utf8");
  if (buffer.byteLength <= maxBytes) {
    return { text, truncated: false };
  }
  return {
    text: `${buffer.subarray(0, maxBytes).toString("utf8")}\n[diff truncated]\n`,
    truncated: true,
  };
}
