// Conversation-list logic: visible ordinary threads, ordering, and the
// purely client-local unread state.

import type { Thread } from "@wuu/protocol";

export function isVisibleThread(thread: Thread): boolean {
  return !thread.archived && !thread.read_only;
}

export function isThreadRunning(t: Thread): boolean {
  if (t.status === "in_progress") return true;
  return (t.turns ?? []).some((turn) => turn.status === "in_progress");
}

export function threadDisplayTitle(t: Thread): string {
  return t.title?.trim() || t.preview?.trim() || "未命名对话";
}

/** Two-band ordering (mirrors AppState.sortThreads): running threads first
 *  by created_at desc (stable while streaming), finished by updated_at desc. */
export function sortThreads(threads: Thread[]): Thread[] {
  const running = threads.filter(isThreadRunning);
  const finished = threads.filter((t) => !isThreadRunning(t));
  const stamp = (value?: string): number => {
    const ms = value ? Date.parse(value) : NaN;
    return Number.isNaN(ms) ? 0 : ms;
  };
  running.sort((a, b) => stamp(b.created_at) - stamp(a.created_at));
  finished.sort(
    (a, b) => stamp(b.updated_at ?? b.created_at) - stamp(a.updated_at ?? a.created_at),
  );
  return [...running, ...finished];
}

/** The turn id unread detection keys on: the newest COMPLETED turn (a turn
 *  that is still streaming never marks a thread unread). */
export function latestCompletedTurnID(t: Thread): string | null {
  const turns = t.turns ?? [];
  for (let i = turns.length - 1; i >= 0; i--) {
    if (turns[i].status !== "in_progress") return turns[i].id;
  }
  return null;
}

/** Unread is purely client-local: the newest completed turn differs from
 *  what the user last viewed, and the thread is not mid-run. */
export function isThreadUnread(t: Thread, lastViewed: Readonly<Record<string, string>>): boolean {
  if (isThreadRunning(t)) return false;
  const latest = latestCompletedTurnID(t);
  if (!latest) return false;
  return lastViewed[t.id] !== latest;
}
