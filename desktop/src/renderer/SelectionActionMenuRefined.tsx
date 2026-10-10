import { forwardRef, useId, type ComponentPropsWithoutRef } from "react";
import type { SelectionActionMenu as BaselineSelectionActionMenu } from "./SelectionActionMenu";
import { SelectionCommentComposerRefined } from "./SelectionCommentComposerRefined";
import "./SelectionActionMenuRefined.css";

type SelectionActionMenuProps = ComponentPropsWithoutRef<typeof BaselineSelectionActionMenu>;

/** Comparison candidate. Source capture, dismissal, and draft lifetime stay with
 * the caller so changing the presentation cannot change the annotation target. */
export const SelectionActionMenuRefined = forwardRef<HTMLDivElement, SelectionActionMenuProps>(function SelectionActionMenuRefined({
  className, style, label, addLabel, commentLabel, commentPlaceholder, commenting, comment, allowEmptyComment = true,
  onCommentChange, onAdd, onCommentStart, onCommentCancel, onCommentSubmit,
  commentToggleRef, commentInputRef, extraActions, status,
}, ref) {
  const descriptionId = useId();
  return <div ref={ref}
    className={`selection-action-refined${commenting ? " selection-action-refined--commenting" : ""}${className ? ` ${className}` : ""}`}
    style={style} role="toolbar" aria-label={label} aria-describedby={status ? descriptionId : undefined}>
    {commenting ? <SelectionCommentComposerRefined
      comment={comment} commentPlaceholder={commentPlaceholder} submitLabel={commentLabel}
      allowEmptyComment={allowEmptyComment} commentInputRef={commentInputRef} describedBy={status ? descriptionId : undefined}
      onCommentChange={onCommentChange} onCommentCancel={onCommentCancel} onCommentSubmit={onCommentSubmit}
    /> : <div className="selection-action-refined__actions" onPointerDown={event => {
      // Keep the source range painted until its action has captured it.
      if (event.button === 0) event.preventDefault();
    }}>
      <button type="button" onClick={onAdd}>{addLabel}</button>
      <button ref={commentToggleRef} type="button" className="selection-action-refined__comment-toggle" onClick={onCommentStart}>{commentLabel}</button>
      {extraActions}
    </div>}
    {status ? <div id={descriptionId} className="selection-action-refined__description">{status}</div> : null}
  </div>;
});

// The comparison preview resolves only its PDF adapter to this alternative.
export { SelectionActionMenuRefined as SelectionActionMenu };
