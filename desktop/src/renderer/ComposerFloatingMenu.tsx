import {
  type CSSProperties,
  type ReactNode,
  type RefObject,
  useLayoutEffect,
  useRef,
  useState
} from "react";
import type {
  FloatingMenuAlign,
  FloatingMenuOwner,
  FloatingMenuPlacement
} from "./ComposerTypes";
import { UILayerPortal } from "./ui/layers/UILayerHost";

export function isInsideFloatingMenu(target: Node, owner: FloatingMenuOwner): boolean {
  const element = target instanceof Element ? target : target.parentElement;
  return Boolean(element?.closest('[data-floating-menu-owner="' + owner + '"]'));
}

function clamp(value: number, min: number, max: number): number {
  return Math.min(Math.max(value, min), max);
}

// Software keyboards change VisualViewport without resizing the layout
// viewport. Composer menus are position:fixed against that layout box, so
// placement and available height have to follow the visible rectangle or a
// card opened above the dock input can sit over the greeting while the
// keyboard is up, then stay there after the keyboard dismisses.
function visibleViewport(): { left: number; top: number; width: number; height: number } {
  const viewport = window.visualViewport;
  if (viewport && viewport.width > 0 && viewport.height > 0) {
    return {
      left: viewport.offsetLeft,
      top: viewport.offsetTop,
      width: viewport.width,
      height: viewport.height,
    };
  }
  return { left: 0, top: 0, width: window.innerWidth, height: window.innerHeight };
}

// Initial flip-threshold estimate used before the panel has been measured.
// Matches the panel's CSS max-height fallback so the first flip decision is
// a conservative "assume the worst case" guess; a follow-up measurement in
// rAF refines the decision with the real panel height. Without this seed,
// a panel below the threshold could be flipped unnecessarily on first
// render and then unflipped on second — a visible flicker.
const PANEL_HEIGHT_ESTIMATE = 320;

export function FloatingMenuPortal({
  anchorRef,
  owner,
  placement,
  align,
  offset = 8,
  crossAxisOffset = 0,
  width,
  matchAnchorWidth = false,
  // When true, flip to the opposite side of the trigger if the
  // requested placement doesn't have room. Use this for dropdowns
  // whose content height is uncertain (model pickers, tag pickers)
  // and which may sit near a viewport edge inside a modal — without
  // flipping, the panel overflows the viewport bottom and the
  // tail of the list is hidden behind the next layer / window
  // chrome. Defaults to false so existing callers keep their
  // explicit placement.
  flip = false,
  children
}: {
  anchorRef: RefObject<HTMLElement | null>;
  owner: FloatingMenuOwner;
  placement: FloatingMenuPlacement;
  align: FloatingMenuAlign;
  offset?: number;
  crossAxisOffset?: number;
  width: number;
  matchAnchorWidth?: boolean;
  flip?: boolean;
  children: ReactNode;
}): JSX.Element | null {
  const [resolvedPlacement, setResolvedPlacement] =
    useState<FloatingMenuPlacement>(placement);
  const [style, setStyle] = useState<CSSProperties>({
    position: "fixed",
    visibility: "hidden"
  });
  // Real panel height once the panel has mounted and rendered. Until then
  // we use PANEL_HEIGHT_ESTIMATE. Stored in state (not a ref) so that the
  // first measurement triggers a fresh useLayoutEffect → re-position with
  // the corrected height.
  const [measuredPanelHeight, setMeasuredPanelHeight] = useState<
    number | null
  >(null);
  const layerRef = useRef<HTMLDivElement>(null);

  useLayoutEffect(() => {
    function updatePosition(): void {
      const anchor = anchorRef.current;
      if (!anchor) {
        return;
      }
      const viewportMargin = 8;
      const visible = visibleViewport();
      const rect = anchor.getBoundingClientRect();
      const menuWidth = matchAnchorWidth && rect.width > 0 ? rect.width : width;
      const baseLeft = align === "right" ? rect.right - menuWidth : rect.left;
      const minLeft = visible.left + viewportMargin;
      const maxLeft = Math.max(minLeft, visible.left + visible.width - menuWidth - viewportMargin);
      const left = clamp(baseLeft + crossAxisOffset, minLeft, maxLeft);

      // Auto-flip: if the requested side has less than the panel's actual
      // height free AND the opposite side has more room, prefer the
      // opposite side. Before the panel has been measured we fall back to
      // PANEL_HEIGHT_ESTIMATE (matches the panel's CSS max-height cap).
      const flipThreshold = measuredPanelHeight ?? PANEL_HEIGHT_ESTIMATE;
      let actualPlacement: FloatingMenuPlacement = placement;
      if (flip && (placement === "above" || placement === "below")) {
        const spaceBelow = visible.top + visible.height - rect.bottom - offset;
        const spaceAbove = rect.top - visible.top - offset;
        if (
          placement === "below" &&
          spaceBelow < flipThreshold &&
          spaceAbove > spaceBelow
        ) {
          actualPlacement = "above";
        } else if (
          placement === "above" &&
          spaceAbove < flipThreshold &&
          spaceBelow > spaceAbove
        ) {
          actualPlacement = "below";
        }
      }
      if (actualPlacement !== resolvedPlacement) {
        setResolvedPlacement(actualPlacement);
      }

      const nextStyle: CSSProperties = {
        left,
        position: "fixed",
        visibility: "visible",
        // Sit above any modal overlay the floating menu might be opened
        // from. The new-participant dialog (and the shared sidebar-name
        // dialog + conversation-search dialog it reuses) is portaled to
        // body at z-index: 200, so a SelectMenu dropdown portaled from
        // inside that dialog needs to be > 200 to remain visible. 220
        // clears the modal band while staying below anything an app
        // surface might intentionally pin above.
        zIndex: 220,
      };
      if (matchAnchorWidth) {
        nextStyle.width = menuWidth;
      }

      // Constrain max-height to the available viewport room on the
      // chosen side. Panels consume the shared available-height variable
      // and scroll internally instead of overflowing the viewport.
      // The select-menu-specific variable remains for compatibility with
      // older consumers while shared composer menus use the generic one.
      let availableHeight: number;
      const visibleBottom = visible.top + visible.height;
      if (actualPlacement === "above") {
        nextStyle.bottom = Math.max(
          window.innerHeight - visibleBottom + viewportMargin,
          window.innerHeight - rect.top + offset
        );
        availableHeight = Math.max(0, rect.top - visible.top - offset - viewportMargin);
      } else if (actualPlacement === "below") {
        nextStyle.top = Math.max(visible.top + viewportMargin, rect.bottom + offset);
        availableHeight = Math.max(
          0,
          visibleBottom - rect.bottom - offset - viewportMargin
        );
      } else {
        nextStyle.top = clamp(
          rect.top + rect.height / 2,
          visible.top + viewportMargin,
          visibleBottom - viewportMargin
        );
        nextStyle.transform = "translateY(-50%)";
        availableHeight = Math.max(
          0,
          Math.min(visible.height - 2 * viewportMargin, 420)
        );
      }
      // CSS custom property — React's CSSProperties type doesn't allow
      // arbitrary `--*` keys, so the cast is the standard escape hatch.
      const styleVariables = nextStyle as Record<string, string>;
      styleVariables["--floating-menu-available-height"] = `${availableHeight}px`;
      styleVariables["--select-menu-max-height"] = `${availableHeight}px`;

      setStyle(nextStyle);
    }

    function measurePanel(): void {
      if (measuredPanelHeight !== null) {
        return;
      }
      const panel = layerRef.current?.querySelector(
        ".select-menu-panel"
      ) as HTMLElement | null;
      if (!panel) {
        return;
      }
      const h = panel.offsetHeight;
      if (h > 0) {
        setMeasuredPanelHeight(h);
      }
    }

    updatePosition();

    // After the first paint, measure the real panel height and let
    // React re-run useLayoutEffect so the flip decision can be refined
    // against the actual box (not the 320px estimate). The rAF guarantees
    // the panel has been laid out at least once.
    if (measuredPanelHeight === null) {
      const raf = requestAnimationFrame(measurePanel);
      // Defensive cleanup in case the effect tears down before rAF fires.
      // (No listener needs to be removed; the rAF callback is a no-op once
      // measuredPanelHeight is no longer null, so a stale fire is harmless.)
      void raf;
    }

    const viewport = window.visualViewport;
    window.addEventListener("resize", updatePosition);
    window.addEventListener("scroll", updatePosition, true);
    viewport?.addEventListener("resize", updatePosition);
    viewport?.addEventListener("scroll", updatePosition);
    return () => {
      window.removeEventListener("resize", updatePosition);
      window.removeEventListener("scroll", updatePosition, true);
      viewport?.removeEventListener("resize", updatePosition);
      viewport?.removeEventListener("scroll", updatePosition);
    };
  }, [
    align,
    anchorRef,
    crossAxisOffset,
    flip,
    matchAnchorWidth,
    measuredPanelHeight,
    offset,
    placement,
    resolvedPlacement,
    width
  ]);

  return (
    <UILayerPortal layer="menu">
      <div
        ref={layerRef}
        className={`floating-menu-layer floating-menu-${resolvedPlacement}`}
        data-wuu-component="menu"
        data-wuu-layer="menu"
        data-wuu-state="open"
        data-floating-menu-owner={owner}
        style={style}
        // The host app has a window-level pointerdown listener that closes
        // menus when a click is outside their trigger. Keep pointerdown
        // inside the floating layer so the button receives its click before
        // the host can unmount the menu.
        onPointerDown={(event) => event.stopPropagation()}
      >
        {children}
      </div>
    </UILayerPortal>
  );
}
