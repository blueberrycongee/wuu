import { describe, expect, it } from "vitest";
import {
  CODE_HIGHLIGHT_CHAR_LIMIT,
  CODE_HIGHLIGHT_HARD_LIMIT,
  highlightCode,
  shouldHighlightCode,
} from "./WorkspaceCodeHighlight";

describe("code highlight limits", () => {
  it("highlights ordinary blocks and keeps a revealed snapshot of a large one", () => {
    const small = "const value = 1;\n";
    const large = "x".repeat(CODE_HIGHLIGHT_CHAR_LIMIT + 1);
    expect(shouldHighlightCode(small, null)).toBe(true);
    expect(shouldHighlightCode(large, null)).toBe(false);
    expect(shouldHighlightCode(large, large)).toBe(true);
    expect(shouldHighlightCode(`${large}y`, large)).toBe(false);
  });

  it("refuses to highlight a block past the hard limit", () => {
    const huge = "x".repeat(CODE_HIGHLIGHT_HARD_LIMIT + 1);
    expect(shouldHighlightCode(huge, huge)).toBe(false);
    expect(highlightCode("json", huge)).toEqual({ html: huge, language: "plaintext" });
  });

  it("escapes markup when a block is past the hard limit", () => {
    const huge = `${"<script>".repeat(20_000)}&`;
    expect(huge.length).toBeGreaterThan(CODE_HIGHLIGHT_HARD_LIMIT);
    expect(highlightCode("html", huge).html).not.toContain("<script>");
    expect(highlightCode("html", huge).html).toContain("&amp;");
  });
});
