type Bounds = Pick<DOMRect, "left" | "top" | "right" | "bottom">;

/** Use the first visible text fragment instead of centering between pages.
 * A retained comment can outlive scrolling its source out of view; its union
 * remains a stable fallback for the caller's collision clamp. */
export function pdfSelectionMenuAnchor(range: Range, bounds: Bounds): DOMRect {
  for (const rect of Array.from(range.getClientRects())) {
    const left = Math.max(rect.left, bounds.left);
    const top = Math.max(rect.top, bounds.top);
    const right = Math.min(rect.right, bounds.right);
    const bottom = Math.min(rect.bottom, bounds.bottom);
    if (right > left && bottom > top) return new DOMRect(left, top, right - left, bottom - top);
  }
  return range.getBoundingClientRect();
}

/** Center on the source. Keep Wuu's document and viewport collision protection
 * when the preferred position above the selection cannot fit. */
export function pdfSelectionMenuPosition(anchor: Bounds, menu: { width: number; height: number; gap: number }, bounds: Bounds) {
  const width = Math.min(menu.width, bounds.right - bounds.left);
  const height = Math.min(menu.height, bounds.bottom - bounds.top);
  const center = (anchor.left + anchor.right) / 2;
  const left = Math.max(bounds.left, Math.min(center - width / 2, bounds.right - width));
  const above = anchor.top - height - menu.gap >= bounds.top;
  const desiredTop = above ? anchor.top - height - menu.gap : anchor.bottom + menu.gap;
  const top = Math.max(bounds.top, Math.min(desiredTop, bounds.bottom - height));
  return { left, top, above };
}
