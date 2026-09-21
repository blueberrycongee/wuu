/*
 * Runtime scrollbar gutter measurement.
 *
 * The conversation composer and the message flow are centered in different
 * containing blocks: the flow lives inside .scroll-region, whose content box
 * already excludes the real scrollbar gutter (scrollbar-gutter: stable
 * both-edges), while the dock composer overlays the full grid cell and mirrors
 * that box with padding-inline: var(--scrollbar-width). For those two boxes to
 * share one centerline, --scrollbar-width must equal the platform's ACTUAL
 * gutter width, not a static design value:
 *
 *   - macOS overlay scrollbars occupy 0px of layout, so any fixed value
 *     shifts the composer off the flow.
 *   - Windows/Linux classic scrollbars occupy a platform-dependent width
 *     that a fixed 10px only approximates.
 *
 * Chromium 121+ (Electron 42) also ignores the legacy ::-webkit-scrollbar
 * width once scrollbar-width is set, so the static token is not even the
 * rendered scrollbar width. Measure the real width before first paint with
 * a probe that inherits the same global scrollbar cascade, then stamp the
 * token on the document root. Every consumer (the composer's gutter mirror,
 * Monaco sizing) then reads the same platform-true value on every machine.
 */

export function measureScrollbarGutterWidth(): number {
  if (typeof document === "undefined") {
    return 0;
  }
  const host = document.body ?? document.documentElement;
  const probe = document.createElement("div");
  probe.setAttribute("aria-hidden", "true");
  probe.style.position = "fixed";
  probe.style.top = "-10000px";
  probe.style.left = "-10000px";
  probe.style.width = "100px";
  probe.style.height = "100px";
  probe.style.overflowY = "scroll";
  probe.style.visibility = "hidden";
  const fill = document.createElement("div");
  fill.style.height = "200px";
  probe.appendChild(fill);
  host.appendChild(probe);
  // For classic scrollbars this is the gutter width the scrollbar takes
  // out of the content box; overlay scrollbars report 0. The probe inherits
  // the global `* { scrollbar-width: thin; ... }` cascade, so it measures
  // exactly what .scroll-region's stable gutter reserves.
  const width = probe.offsetWidth - probe.clientWidth;
  probe.remove();
  return Math.max(0, Math.round(width));
}

export function applyMeasuredScrollbarWidth(): void {
  if (typeof document === "undefined") {
    return;
  }
  document.documentElement.style.setProperty(
    "--scrollbar-width",
    `${measureScrollbarGutterWidth()}px`,
  );
}

/**
 * Re-sync the measured gutter when the window regains focus. The boot-time
 * measurement covers the platform's default; this picks up rare mid-session
 * changes (e.g. the OS "show scroll bars" setting flipping between overlay
 * and classic). Deliberately NOT hooked to resize: the gutter width is
 * constant during a live resize, and adding a synchronous layout read to
 * the resize path would spend drag frame budget for nothing.
 */
export function startScrollbarWidthSync(): void {
  if (typeof window === "undefined") {
    return;
  }
  window.addEventListener("focus", () => {
    applyMeasuredScrollbarWidth();
  });
}
