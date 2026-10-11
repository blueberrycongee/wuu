import { useId, type Ref } from "react";
import { isComposerTextComposing } from "./ComposerSlashCommands";
import { ArrowUp } from "./WuuIcons";
import "./PdfSelectionCommentComposer.css";

type PdfSelectionCommentComposerProps = {
  comment: string;
  commentPlaceholder: string;
  submitLabel: string;
  description?: string;
  describedBy?: string;
  allowEmptyComment?: boolean;
  commentInputRef?: Ref<HTMLTextAreaElement>;
  onCommentChange: (value: string) => void;
  onCommentCancel: () => void;
  onCommentSubmit: () => void;
};

/** The selection owner retains the draft and decides when to clear it. */
export function PdfSelectionCommentComposer({
  comment, commentPlaceholder, submitLabel, description, describedBy, allowEmptyComment = true,
  commentInputRef, onCommentChange, onCommentCancel, onCommentSubmit,
}: PdfSelectionCommentComposerProps): JSX.Element {
  const descriptionId = useId();
  const inputDescription = [describedBy, description ? descriptionId : undefined].filter(Boolean).join(" ") || undefined;
  const canSubmit = allowEmptyComment || comment.trim().length > 0;

  return <div className="pdf-selection-comment" data-wuu-component="pdf-selection-comment" onKeyDown={event => {
    if (event.defaultPrevented || isComposerTextComposing(event)) return;
    if (event.key === "Escape") {
      event.preventDefault();
      event.stopPropagation();
      onCommentCancel();
    }
  }}>
    {description ? <span id={descriptionId} className="pdf-selection-comment__description">{description}</span> : null}
    <textarea ref={commentInputRef} className="pdf-selection-comment__input" rows={1} wrap="soft"
      aria-label={commentPlaceholder} aria-describedby={inputDescription}
      placeholder={commentPlaceholder} value={comment}
      onChange={event => onCommentChange(event.target.value)}
      onKeyDown={event => {
        if (event.defaultPrevented || isComposerTextComposing(event) || event.key !== "Enter" || event.shiftKey) return;
        event.preventDefault();
        event.stopPropagation();
        if (!event.repeat && canSubmit) onCommentSubmit();
      }} />
    <button type="button" className="pdf-selection-comment__submit" aria-label={submitLabel}
      disabled={!canSubmit} onPointerDown={event => event.preventDefault()} onClick={onCommentSubmit}>
      <ArrowUp className="icon" aria-hidden="true" />
    </button>
  </div>;
}
