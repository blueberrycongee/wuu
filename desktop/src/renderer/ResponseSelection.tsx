import { useEffect, useLayoutEffect, useRef, useState, type ComponentPropsWithoutRef, type CSSProperties } from "react";
import { createPortal } from "react-dom";
import type { ResponseSelection } from "../shared/protocol";
import { SelectionActionMenu } from "./SelectionActionMenu";
import { showToast } from "./Toast";
import { translateCurrent, useI18n } from "./i18n";
import "./ResponseSelection.css";

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
    if (!started && start <= next) {
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

function selectionFocusRect(selection: Selection, range: Range): DOMRect {
  if (selection.focusNode) {
    try {
      const focus = document.createRange();
      focus.setStart(selection.focusNode, selection.focusOffset);
      focus.collapse(true);
      return focus.getBoundingClientRect();
    } catch {
      // A detached focus endpoint can occur while a streamed response settles.
    }
  }
  return range.getBoundingClientRect();
}

/** Only mounted, visible, exact source matches can be navigated to. */
export function navigateToResponseSelection(selection: ResponseSelection): boolean {
  sourceHighlight?.clear();
  for (const article of document.querySelectorAll<HTMLElement>("[data-response-item-id]")) {
    if (article.dataset.responseItemId !== selection.source.item_id
      || article.dataset.responseTurnId !== selection.source.turn_id
      || article.closest<HTMLElement>("[data-thread-id]")?.dataset.threadId !== selection.source.thread_id
      || article.dataset.responseSettled !== "true" || !visible(article)) continue;
    const root = article.querySelector<HTMLElement>(".agent-text");
    const range = root && validatedRange(root, selection);
    if (!root || !range) continue;
    const highlights = (globalThis.CSS as typeof CSS & { highlights?: Map<string, unknown> } | undefined)?.highlights;
    const HighlightClass = (globalThis as typeof globalThis & { Highlight?: new (range: Range) => unknown }).Highlight;
    if (highlights && HighlightClass) {
      highlights.set("wuu-response-source", new HighlightClass(range));
    } else {
      window.getSelection()?.removeAllRanges();
      window.getSelection()?.addRange(range);
    }
    const observer = new MutationObserver(() => sourceHighlight?.clear());
    observer.observe(root, { childList: true, characterData: true, subtree: true });
    for (let node: HTMLElement | null = article; node; node = node.parentElement) {
      observer.observe(node, { attributes: true, attributeFilter: ["hidden", "inert", "aria-hidden", "style", "class", "data-thread-id"] });
    }
    sourceHighlight = { root, clear: () => {
      observer.disconnect();
      highlights?.delete("wuu-response-source");
      if (!highlights && window.getSelection()?.rangeCount === 1 && window.getSelection()?.getRangeAt(0) === range) window.getSelection()?.removeAllRanges();
      sourceHighlight = undefined;
    } };
    const start = range.startContainer.parentElement!;
    let scroller: HTMLElement | null = start;
    while (scroller && (!/(auto|scroll)/.test(getComputedStyle(scroller).overflowY) || scroller.scrollHeight <= scroller.clientHeight)) scroller = scroller.parentElement;
    if (scroller) {
      const beginning = range.cloneRange();
      beginning.collapse(true);
      scroller.scrollBy({ top: beginning.getBoundingClientRect().top - scroller.getBoundingClientRect().top - scroller.clientHeight / 2, behavior: "instant" });
    } else {
      start.scrollIntoView({ block: "center", behavior: "instant" });
    }
    return true;
  }
  showToast({ message: translateCurrent("responseSelection.unavailable") });
  return false;
}

export function ResponseSelectionReference({ selection }: { selection: ResponseSelection }): JSX.Element {
  const { t } = useI18n();
  return <section className="response-selection-reference">
    <blockquote>{selection.text}</blockquote>
    {selection.comment ? <p>{selection.comment}</p> : null}
    <button type="button" className="response-selection-source" onClick={() => navigateToResponseSelection(selection)}>{t("responseSelection.source")}</button>
  </section>;
}

export function AssistantResponseArticle({ turnID, itemID, settled, children, ...props }: ComponentPropsWithoutRef<"article"> & {
  turnID: string; itemID: string; settled: boolean;
}): JSX.Element {
  const ref = useRef<HTMLElement>(null);
  const toolbarRef = useRef<HTMLDivElement>(null);
  const commentRef = useRef<HTMLTextAreaElement>(null);
  const commentToggleRef = useRef<HTMLButtonElement>(null);
  const returnToToggleRef = useRef(false);
  const anchorRef = useRef<DOMRect | undefined>(undefined);
  const [captured, setCaptured] = useState<ResponseSelection>();
  const capturedRef = useRef(captured);
  capturedRef.current = captured;
  const [positionRevision, setPositionRevision] = useState(0);
  const [commenting, setCommenting] = useState(false);
  const [comment, setComment] = useState("");
  const [position, setPosition] = useState<CSSProperties & { "--menu-enter-y"?: string }>({ left: 0, top: 0 });
  const { t } = useI18n();
  useLayoutEffect(() => {
    const toolbar = toolbarRef.current;
    const anchor = anchorRef.current;
    if (!captured || !toolbar || !anchor) return;
    const bounds = toolbar.getBoundingClientRect();
    const left = Math.max(8, Math.min(anchor.left, window.innerWidth - bounds.width - 8));
    // Above the passage the panel is pinned by its bottom edge, so a growing
    // comment extends upward and never covers the quoted text.
    setPosition(anchor.top - bounds.height - 8 >= 8
      ? { left, bottom: window.innerHeight - anchor.top + 8, transformOrigin: "bottom left", "--menu-enter-y": "4px" }
      : { left, top: Math.max(8, Math.min(anchor.bottom + 8, window.innerHeight - bounds.height - 8)), transformOrigin: "top left", "--menu-enter-y": "-4px" });
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
  }, [captured, commenting, positionRevision]);
  useEffect(() => {
    // Focus moves into the comment input, so the native selection stops
    // painting; keep the passage marked until the comment is added or dropped.
    const highlights = (globalThis.CSS as typeof CSS & { highlights?: Map<string, unknown> } | undefined)?.highlights;
    const HighlightClass = (globalThis as typeof globalThis & { Highlight?: new (range: Range) => unknown }).Highlight;
    const root = ref.current?.querySelector<HTMLElement>(".agent-text");
    const range = captured && commenting && root ? validatedRange(root, captured) : undefined;
    if (!range || !highlights || !HighlightClass) return;
    highlights.set("wuu-response-selection", new HighlightClass(range));
    return () => { highlights.delete("wuu-response-selection"); };
  }, [captured, commenting]);
  useEffect(() => {
    if (!captured) return;
    const article = ref.current!;
    const root = article.querySelector<HTMLElement>(".agent-text")!;
    const observer = new MutationObserver(() => {
      if (!visible(article) || article.closest<HTMLElement>("[data-thread-id]")?.dataset.threadId !== captured.source.thread_id || !validatedRange(root, captured)) {
        setCaptured(undefined);
        setCommenting(false);
        setComment("");
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
      if (!settled || !root || !native || native.rangeCount !== 1 || native.isCollapsed) return clear();
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
      anchorRef.current = selectionFocusRect(native, range);
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
    const key = (event: KeyboardEvent) => { if (event.key === "Escape") clear(); };
    const scroll = (event: Event) => {
      if (dragging || !capturedRef.current || toolbarRef.current?.contains(event.target as Node)) return;
      const native = window.getSelection();
      const root = article.querySelector<HTMLElement>(".agent-text");
      if (!native || native.rangeCount !== 1 || native.isCollapsed || !root || !visible(article)) return clear();
      const range = native.getRangeAt(0);
      if (!root.contains(range.startContainer) || !root.contains(range.endContainer)) return clear();
      const anchor = selectionFocusRect(native, range);
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
  }, [turnID, itemID, settled]);
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
  return <article {...props} ref={ref} data-response-item-id={itemID} data-response-turn-id={turnID} data-response-settled={settled}>
    {children}
    {captured && settled ? createPortal(<SelectionActionMenu key={commenting ? "comment" : "actions"} ref={toolbarRef}
      className="response-selection-toolbar" style={position} label={t("responseSelection.actions")}
      addLabel={t("responseSelection.add")} commentLabel={t("responseSelection.comment")}
      commentPlaceholder={t("responseSelection.commentPlaceholder")} commenting={commenting} comment={comment}
      commentMarkerPlacement={position.bottom !== undefined ? "after" : "before"}
      onCommentChange={setComment} onAdd={addSelection} onCommentStart={() => setCommenting(true)}
      onCommentCancel={() => { returnToToggleRef.current = true; setCommenting(false); setComment(""); }}
      onCommentSubmit={addSelection} commentToggleRef={commentToggleRef} commentInputRef={commentRef}
      extraActions={ref.current?.closest('[data-wuu-component="side-thread"]') ? undefined
        : <button type="button" onPointerDown={event => event.preventDefault()} onClick={askSide}>{t("responseSelection.askSide")}</button>} />, document.body) : null}
  </article>;
}
