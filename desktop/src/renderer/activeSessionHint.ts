import {
  CODEX_PET_HINTS_MAX,
  type CodexPetHint,
  type Thread,
  type ThreadItem,
  type Turn,
} from "../shared/protocol";
import { translateCurrent } from "./i18n";

// The bubble's preview text is the latest stable agent_message. Thread.preview
// is not a fallback: it typically holds the first turn's user query, and
// showing that reads as stale commentary. Threads with no agent_message are
// omitted from the feed. Row status comes from structured state only — the
// latest user turn's status and pending blocking questions — never from
// wording, so an answer that mentions "error" or "权限" is not mislabeled.

// Walk a thread's turns from newest to oldest, and within each turn walk its
// items from newest to oldest, to find the latest visible assistant text.
//
// Why it walks turns first, items second: a thread may interleave commentary,
// tool calls, and the eventual final_answer across many turns, but the bubble
// is bound to the most recent in-conversation output regardless of which turn
// it landed in. The newest-first descent also matches the in-progress rule:
// while commentary streams the live item's `text` accumulates deltas in
// place, so the newest item is automatically the freshest text.
//
// We deliberately skip items with empty `text` so a not-yet-streamed
// placeholder (the protocol creates the row before the first delta arrives)
// falls through to the next-newest instead of appearing as a blank bubble.
export function latestAgentMessageText(thread: Thread): string | null {
  const turns = thread.turns;
  if (!turns?.length) return null;
  for (let tIndex = turns.length - 1; tIndex >= 0; tIndex -= 1) {
    const items = turns[tIndex]?.items;
    if (!items?.length) continue;
    for (let iIndex = items.length - 1; iIndex >= 0; iIndex -= 1) {
      const item: ThreadItem | undefined = items[iIndex];
      if (!item || item.type !== "agent_message") continue;
      const text = (item.text ?? "").trim();
      if (text) return text;
    }
  }
  return null;
}

export type ActiveSessionHintInput = {
  thread?: Thread | null;
  secondaryThread?: Thread | null;
  threads?: readonly Thread[] | null;
  // Threads whose latest completed turn the user hasn't been on for yet.
  // An idle thread in this set outranks a plain idle one so a finished-but-
  // unread conversation still surfaces in the bubble.
  unreadThreadIDs?: ReadonlySet<string> | null;
  // Threads with a pending blocking question or approval.
  waitingThreadIDs?: ReadonlySet<string> | null;
};

type ScoredThread = {
  thread: Thread;
  priority: number;
  hintStatus: CodexPetHint["status"];
  title: string;
  preview: string;
  attention: boolean;
  updatedAt: number;
};

function scoreThreads(input: ActiveSessionHintInput): ScoredThread[] {
  const all: Thread[] = [];
  if (input.thread) all.push(input.thread);
  if (input.secondaryThread) all.push(input.secondaryThread);
  for (const thread of input.threads ?? []) all.push(thread);

  const byID = new Map<string, Thread>();
  for (const thread of all) {
    if (thread && thread.id && !byID.has(thread.id)) byID.set(thread.id, thread);
  }

  const candidates = Array.from(byID.values()).filter((thread) => !thread.archived);
  if (candidates.length === 0) return [];

  const unreadIDs = input.unreadThreadIDs ?? null;
  const waitingIDs = input.waitingThreadIDs ?? null;

  const scored = candidates.map((thread) => {
    const title = (thread.title ?? "").trim();
    const running = thread.status === "in_progress";
    const waiting = waitingIDs?.has(thread.id) === true;
    const failedTurn = running ? undefined : latestUserTurn(thread);
    const failed = !waiting && failedTurn?.status === "failed";
    // A failed row explains the failure; otherwise the row shows the latest
    // commentary (see latestAgentMessageText for the walk).
    const preview = (
      (failed && failedTurn?.error?.message) ||
      latestAgentMessageText(thread) ||
      ""
    ).replace(/\s+/g, " ").trim();
    const unread = !running && !waiting && !failed && unreadIDs?.has(thread.id) === true;

    let priority: number;
    let hintStatus: CodexPetHint["status"];
    let attention = true;
    if (waiting) {
      priority = 5;
      hintStatus = "needs_review";
    } else if (failed) {
      priority = 4;
      hintStatus = "failed";
    } else if (unread) {
      priority = 3;
      hintStatus = "done";
    } else if (running) {
      priority = 2;
      hintStatus = "running";
      attention = false;
    } else {
      priority = 1;
      hintStatus = "idle";
      attention = false;
    }

    // The currently focused thread wins ties on equal priority so the bubble
    // follows the user as they switch between tabs.
    if (input.thread && thread.id === input.thread.id) priority += 0.5;

    const updatedAt = thread.updated_at ? Date.parse(thread.updated_at) || 0 : 0;
    return {
      thread,
      priority,
      hintStatus,
      title: title || translateCurrent("thread.conversation"),
      preview,
      attention,
      updatedAt,
    };
  });

  scored.sort((left, right) => {
    if (right.priority !== left.priority) return right.priority - left.priority;
    return right.updatedAt - left.updatedAt;
  });

  return scored;
}

// The newest turn the user started; internal and compaction turns are not
// the user's work.
function latestUserTurn(thread: Thread): Turn | undefined {
  const turns = thread.turns ?? [];
  for (let index = turns.length - 1; index >= 0; index -= 1) {
    const turn = turns[index];
    if (turn && (turn.kind ?? "user") === "user") return turn;
  }
  return undefined;
}

function hintFromScored(entry: ScoredThread): CodexPetHint {
  return {
    thread_id: entry.thread.id,
    title: entry.title,
    status: entry.hintStatus,
    preview: entry.preview,
    attention: entry.attention,
    updated_at: entry.updatedAt || Date.now(),
  };
}

// Top-ranked threads that have commentary, capped at CODEX_PET_HINTS_MAX.
// A row with nothing to say is omitted. An empty array hides the bubble.
export function deriveActiveSessionHints(
  input: ActiveSessionHintInput,
  limit: number = CODEX_PET_HINTS_MAX,
): CodexPetHint[] {
  return scoreThreads(input)
    .filter((entry) => entry.preview.length > 0)
    .slice(0, Math.max(0, limit))
    .map(hintFromScored);
}
