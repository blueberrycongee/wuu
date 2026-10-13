import { useEffect, useRef, useState } from "react";
import type { ResponseSelection } from "../shared/protocol";
import { ComposerDocumentCard } from "./ComposerDocumentCard";
import { FloatingMenuPortal } from "./ComposerFloatingMenu";
import { useI18n } from "./i18n";
import { navigateToResponseSelection } from "./ResponseSelection";
import { TruncatedText } from "./TruncatedText";
import { CornerUpLeft, Quote } from "./WuuIcons";
import "./ComposerResponseSelectionCard.css";

function singleLine(text: string): string {
  return text.replace(/\s+/g, " ").trim();
}

/** The established quote attachment, shared by response and file excerpts. */
export function ComposerQuoteCard({ text, comment = "", meta, className, notice, showComment = true, onChangeComment, onRemove, onOpenSource }: {
  text: string;
  comment?: string;
  meta?: string;
  className?: string;
  notice?: string | null;
  showComment?: boolean;
  onChangeComment?: (comment: string) => void;
  onRemove?: () => void;
  onOpenSource?: () => boolean | Promise<boolean>;
}): JSX.Element {
  const { t } = useI18n();
  const [open, setOpen] = useState(false);
  const anchorRef = useRef<HTMLDivElement>(null);
  const panelRef = useRef<HTMLDivElement>(null);
  function close(): void {
    setOpen(false);
    anchorRef.current?.querySelector<HTMLButtonElement>(".composer-document-card-main")?.focus();
  }
  useEffect(() => {
    if (!open) return;
    // The portal starts hidden until its layout effect resolves a position.
    const focusFrame = requestAnimationFrame(() => {
      (panelRef.current?.querySelector<HTMLTextAreaElement>("textarea:not([readonly])") ?? panelRef.current)?.focus({ preventScroll: true });
    });
    const dismiss = (event: MouseEvent): void => {
      const target = event.target as Node;
      if (!anchorRef.current?.contains(target) && !panelRef.current?.contains(target)) setOpen(false);
    };
    document.addEventListener("mousedown", dismiss);
    return () => { cancelAnimationFrame(focusFrame); document.removeEventListener("mousedown", dismiss); };
  }, [open]);
  return <>
    <ComposerDocumentCard ref={anchorRef}
      className={`composer-response-selection-card${className ? ` ${className}` : ""}`}
      icon={<Quote className="icon" />}
      title={<TruncatedText as="strong" className="composer-document-card-title" text={singleLine(text)} />}
      meta={singleLine(comment) || meta || t("responseSelection.cardMeta")}
      openLabel={t("responseSelection.open")} onOpen={() => setOpen(value => !value)}
      removeLabel={t("responseSelection.remove")} onRemove={onRemove} />
    {open ? <FloatingMenuPortal anchorRef={anchorRef} owner="composer-attach"
      placement="above" align="left" width={360} boundarySelector=".composer-frame-shell, .composer-stack, .conversation-pane, .side-thread-panel" flip
      mobileSheet={{ label: t("responseSelection.quote"), onClose: close }}>
      <div ref={panelRef} className={`composer-response-selection-popover${className ? ` ${className}-popover` : ""}`} role="dialog"
        aria-label={t("responseSelection.quote")} tabIndex={-1}
        onKeyDown={event => { event.stopPropagation(); if (event.key === "Escape") { event.preventDefault(); close(); } }}>
        {meta ? <p className="composer-response-selection-meta">{meta}</p> : null}
        <blockquote className="composer-response-selection-quote">{text}</blockquote>
        {showComment ? <textarea className="composer-response-selection-comment" rows={1} wrap="soft"
          aria-label={t("responseSelection.optionalComment")} placeholder={t("responseSelection.optionalComment")}
          value={comment} readOnly={!onChangeComment} onChange={event => onChangeComment?.(event.target.value)} /> : null}
        {notice ? <p className="composer-response-selection-notice" role="status">{notice}</p> : null}
        <div className="composer-response-selection-footer">
          {onOpenSource ? <button type="button" className="composer-response-selection-source" onClick={async () => {
            if (await onOpenSource()) setOpen(false);
          }}><CornerUpLeft className="icon" aria-hidden="true" /><span>{t("responseSelection.source")}</span></button> : null}
          {onRemove ? <button type="button" className="composer-response-selection-remove" onClick={() => { setOpen(false); onRemove(); }}>{t("responseSelection.remove")}</button> : null}
        </div>
      </div>
    </FloatingMenuPortal> : null}
  </>;
}

export function ComposerResponseSelectionCard({ selection, onChange, onRemove }: {
  selection: ResponseSelection;
  onChange?: (selection: ResponseSelection) => void;
  onRemove?: () => void;
}): JSX.Element {
  return <ComposerQuoteCard text={selection.text} comment={selection.comment}
    onChangeComment={onChange ? comment => onChange({ ...selection, comment }) : undefined}
    onRemove={onRemove} onOpenSource={() => navigateToResponseSelection(selection)} />;
}
