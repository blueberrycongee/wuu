import { useState } from "react";
import { FloatingMenuPortal } from "./ComposerFloatingMenu";
import { RefinedQuoteExcerpt, useQuoteCardPreview } from "./ComposerQuoteCardRefined";
import type { FileSelectionCardsProps, FileSelectionPart } from "./FileSelectionCards";
import { usePdfSelectionNavigation } from "./FileSelectionContext";
import { readFileSelectionNavigation } from "./FileSelectionNavigation";
import { useI18n } from "./i18n";
import { fileSelectionLocation } from "./PdfSelection";
import { Tooltip } from "./Tooltip";
import { CornerUpLeft, FileText, MessageSquare, Pencil, Quote, X } from "./WuuIcons";

/** The alternative changes presentation only; saved sources are never rewritten. */
export function SentFileSelectionCardsRefined({ parts, onRemove, onEdit, onOpenFile }: FileSelectionCardsProps): JSX.Element | null {
  if (parts.length === 0) return null;
  return <div className="quote-refined-groups">
    <SelectionGroup parts={parts.filter(part => part.intent === "quote")} onRemove={onRemove} onOpenFile={onOpenFile} />
    <SelectionGroup parts={parts.filter(part => part.intent !== "quote")} onRemove={onRemove} onEdit={onEdit} onOpenFile={onOpenFile} />
  </div>;
}

function SelectionGroup({ parts, onRemove, onEdit, onOpenFile }: FileSelectionCardsProps): JSX.Element | null {
  const { locale, t } = useI18n();
  const openPdf = usePdfSelectionNavigation();
  const [editing, setEditing] = useState<{ id: string; comment: string } | null>(null);
  const preview = useQuoteCardPreview(() => setEditing(null));
  const [locationNotices, setLocationNotices] = useState<Record<string, "changed" | "unavailable" | "original" | undefined>>({});
  const [opening, setOpening] = useState<string | null>(null);
  const allQuotes = parts.every(part => part.intent === "quote");
  const allComments = parts.every(part => part.intent === "comment");
  const allEdits = parts.every(part => part.intent === "edit");
  const label = locale === "zh-CN"
    ? `${parts.length} 个${allQuotes ? "选段" : allComments ? "评论" : allEdits ? "编辑请求" : "文件引用"}`
    : `${parts.length} ${allQuotes ? parts.length === 1 ? "selection" : "selections"
      : allComments ? parts.length === 1 ? "comment" : "comments"
      : allEdits ? parts.length === 1 ? "edit request" : "edit requests"
      : parts.length === 1 ? "file reference" : "file references"}`;

  async function openSource(part: FileSelectionPart): Promise<void> {
    setOpening(part.id);
    try {
      if (part.source.pdf?.artifact_uri) {
        const opened = openPdf?.(part.source) ?? false;
        setLocationNotices(current => ({ ...current, [part.id]: opened ? undefined : "original" }));
        return;
      }
      const target = await readFileSelectionNavigation(part.source);
      setLocationNotices(current => ({ ...current, [part.id]: target.locationChanged
        ? target.available ? "changed" : "unavailable" : undefined }));
      onOpenFile?.(target.path);
    } finally { setOpening(null); }
  }

  function close(): void { preview.close(true); setEditing(null); }

  if (parts.length === 0) return null;
  return <div className="quote-refined-group" data-wuu-component="sent-quote-card" data-wuu-variant="refined">
    <div ref={preview.anchorRef} onPointerEnter={event => preview.revealOnPointer(event.pointerType)}
      onPointerLeave={preview.leave} onBlur={event => preview.blur(event.relatedTarget)}>
      <button ref={preview.triggerRef} type="button" className="quote-refined-pill"
        aria-expanded={preview.open} aria-controls={preview.open ? preview.panelId : undefined} aria-haspopup="dialog"
        onFocus={preview.revealOnFocus} onClick={preview.activate}
        onKeyDown={event => {
          if (event.key === "Tab" && !event.shiftKey && preview.open) { event.preventDefault(); preview.focusPanel(); }
        }}>
        {allQuotes ? <Quote aria-hidden="true" /> : <MessageSquare aria-hidden="true" />}<span>{label}</span>
      </button>
    </div>
    {preview.open ? <FloatingMenuPortal anchorRef={preview.anchorRef} owner="composer-attach"
      placement="above" align="left" width={360} offset={4} flip
      boundarySelector=".composer-frame, .composer-frame-shell, .composer-stack, .conversation-pane, .side-thread-panel, .workspace-file-layout"
      mobileSheet={{ label, onClose: close }}>
      <div ref={preview.panelRef} id={preview.panelId} className="quote-refined-preview"
        data-wuu-component="sent-quote-preview" role="dialog" tabIndex={-1} aria-label={label}
        onPointerEnter={preview.retain} onPointerLeave={preview.leave} onFocus={preview.retain}
        onBlur={event => preview.blur(event.relatedTarget)} onKeyDown={event => event.stopPropagation()}>
        {parts.map(part => {
          const { source } = part;
          const filename = source.path.split(/[\\/]/).pop() || source.path;
          const location = fileSelectionLocation(source);
          const canOpen = Boolean(source.pdf?.artifact_uri || onOpenFile);
          const sourceLabel = `${source.path} · ${location}`;
          const notice = locationNotices[part.id];
          const sourceContent = <><FileText aria-hidden="true" /><span>{filename}</span>
            <span className="quote-refined-location">{location}</span>{canOpen ? <CornerUpLeft aria-hidden="true" /> : null}</>;
          return <section key={part.id} className="quote-refined-entry" data-selection-id={part.id}>
            <div className="quote-refined-entry-heading">
              {canOpen ? <Tooltip content={source.path}><button type="button" className="quote-refined-source-link"
                aria-label={`${t("responseSelection.source")}: ${sourceLabel}`} disabled={opening !== null}
                onClick={() => void openSource(part)}>{sourceContent}</button></Tooltip>
                : <span className="quote-refined-source-link">{sourceContent}</span>}
              {onEdit ? <button type="button" className="quote-refined-icon-action" aria-label={`${t("common.edit")} ${sourceLabel}`}
                onClick={() => setEditing({ id: part.id, comment: part.comment ?? "" })}><Pencil aria-hidden="true" /></button> : null}
              {onRemove ? <button type="button" className="quote-refined-icon-action" aria-label={`${t("common.remove")} ${sourceLabel}`}
                onClick={() => onRemove(part.id)}><X aria-hidden="true" /></button> : null}
            </div>
            {allQuotes ? <RefinedQuoteExcerpt text={source.quote} /> : <>
              {editing?.id === part.id && onEdit ? <div className="quote-refined-editor">
                <textarea autoFocus className="quote-refined-comment-input" wrap="soft" value={editing.comment}
                  aria-label={locale === "zh-CN" ? "评论" : "Comment"}
                  onChange={event => setEditing({ id: part.id, comment: event.target.value })} />
                <div className="quote-refined-editor-actions">
                  <button type="button" onClick={() => setEditing(null)}>{t("common.cancel")}</button>
                  <button type="button" onClick={() => { onEdit(part, editing.comment); setEditing(null); }}>{t("common.save")}</button>
                </div>
              </div> : part.comment ? <p className="quote-refined-comment">{part.comment}</p> : null}
              <details className="quote-refined-original">
                <summary>{locale === "zh-CN" ? "原文" : "Original"}</summary>
                <RefinedQuoteExcerpt text={source.quote} />
              </details>
            </>}
            {notice ? <p className="quote-refined-notice" role="status">
              {t(notice === "original" ? "selectionChip.pdfOriginalConversation"
                : notice === "changed" ? "selectionChip.locationChanged" : "selectionChip.locationUnavailable")}
            </p> : null}
          </section>;
        })}
        {onRemove && parts.length > 1 ? <div className="quote-refined-footer">
          <button type="button" className="quote-refined-remove-action"
            onClick={() => { preview.close(); onRemove(parts.map(part => part.id)); }}>{t("responseSelection.remove")}</button>
        </div> : null}
      </div>
    </FloatingMenuPortal> : null}
  </div>;
}

export { SentFileSelectionCardsRefined as FileSelectionCards };
