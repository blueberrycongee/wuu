import { X } from "./WuuIcons";
import {
  type FormEvent as ReactFormEvent,
  type KeyboardEvent as ReactKeyboardEvent,
  type MouseEvent as ReactMouseEvent,
  type ReactNode,
  useCallback,
  useEffect,
  useRef,
} from "react";
import { createPortal } from "react-dom";
import { useI18n } from "./i18n";
import { useUILayerHost } from "./ui/layers/UILayerHost";

/**
 * Shared chrome for the application's environment-style dialogs
 * (fork picker, git commit/PR, destructive confirmations). Centralizes the
 * backdrop, header, focus, Esc, and backdrop-click dismissal
 * behavior so each dialog only owns its body and footer.
 *
 * Close model:
 * - Pass `onClose` to opt into X button + Escape + backdrop dismissal.
 * - Omit `onClose` to render a non-dismissible dialog.
 * - `closeDisabled` temporarily locks down every close affordance
 *   while an in-flight promise is still resolving.
 *
 * Form model:
 * - By default the panel is a `<div>`. Set `asForm` to render a
 *   `<form>` instead so submit buttons inside `footer` (or anywhere
 *   inside `children`) participate in form submission. Modal calls
 *   `event.preventDefault()` before forwarding submit, mirroring the
 *   previous hand-rolled forms.
 */
export type ModalProps = {
  ariaLabel: string;
  icon?: ReactNode;
  title?: ReactNode;
  subtitle?: ReactNode;
  onClose?: () => void;
  closeDisabled?: boolean;
  showCloseButton?: boolean;
  panelClassName?: string;
  initialFocus?: "first-interactive" | "none";
  asForm?: boolean;
  onSubmit?: (event: ReactFormEvent<HTMLFormElement>) => void;
  footer?: ReactNode;
  children?: ReactNode;
};

export function Modal({
  ariaLabel,
  icon,
  title,
  subtitle,
  onClose,
  closeDisabled = false,
  showCloseButton = true,
  panelClassName,
  initialFocus = "first-interactive",
  asForm = false,
  onSubmit,
  footer,
  children,
}: ModalProps): JSX.Element | null {
  const { t } = useI18n();
  const layerHost = useUILayerHost();
  const panelRef = useRef<HTMLElement | null>(null);
  const dismissible = typeof onClose === "function" && !closeDisabled;

  const setPanelRef = useCallback((node: HTMLElement | null) => {
    panelRef.current = node;
  }, []);

  useEffect(() => {
    if (initialFocus !== "first-interactive") {
      return;
    }
    const panel = panelRef.current;
    if (!panel) {
      return;
    }
    // Skip the dialog's own X button (it always lives in the header
    // and has class `icon-button`). Otherwise land on the first
    // usable form control / button inside the panel.
    const target = panel.querySelector<HTMLElement>(
      "input:not([type=hidden]):not(:disabled), " +
        "textarea:not(:disabled), " +
        "select:not(:disabled), " +
        "button:not(.icon-button):not(:disabled)",
    );
    target?.focus();
  }, [initialFocus]);

  useEffect(() => {
    if (!onClose) {
      return;
    }
    function handleKeyDown(event: KeyboardEvent): void {
      if (event.key !== "Escape") {
        return;
      }
      if (closeDisabled) {
        return;
      }
      event.preventDefault();
      onClose?.();
    }
    window.addEventListener("keydown", handleKeyDown);
    return () => {
      window.removeEventListener("keydown", handleKeyDown);
    };
  }, [onClose, closeDisabled]);

  function handleBackdropClick(): void {
    if (!dismissible) {
      return;
    }
    onClose?.();
  }

  function stopClickBubble(event: ReactMouseEvent<HTMLElement>): void {
    // Clicks inside the panel must not reach the backdrop, otherwise
    // backdrop dismissal fires when the user is interacting with the
    // dialog body (selections, copy, button presses that don't stop
    // propagation themselves).
    event.stopPropagation();
  }

  function handlePanelKeyDown(event: ReactKeyboardEvent<HTMLElement>): void {
    // Keep dialog keystrokes away from app-level shortcuts, but handle Escape
    // before stopping propagation so a focused input can still dismiss.
    event.stopPropagation();
    if (event.key !== "Escape" || !dismissible) {
      return;
    }
    event.preventDefault();
    onClose?.();
  }

  const panelClass = ["environment-dialog", panelClassName]
    .filter(Boolean)
    .join(" ");

  const backdropClass = "app-modal-backdrop modal-backdrop environment-modal-backdrop";

  const panelBody = (
    <>
      {title || icon || (onClose && showCloseButton) ? (
        <div className="environment-dialog-header">
          {icon ? <span className="environment-dialog-icon">{icon}</span> : <span />}
          {onClose && showCloseButton ? (
          <button
            className="icon-button"
            type="button"
            aria-label={t("common.close")}
            disabled={closeDisabled}
            onClick={() => {
              if (!closeDisabled) {
                onClose();
              }
            }}
          >
            <X className="icon" />
          </button>
          ) : null}
        </div>
      ) : null}
      {title ? <h2>{title}</h2> : null}
      {subtitle ? (
        <p className="environment-dialog-subtitle">{subtitle}</p>
      ) : null}
      {children}
      {footer ? <div className="environment-dialog-footer">{footer}</div> : null}
    </>
  );

  const content = (
    <div
      className={backdropClass}
      data-wuu-component="modal-backdrop"
      data-wuu-layer="modal"
      data-wuu-state={closeDisabled ? "locked" : "open"}
      onClick={handleBackdropClick}
      role="presentation"
    >
      {asForm ? (
        <form
          ref={setPanelRef}
          className={panelClass}
          data-wuu-component="dialog"
          data-wuu-dismissible={onClose ? "true" : "false"}
          data-wuu-state={closeDisabled ? "locked" : "open"}
          role="dialog"
          aria-modal="true"
          aria-label={ariaLabel}
          onClick={stopClickBubble}
          onKeyDown={handlePanelKeyDown}
          onSubmit={(event) => {
            event.preventDefault();
            onSubmit?.(event);
          }}
        >
          {panelBody}
        </form>
      ) : (
        <div
          ref={setPanelRef}
          className={panelClass}
          data-wuu-component="dialog"
          data-wuu-dismissible={onClose ? "true" : "false"}
          data-wuu-state={closeDisabled ? "locked" : "open"}
          role="dialog"
          aria-modal="true"
          aria-label={ariaLabel}
          onClick={stopClickBubble}
          onKeyDown={handlePanelKeyDown}
        >
          {panelBody}
        </div>
      )}
    </div>
  );

  return createPortal(content, layerHost);
}
