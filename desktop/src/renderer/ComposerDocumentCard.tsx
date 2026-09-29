import type { ReactNode, Ref } from "react";
import { X } from "./WuuIcons";

/**
 * The card for attachments without a visual thumbnail — folded long pastes,
 * PDFs and quoted passages: a glyph, a title, and one meta line. The
 * title always reserves the remove control's footprint, so revealing it on
 * hover never rewraps the text.
 */
export function ComposerDocumentCard({
  ref,
  className,
  icon,
  title,
  meta,
  openLabel,
  onOpen,
  removeLabel,
  onRemove,
}: {
  /** The card box, for anchoring a panel opened from it. */
  ref?: Ref<HTMLDivElement>;
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
    <div ref={ref} className={`composer-attachment-card composer-document-card${className ? ` ${className}` : ""}`}>
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
