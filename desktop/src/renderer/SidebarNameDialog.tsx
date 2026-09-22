import { type IconComponent } from "./WuuIcons";
import {
  type AnimationEvent,
  type ChangeEvent,
  useCallback,
  useEffect,
  type MouseEvent,
  type ReactElement,
  type ReactNode,
  useRef,
  useState,
} from "react";
import { UILayerPortal } from "./ui/layers/UILayerHost";

export interface SidebarNameDialogProps {
  open: boolean;
  title: string;
  onTitleChange: (title: string) => void;
  onSubmit: () => void;
  onClose: () => void;
  dialogTitle: string;
  dialogTitleId: string;
  fieldLabel: string;
  fieldAriaLabel: string;
  placeholder: string;
  icon: IconComponent;
  submitLabel: string;
  cancelLabel: string;
  content?: ReactNode;
  submitDisabled?: boolean;
  destructiveAction?: {
    label: string;
    onClick: () => void;
    disabled?: boolean;
  };
  secondaryAction?: {
    label: string;
    onClick: () => void;
    disabled?: boolean;
  };
  variant?: "default" | "drawer";
  hideActions?: boolean;
  closeOnEscape?: boolean;
  backgrounded?: boolean;
  dialogClassName?: string;
}

// Shared floating name dialog for the sidebar flows that need a single text
// input (for example, renaming a conversation). Same visual shell as
// the conversation-search overlay so the product stays consistent.
export function SidebarNameDialog({
  open,
  title,
  onTitleChange,
  onSubmit,
  onClose,
  dialogTitle,
  dialogTitleId,
  fieldLabel,
  fieldAriaLabel,
  placeholder,
  icon: Icon,
  submitLabel,
  cancelLabel,
  content,
  submitDisabled,
  destructiveAction,
  secondaryAction,
  variant = "default",
  hideActions = false,
  closeOnEscape = true,
  backgrounded = false,
  dialogClassName = "",
}: SidebarNameDialogProps): ReactElement | null {
  const [closing, setClosing] = useState(false);
  const closeTimerRef = useRef<number | null>(null);

  const finishClose = useCallback((): void => {
    if (closeTimerRef.current !== null) {
      window.clearTimeout(closeTimerRef.current);
      closeTimerRef.current = null;
    }
    onClose();
    setClosing(false);
  }, [onClose]);

  const requestClose = useCallback((): void => {
    if (closing) return;
    if (variant !== "drawer") {
      onClose();
      return;
    }
    setClosing(true);
    closeTimerRef.current = window.setTimeout(finishClose, 220);
  }, [closing, finishClose, onClose, variant]);

  useEffect(() => () => {
    if (closeTimerRef.current !== null) window.clearTimeout(closeTimerRef.current);
  }, []);

  useEffect(() => {
    if (!open || !closeOnEscape) {
      return;
    }
    function handleKeyDown(event: KeyboardEvent): void {
      if (event.key !== "Escape") {
        return;
      }
      event.preventDefault();
      requestClose();
    }
    window.addEventListener("keydown", handleKeyDown);
    return () => window.removeEventListener("keydown", handleKeyDown);
  }, [closeOnEscape, open, requestClose]);

  if (!open) {
    return null;
  }

  function handleOverlayPointerDown(event: MouseEvent<HTMLDivElement>): void {
    if (event.target === event.currentTarget) {
      requestClose();
    }
  }

  function handleDrawerAnimationEnd(event: AnimationEvent<HTMLFormElement>): void {
    if (closing && event.target === event.currentTarget) finishClose();
  }

  function handleInputChange(event: ChangeEvent<HTMLInputElement>): void {
    onTitleChange(event.currentTarget.value);
  }

  return (
    <UILayerPortal layer="dialog">
      <div
        className={`app-modal-backdrop conversation-search-overlay sidebar-name-dialog-overlay${variant === "drawer" ? " sidebar-name-dialog-overlay-drawer" : ""}${closing ? " closing" : ""}${backgrounded ? " backgrounded" : ""}`}
        data-wuu-component="modal-backdrop"
        data-wuu-layer="dialog"
        data-wuu-state={closing ? "closing" : "open"}
        onPointerDown={handleOverlayPointerDown}
      >
        <form
          className={`conversation-search-dialog sidebar-name-dialog${variant === "drawer" ? " sidebar-name-dialog-drawer" : ""}${closing ? " closing" : ""}${backgrounded ? " backgrounded" : ""}${dialogClassName ? ` ${dialogClassName}` : ""}`}
          data-wuu-component="dialog"
          data-wuu-state={closing ? "closing" : "open"}
          role="dialog"
          aria-modal={backgrounded ? undefined : "true"}
          aria-hidden={backgrounded || undefined}
          aria-labelledby={dialogTitleId}
          onAnimationEnd={handleDrawerAnimationEnd}
          onSubmit={(event) => {
            event.preventDefault();
            onSubmit();
          }}
        >
          <div className="sidebar-name-dialog-header">
            <span className="sidebar-name-dialog-icon" aria-hidden="true">
              <Icon className="icon-lg" />
            </span>
            <h2 id={dialogTitleId} className="sidebar-name-dialog-title">
              {dialogTitle}
            </h2>
          </div>
          {content ?? <label className="sidebar-name-dialog-field">
            <span className="sidebar-name-dialog-label">{fieldLabel}</span>
            <input
              className="sidebar-name-dialog-input"
              value={title}
              aria-label={fieldAriaLabel}
              placeholder={placeholder}
              autoFocus
              onChange={handleInputChange}
              onFocus={(event) => event.currentTarget.select()}
            />
          </label>}
          {!hideActions ? <div className="sidebar-name-dialog-actions">
            {destructiveAction ? (
              <button className="sidebar-name-dialog-destructive" type="button" disabled={destructiveAction.disabled} onClick={destructiveAction.onClick}>
                {destructiveAction.label}
              </button>
            ) : null}
            {secondaryAction ? (
              <button className="sidebar-name-dialog-secondary" type="button" disabled={secondaryAction.disabled} onClick={secondaryAction.onClick}>
                {secondaryAction.label}
              </button>
            ) : null}
            {destructiveAction || secondaryAction ? <span className="sidebar-name-dialog-action-spacer" aria-hidden="true" /> : null}
            <button type="button" onClick={requestClose}>
              {cancelLabel}
            </button>
            <button type="submit" disabled={submitDisabled ?? title.trim().length === 0}>
              {submitLabel}
            </button>
          </div> : null}
        </form>
      </div>
    </UILayerPortal>
  );
}
