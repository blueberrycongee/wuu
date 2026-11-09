/*
 * Scrollbar reveal — the single owner of the `.scrollbar-visible` class.
 *
 * The renderer paints a scrollbar only while its container is actually being
 * scrolled (see styles/scrollbars.css). Two paths start that reveal:
 *
 *   - `startScrollbarReveal()` installs one capture-phase scroll listener for
 *     the whole document. `scroll` does not bubble, but it does travel the
 *     capture path, so every scroll container — including ones mounted later —
 *     is covered without per-component wiring.
 *   - `revealScrollbar(node)` is the direct entry for controllers that hold
 *     their scroll node and decide for themselves when a reveal is warranted.
 *
 * A controller that reveals its own node must register that node with
 * `markScrollbarRevealSelfManaged()`. Those surfaces also scroll
 * programmatically — following streamed content, adopting a resized viewport —
 * and the global listener cannot tell those frames apart from a user gesture.
 * Left unregistered, the thumb would stay painted for the whole response
 * instead of fading out after the reader's last scroll.
 *
 * Hovering a region deliberately reveals nothing: the pointer rests inside a
 * bounded tool/reasoning strip while reading, and a thumb drawn over that text
 * is noise. The reserved gutter keeps the layout stable either way.
 */
/** Class the stylesheet keys the painted thumb off. */
export const SCROLLBAR_REVEAL_CLASS = "scrollbar-visible";

/**
 * How long a revealed thumb stays painted after the container's last scroll.
 * Every surface shares this window so a thumb behaves the same wherever it is
 * painted; the stylesheet fades it out over the tail of it.
 */
export const SCROLLBAR_REVEAL_HIDE_DELAY_MS = 700;

const hideTimers = new WeakMap<HTMLElement, number>();
const selfManagedNodes = new WeakSet<HTMLElement>();

function overflows(node: HTMLElement): boolean {
  return node.scrollHeight > node.clientHeight || node.scrollWidth > node.clientWidth;
}

/**
 * Paint `node`'s thumb now and fade it out once scrolling stops. Repeated
 * calls extend the same window, so a scroll burst leaves one timer behind.
 * Silent for containers that do not overflow: they have no thumb to reveal.
 */
export function revealScrollbar(node: HTMLElement): void {
  if (!overflows(node)) {
    return;
  }
  const pending = hideTimers.get(node);
  if (pending !== undefined) {
    window.clearTimeout(pending);
  }
  node.classList.add(SCROLLBAR_REVEAL_CLASS);
  hideTimers.set(
    node,
    window.setTimeout(() => {
      hideTimers.delete(node);
      node.classList.remove(SCROLLBAR_REVEAL_CLASS);
    }, SCROLLBAR_REVEAL_HIDE_DELAY_MS),
  );
}

/**
 * Exempt `node` from the global listener because its controller owns the
 * reveal. Idempotent, and never unregistered: the entry is keyed by the node
 * itself, so a replaced node is collected with it.
 */
export function markScrollbarRevealSelfManaged(node: HTMLElement): void {
  selfManagedNodes.add(node);
}

/** Install the document-wide reveal listener. Returns the uninstaller. */
export function startScrollbarReveal(): () => void {
  const handleScroll = (event: Event): void => {
    const node = event.target;
    // The root scroller reports `document`; only element scrolling paints a
    // gutter that needs a thumb.
    if (!(node instanceof HTMLElement) || selfManagedNodes.has(node)) {
      return;
    }
    revealScrollbar(node);
  };
  document.addEventListener("scroll", handleScroll, { capture: true, passive: true });
  return () => {
    document.removeEventListener("scroll", handleScroll, { capture: true });
  };
}
