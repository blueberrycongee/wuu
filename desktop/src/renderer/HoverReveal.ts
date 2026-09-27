/*
 * Hover intent for transient, visual-only layers (tooltips, sidebar hover
 * cards). One implementation owns the timing and every dismissal rule so
 * the two kinds of layer cannot drift apart:
 *
 * - Opening waits HOVER_REVEAL_OPEN_DELAY_MS. A layer hovered within
 *   SKIP_DELAY_MS of any other layer closing opens immediately, so sweeping
 *   across a row of controls or a list doesn't pay the delay per anchor.
 * - Pointer-down, Escape, scroll, and blur all dismiss. After a dismiss on
 *   press or Escape the layer stays suppressed until the pointer leaves the
 *   anchor, so a clicked control doesn't immediately re-arm its layer.
 * - No layer opens while a context menu is open, and opening one dismisses
 *   the current layer.
 *
 * A hook instance serves many anchors, keyed by the caller: a list renders
 * one layer for whichever row is revealed instead of one hook per row.
 */
import {
  type FocusEvent as ReactFocusEvent,
  type PointerEvent as ReactPointerEvent,
  useEffect,
  useRef,
  useState,
} from "react";
import { hasActiveContextMenu, onContextMenuOpen } from "./ActiveContextMenu";
import { FOCUS_MODALITY_ATTRIBUTE } from "./FocusModality";

export const HOVER_REVEAL_OPEN_DELAY_MS = 400;
const SKIP_DELAY_MS = 300;

// Timestamp of the most recent close, shared across all instances.
let lastRevealClosedAt = -Infinity;

export type HoverRevealAnchorHandlers = {
  onPointerOver: (event: ReactPointerEvent<HTMLElement>) => void;
  onPointerOut: (event: ReactPointerEvent<HTMLElement>) => void;
  onPointerDownCapture: () => void;
  onFocus: (event: ReactFocusEvent<HTMLElement>) => void;
  onBlur: () => void;
};

export type HoverRevealTarget<K> = { key: K; anchor: HTMLElement };

export function useHoverReveal<K>({
  disabled = false,
  focus = "any",
}: {
  disabled?: boolean;
  // "keyboard" opens on focus only when the keyboard moved it. Large layers
  // use it so a click, or the window regaining focus, doesn't pop one up.
  focus?: "any" | "keyboard";
} = {}): {
  revealed: HoverRevealTarget<K> | null;
  anchorHandlers: (key: K) => HoverRevealAnchorHandlers;
} {
  const [revealed, setRevealed] = useState<HoverRevealTarget<K> | null>(null);
  // Mirrors for event handlers and effects, so dismissing never depends on
  // a stale closure capture.
  const revealedRef = useRef<HoverRevealTarget<K> | null>(null);
  const pendingKeyRef = useRef<{ key: K } | null>(null);
  const openTimerRef = useRef<number | null>(null);
  const suppressUntilLeaveRef = useRef(false);

  function clearOpenTimer(): void {
    if (openTimerRef.current !== null) {
      window.clearTimeout(openTimerRef.current);
      openTimerRef.current = null;
    }
    pendingKeyRef.current = null;
  }

  function reveal(next: HoverRevealTarget<K> | null): void {
    if (revealedRef.current === next) {
      return;
    }
    if (!next) {
      lastRevealClosedAt = Date.now();
    }
    revealedRef.current = next;
    setRevealed(next);
  }

  function close(): void {
    clearOpenTimer();
    reveal(null);
  }

  function scheduleOpen(key: K, anchor: HTMLElement): void {
    if (disabled || suppressUntilLeaveRef.current) {
      return;
    }
    const current = revealedRef.current;
    if (current && Object.is(current.key, key)) {
      return;
    }
    if (pendingKeyRef.current && Object.is(pendingKeyRef.current.key, key)) {
      return;
    }
    clearOpenTimer();
    const delay =
      current || Date.now() - lastRevealClosedAt < SKIP_DELAY_MS
        ? 0
        : HOVER_REVEAL_OPEN_DELAY_MS;
    pendingKeyRef.current = { key };
    openTimerRef.current = window.setTimeout(() => {
      openTimerRef.current = null;
      pendingKeyRef.current = null;
      if (!hasActiveContextMenu()) {
        reveal({ key, anchor });
      }
    }, delay);
  }

  function anchorHandlers(key: K): HoverRevealAnchorHandlers {
    return {
      onPointerOver: (event) => {
        if (event.pointerType === "touch") {
          return;
        }
        scheduleOpen(key, event.currentTarget);
      },
      onPointerOut: (event) => {
        const related = event.relatedTarget;
        if (related instanceof Node && event.currentTarget.contains(related)) {
          return;
        }
        suppressUntilLeaveRef.current = false;
        close();
      },
      onPointerDownCapture: () => {
        // The press is about to mutate the surface (run the action, open a
        // menu, start a drag); a layer for the pre-press state would be stale.
        suppressUntilLeaveRef.current = true;
        close();
      },
      onFocus: (event) => {
        if (
          focus === "keyboard" &&
          document.documentElement.getAttribute(FOCUS_MODALITY_ATTRIBUTE) !== "keyboard"
        ) {
          return;
        }
        scheduleOpen(key, event.currentTarget);
      },
      onBlur: close,
    };
  }

  // While open: Escape dismisses (capture, so the layer doesn't race a
  // surface-level handler), any scroll closes — the anchor geometry the
  // position was computed against is gone — and so does a context menu.
  const open = revealed !== null;
  useEffect(() => {
    if (!open) {
      return;
    }
    const handleKeyDown = (event: KeyboardEvent): void => {
      if (event.key === "Escape") {
        event.stopPropagation();
        suppressUntilLeaveRef.current = true;
        close();
      }
    };
    const handleScroll = (): void => close();
    window.addEventListener("keydown", handleKeyDown, true);
    window.addEventListener("scroll", handleScroll, true);
    const stopContextMenuWatch = onContextMenuOpen(close);
    return () => {
      window.removeEventListener("keydown", handleKeyDown, true);
      window.removeEventListener("scroll", handleScroll, true);
      stopContextMenuWatch();
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open]);

  // External state can retire a layer mid-hover (content cleared, or the
  // anchor became disabled).
  useEffect(() => {
    if (disabled) {
      close();
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [disabled]);

  useEffect(() => clearOpenTimer, []);

  return { revealed, anchorHandlers };
}
