import type { FileSelectionSource } from "../shared/protocol";
export type { FileSelectionSource } from "../shared/protocol";

export type FileSelectionRange = {
  start: number;
  end: number;
  blockFallback: boolean;
};

export type FileSelectionLineIndex = { starts: number[]; ends: number[] };

/** Build once per file revision so cursor movement never rescans earlier lines. */
export function fileSelectionLineIndex(text: string): FileSelectionLineIndex {
  const starts = [text.startsWith("\uFEFF") ? 1 : 0];
  const ends: number[] = [];
  for (let index = starts[0]; index < text.length; index++) {
    const code = text.charCodeAt(index);
    if (code !== 10 && code !== 13) continue;
    ends.push(index);
    if (code === 13 && text.charCodeAt(index + 1) === 10) index++;
    starts.push(index + 1);
  }
  ends.push(text.length);
  return { starts, ends };
}

/** Monaco normalizes mixed line endings and hides a leading BOM. Map its
 * coordinates against the original source so quoted bytes remain unchanged. */
export function fileSelectionModelRange(text: string, range: {
  startLineNumber: number; startColumn: number; endLineNumber: number; endColumn: number;
}, index = fileSelectionLineIndex(text)): FileSelectionRange | null {
  const offset = (line: number, column: number) => {
    const start = index.starts[line - 1];
    const end = index.ends[line - 1];
    if (start === undefined || end === undefined || !Number.isInteger(column) || column < 1 || column > end - start + 1) return null;
    return start + column - 1;
  };
  const start = offset(range.startLineNumber, range.startColumn);
  const end = offset(range.endLineNumber, range.endColumn);
  return start !== null && end !== null && start < end && end <= text.length ? { start, end, blockFallback: false } : null;
}

/** Opaque content identity, not a cryptographic integrity or trust check. */
export function fileSelectionRevision(text: string): string {
  let first = 2166136261;
  let second = 5381;
  for (let index = 0; index < text.length; index++) {
    const code = text.charCodeAt(index);
    first = Math.imul(first ^ code, 16777619);
    second = Math.imul(second, 33) ^ code;
  }
  return `text-v1:${text.length}:${(first >>> 0).toString(16)}:${(second >>> 0).toString(16)}`;
}

export function fileSelectionSource(
  workspace: string, path: string, text: string, range: FileSelectionRange,
  revision = fileSelectionRevision(text),
  index = fileSelectionLineIndex(text),
): FileSelectionSource | null {
  const { start, end } = range;
  if (!Number.isInteger(start) || !Number.isInteger(end) || start < 0 || end > text.length || start >= end) return null;
  const position = (offset: number) => {
    let low = 0;
    let high = index.starts.length;
    while (low < high) {
      const middle = (low + high) >>> 1;
      if (index.starts[middle] <= offset) low = middle + 1;
      else high = middle;
    }
    const lineIndex = Math.max(0, low - 1);
    if (text[offset - 1] === "\r" && text[offset] === "\n") return { line: lineIndex + 2, column: 1 };
    return { line: lineIndex + 1, column: offset - index.starts[lineIndex] + 1 + (lineIndex === 0 && text.startsWith("\uFEFF") ? 1 : 0) };
  };
  const from = position(start);
  const to = position(end);
  return {
    workspace, path, start_line: from.line, start_column: from.column,
    end_line: to.line, end_column: to.column, quote: text.slice(start, end), revision,
  };
}

export type SelectionASTNode = {
  type?: string;
  tagName?: string;
  value?: string;
  properties?: Record<string, unknown>;
  children?: SelectionASTNode[];
  position?: { start: { offset?: number }; end: { offset?: number } };
};

/** Mark positions from the parser, never inferred by searching repeated prose. */
export function rehypeFileSelectionMapping() {
  return (tree: SelectionASTNode): void => {
    const visit = (node: SelectionASTNode, code = false): void => {
      const start = node.position?.start.offset;
      const end = node.position?.end.offset;
      if (node.type === "element") {
        node.properties = { ...node.properties };
        for (const key of Object.keys(node.properties)) {
          if (/^data-?file-?source/i.test(key)) delete node.properties[key];
        }
        if (start !== undefined && end !== undefined) {
          node.properties["data-file-source-start"] = start;
          node.properties["data-file-source-end"] = end;
        }
      }
      const inCode = code || node.tagName === "code" || node.tagName === "pre";
      node.children = node.children?.map(child => {
        if (!inCode && child.type === "text" && child.position?.start.offset !== undefined && child.position.end.offset !== undefined) {
          return {
            type: "element", tagName: "span",
            properties: {
              "data-file-source-start": child.position.start.offset,
              "data-file-source-end": child.position.end.offset,
              "data-file-source-text": "true",
            },
            children: [child],
          };
        }
        visit(child, inCode);
        return child;
      });
    };
    visit(tree);
  };
}

function endpoint(root: HTMLElement, node: Node, offset: number, text: string, end: boolean): { offset: number; fallback: boolean } | null {
  // Browser selections can terminate between elements rather than inside text.
  if (node.nodeType === Node.ELEMENT_NODE && node.childNodes.length) {
    const child = node.childNodes[end ? offset - 1 : offset];
    if (child) {
      node = child;
      while (node.childNodes.length) node = end ? node.lastChild! : node.firstChild!;
      offset = end ? (node.textContent?.length ?? 0) : 0;
    }
  }
  const element = node.nodeType === Node.ELEMENT_NODE ? node as Element : node.parentElement;
  if (element?.closest("button, input, textarea, [data-file-selection-ignore]")) return null;
  const mapped = element?.closest<HTMLElement>("[data-file-source-start][data-file-source-end]");
  if (!mapped || !root.contains(mapped)) return null;
  const start = Number(mapped.dataset.fileSourceStart);
  const stop = Number(mapped.dataset.fileSourceEnd);
  if (!Number.isInteger(start) || !Number.isInteger(stop) || start < 0 || stop > text.length || start > stop) return null;
  if (mapped.dataset.fileSourceText === "true" && mapped.textContent === text.slice(start, stop)) {
    const prefix = document.createRange();
    prefix.selectNodeContents(mapped);
    prefix.setEnd(node, offset);
    return { offset: start + prefix.toString().length, fallback: false };
  }
  return { offset: end ? stop : start, fallback: true };
}

export function mapFileDOMSelection(root: HTMLElement, range: Range, text: string): FileSelectionRange | null {
  if (range.collapsed || !root.contains(range.startContainer) || !root.contains(range.endContainer)) return null;
  const from = endpoint(root, range.startContainer, range.startOffset, text, false);
  const to = endpoint(root, range.endContainer, range.endOffset, text, true);
  if (!from || !to || from.offset >= to.offset) return null;
  return { start: from.offset, end: to.offset, blockFallback: from.fallback || to.fallback };
}

/** Locate only the captured coordinates in the matching revision. */
export function fileSelectionOffsets(text: string, source: FileSelectionSource): { start: number; end: number } | null {
  const lines = text.split(/\r\n|\r|\n/);
  const breaks = Array.from(text.matchAll(/\r\n|\r|\n/g));
  const offset = (line: number, column: number) => {
    if (!Number.isInteger(line) || !Number.isInteger(column) || line < 1 || line > lines.length || column < 1 || column > lines[line - 1].length + 1) return null;
    const previous = breaks[line - 2];
    return (line === 1 ? 0 : previous.index! + previous[0].length) + column - 1;
  };
  const start = offset(source.start_line, source.start_column);
  const end = offset(source.end_line, source.end_column);
  return start !== null && end !== null && start < end && text.slice(start, end) === source.quote ? { start, end } : null;
}

export function findFileSelectionDOMAnchor(root: HTMLElement, text: string, source: FileSelectionSource): { element: HTMLElement; range?: Range } | null {
  const offsets = fileSelectionOffsets(text, source);
  if (!offsets) return null;
  const elements = Array.from(root.querySelectorAll<HTMLElement>("[data-file-source-start][data-file-source-end]"));
  const exact = elements.filter(element => element.dataset.fileSourceText === "true"
    && element.textContent === text.slice(Number(element.dataset.fileSourceStart), Number(element.dataset.fileSourceEnd)));
  const first = exact.find(element => Number(element.dataset.fileSourceStart) <= offsets.start && Number(element.dataset.fileSourceEnd) > offsets.start);
  const last = exact.find(element => Number(element.dataset.fileSourceStart) < offsets.end && Number(element.dataset.fileSourceEnd) >= offsets.end);
  if (first?.firstChild && last?.firstChild) {
    const range = document.createRange();
    range.setStart(first.firstChild, offsets.start - Number(first.dataset.fileSourceStart));
    range.setEnd(last.firstChild, offsets.end - Number(last.dataset.fileSourceStart));
    return { element: first, range };
  }
  const enclosing = elements.filter(element => Number(element.dataset.fileSourceStart) <= offsets.start && Number(element.dataset.fileSourceEnd) >= offsets.end)
    .sort((a, b) => (Number(a.dataset.fileSourceEnd) - Number(a.dataset.fileSourceStart)) - (Number(b.dataset.fileSourceEnd) - Number(b.dataset.fileSourceStart)))[0];
  return enclosing ? { element: enclosing } : null;
}
