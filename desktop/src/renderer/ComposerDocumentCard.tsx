import type { ReactNode } from "react";
import { X } from "./WuuIcons";

/**
 * The card for attachments without a visual thumbnail — folded long pastes
 * and PDFs: a glyph tile, a title, and one meta line. The title always
 * reserves the remove control's footprint, so revealing it on hover never
 * rewraps the text.
 */
export function ComposerDocumentCard({
  className,
  icon,
  title,
  meta,
  openLabel,
  onOpen,
  removeLabel,
  onRemove,
}: {
  className?: string;
  icon: ReactNode;
  title: ReactNode;
  meta: ReactNode;
  /** Accessible name of the card body when it is actionable. */
  openLabel?: string;
  onOpen?: () => void;
  removeLabel: string;
  onRemove: () => void;
}): JSX.Element {
  const body = (
    <>
      <span className="composer-document-card-icon" aria-hidden="true">{icon}</span>
      <span className="composer-document-card-text">
        {title}
        <span className="composer-document-card-meta">{meta}</span>
      </span>
    </>
  );
  return (
    <div className={`composer-attachment-card composer-document-card${className ? ` ${className}` : ""}`}>
      {onOpen ? (
        <button className="composer-document-card-main" type="button" aria-label={openLabel} onClick={onOpen}>
          {body}
        </button>
      ) : (
        <div className="composer-document-card-main">{body}</div>
      )}
      <button className="composer-attachment-card-remove" type="button" aria-label={removeLabel} onClick={onRemove}>
        <X aria-hidden="true" />
      </button>
    </div>
  );
}
