import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { MarkdownContent } from "./RichContent";
import { fileSelectionLineIndex, fileSelectionModelRange, fileSelectionRevision, fileSelectionSource, findFileSelectionDOMAnchor, mapFileDOMSelection } from "./FileSelectionMapping";

let container: HTMLDivElement;
let root: Root;
beforeEach(() => { container = document.createElement("div"); document.body.append(container); root = createRoot(container); });
afterEach(() => { act(() => root.unmount()); container.remove(); });

function render(text: string) { act(() => root.render(<MarkdownContent text={text} sourceMapping />)); }
function select(element: Node, start: number, end: number) {
  const range = document.createRange(); range.setStart(element, start); range.setEnd(element, end); return range;
}

describe("file source mapping", () => {
  it("maps repeated strong/link text using AST offsets, including custom link components", () => {
    const text = "repeat **repeat** and [repeat](https://example.com)\n\nrepeat";
    render(text);
    const strong = container.querySelector("strong span")!.firstChild!;
    const link = container.querySelector("a span[data-file-source-text]")!.firstChild!;
    const strongRange = mapFileDOMSelection(container, select(strong, 1, 5), text)!;
    const linkRange = mapFileDOMSelection(container, select(link, 1, 5), text)!;
    expect(strongRange).toEqual({ start: 10, end: 14, blockFallback: false });
    expect(linkRange).toEqual({ start: 24, end: 28, blockFallback: false });
    const across = document.createRange(); across.setStart(strong, 1); across.setEnd(link, 5);
    const source = fileSelectionSource("/repo", "note.md", text, mapFileDOMSelection(container, across, text)!)!;
    expect(source.quote).toBe("epeat** and [repea");
    expect(source.start_column).toBe(11);
    expect(source.end_column).toBe(29);
    expect(findFileSelectionDOMAnchor(container, text, source)?.range?.toString()).toBe("epeat and repea");
  });

  it("reports block fallback for entities and transformed highlighted code without searching", () => {
    const text = "A &amp; B\n\n```ts\nconst answer = 42;\n```";
    render(text);
    const leaf = container.querySelector("p span")!.firstChild!;
    const entity = mapFileDOMSelection(container, select(leaf, 2, 3), text)!;
    expect(entity.blockFallback).toBe(true);
    expect(fileSelectionSource("/repo", "note.md", text, entity)?.quote).toBe("A &amp; B");
    const code = container.querySelector("pre code")!;
    const range = document.createRange(); range.selectNodeContents(code);
    const mapped = mapFileDOMSelection(container, range, text)!;
    expect(mapped.blockFallback).toBe(true);
    expect(fileSelectionSource("/repo", "note.md", text, mapped)?.quote).toBe("```ts\nconst answer = 42;\n```");
    expect(code.textContent).toBe("const answer = 42;\n");
  });

  it("preserves UTF-16 columns, CRLF bytes and end-exclusive ranges", () => {
    const text = "前😀\r\nsecond\r\nlast";
    const source = fileSelectionSource("/repo", "note.txt", text, { start: 1, end: 9, blockFallback: false })!;
    expect(source).toMatchObject({ quote: "😀\r\nseco", start_line: 1, start_column: 2, end_line: 2, end_column: 5 });
    expect(source.revision).toBe(fileSelectionRevision(text));
    expect(fileSelectionRevision(text + "!")).not.toBe(source.revision);
    expect(fileSelectionSource("/repo", "note.txt", text, { start: 8, end: 8, blockFallback: false })).toBeNull();
  });

  it("maps Monaco's normalized coordinates back to mixed line endings and a hidden BOM", () => {
    const text = "\uFEFFfirst\r\nsecond\nthird\rfourth";
    const modelRange = { startLineNumber: 1, startColumn: 1, endLineNumber: 4, endColumn: 3 };
    const index = fileSelectionLineIndex(text);
    const range = fileSelectionModelRange(text, modelRange, index)!;
    const source = fileSelectionSource("/repo", "mixed.txt", text, range, fileSelectionRevision(text), index)!;
    expect(range).toEqual(fileSelectionModelRange(text, modelRange));
    expect(source).toEqual(fileSelectionSource("/repo", "mixed.txt", text, range));
    expect(source.quote).toBe("first\r\nsecond\nthird\rfo");
    expect(source).toMatchObject({ start_line: 1, start_column: 2, end_line: 4, end_column: 3 });
    expect(fileSelectionModelRange(text, { ...modelRange, endLineNumber: 2, endColumn: 20 }, index)).toBeNull();
  });

  it("rejects selections crossing outside the preview and unmapped controls", () => {
    render("hello **world**");
    const outside = document.createTextNode("outside"); document.body.append(outside);
    const range = document.createRange(); range.setStart(container.querySelector("p span")!.firstChild!, 0); range.setEnd(outside, 2);
    expect(mapFileDOMSelection(container, range, "hello **world**")).toBeNull();
    outside.remove();
    render("```ts\nconst a = 1\n```");
    range.selectNodeContents(container.querySelector("button")!);
    expect(mapFileDOMSelection(container, range, "```ts\nconst a = 1\n```")).toBeNull();
  });
});
