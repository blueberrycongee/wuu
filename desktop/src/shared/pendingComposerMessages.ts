import type { AppServerNotification } from "./protocol";

// Content stays with the caller: renderer entries and wire messages share only
// the identity/state needed to reconcile accepted input.
export type PendingComposerEntry = {
  id: string;
  origin?: "queue" | "steer";
  held?: boolean;
  operationState?: unknown;
};
export type PendingComposerMessages<T extends PendingComposerEntry> = { queued: T[]; guides: T[] };
export type PendingComposerMessageKind = "queue" | "guide";
export type PendingComposerMessageRemovalScope = "queue" | "guide" | "all";

/**
 * Append an optimistic pending message without duplicating a server-confirmed
 * entry. Queue ids identify a message across both queue and guide states, so a
 * late IPC response must not recreate an item already supplied by a snapshot.
 */
export function appendPendingComposerMessage<T extends PendingComposerEntry>(
  previous: PendingComposerMessages<T>,
  kind: PendingComposerMessageKind,
  message: T,
): PendingComposerMessages<T> {
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
export function applyHeldComposerSnapshot<T extends PendingComposerEntry>(
  previous: PendingComposerMessages<T>,
  snapshot: T[],
): PendingComposerMessages<T> {
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

export function applyAuthoritativeComposerSnapshot<T extends PendingComposerEntry>(
  previous: PendingComposerMessages<T>,
  snapshot: T[],
): PendingComposerMessages<T> {
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

export function removePendingComposerMessage<T extends PendingComposerEntry>(
  previous: PendingComposerMessages<T>, id: string, scope: PendingComposerMessageRemovalScope = "all",
): PendingComposerMessages<T> {
  const queued = scope === "guide" ? previous.queued : previous.queued.filter(message => message.id !== id);
  const guides = scope === "queue" ? previous.guides : previous.guides.filter(message => message.id !== id);
  return queued.length === previous.queued.length && guides.length === previous.guides.length
    ? previous : { queued, guides };
}

export type PendingComposerUpdate<T extends PendingComposerEntry> = { threadID: string } & (
  | { kind: "held"; messages: T[] }
  | { kind: "upsert"; message: T }
  | { kind: "remove"; id: string; scope: PendingComposerMessageRemovalScope }
);

export function applyPendingComposerUpdate<T extends PendingComposerEntry>(
  previous: PendingComposerMessages<T>, update: PendingComposerUpdate<T>,
): PendingComposerMessages<T> {
  switch (update.kind) {
    case "held": return applyHeldComposerSnapshot(previous, update.messages);
    case "upsert": return appendPendingComposerMessage(
      removePendingComposerMessage(previous, update.message.id),
      update.message.origin === "steer" ? "guide" : "queue", update.message,
    );
    case "remove": return removePendingComposerMessage(previous, update.id, update.scope);
  }
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

/** Decode input lifecycle events once for both renderer state and delayed RPC
 * reconciliation. Turn IDs are not input IDs; only explicit queue/source IDs
 * consume accepted input. Presentation-specific content parsing is injected. */
export function pendingComposerUpdateFromNotification<T extends PendingComposerEntry>(
  notification: AppServerNotification,
  parseMessage: (value: unknown, position: number, held: boolean) => T | undefined,
): PendingComposerUpdate<T> | undefined {
  const params = notification.params;
  if (!isRecord(params)) return undefined;
  if (notification.method === "turn/queued" || notification.method === "turn/steered") {
    const raw = params.message;
    if (!isRecord(raw) || typeof raw.thread_id !== "string" || !raw.thread_id) return undefined;
    const message = parseMessage(raw, 0, false);
    return message ? { threadID: raw.thread_id, kind: "upsert", message } : undefined;
  }
  const threadID = params.thread_id;
  if (typeof threadID !== "string" || !threadID) return undefined;
  if (notification.method === "turn/held") {
    if (!Array.isArray(params.messages)) return undefined;
    const messages = params.messages.flatMap((raw, position) => {
      const message = parseMessage(raw, position, true);
      return message ? [message] : [];
    });
    return { threadID, kind: "held", messages };
  }
  let id: unknown;
  let scope: PendingComposerMessageRemovalScope = "all";
  switch (notification.method) {
    case "turn/unsteered": id = params.steer_id; scope = "guide"; break;
    case "turn/dequeued": id = params.queue_id; scope = "queue"; break;
    case "turn/started": id = params.queue_id; break;
    case "item/completed": {
      const item = params.item;
      if (!isRecord(item) || typeof item.id !== "string" || item.type !== "user_message") return undefined;
      id = item.source_id;
      break;
    }
    default: return undefined;
  }
  return typeof id === "string" && id ? { threadID, kind: "remove", id, scope } : undefined;
}

/**
 * Collect the ids of user messages that have already MATERIALIZED as
 * user_message turn items in a thread. A queued/guide composer message keeps
 * the same id (the server stamps the item's `source_id` with the queue/steer
 * client id — see startQueuedTurn / steer), so any pending entry whose id is
 * in this set has already been sent.
 */
export function materializedComposerMessageIDs(thread: unknown): Set<string> {
  const ids = new Set<string>();
  if (!isRecord(thread) || !Array.isArray(thread.turns)) return ids;
  for (const turn of thread.turns) {
    if (!isRecord(turn) || !Array.isArray(turn.items)) continue;
    for (const item of turn.items) {
      if (isRecord(item) && item.type === "user_message" && typeof item.source_id === "string" && item.source_id) ids.add(item.source_id);
    }
  }
  return ids;
}
