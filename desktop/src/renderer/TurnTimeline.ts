import type { ThreadItem } from "../shared/protocol";

export type TurnTimelineSegment = {
  userItems: ThreadItem[];
  assistantItems: ThreadItem[];
};

/**
 * Split one turn at its user messages so steering input renders after the
 * assistant prefix it interrupted, not above every assistant item in the turn.
 */
export function buildTurnTimeline(
  items: readonly ThreadItem[],
): TurnTimelineSegment[] {
  const segments: TurnTimelineSegment[] = [];
  let current: TurnTimelineSegment = { userItems: [], assistantItems: [] };
  for (const item of items) {
    if (item.type === "user_message") {
      if (current.assistantItems.length > 0) {
        segments.push(current);
        current = { userItems: [], assistantItems: [] };
      }
      current.userItems.push(item);
      continue;
    }
    current.assistantItems.push(item);
  }
  if (current.userItems.length > 0 || current.assistantItems.length > 0) {
    segments.push(current);
  }
  return segments;
}

/**
 * Earlier segments are settled history. Keep a steered partial answer visible
 * as the segment's answer instead of letting the generic process fold hide it.
 */
export function displayItemsForSettledSegment(
  items: readonly ThreadItem[],
): ThreadItem[] {
  return items.map((item) =>
    item.type === "agent_message" && item.cause === "steered_stream_fragment"
      ? { ...item, terminal: true }
      : item,
  );
}
