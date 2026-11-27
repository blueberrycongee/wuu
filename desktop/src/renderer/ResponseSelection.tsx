import { createContext, useContext, useEffect, useLayoutEffect, useRef, useState, type ComponentPropsWithoutRef, type CSSProperties } from "react";
import { createPortal } from "react-dom";
import type { ApplicationServiceV1 } from "../../../packages/plugin-sdk/src/ui";
import type { ResponseSelection } from "../shared/protocol";
import { SelectionActionMenu } from "./SelectionActionMenu";
import { selectionActionMenuMetrics, selectionActionMenuPosition } from "./SelectionActionMenuPosition";
import { showToast } from "./Toast";
import { translateCurrent, useI18n } from "./i18n";
import { syncConversationRenderWindow } from "./ConversationRenderWindow";
import { requestConversationTurnReveal } from "./TurnViewHelpers";
import "./ResponseSelection.css";

export const SideConversationAvailableContext = createContext(false);
type SelectionHistory = {
  application: Pick<ApplicationServiceV1, "getSnapshot" | "invoke">;
  pauseFollowing: (threadId: string) => void;
};
export const ResponseSelectionHistoryContext = createContext<SelectionHistory | undefined>(undefined);

const SOURCE_HIGHLIGHT_DURATION_MS = 1600;
const SOURCE_LAYOUT_ATTEMPTS = 10;
const SOURCE_HISTORY_PAGES = 12;

let sourceHighlight: { root: HTMLElement; clear: () => void } | undefined;

function visible(element: HTMLElement): boolean {
  if (!element.isConnected || element.closest('[hidden], [inert], [aria-hidden="true"]')) return false;
  for (let node: HTMLElement | null = element; node; node = node.parentElement) {
    const style = getComputedStyle(node);
    if (style.display === "none" || style.visibility === "hidden") return false;
  }
  return element.getClientRects().length > 0;
}

function textNodes(root: Node): Text[] {
  const walker = document.createTreeWalker(root, NodeFilter.SHOW_TEXT);
  const nodes: Text[] = [];
  while (walker.nextNode()) nodes.push(walker.currentNode as Text);
  return nodes;
}

function textOffset(root: Node, container: Node, offset: number): number {
  const boundary = document.createRange();
  boundary.setStart(container, offset);
  boundary.collapse(true);
  const walker = document.createTreeWalker(root, NodeFilter.SHOW_TEXT);
  let total = 0;
  while (walker.nextNode()) {
    const node = walker.currentNode as Text;
    if (node === container) return total + offset;
    if (boundary.comparePoint(node, node.length) > 0) return total;
    total += node.length;
  }
  return total;
}

function validatedRange(root: HTMLElement, selection: ResponseSelection): Range | undefined {
  const { start_offset: start, end_offset: end } = selection.source;
  if (!Number.isInteger(start) || !Number.isInteger(end) || start < 0 || end <= start || end > (root.textContent?.length ?? 0)
    || root.textContent?.slice(start, end) !== (selection.source.range_text ?? selection.text)) return;
  const range = document.createRange();
  let offset = 0;
  let started = false;
  for (const node of textNodes(root)) {
    const next = offset + node.length;
    if (!started && start < next) {
      range.setStart(node, start - offset);
      started = true;
    }
    if (started && end <= next) {
      range.setEnd(node, end - offset);
      return range;
    }
    offset = next;
  }
}

function findResponseSource(selection: ResponseSelection): HTMLElement | undefined {
  const { item_id, turn_id, thread_id } = selection.source;
  return Array.from(document.querySelectorAll<HTMLElement>("[data-response-item-id]")).find(article =>
    article.dataset.responseItemId === item_id && article.dataset.responseTurnId === turn_id &&
    article.closest<HTMLElement>("[data-thread-id]")?.dataset.threadId === thread_id &&
    !article.closest(".turn-process-fold.collapsed") && visible(article));
}

function showResponseSource(article: HTMLElement, selection: ResponseSelection, history?: SelectionHistory): boolean {
  const root = article.querySelector<HTMLElement>(".agent-text");
  const range = root && validatedRange(root, selection);
  if (!root || !range) return false;

  history?.pauseFollowing(selection.source.thread_id);
  sourceHighlight?.clear();
  const highlights = globalThis.CSS?.highlights;
  const useCustomHighlight = Boolean(highlights && globalThis.Highlight);
  if (useCustomHighlight) {
    highlights!.set("wuu-response-source", new Highlight(range));
  } else {
    window.getSelection()?.removeAllRanges();
    window.getSelection()?.addRange(range);
  }
  const observer = new MutationObserver((records) => {
    if (records.some(record => record.type !== "attributes") || !visible(article)
      || article.dataset.responseItemId !== selection.source.item_id
      || article.dataset.responseTurnId !== selection.source.turn_id
      || article.closest<HTMLElement>("[data-thread-id]")?.dataset.threadId !== selection.source.thread_id) clear();
  });
  function clear(): void {
    window.clearTimeout(timeout);
    observer.disconnect();
    if (useCustomHighlight) highlights!.delete("wuu-response-source");
    else if (window.getSelection()?.rangeCount === 1 && window.getSelection()?.getRangeAt(0) === range) window.getSelection()?.removeAllRanges();
    sourceHighlight = undefined;
  }
  const timeout = window.setTimeout(clear, SOURCE_HIGHLIGHT_DURATION_MS);
  observer.observe(root, { childList: true, characterData: true, subtree: true });
  for (let node: HTMLElement | null = article; node; node = node.parentElement) {
    observer.observe(node, { attributes: true, attributeFilter: ["hidden", "inert", "aria-hidden", "style", "class", "data-thread-id", "data-response-item-id", "data-response-turn-id"] });
  }
  sourceHighlight = { root, clear };

  const turn = article.closest<HTMLElement>(".turn");
  const priorVisibility = turn?.style.contentVisibility ?? "";
  if (turn) turn.style.contentVisibility = "visible";
  const beginning = range.cloneRange();
  beginning.collapse(true);
  const viewport = article.closest<HTMLElement>(".conversation-split-body, .scroll-region");
  for (let node = range.startContainer.parentElement; node; node = node.parentElement) {
    const style = getComputedStyle(node);
    const bounds = node.getBoundingClientRect();
    const start = beginning.getBoundingClientRect();
    const top = /(auto|scroll)/.test(style.overflowY) && node.scrollHeight > node.clientHeight
      ? start.top - bounds.top - node.clientHeight / 2 : 0;
    const left = /(auto|scroll)/.test(style.overflowX) && node.scrollWidth > node.clientWidth
      && (start.left < bounds.left || start.right > bounds.right) ? start.left - bounds.left - 16 : 0;
    if (top || left) node.scrollBy({ top, left, behavior: "instant" });
    if (node === viewport) break;
  }
  if (viewport) syncConversationRenderWindow(viewport);
  else range.startContainer.parentElement?.scrollIntoView({ block: "center", behavior: "instant" });
  if (turn) turn.style.contentVisibility = priorVisibility;
  article.focus({ preventScroll: true });
  return true;
}

export async function navigateToResponseSelection(
  selection: ResponseSelection,
  history: SelectionHistory | undefined,
  signal: AbortSignal,
): Promise<boolean> {
  if (signal.aborted) return false;
  const { thread_id: threadId, turn_id: turnID, item_id: itemID } = selection.source;
  let article = findResponseSource(selection);
  const application = history?.application;
  if (!article) {
    try {
      const cursors = new Set<string>();
      for (let page = 0; application && page < SOURCE_HISTORY_PAGES && !signal.aborted; page++) {
        const { conversationWorkspace: workspace } = application.getSnapshot();
        if (workspace.primaryThreadId !== threadId && workspace.secondaryThreadId !== threadId) break;
        const thread = workspace.threads.find(thread => thread.id === threadId);
        const cursor = thread?.history_cursor;
        if (thread?.turns.some(turn => turn.id === turnID) || !cursor || cursors.has(cursor)) break;
        cursors.add(cursor);
        await application.invoke("conversation.load-earlier", { threadId, cursor }, { signal });
      }
      if (signal.aborted) return false;
      // The reveal event expands local turn windows and folded response fragments.
      requestConversationTurnReveal(turnID, itemID);
      for (let attempt = 0; attempt < SOURCE_LAYOUT_ATTEMPTS && !signal.aborted; attempt++) {
        await new Promise(resolve => window.setTimeout(resolve, 32));
        if ((article = findResponseSource(selection))) break;
      }
    } catch {
      if (signal.aborted) return false;
    }
  }
  if (signal.aborted) return false;
  if (article && showResponseSource(article, selection, history)) return true;
  showToast({ message: translateCurrent("responseSelection.unavailable") });
  return false;
}

export function AssistantResponseArticle({ turnID, itemID, children, ...props }: ComponentPropsWithoutRef<"article"> & {
  turnID: string; itemID: string;
}): JSX.Element {
  const ref = useRef<HTMLElement>(null);
  const toolbarRef = useRef<HTMLDivElement>(null);
  const commentRef = useRef<HTMLTextAreaElement>(null);
  const commentToggleRef = useRef<HTMLButtonElement>(null);
  const returnToToggleRef = useRef(false);
  const anchorRef = useRef<DOMRect | undefined>(undefined);
  const selectionHighlightRef = useRef<Highlight | undefined>(undefined);
  const [captured, setCaptured] = useState<ResponseSelection>();
  const capturedRef = useRef(captured);
  capturedRef.current = captured;
  const [positionRevision, setPositionRevision] = useState(0);
  const [commenting, setCommenting] = useState(false);
  const [comment, setComment] = useState("");
  const [position, setPosition] = useState<CSSProperties & { "--menu-enter-y"?: string }>({ left: 0, top: 0 });
  const { t } = useI18n();
  const sideConversationAvailable = useContext(SideConversationAvailableContext);
  useLayoutEffect(() => {
    const toolbar = toolbarRef.current;
    const anchor = anchorRef.current;
    if (!captured || !toolbar || !anchor) return;
    const menu = selectionActionMenuMetrics(toolbar, { width: 360, height: 44 });
    const placed = selectionActionMenuPosition(anchor, menu, { left: 8, top: 8, right: window.innerWidth - 8, bottom: window.innerHeight - 8 });
    setPosition(placed.above
      ? { left: placed.left, bottom: window.innerHeight - placed.top - menu.height, transformOrigin: "bottom center", "--menu-enter-y": "4px" }
      : { left: placed.left, top: placed.top, transformOrigin: "top center", "--menu-enter-y": "-4px" });
  }, [captured, commenting, positionRevision]);
  useEffect(() => {
    const toolbar = toolbarRef.current;
    if (!captured || !toolbar || typeof ResizeObserver === "undefined") return;
    const observer = new ResizeObserver(() => setPositionRevision(current => current + 1));
    observer.observe(toolbar);
    return () => observer.disconnect();
  }, [captured, commenting]);
  useLayoutEffect(() => {
    if (!captured) return;
    if (commenting) {
      const root = ref.current!.querySelector<HTMLElement>(".agent-text")!;
      const range = validatedRange(root, captured);
      if (range) {
        window.getSelection()?.removeAllRanges();
        window.getSelection()?.addRange(range);
      }
      // Restoring the native selection can move Chromium focus back to the
      // article. Focus the input last so typing works after one click.
      commentRef.current?.focus({ preventScroll: true });
    }
    else if (returnToToggleRef.current) {
      commentToggleRef.current?.focus({ preventScroll: true });
      returnToToggleRef.current = false;
    }
  }, [captured, commenting]);
  useEffect(() => {
    // Focus moves into the comment input, so the native selection stops
    // painting; keep the passage marked until the comment is added or dropped.
    const highlights = (globalThis.CSS as typeof CSS & { highlights?: Map<string, unknown> } | undefined)?.highlights;
    const HighlightClass = (globalThis as typeof globalThis & { Highlight?: new (range: Range) => Highlight }).Highlight;
    const root = ref.current?.querySelector<HTMLElement>(".agent-text");
    const range = captured && commenting && root ? validatedRange(root, captured) : undefined;
    if (!range || !highlights || !HighlightClass) return;
    const highlight = new HighlightClass(range);
    selectionHighlightRef.current = highlight;
    highlights.set("wuu-response-selection", highlight);
    return () => { selectionHighlightRef.current = undefined; highlights.delete("wuu-response-selection"); };
  }, [captured, commenting]);
  useEffect(() => {
    if (!captured) return;
    const article = ref.current!;
    const root = article.querySelector<HTMLElement>(".agent-text")!;
    const observer = new MutationObserver(() => {
      const range = validatedRange(root, captured);
      if (!visible(article) || article.closest<HTMLElement>("[data-thread-id]")?.dataset.threadId !== captured.source.thread_id || !range) {
        setCaptured(undefined);
        setCommenting(false);
        setComment("");
        return;
      }
      selectionHighlightRef.current?.clear();
      selectionHighlightRef.current?.add(range);
      if (toolbarRef.current?.contains(document.activeElement)) return;
      const native = window.getSelection();
      const current = native?.rangeCount === 1 ? native.getRangeAt(0) : undefined;
      // Markdown updates can replace selected nodes without changing the
      // captured passage. Re-anchor before selectionchange sees a lost range.
      if (native && (!current || current.compareBoundaryPoints(Range.START_TO_START, range) !== 0
        || current.compareBoundaryPoints(Range.END_TO_END, range) !== 0)) {
        native.removeAllRanges();
        native.addRange(range);
      }
    });
    observer.observe(root, { childList: true, characterData: true, subtree: true });
    for (let node: HTMLElement | null = article; node; node = node.parentElement) {
      observer.observe(node, { attributes: true, attributeFilter: ["hidden", "inert", "aria-hidden", "style", "class", "data-thread-id"] });
    }
    return () => observer.disconnect();
  }, [captured]);
  useEffect(() => {
    const article = ref.current!;
    let dragging = false;
    const clear = () => {
      setCaptured(undefined);
      setCommenting(false);
      setComment("");
    };
    const capture = () => {
      if (dragging || toolbarRef.current?.contains(document.activeElement)) return;
      const root = article.querySelector<HTMLElement>(".agent-text");
      const native = window.getSelection();
      if (!root || !native || native.rangeCount !== 1 || native.isCollapsed) return clear();
      const range = native.getRangeAt(0);
      if (!root.contains(range.startContainer) || !root.contains(range.endContainer)) return clear();
      const controls = 'button, input, textarea, select, [role="button"], [contenteditable]:not([contenteditable="false"])';
      if (root.closest(controls)) return clear();
      const selectedContent = range.cloneContents();
      const rangeText = selectedContent.textContent ?? "";
      const selectedControls = selectedContent.querySelectorAll(controls);
      let text = native.toString();
      if (selectedControls.length > 0) {
        selectedControls.forEach(control => control.remove());
        selectedContent.querySelectorAll("p, li, pre, blockquote, h1, h2, h3, h4, h5, h6, br")
          .forEach(block => block.after(document.createTextNode("\n")));
        text = (selectedContent.textContent ?? "").replace(/\n{3,}/g, "\n\n").trim();
      }
      if (!text.trim()) return clear();
      const threadID = article.closest<HTMLElement>("[data-thread-id]")?.dataset.threadId;
      if (!threadID || !visible(article)) return clear();
      const start = textOffset(root, range.startContainer, range.startOffset);
      const selection: ResponseSelection = { id: crypto.randomUUID(), text, source: {
        thread_id: threadID, turn_id: turnID, item_id: itemID, start_offset: start, end_offset: start + rangeText.length,
        ...(text === rangeText ? {} : { range_text: rangeText }),
      } };
      anchorRef.current = range.getBoundingClientRect();
      sourceHighlight?.clear();
      setCommenting(false);
      setComment("");
      setCaptured(root.textContent?.slice(start, start + rangeText.length) === rangeText ? selection : undefined);
    };
    const down = (event: PointerEvent) => {
      if ((event.target as Element)?.closest?.(".response-selection-toolbar")) return;
      dragging = true;
      clear();
    };
    const up = (event: PointerEvent) => {
      dragging = false;
      if (toolbarRef.current?.contains(event.target as Node)) return;
      capture();
    };
    const key = (event: KeyboardEvent) => { if (event.key === "Escape" && !event.isComposing) clear(); };
    const scroll = (event: Event) => {
      if (dragging || !capturedRef.current || toolbarRef.current?.contains(event.target as Node)) return;
      const root = article.querySelector<HTMLElement>(".agent-text");
      if (!root || !visible(article)) return clear();
      const range = validatedRange(root, capturedRef.current);
      if (!range) return clear();
      const anchor = range.getBoundingClientRect();
      if (anchor.bottom < 0 || anchor.top > window.innerHeight) return clear();
      anchorRef.current = anchor;
      setPositionRevision(current => current + 1);
    };
    document.addEventListener("selectionchange", capture);
    document.addEventListener("pointerdown", down);
    document.addEventListener("pointerup", up);
    document.addEventListener("keydown", key);
    document.addEventListener("visibilitychange", clear);
    window.addEventListener("blur", clear);
    window.addEventListener("resize", clear);
    document.addEventListener("scroll", scroll, true);
    clear();
    return () => {
      if (sourceHighlight && article.contains(sourceHighlight.root)) sourceHighlight.clear();
      document.removeEventListener("selectionchange", capture);
      document.removeEventListener("pointerdown", down);
      document.removeEventListener("pointerup", up);
      document.removeEventListener("keydown", key);
      document.removeEventListener("visibilitychange", clear);
      window.removeEventListener("blur", clear);
      window.removeEventListener("resize", clear);
      document.removeEventListener("scroll", scroll, true);
    };
  }, [turnID, itemID]);
  function addSelection(): void {
    if (!captured) return;
    const article = ref.current!;
    const root = article.querySelector<HTMLElement>(".agent-text");
    if (root && visible(article) && article.closest<HTMLElement>("[data-thread-id]")?.dataset.threadId === captured.source.thread_id && validatedRange(root, captured)) {
      window.getSelection()?.removeAllRanges();
      window.dispatchEvent(new CustomEvent<ResponseSelection>("wuu:add-response-selection", {
        detail: { ...captured, ...(commenting && comment.trim() ? { comment: comment.trim() } : {}) },
      }));
    }
    setCaptured(undefined);
    setCommenting(false);
    setComment("");
  }
  function askSide(): void {
    if (!captured) return;
    const article = ref.current;
    const root = article?.querySelector<HTMLElement>(".agent-text");
    if (root && article && visible(article) && validatedRange(root, captured)) {
      window.dispatchEvent(new CustomEvent<ResponseSelection>("wuu:ask-side-selection", { detail: captured }));
    }
    setCaptured(undefined);
  }
  return <article tabIndex={-1} {...props} ref={ref} data-response-item-id={itemID} data-response-turn-id={turnID}>
    {children}
    {captured ? createPortal(<SelectionActionMenu key={commenting ? "comment" : "actions"} ref={toolbarRef}
      className="response-selection-toolbar" style={position} label={t("responseSelection.actions")}
      quoteLabel={t("responseSelection.quote")} submitLabel={t("responseSelection.add")}
      commentPlaceholder={t("responseSelection.optionalComment")} commenting={commenting} comment={comment}
      onCommentChange={setComment} onQuote={() => setCommenting(true)}
      onCommentCancel={() => { returnToToggleRef.current = true; setCommenting(false); setComment(""); }}
      onCommentSubmit={addSelection} commentToggleRef={commentToggleRef} commentInputRef={commentRef}
      extraActions={!sideConversationAvailable || ref.current?.closest('[data-wuu-component="side-thread"]') ? undefined
        : <button type="button" onPointerDown={event => event.preventDefault()} onClick={askSide}>{t("responseSelection.askSide")}</button>} />, document.body) : null}
  </article>;
}
