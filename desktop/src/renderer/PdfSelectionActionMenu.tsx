import { forwardRef, useId, type ComponentPropsWithoutRef } from "react";
import type { SelectionActionMenu as SharedSelectionActionMenu } from "./SelectionActionMenu";
import { PdfSelectionCommentComposer } from "./PdfSelectionCommentComposer";
import "./PdfSelectionActionMenu.css";

type SelectionActionMenuProps = ComponentPropsWithoutRef<typeof SharedSelectionActionMenu>;

/** The PDF owner controls source capture, dismissal, and the annotation draft. */
export const PdfSelectionActionMenu = forwardRef<HTMLDivElement, SelectionActionMenuProps>(function PdfSelectionActionMenu({
  className, style, label, addLabel, commentLabel, commentPlaceholder, commenting, comment, allowEmptyComment = true,
  onCommentChange, onAdd, onCommentStart, onCommentCancel, onCommentSubmit,
  commentToggleRef, commentInputRef, extraActions, status,
}, ref) {
  const descriptionId = useId();
  return <div ref={ref}
    className={`pdf-selection-menu${commenting ? " pdf-selection-menu--commenting" : ""}${className ? ` ${className}` : ""}`}
    style={style} role="toolbar" data-wuu-component="pdf-selection-menu" aria-label={label} aria-describedby={status ? descriptionId : undefined}>
    {commenting ? <PdfSelectionCommentComposer
      comment={comment} commentPlaceholder={commentPlaceholder} submitLabel={commentLabel}
      allowEmptyComment={allowEmptyComment} commentInputRef={commentInputRef} describedBy={status ? descriptionId : undefined}
      onCommentChange={onCommentChange} onCommentCancel={onCommentCancel} onCommentSubmit={onCommentSubmit}
    /> : <div className="pdf-selection-menu__actions" onPointerDown={event => {
      // Keep the source range painted until its action has captured it.
      if (event.button === 0) event.preventDefault();
    }}>
      <button type="button" onClick={onAdd}>{addLabel}</button>
      <button ref={commentToggleRef} type="button" className="pdf-selection-menu__comment-toggle" onClick={onCommentStart}>{commentLabel}</button>
      {extraActions}
    </div>}
    {status ? <div id={descriptionId} className="pdf-selection-menu__description">{status}</div> : null}
  </div>;
});

