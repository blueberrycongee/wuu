import { describe, expect, it } from "vitest";
import { conversationSearchVisibleSnippet } from "./ConversationSearchDisplay";

describe("conversationSearchVisibleSnippet", () => {
  it("keeps a late Chinese match near the start without splitting emoji", () => {
    const result = conversationSearchVisibleSnippet({
      query: "引导 页面",
      snippet: "旧上下文😀".repeat(60) + "引导\n页面需要调整 " + "后文 ".repeat(200),
      title: "Design",
    });
    expect(result).toContain("引导 页面需要调整");
    expect(Array.from(result.slice(0, result.indexOf("引导"))).length).toBeLessThanOrEqual(17);
    expect(result.length).toBeLessThan(180);
    expect(result).not.toMatch(/(?<![\uD800-\uDBFF])[\uDC00-\uDFFF]|[\uD800-\uDBFF](?![\uDC00-\uDFFF])/u);
  });

  it("hides snippets in the recent conversation list", () => {
    expect(
      conversationSearchVisibleSnippet({
        query: "",
        snippet: "Optimize permission menu",
        title: "Optimize permission menu",
      }),
    ).toBe("");
  });

  it("hides a search snippet when it repeats the title", () => {
    expect(
      conversationSearchVisibleSnippet({
        query: "permission",
        snippet: "  Optimize   permission menu  ",
        title: "optimize permission menu",
      }),
    ).toBe("");
  });

  it("shows a search snippet when it adds context beyond the title", () => {
    expect(
      conversationSearchVisibleSnippet({
        query: "permission",
        snippet: "Menu should stay vertical, with one compact row per mode.",
        title: "Optimize permission menu",
      }),
    ).toBe("Menu should stay vertical, with one compact row per mode.");
  });

  it("caps a long search snippet at the source so the right pane stays compact", () => {
    // A 1k-character snippet simulates the leading window the search
    // backend returns for an agent's multi-section markdown reply —
    // far longer than the right pane can show. The cap should cut at
    // the last whitespace inside the limit and append an ellipsis.
    const longSnippet = "word ".repeat(200);
    const result = conversationSearchVisibleSnippet({
      query: "permission",
      snippet: longSnippet,
      title: "Different title",
    });
    expect(result.endsWith("…")).toBe(true);
    expect(result.length).toBeLessThanOrEqual(280);
  });

  it("hard-slices a long snippet when there is no whitespace to break on", () => {
    // A single 500-char token (think a base64 blob or a URL with no
    // separators) cannot be split at a word boundary, so the cap
    // falls back to a hard slice at the limit.
    const longSnippet = "a".repeat(500);
    const result = conversationSearchVisibleSnippet({
      query: "permission",
      snippet: longSnippet,
      title: "Different title",
    });
    expect(result.endsWith("…")).toBe(true);
    expect(result.length).toBeLessThanOrEqual(281);
  });
});
