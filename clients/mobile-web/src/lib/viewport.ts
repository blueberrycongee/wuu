/** Keep the workbench inside the visible area without guessing keyboard state. */
export function startWebViewportSync(): () => void {
  const root = document.documentElement;
  const viewport = window.visualViewport;
  let frame: number | undefined;
  let height: number | undefined;

  const update = (): void => {
    frame = undefined;
    // Pinch zoom changes the visual viewport too. Relaying it into layout
    // would reflow the conversation while the reader is magnifying it.
    if (viewport && viewport.scale !== 1) return;
    const nextHeight = viewport?.height ?? window.innerHeight;
    if (!Number.isFinite(nextHeight) || nextHeight <= 0 || height === nextHeight) return;
    const shrinking = height !== undefined && nextHeight < height;
    height = nextHeight;
    root.style.setProperty("--web-viewport-height", `${nextHeight}px`);
    const focused = document.activeElement;
    if (shrinking && focused instanceof HTMLElement && focused.matches('input, textarea') && focused.closest('.account-home, .web-gate')) {
      // Native IME resizing happens after the browser's first focus scroll.
      // Reconcile once the form has its final visible height, without resizing fields.
      focused.scrollIntoView({ block: 'nearest', inline: 'nearest', behavior: 'instant' });
    }
  };
  const schedule = (): void => {
    // Keyboard and browser chrome can emit both resize events before a paint.
    // Apply their latest geometry once, without adding a second animation
    // that would trail behind the browser's own keyboard transition.
    frame ??= window.requestAnimationFrame(update);
  };

  update();
  viewport?.addEventListener("resize", schedule);
  window.addEventListener("resize", schedule);
  window.addEventListener("pageshow", schedule);
  return () => {
    viewport?.removeEventListener("resize", schedule);
    window.removeEventListener("resize", schedule);
    window.removeEventListener("pageshow", schedule);
    if (frame !== undefined) window.cancelAnimationFrame(frame);
    root.style.removeProperty("--web-viewport-height");
  };
}
