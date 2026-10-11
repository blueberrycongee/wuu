import { useCallback, useEffect, useId, useLayoutEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { useFileSelectionActions } from "./FileSelectionContext";
import { fileSelectionLineIndex, fileSelectionRevision, fileSelectionSource, findFileSelectionDOMAnchor, mapFileDOMSelection, type FileSelectionRange, type FileSelectionSource } from "./FileSelectionMapping";
import { useI18n } from "./i18n";
import { SelectionActionMenu } from "./SelectionActionMenu";
import { selectionActionMenuMetrics, selectionActionMenuPosition } from "./SelectionActionMenuPosition";
import { ArrowUp } from "./WuuIcons";
import { UILayerPortal } from "./ui/layers/UILayerHost";
import "./FileSelectionSurface.css";

export type FileEditorSelection = FileSelectionRange & { getRect: () => DOMRect | null };
export type FileSelectionControls = {
  onSelectionChange?: (selection: FileEditorSelection | null) => void;
  persistentSelection?: FileSelectionSource;
  revealSelection?: { source: FileSelectionSource; request: number };
};
type Capture = {
  ownerKey: string;
  source: FileSelectionSource;
  blockFallback: boolean;
  getRect?: () => DOMRect | null;
  domRange?: Range;
};
type Form = { kind: "comment" | "edit"; value: string; id?: string };

const copy = {
  en: {
    tools: "Selection actions", quote: "Add to conversation", comment: "Comment", edit: "Edit",
    submit: "Send edit request", cancel: "Cancel", remove: "Remove",
    editComment: "Edit comment", comments: "Comments", instruction: "Describe the change",
    commentLabel: "Add an optional comment…", block: "Source block selected; rendered text cannot be mapped exactly.",
    stale: "The file has changed. Select the text again before sending an edit request.",
    close: "Close selection actions",
    sending: "Sending…", failed: "The edit request was not sent. Your instruction is kept; try again.",
    locate: "Locate selection",
    askSide: "Ask in side chat", sideUnavailable: "Start a main conversation to use side chat",
  },
  zh: {
    tools: "选区操作", quote: "添加到对话", comment: "评论", edit: "编辑",
    submit: "发送修改请求", cancel: "取消", remove: "删除",
    editComment: "编辑评论", comments: "评论", instruction: "描述需要的修改",
    commentLabel: "添加可选评论…", block: "已选取源码块；渲染内容无法精确映射。",
    stale: "文件已更新。发送修改请求前，请重新选择文本。", close: "关闭选区操作",
    sending: "发送中…", failed: "修改请求未发送。指令已保留，可以重试。",
    locate: "定位原文",
    askSide: "在侧边聊天中提问", sideUnavailable: "先开始主对话，才能使用侧聊",
  },
};

export function FileSelectionSurface({ workspace, path, text, active = true, children }: {
  workspace: string;
  path: string;
  text: string;
  active?: boolean;
  children: (controls: FileSelectionControls) => ReactNode;
}): JSX.Element {
  const actions = useFileSelectionActions();
  const { locale } = useI18n();
  const labels = locale.startsWith("zh") ? copy.zh : copy.en;
  const host = useRef<HTMLDivElement>(null);
  const content = useRef<HTMLDivElement>(null);
  const popup = useRef<HTMLDivElement>(null);
  const textarea = useRef<HTMLTextAreaElement>(null);
  const commentToggle = useRef<HTMLButtonElement>(null);
  const returnToComment = useRef(false);
  const highlightID = `file-selection-${useId().replace(/:/g, "")}`;
  const revision = useMemo(() => fileSelectionRevision(text), [text]);
  const lineIndex = useMemo(() => fileSelectionLineIndex(text), [text]);
  const [capture, setCapture] = useState<Capture | null>(null);
  const [form, setForm] = useState<Form | null>(null);
  const [sending, setSending] = useState(false);
  const [sendFailed, setSendFailed] = useState(false);
  const [revealSelection, setRevealSelection] = useState<FileSelectionControls["revealSelection"]>();
  const submissionRef = useRef(0);
  const sendingRef = useRef(false);
  const [position, setPosition] = useState<{ left: number; top: number; width: number; height: number } | null>(null);
  const formRef = useRef(form);
  formRef.current = form;
  const enabled = Boolean(actions && active);
  const ownerKey = actions?.ownerKey;
  const current = capture?.ownerKey === ownerKey && capture?.source.workspace === workspace && capture.source.path === path ? capture : null;
  const stale = Boolean(current && current.source.revision !== revision);
  const comments = actions?.comments.filter(part => !part.source.pdf && part.source.workspace === workspace && part.source.path === path) ?? [];

  const close = useCallback(() => {
    submissionRef.current++;
    sendingRef.current = false;
    setSending(false); setSendFailed(false);
    setCapture(null); setForm(null); setPosition(null);
    setRevealSelection(undefined);
    const selection = window.getSelection();
    if (selection?.anchorNode && content.current?.contains(selection.anchorNode)) selection.removeAllRanges();
  }, []);
  useEffect(close, [workspace, path, enabled, ownerKey, close]);

  const onSelectionChange = useCallback((range: FileEditorSelection | null) => {
    if (!enabled || !ownerKey || formRef.current) return;
    const source = range && fileSelectionSource(workspace, path, text, range, revision, lineIndex);
    setCapture(source && range ? { ownerKey, source, blockFallback: false, getRect: range.getRect } : null);
  }, [enabled, ownerKey, workspace, path, text, revision, lineIndex]);

  useEffect(() => {
    if (!enabled || !ownerKey) return;
    let dragging = false;
    const readSelection = (event: Event) => {
      if (dragging) return;
      if (event instanceof KeyboardEvent && event.key === "Escape") return;
      if (event.target instanceof Node && popup.current?.contains(event.target)) return;
      if (formRef.current || popup.current?.contains(document.activeElement)) return;
      const selection = window.getSelection();
      if (!selection?.rangeCount || !content.current) return;
      const range = selection.getRangeAt(0);
      // Monaco owns virtualized text and supplies model positions separately.
      const parent = range.startContainer.nodeType === Node.ELEMENT_NODE ? range.startContainer as Element : range.startContainer.parentElement;
      if (parent?.closest(".monaco-editor")) return;
      const mapped = mapFileDOMSelection(content.current, range, text);
      const source = mapped && fileSelectionSource(workspace, path, text, mapped, revision, lineIndex);
      if (!source || !mapped) { setCapture(null); return; }
      const saved = range.cloneRange();
      setCapture(previous => previous?.ownerKey === ownerKey && previous?.source.revision === source.revision && previous.source.start_line === source.start_line
        && previous.source.start_column === source.start_column && previous.source.end_line === source.end_line && previous.source.end_column === source.end_column
        ? previous : { ownerKey, source, blockFallback: mapped.blockFallback, domRange: saved, getRect: () => saved.getBoundingClientRect() });
    };
    const down = (event: PointerEvent) => { if (event.button === 0 && content.current?.contains(event.target as Node)) dragging = true; };
    const up = (event: PointerEvent) => { if (!dragging) return; dragging = false; readSelection(event); };
    document.addEventListener("selectionchange", readSelection);
    document.addEventListener("pointerdown", down);
    document.addEventListener("pointerup", up);
    document.addEventListener("pointercancel", up);
    document.addEventListener("keyup", readSelection);
    return () => {
      document.removeEventListener("selectionchange", readSelection);
      document.removeEventListener("pointerdown", down);
      document.removeEventListener("pointerup", up);
      document.removeEventListener("pointercancel", up);
      document.removeEventListener("keyup", readSelection);
    };
  }, [enabled, ownerKey, workspace, path, text, revision, lineIndex]);

  useEffect(() => {
    if (!enabled || !current) return;
    const escape = (event: KeyboardEvent) => {
      if (event.key !== "Escape" || event.isComposing) return;
      event.preventDefault();
      event.stopPropagation();
      if (formRef.current?.kind === "comment") {
        returnToComment.current = true;
        setForm(null);
        return;
      }
      close();
      content.current?.focus({ preventScroll: true });
    };
    document.addEventListener("keydown", escape, true);
    return () => document.removeEventListener("keydown", escape, true);
  }, [enabled, current, close]);

  useEffect(() => {
    if (!enabled || !current || !form) return;
    const dismiss = (event: PointerEvent) => {
      if (event.target instanceof Node && !popup.current?.contains(event.target)) close();
    };
    document.addEventListener("pointerdown", dismiss, true);
    return () => document.removeEventListener("pointerdown", dismiss, true);
  }, [enabled, current, form?.kind, close]);

  useLayoutEffect(() => {
    if (!enabled || !current || !host.current) { setPosition(null); return; }
    const place = () => {
      const bounds = host.current?.getBoundingClientRect();
      if (!bounds || bounds.width === 0 || bounds.height === 0) { setPosition(null); return; }
      const left = Math.max(8, bounds.left + 8);
      const top = Math.max(8, bounds.top + 8);
      const right = Math.min(window.innerWidth - 8, bounds.right - 8);
      const bottom = Math.min(window.innerHeight - 8, bounds.bottom - 8);
      if (right <= left || bottom <= top) { setPosition(null); return; }
      const anchor = current.getRect?.();
      // A form remains reachable while typing, even if its source scrolls away.
      if (!form && anchor && (anchor.bottom < top || anchor.top > bottom || anchor.right < left || anchor.left > right)) {
        setPosition(null); return;
      }
      const width = Math.min(form ? 360 : 400, right - left);
      const height = bottom - top;
      const menu = selectionActionMenuMetrics(popup.current, { width, height: form ? 260 : 44 });
      const placed = selectionActionMenuPosition(anchor ?? { left, right: left, top, bottom: top }, menu, { left, top, right, bottom });
      setPosition({
        left: placed.left, top: placed.top, width, height,
      });
    };
    place();
    window.addEventListener("resize", place);
    document.addEventListener("scroll", place, true);
    document.addEventListener("file-selection-layout", place);
    // A sheet transform moves the source without resizing it. Its final
    // position must replace any anchor sampled while the sheet was in flight.
    const finishMovement = (event: TransitionEvent) => {
      if (event.propertyName === "transform" && event.target instanceof Element &&
        host.current && event.target.contains(host.current)) place();
    };
    document.addEventListener("transitionend", finishMovement, true);
    document.addEventListener("transitioncancel", finishMovement, true);
    const observer = typeof ResizeObserver === "undefined" ? undefined : new ResizeObserver(place);
    observer?.observe(host.current);
    if (popup.current) observer?.observe(popup.current);
    return () => {
      window.removeEventListener("resize", place);
      document.removeEventListener("scroll", place, true);
      document.removeEventListener("file-selection-layout", place);
      document.removeEventListener("transitionend", finishMovement, true);
      document.removeEventListener("transitioncancel", finishMovement, true);
      observer?.disconnect();
    };
  }, [enabled, current, form?.kind, Boolean(position), stale]);

  useEffect(() => { if (form) textarea.current?.focus({ preventScroll: true }); }, [form?.kind, form?.id, Boolean(position)]);
  useLayoutEffect(() => {
    if (!form && returnToComment.current) {
      commentToggle.current?.focus({ preventScroll: true });
      returnToComment.current = false;
    }
  }, [form]);

  useEffect(() => {
    // Custom highlights retain the reading selection while focus is in a form.
    const api = window as unknown as { Highlight?: new (...ranges: Range[]) => unknown; CSS?: { highlights?: Map<string, unknown> } };
    if (!current?.domRange || stale || !api.Highlight || !api.CSS?.highlights) return;
    api.CSS.highlights.set(highlightID, new api.Highlight(current.domRange));
    const style = document.createElement("style");
    style.textContent = `::highlight(${highlightID}) { background-color: var(--selection-bg, Highlight); color: inherit; }`;
    document.head.appendChild(style);
    return () => { api.CSS?.highlights?.delete(highlightID); style.remove(); };
  }, [current, stale, highlightID]);

  const startForm = (kind: Form["kind"]) => setForm({ kind, value: "" });
  const captureSaved = (source: FileSelectionSource, locate = false): Capture => {
    const anchor = source.revision === revision && content.current ? findFileSelectionDOMAnchor(content.current, text, source) : null;
    if (locate) {
      anchor?.element.scrollIntoView({ block: "nearest" });
      setRevealSelection({ source, request: Date.now() });
    }
    return { ownerKey: ownerKey!, source, blockFallback: Boolean(anchor && !anchor.range), domRange: anchor?.range,
      getRect: anchor ? () => anchor.range?.getBoundingClientRect() ?? anchor.element.getBoundingClientRect() : undefined };
  };
  return <div className="file-selection-surface" ref={host}>
    <div className="file-selection-content" ref={content} tabIndex={-1}>
      {children(enabled ? { onSelectionChange, persistentSelection: current && !stale ? current.source : undefined, revealSelection } : {})}
    </div>
    {enabled && comments.length > 0 && <aside className="file-selection-comments" aria-label={labels.comments}>
      {comments.map(part => <div className="file-selection-comment" key={part.id}>
        <div className="file-selection-comment-heading"><span>{path}:{part.source.start_line}–{part.source.end_line}</span>
          <button type="button" disabled={part.source.revision !== revision} onClick={() => { setForm(null); setCapture(captureSaved(part.source, true)); }}>{labels.locate}</button>
          <button type="button" onClick={() => {
            setCapture(captureSaved(part.source));
            setForm({ kind: "comment", value: part.comment ?? "", id: part.id });
          }}>{labels.editComment}</button>
          <button type="button" onClick={() => { actions!.removeComment(part.id); if (form?.id === part.id) close(); }}>{labels.remove}</button>
        </div>
        <blockquote>{part.source.quote}</blockquote>
        <p>{part.comment}</p>
        {part.source.revision !== revision && <p role="status">{labels.stale}</p>}
      </div>)}
    </aside>}
    {enabled && current && position && <UILayerPortal layer="popover">
      {form?.kind === "edit" ? <div ref={popup} className="file-selection-popup" data-wuu-component="popover" data-wuu-layer="popover"
        style={{ left: position.left, top: position.top, width: position.width, maxWidth: position.width, maxHeight: position.height }}
        onPointerDown={event => { if ((event.target as Element).closest("button")) event.preventDefault(); }}>
        <form onSubmit={async event => {
          event.preventDefault();
          const value = form.value.trim();
          if (!value || sendingRef.current || stale) return;
          const submission = ++submissionRef.current;
          sendingRef.current = true; setSending(true); setSendFailed(false);
          try {
            const accepted = await actions!.edit(current.source, value);
            if (submission !== submissionRef.current) return;
            if (accepted) close();
            else setSendFailed(true);
          } catch {
            if (submission === submissionRef.current) setSendFailed(true);
          } finally {
            if (submission === submissionRef.current) { sendingRef.current = false; setSending(false); }
          }
        }}>
          <div className="file-selection-edit-composer">
            <textarea ref={textarea} id={`${highlightID}-input`} rows={1} wrap="soft" value={form.value} readOnly={sending}
              aria-label={labels.instruction} placeholder={labels.instruction}
              onChange={event => setForm({ ...form, value: event.target.value })}
              onKeyDown={event => { if (event.key === "Enter" && !event.shiftKey && !event.nativeEvent.isComposing) { event.preventDefault(); event.currentTarget.form?.requestSubmit(); } }} />
            <button type="submit" aria-label={labels.submit} title={labels.submit} disabled={sending || !form.value.trim() || stale}>
              <ArrowUp className="icon" aria-hidden="true" />
            </button>
          </div>
          {current.blockFallback && <p role="status">{labels.block}</p>}
          {stale && <p role="status">{labels.stale}</p>}
          {sendFailed && <p role="alert">{labels.failed}</p>}
          {sending && <p role="status">{labels.sending}</p>}
        </form>
      </div> : <SelectionActionMenu ref={popup} className="file-selection-action-menu"
        style={{ left: position.left, top: position.top, maxWidth: position.width, maxHeight: position.height, overflow: "auto" }}
        label={labels.tools} addLabel={labels.quote} commentLabel={labels.comment} commentPlaceholder={labels.commentLabel}
        commenting={form?.kind === "comment"} comment={form?.kind === "comment" ? form.value : ""}
        allowEmptyComment={!form?.id}
        onCommentChange={value => setForm(previous => previous?.kind === "comment" ? { ...previous, value } : previous)}
        onAdd={() => { actions!.addQuote(current.source); close(); }} onCommentStart={() => startForm("comment")}
        onCommentCancel={() => { returnToComment.current = true; setForm(null); }}
        onCommentSubmit={() => { if (form?.kind !== "comment") return; if (form.value.trim()) actions!.addComment(current.source, form.value.trim(), form.id); else if (!form.id) actions!.addQuote(current.source); else return; close(); }}
        commentToggleRef={commentToggle} commentInputRef={textarea}
        extraActions={<>
          <button type="button" disabled={!actions!.askSide || stale} title={!actions!.askSide ? labels.sideUnavailable : undefined}
            onPointerDown={event => event.preventDefault()} onClick={() => { actions!.askSide?.(current.source); close(); }}>{labels.askSide}</button>
          <button type="button" disabled={stale} onPointerDown={event => event.preventDefault()} onClick={() => startForm("edit")}>{labels.edit}</button>
          <button type="button" aria-label={labels.close} onPointerDown={event => event.preventDefault()} onClick={close}>×</button>
        </>}
        status={<>
          {current.blockFallback && <p className="selection-action-menu-status" role="status">{labels.block}</p>}
          {stale && <p className="selection-action-menu-status" role="status">{labels.stale}</p>}
        </>} />}
    </UILayerPortal>}
  </div>;
}
