import { forwardRef, type CSSProperties, type ReactNode, type Ref } from "react";
import { isComposerTextComposing } from "./ComposerSlashCommands";
import { ArrowUp } from "./WuuIcons";
import "./SelectionActionMenu.css";

type SelectionActionMenuProps = {
  className?: string;
  style?: CSSProperties;
  label: string;
  addLabel: string;
  commentLabel: string;
  commentPlaceholder: string;
  commenting: boolean;
  comment: string;
  allowEmptyComment?: boolean;
  onCommentChange: (value: string) => void;
  onAdd: () => void;
  onCommentStart: () => void;
  onCommentCancel: () => void;
  onCommentSubmit: () => void;
  commentToggleRef?: Ref<HTMLButtonElement>;
  commentInputRef?: Ref<HTMLTextAreaElement>;
  commentMarkerPlacement?: "before" | "after";
  extraActions?: ReactNode;
  status?: ReactNode;
};

export const SelectionActionMenu = forwardRef<HTMLDivElement, SelectionActionMenuProps>(function SelectionActionMenu({
  className, style, label, addLabel, commentLabel, commentPlaceholder, commenting, comment, allowEmptyComment = true,
  onCommentChange, onAdd, onCommentStart, onCommentCancel, onCommentSubmit,
  commentToggleRef, commentInputRef, commentMarkerPlacement = "after", extraActions, status,
}, ref) {
  return <div ref={ref} className={`selection-action-menu${commenting ? " selection-action-menu-commenting" : ""}${className ? ` ${className}` : ""}`}
    style={style} role="toolbar" aria-label={label}>
    <div className="selection-action-menu-controls">
      {commenting ? <>
        <textarea ref={commentInputRef} className="selection-action-comment-input" rows={1} wrap="soft"
          aria-label={commentPlaceholder} placeholder={commentPlaceholder} value={comment}
          onChange={event => onCommentChange(event.target.value)}
          onKeyDown={event => {
            if (event.key === "Escape") {
              event.preventDefault();
              event.stopPropagation();
              onCommentCancel();
            } else if (event.key === "Enter" && !event.shiftKey && !isComposerTextComposing(event)) {
              event.preventDefault();
              if (comment.trim() || allowEmptyComment) onCommentSubmit();
            }
          }} />
        <button type="button" className="selection-action-comment-submit" aria-label={commentLabel}
          disabled={!allowEmptyComment && !comment.trim()} onPointerDown={event => event.preventDefault()} onClick={onCommentSubmit}>
          <ArrowUp className="icon" aria-hidden="true" />
        </button>
      </> : <>
        <button type="button" onPointerDown={event => event.preventDefault()} onClick={onAdd}>{addLabel}</button>
        <button ref={commentToggleRef} type="button" className="selection-action-comment-toggle"
          onPointerDown={event => event.preventDefault()} onClick={onCommentStart}>{commentLabel}</button>
        {extraActions}
      </>}
    </div>
    {commenting ? <span className={`selection-action-comment-marker selection-action-comment-marker-${commentMarkerPlacement}`} aria-hidden="true">1</span> : null}
    {status}
  </div>;
});
