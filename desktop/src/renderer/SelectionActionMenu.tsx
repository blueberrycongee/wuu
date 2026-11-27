import { forwardRef, type CSSProperties, type ReactNode, type Ref } from "react";
import { isComposerTextComposing } from "./ComposerSlashCommands";
import "./SelectionActionMenu.css";

type SelectionActionMenuProps = {
  className?: string;
  style?: CSSProperties;
  label: string;
  quoteLabel: string;
  submitLabel: string;
  commentPlaceholder: string;
  commenting: boolean;
  comment: string;
  allowEmptyComment?: boolean;
  onCommentChange: (value: string) => void;
  onQuote: () => void;
  onCommentCancel: () => void;
  onCommentSubmit: () => void;
  commentToggleRef?: Ref<HTMLButtonElement>;
  commentInputRef?: Ref<HTMLTextAreaElement>;
  extraActions?: ReactNode;
  status?: ReactNode;
};

export const SelectionActionMenu = forwardRef<HTMLDivElement, SelectionActionMenuProps>(function SelectionActionMenu({
  className, style, label, quoteLabel, submitLabel, commentPlaceholder, commenting, comment, allowEmptyComment = true,
  onCommentChange, onQuote, onCommentCancel, onCommentSubmit,
  commentToggleRef, commentInputRef, extraActions, status,
}, ref) {
  return <div ref={ref} className={`selection-action-menu${commenting ? " selection-action-menu-commenting" : ""}${className ? ` ${className}` : ""}`}
    style={style} role="toolbar" aria-label={label}>
    <div className="selection-action-menu-controls">
      {commenting ? <>
        <textarea ref={commentInputRef} className="selection-action-comment-input" rows={1} wrap="soft"
          aria-label={commentPlaceholder} placeholder={commentPlaceholder} value={comment}
          onChange={event => onCommentChange(event.target.value)}
          onKeyDown={event => {
            if (isComposerTextComposing(event)) return;
            if (event.key === "Escape") {
              event.preventDefault();
              event.stopPropagation();
              onCommentCancel();
            } else if (event.key === "Enter" && !event.shiftKey) {
              event.preventDefault();
              if (!event.repeat && (allowEmptyComment || comment.trim())) onCommentSubmit();
            }
          }} />
        <button type="button" className="selection-action-comment-submit"
          disabled={!allowEmptyComment && !comment.trim()} onPointerDown={event => event.preventDefault()} onClick={onCommentSubmit}>
          {submitLabel}
        </button>
      </> : <>
        <button ref={commentToggleRef} type="button" className="selection-action-comment-toggle"
          onPointerDown={event => event.preventDefault()} onClick={onQuote}>{quoteLabel}</button>
        {extraActions}
      </>}
    </div>
    {status}
  </div>;
});
