import { useEffect, useRef, useState } from "react";
import type { ResponseSelection } from "../shared/protocol";
import { FloatingMenuPortal } from "./ComposerFloatingMenu";
import { useI18n } from "./i18n";
import { navigateToResponseSelection } from "./ResponseSelection";
import { MessageSquare, X } from "./WuuIcons";
import "./ComposerResponseSelectionCard.css";

export function ComposerResponseSelectionCard({ selections, onChange, onRemove }: {
  selections: ResponseSelection[];
  onChange?: (selection: ResponseSelection) => void;
  onRemove?: (id: string) => void;
}): JSX.Element | null {
  const { t } = useI18n();
  const [open, setOpen] = useState(false);
  const anchorRef = useRef<HTMLDivElement>(null);
  const panelRef = useRef<HTMLDivElement>(null);

  function close(): void {
    setOpen(false);
    anchorRef.current?.querySelector<HTMLButtonElement>("button")?.focus();
  }

  useEffect(() => {
    if (!open) return;
    panelRef.current?.focus();
    const dismiss = (event: MouseEvent): void => {
      const target = event.target as Node;
      if (!anchorRef.current?.contains(target) && !panelRef.current?.contains(target)) setOpen(false);
    };
    document.addEventListener("mousedown", dismiss);
    return () => document.removeEventListener("mousedown", dismiss);
  }, [open]);

  if (selections.length === 0) return null;
  const countLabel = t(selections.length === 1 ? "responseSelection.countOne" : "responseSelection.countMany", { count: selections.length });

  return <>
    <div ref={anchorRef} className="composer-response-selection-anchor">
      <button type="button" className="composer-response-selection-chip" aria-haspopup="dialog"
        aria-expanded={open} onClick={() => setOpen((value) => !value)}>
        <MessageSquare className="icon" aria-hidden="true" />
        <span>{countLabel}</span>
      </button>
    </div>
    {open ? <FloatingMenuPortal anchorRef={anchorRef} owner="composer-attach"
      placement="above" align="left" width={380} flip
      mobileSheet={{ label: t("responseSelection.quote"), onClose: close }}>
      <div ref={panelRef} className="composer-response-selection-popover" role="dialog"
        aria-label={t("responseSelection.quote")} tabIndex={-1}
        onKeyDown={(event) => {
          event.stopPropagation();
          if (event.key === "Escape") { event.preventDefault(); close(); }
        }}>
        <div className="composer-response-selection-actions">
          <span>{countLabel}</span>
          <button type="button" className="icon-button" aria-label={t("common.close")} onClick={close}>
            <X className="icon" />
          </button>
        </div>
        <div className="composer-response-selection-list">
          {selections.map((selection) => <section key={selection.id} data-selection-id={selection.id}
            className="composer-response-selection-item">
            <blockquote>{selection.text}</blockquote>
            <textarea className="settings-input composer-response-selection-comment"
              aria-label={t("responseSelection.optionalComment")} placeholder={t("responseSelection.optionalComment")}
              value={selection.comment ?? ""} rows={2} readOnly={!onChange}
              onChange={(event) => onChange?.({ ...selection, comment: event.target.value })} />
            <div className="composer-response-selection-actions">
              <button type="button" className="composer-response-selection-source" onClick={() => {
                if (navigateToResponseSelection(selection)) setOpen(false);
              }}>{t("responseSelection.source")}</button>
              <button type="button" className="icon-button composer-response-selection-remove"
                disabled={!onRemove} aria-label={t("responseSelection.remove")} onClick={() => onRemove?.(selection.id)}>
                <X className="icon" />
              </button>
            </div>
          </section>)}
        </div>
      </div>
    </FloatingMenuPortal> : null}
  </>;
}
