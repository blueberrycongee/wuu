import {
  useCallback,
  useEffect,
  useLayoutEffect,
  useMemo,
  useRef,
  useState
} from "react";
import {
  MarkdownContent,
  type RichTextRenderer,
} from "./RichContent";
import {
  streamTextStore,
  useStreamedTextHasValue,
  useStreamedText
} from "./StreamText";
import { useStreamVeil } from "./StreamVeil";
import { mendStreamingMarkdown } from "./StreamingMarkdownMend";
import {
  useConversationBecameRenderActive,
  useConversationRenderActive,
} from "./ConversationRenderActivity";

/**
 * Progressive Markdown renderer used while assistant text is arriving.
 *
 * Single source of truth: the parent owns `isLive`, derived from the
 * back-end thread item. We do not maintain an internal
 * streaming/settling/settled state machine — `isLive` flips the renderer
 * between two modes:
 *   - `isLive=true`: server-streamed chunks render on the store's coalesced
 *                    cadence; newly arrived ranges play a cadence-adaptive opacity fade
 *                    so variable-size batches read as one continuous reveal,
 *                    with a stable cursor marking the output edge.
 *   - `isLive=false`: text remains rendered in full. The cursor fades out
 *                     and `onSettled` fires once the final snapshot lands.
 *
 * `phase` is accepted so callers can pass the same semantic state they use
 * elsewhere, but typography and streaming affordances stay stable across
 * commentary and final-answer text.
 */
type StreamingMarkdownProps = {
  streamKey: string;
  initialText?: string;
  cwd?: string;
  onOpenFile?: (path: string) => void;
  className?: string;
  /** Whether the source item is still receiving deltas. */
  isLive: boolean;
  /**
   * The thread item phase. It is semantic metadata for the parent layout;
   * this renderer keeps commentary and final-answer text visually identical.
   */
  phase: "commentary" | "final_answer";
  onFrame?: () => void;
  onSettled?: () => void;
};

type StreamPhase = "streaming" | "settled";

const DEFAULT_CLASS_NAME = "streaming-markdown rich-content";
const CURSOR_CLASS_NAME = "stream-cursor";
const CURSOR_BLOCK_TAIL_CLASS_NAME = "stream-cursor-block-tail";
const CURSOR_SENTINEL = "";
const CURSOR_MARKDOWN_BOUNDARY = " ";

export function StreamingMarkdown({
  streamKey,
  initialText = "",
  cwd,
  onOpenFile,
  className = DEFAULT_CLASS_NAME,
  isLive,
  onFrame,
  onSettled
}: StreamingMarkdownProps): JSX.Element {
  /* ------------------------- External store wiring ------------------------ */
  const renderActive = useConversationRenderActive();
  const becameRenderActive = useConversationBecameRenderActive();
  const hasStreamValue = useStreamedTextHasValue(streamKey, renderActive);
  const targetText = useStreamedText(streamKey, initialText, renderActive);

  /* ----------------------------- Sticky text ------------------------------ */
  // The text we actually render. The store may be cleared (in `onSettled`)
  // before the parent unmounts us, so the hook falls back to `initialText`
  // instead of blanking the visible message.
  const [renderedText, setRenderedText] = useState(
    targetText,
  );
  const renderedReplacementVersionRef = useRef(
    streamTextStore.replacementVersion(streamKey),
  );
  const acceptedStreamValueRef = useRef(hasStreamValue);
  // Resolve the sticky snapshot before committing DOM. Publishing in a layout
  // effect lets the parent's scroll restore measure the previous text first.
  if (hasStreamValue) {
    acceptedStreamValueRef.current = true;
    renderedReplacementVersionRef.current = streamTextStore.replacementVersion(streamKey);
  }
  if (targetText !== renderedText && !(acceptedStreamValueRef.current &&
    !hasStreamValue && targetText.length < renderedText.length)) {
    renderedReplacementVersionRef.current = streamTextStore.replacementVersion(streamKey);
    setRenderedText(targetText);
  }

  /* ------------------------------ Phase ----------------------------------- */
  // Single internal phase: streaming while upstream is live, settled once
  // it isn't. The back-end message phase never gates rendering of the text.
  const phase: StreamPhase = isLive ? "streaming" : "settled";

  /* ------------------------------- Refs ---------------------------------- */
  const onFrameRef = useRef(onFrame);
  const onSettledRef = useRef(onSettled);
  const settledNotifiedRef = useRef(false);
  const skipRevealFrameRef = useRef(false);
  if (becameRenderActive) {
    skipRevealFrameRef.current = true;
  }

  /* ----------------------- Refs always track props ------------------------ */
  useLayoutEffect(() => {
    onFrameRef.current = onFrame;
    onSettledRef.current = onSettled;
  }, [onFrame, onSettled]);

  /* -------------------------- Settle notification ------------------------ */
  // Fire `onSettled` once the upstream is no longer live AND the visible
  // cursor has caught up to the target text. The parent uses this to drop
  // any external "live" tracking (we don't manage it ourselves anymore).
  const trySettle = useCallback((): void => {
    if (settledNotifiedRef.current) return;
    settledNotifiedRef.current = true;
    onSettledRef.current?.();
  }, []);

  // The store already coalesces provider deltas to one notification per
  // visual update. Rendering those committed chunks directly avoids a
  // second client-side character chase that used to keep React and Markdown
  // busy for seconds after the provider had already delivered the text.
  useLayoutEffect(() => {
    // Revealing a cached running conversation is not a new visual frame.
    // Catch-up text may commit one render later; keep skipping until that
    // snapshot has landed, or the jump from onFrame re-anchors the pane.
    if (skipRevealFrameRef.current) {
      if (renderedText !== targetText) {
        return;
      }
      skipRevealFrameRef.current = false;
      return;
    }
    if (renderActive) {
      onFrameRef.current?.();
    }
  }, [renderActive, renderedText, targetText]);

  useEffect(() => {
    if (isLive) {
      settledNotifiedRef.current = false;
      return;
    }
    trySettle();
  }, [isLive, trySettle]);

  /* ------------------------- Derived view data -------------------------- */
  const visibleText = renderedText;
  const cursorState = isLive ? "shown" : "fading";
  // The cursor appears for all live assistant text. Commentary and final
  // answers share the same visual treatment so a later phase resolution does
  // not cause a typography or affordance jump.
  // Always render the cursor span so the fold body height stays stable
  // when the cursor fades out. Removing the
  // cursor from DOM shrinks scrollHeight by ~1 line (1.05em), which
  // clamps scrollTop in ConversationScrollState and creates a visible
  // UP shift that combines with the next item's auto-follow re-anchor
  // into a V-shape jitter. Visibility is controlled by the parent
  // data-cursor-state attribute (see turns.css) instead.
  const showCursor = true;
  const surfaceRef = useRef<HTMLDivElement>(null);
  const cursorTextRenderer = useMemo(() => createCursorTextRenderer(), []);
  const stableCursorTextRenderer = cursorTextRenderer;
  // Mermaid is expensive; do not flip the markdown renderer for ordinary
  // text. The diagram renderer is enabled as soon as a Mermaid fence appears
  // (not only after streaming ends) so the open fence in the tail can render
  // progressively offscreen while its source grows; messages without a
  // Mermaid fence never enter the diagram renderer at all.
  const renderMermaid = containsMermaidFence(visibleText);

  // Split the visible text into stable blocks + an open tail. Every
  // stable block is its own memoized markdown surface, so promoting a
  // new block from the tail only costs that single block's parse — the
  // earlier blocks stay mounted as-is. This caps per-tick work at
  // O(tail) and per-promotion work at O(one block), independent of the
  // total answer length.
  //
  // The settled phase uses the same split as streaming: keeping the
  // block layout stable across the streaming → settled transition means
  // React reconciles by updating props in place instead of unmounting
  // every previously memoized block and remounting one big tail. That
  // reconciliation jump is what caused the visible "settle flick" on
  // long answers (block-level memo would be wiped the instant the
  // upstream went idle).
  const split = useIncrementalStableBlocks(
    visibleText,
    streamKey,
    renderedReplacementVersionRef.current,
    streamTextStore.has(streamKey),
  );
  // Keep the synthetic cursor separated from the Markdown source. Appending
  // the private-use sentinel directly after a closing emphasis delimiter can
  // make that delimiter non-right-flanking (for example `。**`), exposing
  // the raw `**` in the rendered message. The text renderer removes this
  // parsing-only boundary before it reaches the DOM.
  //
  // When trailing blank lines promote the visible tail into a stable prose
  // block, keep the cursor inside that last block. The old absolute sibling
  // had no inline anchor and could paint at the top of the message instead of
  // after the last visible character. A closed fence remains the one shape
  // that cannot safely accept the sentinel without creating a paragraph below
  // the code card, so it keeps the zero-flow-height sibling fallback.
  // Repeated blank lines are preserved by the scanner, but must not become
  // empty flex children: each would add a prose gap and could draw the cursor
  // in an otherwise empty paragraph below the visible message.
  const visibleBlocks = useMemo(
    () => split.blocks.filter((block) => block.trim().length > 0),
    [split.blocks],
  );
  useStreamVeil(
    surfaceRef, renderedText, isLive,
    `${streamKey}:${renderedReplacementVersionRef.current}`, visibleBlocks.length, renderActive,
  );
  const lastStableBlockIndex = visibleBlocks.length - 1;
  const lastStableBlock = visibleBlocks[lastStableBlockIndex] ?? "";
  const tailIsEmpty = split.tail.trim().length === 0;
  const cursorStableBlockIndex = showCursor &&
    tailIsEmpty &&
    lastStableBlockIndex >= 0 &&
    !endsWithFenceCloser(lastStableBlock.trimEnd())
      ? lastStableBlockIndex
      : -1;
  // An open Mermaid fence in the tail cannot accept the inline cursor
  // sentinel — it would leak into the diagram source and break parsing — so
  // it uses the zero-flow-height sibling cursor like closed fences do.
  const tailHasOpenMermaidFence =
    split.inFence && split.openFenceLanguage?.toLowerCase() === "mermaid";
  const cursorNeedsBlockTail = showCursor && (
    endsWithFenceCloser(split.tail) ||
    tailHasOpenMermaidFence ||
    (tailIsEmpty && lastStableBlockIndex >= 0 && cursorStableBlockIndex < 0)
  );
  // A cursor after a trailing newline creates an extra line box even when
  // settled CSS hides it. Anchor it to the last visible text in the tail too.
  const displayTail = useMemo(
    () => isLive ? mendStreamingMarkdown(split.tail) : split.tail,
    [isLive, split.tail],
  );
  const tailText = showCursor && !cursorNeedsBlockTail && cursorStableBlockIndex < 0
    ? insertCursorBeforeTrailingWhitespace(displayTail)
    : displayTail;

  /* ------------------------------- Render -------------------------------- */
  return (
    <div
      ref={surfaceRef}
      className={className}
      data-stream-state={phase}
      data-cursor-state={cursorState}
    >
      {visibleBlocks.map((block, index) => (
        // Keep stable blocks keyed separately so settled text does not remount
        // into one large markdown tree when streaming ends.
        <div className="streaming-markdown-block" key={index}>
          <MemoMarkdownContent
            text={
              index === cursorStableBlockIndex
                ? insertCursorBeforeTrailingWhitespace(block)
                : block
            }
            cwd={cwd}
            onOpenFile={onOpenFile}
            renderText={
              index === cursorStableBlockIndex
                ? stableCursorTextRenderer
                : undefined
            }
            renderMermaid={renderMermaid}
          />
        </div>
      ))}
      <MarkdownContent
        text={tailText}
        cwd={cwd}
        onOpenFile={onOpenFile}
        renderText={cursorTextRenderer}
        renderMermaid={renderMermaid}
        mermaidStreaming={isLive && tailHasOpenMermaidFence}
      />
      {cursorNeedsBlockTail ? (
        <span
          className={`${CURSOR_CLASS_NAME} ${CURSOR_BLOCK_TAIL_CLASS_NAME}`}
          aria-hidden="true"
        />
      ) : null}
    </div>
  );
}

/**
 * Memoized markdown surface. Stable blocks are passed in by value;
 * React.memo's default shallow compare on the `text` string is exactly
 * what we want — identical text means identical render.
 */
const MemoMarkdownContent = MarkdownContent;

function createCursorTextRenderer(): RichTextRenderer {
  return (text, keyPrefix) => {
    const cursorIndex = text.indexOf(CURSOR_SENTINEL);
    const textBeforeCursor = cursorIndex >= 0 ? text.slice(0, cursorIndex) : text;
    const visibleText = cursorIndex >= 0 && textBeforeCursor.endsWith(CURSOR_MARKDOWN_BOUNDARY)
      ? textBeforeCursor.slice(0, -CURSOR_MARKDOWN_BOUNDARY.length)
      : textBeforeCursor;
    const output: Array<JSX.Element | string> = visibleText ? [visibleText] : [];

    if (cursorIndex >= 0) {
      output.push(
        <span
          key={`${keyPrefix}-cursor`}
          className={CURSOR_CLASS_NAME}
          aria-hidden="true"
        />
      );
      const trailingText = text.slice(cursorIndex + CURSOR_SENTINEL.length);
      if (trailingText) {
        output.push(trailingText);
      }
    }
    return output;
  };
}

export function containsMermaidFence(text: string): boolean {
  return /(^|\n)```[ \t]*mermaid[ \t]*\r?\n/i.test(text);
}

function endsWithFenceCloser(text: string): boolean {
  // Cursor insertion trims trailing whitespace too. Inspect the same boundary
  // or a final newline can place the sentinel on the closing fence itself.
  text = text.trimEnd();
  if (!text) {
    return false;
  }

  // Most snapshots do not end on a fence marker. Check only the final line
  // first so ordinary prose does not allocate and scan every preceding line.
  const finalLineStart = text.lastIndexOf("\n") + 1;
  const finalLine = text.slice(finalLineStart);
  const finalMatch = /^ {0,3}(`{3,}|~{3,})(.*)$/.exec(finalLine);
  if (!finalMatch || finalMatch[2].trim() !== "") {
    return false;
  }

  const finalMarker = finalMatch[1][0] as "`" | "~";
  const finalMarkerLength = finalMatch[1].length;
  let activeFence: { marker: "`" | "~"; length: number } | undefined;
  const precedingLines = text.slice(0, finalLineStart).split("\n");
  for (const line of precedingLines) {
    const match = /^ {0,3}(`{3,}|~{3,})(.*)$/.exec(line);
    if (!match) {
      continue;
    }

    const marker = match[1][0] as "`" | "~";
    if (!activeFence) {
      if (marker === "`" && match[2].includes("`")) {
        continue;
      }
      activeFence = { marker, length: match[1].length };
      continue;
    }

    const isCloser =
      marker === activeFence.marker &&
      match[1].length >= activeFence.length &&
      match[2].trim() === "";
    if (!isCloser) {
      continue;
    }
    activeFence = undefined;
  }
  return Boolean(
    activeFence &&
    activeFence.marker === finalMarker &&
    finalMarkerLength >= activeFence.length,
  );
}

function insertCursorBeforeTrailingWhitespace(text: string): string {
  const trailingWhitespace = text.match(/\s*$/)?.[0] ?? "";
  const visibleEnd = text.length - trailingWhitespace.length;
  return `${text.slice(0, visibleEnd)}${CURSOR_MARKDOWN_BOUNDARY}${CURSOR_SENTINEL}${trailingWhitespace}`;
}

type StableBlockSplit = { blocks: string[]; tail: string };

type StableBlockScanState = StableBlockSplit & {
  sourceKey: string;
  replacementVersion: number;
  textLength: number;
  scanOffset: number;
  blockStart: number;
  inFence: boolean;
  /** A list stays open across blank lines until a dedented block arrives. */
  listContentIndent: number | undefined;
  pendingListBoundary: number | undefined;
  /** Info string of the currently open fenced code block, if any. */
  openFenceLanguage: string | undefined;
};

function useIncrementalStableBlocks(
  text: string,
  sourceKey: string,
  replacementVersion: number,
  appendOnly: boolean,
): StableBlockScanState {
  const committedScanRef = useRef<StableBlockScanState | undefined>(undefined);
  const scan = useMemo(() => {
    const previous = committedScanRef.current;
    const canExtend =
      appendOnly &&
      previous?.sourceKey === sourceKey &&
      previous.replacementVersion === replacementVersion &&
      text.length >= previous.textLength;
    return scanStableBlocks(
      text,
      sourceKey,
      replacementVersion,
      canExtend ? previous : undefined,
    );
  }, [appendOnly, replacementVersion, sourceKey, text]);

  useLayoutEffect(() => {
    committedScanRef.current = scan;
  }, [scan]);

  return scan;
}

function scanStableBlocks(
  text: string,
  sourceKey = "",
  replacementVersion = 0,
  previous?: StableBlockScanState,
): StableBlockScanState {
  let blocks = previous?.blocks ?? [];
  const previousBlocks = blocks;
  let blocksCopied = false;
  let inFence = previous?.inFence ?? false;
  let openFenceLanguage = previous?.openFenceLanguage;
  let blockStart = previous?.blockStart ?? 0;
  let scanOffset = previous?.scanOffset ?? 0;
  let listContentIndent = previous?.listContentIndent;
  let pendingListBoundary = previous?.pendingListBoundary;
  const commitBlock = (end: number): void => {
    if (!blocksCopied && blocks === previousBlocks && previous) {
      blocks = [...blocks];
      blocksCopied = true;
    }
    blocks.push(text.slice(blockStart, end));
    blockStart = end;
  };

  // Only complete lines are scanned. The final partial line may still grow
  // into a fence opener/closer, so deferring it avoids rescanning or rolling
  // back parser state when the next provider chunk arrives.
  for (;;) {
    const lineEnd = text.indexOf("\n", scanOffset);
    if (lineEnd < 0) {
      break;
    }
    const lineStart = scanOffset;
    const line = text.slice(lineStart, lineEnd);
    const blank = line.trim().length === 0;
    const listMarker = !inFence && line.match(/^ {0,3}(?:[-+*]|\d{1,9}[.)])(?:[ \t]+|$)/);
    if (!inFence && !blank) {
      if (pendingListBoundary !== undefined) {
        const indentation = markdownColumnWidth(line.match(/^[ \t]*/)?.[0] ?? "");
        if (!listMarker && indentation < (listContentIndent ?? 0)) {
          commitBlock(pendingListBoundary);
          listContentIndent = undefined;
        }
        pendingListBoundary = undefined;
      }
      if (listMarker) {
        const indent = markdownColumnWidth(listMarker[0]);
        listContentIndent = Math.min(listContentIndent ?? indent, indent);
      }
    }
    let fenceStart = lineStart;
    // CommonMark allows fenced code blocks to be indented by up to three
    // spaces. The Markdown parser accepts that form, so this lightweight
    // boundary scanner must do the same or a blank line inside the fence gets
    // promoted into a stable-block boundary.
    while (
      fenceStart < lineEnd &&
      fenceStart - lineStart < 3 &&
      text.charCodeAt(fenceStart) === 32 /* space */
    ) {
      fenceStart += 1;
    }
    const startsBacktickFence =
      lineEnd - fenceStart >= 3 &&
      text.charCodeAt(fenceStart) === 96 &&
      text.charCodeAt(fenceStart + 1) === 96 &&
      text.charCodeAt(fenceStart + 2) === 96;
    if (startsBacktickFence) {
      if (!inFence) {
        inFence = true;
        openFenceLanguage = text.slice(fenceStart + 3, lineEnd).trim();
      } else {
        let isCloser = true;
        for (let index = fenceStart + 3; index < lineEnd; index += 1) {
          const code = text.charCodeAt(index);
          if (code !== 32 /* space */ && code !== 9 /* tab */) {
            isCloser = false;
            break;
          }
        }
        if (isCloser) {
          inFence = false;
          openFenceLanguage = undefined;
        }
      }
    }

    scanOffset = lineEnd + 1;
    if (!inFence && blank) {
      if (listContentIndent !== undefined) {
        // A later indented paragraph or another item can still belong to the
        // same list. Wait for a complete, dedented line before freezing it.
        pendingListBoundary = scanOffset;
      } else {
        commitBlock(scanOffset);
      }
    }
  }

  return {
    sourceKey,
    replacementVersion,
    textLength: text.length,
    scanOffset,
    blockStart,
    inFence,
    listContentIndent,
    pendingListBoundary,
    openFenceLanguage,
    blocks,
    tail: text.slice(blockStart),
  };
}

function markdownColumnWidth(text: string): number {
  let column = 0;
  for (const character of text) {
    column += character === "\t" ? 4 - column % 4 : 1;
  }
  return column;
}

/**
 * Split `text` into a sequence of "stable" markdown blocks plus an
 * open tail. A block is everything between two blank-line boundaries
 * (`\n\n`). Open fences and lists with a possible indented continuation
 * remain in the tail until their boundary is known.
 *
 * Each stable block has self-contained markdown semantics: prepending
 * or appending more text to the overall document cannot change how the
 * block parses. That property is what makes block-level memoization
 * safe.
 *
 * Exported for tests.
 */
export function splitIntoStableBlocks(
  text: string
): StableBlockSplit {
  const { blocks, tail } = scanStableBlocks(text);
  return { blocks, tail };
}
