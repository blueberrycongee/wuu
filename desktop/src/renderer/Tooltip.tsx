/*
 * Tooltip.
 *
 * Hover/focus hints used to ride the native `title` attribute, which meant
 * the OS decided the styling, the timing, and — worse — the content: call
 * sites dumped full untruncated text into it. This component is the single
 * supported path for hover hints. Native hints remain available at embedded
 * browser boundaries, where renderer portals cannot composite above the page.
 * Both presentations bound what a hint can be:
 *
 * - Content is a designed short string. Anything past
 *   TOOLTIP_MAX_CONTENT_LENGTH is truncated with an ellipsis, so a tooltip
 *   can never silently become a document viewer. Reading full content is
 *   the job of the surface's own expand/open interaction.
 * - Timing and dismissal (hover delay with skip-delay, press, Escape,
 *   scroll, blur, context menus) come from useHoverReveal, shared with the
 *   sidebar hover cards.
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
  useId,
  useEffect,
  type ReactNode,
  useLayoutEffect,
  useRef,
  useState,
} from "react";
import { useOptionalWebWorkspaceControls } from "./WebWorkspaceControlsContext";
import { HOVER_REVEAL_OPEN_DELAY_MS, useHoverReveal, useRetainHoverOwner } from "./HoverReveal";
import { UILayerPortal } from "./ui/layers/UILayerHost";

export const TOOLTIP_MAX_CONTENT_LENGTH = 120;
export const TOOLTIP_OPEN_DELAY_MS = HOVER_REVEAL_OPEN_DELAY_MS;

const VIEWPORT_MARGIN = 8;
const TRIGGER_GAP = 6;

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
  propagateEscape = false,
  native = false,
}: {
  /** Designed hint text. Empty/undefined disables the tooltip. */
  content?: string | null;
  /** Exactly one element child — the trigger the tooltip anchors to. */
  children: ReactNode;
  disabled?: boolean;
  side?: TooltipSide;
  /** Also let the owning disclosure handle Escape after dismissing the hint. */
  propagateEscape?: boolean;
  /** Native hints can overlap an embedded WebContentsView without hiding it. */
  native?: boolean;
}): JSX.Element {
  const wrapperRef = useRef<HTMLSpanElement>(null);
  const layerRef = useRef<HTMLDivElement>(null);
  const [position, setPosition] = useState<CSSProperties | null>(null);

  const inactive = disabled || !content || content.trim() === "";
  const { revealed, anchorHandlers } = useHoverReveal<true>({ disabled: inactive, propagateEscape });
  const open = revealed !== null;
  const token = useId();
  const browser = useOptionalWebWorkspaceControls();
  const nativeHint = native && Boolean(browser?.capabilities.nativeOverlays);
  useRetainHoverOwner(open);

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
    let tip = layer.getBoundingClientRect();

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
    let left = Math.min(
      Math.max(rect.left + rect.width / 2 - tip.width / 2, VIEWPORT_MARGIN),
      maxLeft,
    );
    let top =
      placement === "top"
        ? rect.top - TRIGGER_GAP - tip.height
        : rect.bottom + TRIGGER_GAP;

    // Native web surfaces composite above renderer portals. Move an intersecting
    // hint into nearby app chrome instead of hiding the page during a hover.
    const nativeSurfaces = [...document.querySelectorAll<HTMLElement>("[data-wuu-native-surface]")]
      .filter((element) => !element.closest("[hidden], [inert]")
        && getComputedStyle(element).visibility !== "hidden"
        && getComputedStyle(element).display !== "none")
      .map((element) => element.getBoundingClientRect())
      .filter((surface) => surface.width > 0 && surface.height > 0);
    const intersectsNative = (x: number, y: number) => nativeSurfaces.some((surface) =>
      x < surface.right && x + tip.width > surface.left && y < surface.bottom && y + tip.height > surface.top);
    if (nativeHint) {
      left = Math.min(rect.right + TRIGGER_GAP, maxLeft);
      top = Math.max(VIEWPORT_MARGIN, Math.min(window.innerHeight - tip.height - VIEWPORT_MARGIN, rect.top + (rect.height - tip.height) / 2));
    } else if (intersectsNative(left, top)) {
      // A one-line label fits the toolbar at large UI sizes too. The trigger's
      // accessible name still contains the complete label.
      layer.style.whiteSpace = "nowrap";
      layer.style.overflow = "hidden";
      layer.style.textOverflow = "ellipsis";
      layer.style.maxWidth = `${Math.min(280, window.innerWidth - VIEWPORT_MARGIN * 2)}px`;
      tip = layer.getBoundingClientRect();
      left = Math.min(left, Math.max(VIEWPORT_MARGIN, window.innerWidth - tip.width - VIEWPORT_MARGIN));
      const maxTop = Math.max(VIEWPORT_MARGIN, window.innerHeight - tip.height - VIEWPORT_MARGIN);
      const positions = nativeSurfaces.flatMap((surface) => [
        { left, top: surface.top - tip.height - TRIGGER_GAP },
        { left, top: surface.bottom + TRIGGER_GAP },
      ]).filter((candidate) => candidate.top >= VIEWPORT_MARGIN && candidate.top <= maxTop
        && !intersectsNative(candidate.left, candidate.top))
        .sort((first, second) => Math.abs(first.top - top) - Math.abs(second.top - top));
      const safe = positions[0];
      if (!safe) {
        setPosition({ visibility: "hidden" });
        return;
      }
      top = safe.top;
      placement = top < rect.top ? "top" : "bottom";
    }

    layer.dataset.side = placement;
    setPosition({ left, top, visibility: "visible" });
  }, [open, content, side, nativeHint]);

  useEffect(() => {
    const layer = layerRef.current;
    if (!nativeHint || !browser || !open || !content || !position || !layer) return;
    const rect = layer.getBoundingClientRect(), style = getComputedStyle(layer);
    void browser.showTooltip(token, { text: tooltipContent(content), x: Number(position.left), y: Number(position.top), width: rect.width, height: rect.height,
      style: { fontFamily: style.fontFamily, fontSize: style.fontSize, fontWeight: style.fontWeight, fontStyle: style.fontStyle, color: style.color, backgroundColor: style.backgroundColor, border: style.border,
        borderRadius: style.borderRadius, padding: style.padding, lineHeight: style.lineHeight } }).catch(() => undefined);
    return () => { void browser.showTooltip(token, null).catch(() => undefined); };
  }, [browser, token, nativeHint, open, content, position]);

  return (
    <>
      <span
        ref={wrapperRef}
        className="tooltip-trigger"
        {...anchorHandlers(true)}
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
                style={nativeHint ? { ...position, visibility: "hidden" } : position ?? { visibility: "hidden" }}
              >
                {tooltipContent(content)}
              </div>
            </UILayerPortal>
          )
        : null}
    </>
  );
}
