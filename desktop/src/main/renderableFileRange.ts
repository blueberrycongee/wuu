import { createReadStream, statSync } from "node:fs";
import { Readable } from "node:stream";

// PDF previews and media seeking use byte ranges to avoid downloading the
// entire file before displaying a page or playing from a new position.

export type ByteRange = { start: number; end: number };

// Parses a single `Range: bytes=...` header. Multi-range requests are
// ignored (callers fall back to a full 200 body); PDF.js and Chromium media
// loading use single ranges, so multipart bodies are unnecessary here.
export function parseByteRangeHeader(
  header: string | null,
  size: number,
): ByteRange | "unsatisfiable" | undefined {
  if (!header) {
    return undefined;
  }
  const match = /^bytes=(\d*)-(\d*)$/i.exec(header.trim());
  if (!match) {
    return undefined;
  }
  const [, startRaw, endRaw] = match;
  if (!startRaw && !endRaw) {
    return undefined;
  }
  if (!startRaw) {
    // Suffix form: the last N bytes. N=0 is syntactically valid but names
    // zero bytes, so the header is ignored per RFC 7233.
    const suffixLength = Number(endRaw);
    if (!Number.isSafeInteger(suffixLength) || suffixLength === 0) {
      return undefined;
    }
    if (size === 0) {
      return "unsatisfiable";
    }
    return { start: Math.max(size - suffixLength, 0), end: size - 1 };
  }
  const start = Number(startRaw);
  if (!Number.isSafeInteger(start)) {
    return undefined;
  }
  if (start >= size) {
    return "unsatisfiable";
  }
  const end = endRaw ? Number(endRaw) : size - 1;
  if (!Number.isSafeInteger(end) || end < start) {
    return undefined;
  }
  return { start, end: Math.min(end, size - 1) };
}

export function renderableResponseHeaders(mimeType: string, base?: HeadersInit): Headers {
  const headers = new Headers(base);
  headers.set("content-type", mimeType);
  headers.set("access-control-allow-origin", "*");
  headers.set("accept-ranges", "bytes");
  return headers;
}

// Serves the byte range named by the request's Range header, or returns
// undefined when the header is absent/unparseable and the caller should
// fall back to a full-body response.
export function rangedFileResponse(request: Request, filePath: string, mimeType: string): Response | undefined {
  const rangeHeader = request.headers.get("range");
  if (!rangeHeader) {
    return undefined;
  }
  let size: number;
  try {
    size = statSync(filePath).size;
  } catch {
    return undefined;
  }
  const range = parseByteRangeHeader(rangeHeader, size);
  if (range === undefined) {
    return undefined;
  }
  if (range === "unsatisfiable") {
    return new Response("Range not satisfiable", {
      status: 416,
      headers: renderableResponseHeaders(mimeType, { "content-range": `bytes */${size}` }),
    });
  }
  const body = Readable.toWeb(
    createReadStream(filePath, { start: range.start, end: range.end }),
  ) as unknown as ReadableStream;
  return new Response(body, {
    status: 206,
    headers: renderableResponseHeaders(mimeType, {
      "content-range": `bytes ${range.start}-${range.end}/${size}`,
      "content-length": String(range.end - range.start + 1),
    }),
  });
}
