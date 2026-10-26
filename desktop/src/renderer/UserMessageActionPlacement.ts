const ACTION_GAP = 4;
const VIEWPORT_INSET = 8;

/** Position the action overlay without changing the message's layout box. */
export function placeUserMessageActions(actions: HTMLDivElement | null): (() => void) | undefined {
  const block = actions?.parentElement;
  // Layout-less renderers use the CSS fallback below the message.
  if (!actions || !block || typeof ResizeObserver === "undefined" || typeof Range.prototype.getBoundingClientRect !== "function") return;
  const touch = window.matchMedia("(hover: none), (pointer: coarse)");
  let inView = false;
  const visible = (): boolean => (touch.matches && inView) || block.matches(":hover, :focus-within");
  let frame = 0;
  let tracking = false;
  let disposed = false;

  const place = (): void => {
    if (!visible()) return;
    const viewport = window.visualViewport;
    let left = (viewport?.offsetLeft ?? 0) + VIEWPORT_INSET;
    let top = (viewport?.offsetTop ?? 0) + VIEWPORT_INSET;
    let right = (viewport?.offsetLeft ?? 0) + (viewport?.width ?? window.innerWidth) - VIEWPORT_INSET;
    let bottom = (viewport?.offsetTop ?? 0) + (viewport?.height ?? window.innerHeight) - VIEWPORT_INSET;
    const scroller = block.closest<HTMLElement>(".scroll-region");
    if (scroller) {
      const bounds = scroller.getBoundingClientRect();
      left = Math.max(left, bounds.left + VIEWPORT_INSET);
      top = Math.max(top, bounds.top + VIEWPORT_INSET);
      right = Math.min(right, bounds.right - VIEWPORT_INSET);
      bottom = Math.min(bottom, bounds.bottom - VIEWPORT_INSET);
    }
    const composer = block.closest(".conversation-pane")?.querySelector(".dock-composer-wrap .composer-shell");
    if (composer) bottom = Math.min(bottom, composer.getBoundingClientRect().top - ACTION_GAP);
    actions.style.maxWidth = `${Math.max(0, right - left)}px`;
    const width = actions.offsetWidth;
    const height = actions.offsetHeight;
    const blockBounds = block.getBoundingClientRect();
    const message = block.querySelector<HTMLElement>(".user-message");
    const bounds = message?.getBoundingClientRect() ?? blockBounds;
    // Off-screen messages must not pull their overlays into the visible pane.
    if (bounds.bottom < top || bounds.top > bottom) {
      actions.style.visibility = "hidden";
      return;
    }
    actions.style.visibility = "";
    let x = Math.max(left, Math.min(bounds.right - width, right - width));
    let y = bounds.bottom + ACTION_GAP;
    let placement = "below";

    // Only a final plain paragraph can lend its trailing whitespace. Code,
    // tables, attachments and an expansion control retain their own hit areas.
    const last = message?.querySelector<HTMLElement>(".rich-content > .rich-paragraph:last-child");
    if (message && last && !message.querySelector(".user-message-expand-toggle")) {
      const walker = document.createTreeWalker(last, NodeFilter.SHOW_TEXT);
      let tail: Text | undefined;
      for (let node = walker.nextNode(); node; node = walker.nextNode()) {
        if (node.textContent?.trim()) tail = node as Text;
      }
      if (tail) {
        const end = tail.data.trimEnd().length;
        const range = document.createRange();
        range.setStart(tail, end - 1);
        range.setEnd(tail, end);
        const glyph = range.getBoundingClientRect();
        const style = getComputedStyle(message);
        const contentRight = bounds.right - parseFloat(style.paddingRight);
        const contentBottom = bounds.bottom - parseFloat(style.paddingBottom);
        const lineHeight = parseFloat(getComputedStyle(last).lineHeight);
        const inlineX = contentRight - width;
        const inlineY = contentBottom - (lineHeight + height) / 2;
        // Larger touch targets can reach into the preceding wrapped line.
        range.selectNodeContents(last);
        const overlapsText = [...range.getClientRects()].some(rect =>
          rect.right + ACTION_GAP > inlineX && rect.left < contentRight
          && rect.bottom > inlineY && rect.top < inlineY + height);
        if (!overlapsText && contentBottom - glyph.bottom < lineHeight / 2
          && glyph.right + ACTION_GAP <= inlineX && inlineX >= left && contentRight <= right
          && inlineY >= Math.max(bounds.top, top) && inlineY + height <= Math.min(bounds.bottom, bottom)) {
          x = inlineX;
          y = inlineY;
          placement = "inline";
        }
      }
    }
    if (placement !== "inline") {
      const following = block.nextElementSibling;
      const belowLimit = Math.min(bottom, following ? following.getBoundingClientRect().top - ACTION_GAP : bottom);
      if (y + height > belowLimit) {
        y = bounds.top - height - ACTION_GAP;
        placement = "above";
      }
      y = Math.max(top, Math.min(y, bottom - height));
    }
    actions.dataset.placement = placement;
    actions.style.left = `${x - blockBounds.left}px`;
    actions.style.top = `${y - blockBounds.top}px`;
  };
  const schedule = (): void => {
    if (frame || !visible()) return;
    frame = requestAnimationFrame(() => { frame = 0; place(); });
  };
  const track = (): void => {
    if (!block.isConnected) return;
    actions.style.visibility = touch.matches && !inView ? "hidden" : "";
    const next = visible();
    if (next !== tracking) {
      tracking = next;
      const method = next ? "addEventListener" : "removeEventListener";
      document[method]("scroll", schedule, true);
      window[method]("resize", schedule);
      window.visualViewport?.[method]("resize", schedule);
      window.visualViewport?.[method]("scroll", schedule);
    }
    if (next) place();
  };
  const intersection = new IntersectionObserver(([entry]) => { inView = entry.isIntersecting; track(); });
  intersection.observe(block);
  const observer = new ResizeObserver(schedule);
  observer.observe(block);
  observer.observe(actions);
  const composer = block.closest(".conversation-pane")?.querySelector(".dock-composer-wrap .composer-shell");
  if (composer) observer.observe(composer);
  block.addEventListener("pointerenter", track);
  block.addEventListener("pointerleave", track);
  block.addEventListener("focusin", track);
  block.addEventListener("focusout", scheduleTracking);
  touch.addEventListener("change", track);
  // Focusout fires before the browser updates :focus-within.
  function scheduleTracking(): void { queueMicrotask(track); }
  track();
  // FontFaceSet.ready flushes pending styles. Read it after this commit's refs
  // attach so each message does not flush the preceding message's style writes.
  queueMicrotask(() => {
    if (disposed || !block.isConnected) return;
    void document.fonts?.ready.then(() => { if (!disposed && block.isConnected) schedule(); });
  });
  return () => {
    disposed = true;
    cancelAnimationFrame(frame);
    observer.disconnect();
    intersection.disconnect();
    document.removeEventListener("scroll", schedule, true);
    window.removeEventListener("resize", schedule);
    window.visualViewport?.removeEventListener("resize", schedule);
    window.visualViewport?.removeEventListener("scroll", schedule);
    block.removeEventListener("pointerenter", track);
    block.removeEventListener("pointerleave", track);
    block.removeEventListener("focusin", track);
    block.removeEventListener("focusout", scheduleTracking);
    touch.removeEventListener("change", track);
  };
}
