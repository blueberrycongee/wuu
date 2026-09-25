/*
 * Tooltip.
 *
 * Hover/focus hints used to ride the native `title` attribute, which meant
 * the OS decided the styling, the timing, and — worse — the content: call
 * sites dumped full untruncated text into it. This component is the single
 * supported path for hover hints, and it bounds what a hint can be:
 *
 * - Content is a designed short string. Anything past
 *   TOOLTIP_MAX_CONTENT_LENGTH is truncated with an ellipsis, so a tooltip
 *   can never silently become a document viewer. Reading full content is
 *   the job of the surface's own expand/open interaction.
 * - Timing is fixed: 400ms hover delay, with a short skip-delay window so
 *   sweeping across a row of controls doesn't pay the delay per control.
 * - Pointer-down, Escape, scroll, and blur all dismiss; after a dismiss-
 *   on-press the tooltip stays suppressed until the pointer leaves, so a
 *   clicked control doesn't immediately re-arm its hint.
 * - No hint opens while a context menu is open, and opening one dismisses
 *   the current hint. Native context menus are modal for hover; a hint
 *   beside the menu would repeat or cover it.
 *
 * Accessibility does NOT route through this component: the tooltip is
 * visual-only (pointer-events: none, no aria-describedby wiring). Controls
 * keep their own aria-label; a tooltip must never carry information that
 * isn't otherwise reachable.
 *
 * The trigger is wrapped in a `display: contents` span rather than cloned:
 * the wrapper adds no box to the layout, bubbled pointer/focus events
 * still reach it, and — unlike listeners on the control itself — it keeps
 * working over disabled buttons, which swallow their own mouse events.
 */
import {
  type CSSProperties,
  type PointerEvent as ReactPointerEvent,
  type ReactNode,
  useEffect,
  useLayoutEffect,
  useRef,
  useState,
} from "react";
import { hasActiveContextMenu, onContextMenuOpen } from "./ActiveContextMenu";
import { UILayerPortal } from "./ui/layers/UILayerHost";

export const TOOLTIP_MAX_CONTENT_LENGTH = 120;
export const TOOLTIP_OPEN_DELAY_MS = 400;

const OPEN_DELAY_MS = TOOLTIP_OPEN_DELAY_MS;
const SKIP_DELAY_MS = 300;
const VIEWPORT_MARGIN = 8;
const TRIGGER_GAP = 6;

// Timestamp of the most recent tooltip close, shared across all instances.
// The first tooltip in a sweep pays OPEN_DELAY_MS; any sibling hovered
// within SKIP_DELAY_MS of that close opens immediately.
let lastTooltipClosedAt = -Infinity;

/** Bound tooltip copy: past the cap, end-truncate with an ellipsis. */
export function tooltipContent(content: string): string {
  if (content.length <= TOOLTIP_MAX_CONTENT_LENGTH) {
    return content;
  }
  return `${content.slice(0, TOOLTIP_MAX_CONTENT_LENGTH - 1).trimEnd()}…`;
}

export type TooltipSide = "top" | "bottom";

export function Tooltip({
  content,
  children,
  disabled = false,
  side = "top",
}: {
  /** Designed hint text. Empty/undefined disables the tooltip. */
  content?: string | null;
  /** Exactly one element child — the trigger the tooltip anchors to. */
  children: ReactNode;
  disabled?: boolean;
  side?: TooltipSide;
}): JSX.Element {
  const [open, setOpen] = useState(false);
  const wrapperRef = useRef<HTMLSpanElement>(null);
  const layerRef = useRef<HTMLDivElement>(null);
  const openTimerRef = useRef<number | null>(null);
  // Mirror of `open` for event handlers and effects, so dismissing never
  // depends on a stale closure capture.
  const openRef = useRef(false);
  const suppressUntilLeaveRef = useRef(false);
  const [position, setPosition] = useState<CSSProperties | null>(null);

  const inactive = disabled || !content || content.trim() === "";

  function clearOpenTimer(): void {
    if (openTimerRef.current !== null) {
      window.clearTimeout(openTimerRef.current);
      openTimerRef.current = null;
    }
  }

  function setTooltipOpen(next: boolean): void {
    if (openRef.current === next) {
      return;
    }
    openRef.current = next;
    if (!next) {
      lastTooltipClosedAt = Date.now();
    }
    setOpen(next);
  }

  function closeTooltip(): void {
    clearOpenTimer();
    setTooltipOpen(false);
  }

  function scheduleOpen(): void {
    if (inactive || suppressUntilLeaveRef.current) {
      return;
    }
    clearOpenTimer();
    const delay =
      Date.now() - lastTooltipClosedAt < SKIP_DELAY_MS ? 0 : OPEN_DELAY_MS;
    openTimerRef.current = window.setTimeout(() => {
      openTimerRef.current = null;
      if (!hasActiveContextMenu()) {
        setTooltipOpen(true);
      }
    }, delay);
  }

  function handlePointerOver(event: ReactPointerEvent<HTMLSpanElement>): void {
    if (event.pointerType === "touch") {
      return;
    }
    scheduleOpen();
  }

  function handlePointerOut(event: ReactPointerEvent<HTMLSpanElement>): void {
    const related = event.relatedTarget;
    if (related instanceof Node && wrapperRef.current?.contains(related)) {
      return;
    }
    suppressUntilLeaveRef.current = false;
    closeTooltip();
  }

  function handlePointerDownCapture(): void {
    // Dismiss immediately and stay dismissed until the pointer leaves:
    // the press is about to mutate the surface (run the action, open a
    // menu), and a hint for the pre-press state would be stale.
    suppressUntilLeaveRef.current = true;
    closeTooltip();
  }

  // Measure and place the layer against the trigger. The layer mounts
  // hidden, this effect measures both boxes, and the resulting state
  // update makes it visible — so there is no frame where the tooltip
  // sits at an uncomputed position.
  useLayoutEffect(() => {
    if (!open) {
      setPosition(null);
      return;
    }
    const wrapper = wrapperRef.current;
    const layer = layerRef.current;
    const trigger = wrapper?.firstElementChild;
    if (!wrapper || !layer || !(trigger instanceof HTMLElement)) {
      return;
    }
    const rect = trigger.getBoundingClientRect();
    const tip = layer.getBoundingClientRect();

    let placement: TooltipSide = side;
    const spaceAbove = rect.top - TRIGGER_GAP - VIEWPORT_MARGIN;
    const spaceBelow =
      window.innerHeight - rect.bottom - TRIGGER_GAP - VIEWPORT_MARGIN;
    if (side === "top" && tip.height > spaceAbove && spaceBelow > spaceAbove) {
      placement = "bottom";
    } else if (
      side === "bottom" &&
      tip.height > spaceBelow &&
      spaceAbove > spaceBelow
    ) {
      placement = "top";
    }

    const maxLeft = Math.max(
      VIEWPORT_MARGIN,
      window.innerWidth - tip.width - VIEWPORT_MARGIN,
    );
    const left = Math.min(
      Math.max(rect.left + rect.width / 2 - tip.width / 2, VIEWPORT_MARGIN),
      maxLeft,
    );
    const top =
      placement === "top"
        ? rect.top - TRIGGER_GAP - tip.height
        : rect.bottom + TRIGGER_GAP;

    layer.dataset.side = placement;
    setPosition({ left, top, visibility: "visible" });
  }, [open, content, side]);

  // While open: Escape dismisses (capture, so the tooltip doesn't race a
  // surface-level handler), any scroll closes — the anchor geometry the
  // position was computed against is gone — and so does a context menu.
  useEffect(() => {
    if (!open) {
      return;
    }
    const handleKeyDown = (event: KeyboardEvent): void => {
      if (event.key === "Escape") {
        event.stopPropagation();
        suppressUntilLeaveRef.current = true;
        closeTooltip();
      }
    };
    const handleScroll = (): void => closeTooltip();
    window.addEventListener("keydown", handleKeyDown, true);
    window.addEventListener("scroll", handleScroll, true);
    const stopContextMenuWatch = onContextMenuOpen(closeTooltip);
    return () => {
      window.removeEventListener("keydown", handleKeyDown, true);
      window.removeEventListener("scroll", handleScroll, true);
      stopContextMenuWatch();
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open]);

  // External state can retire a tooltip mid-hover (content cleared, or the
  // control became disabled).
  useEffect(() => {
    if (inactive) {
      closeTooltip();
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [inactive]);

  useEffect(() => clearOpenTimer, []);

  return (
    <>
      <span
        ref={wrapperRef}
        className="tooltip-trigger"
        onPointerOver={handlePointerOver}
        onPointerOut={handlePointerOut}
        onPointerDownCapture={handlePointerDownCapture}
        onFocus={scheduleOpen}
        onBlur={closeTooltip}
      >
        {children}
      </span>
      {open && content
        ? (
            <UILayerPortal layer="tooltip">
              <div
                ref={layerRef}
                className="tooltip-layer"
                data-wuu-component="tooltip"
                data-wuu-layer="tooltip"
                data-wuu-state="open"
                role="tooltip"
                style={position ?? { visibility: "hidden" }}
              >
                {tooltipContent(content)}
              </div>
            </UILayerPortal>
          )
        : null}
    </>
  );
}
