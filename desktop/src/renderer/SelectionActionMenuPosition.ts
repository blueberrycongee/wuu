type Bounds = Pick<DOMRect, "left" | "top" | "right" | "bottom">;

/** Place the complete popup, beside its source. */
export function selectionActionMenuMetrics(menu: HTMLElement | null, fallback: { width: number; height: number }) {
  const box = menu?.getBoundingClientRect();
  // Entrance transforms must not move the settled popup off its source.
  const width = menu?.offsetWidth || box?.width || fallback.width;
  const height = menu?.offsetHeight || box?.height || fallback.height;
  return { width, height, gap: 8 };
}

export function selectionActionMenuPosition(anchor: Bounds, menu: { width: number; height: number; gap: number }, bounds: Bounds) {
  const width = Math.min(menu.width, bounds.right - bounds.left);
  const left = Math.max(bounds.left, Math.min(anchor.left, bounds.right - width));
  const above = anchor.top - menu.height - menu.gap >= bounds.top;
  const top = above ? anchor.top - menu.height - menu.gap
    : Math.max(bounds.top, Math.min(anchor.bottom + menu.gap, bounds.bottom - menu.height));
  return { left, top, above };
}
