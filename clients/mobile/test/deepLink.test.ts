// Deep-link URL parsing. The mobile app registers `wuu://` as a scheme (see
// app.json) and the host's push payload carries a `data.url` of the same
// shape. Parsing must be permissive on input (null, garbage, foreign
// schemes) and exact on output (only the supported routes resolve).

import { describe, expect, it } from "vitest";

import { parseDeepLink } from "../src/lib/deepLink";

describe("parseDeepLink", () => {
  it("rejects null, undefined, and empty input", () => {
    expect(parseDeepLink(null)).toBeNull();
    expect(parseDeepLink(undefined)).toBeNull();
    expect(parseDeepLink("")).toBeNull();
  });

  it("rejects foreign schemes and truly malformed input", () => {
    expect(parseDeepLink("https://example.com/thread/abc")).toBeNull();
    expect(parseDeepLink("not a url at all")).toBeNull();
  });

  it("treats wuu:thread/<id> (single-colon form) the same as wuu://thread/<id>", () => {
    // WHATWG URL parses `wuu:thread/abc` as a non-special scheme; the
    // resulting URL has empty host + pathname "thread/abc". Our parser
    // intentionally accepts both forms because iOS / Android deliver
    // either shape to the app.
    expect(parseDeepLink("wuu:thread/abc")).toEqual({
      kind: "thread",
      threadId: "abc",
    });
  });

  it("parses wuu://thread/<id> (host-style)", () => {
    expect(parseDeepLink("wuu://thread/abc123")).toEqual({
      kind: "thread",
      threadId: "abc123",
    });
  });

  it("parses wuu:///thread/<id> (path-style)", () => {
    expect(parseDeepLink("wuu:///thread/abc123")).toEqual({
      kind: "thread",
      threadId: "abc123",
    });
  });

  it("treats wuu://chat/<id> as an alias for thread", () => {
    expect(parseDeepLink("wuu://chat/abc123")).toEqual({
      kind: "thread",
      threadId: "abc123",
    });
  });

  it("parses wuu://home as a return-to-list deep link", () => {
    expect(parseDeepLink("wuu://home")).toEqual({ kind: "home" });
  });

  it("rejects thread with empty id", () => {
    expect(parseDeepLink("wuu://thread/")).toBeNull();
  });

  it("rejects unknown routes", () => {
    expect(parseDeepLink("wuu://settings/123")).toBeNull();
  });
});
