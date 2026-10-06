/*
 * Hover intent for transient layers (tooltips and sidebar hover cards).
 * Interactive cards retain pointer and focus across the anchor/layer gap.
 * One implementation owns the timing and every dismissal rule so
 * the two kinds of layer cannot drift apart:
 *
 * - Opening waits HOVER_REVEAL_OPEN_DELAY_MS. A layer hovered within
 *   SKIP_DELAY_MS of any other layer closing opens immediately, so sweeping
 *   across a row of controls or a list doesn't pay the delay per anchor.
 * - Anchor presses, Escape, external scroll, and blur dismiss. Interactive
 *   cards also dismiss on outside presses. After a dismiss on press or
 *   Escape the layer stays suppressed until the pointer leaves the
 *   anchor, so a clicked control doesn't immediately re-arm its layer.
 * - No layer opens while a context menu is open, and opening one dismisses
 *   the current layer.
 *
 * A hook instance serves many anchors, keyed by the caller: a list renders
 * one layer for whichever row is revealed instead of one hook per row.
 */
import {
  type FocusEvent as ReactFocusEvent,
  type HTMLAttributes,
  type KeyboardEvent as ReactKeyboardEvent,
  type PointerEvent as ReactPointerEvent,
  type RefObject,
  useEffect,
  useRef,
  useState,
} from "react";
import { hasActiveContextMenu, onContextMenuOpen } from "./ActiveContextMenu";
import { FOCUS_MODALITY_ATTRIBUTE } from "./FocusModality";

export const HOVER_REVEAL_OPEN_DELAY_MS = 400;
const SKIP_DELAY_MS = 300;
const INTERACTIVE_CLOSE_DELAY_MS = 200;

// Timestamp of the most recent close, shared across all instances.
let lastRevealClosedAt = -Infinity;
let focusedHoverLayer: RefObject<HTMLDivElement | null> | null = null;

export type HoverRevealAnchorHandlers = {
  onPointerOver: (event: ReactPointerEvent<HTMLElement>) => void;
  onPointerOut: (event: ReactPointerEvent<HTMLElement>) => void;
  onPointerDownCapture: () => void;
  onFocus: (event: ReactFocusEvent<HTMLElement>) => void;
  onBlur: (event: ReactFocusEvent<HTMLElement>) => void;
  onKeyDown: (event: ReactKeyboardEvent<HTMLElement>) => void;
};

export type HoverRevealLayerProps = HTMLAttributes<HTMLDivElement> & {
  ref: RefObject<HTMLDivElement | null>;
};

export type HoverRevealTarget<K> = { key: K; anchor: HTMLElement };

export function useHoverReveal<K>({
  disabled = false,
  focus = "any",
  interactive = false,
}: {
  disabled?: boolean;
  // "keyboard" opens on focus only when the keyboard moved it. Large layers
  // use it so a click, or the window regaining focus, doesn't pop one up.
  focus?: "any" | "keyboard";
  interactive?: boolean;
} = {}): {
  revealed: HoverRevealTarget<K> | null;
  anchorHandlers: (key: K) => HoverRevealAnchorHandlers;
  layerProps: HoverRevealLayerProps;
} {
  const [revealed, setRevealed] = useState<HoverRevealTarget<K> | null>(null);
  // Mirrors for event handlers and effects, so dismissing never depends on
  // a stale closure capture.
  const revealedRef = useRef<HoverRevealTarget<K> | null>(null);
  const pendingKeyRef = useRef<{ key: K } | null>(null);
  const openTimerRef = useRef<number | null>(null);
  const suppressUntilLeaveRef = useRef(false);
  const layerRef = useRef<HTMLDivElement>(null);
  const closeTimerRef = useRef<number | null>(null);

  function clearCloseTimer(): void {
    if (closeTimerRef.current !== null) {
      window.clearTimeout(closeTimerRef.current);
      closeTimerRef.current = null;
    }
  }

  function containsTarget(target: EventTarget | null): boolean {
    return target instanceof Node && Boolean(
      revealedRef.current?.anchor.contains(target) || layerRef.current?.contains(target),
    );
  }

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
    clearCloseTimer();
    if (focusedHoverLayer === layerRef) focusedHoverLayer = null;
    reveal(null);
  }

  function leave(): void {
    clearOpenTimer();
    if (!interactive) {
      close();
    } else if (!layerRef.current?.contains(document.activeElement)) {
      clearCloseTimer();
      closeTimerRef.current = window.setTimeout(close, INTERACTIVE_CLOSE_DELAY_MS);
    }
  }

  function scheduleOpen(key: K, anchor: HTMLElement): void {
    clearCloseTimer();
    if (disabled || suppressUntilLeaveRef.current) {
      return;
    }
    // A draft belongs to the currently focused card, not the next row the
    // pointer happens to cross while typing.
    if (focusedHoverLayer?.current?.contains(document.activeElement)) return;
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
        if (interactive && containsTarget(related)) return;
        leave();
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
      onBlur: (event) => {
        if (interactive && containsTarget(event.relatedTarget)) return;
        leave();
      },
      onKeyDown: (event) => {
        if (interactive && event.key === "Tab" && !event.shiftKey && revealedRef.current?.key === key) {
          const control = layerRef.current?.querySelector<HTMLElement>("button:not(:disabled), input");
          if (control) {
            event.preventDefault();
            control.focus();
          }
        }
      },
    };
  }

  // A scroll outside the card invalidates its anchor geometry; a context
  // menu retires the card too. Scrolls within an editor do neither.
  const open = revealed !== null;
  useEffect(() => {
    if (!open) {
      return;
    }
    const handleKeyDown = (event: KeyboardEvent): void => {
      if (event.key === "Escape") {
        event.stopPropagation();
        suppressUntilLeaveRef.current = true;
        const anchorHovered = revealedRef.current?.anchor.matches(":hover");
        if (interactive && layerRef.current?.contains(document.activeElement)) {
          revealedRef.current?.anchor.querySelector<HTMLElement>("button")?.focus();
        }
        close();
        if (interactive && !anchorHovered) suppressUntilLeaveRef.current = false;
      }
    };
    const handleScroll = (event: Event): void => {
      if (interactive && event.target instanceof Node && layerRef.current?.contains(event.target)) return;
      close();
    };
    const handlePointerDown = (event: PointerEvent): void => {
      if (!containsTarget(event.target)) close();
    };
    const handleWindowBlur = (): void => close();
    // Editors handle Escape before the layer does; tooltips retain capture
    // dismissal so they do not race a surface-level handler.
    window.addEventListener("keydown", handleKeyDown, !interactive);
    window.addEventListener("scroll", handleScroll, true);
    if (interactive) window.addEventListener("pointerdown", handlePointerDown, true);
    window.addEventListener("blur", handleWindowBlur);
    const stopContextMenuWatch = onContextMenuOpen(close);
    return () => {
      window.removeEventListener("keydown", handleKeyDown, !interactive);
      window.removeEventListener("scroll", handleScroll, true);
      window.removeEventListener("pointerdown", handlePointerDown, true);
      window.removeEventListener("blur", handleWindowBlur);
      stopContextMenuWatch();
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, interactive]);

  // External state can retire a layer mid-hover (content cleared, or the
  // anchor became disabled).
  useEffect(() => {
    if (disabled) {
      close();
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [disabled]);

  useEffect(() => () => {
    clearOpenTimer();
    clearCloseTimer();
    if (focusedHoverLayer === layerRef) focusedHoverLayer = null;
  }, []);

  return {
    revealed,
    anchorHandlers,
    layerProps: {
      ref: layerRef,
      onPointerEnter: clearCloseTimer,
      onPointerLeave: leave,
      onFocus: () => {
        clearCloseTimer();
        focusedHoverLayer = layerRef;
      },
      onBlur: (event) => {
        if (!containsTarget(event.relatedTarget)) close();
      },
    },
  };
}
