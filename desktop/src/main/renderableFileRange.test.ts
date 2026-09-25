import { mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { afterAll, describe, expect, it } from "vitest";
import { parseByteRangeHeader, rangedFileResponse } from "./renderableFileRange";

const tempDirs: string[] = [];

function makeFile(contents: string): string {
  const dir = mkdtempSync(join(tmpdir(), "wuu-file-range-"));
  tempDirs.push(dir);
  const filePath = join(dir, "sample.bin");
  writeFileSync(filePath, contents);
  return filePath;
}

afterAll(() => {
  for (const dir of tempDirs) {
    rmSync(dir, { recursive: true, force: true });
  }
});

describe("parseByteRangeHeader", () => {
  it("returns undefined without a header", () => {
    expect(parseByteRangeHeader(null, 100)).toBeUndefined();
  });

  it("parses an explicit start/end range", () => {
    expect(parseByteRangeHeader("bytes=10-19", 100)).toEqual({ start: 10, end: 19 });
  });

  it("clamps the end to the file size", () => {
    expect(parseByteRangeHeader("bytes=90-9999", 100)).toEqual({ start: 90, end: 99 });
  });

  it("parses an open-ended range", () => {
    expect(parseByteRangeHeader("bytes=40-", 100)).toEqual({ start: 40, end: 99 });
  });

  it("parses a suffix range", () => {
    expect(parseByteRangeHeader("bytes=-25", 100)).toEqual({ start: 75, end: 99 });
  });

  it("clamps an oversized suffix to the whole file", () => {
    expect(parseByteRangeHeader("bytes=-5000", 100)).toEqual({ start: 0, end: 99 });
  });

  it("rejects a start beyond the end of the file", () => {
    expect(parseByteRangeHeader("bytes=100-", 100)).toBe("unsatisfiable");
    expect(parseByteRangeHeader("bytes=0-0", 0)).toBe("unsatisfiable");
  });

  it("ignores malformed headers", () => {
    expect(parseByteRangeHeader("bytes=-", 100)).toBeUndefined();
    expect(parseByteRangeHeader("bytes=", 100)).toBeUndefined();
    expect(parseByteRangeHeader("items=0-10", 100)).toBeUndefined();
    expect(parseByteRangeHeader("bytes=0-1,5-10", 100)).toBeUndefined();
    expect(parseByteRangeHeader("bytes=-0", 100)).toBeUndefined();
    expect(parseByteRangeHeader("bytes=50-20", 100)).toBeUndefined();
  });
});

describe.each(["application/pdf", "video/mp4", "video/webm"])("rangedFileResponse (%s)", (mimeType) => {
  it("returns undefined when the request has no Range header", () => {
    const filePath = makeFile("0123456789");
    const request = new Request("wuu-file://local/abc");
    expect(rangedFileResponse(request, filePath, mimeType)).toBeUndefined();
  });

  it("serves the requested bytes with 206 metadata", async () => {
    const filePath = makeFile("0123456789");
    const request = new Request("wuu-file://local/abc", {
      headers: { range: "bytes=2-5" },
    });
    const response = rangedFileResponse(request, filePath, mimeType);
    expect(response).toBeDefined();
    expect(response!.status).toBe(206);
    expect(response!.headers.get("content-range")).toBe("bytes 2-5/10");
    expect(response!.headers.get("content-length")).toBe("4");
    expect(response!.headers.get("content-type")).toBe(mimeType);
    expect(response!.headers.get("accept-ranges")).toBe("bytes");
    expect(response!.headers.get("access-control-allow-origin")).toBe("*");
    await expect(response!.text()).resolves.toBe("2345");
  });

  it("serves a suffix range from the end of the file", async () => {
    const filePath = makeFile("0123456789");
    const request = new Request("wuu-file://local/abc", {
      headers: { range: "bytes=-3" },
    });
    const response = rangedFileResponse(request, filePath, mimeType);
    expect(response!.status).toBe(206);
    expect(response!.headers.get("content-range")).toBe("bytes 7-9/10");
    await expect(response!.text()).resolves.toBe("789");
  });

  it("answers 416 with the total size for an unsatisfiable range", async () => {
    const filePath = makeFile("0123456789");
    const request = new Request("wuu-file://local/abc", {
      headers: { range: "bytes=10-20" },
    });
    const response = rangedFileResponse(request, filePath, mimeType);
    expect(response!.status).toBe(416);
    expect(response!.headers.get("content-range")).toBe("bytes */10");
  });

  it("falls back to a full response for an unparseable range", () => {
    const filePath = makeFile("0123456789");
    const request = new Request("wuu-file://local/abc", {
      headers: { range: "bytes=banana" },
    });
    expect(rangedFileResponse(request, filePath, mimeType)).toBeUndefined();
  });
});
