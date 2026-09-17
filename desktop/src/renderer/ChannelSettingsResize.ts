import { useCallback, useEffect, useLayoutEffect, useRef, useState, type CSSProperties, type KeyboardEvent, type PointerEvent as ReactPointerEvent } from "react";

const WIDTH_KEY = "wuu.channels.settingsWidth";
const DEFAULT_WIDTH = 340;
const MIN_WIDTH = 280;
const MAX_WIDTH = 560;
const STACKED_WIDTH = 820;
const CHAT_MIN_WIDTH = 400;

function initialWidth(): number {
  const stored = window.localStorage.getItem(WIDTH_KEY);
  const width = stored === null ? DEFAULT_WIDTH : Number(stored);
  return Number.isFinite(width) ? Math.max(MIN_WIDTH, Math.min(MAX_WIDTH, width)) : DEFAULT_WIDTH;
}

export function useChannelSettingsResize(open: boolean) {
  const ref = useRef<HTMLElement>(null);
  const [preferredWidth, setPreferredWidth] = useState(initialWidth);
  const [containerWidth, setContainerWidth] = useState(0);
  const [drag, setDrag] = useState<{ x: number; width: number; pointerId: number } | null>(null);
  const maxWidth = Math.max(MIN_WIDTH, Math.min(MAX_WIDTH, containerWidth - CHAT_MIN_WIDTH));
  const width = Math.min(preferredWidth, maxWidth);
  const docked = open && containerWidth > STACKED_WIDTH;

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
    const clamped = Math.max(MIN_WIDTH, Math.min(maxWidth, next));
    setPreferredWidth(clamped);
    window.localStorage.setItem(WIDTH_KEY, String(clamped));
  }, [maxWidth]);

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
    style: { "--channel-settings-width": `${width}px` } as CSSProperties,
    separatorProps: {
      hidden: !docked,
      "aria-valuemin": MIN_WIDTH,
      "aria-valuemax": maxWidth,
      "aria-valuenow": width,
      "data-resizing": Boolean(drag) || undefined,
      onPointerDown(event: ReactPointerEvent<HTMLDivElement>) {
        if (event.button !== 0 || !docked) return;
        event.preventDefault();
        event.currentTarget.focus();
        setDrag({ x: event.clientX, width, pointerId: event.pointerId });
      },
      onDoubleClick() { updateWidth(DEFAULT_WIDTH); },
      onKeyDown(event: KeyboardEvent<HTMLDivElement>) {
        const next = event.key === "ArrowLeft" ? width + 16
          : event.key === "ArrowRight" ? width - 16
          : event.key === "Home" ? MIN_WIDTH
          : event.key === "End" ? maxWidth : null;
        if (next === null) return;
        event.preventDefault();
        updateWidth(next);
      },
    },
  };
}
