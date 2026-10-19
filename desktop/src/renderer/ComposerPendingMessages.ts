import type { Thread } from "../shared/protocol";
import { OPTIMISTIC_TURN_ID_PREFIX, type QueuedComposerMessage } from "./ComposerMessages";

export type ThreadPendingComposerMessages = {
  queued: QueuedComposerMessage[];
  guides: QueuedComposerMessage[];
};

export type PendingComposerMessagesByThread = Record<
  string,
  ThreadPendingComposerMessages
>;

export type LocatedPendingComposerMessage = {
  threadID: string;
  index: number;
  message: QueuedComposerMessage;
};

export type PendingComposerMessageRemovalScope = "queue" | "guide" | "all";
export type PendingComposerMessageKind = "queue" | "guide";

export function emptyThreadPendingComposerMessages(): ThreadPendingComposerMessages {
  return { queued: [], guides: [] };
}

export function pendingComposerMessagesForThread(
  messagesByThread: PendingComposerMessagesByThread,
  threadID: string | undefined,
): ThreadPendingComposerMessages {
  if (!threadID) {
    return emptyThreadPendingComposerMessages();
  }
  return messagesByThread[threadID] ?? emptyThreadPendingComposerMessages();
}

export function threadPendingComposerMessagesIsEmpty(
  pending: ThreadPendingComposerMessages,
): boolean {
  return pending.queued.length === 0 && pending.guides.length === 0;
}

/**
 * Append an optimistic pending message without duplicating a server-confirmed
 * entry. Queue ids identify a message across both queue and guide states, so a
 * late IPC response must not recreate an item already supplied by a snapshot.
 */
export function appendPendingComposerMessage(
  previous: ThreadPendingComposerMessages,
  kind: PendingComposerMessageKind,
  message: QueuedComposerMessage,
): ThreadPendingComposerMessages {
  if (
    previous.queued.some((candidate) => candidate.id === message.id) ||
    previous.guides.some((candidate) => candidate.id === message.id)
  ) {
    return previous;
  }
  return kind === "queue"
    ? { ...previous, queued: [...previous.queued, message] }
    : { ...previous, guides: [...previous.guides, message] };
}

/**
 * Reconcile the authoritative held snapshot with optimistic pending state.
 * The snapshot replaces all prior held entries and wins over a non-held entry
 * with the same id, while unrelated in-flight optimistic messages survive.
 */
export function applyHeldComposerSnapshot(
  previous: ThreadPendingComposerMessages,
  snapshot: QueuedComposerMessage[],
): ThreadPendingComposerMessages {
  const snapshotIDs = new Set(snapshot.map((message) => message.id));
  return {
    queued: [
      ...previous.queued.filter(
        (message) => !message.held && !snapshotIDs.has(message.id),
      ),
      ...snapshot.filter((message) => message.origin === "queue"),
    ],
    guides: [
      ...previous.guides.filter(
        (message) => !message.held && !snapshotIDs.has(message.id),
      ),
      ...snapshot.filter((message) => message.origin === "steer"),
    ],
  };
}

export function applyAuthoritativeComposerSnapshot(
  previous: ThreadPendingComposerMessages,
  snapshot: QueuedComposerMessage[],
): ThreadPendingComposerMessages {
  const snapshotIDs = new Set(snapshot.map((message) => message.id));
  const localQueued = previous.queued.filter(
    (message) => Boolean(message.operationState) && !snapshotIDs.has(message.id),
  );
  const localGuides = previous.guides.filter(
    (message) => Boolean(message.operationState) && !snapshotIDs.has(message.id),
  );
  return {
    queued: [
      ...snapshot.filter((message) => message.origin === "queue"),
      ...localQueued,
    ],
    guides: [
      ...snapshot.filter((message) => message.origin === "steer"),
      ...localGuides,
    ],
  };
}

export function removePendingComposerMessagesByID(
  messagesByThread: PendingComposerMessagesByThread,
  threadID: string | undefined,
  id: string,
  scope: PendingComposerMessageRemovalScope = "all",
): PendingComposerMessagesByThread {
  if (!threadID || !id) {
    return messagesByThread;
  }
  const previous = messagesByThread[threadID];
  if (!previous) {
    return messagesByThread;
  }
  const next: ThreadPendingComposerMessages = {
    queued:
      scope === "guide"
        ? previous.queued
        : previous.queued.filter((message) => message.id !== id),
    guides:
      scope === "queue"
        ? previous.guides
        : previous.guides.filter((message) => message.id !== id),
  };
  if (
    next.queued.length === previous.queued.length &&
    next.guides.length === previous.guides.length
  ) {
    return messagesByThread;
  }
  const nextByThread = { ...messagesByThread };
  if (threadPendingComposerMessagesIsEmpty(next)) {
    delete nextByThread[threadID];
  } else {
    nextByThread[threadID] = next;
  }
  return nextByThread;
}

export function findPendingComposerMessage(
  messagesByThread: PendingComposerMessagesByThread,
  id: string,
  kind: "queue" | "guide",
  preferredThreadID?: string,
): LocatedPendingComposerMessage | undefined {
  const field = kind === "queue" ? "queued" : "guides";
  const threadIDs = [
    ...(preferredThreadID ? [preferredThreadID] : []),
    ...Object.keys(messagesByThread).filter(
      (threadID) => threadID !== preferredThreadID,
    ),
  ];
  for (const threadID of threadIDs) {
    const messages = messagesByThread[threadID]?.[field] ?? [];
    const index = messages.findIndex((message) => message.id === id);
    if (index >= 0) {
      const message = messages[index];
      return { threadID, index, message };
    }
  }
  return undefined;
}

export function pendingComposerMessageCount(
  pending: ThreadPendingComposerMessages,
): number {
  return pending.queued.length + pending.guides.length;
}

/**
 * Collect the ids of user messages that have already MATERIALIZED as
 * user_message turn items in a thread. A queued/guide composer message keeps
 * the same id (the server stamps the item's `source_id` with the queue/steer
 * client id — see startQueuedTurn / steer), so any pending entry whose id is
 * in this set has already been sent.
 */
export function materializedComposerMessageIDs(
  thread: Thread | undefined | null,
): Set<string> {
  const ids = new Set<string>();
  for (const turn of thread?.turns ?? []) {
    if (turn.id.startsWith(OPTIMISTIC_TURN_ID_PREFIX)) continue;
    for (const item of turn.items ?? []) {
      if (
        item.type === "user_message" &&
        typeof item.source_id === "string" &&
        item.source_id
      ) {
        ids.add(item.source_id);
      }
    }
  }
  return ids;
}

/**
 * Reconcile a thread's pending composer messages against its materialized
 * turns: drop any queued/guide entry whose id already appears as a
 * user_message `source_id` in the thread. This is the self-healing net for a
 * missed removal notification — a queued send's `turn/started` (carrying the
 * queue_id) or the user_message's `item/completed` can be gated out of the
 * renderer when it arrives while the thread is backgrounded (App.tsx's
 * active-context filter), leaving the entry stuck in the composer queue strip
 * / chat "发送中…" bubble even though the message already went out. Keying off
 * the authoritative thread turns guarantees a sent message never lingers as
 * pending. Returns the same reference when nothing changes.
 */
export function reconcilePendingComposerMessagesForThread(
  messagesByThread: PendingComposerMessagesByThread,
  thread: Thread | undefined | null,
): PendingComposerMessagesByThread {
  const threadID = thread?.id;
  if (!threadID) {
    return messagesByThread;
  }
  const previous = messagesByThread[threadID];
  if (!previous) {
    return messagesByThread;
  }
  const materialized = materializedComposerMessageIDs(thread);
  if (materialized.size === 0) {
    return messagesByThread;
  }
  const next: ThreadPendingComposerMessages = {
    queued: previous.queued.filter((message) => !materialized.has(message.id)),
    guides: previous.guides.filter((message) => !materialized.has(message.id)),
  };
  if (
    next.queued.length === previous.queued.length &&
    next.guides.length === previous.guides.length
  ) {
    return messagesByThread;
  }
  const nextByThread = { ...messagesByThread };
  if (threadPendingComposerMessagesIsEmpty(next)) {
    delete nextByThread[threadID];
  } else {
    nextByThread[threadID] = next;
  }
  return nextByThread;
}
