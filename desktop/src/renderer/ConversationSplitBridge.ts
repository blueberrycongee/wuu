/**
 * Shell bridge for opening a thread in the conversation split (the middle
 * column). The conversation area splits into two panes and the requested
 * session becomes the secondary (right) pane.
 *
 * ThreadItemView is deep in the memoized turn tree, so it cannot receive a
 * per-item callback for this without drilling through every pane layer. The
 * App registers its handler once at mount; message actions call this module.
 */

export type OpenThreadInSplit = (threadID: string) => void;

let openThreadInSplitHandler: OpenThreadInSplit | undefined;

export function setOpenThreadInSplitHandler(
  handler: OpenThreadInSplit | undefined,
): void {
  openThreadInSplitHandler = handler;
}

export function requestOpenThreadInSplit(threadID: string): void {
  openThreadInSplitHandler?.(threadID);
}
