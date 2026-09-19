import { describe, expect, it } from "vitest";
import { fileSelectionNavigation } from "./FileSelectionNavigation";
import { fileSelectionSource } from "./FileSelectionMapping";

describe("file selection navigation", () => {
  const original = "header\r\nconst value = true;\r\nreturn value;\r\n";
  const quote = "value = true;\r\nreturn value";
  const start = original.indexOf(quote);
  const source = fileSelectionSource("/repo", "src/main.ts", original, { start, end: start + quote.length, blockFallback: false })!;

  it("retains the captured range when its revision still matches", () => {
    expect(fileSelectionNavigation(source, original)).toEqual({ path: "/repo/src/main.ts#L2,7-L3,13", locationChanged: false });
  });

  it("relocates a unique unchanged quote after lines are inserted", () => {
    expect(fileSelectionNavigation(source, `intro\r\n${original}`)).toEqual({ path: "/repo/src/main.ts#L3,7-L4,13", locationChanged: false });
    expect(source.start_line).toBe(2);
  });

  it.each([undefined, "new content", `${quote}\n${quote}`])("omits stale coordinates when the current quote is not uniquely available", (text) => {
    expect(fileSelectionNavigation(source, text)).toEqual({ path: "/repo/src/main.ts", locationChanged: true });
  });

  it("treats overlapping quote occurrences as ambiguous", () => {
    expect(fileSelectionNavigation({ ...source, quote: "aaa" }, "aaaa").locationChanged).toBe(true);
  });

  it("keeps absolute paths and Windows workspace roots intact", () => {
    expect(fileSelectionNavigation({ ...source, path: "/other/main.ts" }).path).toBe("/other/main.ts");
    expect(fileSelectionNavigation({ ...source, workspace: "C:\\repo\\", path: "src/main.ts" }).path).toBe("C:\\repo/src/main.ts");
  });

  it("navigates first-line BOM excerpts in editor coordinates without changing the snapshot", () => {
    const text = "\uFEFFalpha beta\nsecond line";
    const captured = fileSelectionSource("/repo", "note.txt", text, { start: 1, end: 6, blockFallback: false })!;
    expect(fileSelectionNavigation(captured, text).path).toBe("/repo/note.txt#L1-L1,6");
    expect(captured.start_column).toBe(2);
    expect(captured.end_column).toBe(7);
    expect(fileSelectionNavigation(captured, `\uFEFFprefix ${text.slice(1)}`).path).toBe("/repo/note.txt#L1,8-L1,13");
    expect(fileSelectionNavigation(captured, `\uFEFFintro\n${text.slice(1)}`).path).toBe("/repo/note.txt#L2-L2,6");
  });
});
