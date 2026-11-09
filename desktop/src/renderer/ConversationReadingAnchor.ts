/** A reading location is content-relative; output appended below it must not move it. */
export type ConversationReadingAnchor = { offset: number } & (
  | { turnID: string; messageID?: never }
  | { messageID: string; turnID?: never }
);

function readingItems(viewport: HTMLElement): HTMLElement[] {
  const scope = viewport.querySelector<HTMLElement>('.cached-conversation-pane[data-active="true"]') ?? viewport;
  const turns = [...scope.querySelectorAll<HTMLElement>("[data-turn-id]")];
  return turns.length ? turns : [...scope.querySelectorAll<HTMLElement>("[data-message-id]")];
}

export function captureReadingAnchor(viewport: HTMLElement): ConversationReadingAnchor | undefined {
  const turns = readingItems(viewport);
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
  const offset = rect.top - top;
  return turn.dataset.turnId
    ? { turnID: turn.dataset.turnId, offset }
    : { messageID: turn.dataset.messageId!, offset };
}

export function readingAnchorScrollTop(viewport: HTMLElement, anchor: ConversationReadingAnchor): number | undefined {
  const turn = readingItems(viewport).find(turn => anchor.turnID !== undefined
    ? turn.dataset.turnId === anchor.turnID
    : turn.dataset.messageId === anchor.messageID);
  if (!turn) return undefined;
  const rect = turn.getBoundingClientRect();
  if (rect.height <= 0) return undefined;
  return viewport.scrollTop + rect.top - viewport.getBoundingClientRect().top - anchor.offset;
}
