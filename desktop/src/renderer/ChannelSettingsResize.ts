import { useCallback, useEffect, useLayoutEffect, useRef, useState, type CSSProperties, type KeyboardEvent, type PointerEvent as ReactPointerEvent } from "react";

interface ChannelPanelResizeOptions {
  storageKey: string;
  defaultWidth: number;
  minWidth: number;
  maxWidth: number;
  dockedAbove: number;
}

const CHAT_MIN_WIDTH = 400;

function initialWidth(options: ChannelPanelResizeOptions): number {
  const stored = window.localStorage.getItem(options.storageKey);
  const width = stored === null ? options.defaultWidth : Number(stored);
  return Number.isFinite(width) ? Math.max(options.minWidth, Math.min(options.maxWidth, width)) : options.defaultWidth;
}

export function useChannelSettingsResize(open: boolean) {
  const resize = useChannelPanelResize(open, {
    storageKey: "wuu.channels.settingsWidth", defaultWidth: 340,
    minWidth: 280, maxWidth: 560, dockedAbove: 820,
  });
  return { ...resize, style: { "--channel-settings-width": `${resize.width}px` } as CSSProperties };
}

// Settings and session inspectors share drag cleanup, persistence and chat clearance.
export function useChannelPanelResize<T extends HTMLElement = HTMLElement>(open: boolean, options: ChannelPanelResizeOptions) {
  const { storageKey, defaultWidth, minWidth, maxWidth: limit, dockedAbove } = options;
  const ref = useRef<T>(null);
  const [preferredWidth, setPreferredWidth] = useState(() => initialWidth(options));
  const [containerWidth, setContainerWidth] = useState(0);
  const [drag, setDrag] = useState<{ x: number; width: number; pointerId: number } | null>(null);
  const maxWidth = Math.max(minWidth, Math.min(limit, containerWidth - CHAT_MIN_WIDTH));
  const width = Math.min(preferredWidth, maxWidth);
  const fitsDocked = containerWidth > dockedAbove;
  const docked = open && fitsDocked;

  useLayoutEffect(() => {
    const node = ref.current;
    if (!open || !node) return;
    const measure = () => setContainerWidth(node.clientWidth);
    measure();
    const observer = typeof ResizeObserver === "undefined" ? undefined : new ResizeObserver(measure);
    observer?.observe(node);
    return () => observer?.disconnect();
  }, [open]);

  const updateWidth = useCallback((next: number) => {
    const clamped = Math.max(minWidth, Math.min(maxWidth, next));
    setPreferredWidth(clamped);
    window.localStorage.setItem(storageKey, String(clamped));
  }, [minWidth, maxWidth, storageKey]);

  useEffect(() => {
    if (!drag || !docked) {
      if (drag) setDrag(null);
      return;
    }
    const move = (event: PointerEvent) => {
      if (event.pointerId !== drag.pointerId) return;
      updateWidth(drag.width + drag.x - event.clientX);
    };
    const end = () => setDrag(null);
    const previousCursor = document.body.style.cursor;
    const previousUserSelect = document.body.style.userSelect;
    document.body.style.cursor = "col-resize";
    document.body.style.userSelect = "none";
    window.addEventListener("pointermove", move);
    window.addEventListener("pointerup", end);
    window.addEventListener("pointercancel", end);
    window.addEventListener("blur", end);
    return () => {
      document.body.style.cursor = previousCursor;
      document.body.style.userSelect = previousUserSelect;
      window.removeEventListener("pointermove", move);
      window.removeEventListener("pointerup", end);
      window.removeEventListener("pointercancel", end);
      window.removeEventListener("blur", end);
    };
  }, [drag, docked, updateWidth]);

  return {
    ref,
    width,
    docked,
    fitsDocked,
    resizing: Boolean(drag) && docked,
    separatorProps: {
      hidden: !docked,
      "aria-valuemin": minWidth,
      "aria-valuemax": maxWidth,
      "aria-valuenow": width,
      "data-resizing": Boolean(drag) || undefined,
      onPointerDown(event: ReactPointerEvent<HTMLDivElement>) {
        if (event.button !== 0 || !docked) return;
        event.preventDefault();
        event.currentTarget.focus();
        setDrag({ x: event.clientX, width, pointerId: event.pointerId });
      },
      onDoubleClick() { updateWidth(defaultWidth); },
      onKeyDown(event: KeyboardEvent<HTMLDivElement>) {
        const next = event.key === "ArrowLeft" ? width + 16
          : event.key === "ArrowRight" ? width - 16
          : event.key === "Home" ? minWidth
          : event.key === "End" ? maxWidth : null;
        if (next === null) return;
        event.preventDefault();
        updateWidth(next);
      },
    },
  };
}
