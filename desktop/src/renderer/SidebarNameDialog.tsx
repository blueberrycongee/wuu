import { type ReactElement } from "react";
import { Modal } from "./Modal";

interface SidebarNameDialogProps {
  open: boolean;
  title: string;
  onTitleChange: (title: string) => void;
  onSubmit: () => void;
  onClose: () => void;
  dialogTitle: string;
  /** The field's accessible name; the dialog title already says what to enter. */
  fieldLabel: string;
  placeholder?: string;
  submitLabel: string;
  cancelLabel: string;
  submitDisabled?: boolean;
}

// Single-field naming dialog for the sidebar (new folder, rename folder,
// rename conversation), on the shared Modal chrome and field and button roles.
export function SidebarNameDialog({
  open,
  title,
  onTitleChange,
  onSubmit,
  onClose,
  dialogTitle,
  fieldLabel,
  placeholder,
  submitLabel,
  cancelLabel,
  submitDisabled,
}: SidebarNameDialogProps): ReactElement | null {
  if (!open) {
    return null;
  }
  return (
    <Modal
      ariaLabel={dialogTitle}
      title={dialogTitle}
      onClose={onClose}
      showCloseButton={false}
      asForm
      onSubmit={onSubmit}
      panelClassName="name-dialog"
      footer={(
        <>
          <button type="button" className="settings-button settings-button-ghost" onClick={onClose}>
            {cancelLabel}
          </button>
          <button
            type="submit"
            className="settings-button settings-button-primary"
            disabled={submitDisabled ?? title.trim().length === 0}
          >
            {submitLabel}
          </button>
        </>
      )}
    >
      <input
        className="settings-input"
        value={title}
        aria-label={fieldLabel}
        placeholder={placeholder}
        spellCheck={false}
        onChange={(event) => onTitleChange(event.currentTarget.value)}
        onFocus={(event) => event.currentTarget.select()}
      />
    </Modal>
  );
}
