/** Keep the workbench inside the visible area without guessing keyboard state. */
export function startWebViewportSync(): () => void {
  const root = document.documentElement;
  const viewport = window.visualViewport;
  let frame: number | undefined;
  let height: number | undefined;
  let revealTimer: ReturnType<typeof setTimeout> | undefined;

  const revealField = (): void => {
    const field = document.activeElement;
    if (!(field instanceof HTMLElement) || !field.matches('input, textarea')) return;
    const form = field.closest<HTMLElement>('.account-home, .web-gate');
    if (!form || (viewport && viewport.scale !== 1)) return;
    const bounds = form.getBoundingClientRect();
    const rect = field.getBoundingClientRect();
    const top = Math.max(bounds.top, viewport?.offsetTop ?? 0) + 12;
    const bottom = Math.min(bounds.bottom, (viewport?.offsetTop ?? 0) + (viewport?.height ?? window.innerHeight)) - 12;
    if (bottom <= top) return;
    // Correct only residual occlusion after the browser/IME's focus scroll.
    // Scrolling this container cannot pan the WebView or move an outer shell.
    if (rect.bottom > bottom) form.scrollTop += Math.min(rect.bottom - bottom, rect.top - top);
    else if (rect.top < top) form.scrollTop -= top - rect.top;
  };
  const scheduleReveal = (): void => {
    clearTimeout(revealTimer);
    revealTimer = setTimeout(revealField, 120);
  };

  const update = (): void => {
    frame = undefined;
    // Pinch zoom changes the visual viewport too. Relaying it into layout
    // would reflow the conversation while the reader is magnifying it.
    if (viewport && viewport.scale !== 1) return;
    const nextHeight = viewport?.height ?? window.innerHeight;
    if (!Number.isFinite(nextHeight) || nextHeight <= 0 || height === nextHeight) return;
    height = nextHeight;
    root.style.setProperty("--web-viewport-height", `${nextHeight}px`);
    scheduleReveal();
  };
  const schedule = (): void => {
    // Keyboard and browser chrome can emit both resize events before a paint.
    // Apply their latest geometry once, without adding a second animation
    // that would trail behind the browser's own keyboard transition.
    frame ??= window.requestAnimationFrame(update);
  };

  update();
  viewport?.addEventListener("resize", schedule);
  viewport?.addEventListener("scroll", scheduleReveal);
  window.addEventListener("resize", schedule);
  window.addEventListener("pageshow", schedule);
  document.addEventListener("focusin", scheduleReveal);
  return () => {
    viewport?.removeEventListener("resize", schedule);
    viewport?.removeEventListener("scroll", scheduleReveal);
    window.removeEventListener("resize", schedule);
    window.removeEventListener("pageshow", schedule);
    document.removeEventListener("focusin", scheduleReveal);
    clearTimeout(revealTimer);
    if (frame !== undefined) window.cancelAnimationFrame(frame);
    root.style.removeProperty("--web-viewport-height");
  };
}
