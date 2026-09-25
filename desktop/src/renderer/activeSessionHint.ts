import {
  CODEX_PET_HINTS_MAX,
  type CodexPetHint,
  type Thread,
  type ThreadItem,
} from "../shared/protocol";
import { translateCurrent } from "./i18n";

// The bubble's preview text is the latest stable agent_message. Thread.preview
// is not a fallback: it typically holds the first turn's user query, and
// showing that reads as stale commentary. Threads with no agent_message are
// omitted from the feed.
// REVIEW_RE matches both Chinese affordance words and English ones, but only
// the English alternatives get \b word boundaries — \b requires a word/non-word
// transition, and Chinese characters are word characters, so \b between two
// CJK runes never matches and would silently suppress the chip on
// Chinese-language turns.
const FAILED_RE = /\b(failed|error)\b|失败|错误/i;
const REVIEW_RE = /权限|批准|确认|\b(permission|approve|review)\b/i;

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

  const scored = candidates.map((thread) => {
    const title = (thread.title ?? "").trim();
    // Preview is the latest stable agent_message (see latestAgentMessageText
    // for the walk). Empty string when no commentary exists anywhere — the
    // Thread.preview denormalized field is intentionally NOT used as a
    // fallback, because it typically holds the first turn's user query and
    // showing that here reads as "very early text" instead of "latest
    // commentary". When preview is empty the pet window hides its bubble
    // card entirely rather than render a stale summary.
    const preview = (latestAgentMessageText(thread) ?? "").trim();
    const failed = FAILED_RE.test(preview) || FAILED_RE.test(title);
    const running = thread.status === "in_progress";
    // A running thread can briefly match REVIEW_RE through its in-progress
    // commentary, but the "needs review" chip is a user-action affordance —
    // only flag it once the thread has actually settled.
    const needsReview = !failed && !running && REVIEW_RE.test(preview);
    const unread =
      !failed && !needsReview && !running && unreadIDs?.has(thread.id) === true;

    let priority: number;
    let hintStatus: CodexPetHint["status"];
    let attention = false;
    if (failed) {
      priority = 5;
      hintStatus = "failed";
      attention = true;
    } else if (needsReview) {
      priority = 4;
      hintStatus = "needs_review";
      attention = true;
    } else if (running) {
      priority = 3;
      hintStatus = "running";
    } else if (unread) {
      priority = 2;
      hintStatus = "done";
    } else {
      priority = 1;
      hintStatus = "idle";
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
