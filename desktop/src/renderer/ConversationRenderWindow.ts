/**
 * Renders the turns around a conversation viewport before a large scroll
 * paints.
 *
 * Off-screen turns skip layout and paint through content-visibility, and
 * Chromium decides which of them became relevant only after a frame has
 * painted. A scroll that crosses more than its margin in one frame — a
 * scrollbar drag, a fling, a programmatic jump — would paint the turns it
 * lands on blank. Scroll events run before their frame renders, so marking the
 * landing turns there renders them in that same frame.
 */

const NEAR_ATTRIBUTE = "data-near";
/** Chromium's own relevance margin (half a viewport) covers smaller moves. */
const LARGE_SCROLL_VIEWPORTS = 0.5;
/** Viewports rendered above and below the visible one after a large scroll. */
const NEAR_VIEWPORTS = 1;

type RenderWindow = { top: number; height: number; marked: HTMLElement[] };

const windows = new WeakMap<HTMLElement, RenderWindow>();

export function syncConversationRenderWindow(viewport: HTMLElement): void {
  const list = viewport.querySelector<HTMLElement>(
    '.cached-conversation-pane[data-active="true"] .conversation-width',
  ) ?? viewport.querySelector<HTMLElement>(".conversation-width");
  if (!list) return;
  const top = viewport.scrollTop;
  const height = viewport.clientHeight;
  const previous = windows.get(list);
  if (previous && previous.height === height &&
    Math.abs(top - previous.top) < height * LARGE_SCROLL_VIEWPORTS) return;
  const turns = list.getElementsByClassName("turn");
  const viewportTop = viewport.getBoundingClientRect().top;
  const from = viewportTop - height * NEAR_VIEWPORTS;
  const to = viewportTop + height * (1 + NEAR_VIEWPORTS);
  // Turns are vertically ordered. Reading a skipped turn's own box uses its
  // remembered size and does not lay out its contents.
  let low = 0;
  let high = turns.length;
  while (low < high) {
    const mid = (low + high) >>> 1;
    if (turns[mid].getBoundingClientRect().bottom < from) low = mid + 1;
    else high = mid;
  }
  const marked: HTMLElement[] = [];
  for (let index = low; index < turns.length; index += 1) {
    const turn = turns[index] as HTMLElement;
    if (turn.getBoundingClientRect().top > to) break;
    marked.push(turn);
  }
  for (const turn of previous?.marked ?? []) {
    if (!marked.includes(turn)) turn.removeAttribute(NEAR_ATTRIBUTE);
  }
  for (const turn of marked) {
    if (!turn.hasAttribute(NEAR_ATTRIBUTE)) turn.setAttribute(NEAR_ATTRIBUTE, "");
  }
  windows.set(list, { top, height, marked });
}
