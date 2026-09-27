import { useEffect, useLayoutEffect, useRef, useState, type ComponentPropsWithoutRef } from "react";
import { createPortal } from "react-dom";
import type { ResponseSelection } from "../shared/protocol";
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
  const [commenting, setCommenting] = useState(false);
  const [comment, setComment] = useState("");
  const [position, setPosition] = useState({ left: 0, top: 0 });
  const { t } = useI18n();
  useLayoutEffect(() => {
    const toolbar = toolbarRef.current;
    const anchor = anchorRef.current;
    if (!captured || !toolbar || !anchor) return;
    const bounds = toolbar.getBoundingClientRect();
    const above = anchor.top - bounds.height - 8;
    setPosition({
      left: Math.max(8, Math.min(anchor.left, window.innerWidth - bounds.width - 8)),
      top: Math.max(8, Math.min(above >= 8 ? above : anchor.bottom + 8, window.innerHeight - bounds.height - 8)),
    });
    if (commenting) {
      commentRef.current?.focus({ preventScroll: true });
      const root = ref.current!.querySelector<HTMLElement>(".agent-text")!;
      const range = validatedRange(root, captured);
      if (range) {
        window.getSelection()?.removeAllRanges();
        window.getSelection()?.addRange(range);
      }
    }
    else if (returnToToggleRef.current) {
      commentToggleRef.current?.focus({ preventScroll: true });
      returnToToggleRef.current = false;
    }
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
      if (root.closest(controls) || [...root.querySelectorAll(controls)].some(control => range.intersectsNode(control))) return clear();
      const threadID = article.closest<HTMLElement>("[data-thread-id]")?.dataset.threadId;
      if (!threadID || !visible(article)) return clear();
      const prefix = document.createRange();
      prefix.selectNodeContents(root);
      prefix.setEnd(range.startContainer, range.startOffset);
      const start = textNodes(prefix.cloneContents()).reduce((sum, node) => sum + node.length, 0);
      const rangeText = textNodes(range.cloneContents()).map(node => node.data).join("");
      const text = native.toString();
      if (!text.trim()) return clear();
      const selection: ResponseSelection = { id: crypto.randomUUID(), text, source: {
        thread_id: threadID, turn_id: turnID, item_id: itemID, start_offset: start, end_offset: start + rangeText.length,
        ...(text === rangeText ? {} : { range_text: rangeText }),
      } };
      anchorRef.current = range.getBoundingClientRect();
      setCommenting(false);
      setComment("");
      setCaptured(validatedRange(root, selection) ? selection : undefined);
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
    const scroll = (event: Event) => { if (!toolbarRef.current?.contains(event.target as Node)) clear(); };
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
  return <article {...props} ref={ref} data-response-item-id={itemID} data-response-turn-id={turnID} data-response-settled={settled}>
    {children}
    {captured && settled ? createPortal(<div ref={toolbarRef} className={`response-selection-toolbar${commenting ? " response-selection-commenting" : ""}`} style={position}
      role="group" aria-label={t("responseSelection.actions")}>
      {commenting ? <>
        <blockquote className="response-selection-comment-source">{captured.text}</blockquote>
        <textarea ref={commentRef} className="response-selection-comment-input" rows={2}
          aria-label={t("responseSelection.optionalComment")} placeholder={t("responseSelection.optionalComment")}
          value={comment} onChange={event => setComment(event.target.value)}
          onKeyDown={event => {
            if (event.key === "Escape") {
              event.preventDefault();
              event.stopPropagation();
              returnToToggleRef.current = true;
              setCommenting(false);
              setComment("");
            }
          }} />
        <div className="response-selection-comment-actions">
          <button type="button" className="response-selection-add" onClick={addSelection}>{t("responseSelection.add")}</button>
        </div>
      </> : <>
        <button type="button" className="response-selection-add" onPointerDown={event => event.preventDefault()} onClick={addSelection}>{t("responseSelection.add")}</button>
        <button ref={commentToggleRef} type="button" className="response-selection-comment-toggle"
          onPointerDown={event => event.preventDefault()} onClick={() => setCommenting(true)}>{t("responseSelection.comment")}</button>
      </>}
    </div>, document.body) : null}
  </article>;
}
