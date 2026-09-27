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

/**
 * One quoted passage waiting in the composer's attachment tray. The card
 * reads like the other document cards; opening it shows the full quote, the
 * optional comment, and source navigation in a floating panel.
 */
export function ComposerResponseSelectionCard({ selection, onChange, onRemove }: {
  selection: ResponseSelection;
  /** Absent while the composer is read-only; the comment then shows as-is. */
  onChange?: (selection: ResponseSelection) => void;
  onRemove: () => void;
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
    (panelRef.current?.querySelector<HTMLTextAreaElement>("textarea:not([readonly])") ?? panelRef.current)?.focus();
    const dismiss = (event: MouseEvent): void => {
      const target = event.target as Node;
      if (!anchorRef.current?.contains(target) && !panelRef.current?.contains(target)) setOpen(false);
    };
    document.addEventListener("mousedown", dismiss);
    return () => document.removeEventListener("mousedown", dismiss);
  }, [open]);

  const comment = singleLine(selection.comment ?? "");
  return <>
    <ComposerDocumentCard
      ref={anchorRef}
      className="composer-response-selection-card"
      icon={<Quote className="icon" />}
      title={<TruncatedText as="strong" className="composer-document-card-title" text={singleLine(selection.text)} />}
      meta={comment || t("responseSelection.cardMeta")}
      openLabel={t("responseSelection.open")}
      onOpen={() => setOpen((value) => !value)}
      removeLabel={t("responseSelection.remove")}
      onRemove={onRemove}
    />
    {open ? <FloatingMenuPortal anchorRef={anchorRef} owner="composer-attach"
      placement="above" align="left" width={360} flip
      mobileSheet={{ label: t("responseSelection.quote"), onClose: close }}>
      <div ref={panelRef} className="composer-response-selection-popover" role="dialog"
        aria-label={t("responseSelection.quote")} tabIndex={-1}
        onKeyDown={(event) => {
          event.stopPropagation();
          if (event.key === "Escape") { event.preventDefault(); close(); }
        }}>
        <blockquote className="composer-response-selection-quote">{selection.text}</blockquote>
        <textarea className="composer-response-selection-comment" rows={1}
          aria-label={t("responseSelection.optionalComment")} placeholder={t("responseSelection.optionalComment")}
          value={selection.comment ?? ""} readOnly={!onChange}
          onChange={(event) => onChange?.({ ...selection, comment: event.target.value })} />
        <div className="composer-response-selection-footer">
          <button type="button" className="composer-response-selection-source" onClick={() => {
            if (navigateToResponseSelection(selection)) setOpen(false);
          }}>
            <CornerUpLeft className="icon" aria-hidden="true" />
            <span>{t("responseSelection.source")}</span>
          </button>
          <button type="button" className="composer-response-selection-remove" onClick={() => { setOpen(false); onRemove(); }}>
            {t("responseSelection.remove")}
          </button>
        </div>
      </div>
    </FloatingMenuPortal> : null}
  </>;
}
