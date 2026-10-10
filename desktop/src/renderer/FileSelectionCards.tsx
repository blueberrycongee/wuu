import { fileSelectionLocation } from "./PdfSelection";
import { usePdfSelectionNavigation } from "./FileSelectionContext";
import { FileText, MessageSquare, Pencil, X } from "lucide-react";
import { useEffect, useId, useRef, useState } from "react";
import type { MessageContentPart } from "../shared/protocol";
import { FloatingMenuPortal } from "./ComposerFloatingMenu";
import { readFileSelectionNavigation } from "./FileSelectionNavigation";
import { useI18n } from "./i18n";
import "./FileSelectionCards.css";

export type FileSelectionPart = Extract<MessageContentPart, { type: "file_selection" }>;

export type FileSelectionCardsProps = {
  parts: readonly FileSelectionPart[];
  onRemove?: (id: string | string[]) => void;
  onEdit?: (part: FileSelectionPart, comment: string) => void;
  onOpenFile?: (path: string) => void;
};

/** Shared attachment group for composer drafts and immutable message history. */
export function FileSelectionCards({ parts, onRemove, onEdit, onOpenFile }: FileSelectionCardsProps): JSX.Element | null {
  if (parts.length === 0) return null;
  return <div className="file-selection-groups">
    <FileSelectionCardGroup parts={parts.filter((part) => part.intent === "quote")} onRemove={onRemove} onOpenFile={onOpenFile} />
    <FileSelectionCardGroup parts={parts.filter((part) => part.intent !== "quote")} onRemove={onRemove} onEdit={onEdit} onOpenFile={onOpenFile} />
  </div>;
}

function FileSelectionCardGroup({ parts, onRemove, onEdit, onOpenFile }: FileSelectionCardsProps): JSX.Element | null {
  const { locale, t } = useI18n();
  const [open, setOpen] = useState(false);
  const [editing, setEditing] = useState<{ id: string; comment: string } | null>(null);
  const [locationNotices, setLocationNotices] = useState<Record<string, "changed" | "unavailable" | "original" | undefined>>({});
  const anchorRef = useRef<HTMLButtonElement>(null);
  const chipRef = useRef<HTMLDivElement>(null);
  const panelRef = useRef<HTMLDivElement>(null);
  const closeTimer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
  const panelID = useId();
  const openPdf = usePdfSelectionNavigation();
  const allComments = parts.every((part) => part.intent === "comment");
  const allEdits = parts.every((part) => part.intent === "edit");
  const allQuotes = parts.every((part) => part.intent === "quote");
  const label = locale === "zh-CN"
    ? `${parts.length} 个${allQuotes ? "已选文本片段" : allComments ? "评论" : allEdits ? "编辑请求" : "文件引用"}`
    : `${parts.length} ${allQuotes ? parts.length === 1 ? "selected text fragment" : "selected text fragments"
      : allComments ? parts.length === 1 ? "comment" : "comments"
      : allEdits ? parts.length === 1 ? "edit request" : "edit requests"
      : parts.length === 1 ? "file reference" : "file references"}`;

  async function openFile(part: FileSelectionPart): Promise<void> {
    if (part.source.pdf?.artifact_uri) {
      const opened = openPdf?.(part.source) ?? false;
      setLocationNotices(current => ({ ...current, [part.id]: opened ? undefined : "original" }));
      return;
    }
    const target = await readFileSelectionNavigation(part.source);
    setLocationNotices(current => ({ ...current, [part.id]: target.locationChanged
      ? target.available ? "changed" : "unavailable" : undefined }));
    onOpenFile?.(target.path);
  }

  function locationNotice(id: string): JSX.Element | null {
    const notice = locationNotices[id];
    return notice ? <p className="file-selection-location-notice" role="status">
      {t(notice === "original" ? "selectionChip.pdfOriginalConversation"
        : notice === "changed" ? "selectionChip.locationChanged" : "selectionChip.locationUnavailable")}
    </p> : null;
  }

  function cancelClose(): void {
    clearTimeout(closeTimer.current);
  }

  function close(): void {
    cancelClose();
    setOpen(false);
    setEditing(null);
  }

  function contains(target: EventTarget | null): boolean {
    return target instanceof Node && Boolean(chipRef.current?.contains(target) || panelRef.current?.contains(target));
  }

  function scheduleClose(): void {
    cancelClose();
    // Bridge the gap between the trigger and its portaled panel.
    closeTimer.current = setTimeout(() => {
      if (!contains(document.activeElement)) close();
    }, 180);
  }

  useEffect(() => () => clearTimeout(closeTimer.current), []);
  useEffect(() => {
    if (!open) return;
    const dismiss = (event: PointerEvent): void => {
      if (!contains(event.target)) close();
    };
    const escape = (event: KeyboardEvent): void => {
      if (event.key !== "Escape") return;
      event.preventDefault();
      event.stopPropagation();
      if (panelRef.current?.contains(document.activeElement)) anchorRef.current?.focus({ preventScroll: true });
      close();
    };
    document.addEventListener("pointerdown", dismiss, true);
    document.addEventListener("keydown", escape, true);
    return () => {
      document.removeEventListener("pointerdown", dismiss, true);
      document.removeEventListener("keydown", escape, true);
    };
  }, [open]);

  if (parts.length === 0) return null;
  return (
    <div className="file-selection-cards">
      <div ref={chipRef} className={allQuotes ? "file-selection-quote-chip" : undefined}>
      <button
        ref={anchorRef}
        type="button"
        className="file-selection-tag"
        aria-expanded={open}
        aria-controls={open ? panelID : undefined}
        aria-haspopup="dialog"
        onPointerEnter={(event) => { if (event.pointerType !== "touch") { cancelClose(); setOpen(true); } }}
        onPointerLeave={scheduleClose}
        onFocus={() => { cancelClose(); setOpen(true); }}
        onBlur={(event) => { if (!contains(event.relatedTarget)) close(); }}
        onClick={() => { cancelClose(); setOpen(true); }}
        onKeyDown={(event) => {
          if (event.key === "Tab" && !event.shiftKey && open) {
            const first = panelRef.current?.querySelector<HTMLElement>("button, summary") ?? (allQuotes ? panelRef.current : null);
            if (first) { event.preventDefault(); first.focus(); }
          }
        }}
      >
        {allQuotes ? <>
          <span className="file-selection-quote-icon" aria-hidden="true"><MessageSquare /></span>
          <span className="file-selection-quote-summary">
            <strong>{parts.length === 1 ? parts[0].source.quote.replace(/\s+/g, " ").trim() : label}</strong>
            <small>{label}</small>
          </span>
        </> : <><MessageSquare aria-hidden="true" /><span>{label}</span></>}
      </button>
      {allQuotes && onRemove ? <button
        type="button"
        className="file-selection-quote-remove"
        aria-label={locale === "zh-CN" ? "移除已选文本片段" : "Remove selected text fragments"}
        onClick={() => onRemove(parts.length === 1 ? parts[0].id : parts.map((part) => part.id))}
      ><X aria-hidden="true" /></button> : null}
      </div>
      {open ? (
        <FloatingMenuPortal anchorRef={anchorRef} owner="composer-attach" placement="above" align="left" width={420} boundarySelector=".composer-frame, .composer-frame-shell, .composer-stack, .conversation-pane, .side-thread-panel, .workspace-file-layout" flip>
          <div
            ref={panelRef}
            id={panelID}
            className={`file-selection-panel${allQuotes ? " file-selection-quote-panel" : ""}`}
            role="dialog"
            tabIndex={-1}
            aria-label={label}
            onPointerEnter={cancelClose}
            onPointerLeave={scheduleClose}
            onFocus={cancelClose}
            onBlur={(event) => { if (!contains(event.relatedTarget)) close(); }}
          >
            {allQuotes ? parts.map((part) => <div className="file-selection-quote-entry" key={part.id}>
              <p className="file-selection-quote-text">{part.source.quote}</p>
              {onOpenFile ? <button type="button" className="file-selection-location" onClick={() => void openFile(part)}>
                {part.source.pdf ? `${part.source.path} · ${fileSelectionLocation(part.source)}` : `${part.source.path}:${part.source.start_line}`}
              </button> : null}
              {parts.length > 1 && onRemove ? <button type="button" className="file-selection-action"
                aria-label={`${t("common.remove")} ${part.source.pdf ? `${part.source.path} · ${fileSelectionLocation(part.source)}` : `${part.source.path}:${part.source.start_line}`}`}
                onClick={() => onRemove(part.id)}><X aria-hidden="true" /></button> : null}
              {locationNotice(part.id)}
            </div>) : <>
            <div className="file-selection-panel-heading">
              <span>{label}</span>
              <button type="button" className="file-selection-action" aria-label={t("common.close")} onClick={() => {
                anchorRef.current?.focus({ preventScroll: true });
                close();
              }}><X aria-hidden="true" /></button>
            </div>
            {parts.map((part) => {
              const { source } = part;
              const filename = source.path.split(/[\\/]/).pop() || source.path;
              const location = fileSelectionLocation(source);
              return (
                <section key={part.id} className="file-selection-card">
                  <div className="file-selection-card-heading">
                    <FileText aria-hidden="true" />
                    {onOpenFile ? (
                      <button type="button" className="file-selection-location" onClick={() => void openFile(part)}>
                        {filename}<span>{location}</span>
                      </button>
                    ) : <span className="file-selection-location">{filename}<span>{location}</span></span>}
                    {onEdit ? <button type="button" className="file-selection-action" aria-label={`${t("common.edit")} ${filename} ${location}`} onClick={() => setEditing({ id: part.id, comment: part.comment ?? "" })}><Pencil aria-hidden="true" /></button> : null}
                    {onRemove ? <button type="button" className="file-selection-action" aria-label={`${t("common.remove")} ${filename} ${location}`} onClick={() => onRemove(part.id)}><X aria-hidden="true" /></button> : null}
                  </div>
                  {source.path !== filename ? <div className="file-selection-path">{source.path}</div> : null}
                  {editing?.id === part.id && onEdit ? (
                    <div className="file-selection-comment-editor">
                      <textarea
                        autoFocus
                        wrap="soft"
                        aria-label={locale === "zh-CN" ? "评论" : "Comment"}
                        value={editing.comment}
                        onChange={(event) => setEditing({ id: part.id, comment: event.target.value })}
                      />
                      <div className="file-selection-comment-actions">
                        <button type="button" onClick={() => setEditing(null)}>{t("common.cancel")}</button>
                        <button type="button" onClick={() => {
                          onEdit(part, editing.comment);
                          setEditing(null);
                        }}>{t("common.save")}</button>
                      </div>
                    </div>
                  ) : part.comment ? <p className="file-selection-comment">{part.comment}</p> : null}
                  {locationNotice(part.id)}
                  <details className="file-selection-original">
                    <summary>{locale === "zh-CN" ? "原文" : "Original"}</summary>
                    <pre>{source.quote}</pre>
                  </details>
                </section>
              );
            })}
            </>}
          </div>
        </FloatingMenuPortal>
      ) : null}
    </div>
  );
}
