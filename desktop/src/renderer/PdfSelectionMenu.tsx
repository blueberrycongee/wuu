import { useEffect, useLayoutEffect, useRef, useState, type RefObject } from "react";
import type { FileSelectionSource } from "../shared/protocol";
import { useFileSelectionActions } from "./FileSelectionContext";
import type { PdfPreviewSource } from "./PdfSelection";
import { SelectionActionMenu } from "./SelectionActionMenu";
import { selectionActionMenuMetrics, selectionActionMenuPosition } from "./SelectionActionMenuPosition";
import { UILayerPortal } from "./ui/layers/UILayerHost";
import { useI18n } from "./i18n";

/** PDF.js owns the text layer inside a shadow root. Capture its native range
 * before focus moves to a comment, and retain the captured version as data. */
export function PdfSelectionMenu({ hostRef, source, active }: {
  hostRef: RefObject<HTMLDivElement | null>;
  source?: PdfPreviewSource;
  active: boolean;
}): JSX.Element | null {
  const actions = useFileSelectionActions();
  const { t } = useI18n();
  const popup = useRef<HTMLDivElement>(null);
  const textarea = useRef<HTMLTextAreaElement>(null);
  const toggle = useRef<HTMLButtonElement>(null);
  const [capture, setCapture] = useState<{ source: FileSelectionSource; range: Range; owner: string; sourceKey: string }>();
  const [commenting, setCommenting] = useState(false);
  const [comment, setComment] = useState("");
  const [position, setPosition] = useState<{ left: number; top: number; width: number; height: number }>();
  const commentingRef = useRef(commenting);
  commentingRef.current = commenting;
  const sourceKey = JSON.stringify(source);
  const owner = actions?.ownerKey;
  const current = active && capture?.owner === owner && capture?.sourceKey === sourceKey ? capture : undefined;

  function close(): void {
    setCapture(undefined); setCommenting(false); setComment(""); setPosition(undefined);
    const root = hostRef.current?.shadowRoot as (ShadowRoot & { getSelection?: () => Selection | null }) | null;
    const selection = root?.getSelection?.();
    if (selection?.anchorNode?.getRootNode() === root) selection.removeAllRanges();
  }

  useEffect(close, [sourceKey, active, owner]);
  useEffect(() => {
    const host = hostRef.current;
    const root = host?.shadowRoot as (ShadowRoot & { getSelection?: () => Selection | null }) | null;
    if (!host || !root || !source || !active || !owner) return;
    let dragging = false;
    const read = () => {
      if (dragging || commentingRef.current || popup.current?.contains(document.activeElement)) return;
      const selection = root.getSelection?.() ?? window.getSelection();
      if (!selection?.rangeCount || selection.isCollapsed) { setCapture(undefined); return; }
      const range = selection.getRangeAt(0);
      const element = (node: Node): Element | null => node instanceof Element ? node : node.parentElement;
      const start = element(range.startContainer);
      const end = element(range.endContainer);
      if (!start?.closest(".textLayer") || !end?.closest(".textLayer")
        || start.getRootNode() !== root || end.getRootNode() !== root) { setCapture(undefined); return; }
      const first = Number(start.closest<HTMLElement>(".page[data-page-number]")?.dataset.pageNumber);
      const last = Number(end.closest<HTMLElement>(".page[data-page-number]")?.dataset.pageNumber);
      const quote = selection.toString();
      if (!Number.isSafeInteger(first) || first < 1 || !Number.isSafeInteger(last) || last < first || !quote.trim()) return;
      setCapture({ owner, sourceKey, range: range.cloneRange(), source: {
        workspace: source.workspace, path: source.path, revision: source.revision, quote,
        start_line: 0, start_column: 0, end_line: 0, end_column: 0,
        pdf: { start_page: first, end_page: last, ...source.artifact },
      } });
    };
    const down = (event: Event) => { if ((event as PointerEvent).button === 0) dragging = true; };
    const up = () => { if (!dragging) return; dragging = false; read(); };
    const cancel = () => { if (!dragging) return; dragging = false; close(); };
    const blur = () => { dragging = false; };
    const keyup = (event: Event) => { if ((event as KeyboardEvent).key !== "Escape") read(); };
    root.addEventListener("pointerdown", down);
    document.addEventListener("pointerup", up);
    document.addEventListener("pointercancel", cancel);
    window.addEventListener("blur", blur);
    root.addEventListener("keyup", keyup);
    root.addEventListener("selectionchange", read);
    document.addEventListener("selectionchange", read);
    return () => {
      root.removeEventListener("pointerdown", down); document.removeEventListener("pointerup", up);
      document.removeEventListener("pointercancel", cancel); window.removeEventListener("blur", blur); root.removeEventListener("keyup", keyup);
      root.removeEventListener("selectionchange", read); document.removeEventListener("selectionchange", read);
    };
  }, [hostRef, sourceKey, active, owner]);

  useEffect(() => {
    if (!current) return;
    const dismiss = (event: PointerEvent) => {
      const target = event.target as Node;
      if (!hostRef.current?.contains(target) && !popup.current?.contains(target)) close();
    };
    const escape = (event: KeyboardEvent) => {
      if (event.key !== "Escape" || event.isComposing) return;
      event.preventDefault(); event.stopPropagation();
      if (commentingRef.current) { setCommenting(false); requestAnimationFrame(() => toggle.current?.focus()); }
      else { close(); hostRef.current?.focus({ preventScroll: true }); }
    };
    document.addEventListener("pointerdown", dismiss, true);
    document.addEventListener("keydown", escape, true);
    return () => { document.removeEventListener("pointerdown", dismiss, true); document.removeEventListener("keydown", escape, true); };
  }, [current, hostRef]);

  useLayoutEffect(() => {
    const host = hostRef.current;
    if (!current || !host) { setPosition(undefined); return; }
    const place = () => {
      const bounds = host.getBoundingClientRect();
      const composer = host.closest(".workspace-file-layout, .workspace-artifact-document")
        ?.querySelector<HTMLElement>(".workspace-document-composer")?.getBoundingClientRect();
      const left = Math.max(8, bounds.left + 8), top = Math.max(8, bounds.top + 8);
      const right = Math.min(innerWidth - 8, bounds.right - 8);
      const bottom = Math.min(innerHeight - 8, bounds.bottom - 8, composer?.top ?? Infinity);
      if (right <= left || bottom <= top) { setPosition(undefined); return; }
      const anchor = current.range.getBoundingClientRect();
      if (!commenting && (anchor.bottom < top || anchor.top > bottom || anchor.right < left || anchor.left > right)) {
        setPosition(undefined); return;
      }
      const width = Math.min(commenting ? 360 : 330, right - left);
      const metrics = selectionActionMenuMetrics(popup.current, { width, height: commenting ? 120 : 44 });
      const placed = selectionActionMenuPosition(anchor, metrics, { left, top, right, bottom });
      setPosition({ left: placed.left, top: placed.top, width, height: bottom - top });
    };
    place();
    const root = host.shadowRoot;
    root?.addEventListener("scroll", place, true);
    document.addEventListener("scroll", place, true);
    window.addEventListener("resize", place);
    document.addEventListener("file-selection-layout", place);
    const observer = new ResizeObserver(place);
    observer.observe(host);
    if (popup.current) observer.observe(popup.current);
    return () => { observer.disconnect(); root?.removeEventListener("scroll", place, true);
      document.removeEventListener("scroll", place, true); window.removeEventListener("resize", place);
      document.removeEventListener("file-selection-layout", place); };
  }, [current, commenting, Boolean(position), hostRef]);
  useLayoutEffect(() => { if (commenting) textarea.current?.focus({ preventScroll: true }); }, [commenting]);

  if (!current || !position || !actions) return null;
  return <UILayerPortal layer="popover"><SelectionActionMenu ref={popup} className="pdf-selection-action-menu"
    style={{ left: position.left, top: position.top, maxWidth: position.width, maxHeight: position.height, overflow: "auto" }}
    label={t("pdfSelection.actions")} addLabel={t("pdfSelection.add")} commentLabel={t("pdfSelection.comment")}
    commentPlaceholder={t("responseSelection.optionalComment")} commenting={commenting} comment={comment}
    onCommentChange={setComment} onCommentStart={() => setCommenting(true)}
    onCommentCancel={() => { setCommenting(false); requestAnimationFrame(() => toggle.current?.focus()); }}
    onAdd={() => { actions.addQuote(current.source); close(); }}
    onCommentSubmit={() => {
      if (comment.trim()) actions.addComment(current.source, comment.trim());
      else actions.addQuote(current.source);
      close();
    }} commentToggleRef={toggle} commentInputRef={textarea}
    extraActions={<button type="button" aria-label={t("common.close")} onPointerDown={event => event.preventDefault()} onClick={close}>×</button>}
    status={<p className="selection-action-menu-status">{current.source.pdf!.start_page === current.source.pdf!.end_page
      ? t("pdfSelection.page", { page: current.source.pdf!.start_page })
      : t("pdfSelection.pages", { start: current.source.pdf!.start_page, end: current.source.pdf!.end_page })}</p>}
  /></UILayerPortal>;
}
