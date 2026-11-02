import { useEffect, useRef, type MouseEvent, type ReactNode } from "react";
import { Tooltip } from "./Tooltip";
import { X } from "./WuuIcons";
import { useI18n } from "./i18n";

// A table can open an embedded image. Only the top preview owns shortcuts,
// and the page stays locked until the last preview leaves.
const previews: HTMLElement[] = [];
let pageOverflow = "";

export function ContentPreview({
  label,
  className = "",
  closeLabel,
  onClose,
  onKeyDown,
  onClick,
  children,
}: {
  label: string;
  className?: string;
  closeLabel?: string;
  onClose: () => void;
  onKeyDown?: (event: KeyboardEvent) => void;
  onClick?: (event: MouseEvent<HTMLDivElement>) => void;
  children: ReactNode;
}): JSX.Element {
  const { t } = useI18n();
  const overlayRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    const overlay = overlayRef.current!;
    const previousFocus = document.activeElement as HTMLElement | null;
    if (previews.length === 0) {
      pageOverflow = document.body.style.overflow;
      document.body.style.overflow = "hidden";
    }
    previews.push(overlay);
    overlay.style.setProperty("--content-preview-depth", String(previews.length));
    overlay.focus();
    return () => {
      const wasTop = previews.at(-1) === overlay;
      previews.splice(previews.indexOf(overlay), 1);
      if (previews.length === 0) document.body.style.overflow = pageOverflow;
      if (wasTop && previousFocus?.isConnected) previousFocus.focus({ preventScroll: true });
    };
  }, []);

  useEffect(() => {
    function keydown(event: KeyboardEvent): void {
      const overlay = overlayRef.current!;
      if (previews.at(-1) !== overlay) return;
      if (event.key === "Escape") {
        event.stopPropagation();
        event.preventDefault();
        onClose();
        return;
      }
      if (event.key === "Tab") {
        event.stopPropagation();
        const controls = Array.from(overlay.querySelectorAll<HTMLElement>(
          'button:not(:disabled), a[href], input:not(:disabled), select:not(:disabled), textarea:not(:disabled), [tabindex]:not([tabindex="-1"])',
        )).filter(element => !element.hidden && element.getAttribute("aria-hidden") !== "true");
        const index = controls.indexOf(document.activeElement as HTMLElement);
        const next = event.shiftKey ? (index <= 0 ? controls.length - 1 : index - 1) : (index + 1) % controls.length;
        event.preventDefault();
        controls[next]?.focus();
        return;
      }
      if (onKeyDown) {
        event.stopPropagation();
        onKeyDown(event);
      }
    }
    document.addEventListener("keydown", keydown, true);
    return () => document.removeEventListener("keydown", keydown, true);
  }, [onClose, onKeyDown]);

  return (
    <div ref={overlayRef} className={`content-preview-overlay ${className}`} role="dialog" aria-modal="true"
      aria-label={label} tabIndex={-1} onKeyDown={event => event.stopPropagation()} onClick={event => {
        if (onClick) onClick(event);
        else if (event.target === event.currentTarget) onClose();
      }}>
      {children}
      <Tooltip asChild content={t("imagePreview.closeShortcut")}><button type="button"
        className="content-preview-close" onClick={event => { event.stopPropagation(); onClose(); }}
        aria-label={closeLabel ?? t("common.close")}>
        <X className="icon" aria-hidden="true" />
      </button></Tooltip>
    </div>
  );
}
