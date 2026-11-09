const VIEWPORT_MARGIN = 8;

export type ContextMenuOrigin =
  | "top-left"
  | "top-right"
  | "bottom-left"
  | "bottom-right";

export type ContextMenuLayout = {
  left: number;
  top: number;
  origin: ContextMenuOrigin;
};

function clamp(value: number, min: number, max: number): number {
  return Math.min(Math.max(value, min), Math.max(min, max));
}

/**
 * Places a cursor-anchored menu inside the viewport. The menu prefers to grow
 * down and right, flips to the other side of the cursor when it fits there,
 * and clamps as a fallback when the viewport is smaller than the menu.
 */
export function placeContextMenu(
  x: number,
  y: number,
  menuWidth: number,
  menuHeight: number,
  viewportWidth: number,
  viewportHeight: number,
): ContextMenuLayout {
  const fitsRight = x + menuWidth + VIEWPORT_MARGIN <= viewportWidth;
  const fitsLeft = x - menuWidth >= VIEWPORT_MARGIN;
  const fitsBelow = y + menuHeight + VIEWPORT_MARGIN <= viewportHeight;
  const fitsAbove = y - menuHeight >= VIEWPORT_MARGIN;
  const growsRight = fitsRight || !fitsLeft;
  const growsDown = fitsBelow || !fitsAbove;

  return {
    left: clamp(
      growsRight ? x : x - menuWidth,
      VIEWPORT_MARGIN,
      viewportWidth - menuWidth - VIEWPORT_MARGIN,
    ),
    top: clamp(
      growsDown ? y : y - menuHeight,
      VIEWPORT_MARGIN,
      viewportHeight - menuHeight - VIEWPORT_MARGIN,
    ),
    origin: `${growsDown ? "top" : "bottom"}-${growsRight ? "left" : "right"}`,
  };
}
