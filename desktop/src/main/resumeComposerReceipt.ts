import {
  applyAuthoritativeComposerSnapshot,
  applyPendingComposerUpdate,
  materializedComposerMessageIDs,
  removePendingComposerMessage,
  type PendingComposerUpdate,
} from "../shared/pendingComposerMessages";

export type WireComposerMessage = {
  id: string;
  origin: "queue" | "steer";
  held: boolean;
  position: number;
  raw: Record<string, unknown>;
};

export function wireComposerMessage(value: unknown, position: number, held: boolean): WireComposerMessage | undefined {
  if (!isRecord(value) || typeof value.id !== "string" || !value.id || (value.origin !== "queue" && value.origin !== "steer")) return undefined;
  return { id: value.id, origin: value.origin, held, position, raw: value };
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

// A delayed resume is not a newer authoritative fact than input lifecycle
// events already observed during that request. Reuse the renderer's reducer,
// but keep complete wire content and cross-origin order for both deliveries.
export function rebaseResumeComposerResult(result: unknown, updates: PendingComposerUpdate<WireComposerMessage>[]): unknown {
  if (!isRecord(result) || !isRecord(result.thread) || typeof result.thread.id !== "string") return result;
  const threadID = result.thread.id;
  const relevant = updates.filter(update => update.threadID === threadID);
  if (relevant.length === 0) return result;
  let position = 0;
  const parse = (raw: unknown, held: boolean) => wireComposerMessage(raw, position++, held);
  const initial = [
    ...(Array.isArray(result.held_user_messages) ? result.held_user_messages : []).flatMap(raw => {
      const message = parse(raw, true);
      return message ? [message] : [];
    }),
    ...(Array.isArray(result.pending_user_messages) ? result.pending_user_messages : []).flatMap(raw => {
      const message = parse(raw, false);
      return message ? [message] : [];
    }),
  ];
  let state = applyAuthoritativeComposerSnapshot<WireComposerMessage>({ queued: [], guides: [] }, initial);
  for (const update of relevant) {
    // Snapshot positions are local to an event. Assign receipt-order positions
    // so splitting into queue/guide buckets never reorders held recovery input.
    const ordered = update.kind === "held"
      ? { ...update, messages: update.messages.map(message => ({ ...message, position: position++ })) }
      : update.kind === "upsert"
        ? { ...update, message: { ...update.message, position: position++ } }
        : update;
    state = applyPendingComposerUpdate(state, ordered);
  }
  // A fresh history may already contain an input whose earlier queued event
  // was recorded during this same read. Never put that input back in composer.
  for (const id of materializedComposerMessageIDs(result.thread)) state = removePendingComposerMessage(state, id);
  const messages = [...state.queued, ...state.guides].sort((a, b) => a.position - b.position);
  return {
    ...result,
    held_user_messages: messages.filter(message => message.held).map(message => message.raw),
    pending_user_messages: messages.filter(message => !message.held).map(message => message.raw),
  };
}
