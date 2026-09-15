import { useCallback, useLayoutEffect, type RefObject } from "react";

// Measure the live textarea, not the deferred draft in the surrounding composer.
// This also counts soft wraps and preserves the browser's IME/selection state.
export function useComposerHeight(ref: RefObject<HTMLTextAreaElement | null>, expanded: boolean, value: string): void {
  const resize = useCallback(() => {
    const input = ref.current;
    const composer = input?.closest<HTMLElement>(".composer");
    const stack = input?.closest<HTMLElement>(".composer-stack");
    if (!input || !composer || !stack) return;
    if (!input.offsetWidth) {
      stack.style.removeProperty("--composer-input-growth");
      return;
    }

    const scrollTop = input.scrollTop;
    input.style.height = "0px";
    const baseInputHeight = input.offsetHeight;
    const baseComposerHeight = composer.offsetHeight;
    const chromeHeight = baseComposerHeight - baseInputHeight;
    input.style.setProperty("--composer-chrome-height", `${chromeHeight}px`);
    const style = getComputedStyle(input);
    const borderHeight = parseFloat(style.borderTopWidth) + parseFloat(style.borderBottomWidth);
    const maximum = Math.max(baseInputHeight, parseFloat(style.maxHeight));
    const desired = expanded ? maximum : input.value ? input.scrollHeight + borderHeight : baseInputHeight;
    input.style.height = `${Math.max(baseInputHeight, Math.min(maximum, desired))}px`;
    input.scrollTop = scrollTop;

    // Grow upward without moving the send controls or the conversation's scroll
    // anchor. Put the offset on the common ancestor of the frame and queue tray.
    stack.style.setProperty("--composer-input-growth", `${Math.max(0, composer.offsetHeight - baseComposerHeight)}px`);
  }, [expanded, ref]);

  // Run after every local value commit, including history, paste, and send-clear.
  useLayoutEffect(resize, [resize, value]);
  useLayoutEffect(() => {
    const input = ref.current;
    if (!input) return;
    // Resizing an observed ancestor inside its delivery can invalidate another
    // observer's measurements. Coalesce layout notifications into the next frame.
    let frame = 0;
    const scheduleResize = () => {
      cancelAnimationFrame(frame);
      frame = requestAnimationFrame(resize);
    };
    const observer = typeof ResizeObserver === "undefined" ? null : new ResizeObserver(scheduleResize);
    observer?.observe(input);
    if (input.parentElement) observer?.observe(input.parentElement);
    // Font preferences are inherited CSS variables; a fixed-size textarea can
    // rewrap without emitting a resize notification of its own.
    const preferences = new MutationObserver(scheduleResize);
    preferences.observe(document.documentElement, { attributes: true, attributeFilter: ["style", "class"] });
    preferences.observe(document.body, { attributes: true, attributeFilter: ["style", "class"] });
    window.addEventListener("resize", scheduleResize);
    window.visualViewport?.addEventListener("resize", scheduleResize);
    document.fonts?.addEventListener("loadingdone", scheduleResize);
    return () => {
      observer?.disconnect();
      cancelAnimationFrame(frame);
      preferences.disconnect();
      window.removeEventListener("resize", scheduleResize);
      window.visualViewport?.removeEventListener("resize", scheduleResize);
      document.fonts?.removeEventListener("loadingdone", scheduleResize);
    };
  }, [ref, resize]);
}
