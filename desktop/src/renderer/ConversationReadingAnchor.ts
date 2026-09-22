/** A reading location is content-relative; output appended below it must not move it. */
export type ConversationReadingAnchor = { turnID: string; offset: number };

function readingTurns(viewport: HTMLElement): HTMLElement[] {
  const scope = viewport.querySelector<HTMLElement>('.cached-conversation-pane[data-active="true"]') ?? viewport;
  return [...scope.querySelectorAll<HTMLElement>('[data-turn-id]')];
}

export function captureReadingAnchor(viewport: HTMLElement): ConversationReadingAnchor | undefined {
  const turns = readingTurns(viewport);
  const top = viewport.getBoundingClientRect().top;
  // Turns are ordered vertically. Avoid laying out every old turn on each scroll.
  let low = 0;
  let high = turns.length;
  while (low < high) {
    const mid = (low + high) >>> 1;
    if (turns[mid].getBoundingClientRect().bottom <= top) low = mid + 1;
    else high = mid;
  }
  const turn = turns[low];
  if (!turn) return undefined;
  const rect = turn.getBoundingClientRect();
  if (rect.height <= 0 || rect.top >= top + viewport.clientHeight) return undefined;
  return { turnID: turn.dataset.turnId!, offset: rect.top - top };
}

export function readingAnchorScrollTop(viewport: HTMLElement, anchor: ConversationReadingAnchor): number | undefined {
  const turn = readingTurns(viewport).find(turn => turn.dataset.turnId === anchor.turnID);
  if (!turn) return undefined;
  const rect = turn.getBoundingClientRect();
  if (rect.height <= 0) return undefined;
  return viewport.scrollTop + rect.top - viewport.getBoundingClientRect().top - anchor.offset;
}
