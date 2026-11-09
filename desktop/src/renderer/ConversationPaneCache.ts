import type { Thread } from "../shared/protocol";
import {
  TURN_LIST_COLLAPSE_THRESHOLD,
  TURN_LIST_RECENT_FULL_TURNS,
} from "./ConversationTurnPolicy";

export const MAX_CACHED_CONVERSATION_PANES = 8;
export const CACHED_CONVERSATION_RENDER_BUDGET = 240;

export function retainCachedConversationPaneThreads({
  threadIDs,
  currentThreadsByID,
  previousThreadsByID,
}: {
  threadIDs: readonly string[];
  currentThreadsByID: ReadonlyMap<string, Thread>;
  previousThreadsByID: ReadonlyMap<string, Thread>;
}): Map<string, Thread> {
  const retained = new Map<string, Thread>();
  for (const threadID of threadIDs) {
    const thread =
      currentThreadsByID.get(threadID) ?? previousThreadsByID.get(threadID);
    if (thread) {
      retained.set(threadID, thread);
    }
  }
  return retained;
}

export function conversationPaneRenderWeight(thread: Thread | undefined): number {
  const turns = thread?.turns ?? [];
  if (turns.length <= TURN_LIST_COLLAPSE_THRESHOLD) {
    return Math.max(1, turns.length);
  }

  const firstRecentFullIndex = Math.max(
    0,
    turns.length - TURN_LIST_RECENT_FULL_TURNS,
  );
  let fullTurns = 0;
  let collapsedTurns = 0;
  turns.forEach((turn, index) => {
    if (index >= firstRecentFullIndex || turn.status === "in_progress") {
      fullTurns += 1;
    } else {
      collapsedTurns += 1;
    }
  });
  return Math.max(1, Math.ceil(fullTurns + collapsedTurns / 10));
}

export function selectCachedConversationPaneIDs({
  activeThreadID,
  previousThreadIDs,
  openThreadIDs,
  threadsByID,
}: {
  activeThreadID?: string;
  previousThreadIDs: readonly string[];
  openThreadIDs: ReadonlySet<string>;
  threadsByID: ReadonlyMap<string, Thread>;
}): string[] {
  const candidates = [
    ...(activeThreadID ? [activeThreadID] : []),
    ...previousThreadIDs.filter(
      (threadID) => threadID !== activeThreadID && openThreadIDs.has(threadID),
    ),
  ];
  const selected: string[] = [];
  const seen = new Set<string>();
  let renderWeight = 0;

  for (const threadID of candidates) {
    if (seen.has(threadID) || !openThreadIDs.has(threadID)) {
      continue;
    }
    seen.add(threadID);
    const weight = conversationPaneRenderWeight(threadsByID.get(threadID));
    const isActive = threadID === activeThreadID;
    if (
      !isActive &&
      (selected.length >= MAX_CACHED_CONVERSATION_PANES ||
        renderWeight + weight > CACHED_CONVERSATION_RENDER_BUDGET)
    ) {
      continue;
    }
    selected.push(threadID);
    renderWeight += weight;
  }

  return selected;
}
