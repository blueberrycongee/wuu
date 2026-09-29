// Renderer state for the single side thread attached to each main thread.

import type {
  SideThreadEvent,
  SideThreadMessage,
  SideThreadSummary,
} from "../shared/protocol";

export type SideThreadEntryState = {
  // Closing the panel preserves history and draft for this main thread.
  open: boolean;
  // Null means no side thread has been persisted yet.
  summary: SideThreadSummary | null;
  messages: SideThreadMessage[];
  draft: string;
  streaming: boolean;
  lastError?: string;
};

export const SIDE_THREAD_DEFAULT_WIDTH = 400;
export const SIDE_THREAD_MIN_WIDTH = 320;
export const SIDE_THREAD_MAX_WIDTH = 640;

export type SideThreadStoreState = {
  byThread: Record<string, SideThreadEntryState>;
  width: number;
};

export function createEmptySideThreadEntry(): SideThreadEntryState {
  return {
    open: false,
    summary: null,
    messages: [],
    draft: "",
    streaming: false
  };
}

export function createInitialSideThreadStore(
  width: number = SIDE_THREAD_DEFAULT_WIDTH
): SideThreadStoreState {
  return {
    byThread: {},
    width: clampSideThreadWidth(width)
  };
}

// A failed reply already shows its error in the transcript. Only failures no
// reply carries (loading history, sending, resetting) are reported beside the
// composer.
export function sideThreadRequestError(
  entry: SideThreadEntryState
): string | undefined {
  const error = entry.lastError;
  const latestReply = entry.messages.findLast((message) => message.role === "assistant");
  return latestReply?.status === "failed" && latestReply.error_message === error
    ? undefined
    : error;
}

export function clampSideThreadWidth(value: number): number {
  if (!Number.isFinite(value)) {
    return SIDE_THREAD_DEFAULT_WIDTH;
  }
  if (value < SIDE_THREAD_MIN_WIDTH) {
    return SIDE_THREAD_MIN_WIDTH;
  }
  if (value > SIDE_THREAD_MAX_WIDTH) {
    return SIDE_THREAD_MAX_WIDTH;
  }
  return Math.round(value);
}

export function ensureSideThreadEntry(
  store: SideThreadStoreState,
  mainThreadId: string
): { store: SideThreadStoreState; entry: SideThreadEntryState } {
  const existing = store.byThread[mainThreadId];
  if (existing) {
    return { store, entry: existing };
  }
  const entry = createEmptySideThreadEntry();
  return {
    store: {
      ...store,
      byThread: { ...store.byThread, [mainThreadId]: entry }
    },
    entry
  };
}

export type SideThreadAction =
  | { type: "open"; mainThreadId: string }
  | { type: "close"; mainThreadId: string }
  | { type: "setDraft"; mainThreadId: string; draft: string }
  | { type: "mergeSummary"; mainThreadId: string; summary: SideThreadSummary }
  | { type: "appendMessage"; mainThreadId: string; message: SideThreadMessage }
  | {
      type: "updateMessage";
      mainThreadId: string;
      messageId: string;
      patch: Partial<SideThreadMessage>;
    }
  | { type: "removeMessage"; mainThreadId: string; messageId: string }
  | {
      type: "mergeHistory";
      mainThreadId: string;
      summary: SideThreadSummary;
      messages: SideThreadMessage[];
    }
  | { type: "setStreaming"; mainThreadId: string; streaming: boolean }
  | { type: "setError"; mainThreadId: string; error: string | undefined }
  | { type: "reset"; mainThreadId: string }
  | { type: "applyEvent"; event: SideThreadEvent }
  | { type: "setWidth"; width: number };

export function reduceSideThreadStore(
  store: SideThreadStoreState,
  action: SideThreadAction
): SideThreadStoreState {
  switch (action.type) {
    case "open":
      return updateEntry(store, action.mainThreadId, (entry) => ({
        ...entry,
        open: true
      }));
    case "close":
      return updateEntry(store, action.mainThreadId, (entry) => ({
        ...entry,
        open: false
      }));
    case "setDraft":
      return updateEntry(store, action.mainThreadId, (entry) => ({
        ...entry,
        draft: action.draft
      }));
    case "mergeSummary":
      return mergeSummarySnapshot(store, action.mainThreadId, action.summary);
    case "appendMessage":
      return updateEntry(store, action.mainThreadId, (entry) => ({
        ...entry,
        messages: [...entry.messages, action.message]
      }));
    case "updateMessage":
      return updateEntry(store, action.mainThreadId, (entry) => ({
        ...entry,
        messages: entry.messages.map((m) =>
          m.id === action.messageId ? { ...m, ...action.patch } : m
        )
      }));
    case "removeMessage":
      return updateEntry(store, action.mainThreadId, (entry) => ({
        ...entry,
        messages: entry.messages.filter((message) => message.id !== action.messageId)
      }));
    case "mergeHistory":
      return mergeHistorySnapshot(
        store,
        action.mainThreadId,
        action.summary,
        action.messages
      );
    case "setStreaming":
      return updateEntry(store, action.mainThreadId, (entry) => ({
        ...entry,
        streaming: action.streaming
      }));
    case "setError":
      return updateEntry(store, action.mainThreadId, (entry) => ({
        ...entry,
        lastError: action.error
      }));
    case "reset":
      return updateEntry(store, action.mainThreadId, resetEntry);
    case "applyEvent":
      return applySideThreadEvent(store, action.event);
    case "setWidth":
      return { ...store, width: clampSideThreadWidth(action.width) };
    default:
      return store;
  }
}

function updateEntry(
  store: SideThreadStoreState,
  mainThreadId: string,
  fn: (entry: SideThreadEntryState) => SideThreadEntryState
): SideThreadStoreState {
  const ensured = ensureSideThreadEntry(store, mainThreadId);
  const nextEntry = fn(ensured.entry);
  if (nextEntry === ensured.entry) {
    return ensured.store;
  }
  return {
    ...ensured.store,
    byThread: {
      ...ensured.store.byThread,
      [mainThreadId]: nextEntry
    }
  };
}

function mergeHistorySnapshot(
  store: SideThreadStoreState,
  mainThreadId: string,
  summary: SideThreadSummary,
  messages: SideThreadMessage[]
): SideThreadStoreState {
  return updateEntry(store, mainThreadId, (entry) => {
    const mergedSummary = newerSummary(entry.summary, summary);
    const mergedMessages = mergeMessages(messages, entry.messages);
    const streaming = mergedSummary.status === "running";
    if (
      mergedSummary === entry.summary &&
      sameMessageSequence(mergedMessages, entry.messages) &&
      streaming === entry.streaming
    ) {
      return entry;
    }
    return {
      ...entry,
      summary: mergedSummary,
      messages: mergedMessages,
      streaming
    };
  });
}

function mergeSummarySnapshot(
  store: SideThreadStoreState,
  mainThreadId: string,
  summary: SideThreadSummary
): SideThreadStoreState {
  return updateEntry(store, mainThreadId, (entry) => {
    const mergedSummary = newerSummary(entry.summary, summary);
    const streaming = mergedSummary.status === "running";
    if (mergedSummary === entry.summary && streaming === entry.streaming) {
      return entry;
    }
    return {
      ...entry,
      summary: mergedSummary,
      streaming
    };
  });
}

function newerSummary(
  current: SideThreadSummary | null,
  incoming: SideThreadSummary
): SideThreadSummary {
  if (!current) {
    return incoming;
  }
  if (current.revision !== incoming.revision) {
    return current.revision > incoming.revision ? current : incoming;
  }
  if (current.updated_at !== incoming.updated_at) {
    const currentUpdatedAt = Date.parse(current.updated_at);
    const incomingUpdatedAt = Date.parse(incoming.updated_at);
    if (Number.isFinite(currentUpdatedAt) && Number.isFinite(incomingUpdatedAt)) {
      return currentUpdatedAt > incomingUpdatedAt ? current : incoming;
    }
    return Number.isFinite(currentUpdatedAt) ? current : incoming;
  }
  return sameSideThreadSummary(current, incoming) ? current : incoming;
}

function sameSideThreadSummary(
  current: SideThreadSummary,
  incoming: SideThreadSummary
): boolean {
  return (
    current.side_thread_id === incoming.side_thread_id &&
    current.main_thread_id === incoming.main_thread_id &&
    current.status === incoming.status &&
    current.revision === incoming.revision &&
    current.created_at === incoming.created_at &&
    current.updated_at === incoming.updated_at &&
    current.main_task_summary?.running === incoming.main_task_summary?.running &&
    current.main_task_summary?.last_user_message ===
      incoming.main_task_summary?.last_user_message
  );
}

function sameMessageSequence(
  left: SideThreadMessage[],
  right: SideThreadMessage[]
): boolean {
  return left.length === right.length && left.every((message, index) => message === right[index]);
}

function mergeMessages(
  persisted: SideThreadMessage[],
  local: SideThreadMessage[]
): SideThreadMessage[] {
  const localById = new Map(local.map((message) => [message.id, message]));
  const merged = persisted.map((message) => {
    const localMessage = localById.get(message.id);
    if (!localMessage) {
      return message;
    }
    if (isTerminalMessage(message) && !isTerminalMessage(localMessage)) {
      return message;
    }
    return localMessage;
  });
  const persistedIds = new Set(persisted.map((message) => message.id));
  for (const message of local) {
    if (!persistedIds.has(message.id)) {
      merged.push(message);
    }
  }
  return merged;
}

// A reset removes the durable record, so the summary and history restart from
// scratch. Panel visibility and any unsent draft stay with the user.
function resetEntry(entry: SideThreadEntryState): SideThreadEntryState {
  return {
    ...entry,
    summary: null,
    messages: [],
    streaming: false,
    lastError: undefined
  };
}

function isTerminalMessage(message: SideThreadMessage): boolean {
  return (
    message.status === "completed" ||
    message.status === "failed" ||
    message.status === "interrupted"
  );
}

function applySideThreadEvent(
  store: SideThreadStoreState,
  event: SideThreadEvent
): SideThreadStoreState {
  const mainThreadId = event.main_thread_id;

  switch (event.type) {
    case "status": {
      return updateEntry(store, mainThreadId, (entry) => {
        const summary = newerSummary(entry.summary, event.summary);
        const terminalStatus: SideThreadMessage["status"] =
          summary.status === "running" ? undefined : summary.status;
        const messages =
          terminalStatus === undefined
            ? entry.messages
            : entry.messages.map((message) =>
                message.role === "assistant" && message.status === "streaming"
                  ? { ...message, status: terminalStatus }
                  : message
              );
        return {
          ...entry,
          summary,
          messages,
          streaming: summary.status === "running",
          lastError: summary.status === "failed" ? entry.lastError : undefined
        };
      });
    }
    case "delta": {
      const ensured = ensureSideThreadEntry(store, mainThreadId);
      return updateEntry(ensured.store, mainThreadId, (entry) => {
        if (
          entry.summary &&
          (event.revision < entry.summary.revision ||
            (event.revision === entry.summary.revision &&
              entry.summary.status !== "running"))
        ) {
          return entry;
        }
        const existingIndex = entry.messages.findIndex(
          (m) => m.id === event.message_id
        );
        if (existingIndex >= 0 && isTerminalMessage(entry.messages[existingIndex])) {
          return entry;
        }
        if (existingIndex < 0) {
          const fresh: SideThreadMessage = {
            id: event.message_id,
            side_thread_id: event.side_thread_id,
            role: "assistant",
            text: event.text_delta,
            status: "streaming",
            created_at: new Date().toISOString()
          };
          return {
            ...entry,
            streaming: true,
            messages: [...entry.messages, fresh]
          };
        }
        return {
          ...entry,
          streaming: true,
          messages: entry.messages.map((m, index) =>
            index === existingIndex
              ? {
                  ...m,
                  text: m.text + event.text_delta,
                  status: "streaming"
                }
              : m
          )
        };
      });
    }
    case "items": {
      const ensured = ensureSideThreadEntry(store, mainThreadId);
      return updateEntry(ensured.store, mainThreadId, (entry) => {
        if (
          entry.summary &&
          (event.revision < entry.summary.revision ||
            (event.revision === entry.summary.revision &&
              entry.summary.status !== "running"))
        ) {
          return entry;
        }
        const existingIndex = entry.messages.findIndex(
          (message) => message.id === event.message_id
        );
        if (existingIndex >= 0 && isTerminalMessage(entry.messages[existingIndex])) {
          return entry;
        }
        if (existingIndex < 0) {
          const fresh: SideThreadMessage = {
            id: event.message_id,
            side_thread_id: event.side_thread_id,
            role: "assistant",
            text: "",
            items: event.items,
            status: "streaming",
            created_at: new Date().toISOString()
          };
          return {
            ...entry,
            streaming: true,
            messages: [...entry.messages, fresh]
          };
        }
        return {
          ...entry,
          streaming: true,
          messages: entry.messages.map((message, index) =>
            index === existingIndex
              ? { ...message, items: event.items, status: "streaming" }
              : message
          )
        };
      });
    }
    case "message": {
      const ensured = ensureSideThreadEntry(store, mainThreadId);
      return updateEntry(ensured.store, mainThreadId, (entry) => {
        if (entry.summary && event.revision < entry.summary.revision) {
          return entry;
        }
        const existingIndex = entry.messages.findIndex(
          (m) => m.id === event.message.id
        );
        if (existingIndex < 0) {
          return {
            ...entry,
            streaming: false,
            messages: [...entry.messages, event.message]
          };
        }
        return {
          ...entry,
          streaming: false,
          messages: entry.messages.map((m, index) =>
            index === existingIndex ? event.message : m
          )
        };
      });
    }
    case "error": {
      return updateEntry(store, mainThreadId, (entry) => {
        if (entry.summary && event.revision < entry.summary.revision) {
          return entry;
        }
        return {
          ...entry,
          streaming: false,
          lastError: event.error_message,
          messages: entry.messages.map((m) =>
            m.id === event.message_id
              ? { ...m, status: "failed", error_message: event.error_message }
              : m
          )
        };
      });
    }
    case "reset":
      return updateEntry(store, mainThreadId, resetEntry);
    default:
      return store;
  }
}
