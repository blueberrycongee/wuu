import { type IconComponent } from "./WuuIcons";
import {
  type ChangeEvent,
  useEffect,
  type MouseEvent,
  type ReactElement,
} from "react";
import { UILayerPortal } from "./ui/layers/UILayerHost";

interface SidebarNameDialogProps {
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
  submitDisabled?: boolean;
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
  submitDisabled,
}: SidebarNameDialogProps): ReactElement | null {
  useEffect(() => {
    if (!open) {
      return;
    }
    function handleKeyDown(event: KeyboardEvent): void {
      if (event.key !== "Escape") {
        return;
      }
      event.preventDefault();
      onClose();
    }
    window.addEventListener("keydown", handleKeyDown);
    return () => window.removeEventListener("keydown", handleKeyDown);
  }, [onClose, open]);

  if (!open) {
    return null;
  }

  function handleOverlayPointerDown(event: MouseEvent<HTMLDivElement>): void {
    if (event.target === event.currentTarget) {
      onClose();
    }
  }

  function handleInputChange(event: ChangeEvent<HTMLInputElement>): void {
    onTitleChange(event.currentTarget.value);
  }

  return (
    <UILayerPortal layer="dialog">
      <div
        className="app-modal-backdrop conversation-search-overlay sidebar-name-dialog-overlay"
        data-wuu-component="modal-backdrop"
        data-wuu-layer="dialog"
        data-wuu-state="open"
        onPointerDown={handleOverlayPointerDown}
      >
        <form
          className="conversation-search-dialog sidebar-name-dialog"
          data-wuu-component="dialog"
          data-wuu-state="open"
          role="dialog"
          aria-modal="true"
          aria-labelledby={dialogTitleId}
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
          <label className="sidebar-name-dialog-field">
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
          </label>
          <div className="sidebar-name-dialog-actions">
            <button type="button" onClick={onClose}>
              {cancelLabel}
            </button>
            <button type="submit" disabled={submitDisabled ?? title.trim().length === 0}>
              {submitLabel}
            </button>
          </div>
        </form>
      </div>
    </UILayerPortal>
  );
}
