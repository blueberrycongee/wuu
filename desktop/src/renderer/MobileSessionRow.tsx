import { useEffect, useRef } from "react";

export function MobileSessionRow({ title, active, pending, running, unread, enabled, statusLabel, onSelect, onActions }: {
  title: string;
  active: boolean;
  pending: boolean;
  running: boolean;
  unread: boolean;
  enabled: boolean;
  statusLabel?: string;
  onSelect: () => void;
  onActions: (button: HTMLButtonElement) => void;
}): JSX.Element {
  const timer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
  const origin = useRef<{ x: number; y: number } | undefined>(undefined);
  const cleanup = useRef<(() => void) | undefined>(undefined);
  const suppressClick = useRef(false);
  const touch = useRef(false);
  const cancelPress = () => {
    clearTimeout(timer.current);
    timer.current = undefined;
    origin.current = undefined;
    cleanup.current?.();
    cleanup.current = undefined;
  };
  useEffect(() => cancelPress, []);
  useEffect(() => { if (!enabled) cancelPress(); }, [enabled]);

  return <button
    type="button"
    className="mobile-session-row"
    aria-label={title}
    aria-description={running || unread ? statusLabel : undefined}
    aria-current={active ? "page" : undefined}
    aria-busy={pending}
    onPointerDown={event => {
      cancelPress();
      suppressClick.current = false;
      if (!enabled) return;
      touch.current = event.pointerType === "touch" || event.pointerType === "pen";
      if (!touch.current || event.button !== 0 || event.isPrimary === false) return;
      const button = event.currentTarget;
      origin.current = { x: event.clientX, y: event.clientY };
      const cancel = () => { cancelPress(); suppressClick.current = true; };
      window.addEventListener("scroll", cancel, true);
      window.addEventListener("blur", cancel);
      cleanup.current = () => {
        window.removeEventListener("scroll", cancel, true);
        window.removeEventListener("blur", cancel);
      };
      timer.current = setTimeout(() => {
        cancelPress();
        suppressClick.current = true;
        onActions(button);
      }, 500);
    }}
    onPointerMove={event => {
      if (origin.current && Math.hypot(event.clientX - origin.current.x, event.clientY - origin.current.y) > 8) {
        cancelPress();
        suppressClick.current = true;
      }
    }}
    onPointerUp={event => { cancelPress(); if (suppressClick.current) event.preventDefault(); }}
    onPointerCancel={() => { cancelPress(); suppressClick.current = true; }}
    onContextMenu={event => {
      event.preventDefault();
      // The browser's touch menu must not reopen a dismissed action sheet.
      if (!enabled || touch.current) return;
      cancelPress();
      suppressClick.current = true;
      onActions(event.currentTarget);
    }}
    onKeyDown={event => {
      if (event.key === "Enter" || event.key === " ") suppressClick.current = false;
      if (event.key === "ContextMenu" || (event.shiftKey && event.key === "F10")) {
        event.preventDefault();
        onActions(event.currentTarget);
      }
    }}
    onClick={() => {
      if (!enabled) return;
      if (suppressClick.current) { suppressClick.current = false; return; }
      onSelect();
    }}
  >
    <span className="mobile-session-title">{title}</span>
    {running || unread ? <span className={`mobile-session-status${running ? " running" : ""}`} role="img" aria-label={statusLabel} /> : null}
  </button>;
}
